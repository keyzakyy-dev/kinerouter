package main

// HTTP + JSON plumbing for the Terminal UI. Every call goes to the gateway on
// loopback with the same CLI token the dashboard pages use, so the TUI needs no
// password even when login is required.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/auth"
)

type tui struct {
	base  string
	port  int
	token string
	hc    *http.Client
	in    *bufio.Reader
}

func newTUI(base string, port int, dataDir string) *tui {
	token := ""
	if dataDir != "" {
		token = auth.CLIToken(auth.LoadMachineID(dataDir, auth.CLIAuthSalt))
	}
	return &tui{base: base, port: port, token: token, hc: &http.Client{Timeout: 150 * time.Second}}
}

// do sends one API call and decodes the JSON object body. Non-2xx responses are
// returned as an error carrying the server's message.
func (t *tui) do(method, path string, body any) (map[string]any, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, t.base+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if t.token != "" {
		req.Header.Set("x-kine-cli-token", t.token)
	}
	resp, err := t.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	out := map[string]any{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			out = map[string]any{}
		}
	}
	if resp.StatusCode >= 400 {
		return out, fmt.Errorf("%s", apiError(out, resp.StatusCode))
	}
	return out, nil
}

func apiError(m map[string]any, code int) string {
	switch v := m["error"].(type) {
	case string:
		return v
	case map[string]any:
		if s := asStr(v["message"]); s != "" {
			return s
		}
	}
	if s := asStr(m["message"]); s != "" {
		return s
	}
	return "HTTP " + strconv.Itoa(code)
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func asStr(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(s)
	}
	return ""
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

// pick reads a dotted path out of a decoded JSON object.
func pick(v any, path ...string) any {
	cur := v
	for _, key := range path {
		m := asMap(cur)
		if m == nil {
			return nil
		}
		cur = m[key]
	}
	return cur
}

// maskKey mirrors 9Router's format.js maskKey.
func tuiMaskKey(key string) string {
	if len(key) < 8 {
		return "***"
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}
