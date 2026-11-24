import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Search } from 'lucide-react'
import {
  api,
  type Connection,
  type ProviderNode,
  type ProviderSummary,
  type ProviderTestMode,
  type ProviderTestResult,
  type ProviderTestResults,
} from '@/lib/api'
import { PageHeader } from '@/components/layout'
import { ProviderIcon } from '@/components/provider-icon'
import { Icon } from '@/components/icon'
import { AddCompatibleModal } from '@/components/add-compatible-modal'
import { ModelAvailabilityBadge } from '@/components/model-availability-badge'
import { Modal } from '@/components/modal'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { getErrorCode, getRelativeTime } from '@/lib/format'
import { useNotify } from '@/lib/toast'
import { cn } from '@/lib/utils'

/**
 * Providers grid, ported from src/app/(dashboard)/dashboard/providers/page.js:
 * custom compatible nodes, OAuth, Free Tier and API Key sections, each card
 * carrying per-connection status, a bulk on/off toggle and the section's
 * "Test All" batch probe.
 */

const APIKEY_INITIAL_VISIBLE = 20

type Stats = {
  connected: number
  error: number
  total: number
  errorCode: string | null
  errorTime: string | null
  allDisabled: boolean
}

const STATUS_FILTER_OPTIONS = [
  { value: 'all', label: 'All' },
  { value: 'active', label: 'Active' },
  { value: 'inactive', label: 'Inactive' },
  { value: 'none', label: 'No connection' },
] as const

type StatusFilter = (typeof STATUS_FILTER_OPTIONS)[number]['value']

/** The HTTP/auth/billing tag shown next to the error count. */
function connectionErrorTag(c: Connection): string | null {
  const type = c.lastErrorType as string | undefined
  if (type === 'runtime_error') return 'RUNTIME'
  if (
    type === 'upstream_auth_error' ||
    type === 'auth_missing' ||
    type === 'token_refresh_failed' ||
    type === 'token_expired'
  )
    return 'AUTH'
  if (type === 'upstream_rate_limited') return '429'
  if (type === 'upstream_unavailable') return '5XX'
  if (type === 'network_error') return 'NET'

  const numeric = Number(c.errorCode)
  if (Number.isFinite(numeric) && numeric >= 400) return String(numeric)

  const fromMessage = getErrorCode(c.lastError as string | undefined)
  if (fromMessage === '401' || fromMessage === '403') return 'AUTH'
  if (fromMessage && fromMessage !== 'ERR') return fromMessage

  const msg = String(c.lastError ?? '').toLowerCase()
  if (msg.includes('runtime') || msg.includes('not runnable') || msg.includes('not installed')) return 'RUNTIME'
  if (
    msg.includes('invalid api key') ||
    msg.includes('token invalid') ||
    msg.includes('revoked') ||
    msg.includes('unauthorized')
  )
    return 'AUTH'
  return 'ERR'
}

function connectionStatus(conns: Connection[], providerId: string, authTypes: string[]): Stats {
  const picked = conns.filter((c) => c.provider === providerId && authTypes.includes(c.authType))
  // A connection whose models are cooling down is still usable, so an
  // "unavailable" test status with an active model lock counts as connected.
  const effective = (c: Connection) => {
    const locked = Object.entries(c).some(
      ([k, v]) => k.startsWith('modelLock_') && v && new Date(String(v)).getTime() > Date.now(),
    )
    return c.testStatus === 'unavailable' && !locked ? 'active' : (c.testStatus as string | undefined)
  }
  const connected = picked.filter((c) => ['active', 'success'].includes(effective(c) ?? '')).length
  const errors = picked
    .filter((c) => ['error', 'expired', 'unavailable'].includes(effective(c) ?? ''))
    .sort((a, b) => new Date(String(b.lastErrorAt ?? 0)).getTime() - new Date(String(a.lastErrorAt ?? 0)).getTime())
  const latest = errors[0]
  return {
    connected,
    error: errors.length,
    total: picked.length,
    errorCode: latest ? connectionErrorTag(latest) : null,
    errorTime: latest?.lastErrorAt ? getRelativeTime(String(latest.lastErrorAt)) : null,
    allDisabled: picked.length > 0 && picked.every((c) => c.isActive === false),
  }
}

/** kiro accepts oauth + apikey but declares no authModes; free/apikey
 *  providers default to key auth even when the registry omits authModes. */
function dualAuthTypes(p: ProviderSummary, key: string): string | string[] {
  if (key === 'kiro') return ['oauth', 'apikey', 'api_key']
  if (!Array.isArray(p.authModes)) {
    return ['freeTier', 'apikey', 'webCookie'].includes(p.category)
      ? ['oauth', 'apikey', 'api_key']
      : 'oauth'
  }
  if (!p.authModes.includes('apikey')) return 'oauth'
  return ['oauth', 'apikey', 'api_key']
}

function asList(authTypes: string | string[]): string[] {
  return Array.isArray(authTypes) ? authTypes : [authTypes]
}

/** The reference sorts by registry priority first, then connected, then name. */
function byPriority(list: ProviderSummary[], statsOf: (p: ProviderSummary) => Stats): ProviderSummary[] {
  return [...list].sort((a, b) => {
    const pa = a.priority ?? 999
    const pb = b.priority ?? 999
    if (pa !== pb) return pa - pb
    const ca = statsOf(a).connected > 0 ? 0 : 1
    const cb = statsOf(b).connected > 0 ? 0 : 1
    if (ca !== cb) return ca - cb
    return (a.display?.name ?? a.id).localeCompare(b.display?.name ?? b.id)
  })
}

function byConnected(list: ProviderSummary[], statsOf: (p: ProviderSummary) => Stats): ProviderSummary[] {
  return [...list].sort((a, b) => {
    const ca = statsOf(a).total > 0 ? 0 : 1
    const cb = statsOf(b).total > 0 ? 0 : 1
    return ca - cb || (a.display?.name ?? a.id).localeCompare(b.display?.name ?? b.id)
  })
}

function StatusLine({ stats, noAuth }: { stats: Stats; noAuth?: boolean }) {
  const { connected, error, errorCode, errorTime, allDisabled } = stats
  if (allDisabled) {
    return (
      <Badge size="sm">
        <span className="flex items-center gap-1">
          <Icon name="pause_circle" className="text-[12px]" />
          Disabled
        </span>
      </Badge>
    )
  }
  if (noAuth) {
    return (
      <Badge variant="success" size="sm" dot className="bg-emerald-500/12 text-emerald-700 dark:text-emerald-400">
        Ready
      </Badge>
    )
  }
  if (connected === 0 && error === 0) return <span className="text-text-muted">No connections</span>
  return (
    <>
      {connected > 0 && (
        <Badge variant="success" size="sm" dot className="bg-emerald-500/12 text-emerald-700 dark:text-emerald-400">
          {connected} Connected
        </Badge>
      )}
      {error > 0 && (
        <Badge variant="error" size="sm" dot>
          {errorCode ? `${error} Error (${errorCode})` : `${error} Error`}
        </Badge>
      )}
      {errorTime && <span className="text-text-muted">{errorTime}</span>}
    </>
  )
}

function ProviderCard({
  p,
  stats,
  iconSrc,
  extraBadge,
  onToggle,
}: {
  p: ProviderSummary
  stats: Stats
  iconSrc?: string
  extraBadge?: ReactNode
  onToggle: (active: boolean) => void
}) {
  const navigate = useNavigate()
  const name = p.display?.name ?? p.id
  const color = p.display?.color ?? '#888'
  const open = () => navigate(`/dashboard/providers/${encodeURIComponent(p.id)}`)
  return (
    <div
      role="link"
      tabIndex={0}
      onClick={open}
      onKeyDown={(e) => e.key === 'Enter' && open()}
      className={cn(
        'group flex cursor-pointer items-center justify-between gap-3 rounded-lg border border-border bg-card p-3 transition-colors hover:bg-muted/20',
        stats.allDisabled && 'opacity-50',
      )}
    >
      <div className="flex min-w-0 items-center gap-3">
        <div
          className="flex size-8 shrink-0 items-center justify-center overflow-hidden rounded-lg"
          style={{ backgroundColor: `${color}15` }}
        >
          <ProviderIcon
            provider={iconSrc ?? p.id}
            size={30}
            fallbackText={p.display?.textIcon || p.id.slice(0, 2).toUpperCase()}
          />
        </div>
        <div className="min-w-0">
          <h3 className="truncate text-sm font-semibold">{name}</h3>
          <div className="flex min-w-0 flex-wrap items-center gap-1.5 text-xs">
            <StatusLine stats={stats} noAuth={p.noAuth} />
            {!stats.allDisabled && extraBadge}
          </div>
        </div>
      </div>
      {stats.total > 0 && (
        <div
          className="shrink-0 opacity-100 transition-opacity sm:opacity-0 sm:group-hover:opacity-100"
          onClick={(e) => {
            e.stopPropagation()
            onToggle(stats.allDisabled)
          }}
        >
          <Switch checked={!stats.allDisabled} title={stats.allDisabled ? 'Enable provider' : 'Disable provider'} />
        </div>
      )}
    </div>
  )
}

/** Icon ids the reference ships as fixed assets for compatible nodes. */
function nodeIcon(node: ProviderNode): string {
  if (node.type === 'anthropic-compatible') return 'anthropic-m'
  return node.apiType === 'responses' ? 'oai-r' : 'oai-cc'
}

function TestResultsBody({ results }: { results: ProviderTestResults }) {
  if (results.error && !results.results) {
    return (
      <div className="py-6 text-center">
        <Icon name="error" className="mb-2 block text-[32px] text-danger" />
        <p className="text-sm text-danger">{results.error}</p>
      </div>
    )
  }
  const { summary, mode, results: items = [] } = results
  const modeLabel =
    ({ oauth: 'OAuth', free: 'Free', apikey: 'API Key', provider: 'Provider', all: 'All', compatible: 'Compatible' } as Record<
      string,
      string
    >)[mode] ?? mode
  return (
    <div className="flex min-w-0 flex-col gap-3">
      {summary && (
        <div className="mb-1 flex flex-wrap items-center gap-2 text-xs sm:gap-3">
          <span className="text-text-muted">{modeLabel} Test</span>
          <span className="rounded bg-success/15 px-2 py-0.5 font-medium text-success">{summary.passed} passed</span>
          {summary.failed > 0 && (
            <span className="rounded bg-danger/15 px-2 py-0.5 font-medium text-danger">{summary.failed} failed</span>
          )}
          <span className="text-text-muted sm:ml-auto">{summary.total} tested</span>
        </div>
      )}
      {items.map((r: ProviderTestResult, i: number) => (
        <div
          key={r.connectionId || i}
          className="flex min-w-0 flex-wrap items-center gap-2 rounded-lg bg-surface-2 px-3 py-2 text-xs sm:flex-nowrap"
        >
          <Icon name={r.valid ? 'check_circle' : 'error'} className={r.valid ? 'text-[16px] text-success' : 'text-[16px] text-danger'} />
          <div className="min-w-0 flex-[1_1_160px]">
            <span className="block truncate font-medium sm:inline">{r.connectionName}</span>
            <span className="block truncate text-text-muted sm:ml-1.5 sm:inline">({r.provider})</span>
          </div>
          {r.latencyMs !== undefined && (
            <span className="shrink-0 font-mono tabular-nums text-text-muted">{r.latencyMs}ms</span>
          )}
          <span
            className={cn(
              'shrink-0 rounded px-1.5 py-0.5 text-[10px] font-bold uppercase',
              r.valid ? 'bg-success/15 text-success' : 'bg-danger/15 text-danger',
            )}
          >
            {r.valid ? 'OK' : (r.diagnosis?.type ?? 'ERROR')}
          </span>
        </div>
      ))}
      {items.length === 0 && (
        <div className="py-4 text-center text-sm text-text-muted">No active connections found for this group.</div>
      )}
    </div>
  )
}

function TestAllButton({ mode, testing, onClick }: { mode: ProviderTestMode; testing: ProviderTestMode | null; onClick: () => void }) {
  const active = testing === mode
  return (
    <button
      onClick={onClick}
      disabled={!!testing}
      title={`Test all ${mode === 'apikey' ? 'API Key' : mode} connections`}
      className={cn(
        'flex w-full items-center justify-center gap-1.5 rounded-lg border px-3 py-2 text-xs font-medium transition-colors sm:w-auto sm:py-1.5',
        active
          ? 'animate-pulse border-primary/40 bg-primary/20 text-primary'
          : 'border-border bg-bg text-text-muted hover:border-primary/40 hover:text-text-main',
      )}
    >
      <Icon name="play_arrow" className={active ? 'animate-spin text-[14px]' : 'text-[14px]'} />
      {active ? 'Testing...' : 'Test All'}
    </button>
  )
}

const GRID = 'grid grid-cols-1 gap-3 sm:grid-cols-2 sm:gap-4 lg:grid-cols-3 xl:grid-cols-4'

export function ProvidersPage() {
  const [providers, setProviders] = useState<ProviderSummary[]>([])
  const [conns, setConns] = useState<Connection[]>([])
  const [nodes, setNodes] = useState<ProviderNode[]>([])
  const [loading, setLoading] = useState(true)
  const [query, setQuery] = useState('')
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [searchParams, setSearchParams] = useSearchParams()
  const tabParam = searchParams.get('tab')
  const tab = tabParam === 'oauth' || tabParam === 'free' || tabParam === 'apikey' ? tabParam : 'custom'
  const [showAllApikey, setShowAllApikey] = useState(false)
  const [addVariant, setAddVariant] = useState<'openai' | 'anthropic' | null>(null)
  const [testing, setTesting] = useState<ProviderTestMode | null>(null)
  const [testResults, setTestResults] = useState<ProviderTestResults | null>(null)
  const notify = useNotify()

  const load = () =>
    Promise.all([api.providers(), api.providerNodes()])
      .then(([p, n]) => {
        setProviders(p.providers)
        setConns(p.connections ?? [])
        setNodes(n.nodes ?? [])
      })
      .catch(() => {})
      .finally(() => setLoading(false))

  useEffect(() => {
    void load()
  }, [])

  const statsOf = (id: string, authTypes: string | string[]) => connectionStatus(conns, id, asList(authTypes))

  const q = query.trim().toLowerCase()
  const matchSearch = (name: string) => !q || name.toLowerCase().includes(q)
  const matchStatus = (s: Stats, noAuth?: boolean) => {
    if (statusFilter === 'all') return true
    const status = noAuth ? 'active' : s.total === 0 ? 'none' : s.allDisabled ? 'inactive' : 'active'
    return status === statusFilter
  }

  const toggle = async (providerId: string, authTypes: string | string[], active: boolean) => {
    const types = asList(authTypes)
    const picked = conns.filter((c) => c.provider === providerId && types.includes(c.authType))
    // Optimistic: the card flips immediately, then each connection is saved.
    setConns((prev) => prev.map((c) => (picked.some((p) => p.id === c.id) ? { ...c, isActive: active } : c)))
    const results = await Promise.allSettled(picked.map((c) => api.updateConnection(c.id, { isActive: active })))
    if (results.some((r) => r.status === 'rejected')) {
      notify.error('Failed to update some connections')
      await load()
    }
  }

  const batchTest = async (mode: ProviderTestMode) => {
    if (testing) return
    setTesting(mode)
    setTestResults(null)
    try {
      const r = await api.testBatch({ mode })
      setTestResults(r)
      if (r.summary) {
        if (r.summary.failed === 0) notify.success(`All ${r.summary.total} tests passed`)
        else notify.warning(`${r.summary.passed}/${r.summary.total} passed, ${r.summary.failed} failed`)
      }
    } catch {
      setTestResults({ mode, results: [], testedAt: '', summary: { total: 0, passed: 0, failed: 0 }, error: 'Test request failed' })
      notify.error('Provider test failed')
    } finally {
      setTesting(null)
    }
  }

  const ofCategory = (cat: string) =>
    providers.filter((p) => p.category === cat && !p.hidden && (p.serviceKinds ?? ['llm']).includes('llm'))
  const groups = {
    oauth: ofCategory('oauth'),
    free: ofCategory('free'),
    freeTier: ofCategory('freeTier'),
    apikey: ofCategory('apikey'),
    webCookie: ofCategory('webCookie'),
  }

  const stats = (p: ProviderSummary, key: string, authTypes: string | string[]) => {
    const s = statsOf(key, authTypes)
    return { s, visible: matchSearch(p.display?.name ?? p.id) && matchStatus(s, p.noAuth) }
  }

  const oauthEntries = byPriority(
    groups.oauth.filter((p) => stats(p, p.id, dualAuthTypes(p, p.id)).visible),
    (p) => statsOf(p.id, dualAuthTypes(p, p.id)),
  )
  const freeEntries = groups.free
    .filter((p) => stats(p, p.id, dualAuthTypes(p, p.id)).visible)
    .sort((a, b) => (b.noAuth ? 1 : 0) - (a.noAuth ? 1 : 0))
  // Free Tier: priority, then no-auth providers last, then connected first.
  const freeTierEntries = groups.freeTier
    .filter((p) => stats(p, p.id, dualAuthTypes(p, p.id)).visible)
    .sort((a, b) => {
      const pa = a.priority ?? 999
      const pb = b.priority ?? 999
      if (pa !== pb) return pa - pb
      const na = (b.noAuth ? 1 : 0) - (a.noAuth ? 1 : 0)
      if (na !== 0) return na
      const ca = statsOf(a.id, dualAuthTypes(a, a.id)).connected > 0 ? 0 : 1
      const cb = statsOf(b.id, dualAuthTypes(b, b.id)).connected > 0 ? 0 : 1
      return ca - cb || (a.display?.name ?? a.id).localeCompare(b.display?.name ?? b.id)
    })
  const apikeyEntries = byConnected(
    groups.apikey.filter((p) => stats(p, p.id, 'apikey').visible),
    (p) => statsOf(p.id, 'apikey'),
  )
  const webCookieEntries = byConnected(
    groups.webCookie.filter((p) => stats(p, p.id, 'cookie').visible),
    (p) => statsOf(p.id, 'cookie'),
  )

  const oaiNodes = nodes.filter(
    (n) => n.type === 'openai-compatible' && matchSearch(n.name ?? 'OpenAI Compatible') && matchStatus(statsOf(n.id, 'apikey')),
  )
  const anthNodes = nodes.filter(
    (n) => n.type === 'anthropic-compatible' && matchSearch(n.name ?? 'Anthropic Compatible') && matchStatus(statsOf(n.id, 'apikey')),
  )

  const searching = !!q || statusFilter !== 'all'
  const visibleApikey = searching || showAllApikey ? apikeyEntries : apikeyEntries.slice(0, APIKEY_INITIAL_VISIBLE)
  const hiddenApikey = apikeyEntries.length - APIKEY_INITIAL_VISIBLE
  const anyResult =
    oaiNodes.length + anthNodes.length + oauthEntries.length + freeEntries.length +
    freeTierEntries.length + apikeyEntries.length + webCookieEntries.length > 0

  return (
    <>
      <PageHeader
        title="Providers"
        description={`${providers.length} providers in the registry · ${new Set(conns.map((c) => c.provider)).size} connected`}
        actions={
          <div className="flex items-center gap-2">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                className="h-8 w-56 pl-8 text-xs"
                placeholder="Search providers..."
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </div>
            <Select value={statusFilter} onValueChange={(v) => setStatusFilter(v as StatusFilter)}>
              <SelectTrigger className="h-8 w-36 text-xs" aria-label="Filter providers by connection status">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {STATUS_FILTER_OPTIONS.map((o) => (
                  <SelectItem key={o.value} value={o.value}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        }
      />

      {loading ? (
        <p className="py-12 text-center text-xs text-muted-foreground">Loading providers…</p>
      ) : (
        <div className="flex min-w-0 flex-col gap-6">
          {!anyResult && (
            <div className="rounded-xl border border-dashed border-border py-8 text-center">
              <Icon name="search_off" className="mb-2 text-[32px] text-text-muted" />
              <p className="text-sm text-text-muted">No providers match your search or filters</p>
            </div>
          )}

          <Tabs
            value={tab}
            onValueChange={(v) => setSearchParams(v === 'custom' ? {} : { tab: v }, { replace: true })}
            className="flex flex-col gap-4"
          >
            <TabsList className="w-fit max-w-full overflow-x-auto">
              <TabsTrigger value="custom">
                Custom<span className="text-[10px] tabular-nums opacity-60">{oaiNodes.length + anthNodes.length}</span>
              </TabsTrigger>
              <TabsTrigger value="oauth">
                OAuth<span className="text-[10px] tabular-nums opacity-60">{oauthEntries.length}</span>
              </TabsTrigger>
              <TabsTrigger value="free">
                Free Tier<span className="text-[10px] tabular-nums opacity-60">{freeEntries.length + freeTierEntries.length}</span>
              </TabsTrigger>
              <TabsTrigger value="apikey">
                API Key<span className="text-[10px] tabular-nums opacity-60">{apikeyEntries.length + webCookieEntries.length}</span>
              </TabsTrigger>
            </TabsList>

            <TabsContent value="custom" className="mt-0 flex flex-col gap-4">
              {oaiNodes.length + anthNodes.length === 0 ? (
                <div className="flex flex-col items-center gap-4 rounded-xl bg-surface-2 px-6 py-10 text-center">
                  <Icon name="extension" className="text-[20px] text-text-main" />
                  <div className="flex flex-col gap-1">
                    <p className="text-sm font-medium">Add your first custom provider</p>
                    <p className="max-w-md text-xs leading-relaxed text-text-muted">
                      Point KineRouter at any OpenAI- or Anthropic-compatible endpoint: a local model server, gateway, or hosted proxy.
                    </p>
                  </div>
                  <div className="flex w-full flex-col gap-2 sm:w-auto sm:flex-row">
                    <Button size="sm" onClick={() => setAddVariant('openai')}>
                      <Icon name="add" className="text-[16px]" /> Add OpenAI Compatible
                    </Button>
                    <Button size="sm" variant="secondary" onClick={() => setAddVariant('anthropic')}>
                      <Icon name="add" className="text-[16px]" /> Add Anthropic Compatible
                    </Button>
                  </div>
                </div>
              ) : (
                <>
                  <div className="flex flex-col gap-2 sm:flex-row sm:justify-end">
                    <Button size="sm" onClick={() => setAddVariant('anthropic')} className="w-full sm:w-auto">
                      <Icon name="add" className="text-[16px]" /> Add Anthropic Compatible
                    </Button>
                    <Button size="sm" variant="secondary" onClick={() => setAddVariant('openai')} className="w-full sm:w-auto">
                      <Icon name="add" className="text-[16px]" /> Add OpenAI Compatible
                    </Button>
                  </div>
                  <div className={GRID}>
                    {[...oaiNodes, ...anthNodes].map((n) => (
                      <ProviderCard
                        key={n.id}
                        p={{
                          id: n.id,
                          category: 'compatible',
                          authType: 'apikey',
                          hasOAuth: false,
                          display: { name: n.name ?? (n.type === 'anthropic-compatible' ? 'Anthropic Compatible' : 'OpenAI Compatible') },
                          modelsCount: 0,
                          connections: 0,
                          activeConnections: 0,
                          serviceKinds: ['llm'],
                        }}
                        stats={statsOf(n.id, 'apikey')}
                        iconSrc={nodeIcon(n)}
                        extraBadge={
                          <Badge size="sm">
                            {n.type === 'anthropic-compatible' ? 'Messages' : n.apiType === 'responses' ? 'Responses' : 'Chat'}
                          </Badge>
                        }
                        onToggle={(active) => void toggle(n.id, 'apikey', active)}
                      />
                    ))}
                  </div>
                </>
              )}
            </TabsContent>

            <TabsContent value="oauth" className="mt-0 flex flex-col gap-4">
              <div className="flex flex-wrap items-center justify-end gap-2">
                <ModelAvailabilityBadge />
                <TestAllButton mode="oauth" testing={testing} onClick={() => void batchTest('oauth')} />
              </div>
              <div className={GRID}>
                {oauthEntries.map((p) => {
                  const types = dualAuthTypes(p, p.id)
                  return (
                    <ProviderCard
                      key={p.id}
                      p={p}
                      stats={statsOf(p.id, types)}
                      onToggle={(active) => void toggle(p.id, types, active)}
                    />
                  )
                })}
              </div>
            </TabsContent>

            <TabsContent value="free" className="mt-0 flex flex-col gap-4">
              <div className="flex flex-wrap items-center justify-end gap-2">
                <TestAllButton mode="free" testing={testing} onClick={() => void batchTest('free')} />
              </div>
              <div className={GRID}>
                {freeEntries.map((p) => {
                  const types = dualAuthTypes(p, p.id)
                  return (
                    <ProviderCard
                      key={p.id}
                      p={p}
                      stats={statsOf(p.id, types)}
                      onToggle={(active) => void toggle(p.id, types, active)}
                    />
                  )
                })}
                {freeTierEntries.map((p) => {
                  const types = dualAuthTypes(p, p.id)
                  return (
                    <ProviderCard
                      key={p.id}
                      p={p}
                      stats={statsOf(p.id, types)}
                      onToggle={(active) => void toggle(p.id, types, active)}
                    />
                  )
                })}
              </div>
            </TabsContent>

            <TabsContent value="apikey" className="mt-0 flex flex-col gap-4">
              <div className="flex flex-wrap items-center justify-end gap-2">
                <TestAllButton mode="apikey" testing={testing} onClick={() => void batchTest('apikey')} />
              </div>
              <div className={GRID}>
                {visibleApikey.map((p) => (
                  <ProviderCard
                    key={p.id}
                    p={p}
                    stats={statsOf(p.id, 'apikey')}
                    onToggle={(active) => void toggle(p.id, 'apikey', active)}
                  />
                ))}
                {webCookieEntries.map((p) => (
                  <ProviderCard
                    key={p.id}
                    p={p}
                    stats={statsOf(p.id, 'cookie')}
                    onToggle={(active) => void toggle(p.id, 'cookie', active)}
                  />
                ))}
              </div>
              {!searching && !showAllApikey && hiddenApikey > 0 && (
                <button
                  onClick={() => setShowAllApikey(true)}
                  className="flex w-full items-center justify-center gap-1.5 rounded-lg border border-dashed border-primary/40 px-3 py-2.5 text-sm font-medium text-primary transition-colors hover:border-primary hover:bg-primary/5"
                >
                  <Icon name="expand_more" className="text-[16px]" />
                  Show all {apikeyEntries.length} providers
                </button>
              )}
            </TabsContent>
          </Tabs>

          <AddCompatibleModal
            variant="openai"
            isOpen={addVariant === 'openai'}
            onClose={() => setAddVariant(null)}
            onCreated={(node) => {
              setNodes((prev) => [...prev, node])
              setAddVariant(null)
            }}
          />
          <AddCompatibleModal
            variant="anthropic"
            isOpen={addVariant === 'anthropic'}
            onClose={() => setAddVariant(null)}
            onCreated={(node) => {
              setNodes((prev) => [...prev, node])
              setAddVariant(null)
            }}
          />

          <Modal isOpen={!!testResults} onClose={() => setTestResults(null)} title="Test Results" size="lg">
            {testResults && <TestResultsBody results={testResults} />}
          </Modal>
        </div>
      )}
    </>
  )
}
