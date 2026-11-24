package executor

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildFakeAGY writes a tiny ACP-speaking Go binary used as AGY_PATH.
func buildFakeAGY(t *testing.T, reply string) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.go")
	src := `package main
import ("bufio";"encoding/json";"fmt";"os";"strings")
func main(){
 r:=bufio.NewReader(os.Stdin); w:=bufio.NewWriter(os.Stdout); defer w.Flush()
 reply:=` + mustJSONString(reply) + `
 for { line,err:=r.ReadString('\n'); if err!=nil{return}
  var m map[string]any; if json.Unmarshal([]byte(line),&m)!=nil{continue}
  method,_:=m["method"].(string); id:=m["id"]
  switch method {
  case "initialize":
   fmt.Fprintf(w,"{\"jsonrpc\":\"2.0\",\"id\":%v,\"result\":{\"protocolVersion\":1}}\n",id)
  case "session/new":
   fmt.Fprintf(w,"{\"jsonrpc\":\"2.0\",\"id\":%v,\"result\":{\"sessionId\":\"s-1\"}}\n",id)
  case "session/prompt":
   parts:=strings.SplitN(reply,"|",2)
   b,_:=json.Marshal(parts[0])
   fmt.Fprintf(w,"{\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"sessionId\":\"s-1\",\"update\":{\"sessionUpdate\":\"agent_message_chunk\",\"content\":{\"type\":\"text\",\"text\":%s}}}}\n",b)
   w.Flush()
   final:=""
   if len(parts)>1 { final=parts[1] }
   b2,_:=json.Marshal(parts[0]+final)
   fmt.Fprintf(w,"{\"jsonrpc\":\"2.0\",\"id\":%v,\"result\":{\"stopReason\":\"end_turn\",\"content\":[{\"type\":\"text\",\"text\":%s}]}}\n",id,b2)
  }
  w.Flush()
 }
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "fakeagy")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("build fake agy: %v: %s", err, out)
	}
	return bin
}

func mustJSONString(s string) string { b, _ := json.Marshal(s); return string(b) }

func agyReq(t *testing.T, stream bool) Request {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"model":    "gemini-3.8-flash-medium",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	return Request{
		Provider:     "agy-acp",
		Model:        "gemini-3.8-flash-medium",
		Body:         body,
		Stream:       stream,
		ConnectionID: "conn-test",
		Creds:        Credentials{AccessToken: "access", RefreshToken: "refresh", Extra: map[string]any{"agyPath": os.Getenv("AGY_PATH")}},
	}
}

func TestAGYACPNonStream(t *testing.T) {
	bin := buildFakeAGY(t, "PONG")
	t.Setenv("AGY_PATH", bin)
	t.Setenv("AGY_ACP_DATA_DIR", t.TempDir())
	resp, err := New().Execute(context.Background(), agyReq(t, false))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Status != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.Status, b)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Choices) != 1 || out.Choices[0].Message.Content != "PONGPONG" || out.Choices[0].FinishReason != "stop" {
		t.Fatalf("unexpected completion: %+v", out)
	}
}

func TestAGYACPStream(t *testing.T) {
	bin := buildFakeAGY(t, "Hello |world")
	t.Setenv("AGY_PATH", bin)
	t.Setenv("AGY_ACP_DATA_DIR", t.TempDir())
	resp, err := New().Execute(context.Background(), agyReq(t, true))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	s := string(raw)
	for _, want := range []string{`"role":"assistant"`, `"content":"Hello "`, "data: [DONE]"} {
		if !strings.Contains(s, want) {
			t.Fatalf("stream missing %q:\n%s", want, s)
		}
	}
}

func TestAGYACPBannerIsAuthError(t *testing.T) {
	bin := buildFakeAGY(t, "⚠ needs sign-in to continue")
	t.Setenv("AGY_PATH", bin)
	t.Setenv("AGY_ACP_DATA_DIR", t.TempDir())
	resp, err := New().Execute(context.Background(), agyReq(t, false))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Status != 401 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 401: %s", resp.Status, b)
	}
}
