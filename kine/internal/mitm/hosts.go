package mitm

// /etc/hosts maintenance. Reads go through os.ReadFile (world-readable);
// writes go through sudo (tee) because the file is root-owned.

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
)

func hostsFile() string { return "/etc/hosts" }

// DNSStatus reports, per tool, whether every one of its hosts is redirected.
func DNSStatus() map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(hostsFile())
	if err != nil {
		for tool := range ToolHosts {
			out[tool] = false
		}
		return out
	}
	content := string(data)
	for tool, hosts := range ToolHosts {
		all := true
		for _, h := range hosts {
			if !contentHasHost(content, h) {
				all = false
				break
			}
		}
		out[tool] = all
	}
	return out
}

func contentHasHost(content, host string) bool {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if field == host {
				return true
			}
		}
	}
	return false
}

// AddDNS redirects the tool's hosts to 127.0.0.1, appending only what is missing.
func AddDNS(tool, password string) error {
	hosts, ok := ToolHosts[tool]
	if !ok {
		return fmt.Errorf("unknown tool: %s", tool)
	}
	current, err := os.ReadFile(hostsFile())
	if err != nil {
		return err
	}
	content := string(current)
	var missing []string
	for _, h := range hosts {
		if !contentHasHost(content, h) {
			missing = append(missing, h)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	next := strings.TrimRight(content, "\n \t\r") + "\n"
	for _, h := range missing {
		next += "127.0.0.1 " + h + "\n"
	}
	if err := writeHosts(next, password); err != nil {
		return err
	}
	flushDNS(password)
	return nil
}

// RemoveDNS drops every line that lists one of the tool's hosts.
func RemoveDNS(tool, password string) error {
	hosts, ok := ToolHosts[tool]
	if !ok {
		return fmt.Errorf("unknown tool: %s", tool)
	}
	current, err := os.ReadFile(hostsFile())
	if err != nil {
		return err
	}
	next, changed := stripHosts(string(current), hosts)
	if !changed {
		return nil
	}
	if err := writeHosts(next, password); err != nil {
		return err
	}
	flushDNS(password)
	return nil
}

// RemoveAllDNS drops every tool host in one write — used on server shutdown.
func RemoveAllDNS(password string) {
	current, err := os.ReadFile(hostsFile())
	if err != nil {
		return
	}
	var all []string
	for _, hosts := range ToolHosts {
		all = append(all, hosts...)
	}
	next, changed := stripHosts(string(current), all)
	if !changed {
		return
	}
	if err := writeHosts(next, password); err != nil {
		return
	}
	flushDNS(password)
}

// stripHosts removes lines mentioning any of hosts. Returns the new content
// and whether anything changed.
func stripHosts(content string, hosts []string) (string, bool) {
	set := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		set[h] = true
	}
	changed := false
	var kept []string
	for _, line := range strings.Split(content, "\n") {
		hit := false
		for _, field := range strings.Fields(strings.SplitN(line, "#", 2)[0]) {
			if set[field] {
				hit = true
				break
			}
		}
		if hit {
			changed = true
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimRight(strings.Join(kept, "\n"), "\n \t\r") + "\n", changed
}

// writeHosts overwrites /etc/hosts through sudo tee, preserving permissions.
func writeHosts(content, password string) error {
	escaped := strings.ReplaceAll(content, "'", `'\''`)
	_, err := ExecPrivileged("printf '%s' '"+escaped+`' | tee /etc/hosts > /dev/null`, password)
	if err != nil {
		return fmt.Errorf("Failed to write hosts: %v", err)
	}
	return nil
}

func flushDNS(password string) {
	if runtime.GOOS == "darwin" {
		ExecPrivileged("dscacheutil -flushcache; killall -HUP mDNSResponder", password)
		return
	}
	ExecPrivileged("resolvectl flush-caches 2>/dev/null || true", password)
}

// sortedTools returns tool ids in a stable order (for status output).
func sortedTools() []string {
	out := make([]string, 0, len(ToolHosts))
	for t := range ToolHosts {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
