package gateway

// Inline stdio<->SSE bridge for MCP. Ported from src/lib/mcp/stdioSseBridge.js
// and src/app/api/mcp/[plugin]/{sse,message}/route.js.
//
// One child process per plugin, spawned lazily on the first SSE connect and
// killed when its last session drops. JSON-RPC frames from the child's stdout
// broadcast to every session; client POSTs are written to the child's stdin.
//
// Security: only the fixed coworkLocalStdioPlugins preset may be spawned —
// there is no user-supplied command, so this cannot become an RCE surface.

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
)

const (
	mcpMaxTextChars       = 50000
	mcpCollapseThreshold  = 30
	mcpCollapseKeepHead   = 10
	mcpCollapseKeepTail   = 5
	mcpBridgeStderrPrefix = "[mcp:"
)

type mcpSession struct {
	send func(data string) error
}

type mcpBridge struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	mu       sync.Mutex
	sessions map[string]*mcpSession
}

var (
	mcpBridgesMu sync.Mutex
	mcpBridges   = map[string]*mcpBridge{}
)

// mcpPlugin finds a preset stdio plugin by name.
func mcpPlugin(name string) (*coworkPlugin, bool) {
	for i := range coworkLocalStdioPlugins {
		if coworkLocalStdioPlugins[i].Name == name {
			return &coworkLocalStdioPlugins[i], true
		}
	}
	return nil, false
}

func (g *Gateway) registerMCPRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/mcp/{plugin}/sse", g.requireAuth(g.handleMCPSse))
	mux.HandleFunc("POST /api/mcp/{plugin}/message", g.requireAuth(g.handleMCPMessage))
}

// getOrSpawn returns the running bridge, spawning the plugin's child on demand.
func (g *Gateway) getOrSpawn(name string) (*mcpBridge, error) {
	mcpBridgesMu.Lock()
	defer mcpBridgesMu.Unlock()
	if b, ok := mcpBridges[name]; ok {
		return b, nil
	}
	p, ok := mcpPlugin(name)
	if !ok {
		return nil, fmt.Errorf("Unknown local plugin: %s", name)
	}
	cmd := exec.Command(p.Command, p.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	b := &mcpBridge{cmd: cmd, stdin: stdin, sessions: map[string]*mcpSession{}}
	mcpBridges[name] = b

	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			log.Printf("%s%s] %s", mcpBridgeStderrPrefix, name, sc.Text())
		}
	}()
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64<<10), 8<<20)
		for sc.Scan() {
			line := mcpFilterFrame(strings.TrimSpace(sc.Text()))
			if line == "" {
				continue
			}
			b.mu.Lock()
			for _, s := range b.sessions {
				_ = s.send("event: message\ndata: " + line + "\n\n")
			}
			b.mu.Unlock()
		}
		_ = cmd.Wait()
		mcpBridgesMu.Lock()
		if mcpBridges[name] == b {
			delete(mcpBridges, name)
		}
		mcpBridgesMu.Unlock()
		log.Printf("%smcp:%s] exited", mcpBridgeStderrPrefix, name)
	}()
	return b, nil
}

func (g *Gateway) handleMCPSse(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("plugin")
	if _, ok := mcpPlugin(name); !ok {
		http.Error(w, "Unknown plugin: "+name, http.StatusNotFound)
		return
	}
	b, err := g.getOrSpawn(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sid := mcpRandomID()
	sess := &mcpSession{send: func(data string) error {
		if _, err := io.WriteString(w, data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}}
	b.mu.Lock()
	b.sessions[sid] = sess
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.sessions, sid)
		remaining := len(b.sessions)
		b.mu.Unlock()
		if remaining == 0 {
			// Last session gone: kill the child to avoid an idle orphan.
			_ = b.cmd.Process.Kill()
			mcpBridgesMu.Lock()
			if mcpBridges[name] == b {
				delete(mcpBridges, name)
			}
			mcpBridgesMu.Unlock()
		}
	}()

	_ = sess.send("event: endpoint\ndata: /api/mcp/" + name + "/message?sessionId=" + sid + "\n\n")
	<-r.Context().Done()
}

func (g *Gateway) handleMCPMessage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("plugin")
	if _, ok := mcpPlugin(name); !ok {
		writeErr(w, http.StatusNotFound, "Unknown plugin: "+name)
		return
	}
	var body any
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusInternalServerError, "invalid JSON")
		return
	}
	mcpBridgesMu.Lock()
	b := mcpBridges[name]
	mcpBridgesMu.Unlock()
	if b == nil {
		writeErr(w, http.StatusInternalServerError, "Bridge not running: "+name)
		return
	}
	line, _ := json.Marshal(body)
	if _, err := b.stdin.Write(append(line, '\n')); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func mcpRandomID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf[:4]) + "-" + hex.EncodeToString(buf[4:6]) + "-" +
		hex.EncodeToString(buf[6:8]) + "-" + hex.EncodeToString(buf[8:10]) + "-" + hex.EncodeToString(buf[10:])
}

// mcpFilterFrame applies the bridge's text compression to tool/result content
// blocks only; frames that change nothing are returned untouched.
func mcpFilterFrame(line string) string {
	var msg map[string]any
	if json.Unmarshal([]byte(line), &msg) != nil {
		return line
	}
	result, _ := msg["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return line
	}
	mutated := false
	for _, item := range content {
		block, _ := item.(map[string]any)
		if strAny(block["type"]) != "text" {
			continue
		}
		text, ok := block["text"].(string)
		if !ok {
			continue
		}
		if filtered := mcpSmartFilterText(text); filtered != text {
			block["text"] = filtered
			mutated = true
		}
	}
	if !mutated {
		return line
	}
	out, _ := json.Marshal(msg)
	return string(out)
}

func mcpSmartFilterText(text string) string {
	if len(text) < 2000 {
		return text
	}
	// Drop noise lines then collapse repeated sibling roles.
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "- generic:" || t == `- text: ""` {
			continue
		}
		kept = append(kept, ln)
	}
	out := strings.Join(mcpCollapseRepeated(kept), "\n")
	if len(out) > mcpMaxTextChars {
		head := out[:mcpMaxTextChars-300]
		out = head + fmt.Sprintf("\n\n... [truncated %d chars by kinerouter bridge. Page is large; ask user to scroll/navigate to a specific section, or click an element with the refs shown above]",
			len(text)-len(head))
	}
	return out
}

// mcpSibling matches "<indent>- <role>", the tree shape the bridge collapses.
func mcpSibling(line string) (indent, role string, ok bool) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	rest := line[i:]
	if !strings.HasPrefix(rest, "- ") {
		return "", "", false
	}
	rest = rest[2:]
	j := 0
	for j < len(rest) && (rest[j] >= 'a' && rest[j] <= 'z' || rest[j] >= 'A' && rest[j] <= 'Z') {
		j++
	}
	if j == 0 {
		return "", "", false
	}
	if j < len(rest) && !(rest[j] == ' ' || rest[j] == ':' || rest[j] == '\t') {
		return "", "", false
	}
	return line[:i], rest[:j], true
}

// mcpCollapseRepeated drops the repetitive middle of a run of >=30 identical
// siblings, keeping the first 10 and last 5, matching collapseRepeated.
func mcpCollapseRepeated(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		indent, role, ok := mcpSibling(lines[i])
		if !ok {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < len(lines) {
			if in, r, ok := mcpSibling(lines[j]); ok && in == indent && r == role {
				j++
				continue
			}
			if strings.HasPrefix(lines[j], indent+" ") || strings.HasPrefix(lines[j], indent+"\t") {
				j++
				continue
			}
			break
		}
		if n := j - i; n >= mcpCollapseThreshold {
			headEnd := j
			count := 0
			for k := i; k < j; k++ {
				if in, r, ok := mcpSibling(lines[k]); ok && in == indent && r == role {
					count++
					if count > mcpCollapseKeepHead {
						headEnd = k
						break
					}
				}
			}
			positions := []int{}
			for k := i; k < j; k++ {
				if in, r, ok := mcpSibling(lines[k]); ok && in == indent && r == role {
					positions = append(positions, k)
				}
			}
			tailStart := j
			if len(positions) > mcpCollapseKeepTail {
				tailStart = positions[len(positions)-mcpCollapseKeepTail]
			}
			out = append(out, lines[i:headEnd]...)
			out = append(out, fmt.Sprintf("%s... [%d similar %q items omitted by kinerouter bridge]",
				indent, n-mcpCollapseKeepHead-mcpCollapseKeepTail, role))
			if tailStart < j {
				out = append(out, lines[tailStart:j]...)
			}
		} else {
			out = append(out, lines[i:j]...)
		}
		i = j
	}
	return out
}
