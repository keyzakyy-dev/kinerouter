package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/executor"
	"github.com/keyzakyy-dev/kinerouter/internal/lease"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

// chatRequest is the decoded inbound body, kept as a raw map for passthrough.
type chatRequest struct {
	Model  string
	Stream bool
	Format string // client format: openai | claude | responses | gemini
	Raw    map[string]any
}

func (g *Gateway) handleChat(w http.ResponseWriter, r *http.Request, clientFormat string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, g.maxBody))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "failed to read body")
		return
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	stream, _ := raw["stream"].(bool)
	g.handleRaw(w, r, raw, clientFormat, stream)
}

// handleRaw runs the chat pipeline on an already-decoded body. stream is passed
// separately because Gemini-native clients signal it in the URL, not the body.
// resolveAliasChain follows kv modelAliases until the value stops changing, so
// an alias may point at another alias, a combo or a concrete provider/model.
func (g *Gateway) resolveAliasChain(name string) string {
	aliases, err := g.store.GetModelAliases()
	if err != nil {
		return name
	}
	seen := map[string]bool{name: true}
	current := name
	for i := 0; i < 8; i++ {
		next, ok := aliases[current]
		if !ok || next == "" || next == current || seen[next] {
			break
		}
		seen[next] = true
		current = next
	}
	return current
}

func (g *Gateway) handleRaw(w http.ResponseWriter, r *http.Request, raw map[string]any, clientFormat string, stream bool) {
	modelStr, _ := raw["model"].(string)
	if modelStr == "" {
		writeErr(w, http.StatusBadRequest, "Missing model")
		return
	}
	modelStr = stripContextMarker(modelStr)

	apiKey := extractAPIKey(r)
	apiKeyID := ""
	settings, err := g.store.GetSettings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "settings unavailable")
		return
	}
	if require, _ := settings["requireApiKey"].(bool); require {
		if apiKey == "" {
			writeErr(w, http.StatusUnauthorized, "Missing API key")
			return
		}
		ok, key, err := g.store.ValidateAPIKey(apiKey)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "key lookup failed")
			return
		}
		if !ok {
			writeErr(w, http.StatusUnauthorized, "Invalid API key")
			return
		}
		apiKeyID = key.ID
		if !g.keyAllows(key, modelStr) {
			writeErr(w, http.StatusForbidden, "API key is not allowed to use this model")
			return
		}
	}

	// Bypass CLI helper calls before combo rotation burns a slot.
	ua := r.Header.Get("User-Agent")
	if out := g.bypassResponse(raw, modelStr, ua, clientFormat, stream); out != nil {
		ct := "application/json"
		if stream && clientFormat != translate.FormatClaude && clientFormat != translate.FormatGemini {
			ct = "text/event-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(http.StatusOK)
		w.Write(out)
		return
	}

	// Combo expansion: try members in order. An alias may name a combo, so the
	// requested id is resolved through the alias table first.
	modelStr = g.resolveAliasChain(modelStr)
	if combo, err := g.store.GetComboByName(modelStr); err == nil && combo != nil && len(combo.Models) > 0 {
		g.applyJailbreak(raw, apiKeyID, g.jailbreakCandidateIDs(modelStr, combo, nil), clientFormat)
		g.handleCombo(w, r, combo, raw, apiKey, clientFormat, stream)
		return
	}

	targets, err := g.resolveTargets(modelStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	g.applyJailbreak(raw, apiKeyID, g.jailbreakCandidateIDs(modelStr, nil, targets), clientFormat)
	// A single model that cannot serve the request's modality falls back to the
	// capacity-adapter pool first, as in the solo path.
	if required := detectRequiredCapabilities(raw); len(required) > 0 {
		chain := g.augmentWithCapacity([]string{modelStr}, required)
		if len(chain) > 1 {
			combo := &store.Combo{Name: modelStr, Models: chain}
			g.handleComboStrategy(w, r, combo, raw, apiKey, clientFormat, stream,
				g.adapterStrategy(required), 1, resolveSessionID(r.Header, raw, ""))
			return
		}
	}
	g.dispatchDone(w, r, targets, raw, apiKey, clientFormat, stream, resolveSessionID(r.Header, raw, ""))
}

// dispatchDone runs the solo dispatch path and writes the failure it returns.
// Attempts only write on success, so a non-ok result still has a clean writer.
func (g *Gateway) dispatchDone(w http.ResponseWriter, r *http.Request, targets []target, raw map[string]any, apiKey, clientFormat string, stream bool, sessionID string) {
	res := g.dispatch(w, r, targets, raw, apiKey, clientFormat, stream, sessionID)
	if res.ok {
		return
	}
	status := res.status
	if status >= 200 && status < 300 {
		status = http.StatusServiceUnavailable
	}
	if status == 0 {
		status = http.StatusServiceUnavailable
	}
	writeErr(w, status, firstNonEmpty(upstreamMessage(res.body), "No available credentials for this model"))
}

// targetFor resolves a provider token to the provider that serves it and the
// model id to send upstream. A custom node prefix wins over a registry alias of
// the same name, matching resolvePrefixProvider running first.
func (g *Gateway) targetFor(provider, model string) (target, bool) {
	if n := g.nodeByPrefix(provider); n != nil {
		return target{Provider: n.ID, Model: g.nodeWireModel(n.ID, model)}, true
	}
	if id := g.reg.ResolveAlias(provider); g.reg.IsKnownProvider(id) {
		return target{Provider: id, Model: g.reg.UpstreamModelID(id, model)}, true
	}
	return target{}, false
}

// splitProviderModel splits "provider/model" without resolving the provider
// alias, so a custom node prefix can be checked before the alias table.
func splitProviderModel(model string) (provider, rest string, ok bool) {
	i := strings.IndexByte(model, '/')
	if i <= 0 {
		return "", model, false
	}
	return model[:i], model[i+1:], true
}

// resolveTargets maps a requested model to ordered provider/model targets.
func (g *Gateway) resolveTargets(modelStr string) ([]target, error) {
	if provider, model, ok := splitProviderModel(modelStr); ok {
		if t, ok := g.targetFor(provider, model); ok {
			return []target{t}, nil
		}
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
	// Model alias table (kv scope modelAliases).
	aliases, err := g.store.GetModelAliases()
	if err == nil {
		if resolved, ok := aliases[modelStr]; ok {
			if provider, model, ok := splitProviderModel(resolved); ok {
				if t, ok := g.targetFor(provider, model); ok {
					return []target{t}, nil
				}
			}
		}
	}
	provider := g.inferProvider(modelStr)
	if provider == "" {
		return nil, fmt.Errorf("cannot resolve provider for model %q; use provider/model", modelStr)
	}
	return []target{{Provider: provider, Model: modelStr}}, nil
}

type target struct {
	Provider string
	Model    string
}

// forceStream reports whether the provider only emits SSE (Codex / ChatGPT
// backend). A non-stream client still gets JSON: attempt() folds the stream.
func (g *Gateway) forceStream(provider string) bool {
	t, ok := g.reg.ByID[provider]
	return ok && t.Transport != nil && t.Transport.ForceStream
}

// flattenComboModels replaces a nested combo member with its leaves so
// rotation covers every reachable model; cycles stop at the visited set.
func (g *Gateway) flattenComboModels(name string, models []string) []string {
	out := []string{}
	var walk func([]string, map[string]bool)
	walk = func(list []string, seen map[string]bool) {
		for _, member := range list {
			if nested, err := g.store.GetComboByName(g.resolveAliasChain(member)); err == nil && nested != nil && len(nested.Models) > 0 && !seen[nested.Name] {
				seen[nested.Name] = true
				walk(nested.Models, seen)
				continue
			}
			out = append(out, member)
		}
	}
	walk(models, map[string]bool{name: true})
	return out
}

func (g *Gateway) handleCombo(w http.ResponseWriter, r *http.Request, combo *store.Combo, raw map[string]any, apiKey, clientFormat string, stream bool) {
	strategy, sticky := g.comboConfig(combo)
	// Fusion fans out to every panel member and judges the answers; it runs on
	// the stored member list, untouched by the capacity adapter (chat.js).
	if strategy == "fusion" {
		g.handleFusion(w, r, combo, raw, apiKey, clientFormat, stream)
		return
	}
	// Capacity adapter: a member set that covers no requested modality is
	// prefixed with the adapter pool (combo.js augmentModelsWithCapacityAdapter).
	members := g.flattenComboModels(combo.Name, combo.Models)
	if required := detectRequiredCapabilities(raw); len(required) > 0 {
		if augmented := g.augmentWithCapacity(members, required); len(augmented) > len(members) {
			members = augmented
			strategy = g.adapterStrategy(required)
			sticky = 1
		}
	}
	g.handleComboStrategy(w, r, &store.Combo{Name: combo.Name, Models: members}, raw,
		apiKey, clientFormat, stream, strategy, sticky, resolveSessionID(r.Header, raw, ""))
}

// handleComboStrategy tries members in order with an explicit rotation strategy.
func (g *Gateway) handleComboStrategy(w http.ResponseWriter, r *http.Request, combo *store.Combo, raw map[string]any, apiKey, clientFormat string, stream bool, strategy string, sticky int, sessionID string) {
	order := rotateMembers(combo.Name, combo.Models, strategy, sticky)
	if strategy == "smart" {
		// System One picks the member; failure keeps the configured order.
		order = g.smartComboOrder(r.Context(), combo, g.comboConfigMap(combo.Name), raw, sessionID)
	}
	var lastErr []byte
	var lastStatus int
	for _, member := range order {
		targets, err := g.resolveTargets(member)
		if err != nil {
			continue
		}
		result := g.dispatch(w, r, targets, raw, apiKey, clientFormat, stream, sessionID)
		if result.ok {
			return
		}
		lastErr, lastStatus = result.body, result.status
	}
	if lastStatus == 0 {
		lastStatus = http.StatusServiceUnavailable
	}
	if lastStatus >= 200 && lastStatus < 300 {
		lastStatus = http.StatusServiceUnavailable
	}
	w.WriteHeader(lastStatus)
	if len(lastErr) > 0 {
		w.Write(lastErr)
	}
}

type dispatchResult struct {
	ok     bool
	status int
	body   []byte
}

// dispatch runs one target through the account-fallback loop.
func (g *Gateway) dispatch(w http.ResponseWriter, r *http.Request, targets []target, raw map[string]any, apiKey, clientFormat string, stream bool, sessionID string) dispatchResult {
	var last dispatchResult
	for _, t := range targets {
		conns, err := g.store.ListConnections(t.Provider, true)
		if err == nil {
			conns = filterByEnabledModels(t.Provider, conns, t.Model)
			conns = g.filterByStrictAssignment(t.Provider, conns, t.Model)
		}
		if err != nil || len(conns) == 0 {
			// Free / no-auth provider: one virtual account, no stored credential.
			if g.exec.IsNoAuth(t.Provider) {
				res, err := g.attempt(r.Context(), r.Header, &store.Connection{ID: "noauth", Provider: t.Provider},
					t, raw, apiKey, clientFormat, stream, w, sessionID)
				if err == nil {
					return res
				}
				var upErr *upstreamError
				if errors.As(err, &upErr) {
					return dispatchResult{status: upErr.status, body: []byte(upErr.raw)}
				}
				continue
			}
			continue
		}
		tried := map[string]bool{}
		for attempt := 0; attempt <= len(conns); attempt++ {
			conn := pinnedConnection(conns, r.Header.Get("x-connection-id"), tried)
			if conn == nil {
				conn = g.pickConnection(conns, t.Model, tried)
			}
			if conn == nil {
				break
			}
			tried[conn.ID] = true
			res, err := g.attempt(r.Context(), r.Header, conn, t, raw, apiKey, clientFormat, stream, w, sessionID)
			if err == nil {
				return res
			}
			var upErr *upstreamError
			if !errors.As(err, &upErr) {
				// Transport failure: next account.
				logWarn("fallback", "upstream failed provider=%s model=%s error=%v", t.Provider, t.Model, err)
				continue
			}
			if upErr.status == http.StatusUnauthorized || upErr.status == http.StatusForbidden ||
				upErr.status == http.StatusTooManyRequests || upErr.status == http.StatusPaymentRequired {
				g.markUnavailable(conn, upErr.status, upErr.message, t.Model)
				logWarn("fallback", "upstream failed provider=%s model=%s status=%d error=%s conn=%s",
					t.Provider, t.Model, upErr.status, upErr.message, conn.ID)
				last = dispatchResult{status: upErr.status, body: []byte(upErr.raw)}
				continue
			}
			if upErr.status >= 500 {
				// A 5xx (including an unusable 200 body) is retryable: another
				// account or combo member may answer.
				logWarn("fallback", "upstream failed provider=%s model=%s status=%d conn=%s",
					t.Provider, t.Model, upErr.status, conn.ID)
				last = dispatchResult{status: upErr.status, body: []byte(upErr.raw)}
				continue
			}
			return dispatchResult{status: upErr.status, body: []byte(upErr.raw)}
		}
	}
	if last.status != 0 {
		return last
	}
	return dispatchResult{status: http.StatusServiceUnavailable,
		body: []byte(`{"error":{"message":"No available credentials for this model","type":"service_unavailable"}}`)}
}

type upstreamError struct {
	status  int
	message string
	raw     string
}

func (e *upstreamError) Error() string { return fmt.Sprintf("upstream %d: %s", e.status, e.message) }

// attempt performs one upstream call and streams the result back.
func (g *Gateway) attempt(ctx context.Context, hdr http.Header, conn *store.Connection, t target, raw map[string]any, apiKey, clientFormat string, stream bool, w http.ResponseWriter, sessionID string) (res dispatchResult, err error) {
	// One in-flight slot per attempt; the deferred release covers every exit
	// path (JSON, folded stream, mid-stream error).
	attemptStart := time.Now()
	g.store.TrackPending(t.Model, t.Provider, conn.ID, true, false)
	defer func() {
		g.store.TrackPending(t.Model, t.Provider, conn.ID, false, err != nil)
		if err != nil {
			msg := err.Error()
			if upErr := (*upstreamError)(nil); errors.As(err, &upErr) && upErr.message != "" {
				msg = upErr.message
			}
			g.saveDetail(conn, t, translate.Usage{}, attemptStart, "error", msg)
		}
	}()

	wire := g.reg.Format(t.Provider)
	// Custom nodes declare their own wire format (openai / responses / claude).
	if isNodeProvider(t.Provider) {
		wire = nodeFormat(t.Provider, conn.Specific())
	}
	// A declared per-model target format wins over the provider default (e.g.
	// OpenCode's gpt-5.x lane is Responses while its claude ids are messages).
	if tf := g.reg.ModelTargetFormat(t.Provider, t.Model); tf != "" {
		wire = tf
	}
	if src := g.reg.TransportForSource(t.Provider, clientFormat); src != nil {
		wire = src.Format
	}
	providerFormat := wireFormat(wire)
	// Responses upstreams stream only; ask for SSE even from a JSON client,
	// then fold below.
	// Cursor's upstream RPC is always a frame stream, even for a JSON client;
	// the fold below turns it back into one object.
	streamUpstream := stream || providerFormat == translate.FormatResponses ||
		g.forceStream(t.Provider) || providerFormat == "cursor"
	// An adapter-pool model may have a smaller context window than the request
	// assumes; trim the middle of the history to fit before translating.
	reqRaw := raw
	if g.isAdapterModel(t.Provider, t.Model) {
		if ctxWin := g.contextWindowFor(t.Provider + "/" + t.Model); ctxWin > 0 {
			reqRaw = stripHistoryForContext(cloneMap(raw), ctxWin)
		}
	}
	// Per-provider thinking override from the dashboard, mirroring
	// chatCore.js: injected before translation and only when the client has
	// not already chosen a mode. Copy first — reqRaw may alias the caller's
	// body, which a combo retry reuses.
	if mode := g.providerThinkingMode(t.Provider); mode != "" && mode != "auto" {
		reqRaw = cloneMap(reqRaw)
		switch mode {
		case "on":
			if reqRaw["thinking"] == nil {
				reqRaw["thinking"] = map[string]any{"type": "enabled", "budget_tokens": 10000}
			}
		case "off":
			if reqRaw["thinking"] == nil {
				reqRaw["thinking"] = map[string]any{"type": "disabled"}
			}
		default:
			if reqRaw["reasoning_effort"] == nil {
				reqRaw["reasoning_effort"] = mode
			}
		}
	}
	reqBody, err := translateRequestWithSession(clientFormat, providerFormat, reqRaw, t.Model, streamUpstream, sessionID)
	if err != nil {
		return dispatchResult{}, err
	}
	// Token savers run on the translated body (RTK compress → caveman →
	// ponytail → custom prompt), as chatCore.js does.
	var savedTokens int64
	if settings, err := g.store.GetSettings(); err == nil {
		cfg := saverCfgFrom(g, settings, hdr, t.Model, providerFormat)
		if cfg.active() {
			// The console reports what the savers bought: bytes removed from the
			// outbound body, in the same 4-bytes-per-token unit the rest of the
			// dashboard uses.
			before := len(mustJSON(reqBody))
			cfg.apply(reqBody, providerFormat)
			if after := len(mustJSON(reqBody)); after < before {
				savedTokens = int64(before-after) / 4
			}
		} else {
			cfg.apply(reqBody, providerFormat)
		}
	}
	// Per-provider body quirks (kimchi artifact stripping, codebuddy neutral
	// system prompt, azure/iflow/etc. transport handled after build).
	executor.ApplyQuirksBody(t.Provider, t.Model, reqBody, streamUpstream)
	// Kiro reads the CodeWhisperer payload shape from an OpenAI- or
	// Claude-shaped body directly (no pivot), so the savers above rewrote the
	// source body and Kiro must be built last.
	var kiroRestore map[string]string
	if wire == "kiro" {
		payload, restore, ok := translate.ToKiro(reqBody, t.Model, translate.KiroOptions{
			ProfileARN: translate.KiroProfileARN(conn.SpecificStr("authMethod"), conn.SpecificStr("profileArn")),
			SessionID:  firstNonEmpty(conn.SpecificStr("conversationId"), sessionID, conn.ID),
		})
		if !ok {
			return dispatchResult{}, &upstreamError{status: http.StatusBadRequest, raw: `{"error":{"message":"kiro: conversation cannot be canonicalized","type":"invalid_request_error"}}`}
		}
		kiroRestore = restore
		reqBody = payload
	}
	// Cursor sends protobuf, not JSON; build the frame here so the surface
	// choice (legacy vs agent duplex) can also see the connection.
	var cursorPlan cursorRequest
	if wire == "cursor" {
		ua := ""
		if hdr != nil {
			ua = hdr.Get("User-Agent")
		}
		forceAgent := strings.Contains(strings.ToLower(ua), "claude-code") || strings.Contains(strings.ToLower(ua), "claude-cli")
		cursorPlan = prepareCursorRequest(reqBody, t.Model, forceAgent)
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return dispatchResult{}, err
	}
	if wire == "cursor" {
		bodyBytes = cursorPlan.body
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	creds := executor.CredentialsFromConnection(conn)
	// agy-acp signs in with the antigravity Google client but keeps its own
	// rows. A connection may reference an antigravity account instead of
	// carrying tokens, so resolve (and refresh) that source here.
	if t.Provider == "agy-acp" {
		if src := conn.SpecificStr("sourceConnection"); src != "" && src != conn.ID {
			ref, rerr := g.store.GetConnection(src)
			if rerr != nil || ref == nil || ref.Provider != "antigravity" {
				return dispatchResult{}, &upstreamError{status: http.StatusBadRequest,
					message: "agy-acp: source connection must be an antigravity connection",
					raw:     string(mustJSON(map[string]any{"error": map[string]any{"message": "agy-acp: source connection must be an antigravity connection", "type": "invalid_request_error"}}))}
			}
			if _, rerr := g.oauth.RefreshOne(ctx, g.store, ref, 2*time.Minute); rerr == nil {
				if fresh, ferr := g.store.GetConnection(src); ferr == nil && fresh != nil {
					ref = fresh
				}
			}
			creds.AccessToken = ref.Str("accessToken")
			creds.RefreshToken = ref.Str("refreshToken")
			if exp, perr := time.Parse(time.RFC3339, ref.Str("expiresAt")); perr == nil {
				creds.ExpiresAt = exp
			}
		}
	}
	// MiniMax desktop lease: the connection stores no token, so borrow one
	// from the running desktop broker (cached until it nears expiry).
	leaseDir := ""
	if lease.IsLeaseAuth(conn.AuthType) {
		leaseDir = lease.ResolveDataDir(conn.SpecificStr("mcodeDataDir"))
		tok, lerr := lease.TokenFor(leaseDir)
		if lerr != nil {
			return dispatchResult{}, &upstreamError{status: http.StatusBadGateway,
				message: lerr.Error(),
				raw:     string(mustJSON(map[string]any{"error": map[string]any{"message": lerr.Error(), "type": "upstream_error"}}))}
		}
		creds.AccessToken = tok.AccessToken
	}
	// A connection bound to a proxy pool uses the pool's URL unless a
	// connection-level proxy already overrides it (connections.go). The pool is
	// validated at save time, so a stored URL is trusted here.
	if creds.ProxyURL == "" {
		creds.ProxyURL = g.providerProxyURL(t.Provider)
		if creds.ProxyURL == "" {
			if pid := conn.SpecificStr("proxyPoolId"); pid != "" {
				if pool, _ := g.store.GetProxyPool(pid); pool != nil && pool.IsActive {
					if u, ok := pool.Data["proxyUrl"].(string); ok {
						creds.ProxyURL = u
					}
				}
			}
		}
	}
	if sessionID != "" {
		creds.SessionID = sessionID
	}
	if wire == "commandcode" {
		// x-session-id matches the body's threadId; the slug stands in for the
		// CLI's slugify(cwd).
		thread := translate.SessionUUID(sessionID)
		creds.ExtraHeaders = map[string]string{
			"x-project-slug": projectSlug(commandCodeWorkingDir()),
			"x-session-id":   thread,
		}
		// The translator already emitted threadId; keep both in one place by
		// rewriting the body from the same value.
		reqBody["threadId"] = thread
		if b, err := json.Marshal(reqBody); err == nil {
			bodyBytes = b
		}
	}
	// User header overrides from the dashboard apply after the registry headers
	// (build() order), so they can pin e.g. an extra beta header. The override
	// route blocks host/authorization/cookie, so auth cannot be hijacked here.
	if oh := g.providerOverrideHeaders(g.reg.ResolveAlias(t.Provider)); len(oh) > 0 {
		if creds.ExtraHeaders == nil {
			creds.ExtraHeaders = map[string]string{}
		}
		for k, v := range oh {
			creds.ExtraHeaders[k] = v
		}
	}
	execReq := executor.Request{
		Provider:     t.Provider,
		Model:        t.Model,
		Format:       wire,
		Body:         bodyBytes,
		Stream:       streamUpstream,
		Creds:        creds,
		ConnectionID: conn.ID,
	}
	// A custom node owns its endpoint (baseUrl + /chat/completions, /responses
	// or /messages), which the registry cannot derive.
	if isNodeProvider(t.Provider) {
		endpoint, err := g.nodeEndpoint(t.Provider, conn.Specific())
		if err != nil {
			return dispatchResult{}, &upstreamError{status: http.StatusBadRequest,
				raw: `{"error":{"message":"` + err.Error() + `","type":"invalid_request_error"}}`}
		}
		execReq.URLOverride = endpoint
	}
	var resp *executor.Response
	var agentSession *executor.CursorSession
	if wire == "cursor" {
		// Cursor's machine id is required for both the checksum and the
		// session id; the JS executor refuses the call without it too.
		if creds.MachineID == "" {
			return dispatchResult{}, &upstreamError{status: http.StatusBadRequest,
				raw: `{"error":{"message":"cursor: machine id is required","type":"invalid_request_error"}}`}
		}
		endpoint, err := executor.CursorURL(cursorPlan.agent)
		if err != nil {
			return dispatchResult{}, err
		}
		execReq.URLOverride = endpoint
		if cursorPlan.agent {
			agentSession, err = g.exec.CursorAgentSession(ctx, execReq, endpoint)
			if err != nil {
				return dispatchResult{}, err
			}
			agentSession.Write(cursorPlan.body)
			defer agentSession.Close()
			resp = agentSession.Response
		}
	}
	if agentSession == nil {
		resp, err = g.exec.Execute(ctx, execReq)
		if err != nil {
			return dispatchResult{}, err
		}
	}
	defer resp.Body.Close()

	if resp.Status < 200 || resp.Status >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		if leaseDir != "" && resp.Status == http.StatusUnauthorized {
			// The desktop app rotated the token out from under us — drop the
			// cache so the next attempt takes a fresh lease.
			lease.Invalidate(leaseDir)
		}
		status, errBody := normalizeQuota(t.Provider, resp.Status, errBody)
		return dispatchResult{}, &upstreamError{
			status:  status,
			message: upstreamMessage(errBody),
			raw:     string(errBody),
		}
	}

	start := time.Now()
	usage := translate.Usage{}

	if !stream {
		if wire == "cursor" {
			folded, err := collectCursor(resp.Body, agentSession, t.Model)
			if err != nil {
				return dispatchResult{}, err
			}
			foldedBody := mustJSON(folded)
			if e := emptyCompletionError(foldedBody); e != nil {
				return dispatchResult{}, e
			}
			outBody, u := translateNonStream(translate.FormatOpenAI, clientFormat, foldedBody)
			mergeUsage(&usage, u)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(outBody)
			g.recordUsage(conn, t, apiKey, clientFormat, u, start, nil, savedTokens)
			return dispatchResult{ok: true, status: http.StatusOK}, nil
		}
		// Kiro answers in AWS EventStream, never SSE: fold frames into one
		// Chat Completions object first.
		if wire == "kiro" {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
			folded := translate.CollectKiroEvents(bytes.NewReader(respBody), t.Model, kiroRestore)
			providerFormat = translate.FormatOpenAI
			foldedBody := mustJSON(folded)
			if e := emptyCompletionError(foldedBody); e != nil {
				return dispatchResult{}, e
			}
			outBody, u := translateNonStream(providerFormat, clientFormat, foldedBody)
			mergeUsage(&usage, u)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(outBody)
			g.recordUsage(conn, t, apiKey, clientFormat, u, start, nil, savedTokens)
			return dispatchResult{ok: true, status: http.StatusOK}, nil
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		// A Responses upstream always streams; a non-stream client must fold SSE back.
		if isSSE(respBody) {
			respBody = mustJSON(translate.CollectResponsesStream(bytes.NewReader(respBody)))
			providerFormat = translate.FormatOpenAI
		} else if isNDJSONStream(providerFormat) {
			respBody = mustJSON(collectNDJSON(providerFormat, bytes.NewReader(respBody)))
			providerFormat = translate.FormatOpenAI
		}
		if e := emptyCompletionError(respBody); e != nil {
			return dispatchResult{}, e
		}
		outBody, u := translateNonStream(providerFormat, clientFormat, respBody)
		mergeUsage(&usage, u)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(outBody)
		g.recordUsage(conn, t, apiKey, clientFormat, u, start, nil, savedTokens)
		return dispatchResult{ok: true, status: http.StatusOK}, nil
	}

	// Streaming: forward translated SSE. The peeker holds the response until
	// the stream proves it carries content, so a silent 200 can still fail over.
	peek := newStreamPeeker(w)
	peek.Header().Set("Content-Type", "text/event-stream")
	peek.Header().Set("Cache-Control", "no-cache")
	peek.Header().Set("Connection", "keep-alive")
	peek.Header().Set("X-Accel-Buffering", "no")
	peek.WriteHeader(http.StatusOK)

	stats := &streamStats{}
	out := statsWriter{w: peek, s: stats}

	var u translate.Usage
	if wire == "cursor" {
		// ponytail: no empty-turn tail-only retry (JS CURSOR_EMPTY_TURN_MAX_RETRIES)
		// and no live GetUsableModels resolution for the Auto alias. Add the retry
		// when a long Cursor replay is seen wedging; add model resolution when
		// Auto must report the concrete upstream model.
		u, err = streamCursor(resp.Body, agentSession, clientFormat, t.Model, peek, peek)
	} else if wire == "kiro" {
		u = streamKiro(resp.Body, clientFormat, t.Model, kiroRestore, out, peek)
		err = nil
	} else {
		u, err = streamTranslate(resp.Body, providerFormat, clientFormat, out, peek)
	}
	if err != nil {
		if !peek.committed {
			return dispatchResult{}, err
		}
		// Headers already sent; report what we have.
		g.recordUsage(conn, t, apiKey, clientFormat, u, start, stats, savedTokens)
		return dispatchResult{ok: true, status: http.StatusOK}, nil
	}
	if ferr := peek.Finish(); ferr != nil {
		if errors.Is(ferr, errStreamingZeroContent) {
			return dispatchResult{}, ferr
		}
		// In-band provider error was already committed for the client.
		g.recordUsage(conn, t, apiKey, clientFormat, u, start, stats, savedTokens)
		return dispatchResult{ok: true, status: http.StatusOK}, nil
	}
	g.recordUsage(conn, t, apiKey, clientFormat, u, start, stats, savedTokens)
	return dispatchResult{ok: true, status: http.StatusOK}, nil
}

// pinnedConnection honours a client's x-connection-id request, but only when
// the id names a connection of the provider being tried and it has not already
// failed in this request.
func pinnedConnection(conns []*store.Connection, id string, tried map[string]bool) *store.Connection {
	if id == "" {
		return nil
	}
	for _, c := range conns {
		if c.ID == id && !tried[c.ID] {
			return c
		}
	}
	return nil
}

// connectionEnabledModels reads providerSpecificData.enabledModels first, then
// the top-level field: both writers are in the wild.
func connectionEnabledModels(c *store.Connection) []string {
	for _, raw := range []any{c.Specific()["enabledModels"], c.Get("enabledModels")} {
		list, ok := raw.([]any)
		if !ok {
			continue
		}
		out := make([]string, 0, len(list))
		for _, v := range list {
			if s, ok := v.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// assignedModel reads the model an account was pinned to (assignedModel, or
// freebuffModel for Freebuff seats), from the top level or providerSpecificData.
func assignedModel(c *store.Connection) string {
	keys := []string{"assignedModel", "freebuffModel"}
	for _, key := range keys {
		if v, ok := c.Get(key).(string); ok && v != "" {
			return v
		}
	}
	for _, key := range keys {
		if v, ok := c.Specific()[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// filterByStrictAssignment honours providerStrategies[provider]
// .strictModelAssignment: only accounts pinned to the requested model are
// eligible. Off by default and a no-op for every other provider.
func (g *Gateway) filterByStrictAssignment(provider string, conns []*store.Connection, model string) []*store.Connection {
	if model == "" || len(conns) == 0 {
		return conns
	}
	settings, err := g.store.GetSettings()
	if err != nil {
		return conns
	}
	byProvider, _ := settings["providerStrategies"].(map[string]any)
	entry, _ := byProvider[provider].(map[string]any)
	strict, _ := entry["strictModelAssignment"].(bool)
	if !strict {
		return conns
	}
	kept := make([]*store.Connection, 0, len(conns))
	for _, c := range conns {
		if assignedModel(c) == model {
			kept = append(kept, c)
		}
	}
	return kept
}

// filterByEnabledModels drops codex accounts whose enabledModels pin excludes
// the requested model; every other provider is untouched.
func filterByEnabledModels(provider string, conns []*store.Connection, model string) []*store.Connection {
	if provider != "codex" || model == "" {
		return conns
	}
	kept := make([]*store.Connection, 0, len(conns))
	for _, c := range conns {
		enabled := connectionEnabledModels(c)
		if len(enabled) == 0 {
			kept = append(kept, c)
			continue
		}
		for _, m := range enabled {
			if m == model {
				kept = append(kept, c)
				break
			}
		}
	}
	return kept
}

func (g *Gateway) pickConnection(conns []*store.Connection, model string, tried map[string]bool) *store.Connection {
	avail := make([]*store.Connection, 0, len(conns))
	for _, c := range conns {
		if !tried[c.ID] && !g.isModelLocked(c, model) {
			avail = append(avail, c)
		}
	}
	if len(avail) == 0 {
		return nil
	}
	if len(tried) == 0 {
		if strategy, sticky := g.rrConfig(avail[0].Provider); strategy == "round-robin" {
			c, stay := rrPick(avail, sticky)
			count := 1
			if stay {
				count = rrCount(c) + 1
			}
			g.store.PatchConnection(c.ID, map[string]any{
				"lastUsedAt":          time.Now().UTC().Format(time.RFC3339),
				"consecutiveUseCount": count,
			})
			return c
		}
	}
	return avail[0]
}

// providerThinkingMode reads settings.providerThinking[provider].mode.
func (g *Gateway) providerThinkingMode(provider string) string {
	settings, err := g.store.GetSettings()
	if err != nil {
		return ""
	}
	byProvider, _ := settings["providerThinking"].(map[string]any)
	entry, _ := byProvider[g.reg.ResolveAlias(provider)].(map[string]any)
	mode, _ := entry["mode"].(string)
	return mode
}

// rrConfig resolves the per-provider strategy override first, then the global
// setting. Mirrors auth.js getProviderCredentials.
func (g *Gateway) rrConfig(provider string) (strategy string, sticky int) {
	settings, err := g.store.GetSettings()
	if err != nil {
		return "", 3
	}
	strategy, _ = settings["fallbackStrategy"].(string)
	sticky = int(num(settings["stickyRoundRobinLimit"]))
	if p, ok := settings["providerStrategies"].(map[string]any); ok {
		if po, ok := p[provider].(map[string]any); ok {
			if s, ok := po["fallbackStrategy"].(string); ok && s != "" {
				strategy = s
			}
			if n := int(num(po["stickyRoundRobinLimit"])); n > 0 {
				sticky = n
			}
		}
	}
	if sticky <= 0 {
		sticky = 3
	}
	return strategy, sticky
}

// saveProviderStrategy writes one field of providerStrategies[alias] and keeps
// the rest of that provider's entry (proxyPoolId, rotateStrategy) intact.
// handlePatchSettings replaces the whole map, so a blind PATCH would drop them.
func (g *Gateway) saveProviderStrategy(alias string, apply func(map[string]any)) error {
	settings, err := g.store.GetSettings()
	if err != nil {
		return err
	}
	all, _ := settings["providerStrategies"].(map[string]any)
	entry, _ := all[alias].(map[string]any)
	if entry != nil {
		entry = cloneMap(entry)
	} else {
		entry = map[string]any{}
	}
	apply(entry)
	next := cloneMap(all)
	if len(entry) == 0 {
		delete(next, alias)
	} else {
		next[alias] = entry
	}
	_, err = g.store.UpdateSettings(map[string]any{"providerStrategies": next})
	return err
}

// providerProxyURL resolves providerStrategies[alias].proxyPoolId, honouring
// rotateStrategy across every active pool (rotateStrategy=none pins the one
// pool). An unbound provider returns ""; per-connection pools win over this,
// so the answer is only consulted when the connection has no proxy of its own.
func (g *Gateway) providerProxyURL(provider string) string {
	settings, err := g.store.GetSettings()
	if err != nil {
		return ""
	}
	all, _ := settings["providerStrategies"].(map[string]any)
	entry, _ := all[g.reg.ResolveAlias(provider)].(map[string]any)
	if entry == nil {
		return ""
	}
	pools, err := g.store.ListProxyPools(nil)
	if err != nil || len(pools) == 0 {
		return ""
	}
	url := func(id string) string {
		for _, p := range pools {
			if p.ID == id && p.IsActive {
				u, _ := p.Data["proxyUrl"].(string)
				return u
			}
		}
		return ""
	}
	active := make([]string, 0, len(pools))
	for _, p := range pools {
		if p.IsActive {
			active = append(active, p.ID)
		}
	}
	switch entry["rotateStrategy"] {
	case "round-robin":
		if len(active) == 0 {
			return ""
		}
		key := g.reg.ResolveAlias(provider)
		g.proxyMu.Lock()
		i := g.proxyNext[key]
		g.proxyNext[key] = (i + 1) % len(active)
		g.proxyMu.Unlock()
		return url(active[i%len(active)])
	case "random":
		if len(active) == 0 {
			return ""
		}
		return url(active[rand.IntN(len(active))])
	}
	id, _ := entry["proxyPoolId"].(string)
	return url(id)
}

// rrPick stays on the most recently used account until its sticky budget is
// spent, then rotates to the least recently used (never-used first).
func rrPick(avail []*store.Connection, sticky int) (c *store.Connection, stay bool) {
	sorted := append([]*store.Connection(nil), avail...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ui, uj := sorted[i].Str("lastUsedAt"), sorted[j].Str("lastUsedAt")
		switch {
		case ui == "" && uj == "":
			return sorted[i].Priority < sorted[j].Priority
		case ui == "":
			return true // never used is the oldest
		case uj == "":
			return false
		}
		return ui < uj
	})
	oldest := sorted[0]
	var newest *store.Connection
	for i := len(sorted) - 1; i >= 0; i-- {
		if sorted[i].Str("lastUsedAt") != "" {
			newest = sorted[i]
			break
		}
	}
	if newest != nil && rrCount(newest) < sticky {
		return newest, true
	}
	return oldest, false
}

func rrCount(c *store.Connection) int {
	return int(num(c.Get("consecutiveUseCount")))
}

func (g *Gateway) isModelLocked(c *store.Connection, model string) bool {
	if until, ok := c.Data["modelLock_"+model].(string); ok && until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil && time.Now().Before(t) {
			return true
		}
	}
	if until, ok := c.Data["modelLock___all"].(string); ok && until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil && time.Now().Before(t) {
			return true
		}
	}
	return false
}

func (g *Gateway) markUnavailable(c *store.Connection, status int, message, model string) {
	until := time.Now().Add(2 * time.Minute)
	if status == http.StatusTooManyRequests {
		until = time.Now().Add(5 * time.Minute)
	}
	g.store.PatchConnection(c.ID, map[string]any{
		"modelLock_" + model: until.UTC().Format(time.RFC3339),
		"testStatus":         "unavailable",
		"lastError":          message,
		"errorCode":          status,
		"lastErrorAt":        time.Now().UTC().Format(time.RFC3339),
	})
}

// usageLogLine renders the same one-line usage record 9router-go emits:
// provider, model, token split, stream timing, cost, what the savers bought,
// and the account that paid for the turn. Token counts are cache-inclusive,
// matching the stored usage row.
func usageLogLine(conn *store.Connection, t target, u translate.Usage, cost float64, start time.Time, stats *streamStats, savedTokens int64) string {
	prompt := u.PromptTokens + u.CacheRead + u.CacheCreation
	line := fmt.Sprintf("logged provider=%s model=%s prompt=%d completion=%d cached=%d cache_creation=%d cache_creation_5m=0 cache_creation_1h=0 ttft_ms=%d latency_ms=%d cost=%v tok_per_sec=%v max_gap_ms=%d",
		t.Provider, t.Model, prompt, u.CompletionTokens, u.CacheRead, u.CacheCreation,
		stats.ttftMs(start), time.Since(start).Milliseconds(), cost,
		stats.tokPerSec(u.CompletionTokens), stats.maxGapMs())
	if savedTokens > 0 {
		original := prompt + savedTokens
		line += fmt.Sprintf(" compressed=%d->%d (%d%% saved)", original, prompt, savedTokens*100/original)
	}
	if conn.ID == "" || conn.ID == "noauth" {
		return line + " account=Public / Direct"
	}
	line += " conn=" + conn.ID
	if conn.Name != "" {
		line += " connName=" + conn.Name
	}
	if conn.Email != "" {
		line += " email=" + conn.Email
	}
	if egress := conn.Str("egress"); egress != "" {
		line += " egress=" + egress
	}
	return line
}

func (g *Gateway) recordUsage(conn *store.Connection, t target, apiKey, endpoint string, u translate.Usage, start time.Time, stats *streamStats, savedTokens int64) {
	// A successful call proves the connection works; drop any stale
	// "unavailable" marker left by an earlier failure.
	if conn.Str("testStatus") != "active" || conn.Str("lastError") != "" {
		g.store.PatchConnection(conn.ID, map[string]any{
			"testStatus":   "active",
			"lastError":    nil,
			"lastErrorAt":  nil,
			"errorCode":    nil,
			"backoffLevel": 0,
		})
	}
	var cost float64
	if id := g.reg.ResolveAlias(t.Provider); id != "" {
		cost = g.pricing.Cost(id, t.Model, u.PromptTokens+u.CacheRead+u.CacheCreation, u.CompletionTokens)
	}
	tokens := map[string]int64{
		"prompt_tokens":     u.PromptTokens,
		"completion_tokens": u.CompletionTokens,
		"cached_tokens":     u.CacheRead,
	}
	g.saveDetail(conn, t, u, start, "success", "")
	logInfo("usage", "%s", usageLogLine(conn, t, u, cost, start, stats, savedTokens))
	g.store.SaveUsage(store.UsageRecord{
		Timestamp:        time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Provider:         t.Provider,
		Model:            t.Model,
		ConnectionID:     conn.ID,
		APIKey:           apiKey,
		Endpoint:         endpoint,
		PromptTokens:     u.PromptTokens + u.CacheRead + u.CacheCreation,
		CompletionTokens: u.CompletionTokens,
		Cost:             cost,
		Status:           "success",
		Tokens:           string(mustJSON(tokens)),
	})
}

// ---------------------------------------------------------------- helpers

func (g *Gateway) keyAllows(key *store.APIKey, model string) bool {
	if key == nil || !key.AccessRestricted {
		return true
	}
	for _, allowed := range key.AccessAllow {
		if strings.EqualFold(allowed, model) || strings.EqualFold(allowed, "*") {
			return true
		}
	}
	return false
}

func extractAPIKey(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if v := r.Header.Get("x-api-key"); v != "" {
		return v
	}
	if v := r.Header.Get("x-goog-api-key"); v != "" {
		return v
	}
	return r.URL.Query().Get("key")
}

// stripContextMarker removes Claude Code's `<model>[1m]` capability marker.
// Only that marker is stripped: a real model id may end in another bracket.
func stripContextMarker(s string) string {
	if len(s) > 4 && strings.EqualFold(s[len(s)-4:], "[1m]") {
		return s[:len(s)-4]
	}
	return s
}

func upstreamMessage(body []byte) string {
	var obj map[string]any
	if json.Unmarshal(body, &obj) == nil {
		if e, ok := obj["error"].(map[string]any); ok {
			if m, ok := e["message"].(string); ok {
				return m
			}
		}
		if m, ok := obj["message"].(string); ok {
			return m
		}
	}
	s := string(body)
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}

func mergeUsage(dst *translate.Usage, u translate.Usage) {
	if u.PromptTokens > dst.PromptTokens {
		dst.PromptTokens = u.PromptTokens
	}
	if u.CompletionTokens > dst.CompletionTokens {
		dst.CompletionTokens = u.CompletionTokens
	}
	dst.CacheRead += u.CacheRead
	dst.CacheCreation += u.CacheCreation
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// ---------------------------------------------------------------- format dispatch

// wireFormat collapses the Cloud Code / Vertex formats onto their Gemini body
// shape; the executor still gets the raw registry format for URL + headers.
func wireFormat(f string) string {
	switch f {
	case "commandcode", "ollama", "cursor":
		// Own body shapes, built by their translators; never pivoted to Gemini.
		return f
	case "gemini-cli", "antigravity", "vertex":
		return translate.FormatGemini
	}
	return f
}

func translateRequestWithSession(client, provider string, raw map[string]any, model string, stream bool, sessionID string) (map[string]any, error) {
	if client == provider {
		out := cloneMap(raw)
		out["model"] = model
		if provider == "claude" {
			if _, ok := out["max_tokens"]; !ok {
				out["max_tokens"] = 64000
			}
		}
		if provider == translate.FormatResponses {
			out["stream"] = true
		}
		return out, nil
	}
	// Responses requests pivot through Chat Completions like every other source.
	if client == translate.FormatResponses && provider != translate.FormatResponses {
		raw = translate.ResponsesRequestToChat(raw)
		client = translate.FormatOpenAI
	}
	if provider == translate.FormatResponses && client != translate.FormatResponses {
		casey := cloneMap(raw)
		if client != translate.FormatOpenAI {
			// Normalize to Chat Completions first so a single converter serves every source.
			var err error
			casey, err = translateRequestWithSession(client, translate.FormatOpenAI, raw, model, stream, "")
			if err != nil {
				return nil, err
			}
		}
		return translate.ChatRequestToResponses(casey, model, true), nil
	}
	switch {
	case client == translate.FormatOpenAI && provider == translate.FormatClaude:
		var in translate.OpenAIRequest
		if err := remarshal(raw, &in); err != nil {
			return nil, err
		}
		out := translate.OpenAIRequestToClaude(&in, model)
		return toMap(out)
	case client == translate.FormatClaude && provider == translate.FormatOpenAI:
		var in translate.ClaudeRequest
		if err := remarshal(raw, &in); err != nil {
			return nil, err
		}
		out := translate.ClaudeRequestToOpenAI(&in, model)
		return toMap(out)
	case client == translate.FormatOpenAI && provider == translate.FormatOpenAI:
		out := cloneMap(raw)
		out["model"] = model
		if stream {
			out["stream"] = true
			out["stream_options"] = map[string]any{"include_usage": true}
		} else {
			delete(out, "stream_options")
		}
		return out, nil
	case provider == "commandcode":
		if client != translate.FormatOpenAI {
			var err error
			raw, err = translateRequestWithSession(client, translate.FormatOpenAI, raw, model, stream, sessionID)
			if err != nil {
				return nil, err
			}
		}
		return commandCodeRequest(raw, model, sessionID, commandCodeWorkingDir()), nil
	case provider == "ollama":
		if client != translate.FormatOpenAI {
			var err error
			raw, err = translateRequestWithSession(client, translate.FormatOpenAI, raw, model, stream, sessionID)
			if err != nil {
				return nil, err
			}
		}
		return translate.ToOllama(raw, model, stream)
	case provider == "cursor":
		// The cursor body is protobuf, not JSON; the request map only carries the
		// source messages through to attempt(), which builds the frame there so
		// the agent/legacy choice can also see the connection.
		if client != translate.FormatOpenAI {
			var err error
			raw, err = translateRequestWithSession(client, translate.FormatOpenAI, raw, model, stream, sessionID)
			if err != nil {
				return nil, err
			}
		}
		return raw, nil
	case provider == translate.FormatGemini:
		if client != translate.FormatOpenAI {
			var err error
			raw, err = translateRequestWithSession(client, translate.FormatOpenAI, raw, model, stream, sessionID)
			if err != nil {
				return nil, err
			}
		}
		return translate.ToGemini(raw, model, stream)
	case provider == "vertex":
		if client != translate.FormatOpenAI {
			var err error
			raw, err = translateRequestWithSession(client, translate.FormatOpenAI, raw, model, stream, sessionID)
			if err != nil {
				return nil, err
			}
		}
		return translate.ToVertex(raw, model, stream)
	}
	return nil, fmt.Errorf("unsupported translation %s → %s", client, provider)
}

func translateNonStream(provider, client string, body []byte) ([]byte, translate.Usage) {
	var u translate.Usage
	if provider == client {
		return body, extractUsage(body, provider)
	}
	var m map[string]any
	json.Unmarshal(body, &m)
	u = extractUsage(body, provider)

	// Provider → Chat Completions.
	switch provider {
	case translate.FormatClaude:
		var cr translate.ClaudeMessageResp
		json.Unmarshal(body, &cr)
		if cr.Usage != nil {
			u.PromptTokens = cr.Usage.InputTokens
			u.CacheRead = cr.Usage.CacheReadInputTokens
			u.CacheCreation = cr.Usage.CacheCreationInputTokens
			u.CompletionTokens = cr.Usage.OutputTokens
		}
		m = translate.ClaudeResponseToOpenAI(&cr)
	case translate.FormatGemini:
		m = translate.GeminiResponseToOpenAI(m)
		u = extractUsage(mustJSON(m), translate.FormatOpenAI)
	case translate.FormatResponses:
		m = translate.ResponsesNonStreamToOpenAI(m)
		u = extractUsage(mustJSON(m), translate.FormatOpenAI)
	case "ollama":
		m = translate.OllamaBodyToOpenAI(m)
		u = extractUsage(mustJSON(m), translate.FormatOpenAI)
	}
	if client == translate.FormatOpenAI {
		return mustJSON(m), u
	}
	// Chat Completions → client.
	switch client {
	case translate.FormatClaude:
		return mustJSON(translate.OpenAIToClaudeResponse(m)), u
	case translate.FormatResponses:
		return mustJSON(translate.OpenAINonStreamToResponses(m)), u
	}
	return body, u
}

func isSSE(body []byte) bool {
	head := bytes.TrimSpace(body)
	return bytes.HasPrefix(head, []byte("data:")) || bytes.HasPrefix(head, []byte("event:"))
}

func extractUsage(body []byte, format string) translate.Usage {
	var u translate.Usage
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return u
	}
	if usage, ok := m["usage"].(map[string]any); ok {
		u.PromptTokens = num(usage["prompt_tokens"]) + num(usage["input_tokens"])
		u.CompletionTokens = num(usage["completion_tokens"]) + num(usage["output_tokens"])
		u.CacheRead = num(usage["cache_read_input_tokens"]) + num(cachedTokens(usage))
		u.CacheCreation = num(usage["cache_creation_input_tokens"])
	}
	return u
}

func cachedTokens(usage map[string]any) any {
	if d, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		return d["cached_tokens"]
	}
	return nil
}

func num(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}

// streamFrame is one normalized upstream SSE frame: raw data plus the
// `event:` name (Responses upstreams set it; other formats do not).
type streamFrame struct {
	event string
	data  []byte
}

// streamTranslate pipes an upstream SSE stream through the format translator.
func streamTranslate(upstream io.Reader, provider, client string, w io.Writer, flusher http.Flusher) (translate.Usage, error) {
	sc := bufio.NewScanner(upstream)
	sc.Buffer(make([]byte, 64*1024), 8<<20)

	var claudeToOpenAI *translate.ClaudeToOpenAI
	var openAIToClaude *translate.OpenAIToClaude
	var openAIToResponses *translate.OpenAIToResponses
	var responsesToOpenAI *translate.ResponsesToOpenAIStream
	var geminiToOpenAI *translate.GeminiToOpenAI
	var ollamaToOpenAI *translate.OllamaToOpenAI
	var ccToOpenAI *translate.CommandCodeToOpenAI
	switch {
	case provider == translate.FormatClaude && client == translate.FormatOpenAI:
		claudeToOpenAI = &translate.ClaudeToOpenAI{}
	case provider == translate.FormatOpenAI && client == translate.FormatClaude:
		openAIToClaude = &translate.OpenAIToClaude{}
	case provider == translate.FormatOpenAI && client == translate.FormatResponses:
		openAIToResponses = &translate.OpenAIToResponses{}
	case provider == translate.FormatResponses && client == translate.FormatOpenAI:
		responsesToOpenAI = &translate.ResponsesToOpenAIStream{}
	case provider == translate.FormatGemini && client == translate.FormatOpenAI:
		geminiToOpenAI = &translate.GeminiToOpenAI{}
	case provider == translate.FormatGemini && client == translate.FormatClaude:
		geminiToOpenAI = &translate.GeminiToOpenAI{}
		openAIToClaude = &translate.OpenAIToClaude{}
	case provider == translate.FormatGemini && client == translate.FormatResponses:
		geminiToOpenAI = &translate.GeminiToOpenAI{}
		openAIToResponses = &translate.OpenAIToResponses{}
	case provider == "commandcode":
		ccToOpenAI = &translate.CommandCodeToOpenAI{}
		switch client {
		case translate.FormatClaude:
			openAIToClaude = &translate.OpenAIToClaude{}
		case translate.FormatResponses:
			openAIToResponses = &translate.OpenAIToResponses{}
		}
	case provider == "ollama":
		ollamaToOpenAI = &translate.OllamaToOpenAI{}
		switch client {
		case translate.FormatClaude:
			openAIToClaude = &translate.OpenAIToClaude{}
		case translate.FormatResponses:
			openAIToResponses = &translate.OpenAIToResponses{}
		}
	}

	var pendingEvent string
	flush := func() translate.Usage {
		return usageOf(claudeToOpenAI, openAIToClaude, geminiToOpenAI, ollamaToOpenAI, ccToOpenAI)
	}
	handle := func(event string, data []byte) {
		if bytes.Equal(data, []byte("[DONE]")) {
			switch {
			case openAIToClaude != nil:
				for _, ev := range openAIToClaude.Finish() {
					writeClaudeEvent(w, ev)
				}
				writeDone(w)
			case openAIToResponses != nil:
				for _, ev := range openAIToResponses.Flush() {
					writeResponsesEvent(w, ev)
				}
				writeDone(w)
			case responsesToOpenAI != nil:
				for _, ch := range responsesToOpenAI.Flush() {
					fmt.Fprintf(w, "data: %s\n\n", mustJSON(ch))
				}
				writeDone(w)
			case geminiToOpenAI != nil:
				for _, ch := range geminiToOpenAI.Flush() {
					emitChat(w, ch, openAIToClaude, openAIToResponses)
				}
				finishClientHop(w, openAIToClaude, openAIToResponses)
				writeDone(w)
			case ccToOpenAI != nil:
				emitChat(w, ccToOpenAI.Flush(), openAIToClaude, openAIToResponses)
				finishClientHop(w, openAIToClaude, openAIToResponses)
				writeDone(w)
			case ollamaToOpenAI != nil:
				for _, ch := range ollamaToOpenAI.Flush() {
					emitChat(w, ch, openAIToClaude, openAIToResponses)
				}
				finishClientHop(w, openAIToClaude, openAIToResponses)
				writeDone(w)
			default:
				writeDone(w)
			}
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		var payload map[string]any
		if json.Unmarshal(data, &payload) != nil {
			return
		}
		if ollamaToOpenAI != nil {
			if ch := ollamaToOpenAI.Transform(payload); ch != nil {
				emitChat(w, ch, openAIToClaude, openAIToResponses)
			}
			return
		}
		if ccToOpenAI != nil {
			if ch := ccToOpenAI.Transform(payload); ch != nil {
				emitChat(w, ch, openAIToClaude, openAIToResponses)
			}
			return
		}
		switch {
		case claudeToOpenAI != nil:
			for _, ch := range claudeToOpenAI.Transform(payload) {
				fmt.Fprintf(w, "data: %s\n\n", mustJSON(ch))
			}
		case openAIToClaude != nil:
			for _, ev := range openAIToClaude.Transform(payload) {
				writeClaudeEvent(w, ev)
			}
		case openAIToResponses != nil:
			for _, ev := range openAIToResponses.Transform(payload) {
				writeResponsesEvent(w, ev)
			}
		case responsesToOpenAI != nil:
			for _, ch := range responsesToOpenAI.Transform(event, payload) {
				fmt.Fprintf(w, "data: %s\n\n", mustJSON(ch))
			}
		case geminiToOpenAI != nil:
			for _, ch := range geminiToOpenAI.Transform(payload) {
				emitChat(w, ch, openAIToClaude, openAIToResponses)
			}
		default:
			w.Write([]byte("data: "))
			w.Write(data)
			w.Write([]byte("\n\n"))
		}
		if flusher != nil {
			flusher.Flush()
		}
	}

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			pendingEvent = ""
			continue
		}
		if bytes.HasPrefix(line, []byte(":")) {
			continue
		}
		if bytes.HasPrefix(line, []byte("event:")) {
			pendingEvent = string(bytes.TrimSpace(line[len("event:"):]))
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			handle(pendingEvent, bytes.TrimSpace(line[len("data:"):]))
		} else if (ollamaToOpenAI != nil || ccToOpenAI != nil) && line[0] == '{' {
			handle("", line)
		}
	}
	if err := sc.Err(); err != nil {
		return flush(), err
	}
	// Upstream closed without [DONE]: finalize whatever the translator holds.
	switch {
	case openAIToClaude != nil:
		for _, ev := range openAIToClaude.Finish() {
			writeClaudeEvent(w, ev)
		}
	case openAIToResponses != nil:
		for _, ev := range openAIToResponses.Flush() {
			writeResponsesEvent(w, ev)
		}
	case responsesToOpenAI != nil:
		for _, ch := range responsesToOpenAI.Flush() {
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(ch))
		}
	case geminiToOpenAI != nil:
		for _, ch := range geminiToOpenAI.Flush() {
			emitChat(w, ch, openAIToClaude, openAIToResponses)
		}
		finishClientHop(w, openAIToClaude, openAIToResponses)
	case ccToOpenAI != nil:
		finishClientHop(w, openAIToClaude, openAIToResponses)
		writeDone(w)
	case ollamaToOpenAI != nil:
		finishClientHop(w, openAIToClaude, openAIToResponses)
		writeDone(w)
	}
	if flusher != nil {
		flusher.Flush()
	}
	return flush(), nil
}

func usageOf(c2o *translate.ClaudeToOpenAI, o2c *translate.OpenAIToClaude, g2o *translate.GeminiToOpenAI, o2o *translate.OllamaToOpenAI, cc *translate.CommandCodeToOpenAI) translate.Usage {
	switch {
	case cc != nil:
		return cc.Usage
	case o2o != nil:
		u := o2o.Usage
		return u
	case g2o != nil:
		u, _ := g2o.UsageTokens()
		return u
	case c2o != nil:
		return c2o.Usage
	case o2c != nil:
		return o2c.Usage
	}
	return translate.Usage{}
}

// emitChat forwards one Chat Completions chunk, optionally through the second
// translation hop (gemini upstream → claude/responses client).
func emitChat(w io.Writer, ch map[string]any, o2c *translate.OpenAIToClaude, o2r *translate.OpenAIToResponses) {
	switch {
	case o2c != nil:
		for _, ev := range o2c.Transform(ch) {
			writeClaudeEvent(w, ev)
		}
	case o2r != nil:
		for _, ev := range o2r.Transform(ch) {
			writeResponsesEvent(w, ev)
		}
	default:
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(ch))
	}
}

// finishClientHop closes the second hop when the gemini upstream ended.
func finishClientHop(w io.Writer, o2c *translate.OpenAIToClaude, o2r *translate.OpenAIToResponses) {
	switch {
	case o2c != nil:
		for _, ev := range o2c.Finish() {
			writeClaudeEvent(w, ev)
		}
	case o2r != nil:
		for _, ev := range o2r.Flush() {
			writeResponsesEvent(w, ev)
		}
	}
}

func writeClaudeEvent(w io.Writer, ev map[string]any) {
	typ, _ := ev["type"].(string)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, mustJSON(ev))
}

func writeDone(w io.Writer) {
	io.WriteString(w, "data: [DONE]\n\n")
}

func writeResponsesEvent(w io.Writer, ev translate.ResponsesEvent) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, mustJSON(ev.Data))
}

func remarshal(raw map[string]any, dst any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func toMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = json.Unmarshal(b, &out)
	return out, err
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// inferProvider mirrors the config-driven prefix rules in
// open-sse/services/model.js (subset used by the four milestone providers).
func (g *Gateway) hasActiveConnection(provider string) bool {
	conns, err := g.store.ListConnections(provider, true)
	return err == nil && len(conns) > 0
}

// codexOnlyModelSlug reports whether a bare id exists only on the Codex
// backend, not the OpenAI API (upstream codexOnlyModelSlug).
func codexOnlyModelSlug(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	if lower == "codex-auto-review" {
		return true
	}
	if len(lower) > 7 && (strings.HasPrefix(lower, "gpt-5.") || strings.HasPrefix(lower, "gpt-6.")) {
		return true
	}
	return strings.HasPrefix(lower, "gpt-6-") || strings.HasPrefix(lower, "gpt-daybreak-") ||
		strings.HasPrefix(lower, "gpt-reserve")
}

// inferProvider resolves a bare model id to the provider that most likely
// serves it, preferring a provider the user actually connected.
func (g *Gateway) inferProvider(model string) string {
	lower := strings.ToLower(strings.TrimSpace(model))
	if codexOnlyModelSlug(lower) {
		return "codex"
	}
	switch {
	case strings.HasPrefix(lower, "claude-"):
		// A Claude OAuth account alone serves bare Claude ids (upstream
		// fallbackToClaudeOAuth).
		if !g.hasActiveConnection("anthropic") && g.hasActiveConnection("claude") {
			return "claude"
		}
		return "anthropic"
	case strings.HasPrefix(lower, "gpt-") && !strings.HasPrefix(lower, "gpt-image"):
		if g.hasActiveConnection("codex") {
			return "codex"
		}
		if g.hasActiveConnection("openai") {
			return "openai"
		}
		return "codex"
	case strings.HasPrefix(lower, "gemini-"):
		return "gemini"
	case strings.HasPrefix(lower, "grok-"):
		return "xai"
	}
	for _, p := range []string{"openai", "anthropic", "deepseek"} {
		if g.hasActiveConnection(p) {
			return p
		}
	}
	return ""
}

// normalizeQuota rewrites MiniMax Code credit refusals to 429 so the account
// loop fails over instead of refresh-retrying a half-spent account. A 403 that
// does not name the balance stays put: that one is a real auth refusal.
var quotaWords = []string{
	"insufficient", "balance", "credit", "quota", "exhaust", "limit",
	"余额", "积分", "额度", "不足", "用完", "上限",
}

func normalizeQuota(provider string, status int, body []byte) (int, []byte) {
	if provider != "minimax-code" && provider != "minimax-code-global" {
		return status, body
	}
	if status != 402 && status != 403 {
		return status, body
	}
	low := strings.ToLower(string(body))
	hit := false
	for _, w := range quotaWords {
		if strings.Contains(low, w) {
			hit = true
			break
		}
	}
	if !hit {
		return status, body
	}
	msg := strings.TrimSpace(string(body))
	var v map[string]any
	if json.Unmarshal(body, &v) == nil {
		if e, ok := v["error"].(map[string]any); ok {
			if m, ok := e["message"].(string); ok && m != "" {
				msg = m
			}
		}
		for _, k := range []string{"message", "msg"} {
			if m, ok := v[k].(string); ok && m != "" {
				msg = m
			}
		}
		if b, ok := v["base_resp"].(map[string]any); ok {
			if m, ok := b["status_msg"].(string); ok && m != "" {
				msg = m
			}
		}
	}
	out, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{
		"type": "rate_limit_error", "message": "usage limit reached: " + msg,
	}})
	return http.StatusTooManyRequests, out
}
