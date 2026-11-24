export type AuthStatus = {
  authenticated: boolean
  requireLogin: boolean
  requireApiKey?: boolean
  authMode?: string
  needsSetup?: boolean
  hasPassword?: boolean
}

export type ApiKey = {
  id: string
  key: string
  name: string
  machineId: string
  isActive: boolean
  createdAt: string
  accessRestricted: boolean
  accessAllow: string[]
}


/** Persona / jailbreak preset (agents.md-style system prompt injection). */
export type JailbreakKeyword = { text: string; mode?: '' | 'partial' | 'regex' }
export type JailbreakPreset = {
  id?: string
  name?: string
  prompt: string
  geminiSafety?: boolean
  source?: string
  kind?: string
  keywords?: JailbreakKeyword[]
  excludes?: JailbreakKeyword[]
  position?: '' | 'system' | 'append'
  mode?: '' | 'chat'
  order?: number
  syncOrigin?: string
  syncPath?: string
  createdAt?: string
  updatedAt?: string
}
export type JailbreakBinding = { enabled: boolean; presetId?: string }
export type JailbreakMatch = { injected: boolean; presetId?: string; presetName?: string; reason: string }
export type JailbreakSyncSource = { repo: string; ref?: string; path?: string; enabled: boolean }


/** Web terminal: one shell command per request. */
export type TerminalInfo = {
  cwd: string
  user: string
  host: string
  shell: string
  remote: boolean
  allowed: boolean
}
export type TerminalResult = { output: string; exitCode: number; cwd: string }

export type ProviderDisplay = {
  name: string
  icon?: string
  color?: string
  textIcon?: string
  website?: string
  /** Registry display.notice: header link + info banner. */
  notice?: { apiKeyUrl?: string; signupUrl?: string; text?: string }
  /** Per-media-kind warning banner, keyed by serviceKind. */
  kindNotice?: Record<string, string>
  deprecated?: boolean
  deprecationNotice?: string
}

export type ProviderSummary = {
  id: string
  alias?: string
  category: string
  authType: string
  hasOAuth: boolean
  display: ProviderDisplay
  modelsCount: number
  connections: number
  activeConnections: number
  /** providerMedia serviceKinds; ["llm"] when the provider has no media entry. */
  serviceKinds: string[]
  /** True for providers that need no stored credential (free endpoints). */
  noAuth?: boolean
  /** providerModels table the model picker groups by provider. */
  models?: ProviderModel[]
  /** Registry sort key; the providers grid orders by this (unset sorts last). */
  priority?: number
  /** Every credential kind the provider accepts (oauth, apikey, …). */
  authModes?: string[]
  /** Short model prefix the dashboard shows ("ag/model"). */
  uiAlias?: string
  /** Provider KineRouter warns about before a key is added. */
  deprecated?: boolean
  /** Media-only registry entry kept out of the providers grid. */
  hidden?: boolean
}

export type ProviderTestMode = 'provider' | 'oauth' | 'free' | 'apikey' | 'compatible' | 'all'

export type ProviderTestResult = {
  provider: string
  connectionId: string
  connectionName: string
  authType: string
  valid: boolean
  latencyMs: number
  testedAt: string
  error?: string
  diagnosis?: { type?: string }
}

export type ProviderTestResults = {
  mode: ProviderTestMode
  providerId?: string | null
  results: ProviderTestResult[]
  testedAt: string
  summary: { total: number; passed: number; failed: number }
  error?: string
}

export type ModelAvailabilityEntry = {
  provider: string
  model: string
  status: 'cooldown' | 'unavailable' | 'available' | 'unknown'
  until?: string
  connectionId: string
  connectionName: string
  lastError?: string | null
  lastErrorAt?: string | null
}

export type ModelAvailability = { models: ModelAvailabilityEntry[]; unavailableCount: number }

export type CliToolStatus = {
  installed: boolean
  hasKineRouter?: boolean
  settings?: unknown
  config?: unknown
  message?: string
  settingsPath?: string
  configPath?: string
  [extra: string]: unknown
}

export type MitmStatus = {
  running: boolean
  pid: number | null
  certExists: boolean
  certTrusted: boolean
  dnsStatus: Record<string, boolean>
  hasCachedPassword: boolean
  isWin: boolean
  needsSudoPassword: boolean
  isAdmin: boolean
  mitmRouterBaseUrl: string
}

export type TunnelStatus = {
  tunnel: {
    enabled: boolean
    settingsEnabled: boolean
    tunnelUrl: string
    shortId: string
    publicUrl: string
    running: boolean
  }
  tailscale: {
    enabled: boolean
    settingsEnabled: boolean
    tunnelUrl: string
    running: boolean
    loggedIn: boolean
  }
  download: { downloading: boolean; progress: number }
}

export type TailscaleCheck = {
  installed: boolean
  loggedIn: boolean
  platform: string
  brewAvailable: boolean
  daemonRunning: boolean
  customDaemonRunning: boolean
  systemDaemonRunning: boolean
  hasCachedPassword: boolean
}

/** /api/tunnel/tailscale-enable: success, or the next step the user must take. */
export type TailscaleEnableResult = {
  success?: boolean
  tunnelUrl?: string
  needsLogin?: boolean
  authUrl?: string
  funnelNotEnabled?: boolean
  enableUrl?: string
  error?: string
}

export type ProviderModel = {
  id: string
  name?: string
  upstreamModelId?: string
  kind?: string
  capabilities?: string[]
  contextLength?: number
  maxOutputTokens?: number
}

export type Connection = {
  id: string
  provider: string
  authType: string
  name?: string
  email?: string
  priority?: number
  isActive: boolean
  createdAt: string
  updatedAt: string
  [extra: string]: unknown
}

export type ProviderNode = {
  id: string
  type?: string
  name?: string
  createdAt: string
  updatedAt: string
  [extra: string]: unknown
}

export type ProviderDetail = {
  id: string
  alias?: string
  category: string
  authType: string
  display: ProviderDisplay
  models: ProviderModel[]
  connections: Connection[]
  /** Model prefix shown in the dashboard ("ag/model" for antigravity). */
  uiAlias?: string
  authModes?: string[]
  authHint?: string
  noAuth?: boolean
  hasOAuth?: boolean
  transport?: { baseUrl?: string; headers?: Record<string, string> }
  /** Present only for custom compatible nodes; replaces the registry fields. */
  node?: ProviderNode
  /** Per-model thinking levels and capability badges (registry providers). */
  modelMeta?: Record<
    string,
    { thinkingLevels?: string[]; caps?: { vision?: boolean; reasoning?: boolean; contextWindow?: number; maxOutput?: number } }
  >
  /** Public catalogue the dashboard offers as suggested model chips. */
  modelsFetcher?: { url?: string; type?: string; [extra: string]: unknown }
  /** Non-API-key credential form, e.g. "aws" for bedrock. */
  credentialForm?: string
  /** Cluster choices a key is bound to (xiaomi-*). */
  regions?: Array<{ id: string; label: string }>
  defaultRegion?: string
  /** providerMedia config blocks the media detail page's info card renders. */
  searchConfig?: MediaKindConfig
  fetchConfig?: MediaKindConfig
  ttsConfig?: MediaKindConfig
  sttConfig?: MediaKindConfig
  embeddingConfig?: MediaKindConfig
  imageConfig?: MediaKindConfig
  videoConfig?: MediaKindConfig
  systemoneConfig?: MediaKindConfig
  searchViaChat?: MediaKindConfig
}

export type MediaKindConfig = {
  mode?: string
  baseUrl?: string
  method?: string
  defaultModel?: string
  costPerQuery?: number
  pricingUrl?: string
  freeTier?: string
  freeMonthlyQuota?: number
  searchTypes?: string[]
  formats?: string[]
  maxCharacters?: number
  defaultMaxResults?: number
  maxMaxResults?: number
  [extra: string]: unknown
}

export type ProxyPool = {
  id: string
  name?: string
  isActive?: boolean
  testStatus?: string
  [extra: string]: unknown
}

export type OAuthStart = {
  authorizeUrl: string
  state: string
  redirectUri: string
  [extra: string]: unknown
}

export type OAuthDeviceStart = {
  state: string
  provider: string
  userCode: string
  verificationUri: string
  verificationUriComplete?: string
  expiresIn: number
  interval: number
}

export type OAuthPoll = {
  status: 'pending' | 'connected'
  connection?: Connection
  error?: string
}

export type ProviderOverride = {
  headers: Record<string, string>
  builtinHeaders: Record<string, string>
}

export type Combo = {
  id: string
  name: string
  kind?: string
  models: string[]
  createdAt: string
  updatedAt: string
}

/** A `codex -p <name>` profile file (~/.codex/<name>.config.toml). */
export type CodexProfile = { name: string; model: string; command: string }

/** One Anthropic MCP registry entry behind the Cowork marketplace. */
export type McpRegistryServer = {
  name: string
  slug: string
  title: string
  description: string
  url: string
  transport: string
  oauth: boolean
  toolNames: string[]
  toolCount: number
  iconUrl?: unknown
}

/** One row of the usage tables (usageRepo.getUsageStats). */
export type UsageStat = {
  requests: number
  promptTokens: number
  completionTokens: number
  cachedTokens: number
  cost: number
  rawModel?: string
  provider?: string
  connectionId?: string
  accountName?: string
  apiKeyMasked?: string | null
  keyName?: string
  apiKeyKey?: string
  endpoint?: string
  lastUsed?: string
}

export type ActiveRequest = { model: string; provider: string; account: string; count: number }

export type RecentRequest = {
  timestamp: string
  model: string
  provider: string
  promptTokens: number
  completionTokens: number
  cachedTokens: number
  status: string
}

export type UsageStats = {
  period: string
  totalRequests: number
  totalPromptTokens: number
  totalCompletionTokens: number
  totalCachedTokens: number
  totalCost: number
  byProvider: Record<string, UsageStat>
  byModel: Record<string, UsageStat>
  byAccount: Record<string, UsageStat>
  byApiKey: Record<string, UsageStat>
  byEndpoint: Record<string, UsageStat>
  /** Live in-flight counters; only the stream refreshes these after load. */
  pending?: { byModel: Record<string, number>; byAccount: Record<string, Record<string, number>> }
  activeRequests: ActiveRequest[]
  recentRequests: RecentRequest[]
  errorProvider: string
}

export type UsageChartPoint = { label: string; tokens: number; cost: number; requests: number }

export type RequestDetail = {
  id: string
  timestamp: string
  provider: string
  model: string
  connectionId: string
  status: string
  latency?: { ttft?: number; total?: number }
  tokens?: {
    prompt_tokens?: number
    completion_tokens?: number
    input_tokens?: number
    output_tokens?: number
    cached_tokens?: number
    cache_read_input_tokens?: number
    cache_creation_input_tokens?: number
  }
  /** Redacted to {redacted:true} by the API; bodies never leave the server. */
  request?: unknown
  providerRequest?: unknown
  providerResponse?: unknown
  response?: { content?: string; thinking?: string; error?: string }
  pxpipe?: {
    applied: boolean
    reason?: string
    detail?: string
    tokensBeforeEst?: number
    tokensAfterEst?: number
    savedPct?: number
    imageCount?: number
    durationMs?: number
  }
}

export type RequestDetailsPagination = {
  page: number
  pageSize: number
  totalItems: number
  totalPages: number
  hasNext: boolean
  hasPrev: boolean
}

export type RequestDetailsPage = {
  details: RequestDetail[]
  pagination: RequestDetailsPagination
}

export type Settings = Record<string, unknown> & {
  requireLogin?: boolean
  requireApiKey?: boolean
  comboStrategy?: string
  fallbackStrategy?: string
  stickyRoundRobinLimit?: number
  comboStickyRoundRobinLimit?: number
  enableObservability?: boolean
  hasPassword?: boolean
  oidcConfigured?: boolean
  authMode?: string
  ssoType?: string
  oidcIssuerUrl?: string
  oidcClientId?: string
  oidcScopes?: string
  oidcLoginLabel?: string
  samlEntryPoint?: string
  samlIssuer?: string
  samlCert?: string
  samlLoginLabel?: string
  samlAttributeEmail?: string
  samlAttributeName?: string
  outboundProxyEnabled?: boolean
  outboundProxyUrl?: string
  outboundNoProxy?: string
}

export type ModelEntry = {
  id: string
  object: string
  owned_by: string
  /** /api/models attaches capability flags for the composer's badges. */
  caps?: { vision?: boolean; reasoning?: boolean; contextWindow?: number; maxOutput?: number }
}

export type CustomModel = {
  providerAlias: string
  id: string
  type?: string
  name?: string
  caps?: Record<string, unknown>
  transport?: string
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    ...init,
    headers: {
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...(init?.headers ?? {}),
    },
  })
  const text = await res.text()
  let body: unknown = null
  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      body = text
    }
  }
  if (!res.ok) {
    const message =
      (body as { error?: { message?: string } | string })?.error &&
      typeof (body as { error: unknown }).error === 'object'
        ? ((body as { error: { message?: string } }).error.message ?? res.statusText)
        : typeof (body as { error?: string })?.error === 'string'
          ? (body as { error: string }).error
          : res.statusText
    throw new ApiError(res.status, message || `HTTP ${res.status}`)
  }
  return body as T
}

/** One sponsored creative served by an ad-funded provider. */
export type SponsoredAd = {
  adId: string
  clickUrl?: string
  source?: string
  creative: {
    title?: string
    body?: string
    cta?: string
    imageUrl?: string
    sponsorName?: string
    brandColor?: string
  }
}

const json = (method: string, data: unknown): RequestInit => ({
  method,
  body: JSON.stringify(data),
})

export const api = {
  authStatus: () => request<AuthStatus>('/api/auth/status'),
  login: (password: string) => request<{ ok: boolean }>('/api/auth/login', json('POST', { password })),
  logout: () => request<{ ok: boolean }>('/api/auth/logout', { method: 'POST' }),

  /** Web terminal on the gateway host. */
  terminalInfo: () => request<TerminalInfo>('/api/terminal'),
  terminalExec: (command: string, cwd: string) =>
    request<TerminalResult>('/api/terminal/exec', json('POST', { command, cwd })),
  /** Persona / jailbreak presets, bindings and keyword rules. */
  jailbreakPresets: () => request<JailbreakPreset[]>('/api/jailbreak/presets'),
  jailbreakSavePreset: (preset: JailbreakPreset) =>
    request<{ ok: boolean; preset: JailbreakPreset }>('/api/jailbreak/presets', json('PUT', preset)),
  jailbreakDeletePreset: (id: string) =>
    request<{ ok: boolean }>(`/api/jailbreak/presets/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  jailbreakDuplicatePreset: (id: string) =>
    request<{ ok: boolean; preset: JailbreakPreset }>(
      `/api/jailbreak/presets/${encodeURIComponent(id)}/duplicate`,
      json('POST', {}),
    ),
  jailbreakReorder: (order: string[]) =>
    request<{ ok: boolean }>('/api/jailbreak/presets/order', json('PUT', { order })),
  jailbreakUpload: (preset: { name: string; content: string; kind?: string }) =>
    request<{ ok: boolean; preset: JailbreakPreset }>('/api/jailbreak/presets/upload', json('POST', preset)),
  jailbreakBindings: () => request<Record<string, JailbreakBinding>>('/api/jailbreak/models'),
  jailbreakModelCatalog: () => request<{ models: string[] }>('/api/jailbreak/models/catalog'),
  jailbreakSetModel: (data: { id: string; enabled: boolean; presetId: string }) =>
    request<{ ok: boolean }>('/api/jailbreak/models', json('PUT', data)),
  jailbreakDeleteModel: (id: string) =>
    request<{ ok: boolean }>('/api/jailbreak/models', json('DELETE', { id })),
  jailbreakKeyBindings: (keyId: string) =>
    request<Record<string, JailbreakBinding>>(
      `/api/jailbreak/keys/bindings?keyId=${encodeURIComponent(keyId)}`,
    ),
  jailbreakSetKeyModel: (data: { keyId: string; id: string; enabled: boolean; presetId: string }) =>
    request<{ ok: boolean }>('/api/jailbreak/keys/model', json('PUT', data)),
  jailbreakExcludes: () => request<{ excludes: JailbreakKeyword[] }>('/api/jailbreak/excludes'),
  jailbreakSaveExcludes: (excludes: JailbreakKeyword[]) =>
    request<{ ok: boolean; excludes: JailbreakKeyword[] }>('/api/jailbreak/excludes', json('PUT', { excludes })),
  jailbreakImport: (data: { url?: string; name?: string; text?: string; kind?: string }) =>
    request<{ ok: boolean; imported: number; repo?: string }>('/api/jailbreak/import', json('POST', data)),
  jailbreakResolve: (id: string, apiKeyId?: string) =>
    request<JailbreakMatch>('/api/jailbreak/resolve', json('POST', { id, apiKeyId })),
  jailbreakUsage: () => request<{ usage: Record<string, number> }>('/api/jailbreak/usage'),
  jailbreakPreview: (id: string, messages: { role: string; content: string }[], apiKeyId?: string) =>
    request<{ injected: boolean; reason: string; messages: unknown[]; presetId?: string; presetName?: string }>(
      '/api/jailbreak/preview',
      json('POST', { id, messages, apiKeyId }),
    ),
  jailbreakGithub: () => request<{ hasToken: boolean; apiBase: string }>('/api/jailbreak/github'),
  jailbreakSaveGithub: (data: { token?: string; apiBase?: string }) =>
    request<{ ok: boolean; hasToken: boolean }>('/api/jailbreak/github/token', json('PUT', data)),
  jailbreakSources: () => request<{ sources: JailbreakSyncSource[] }>('/api/jailbreak/sync/sources'),
  jailbreakSaveSources: (sources: JailbreakSyncSource[]) =>
    request<{ ok: boolean; sources: JailbreakSyncSource[] }>('/api/jailbreak/sync/sources', json('PUT', { sources })),
  jailbreakSyncRun: () =>
    request<{ ok: boolean; results: { source: string; imported: number; removed: number; error?: string }[] }>(
      '/api/jailbreak/sync/run',
      json('POST', {}),
    ),
  keys: () => request<{ keys: ApiKey[]; machineId: string }>('/api/keys'),
  createKey: (name: string) => request<ApiKey>('/api/keys', json('POST', { name })),
  // `access` is the reference route's shape (`{restricted, allow}`); the model
  // ACL fields on ApiKey are what the server returns.
  updateKey: (id: string, patch: Partial<ApiKey> | { access: { restricted: boolean; allow: string[] } }) =>
    request<ApiKey>(`/api/keys/${id}`, json('PATCH', patch)),
  deleteKey: (id: string) => request<{ ok: boolean }>(`/api/keys/${id}`, { method: 'DELETE' }),

  // `connections` is served from the same URL for the ported media-provider
  // pages, which filter credentials-free connections by provider id.
  providers: () => request<{ providers: ProviderSummary[]; connections: Connection[] }>('/api/providers'),
  provider: (id: string) => request<ProviderDetail>(`/api/providers/${id}`),
  providerNodes: () => request<{ nodes: ProviderNode[] }>('/api/provider-nodes'),
  createProviderNode: (body: Record<string, unknown>) =>
    request<{ node: ProviderNode }>('/api/provider-nodes', json('POST', body)),
  updateProviderNode: (id: string, body: Record<string, unknown>) =>
    request<{ node: ProviderNode }>(`/api/provider-nodes/${id}`, json('PUT', body)),
  deleteProviderNode: (id: string) =>
    request<{ ok: boolean }>(`/api/provider-nodes/${id}`, { method: 'DELETE' }),

  createConnection: (body: {
    provider: string
    apiKey?: string
    name?: string
    priority?: number
    providerSpecificData?: Record<string, unknown>
    connectionProxyEnabled?: boolean
    connectionProxyUrl?: string
  }) => request<{ connection: Connection }>('/api/connections', json('POST', body)),
  updateConnection: (id: string, body: Record<string, unknown>) =>
    request<{ connection: Connection }>(`/api/connections/${id}`, json('PUT', body)),
  deleteConnection: (id: string) =>
    request<{ ok: boolean }>(`/api/connections/${id}`, { method: 'DELETE' }),

  providerOverrides: (id: string) =>
    request<ProviderOverride>(`/api/providers/${id}/overrides`),
  saveProviderOverrides: (id: string, headers: Record<string, string>) =>
    request<{ headers: Record<string, string> }>(
      `/api/providers/${id}/overrides`,
      json('PUT', { headers }),
    ),
  /** Enumerate models through one connection (node /models or the registry). */
  connectionModels: (connectionId: string) =>
    request<{ models: Array<{ id: string; name?: string }> }>(
      `/api/providers/${connectionId}/models`,
    ),
  testConnection: (connectionId: string) =>
    request<{ valid: boolean; refreshed?: boolean; error?: string }>(
      `/api/providers/${connectionId}/test`,
      { method: 'POST' },
    ),
  testConnectionModels: (connectionId: string) =>
    request<{ results: Array<Record<string, unknown> & { modelId: string }> }>(
      `/api/providers/${connectionId}/test-models`,
      { method: 'POST' },
    ),
  validateProvider: (body: { provider: string; apiKey: string; providerSpecificData?: Record<string, unknown> }) =>
    request<{ valid: boolean; error?: string }>('/api/providers/validate', json('POST', body)),
  suggestedModels: (url: string, type: string) =>
    request<{ data: Array<{ id: string; name?: string; contextLength?: number }> }>(
      `/api/providers/suggested-models?url=${encodeURIComponent(url)}&type=${encodeURIComponent(type)}`,
    ),
  kiloFreeModels: () =>
    request<{ models: Array<{ id: string; name?: string; isFree?: boolean; context_length?: number }> }>(
      '/api/providers/kilo/free-models',
    ),

  proxyPools: () => request<{ proxyPools: ProxyPool[] }>('/api/proxy-pools'),

  testModel: (model: string, kind = 'llm') =>
    request<{ ok: boolean; error?: string }>('/api/models/test', json('POST', { model, kind })),
  addCustomModel: (body: {
    providerAlias: string
    id: string
    type?: string
    name?: string
    caps?: Record<string, unknown>
    transport?: string
  }) => request<{ success: boolean; added: boolean }>('/api/models/custom', json('POST', body)),
  deleteCustomModel: (providerAlias: string, id: string, type = 'llm') =>
    request<{ success: boolean }>(
      `/api/models/custom?providerAlias=${encodeURIComponent(providerAlias)}&id=${encodeURIComponent(id)}&type=${encodeURIComponent(type)}`,
      { method: 'DELETE' },
    ),
  /** Disabled model ids for one provider alias (`ids`), or the global map. */
  disabledFor: (providerAlias: string) =>
    request<{ ids: string[] }>(`/api/models/disabled?providerAlias=${encodeURIComponent(providerAlias)}`),
  disableModels: (providerAlias: string, ids: string[]) =>
    request<{ ok?: boolean; success?: boolean }>(
      '/api/models/disabled',
      json('POST', { providerAlias, ids }),
    ),
  enableModel: (providerAlias: string, id: string) =>
    request<{ ok?: boolean; success?: boolean }>(
      `/api/models/disabled?providerAlias=${encodeURIComponent(providerAlias)}&id=${encodeURIComponent(id)}`,
      { method: 'DELETE' },
    ),

  /** Credential-import lanes that post a provider-native token/PAT. */
  importCredential: (provider: string, body: Record<string, unknown>) =>
    request<{ connection?: Connection; success?: boolean }>(`/api/oauth/${provider}/import`, json('POST', body)),
  importGitlabPAT: (token: string, baseUrl?: string) =>
    request<{ connection?: Connection }>('/api/oauth/gitlab/pat', json('POST', { token, baseUrl })),
  importKiroAPIKey: (apiKey: string, region: string) =>
    request<{ connection?: Connection }>('/api/oauth/kiro/api-key', json('POST', { apiKey, region })),
  importCursor: (accessToken: string, machineId: string) =>
    request<{ connection?: Connection }>('/api/oauth/cursor/import', json('POST', { accessToken, machineId })),
  importZed: (accessToken: string, userId: string, systemId?: string) =>
    request<{ connection?: Connection }>('/api/oauth/zed/import', json('POST', { accessToken, userId, systemId })),
  importXiaomiKey: (body: Record<string, unknown>) =>
    request<{ connection?: Connection }>('/api/oauth/xiaomi-mimo/api-key', json('POST', body)),
  autoImport: (provider: string) =>
    request<Record<string, unknown>>(`/api/oauth/${provider}/auto-import`),
  /** MiniMax Code desktop lease: borrow the running app's sign-in. */
  leaseStatus: (provider: string, dataDir?: string) =>
    request<{ found: boolean; dataDir?: string; status?: string; error?: string; message?: string }>(
      `/api/oauth/${provider}/lease-status${dataDir ? `?data_dir=${encodeURIComponent(dataDir)}` : ''}`,
    ),
  leaseConnect: (provider: string, dataDir?: string) =>
    request<{ success?: boolean; reused?: boolean; connection?: { id: string; provider: string }; error?: string; code?: string }>(
      `/api/oauth/${provider}/lease-connect`,
      json('POST', dataDir ? { dataDir } : {}),
    ),
  oauthProviders: () =>
    request<{ providers: string[]; device: string[] }>('/api/oauth/providers'),
  oauthAuthorize: (provider: string, params: Record<string, string> = {}) =>
    request<OAuthStart>(`/api/oauth/${provider}/authorize?${new URLSearchParams(params)}`),
  oauthExchange: (provider: string, code: string, state: string) =>
    request<Connection>(`/api/oauth/${provider}/exchange`, json('POST', { code, state })),
  oauthDevice: (provider: string) =>
    request<OAuthDeviceStart>(`/api/oauth/${provider}/device`, { method: 'POST' }),
  oauthPoll: (provider: string, state: string) =>
    request<OAuthPoll>(`/api/oauth/${provider}/poll?state=${encodeURIComponent(state)}`),
  /** Ad-funded providers: banner creative + verified impression report. */
  freePiAd: (connectionId: string) =>
    request<{ ad: SponsoredAd | null }>(`/api/free-pi/ad?connectionId=${encodeURIComponent(connectionId)}`),
  freePiImpression: (connectionId: string, adId: string) =>
    request<{ recorded: boolean }>('/api/free-pi/ad/impression', json('POST', { connectionId, adId })),
  freebuffAd: (connectionId: string) =>
    request<{ ad: SponsoredAd | null }>(`/api/freebuff/ad?connectionId=${encodeURIComponent(connectionId)}`),
  freebuffImpression: (connectionId: string, adId: string) =>
    request<{ recorded: boolean }>('/api/freebuff/ad/impression', json('POST', { connectionId, adId })),
  /** iflow: exchange a browser cookie for an API key and store a connection. */
  iflowCookie: (cookie: string) =>
    request<{ connection?: Connection; keys?: number }>('/api/oauth/iflow/cookie', json('POST', { cookie })),
  /** Bulk account import (codex / grok-cli): accepts account objects or JSON. */
  bulkImport: (provider: 'codex' | 'grok-cli', body: Record<string, unknown>) =>
    request<{ success: number; failed: number; results: Array<Record<string, unknown>> }>(
      `/api/oauth/${provider}/bulk-import`,
      json('POST', body),
    ),
  /** Validate a compatible node's baseUrl/apiKey before saving. */
  validateProviderNode: (body: { baseUrl: string; apiKey: string; type: string; modelId?: string }) =>
    request<{ valid: boolean; error?: string; dimensions?: number; method?: string }>(
      '/api/provider-nodes/validate',
      json('POST', body),
    ),

  /** Probe every connection in an auth group (the providers page's "Test All"). */
  testBatch: (body: { mode: ProviderTestMode; providerId?: string }) =>
    request<ProviderTestResults>('/api/providers/test-batch', json('POST', body)),
  /** Active model cooldowns/locks across every connection. */
  modelsAvailability: () => request<ModelAvailability>('/api/models/availability'),
  clearCooldown: (body: { provider: string; model: string }) =>
    request<{ ok?: boolean }>('/api/models/availability', json('POST', { action: 'clearCooldown', ...body })),

  combos: () => request<{ combos: Combo[] }>('/api/combos'),
  combo: (id: string) => request<Combo>(`/api/combos/${id}`),
  createCombo: (c: Pick<Combo, 'name' | 'models'> & { kind?: string }) =>
    request<Combo>('/api/combos', json('POST', c)),
  updateCombo: (id: string, c: Partial<Combo>) => request<Combo>(`/api/combos/${id}`, json('PUT', c)),
  deleteCombo: (id: string) => request<{ ok: boolean }>(`/api/combos/${id}`, { method: 'DELETE' }),

  settings: () => request<Settings>('/api/settings'),
  patchSettings: (patch: Record<string, unknown>) => request<Settings>('/api/settings', json('PATCH', patch)),

  // Whole-database backup. The password rides a header so it never lands in the
  // URL or in server access logs (mirrors the reference page).
  exportDatabase: (password: string) =>
    request<Record<string, unknown>>('/api/settings/database', {
      headers: { 'x-kine-password': password },
    }),
  importDatabase: (payload: Record<string, unknown>, password: string) =>
    request<{ success: boolean }>('/api/settings/database', json('POST', { ...payload, password })),
  proxyTest: (proxyUrl: string) =>
    request<{ ok: boolean; status?: number; elapsedMs?: number; error?: string }>(
      '/api/settings/proxy-test',
      json('POST', { proxyUrl }),
    ),
  oidcTest: (body: Record<string, unknown>) =>
    request<{
      ok?: boolean; error?: string; issuerUrl?: string
      clientSecretTested?: boolean; clientSecretValid?: boolean; message?: string
    }>('/api/auth/oidc/test', json('POST', body)),
  samlTest: (body: Record<string, unknown>) =>
    request<{ ok?: boolean; error?: string; message?: string }>('/api/auth/saml/test', json('POST', body)),
  shutdown: () => request<{ success: boolean }>('/api/version/shutdown', { method: 'POST' }),
  setLocale: (locale: string) =>
    request<{ success: boolean; locale: string }>('/api/locale', json('POST', { locale })),

  models: () => request<{ object: string; data: ModelEntry[] }>('/api/models'),
  // Picker support: user-added models and per-provider disabled ids.
  customModels: () => request<{ models: CustomModel[] }>('/api/models/custom'),
  disabledModels: () => request<{ disabled: Record<string, string[]> }>('/api/models/disabled'),
  aliases: () => request<{ aliases: Record<string, string> }>('/api/models/alias'),
  setAlias: (alias: string, model: string) =>
    request<{ ok: boolean }>('/api/models/alias', json('PUT', { alias, model })),
  deleteAlias: (alias: string) =>
    request<{ ok: boolean }>(`/api/models/alias?alias=${encodeURIComponent(alias)}`, { method: 'DELETE' }),

  usageStats: (period: string) => request<UsageStats>(`/api/usage/stats?period=${period}`),
  usageChart: (period: string) => request<UsageChartPoint[]>(`/api/usage/chart?period=${period}`),
  /** Pre-formatted request-log lines (`a | b | c`), newest first. */
  usageRequestLogs: () => request<string[]>('/api/usage/request-logs'),
  /** Providers that appear in usage history, with display names. */
  usageProviders: () => request<{ providers: { id: string; name: string }[] }>('/api/usage/providers'),
  requestDetails: (params: Record<string, string>) =>
    request<RequestDetailsPage>(`/api/usage/request-details?${new URLSearchParams(params)}`),

  version: () =>
    request<{
      version: string
      latest: string
      latestVersion: string
      updateAvailable: boolean
      hasUpdate: boolean
      installMethod?: string
      installCommand?: string
      releaseNotes?: string
    }>('/api/version'),

  /** Fresh GitHub check; used when the modal opens. */
  checkVersion: () =>
    request<{
      version: string
      latestVersion: string
      hasUpdate: boolean
      installMethod?: string
      installCommand?: string
      releaseNotes?: string
    }>('/api/version/check'),

  /** Background updater state: opt-in flag, interval, last look. */
  versionStatus: () =>
    request<{
      autoUpdateEnabled?: boolean
      updateInProgress?: boolean
      lastCheckTime?: string
      checkIntervalHours?: number
    }>('/api/version/status'),

  /** Persist the opt-in for unattended updates. */
  setAutoUpdate: (enabled: boolean) =>
    request<{ success: boolean; autoUpdateEnabled?: boolean }>(
      '/api/version/auto-update',
      json('POST', { enabled }),
    ),

  /** Install the newest release for this delivery channel. */
  triggerUpdate: () =>
    request<{ success: boolean; status?: string; version?: string; message?: string }>(
      '/api/version/update',
      { method: 'POST' },
    ),

  // CLI tools: one settings endpoint per tool (see internal/gateway/clitools.go).
  // The body shape differs per tool; pages own it.
  cliStatuses: () => request<Record<string, CliToolStatus>>('/api/cli-tools/all-statuses'),
  cliTool: (tool: string) => request<CliToolStatus>(`/api/cli-tools/${tool}-settings`),
  cliApply: (tool: string, body: Record<string, unknown>) =>
    request<{ message?: string }>(`/api/cli-tools/${tool}-settings`, json('POST', body)),
  cliReset: (tool: string) =>
    request<{ message?: string }>(`/api/cli-tools/${tool}-settings`, { method: 'DELETE' }),

  // Codex CLI named profiles (internal/gateway/clitools.go §codex profiles).
  codexProfiles: () => request<{ profiles: CodexProfile[] }>('/api/cli-tools/codex-profiles'),
  createCodexProfile: (name: string, model: string) =>
    request<{ success: boolean; profile: CodexProfile }>(
      '/api/cli-tools/codex-profiles',
      json('POST', { name, model }),
    ),
  deleteCodexProfile: (name: string) =>
    request<{ success: boolean }>('/api/cli-tools/codex-profiles', json('DELETE', { name })),

  // Cowork MCP marketplace (internal/gateway/cowork.go).
  mcpRegistry: () => request<{ servers: McpRegistryServer[]; total: number }>('/api/cli-tools/cowork-mcp-registry'),
  mcpTools: (url: string) =>
    request<{ tools: { name: string; description?: string }[]; requiresAuth?: boolean; error?: string }>(
      '/api/cli-tools/cowork-mcp-tools',
      json('POST', { url }),
    ),

  // Antigravity MITM server control (internal/gateway/mitm.go).
  mitmStatus: () => request<MitmStatus>('/api/cli-tools/antigravity-mitm'),
  mitmStart: (body: { apiKey: string; sudoPassword?: string; mitmRouterBaseUrl?: string; forceKillPort443?: boolean }) =>
    request<{ success: boolean; running: boolean; pid: number }>(
      '/api/cli-tools/antigravity-mitm',
      json('POST', body),
    ),
  mitmStop: (sudoPassword?: string) =>
    request<{ success: boolean; running: boolean }>('/api/cli-tools/antigravity-mitm', {
      method: 'DELETE',
      ...(sudoPassword ? { body: JSON.stringify({ sudoPassword }) } : {}),
    }),
  mitmAction: (tool: string, action: 'enable' | 'disable' | 'trust-cert', sudoPassword?: string) =>
    request<{ success: boolean; dnsStatus?: Record<string, boolean>; certTrusted?: boolean }>(
      '/api/cli-tools/antigravity-mitm',
      json('PATCH', { tool, action, sudoPassword }),
    ),
  mitmAliases: () => request<{ aliases: Record<string, Record<string, string>> }>(
    '/api/cli-tools/antigravity-mitm/alias',
  ),
  mitmSetAliases: (tool: string, mappings: Record<string, string>) =>
    request<{ success: boolean; aliases: Record<string, string> }>(
      '/api/cli-tools/antigravity-mitm/alias',
      json('PUT', { tool, mappings }),
    ),

  // Public exposure (internal/gateway/tunnel.go).
  tunnelStatus: () => request<TunnelStatus>('/api/tunnel/status'),
  tunnelEnable: () =>
    request<{ success: boolean; tunnelUrl: string; shortId: string; publicUrl: string; alreadyRunning?: boolean }>(
      '/api/tunnel/enable',
      { method: 'POST' },
    ),
  tunnelDisable: () => request<{ success: boolean }>('/api/tunnel/disable', { method: 'POST' }),
  tailscaleCheck: () => request<TailscaleCheck>('/api/tunnel/tailscale-check'),
  tailscaleEnable: () =>
    request<TailscaleEnableResult>('/api/tunnel/tailscale-enable', { method: 'POST' }),
  tailscaleDisable: () => request<{ success: boolean }>('/api/tunnel/tailscale-disable', { method: 'POST' }),
  /** Streams SSE `progress` / `done` / `error` events; resolves with the done payload. */
  tailscaleInstall: async (
    sudoPassword: string,
    onProgress?: (message: string) => void,
  ): Promise<{ authUrl?: string | null }> => {
    const res = await fetch('/api/tunnel/tailscale-install', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sudoPassword }),
    })
    if (!res.ok || !res.body) {
      const text = await res.text()
      let message = res.statusText
      try {
        message = (JSON.parse(text) as { error?: string }).error ?? message
      } catch { /* not JSON */ }
      throw new ApiError(res.status, message || `HTTP ${res.status}`)
    }
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    let done: { authUrl?: string | null } = {}
    for (;;) {
      const { done: end, value } = await reader.read()
      if (end) break
      buffer += decoder.decode(value, { stream: true })
      const frames = buffer.split('\n\n')
      buffer = frames.pop() || ''
      for (const frame of frames) {
        let event = 'progress'
        let data: Record<string, unknown> | null = null
        for (const line of frame.split('\n')) {
          if (line.startsWith('event: ')) event = line.slice(7).trim()
          else if (line.startsWith('data: ')) {
            try { data = JSON.parse(line.slice(6)) as Record<string, unknown> } catch { /* skip */ }
          }
        }
        if (!data) continue
        if (event === 'progress') onProgress?.(String(data.message ?? ''))
        else if (event === 'done') done = { authUrl: (data.authUrl as string | null) ?? null }
        else if (event === 'error') throw new ApiError(500, String(data.error ?? 'Install failed'))
      }
    }
    return done
  },
}
