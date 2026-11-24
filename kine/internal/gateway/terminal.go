package gateway

// Web terminal: runs one shell command per request and returns its output.
// Stateless by design — the browser keeps an output log and a working
// directory, the server keeps nothing.
//
// This is remote code execution by intent: it runs as the gateway's own OS
// user. Access is therefore the dashboard session, and a non-loopback peer is
// refused unless the operator sets terminalAllowRemote. ponytail: one command
// per POST, no PTY, so interactive full-screen programs (vim, top) are not
// supported; add a pty + websocket bridge when someone needs one.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	terminalTimeout   = 60 * time.Second
	terminalMaxOutput = 1 << 20
)

func (g *Gateway) registerTerminalRoutes(mux *http.ServeMux) {
	r := g.requireAuth
	mux.HandleFunc("GET /api/terminal", r(g.handleTerminalInfo))
	mux.HandleFunc("POST /api/terminal/exec", r(g.handleTerminalExec))
}

// terminalHome is the starting directory; a missing home falls back to /.
func terminalHome() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return "/"
}

// terminalAllowed refuses a shell for a remote peer unless explicitly enabled.
func (g *Gateway) terminalAllowed(r *http.Request) bool {
	if isLoopback(clientHost(r)) {
		return true
	}
	return g.store.SettingBool("terminalAllowRemote", false)
}

func (g *Gateway) handleTerminalInfo(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cwd":     terminalHome(),
		"user":    firstNonEmpty(os.Getenv("USER"), os.Getenv("LOGNAME"), "user"),
		"host":    host,
		"shell":   shell,
		"remote":  !isLoopback(clientHost(r)),
		"allowed": g.terminalAllowed(r),
	})
}

func (g *Gateway) handleTerminalExec(w http.ResponseWriter, r *http.Request) {
	if !g.terminalAllowed(r) {
		writeErr(w, http.StatusForbidden, "the web terminal is limited to local requests; set terminalAllowRemote to allow remote operators")
		return
	}
	var req struct {
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		writeErr(w, http.StatusBadRequest, "command is required")
		return
	}
	cwd := terminalCwd(req.Cwd)

	// cd is handled here so the browser can navigate directories without a
	// persistent shell process; every later command carries the new cwd.
	if rest, ok := terminalCd(command); ok {
		target := expandHome(rest)
		if !filepath.IsAbs(target) {
			target = filepath.Join(cwd, target)
		}
		next := filepath.Clean(target)
		if info, err := os.Stat(next); err != nil || !info.IsDir() {
			writeJSON(w, http.StatusOK, map[string]any{
				"output": "cd: no such directory: " + rest, "exitCode": 1, "cwd": cwd,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"output": "", "exitCode": 0, "cwd": next})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), terminalTimeout)
	defer cancel()
	cmd := terminalShell(ctx, command)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TERM=dumb", "CLICOLOR=0", "NO_COLOR=1")
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Start(); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"output": err.Error(), "exitCode": 1, "cwd": cwd,
		})
		return
	}
	// A wedged pipeline must not outlive the request: kill the whole group.
	kill := time.AfterFunc(terminalTimeout, func() { killTerminal(cmd) })
	defer kill.Stop()
	err := cmd.Wait()
	out := buf.Bytes()
	if len(out) > terminalMaxOutput {
		out = append(out[:terminalMaxOutput], []byte("\n[output truncated]\n")...)
	}
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.As(err, &exitErr):
			exitCode = exitErr.ExitCode()
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			exitCode = 124
			out = append(out, []byte("\n[command timed out after 60s]\n")...)
		default:
			exitCode = 1
			out = append(out, []byte("\n"+err.Error()+"\n")...)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"output": string(out), "exitCode": exitCode, "cwd": cwd,
	})
}

// terminalCwd keeps the working directory inside a real directory.
func terminalCwd(raw string) string {
	if raw == "" {
		return terminalHome()
	}
	raw = expandHome(raw)
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(terminalHome(), raw)
	}
	clean := filepath.Clean(raw)
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return terminalHome()
	}
	return clean
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return filepath.Join(terminalHome(), strings.TrimPrefix(p, "~"))
	}
	return p
}

// terminalCd parses "cd", "cd dir" and "cd dir && something" (the latter is
// left to the shell because it is not a pure navigation).
func terminalCd(command string) (string, bool) {
	if !(command == "cd" || strings.HasPrefix(command, "cd ")) {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(command, "cd"))
	if strings.ContainsAny(rest, "&|;") {
		return "", false
	}
	rest = strings.Trim(rest, `"'`)
	return rest, true
}
