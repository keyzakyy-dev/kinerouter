package gateway

import (
	"net/http"
	"strings"
	"testing"
)

func TestSaverCfgGates(t *testing.T) {
	settings := map[string]any{
		"rtkEnabled": true, "cavemanEnabled": true, "cavemanLevel": "lite",
		"ponytailEnabled": false, "customPromptEnabled": true, "customPrompt": "be brief",
	}
	cfg := saverCfgFrom(nil, settings, http.Header{}, "m", "openai")
	if !cfg.compress || cfg.caveman == "" || cfg.ponytail != "" || cfg.custom != "be brief" {
		t.Fatalf("unexpected cfg %+v", cfg)
	}
	off := http.Header{}
	off.Set(tokenSaverHeader, "off")
	cfg = saverCfgFrom(nil, settings, off, "m", "openai")
	if cfg.compress || cfg.caveman != "" {
		t.Fatal("token-saver: off must disable RTK and style savers")
	}
	if cfg.custom != "be brief" {
		t.Fatal("custom prompt is independent of the saver header")
	}
}

func TestSaverApplyOrder(t *testing.T) {
	settings := map[string]any{
		"rtkEnabled": true, "cavemanEnabled": true, "cavemanLevel": "lite",
		"ponytailEnabled": true, "ponytailLevel": "lite",
		"customPromptEnabled": true, "customPrompt": "CUSTOM",
	}
	body := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": "hi"},
	}}
	cfg := saverCfgFrom(nil, settings, http.Header{}, "m", "openai")
	cfg.apply(body, "openai")
	sys := body["messages"].([]any)[0].(map[string]any)
	if sys["role"] != "system" {
		t.Fatalf("a system message should have been prepended: %+v", body)
	}
	content := sys["content"].(string)
	if !strings.HasSuffix(content, "\n\nCUSTOM") {
		t.Fatalf("custom prompt must be appended last: %q", content)
	}
	if strings.Count(content, "\n\n") != 2 {
		t.Fatalf("want caveman + ponytail + custom (3 segments), got %d", strings.Count(content, "\n\n")+1)
	}
}
