package mitm

// Per-tool upstream hosts. Mirrors src/shared/constants/mitmToolHosts.js;
// each host is written to /etc/hosts as "127.0.0.1 <host>" when its tool's DNS
// is enabled.

import "strings"

var ToolHosts = map[string][]string{
	"antigravity": {"daily-cloudcode-pa.googleapis.com", "cloudcode-pa.googleapis.com"},
	"copilot":     {"api.individual.githubcopilot.com"},
	"kiro":        {"runtime.us-east-1.kiro.dev", "q.us-east-1.amazonaws.com", "codewhisperer.us-east-1.amazonaws.com"},
	"cursor":      {"api2.cursor.sh"},
}

var hostToTool = map[string]string{
	"daily-cloudcode-pa.googleapis.com":     "antigravity",
	"cloudcode-pa.googleapis.com":           "antigravity",
	"api.individual.githubcopilot.com":      "copilot",
	"q.us-east-1.amazonaws.com":             "kiro",
	"codewhisperer.us-east-1.amazonaws.com": "kiro",
	"runtime.us-east-1.kiro.dev":            "kiro",
	"api2.cursor.sh":                        "cursor",
}

// ToolForHost maps Host (with optional :port) to its tool id, "" if unknown.
func ToolForHost(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return hostToTool[host]
}
