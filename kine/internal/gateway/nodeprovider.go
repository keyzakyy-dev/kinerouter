package gateway

// Custom provider nodes at request time. A connection whose provider is a
// providerNodes id (openai-compatible-*, anthropic-compatible-*,
// custom-embedding-*) is served from the node's baseUrl, mirroring
// open-sse/services/provider.js + executors/base.js + embeddingProviders/
// openaiCompatNode.js.

import (
	"strings"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

// nodeByPrefix matches "prefix/model" against the stored nodes' user prefixes,
// or the node id itself (custom models and model tests store the id as the
// provider alias). A prefix that collides with a registry provider id/alias
// never wins, matching RESERVED_PROVIDER_PREFIXES in src/sse/services/model.js.
func (g *Gateway) nodeByPrefix(prefix string) *store.ProviderNode {
	if prefix == "" || g.reg.IsKnownProvider(g.reg.ResolveAlias(prefix)) {
		return nil
	}
	nodes, err := g.store.ListProviderNodes()
	if err != nil {
		return nil
	}
	for _, n := range nodes {
		if n.ID == prefix {
			return n
		}
	}
	for _, n := range nodes {
		if p, _ := n.Data["prefix"].(string); p == prefix {
			return n
		}
	}
	return nil
}

// nodeAPIType is the stored apiType, else the id substring for legacy nodes
// whose id embeds it (resolveOpenAICompatibleApiType).
func nodeAPIType(provider string, sp map[string]any) string {
	if v, _ := sp["apiType"].(string); v == "chat" || v == "responses" {
		return v
	}
	if strings.Contains(provider, "responses") {
		return "responses"
	}
	return "chat"
}

// nodeFormat is the upstream wire format a node speaks.
func nodeFormat(provider string, sp map[string]any) string {
	switch {
	case strings.HasPrefix(provider, nodePrefixAnthropic):
		return translate.FormatClaude
	case strings.HasPrefix(provider, nodePrefixOpenAI), strings.HasPrefix(provider, nodePrefixEmbedding):
		if nodeAPIType(provider, sp) == "responses" {
			return translate.FormatResponses
		}
		return translate.FormatOpenAI
	}
	return translate.FormatOpenAI
}

// nodeBaseURL resolves the node's endpoint from the connection first (the node
// update route keeps it in sync), then the node row.
func (g *Gateway) nodeBaseURL(provider string, sp map[string]any) string {
	if v, _ := sp["baseUrl"].(string); v != "" {
		return strings.TrimRight(v, "/")
	}
	if n, _ := g.store.GetProviderNode(provider); n != nil {
		if v, _ := n.Data["baseUrl"].(string); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	return ""
}

// nodeEndpoint is the full upstream URL for a node chat call.
func (g *Gateway) nodeEndpoint(provider string, sp map[string]any) (string, error) {
	base := g.nodeBaseURL(provider, sp)
	if base == "" {
		return "", errNoNodeBaseURL
	}
	if strings.HasPrefix(provider, nodePrefixAnthropic) {
		return strings.TrimSuffix(base, "/messages") + "/messages", nil
	}
	if nodeAPIType(provider, sp) == "responses" {
		return base + "/responses", nil
	}
	return base + "/chat/completions", nil
}

var errNoNodeBaseURL = errStr("provider node has no base URL")

type errStr string

func (e errStr) Error() string { return string(e) }

// nodeEmbeddingURL is the embeddings endpoint for a custom node.
func (g *Gateway) nodeEmbeddingURL(provider string, sp map[string]any) string {
	base := strings.TrimSuffix(g.nodeBaseURL(provider, sp), "/embeddings")
	return base + "/embeddings"
}

// isNodeProviderID reports whether a connection's provider is a node row.
func (g *Gateway) isNodeProviderID(id string) bool {
	if !isNodeProvider(id) {
		return false
	}
	n, err := g.store.GetProviderNode(id)
	return err == nil && n != nil
}
