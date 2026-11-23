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
// provider alias). A node prefix shadows a registry id/alias of the same name,
// matching resolvePrefixProvider running before alias resolution.
func (g *Gateway) nodeByPrefix(prefix string) *store.ProviderNode {
	if prefix == "" {
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

// nodeLanes are the routes a node baseUrl may already name. A base ending in
// one is a full endpoint, not a prefix; appending a lane behind it would double
// the path.
var nodeLanes = []string{
	"/chat/completions", "/messages", "/responses", "/completions",
	"/chat", "/embeddings", "/api/chat", "/api/generate",
}

// nodeLane is the route a node type serves. An openai-compatible node picks
// Chat Completions or Responses from its stored apiType, falling back to the
// id substring for legacy rows; every other node type has a fixed lane.
func nodeLane(provider string, sp map[string]any) string {
	switch {
	case strings.HasPrefix(provider, nodePrefixAnthropic):
		return "/messages"
	case strings.HasPrefix(provider, nodePrefixOpenAI):
		if nodeAPIType(provider, sp) == "responses" {
			return "/responses"
		}
	}
	return "/chat/completions"
}

// nodeNeedsVersionPrefix reports whether the base URL carries no path beyond
// its authority, so the gateway's /v1 is still missing.
func nodeNeedsVersionPrefix(base string) bool {
	_, rest, found := strings.Cut(base, "://")
	if !found {
		return true
	}
	_, path, _ := strings.Cut(rest, "/")
	return strings.Trim(path, "/") == ""
}

// nodeEndpoint is the full upstream URL for a node chat call. The stored
// baseUrl is a prefix: a bare host gains /v1, a path gains only the lane, and a
// base that already names a lane is left alone.
func (g *Gateway) nodeEndpoint(provider string, sp map[string]any) (string, error) {
	base := strings.TrimRight(g.nodeBaseURL(provider, sp), "/")
	if base == "" {
		return "", errNoNodeBaseURL
	}
	for _, lane := range nodeLanes {
		if strings.HasSuffix(base, lane) {
			return base, nil
		}
	}
	lane := nodeLane(provider, sp)
	if nodeNeedsVersionPrefix(base) {
		return base + "/v1" + lane, nil
	}
	return base + lane, nil
}

// nodeModelsURL is the models listing that belongs to the node's lane, used by
// the dashboard's validation and live catalogue probes.
func (g *Gateway) nodeModelsURL(provider string, sp map[string]any) string {
	if u, err := g.nodeEndpoint(provider, sp); err == nil {
		if i := strings.Index(u, "/chat/completions"); i >= 0 {
			return u[:i] + "/models"
		}
		for _, lane := range nodeLanes {
			if strings.HasSuffix(u, lane) {
				return strings.TrimSuffix(u, lane) + "/models"
			}
		}
		return strings.TrimRight(u, "/") + "/models"
	}
	return strings.TrimRight(g.nodeBaseURL(provider, sp), "/") + "/models"
}

var errNoNodeBaseURL = errStr("provider node has no base URL")

type errStr string

func (e errStr) Error() string { return string(e) }

// nodeEmbeddingURL is the embeddings endpoint for a custom node. It derives
// from the chat lane, matching the embeddings handler's buildEmbeddingsURL, so
// a bare host still gains /v1 and a stored /embeddings is not doubled.
func (g *Gateway) nodeEmbeddingURL(provider string, sp map[string]any) string {
	base := strings.TrimRight(g.nodeBaseURL(provider, sp), "/")
	if base == "" || strings.HasSuffix(base, "/embeddings") {
		return base
	}
	if u, err := g.nodeEndpoint(provider, sp); err == nil {
		if i := strings.LastIndex(u, "/chat/completions"); i >= 0 {
			return u[:i] + "/embeddings"
		}
		base = u
	}
	return base + "/embeddings"
}

// nodeWireModel strips the node's own routing prefix from the model id. The
// prefix only selects the node; a request forwarded with it reaches the
// upstream gateway as a model it has never heard of.
func (g *Gateway) nodeWireModel(id, model string) string {
	n, _ := g.store.GetProviderNode(id)
	if n == nil {
		return model
	}
	prefix, _ := n.Data["prefix"].(string)
	if prefix == "" {
		return model
	}
	if rest, ok := strings.CutPrefix(model, prefix+"/"); ok {
		return rest
	}
	return model
}

// isNodeProviderID reports whether a connection's provider is a node row.
func (g *Gateway) isNodeProviderID(id string) bool {
	if !isNodeProvider(id) {
		return false
	}
	n, err := g.store.GetProviderNode(id)
	return err == nil && n != nil
}
