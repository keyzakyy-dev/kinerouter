// Package providerdata embeds the provider registry. The JSON beside this file
// is the source of truth: edit it and rebuild.
package providerdata

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed registry.json
var raw []byte

type AuthSpec struct {
	Combined bool      `json:"combined"`
	Header   string    `json:"header"`
	Scheme   string    `json:"scheme"`
	APIKey   *AuthSpec `json:"apiKey,omitempty"`
	OAuth    *AuthSpec `json:"oauth,omitempty"`
	// Hooks names header quirks the executor applies after auth (registry
	// `auth.hooks`: kimiHeaders, clineHeaders, museHeaders, ...).
	Hooks []string `json:"hooks,omitempty"`
}

type Transport struct {
	BaseURL        string            `json:"baseUrl"`
	BaseURLs       []string          `json:"baseUrls"`
	Format         string            `json:"format"`
	Headers        map[string]string `json:"headers"`
	Auth           *AuthSpec         `json:"auth"`
	URLSuffix      string            `json:"urlSuffix"`
	ForceStream    bool              `json:"forceStream"`
	ThinkingFormat string            `json:"thinkingFormat"`
	NoAuth         bool              `json:"noAuth"`
	Retry          map[string]Retry  `json:"retry"`
	Transports     []Transport       `json:"transports"`
	Quirks         map[string]any    `json:"quirks"`
	CLIVersion     string            `json:"cliVersion"`
	APIClient      string            `json:"apiClient"`
	// OAuth client pair when the registry keeps it on the transport instead of
	// the oauth block (gemini-cli, antigravity).
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	// Usage holds the registry `usage` block (quota/usage endpoint URLs).
	Usage map[string]any `json:"usage,omitempty"`
}

// Retry is heterogeneous upstream (an object for most providers, a bare int
// for kiro), so it decodes to raw JSON.
type Retry = json.RawMessage

// OAuth keeps the fields the runtime authorize/refresh paths need and stashes
// every other provider-specific key in Extra (registry entries vary widely:
// device-code URLs, RSA settings, CLI endpoints, ...).
type OAuth struct {
	ClientID            string          `json:"clientId"`
	ClientSecret        string          `json:"clientSecret"`
	AuthorizeURL        string          `json:"authorizeUrl"`
	TokenURL            string          `json:"tokenUrl"`
	DeviceCodeURL       string          `json:"deviceCodeUrl"`
	DeviceURL           string          `json:"deviceUrl"`
	UserInfoURL         string          `json:"userInfoUrl"`
	RefreshURL          string          `json:"refreshUrl"`
	Scopes              json.RawMessage `json:"scopes"` // string or []string
	Scope               string          `json:"scope"`
	CodeChallengeMethod string          `json:"codeChallengeMethod"`
	ExtraParams         map[string]any  `json:"extraParams"`
	RefreshLeadMs       int64           `json:"refreshLeadMs"`
	Refresh             map[string]any  `json:"refresh"`
	Extra               map[string]any  `json:"-"`
}

func (o *OAuth) UnmarshalJSON(b []byte) error {
	type plain OAuth
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	var all map[string]any
	json.Unmarshal(b, &all)
	delete(all, "scopes")
	delete(all, "refresh")
	delete(all, "extraParams")
	*o = OAuth(p)
	o.Extra = all
	return nil
}

// ScopeList normalizes the scopes field to a slice.
func (o *OAuth) ScopeList() []string {
	if o == nil || len(o.Scopes) == 0 {
		return nil
	}
	var list []string
	if json.Unmarshal(o.Scopes, &list) == nil {
		return list
	}
	var single string
	if json.Unmarshal(o.Scopes, &single) == nil && single != "" {
		return []string{single}
	}
	return nil
}

type Model struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	UpstreamModelID string         `json:"upstreamModelId"`
	Kind            string         `json:"kind"`
	Capabilities    []string       `json:"capabilities"`
	ContextLength   int64          `json:"contextLength,omitempty"`
	MaxOutputTokens int64          `json:"maxOutputTokens,omitempty"`
	Transport       string         `json:"transport,omitempty"`
	TargetFormat    string         `json:"targetFormat,omitempty"`
	Dimensions      int            `json:"dimensions,omitempty"`
	Extra           map[string]any `json:"-"`
}

func (m *Model) UnmarshalJSON(b []byte) error {
	type plain Model
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	var all map[string]any
	json.Unmarshal(b, &all)
	for _, k := range []string{"id", "name", "upstreamModelId", "kind", "capabilities",
		"contextLength", "maxOutputTokens", "transport", "targetFormat", "dimensions"} {
		delete(all, k)
	}
	*m = Model(p)
	m.Extra = all
	return nil
}

type Entry struct {
	ID        string     `json:"id"`
	Alias     string     `json:"alias"`
	Aliases   []string   `json:"aliases"`
	Category  string     `json:"category"`
	AuthType  string     `json:"authType"`
	Transport *Transport `json:"transport"`
	// Transports is the multi-endpoint table (format → URL/auth): the transport
	// whose format matches the client source format is used directly, skipping
	// translation. Transport stays the provider default.
	Transports []*Transport `json:"transports,omitempty"`
	OAuth      *OAuth       `json:"oauth"`
	Models     []Model      `json:"models"`
	NoAuth     bool         `json:"noAuth"`
	Display    Display      `json:"display"`
	// Features is the registry `features` map (usage, usageApikey, ...).
	Features map[string]any `json:"features,omitempty"`
	// UIAlias is the short prefix the dashboard shows for a model
	// ("ag/model" rather than "antigravity/model").
	UIAlias string `json:"uiAlias,omitempty"`
	// AuthModes lists every credential kind a provider accepts; AuthHint is the
	// per-provider note shown above the credential field.
	AuthModes []string `json:"authModes,omitempty"`
	AuthHint  string   `json:"authHint,omitempty"`
	// ModelsFetcher is the public catalogue the dashboard offers as
	// "suggested models" ({url, type}); proxied through /api/providers/
	// suggested-models so the browser never dials the provider itself.
	ModelsFetcher map[string]any `json:"modelsFetcher,omitempty"`
	// CredentialForm / AuthType carry the registry's non-key credential kinds
	// ("aws", "cookie") so the add-connection form can label the field.
	CredentialForm string `json:"credentialForm,omitempty"`
	// Priority orders the providers list (the registry sort key); 0 means
	// unset and sorts after every listed priority.
	Priority int `json:"priority,omitempty"`
	// Hidden keeps a media-only entry out of the providers grid, as the upstream
	// registry's `info.hidden` does.
	Hidden bool `json:"hidden,omitempty"`
	// Regions are the cluster choices a key is bound to (xiaomi-*), shown as a
	// select in the add-connection form; DefaultRegion preselects one.
	Regions       []Region `json:"regions,omitempty"`
	DefaultRegion string   `json:"defaultRegion,omitempty"`
}

// Region is a registry select option ({id,label}).
type Region struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// UsageURL reads one URL out of the registry usage block.
func (e *Entry) UsageURL(key string) string {
	if e == nil || e.Transport == nil || e.Transport.Usage == nil {
		return ""
	}
	s, _ := e.Transport.Usage[key].(string)
	return s
}

// UsageApikey reports whether the registry allows an api-key connection to
// query usage (USAGE_APIKEY_PROVIDERS in the JS app).
func (e *Entry) UsageApikey() bool {
	v, _ := e.Features["usageApikey"].(bool)
	return v
}

// UsageSupported reports whether the provider has a quota endpoint
// (USAGE_SUPPORTED_PROVIDERS in the JS app).
func (e *Entry) UsageSupported() bool {
	v, _ := e.Features["usage"].(bool)
	return v
}

type Display struct {
	Name     string `json:"name"`
	Icon     string `json:"icon"`
	Color    string `json:"color"`
	TextIcon string `json:"textIcon"`
	Website  string `json:"website"`
	// Notice is the registry `display.notice` block: apiKeyUrl, signupUrl and
	// an optional free-text note, rendered as the header link and info banner.
	Notice map[string]any `json:"notice,omitempty"`
	// Deprecated marks a provider the dashboard warns about before a connection is
	// added; DeprecationNotice is the risk text shown in the banner and modal.
	Deprecated        bool           `json:"deprecated,omitempty"`
	DeprecationNotice string         `json:"deprecationNotice,omitempty"`
	KindNotice        map[string]any `json:"kindNotice,omitempty"`
}

type Registry struct {
	Media   map[string]map[string]any // providerMedia: embedding/tts/stt/image configs
	Entries []Entry
	ByID    map[string]*Entry
	AliasTo map[string]string  // alias token → provider id
	Models  map[string][]Model // provider alias → models (providerModels table)
	OAuth   map[string]*OAuth
}

var (
	once sync.Once
	reg  *Registry
)

// RiskNotice is the registry sentinel; RiskNoticeText is what the UI shows,
// copied verbatim from the upstream dashboard's provider display table.
const (
	RiskNotice     = "RISK_NOTICE"
	RiskNoticeText = "Risk Notice: This provider uses a subscription/OAuth session not officially licensed for proxy/router use. Account may be restricted or banned. Use at your own risk."
)

type file struct {
	Registry       []Entry                   `json:"registry"`
	Providers      map[string]Transport      `json:"providers"`
	ProviderModels map[string][]Model        `json:"providerModels"`
	ProviderOAuth  map[string]OAuth          `json:"providerOAuth"`
	ProviderMedia  map[string]map[string]any `json:"providerMedia"`
}

// Get returns the parsed registry (sync.Once — lazily initialized).
func Get() *Registry {
	once.Do(func() {
		var f file
		if err := json.Unmarshal(raw, &f); err != nil {
			panic("providerdata: " + err.Error())
		}
		r := &Registry{
			Media:   f.ProviderMedia,
			Entries: f.Registry,
			ByID:    map[string]*Entry{},
			AliasTo: map[string]string{},
			Models:  f.ProviderModels,
			OAuth:   map[string]*OAuth{},
		}
		for i := range r.Entries {
			e := &r.Entries[i]
			if t, ok := f.Providers[e.ID]; ok && e.Transport == nil {
				e.Transport = &t
			}
			if o, ok := f.ProviderOAuth[e.ID]; ok {
				c := o
				r.OAuth[e.ID] = &c
				if e.OAuth == nil {
					e.OAuth = &c
				}
			}
			r.ByID[e.ID] = e
			r.AliasTo[e.ID] = e.ID
			if e.Alias != "" {
				r.AliasTo[e.Alias] = e.ID
			}
			for _, a := range e.Aliases {
				r.AliasTo[a] = e.ID
			}
			// Registry stores the sentinel "RISK_NOTICE"; the dashboard expands it to
			// the shared warning text at load (src/shared/constants/providers.js).
			if e.Display.DeprecationNotice == RiskNotice {
				e.Display.DeprecationNotice = RiskNoticeText
			}
			if e.Transport != nil && e.Transport.Format == "" {
				e.Transport.Format = "openai"
			}
		}
		// Media-only aliases kept by the JS engine (not present as registry ids).
		for alias, id := range map[string]string{
			"el": "elevenlabs", "jina": "jina-ai", "jina-ai": "jina-ai",
			"polly": "aws-polly", "aws-polly": "aws-polly",
		} {
			r.AliasTo[alias] = id
		}
		reg = r
	})
	return reg
}

// Media returns the providerMedia entry for a provider id, or nil.
func (r *Registry) MediaFor(id string) map[string]any {
	if m, ok := r.Media[id]; ok {
		return m
	}
	return nil
}

// ResolveAlias mirrors resolveProviderAlias.
func (r *Registry) ResolveAlias(token string) string {
	if id, ok := r.AliasTo[token]; ok {
		return id
	}
	return token
}

// OAuthFor returns the OAuth configuration for a provider id, or nil.
// OAuth configs live in providerOAuth, not inside the per-provider entry, so
// both are consulted.
func (r *Registry) OAuthFor(id string) *OAuth {
	if e, ok := r.ByID[id]; ok && e.OAuth != nil {
		return e.OAuth
	}
	if o, ok := r.OAuth[id]; ok {
		return o
	}
	return nil
}

// ClientCreds resolves the OAuth client pair: the oauth block's own values
// first, else the transport-level ones.
func (r *Registry) ClientCreds(id string) (clientID, clientSecret string) {
	if o := r.OAuthFor(id); o != nil {
		clientID, clientSecret = o.ClientID, o.ClientSecret
	}
	if t := r.TransportFor(id); t != nil {
		if clientID == "" {
			clientID = t.ClientID
		}
		if clientSecret == "" {
			clientSecret = t.ClientSecret
		}
	}
	return
}

// DeviceProviders reports which providers have a device-code endpoint.
func (r *Registry) DeviceProviders() map[string]bool {
	out := map[string]bool{}
	for id, o := range r.OAuth {
		if o.DeviceCodeURL != "" || o.DeviceURL != "" {
			out[id] = true
		}
	}
	return out
}

// TransportFor returns the transport entry for a provider id, or nil.
func (r *Registry) TransportFor(id string) *Transport {
	if e, ok := r.ByID[id]; ok {
		return e.Transport
	}
	return nil
}

// TransportForSource returns the multi-endpoint transport whose format matches
// the client source format, or nil when the provider has no such table and the
// default transport applies (chatCore's sourceFormat-matched transport).
func (r *Registry) TransportForSource(id, sourceFormat string) *Transport {
	e, ok := r.ByID[id]
	if !ok || len(e.Transports) == 0 {
		return nil
	}
	for _, t := range e.Transports {
		if t.Format == sourceFormat {
			return t
		}
	}
	return nil
}

// ModelTargetFormat is the declared upstream format for a model
// (providerModels `targetFormat`), or "" when undeclared.
func (r *Registry) ModelTargetFormat(id, model string) string {
	e, ok := r.ByID[id]
	if !ok {
		return ""
	}
	for _, m := range e.Models {
		if m.ID == model {
			return m.TargetFormat
		}
	}
	return ""
}

// Format returns the upstream wire format for a provider ("openai" default).
func (r *Registry) Format(id string) string {
	if t := r.TransportFor(id); t != nil && t.Format != "" {
		return t.Format
	}
	return "openai"
}

// UpstreamModelID maps a public model id to the id the upstream expects.
func (r *Registry) UpstreamModelID(provider, model string) string {
	e, ok := r.ByID[provider]
	if !ok {
		return model
	}
	for _, m := range e.Models {
		if m.ID == model && m.UpstreamModelID != "" {
			return stripEffortSuffix(m.UpstreamModelID)
		}
	}
	return model
}

// stripEffortSuffix removes the "(high)" / "(medium)" / "(low)" effort marker
// a registry row carries. The tier is request metadata, not part of the wire
// model id; Google answers a suffixed id with 404.
func stripEffortSuffix(model string) string {
	if i := strings.IndexByte(model, '('); i > 0 && strings.HasSuffix(model, ")") {
		return strings.TrimSpace(model[:i])
	}
	return model
}

// IsKnownProvider reports whether a token names a registry provider.
func (r *Registry) IsKnownProvider(id string) bool {
	_, ok := r.ByID[id]
	return ok
}

// ProviderIDFromModel resolves the "provider/model" prefix.
func (r *Registry) ProviderIDFromModel(model string) (provider, rest string, ok bool) {
	i := strings.IndexByte(model, '/')
	if i <= 0 {
		return "", model, false
	}
	return r.ResolveAlias(model[:i]), model[i+1:], true
}
