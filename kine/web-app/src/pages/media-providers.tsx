import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, Navigate, useNavigate, useParams } from 'react-router-dom'
import { Icon } from '@/components/icon'
import { PageHeader } from '@/components/layout'
import { ModelSelectModal, type PickerModel } from '@/components/model-select-modal'
import { ProviderIcon } from '@/components/provider-icon'
import { ConfirmModal, Modal } from '@/components/modal'
import { AddApiKeyModal, NoAuthProxyCard } from '@/pages/provider-detail'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  api,
  type Combo,
  type Connection,
  type CustomModel,
  type MediaKindConfig,
  type ProviderDetail,
  type ProviderNode,
  type ProviderSummary,
  type ProxyPool,
} from '@/lib/api'
import { moveItem } from '@/lib/format'
import { useCopyToClipboard } from '@/lib/use-copy-to-clipboard'
import { useNotify } from '@/lib/toast'
import { cn } from '@/lib/utils'

/** Media provider kinds, mirrored from MEDIA_PROVIDER_KINDS in the reference. */
export const MEDIA_KINDS = [
  { id: 'embedding', label: 'Embedding', endpoint: '/v1/embeddings' },
  { id: 'image', label: 'Text to Image', endpoint: '/v1/images/generations' },
  { id: 'imageToText', label: 'Image to Text', endpoint: '/v1/images/understanding' },
  { id: 'tts', label: 'Text To Speech', endpoint: '/v1/audio/speech' },
  { id: 'stt', label: 'Speech To Text', endpoint: '/v1/audio/transcriptions' },
  { id: 'webSearch', label: 'Web Search', endpoint: '/v1/search' },
  { id: 'webFetch', label: 'Web Fetch', endpoint: '/v1/web/fetch' },
  { id: 'video', label: 'Video', endpoint: '/v1/videos/generations' },
  { id: 'music', label: 'Music', endpoint: '/v1/audio/music' },
  { id: 'systemone', label: 'System One', endpoint: '/v1/systemone' },
]

const kindOf = (id: string) => MEDIA_KINDS.find((k) => k.id === id)
const VALID_NAME_REGEX = /^[a-zA-Z0-9_.\-]+$/

// ------------------------------------------------------------------ helpers

function useMediaData() {
  const [providers, setProviders] = useState<ProviderSummary[]>([])
  const [connections, setConnections] = useState<Connection[]>([])
  const load = useCallback(() => {
    api
      .providers()
      .then((d) => {
        setProviders(d.providers)
        setConnections(d.connections)
      })
      .catch(() => {
        setProviders([])
        setConnections([])
      })
  }, [])
  useEffect(load, [load])
  return { providers, connections, setConnections, load }
}

/** A connection whose models are cooling down still counts as usable. */
function effectiveStatus(c: Connection): string | undefined {
  const locked = Object.entries(c).some(
    ([k, v]) => k.startsWith('modelLock_') && v && new Date(String(v)).getTime() > Date.now(),
  )
  return c.testStatus === 'unavailable' && !locked ? 'active' : (c.testStatus as string | undefined)
}

function connStats(providerId: string, connections: Connection[]) {
  const mine = connections.filter((c) => c.provider === providerId)
  const statuses = mine.map(effectiveStatus)
  const active = mine.filter((c) => c.isActive !== false).length
  return {
    total: mine.length,
    active,
    connected: statuses.filter((s) => s === 'active' || s === 'success').length,
    error: statuses.filter((s) => s === 'error' || s === 'expired' || s === 'unavailable').length,
    allOff: mine.length > 0 && active === 0,
  }
}

function StatusBadges({ p, connections }: { p: ProviderSummary; connections: Connection[] }) {
  const s = connStats(p.id, connections)
  if (p.noAuth) return <Badge variant="success" size="sm">Ready</Badge>
  if (s.allOff) return <Badge size="sm">Disabled</Badge>
  if (s.total === 0) return <span className="text-xs text-text-muted">No connections</span>
  return (
    <>
      {s.connected > 0 && (
        <Badge variant="success" size="sm" dot>
          {s.connected} Connected
        </Badge>
      )}
      {s.error > 0 && (
        <Badge variant="error" size="sm" dot>
          {s.error} Error
        </Badge>
      )}
      {s.connected === 0 && s.error === 0 && <Badge size="sm">{s.total} Added</Badge>}
    </>
  )
}

const hexTint = (color?: string) => (color && color.length > 7 ? color : `${color ?? '#888'}15`)

// ------------------------------------------------------------------ combo helpers

async function createCombo(kind: string, base: string, existing: Combo[]): Promise<Combo> {
  let name = base
  let i = 1
  const names = new Set(existing.map((c) => c.name))
  while (names.has(name)) name = `${base}-${i++}`
  return api.createCombo({ name, models: [], kind })
}

function ComboList({ combos, providers, emptyText }: { combos: Combo[]; providers: ProviderSummary[]; emptyText?: boolean }) {
  if (combos.length === 0) {
    return emptyText ? <p className="text-xs italic text-text-muted">No combos yet.</p> : null
  }
  const byId = new Map(providers.map((p) => [p.id, p]))
  return (
    <div className="flex flex-col gap-2">
      {combos.map((combo) => (
        <Link key={combo.id} to={`/dashboard/media-providers/combo/${combo.id}`}>
          <Card className="p-2 transition-colors hover:bg-surface-2/40">
            <div className="flex min-w-0 items-center gap-3">
              <Icon name="layers" className="text-[18px] text-primary" />
              <code className="min-w-0 flex-1 truncate font-mono text-sm font-medium text-text-main">{combo.name}</code>
              <div className="flex flex-wrap items-center gap-1 sm:shrink-0">
                {combo.models.slice(0, 6).map((entry, i) => {
                  const pid = typeof entry === 'string' ? entry.split('/')[0] : ''
                  const p = byId.get(pid)
                  return (
                    <div
                      key={`${entry}-${i}`}
                      title={p?.display?.name ?? entry}
                      className="flex size-5 items-center justify-center rounded"
                      style={{ backgroundColor: hexTint(p?.display?.color) }}
                    >
                      <ProviderIcon provider={pid} size={18} fallbackText={p?.display?.textIcon} />
                    </div>
                  )
                })}
                {combo.models.length > 6 && <span className="ml-1 text-[10px] text-text-muted">+{combo.models.length - 6}</span>}
              </div>
              <span className="shrink-0 text-[11px] text-text-muted">{combo.models.length}</span>
              <Icon name="chevron_right" className="text-[16px] text-text-muted" />
            </div>
          </Card>
        </Link>
      ))}
    </div>
  )
}

// ------------------------------------------------------------------ provider card

function ProviderCard({
  p,
  connections,
  kind,
  onToggle,
  custom,
}: {
  p: ProviderSummary
  connections: Connection[]
  kind: string
  onToggle?: (active: boolean) => void
  custom?: boolean
}) {
  const { total, allOff } = connStats(p.id, connections)
  return (
    <Link to={`/dashboard/media-providers/${kind}/${encodeURIComponent(p.id)}`} className="group block">
      <Card className={cn('h-full p-3 transition-colors', allOff && 'opacity-50')}>
        <div className="flex min-w-0 items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            <div className="flex size-8 shrink-0 items-center justify-center rounded-lg" style={{ backgroundColor: hexTint(p.display?.color) }}>
              <ProviderIcon provider={p.id} size={30} fallbackText={p.display?.textIcon} />
            </div>
            <div className="min-w-0">
              <h3 className="truncate text-sm font-semibold text-text-main">{p.display?.name ?? p.id}</h3>
              <div className="mt-0.5 flex flex-wrap items-center gap-1.5">
                {custom && <Badge size="sm">Custom</Badge>}
                <StatusBadges p={p} connections={connections} />
              </div>
            </div>
          </div>
          {total > 0 && onToggle && (
            <div
              className="shrink-0 opacity-100 transition-opacity sm:opacity-0 sm:group-hover:opacity-100"
              onClick={(e) => {
                e.preventDefault()
                e.stopPropagation()
                onToggle(allOff)
              }}
            >
              <Switch checked={!allOff} onCheckedChange={() => {}} title={allOff ? 'Enable provider' : 'Disable provider'} />
            </div>
          )}
        </div>
      </Card>
    </Link>
  )
}

function ProviderGrid({ kindId, providers, connections, onToggle }: {
  kindId: string
  providers: ProviderSummary[]
  connections: Connection[]
  onToggle: (id: string, active: boolean) => void
}) {
  if (providers.length === 0) {
    return (
      <div className="rounded-xl border border-dashed border-border py-12 text-center text-sm text-text-muted">
        No providers support <strong>{kindOf(kindId)?.label ?? kindId}</strong> yet.
      </div>
    )
  }
  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      {providers.map((p) => (
        <ProviderCard key={p.id} p={p} connections={connections} kind={kindId} onToggle={(a) => onToggle(p.id, a)} />
      ))}
    </div>
  )
}

/** Synthetic provider rows for custom embedding nodes (reference: CE/#6366F1). */
function customNodeProvider(n: ProviderNode): ProviderSummary {
  return {
    id: n.id,
    alias: n.id,
    category: 'compatible',
    authType: 'apikey',
    hasOAuth: false,
    display: { name: n.name || 'Custom Embedding', color: '#6366F1', textIcon: 'CE' },
    modelsCount: 0,
    connections: 0,
    activeConnections: 0,
    serviceKinds: ['embedding'],
  }
}

// ------------------------------------------------------------------ list

/** `/dashboard/media-providers/:kind`, for providers whose serviceKinds include it. */
export function MediaProvidersPage() {
  const { kind = '' } = useParams()
  const notify = useNotify()
  const { providers, connections, setConnections, load } = useMediaData()
  const [nodes, setNodes] = useState<ProviderNode[]>([])
  const [showAddCustom, setShowAddCustom] = useState(false)
  const cfg = kindOf(kind)
  const isEmbedding = kind === 'embedding'

  const loadNodes = useCallback(() => {
    if (!isEmbedding) return
    api
      .providerNodes()
      .then((d) => setNodes((d.nodes ?? []).filter((n) => n.type === 'custom-embedding')))
      .catch(() => setNodes([]))
  }, [isEmbedding])
  useEffect(loadNodes, [loadNodes])

  const matching = useMemo(
    () => providers.filter((p) => (p.serviceKinds ?? ['llm']).includes(kind)),
    [providers, kind],
  )
  const customProviders = nodes.map(customNodeProvider)

  const toggle = async (providerId: string, next: boolean) => {
    setConnections((prev) => prev.map((c) => (c.provider === providerId ? { ...c, isActive: next } : c)))
    const mine = connections.filter((c) => c.provider === providerId)
    const results = await Promise.allSettled(
      mine.map((c) => api.updateConnection(c.id, { isActive: next })),
    )
    if (results.some((r) => r.status === 'rejected')) notify.error('Some connections failed to update.')
    load()
  }

  // webSearch/webFetch listings are merged into /web.
  if (kind === 'webSearch' || kind === 'webFetch') {
    return <Navigate to="/dashboard/media-providers/web" replace />
  }
  if (!cfg) {
    return <p className="py-12 text-center text-sm text-text-muted">Unknown media kind: {kind}</p>
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={cfg.label}
        description={`${matching.length + customProviders.length} providers · ${cfg.endpoint}`}
        actions={
          isEmbedding ? (
            <Button size="sm" onClick={() => setShowAddCustom(true)}>
              <Icon name="add" className="text-[16px]" /> Add Custom Embedding
            </Button>
          ) : undefined
        }
      />

      {customProviders.length > 0 && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {customProviders.map((p) => (
            <ProviderCard key={p.id} p={p} connections={connections} kind={kind} onToggle={(a) => void toggle(p.id, a)} custom />
          ))}
        </div>
      )}

      <ProviderGrid kindId={kind} providers={matching} connections={connections} onToggle={(id, a) => void toggle(id, a)} />

      {showAddCustom && (
        <AddCustomEmbeddingModal
          onClose={() => setShowAddCustom(false)}
          onSaved={() => {
            setShowAddCustom(false)
            loadNodes()
            load()
          }}
        />
      )}
    </div>
  )
}

/** `/dashboard/media-providers/web`: webSearch + webFetch side by side. */
export function WebProvidersPage() {
  const { providers, connections } = useMediaData()
  const [combos, setCombos] = useState<Combo[]>([])
  const navigate = useNavigate()
  const notify = useNotify()

  const loadCombos = useCallback(() => {
    api.combos().then((d) => setCombos(d.combos ?? [])).catch(() => setCombos([]))
  }, [])
  useEffect(loadCombos, [loadCombos])

  const section = (kindId: string) => providers.filter((p) => (p.serviceKinds ?? []).includes(kindId))

  const onCreate = async (kindId: string) => {
    try {
      const base = kindId === 'webSearch' ? 'search-combo' : 'fetch-combo'
      const created = await createCombo(kindId, base, combos)
      navigate(`/dashboard/media-providers/combo/${created.id}`)
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to create combo')
    }
  }

  const renderSection = (kindId: 'webSearch' | 'webFetch', title: string, icon: string) => {
    const list = section(kindId)
    const kindCombos = combos.filter((c) => c.kind === kindId)
    return (
      <div>
        <div className="mb-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex flex-wrap items-center gap-2">
            <Icon name={icon} className="text-primary" />
            <h2 className="text-base font-semibold text-text-main">{title}</h2>
            <span className="text-xs text-text-muted">({list.length} providers · {kindCombos.length} combos)</span>
          </div>
          <Button size="sm" onClick={() => void onCreate(kindId)}>
            <Icon name="add" className="text-[16px]" /> Create Combo
          </Button>
        </div>
        {kindCombos.length > 0 && (
          <div className="mb-4">
            <ComboList combos={kindCombos} providers={providers} />
          </div>
        )}
        {list.length === 0 ? (
          <div className="rounded-xl border border-dashed border-border py-8 text-center text-sm text-text-muted">No providers.</div>
        ) : (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
            {list.map((p) => <ProviderCard key={p.id} p={p} connections={connections} kind={kindId} />)}
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-8">
      {renderSection('webSearch', 'Web Search', 'search')}
      <div className="border-t border-border" />
      {renderSection('webFetch', 'Web Fetch', 'travel_explore')}
    </div>
  )
}

// ------------------------------------------------------------------ detail

export function MediaProviderDetailPage() {
  const { kind = '', providerId = '' } = useParams()
  const notify = useNotify()
  const navigate = useNavigate()
  const { providers, connections, setConnections, load } = useMediaData()
  const [detail, setDetail] = useState<ProviderDetail | null>(null)
  const [customNode, setCustomNode] = useState<ProviderNode | null>(null)
  const [customLoading, setCustomLoading] = useState(false)
  const [showEditModal, setShowEditModal] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [showAddKey, setShowAddKey] = useState(false)
  const [proxyPools, setProxyPools] = useState<ProxyPool[]>([])
  const [settings, setSettings] = useState<Record<string, unknown>>({})
  const cfg = kindOf(kind)

  const isCustom = useMemo(() => providerId.startsWith('custom-embedding-'), [providerId])

  useEffect(() => {
    api.proxyPools().then((d) => setProxyPools(d.proxyPools ?? [])).catch(() => setProxyPools([]))
    api.settings().then(setSettings).catch(() => setSettings({}))
  }, [])

  useEffect(() => {
    api.provider(providerId).then(setDetail).catch(() => setDetail(null))
  }, [providerId])

  useEffect(() => {
    if (!isCustom) return
    setCustomLoading(true)
    api
      .providerNodes()
      .then((d) => setCustomNode((d.nodes ?? []).find((n) => n.id === providerId) ?? null))
      .catch(() => setCustomNode(null))
      .finally(() => setCustomLoading(false))
  }, [providerId, isCustom])

  if (!cfg) return <p className="py-12 text-center text-sm text-text-muted">Unknown media kind: {kind}</p>
  if (isCustom && customLoading) return <p className="py-12 text-center text-sm text-text-muted">Loading…</p>
  if (isCustom && !customNode) return <p className="py-12 text-center text-sm text-text-muted">Provider not found.</p>

  const summary = providers.find((p) => p.id === providerId)
  const name = isCustom ? customNode?.name || 'Custom Embedding' : detail?.display?.name ?? summary?.display?.name ?? providerId
  const textIcon = isCustom ? 'CE' : detail?.display?.textIcon ?? summary?.display?.textIcon
  const kinds = isCustom ? ['embedding'] : (summary?.serviceKinds ?? ['embedding'])
  const mine = connections.filter((c) => c.provider === providerId)

  const toggleAll = (pid: string, conns: Connection[], next: boolean) => {
    setConnections((prev) => prev.map((c) => (c.provider === pid ? { ...c, isActive: next } : c)))
    return Promise.allSettled(conns.map((c) => api.updateConnection(c.id, { isActive: next }))).then(() => load())
  }

  const deleteCustom = async () => {
    try {
      await api.deleteProviderNode(providerId)
      navigate(`/dashboard/media-providers/${kind}`)
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to delete node')
    }
  }

  const configFor = (): { title: string; config?: MediaKindConfig } | null => {
    if (!detail || isCustom) return null
    const pick =
      kind === 'webFetch' ? detail.fetchConfig
      : kind === 'tts' ? detail.ttsConfig
      : kind === 'stt' ? detail.sttConfig
      : kind === 'embedding' ? detail.embeddingConfig
      : kind === 'systemone' ? detail.systemoneConfig
      : kind === 'image' ? detail.imageConfig
      : kind === 'video' ? detail.videoConfig
      : detail.searchConfig || (detail.searchViaChat
        ? {
            mode: 'chat-completions',
            defaultModel: detail.searchViaChat.defaultModel,
            pricingUrl: detail.searchViaChat.pricingUrl,
            freeTier: detail.searchViaChat.freeTier,
          }
        : undefined)
    if (!pick) return null
    return { title: `${cfg.label} Config`, config: pick }
  }
  const info = configFor()

  return (
    <div className="flex flex-col gap-5">
      <Link
        to={`/dashboard/media-providers/${kind}`}
        className="inline-flex w-fit items-center gap-1 text-xs text-text-muted transition-colors hover:text-text-main"
      >
        <Icon name="arrow_back" className="text-[15px]" /> {cfg.label}
      </Link>

      <div className="flex flex-wrap items-center gap-3">
        <span className="flex size-11 shrink-0 items-center justify-center rounded-xl bg-surface-2">
          <ProviderIcon provider={isCustom ? 'custom-embedding' : providerId} size={34} fallbackText={textIcon} />
        </span>
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-xl font-semibold tracking-tight text-text-main">{name}</h1>
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            {isCustom && <Chip>{String(customNode?.prefix ?? '')}</Chip>}
            {kinds.map((k) => (
              <Chip key={k} active={k === kind}>
                {k.toUpperCase()}
              </Chip>
            ))}
            {!isCustom && detail?.display?.notice?.apiKeyUrl && (
              <a
                href={detail.display.notice.apiKeyUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1 rounded bg-surface-2 px-1.5 py-0.5 text-[10px] font-medium text-text-main transition-colors hover:bg-card"
              >
                <Icon name="open_in_new" className="text-[11px]" /> Get API key
              </a>
            )}
          </div>
        </div>
        {isCustom && (
          <div className="flex items-center gap-1.5">
            <Button size="sm" variant="secondary" onClick={() => setShowEditModal(true)}>
              <Icon name="edit" className="text-[16px]" /> Edit
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(true)} className="text-danger hover:bg-danger/10">
              <Icon name="delete" className="text-[16px]" /> Delete
            </Button>
          </div>
        )}
      </div>

      {!isCustom && detail?.display?.kindNotice?.[kind] && (
        <div className="flex items-start gap-2.5 rounded-xl bg-surface-2 px-4 py-3 text-warning">
          <Icon name="warning" className="mt-0.5 text-[16px]" />
          <p className="text-xs leading-relaxed">{detail.display.kindNotice[kind]}</p>
        </div>
      )}

      {!isCustom && detail?.display?.notice?.text && !detail.display.deprecated && (
        <div className="flex flex-wrap items-center gap-3 rounded-xl bg-surface px-4 py-3">
          <Icon name="info" className="shrink-0 text-[16px] text-text-muted" />
          <p className="min-w-0 flex-1 text-xs leading-relaxed text-text-main">{detail.display.notice.text}</p>
          {detail.display.notice.apiKeyUrl && (
            <a
              href={detail.display.notice.apiKeyUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 rounded-lg bg-surface-2 px-2.5 py-1 text-[11px] font-medium text-text-main transition-colors hover:bg-card"
            >
              Get API key <Icon name="open_in_new" className="text-[12px]" />
            </a>
          )}
        </div>
      )}

      {!isCustom && detail?.noAuth ? (
        <NoAuthProxyCard providerId={providerId} proxyPools={proxyPools} settings={settings} onSaved={setSettings} />
      ) : kind === 'stt' && detail?.hasOAuth ? (
        <section className="flex flex-wrap items-center gap-3 rounded-xl bg-surface px-4 py-3">
          <p className="min-w-0 flex-1 text-xs text-text-muted">This service shares your existing OAuth connections.</p>
          <Link
            to={`/dashboard/providers/${providerId}`}
            className="inline-flex items-center gap-1 rounded-lg bg-surface-2 px-2.5 py-1 text-[11px] font-medium text-text-main transition-colors hover:bg-card"
          >
            Manage OAuth <Icon name="arrow_forward" className="text-[12px]" />
          </Link>
        </section>
      ) : (
        <section className="overflow-hidden rounded-xl bg-surface">
          <div className="flex flex-wrap items-center justify-between gap-2 px-4 py-3">
            <div className="flex items-center gap-2">
              <Icon name="key" className="text-[15px] text-text-muted" />
              <h2 className="text-sm font-semibold text-text-main">Connections</h2>
              <span className="font-mono text-[11px] text-text-muted">{mine.length}</span>
            </div>
            <div className="flex items-center gap-2">
              {mine.length > 0 && (
                <label className="flex items-center gap-1.5 text-[11px] text-text-muted">
                  Enabled
                  <Switch checked={mine.some((c) => c.isActive !== false)} onCheckedChange={(next) => void toggleAll(providerId, mine, next)} />
                </label>
              )}
              <Button size="sm" variant="secondary" onClick={() => setShowAddKey(true)}>
                <Icon name="add" className="text-[16px]" /> Add API key
              </Button>
            </div>
          </div>
          {mine.length === 0 ? (
            <p className="border-t border-border px-4 py-8 text-center text-xs text-text-muted">
              No connections yet. Add an API key to start routing requests.
            </p>
          ) : (
            <ul className="divide-y divide-border border-t border-border">
              {mine.map((c) => (
                <li key={c.id} className="flex items-center justify-between gap-3 px-4 py-2.5">
                  <span className="truncate text-xs text-text-main">{c.name || c.email || c.id}</span>
                  <div className="flex shrink-0 items-center gap-1.5">
                    {typeof c.testStatus === 'string' && c.testStatus !== '' && (
                      <Chip danger={c.testStatus === 'error'}>{c.testStatus}</Chip>
                    )}
                    <Chip>{c.isActive ? 'active' : 'inactive'}</Chip>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      {kind !== 'tts' && kind !== 'webSearch' && kind !== 'webFetch' && (
        <MediaModelsCard
          providerId={providerId}
          kind={kind}
          isCustom={isCustom}
          aliasOverride={isCustom ? String(customNode?.prefix ?? '') : undefined}
          detail={detail}
        />
      )}

      {info && <ProviderInfoCard title={info.title} config={info.config!} website={detail?.display?.notice?.apiKeyUrl || detail?.display?.website} />}

      <ExampleCard kind={kind} provider={providerId} detail={detail} isCustom={isCustom} customAlias={String(customNode?.prefix ?? '')} />

      {showAddKey && detail && (
        <AddApiKeyModal
          provider={providerId}
          providerName={detail.display?.name ?? providerId}
          isCompatible={false}
          authType={detail.authType}
          authHint={detail.authHint}
          credentialForm={detail.credentialForm}
          existingNames={mine.map((c) => c.name ?? '').filter(Boolean)}
          proxyPools={proxyPools}
          onClose={() => setShowAddKey(false)}
          onSaved={async () => {
            setShowAddKey(false)
            load()
          }}
        />
      )}

      {(showEditModal || isCustom && showEditModal) && isCustom && (
        <AddCustomEmbeddingModal
          node={customNode}
          onClose={() => setShowEditModal(false)}
          onSaved={(n) => {
            setCustomNode(n)
            setShowEditModal(false)
          }}
        />
      )}

      <ConfirmModal
        isOpen={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        onConfirm={() => void deleteCustom()}
        title="Delete provider node"
        message="Delete this Custom Embedding node? Its connections stay under the deleted id."
        confirmLabel="Delete"
        variant="danger"
      />
    </div>
  )
}

// ------------------------------------------------------------------ models card

function MediaModelsCard({
  providerId,
  kind,
  isCustom,
  aliasOverride,
  detail,
}: {
  providerId: string
  kind: string
  isCustom: boolean
  aliasOverride?: string
  detail: ProviderDetail | null
}) {
  const notify = useNotify()
  const [custom, setCustom] = useState<CustomModel[]>([])
  const [aliases, setAliases] = useState<Record<string, string>>({})
  const [disabled, setDisabled] = useState<string[]>([])
  const [newModel, setNewModel] = useState('')
  const [testing, setTesting] = useState<string | null>(null)

  const alias = aliasOverride || detail?.uiAlias || providerId

  const load = useCallback(() => {
    api.customModels().then((d) => setCustom(d.models ?? [])).catch(() => setCustom([]))
    api.aliases().then((d) => setAliases(d.aliases ?? {})).catch(() => setAliases({}))
    api.disabledFor(alias).then((d) => setDisabled(d.ids ?? [])).catch(() => setDisabled([]))
  }, [alias])
  useEffect(load, [load])

  // Strict kind match, like the reference ModelsCard: a provider lists its
  // image, embedding, tts and llm models separately.
  const builtin = (detail?.models ?? []).filter((m) => m.kind === kind)
  const customMine = custom.filter((m) => m.providerAlias === alias && (!m.type || m.type === kind))
  const added = new Set(customMine.map((m) => m.id))

  const test = async (id: string) => {
    setTesting(id)
    try {
      const r = await api.testModel(`${alias}/${id}`, kind)
      notify[r.ok ? 'success' : 'error'](r.ok ? 'Model works' : r.error || 'Model failed')
    } finally {
      setTesting(null)
    }
  }

  const add = async () => {
    if (!newModel.trim()) return
    try {
      await api.addCustomModel({ providerAlias: alias, id: newModel.trim(), type: kind })
      setNewModel('')
      load()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to add model')
    }
  }

  const remove = async (id: string) => {
    try {
      await api.deleteCustomModel(alias, id, kind)
      load()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to delete model')
    }
  }

  const toggleDisabled = async (id: string) => {
    try {
      if (disabled.includes(id)) await api.enableModel(alias, id)
      else await api.disableModels(alias, [...disabled, id])
      load()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to update model')
    }
  }

  return (
    <section className="overflow-hidden rounded-xl bg-surface">
      <div className="flex items-center gap-2 px-4 py-3">
        <Icon name="layers" className="text-[15px] text-text-muted" />
        <h2 className="text-sm font-semibold text-text-main">Models</h2>
        <Chip>{kind.toUpperCase()}</Chip>
        <span className="ml-auto font-mono text-[11px] text-text-muted">{builtin.length + customMine.length}</span>
      </div>

      {builtin.length === 0 && customMine.length === 0 ? (
        <p className="border-t border-border px-4 py-8 text-center text-xs text-text-muted">
          No {kind} models registered.
        </p>
      ) : (
        <div className="divide-y divide-border border-t border-border">
          {builtin.map((m) => {
            const full = `${alias}/${m.id}`
            const off = disabled.includes(m.id)
            return (
              <div key={m.id} className="flex items-center gap-1.5 px-4 py-2 transition-colors hover:bg-surface-2/40">
                <code className={cn('min-w-0 flex-1 truncate font-mono text-xs', off ? 'text-text-muted' : 'text-text-main')}>
                  {full}
                </code>
                {aliases[full] && <Chip>→ {aliases[full]}</Chip>}
                {off && <Chip>disabled</Chip>}
                <button
                  className="flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-2 hover:text-text-main disabled:opacity-30"
                  disabled={testing === m.id}
                  onClick={() => void test(m.id)}
                  title="Test model"
                >
                  <Icon name={testing === m.id ? 'progress_activity' : 'science'} className={cn('text-[16px]', testing === m.id && 'animate-spin')} />
                </button>
                <button
                  className="flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-2 hover:text-text-main"
                  onClick={() => void toggleDisabled(m.id)}
                  title={off ? 'Enable model' : 'Disable model'}
                >
                  <Icon name={off ? 'check' : 'block'} className="text-[16px]" />
                </button>
              </div>
            )
          })}
          {customMine.map((m) => (
            <div key={`custom-${m.id}`} className="flex items-center gap-1.5 px-4 py-2 transition-colors hover:bg-surface-2/40">
              <code className="min-w-0 flex-1 truncate font-mono text-xs text-text-main">{alias}/{m.id}</code>
              <Chip>custom</Chip>
              <button
                className="flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-2 hover:text-text-main disabled:opacity-30"
                disabled={testing === m.id}
                onClick={() => void test(m.id)}
                title="Test model"
              >
                <Icon name={testing === m.id ? 'progress_activity' : 'science'} className={cn('text-[16px]', testing === m.id && 'animate-spin')} />
              </button>
              <button
                className="flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-text-muted transition-colors hover:bg-danger/10 hover:text-danger"
                onClick={() => void remove(m.id)}
                title="Remove model"
              >
                <Icon name="delete" className="text-[16px]" />
              </button>
            </div>
          ))}
        </div>
      )}
      {(isCustom || builtin.length > 0) && (
        <div className="flex items-center gap-2 border-t border-border px-4 py-3">
          <Input
            value={newModel}
            onChange={(e) => setNewModel(e.target.value)}
            placeholder="Add model id (e.g. text-embedding-3-small)"
            className="h-8 rounded-lg border-0 bg-surface-2 font-mono text-xs shadow-none"
          />
          <Button size="sm" variant="secondary" onClick={() => void add()} disabled={!newModel.trim() || added.has(newModel.trim())}>
            <Icon name="add" className="text-[16px]" /> Add
          </Button>
        </div>
      )}
    </section>
  )
}

// ------------------------------------------------------------------ provider info

/** Port of ProviderInfoCard.js FIELD_SCHEMA: rows filtered to non-empty values. */
function ProviderInfoCard({ title, config, website }: { title: string; config: MediaKindConfig; website?: string }) {
  const rows: [string, string][] = []
  const push = (label: string, value: unknown) => {
    if (value === undefined || value === null || value === '') return
    rows.push([label, String(value)])
  }
  push('Mode', config.mode)
  push('Default model', config.defaultModel)
  push('Base URL', config.baseUrl)
  if (config.costPerQuery !== undefined) {
    rows.push(['Cost / query', config.costPerQuery === 0 ? 'Free' : `$${Number(config.costPerQuery).toFixed(4)}`])
  }
  push('Pricing', config.pricingUrl)
  push('Free tier', config.freeTier)
  if (config.freeMonthlyQuota !== undefined) {
    const q = Number(config.freeMonthlyQuota)
    rows.push(['Free quota', q === 0 ? '-' : q >= 999999 ? 'Unlimited' : `${q.toLocaleString()} / mo`])
  }
  if (Array.isArray(config.searchTypes)) push('Search types', config.searchTypes.join(', '))
  if (Array.isArray(config.formats)) push('Formats', config.formats.join(', '))
  push('Max results', config.maxMaxResults)
  if (config.maxCharacters !== undefined) rows.push(['Max characters', Number(config.maxCharacters).toLocaleString()])

  if (rows.length === 0) return null
  return (
    <section className="overflow-hidden rounded-xl bg-surface">
      <div className="flex items-center justify-between gap-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-text-main">{title}</h2>
        {website && (
          <a
            href={website}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-[11px] font-medium text-text-muted transition-colors hover:text-text-main"
          >
            Docs <Icon name="open_in_new" className="text-[12px]" />
          </a>
        )}
      </div>
      <dl className="grid grid-cols-1 gap-x-8 gap-y-1 border-t border-border px-4 py-3 text-xs sm:grid-cols-2">
        {rows.map(([k, v]) => (
          <div key={k} className="flex min-w-0 items-baseline justify-between gap-3">
            <dt className="shrink-0 text-[11px] text-text-muted">{k}</dt>
            <dd className="min-w-0 truncate text-right font-mono text-[11px] text-text-main" title={v}>
              {v}
            </dd>
          </div>
        ))}
      </dl>
    </section>
  )
}

// ------------------------------------------------------------------ example

type ExtraField =
  | { key: string; label: string; type: 'select'; default: string; options: string[] }
  | { key: string; label: string; type: 'number'; default: number; min?: number; max?: number }
  | { key: string; label: string; type: 'text'; default: string }
  | { key: string; label: string; type: 'boolean'; default: boolean }
  | { key: string; label: string; type: 'image'; default: string }

type KindExample = {
  inputLabel: string
  defaultInput: string
  bodyKey: string
  extraBody?: Record<string, unknown>
  extraFields?: ExtraField[]
  defaultResponse: string
}

/** Port of KIND_EXAMPLE_CONFIG in exampleShared.js. */
const KIND_EXAMPLE: Record<string, KindExample> = {
  webSearch: {
    inputLabel: 'Query',
    defaultInput: 'What is the latest news about AI?',
    bodyKey: 'query',
    defaultResponse: `{\n  "results": [\n    { "title": "...", "url": "...", "snippet": "..." }\n  ]\n}`,
    extraFields: [
      { key: 'search_type', label: 'Type', type: 'select', default: 'web', options: ['web', 'news'] },
      { key: 'max_results', label: 'Max results', type: 'number', default: 5, min: 1, max: 100 },
      { key: 'country', label: 'Country', type: 'text', default: '' },
      { key: 'language', label: 'Language', type: 'text', default: '' },
    ],
  },
  webFetch: {
    inputLabel: 'URL',
    defaultInput: 'https://example.com',
    bodyKey: 'url',
    defaultResponse: `{\n  "content": "...",\n  "title": "...",\n  "url": "..."\n}`,
    extraFields: [
      { key: 'format', label: 'Format', type: 'select', default: 'markdown', options: ['markdown', 'text', 'html'] },
      { key: 'max_characters', label: 'Max chars', type: 'number', default: 0, min: 0 },
    ],
  },
  image: {
    inputLabel: 'Prompt',
    defaultInput: 'A cute cat wearing a hat',
    bodyKey: 'prompt',
    defaultResponse: `{\n  "data": [\n    { "url": "...", "b64_json": "..." }\n  ]\n}`,
    extraFields: [
      { key: 'n', label: 'n', type: 'number', default: 1, min: 1, max: 4 },
      { key: 'size', label: 'Size', type: 'select', default: 'auto', options: ['auto', '1024x1024', '1024x1536', '1536x1024', '1024x1792', '1792x1024'] },
      { key: 'quality', label: 'Quality', type: 'select', default: 'auto', options: ['auto', 'low', 'medium', 'high', 'standard', 'hd'] },
      { key: 'background', label: 'Background', type: 'select', default: 'auto', options: ['auto', 'transparent', 'opaque'] },
      { key: 'style', label: 'Style', type: 'select', default: '', options: ['', 'vivid', 'natural'] },
      { key: 'response_format', label: 'Format', type: 'select', default: '', options: ['', 'url', 'b64_json'] },
      { key: 'output_format', label: 'Codec', type: 'select', default: 'png', options: ['png', 'jpeg', 'webp'] },
      { key: 'watermark', label: 'Watermark', type: 'boolean', default: true },
      { key: 'prompt_extend', label: 'Prompt Extend', type: 'boolean', default: true },
    ],
  },
  imageToText: {
    inputLabel: 'Image URL',
    defaultInput: 'https://upload.wikimedia.org/wikipedia/commons/thumb/3/3a/Cat03.jpg/1200px-Cat03.jpg',
    bodyKey: 'url',
    extraBody: { prompt: 'Describe this image in detail' },
    defaultResponse: `{\n  "text": "A cat sitting on a windowsill...",\n  "model": "..."\n}`,
  },
  video: {
    inputLabel: 'Prompt',
    defaultInput: 'A serene lake at sunset',
    bodyKey: 'prompt',
    defaultResponse: `{\n  "data": [\n    { "url": "..." }\n  ]\n}`,
    extraFields: [{ key: 'image', label: 'Image (URL or upload)', type: 'image', default: '' }],
  },
  music: {
    inputLabel: 'Prompt',
    defaultInput: 'A calm piano melody',
    bodyKey: 'prompt',
    defaultResponse: `{\n  "data": [\n    { "url": "...", "format": "mp3" }\n  ]\n}`,
  },
  systemone: {
    inputLabel: 'State',
    defaultInput: 'My payments have failed for three days and I am losing sales. Please help now.',
    bodyKey: 'state',
    extraBody: { questions: { is_urgent: { type: 'noul', instructions: 'Does this request require urgent attention?' } } },
    defaultResponse: `{\n  "model": "jev-1.13",\n  "answers": {\n    "is_urgent": { "type": "noul", "noul": 0.99 }\n  }\n}`,
  },
  embedding: {
    inputLabel: 'Input',
    defaultInput: 'The quick brown fox jumps over the lazy dog',
    bodyKey: 'input',
    defaultResponse: `{\n  "object": "list",\n  "data": [{\n    "object": "embedding",\n    "index": 0,\n    "embedding": [0.002301, -0.019212, "..."]\n  }],\n  "usage": { "prompt_tokens": 9, "total_tokens": 9 }\n}`,
    extraFields: [{ key: 'dimensions', label: 'Dimensions', type: 'number', default: 0, min: 0 }],
  },
  tts: {
    inputLabel: 'Text',
    defaultInput: 'Hello, this is a text to speech test.',
    bodyKey: 'input',
    defaultResponse: '// Audio appears here after running.',
    extraFields: [
      { key: 'voice', label: 'Voice', type: 'text', default: '' },
      { key: 'response_format', label: 'Format', type: 'select', default: 'mp3', options: ['mp3', 'json'] },
    ],
  },
  stt: {
    inputLabel: 'Language (optional)',
    defaultInput: '',
    bodyKey: 'language',
    defaultResponse: `{\n  "text": "Hello world..."\n}`,
    extraFields: [{ key: 'response_format', label: 'Format', type: 'select', default: 'json', options: ['json', 'text', 'srt', 'vtt', 'verbose_json'] }],
  },
}

/** Long base64 / data-URI values are masked so the JSON preview stays readable. */
function maskB64(obj: unknown): unknown {
  if (Array.isArray(obj)) return obj.map(maskB64)
  if (obj && typeof obj === 'object') {
    const out: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(obj as Record<string, unknown>)) {
      if (typeof v === 'string' && v.length > 100 && (k === 'b64_json' || v.startsWith('data:'))) {
        out[k] = v.startsWith('data:') ? `<${v.length} chars data URI>` : `<${v.length} chars base64>`
      } else out[k] = maskB64(v)
    }
    return out
  }
  return obj
}

const isTunnelPublic = (url: string) => /^https?:\/\//.test(url) && !url.includes('localhost')

/**
 * Live request example: one card for every media kind, driven by KIND_EXAMPLE.
 * The reference ships per-kind cards (Embedding/Tts/Stt/Generic); the fields
 * and bodies they build are the same, so they collapse into this card.
 * ponytail: TTS voice/language pickers and the model-report SSE progress
 * stream are not ported; add when a TTS provider needs preset voices.
 */
function ExampleCard({
  kind,
  provider,
  detail,
  isCustom,
  customAlias,
}: {
  kind: string
  provider: string
  detail: ProviderDetail | null
  isCustom: boolean
  customAlias: string
}) {
  const cfg = kindOf(kind)!
  const ex = KIND_EXAMPLE[kind]
  const alias = isCustom ? customAlias || provider : detail?.uiAlias || provider

  const [model, setModel] = useState('')
  const [input, setInput] = useState(ex?.defaultInput ?? '')
  const [apiKey, setApiKey] = useState('')
  const [localEndpoint, setLocalEndpoint] = useState('')
  const [tunnelEndpoint, setTunnelEndpoint] = useState('')
  const [useTunnel, setUseTunnel] = useState(false)
  const [connectionId, setConnectionId] = useState('')
  const [conns, setConns] = useState<Connection[]>([])
  const [extra, setExtra] = useState<Record<string, string | number | boolean>>(() => {
    const out: Record<string, string | number | boolean> = {}
    for (const f of ex?.extraFields ?? []) out[f.key] = f.default
    return out
  })
  const [file, setFile] = useState<File | null>(null)
  const [result, setResult] = useState<unknown>(null)
  const [mediaUrl, setMediaUrl] = useState('')
  const [latency, setLatency] = useState<number | null>(null)
  const [error, setError] = useState('')
  const [running, setRunning] = useState(false)
  const { copied, copy } = useCopyToClipboard()
  const notify = useNotify()

  useEffect(() => {
    setLocalEndpoint(window.location.origin)
    api.keys().then((k) => setApiKey((k.keys.find((x) => x.isActive !== false) ?? k.keys[0])?.key ?? '')).catch(() => {})
    api.tunnelStatus().then((s) => setTunnelEndpoint(s.tunnel?.tunnelUrl ?? '')).catch(() => {})
  }, [])

  useEffect(() => {
    const mine = detail?.models ?? []
    const first = mine.find((m) => m.kind === kind) ?? (kind === 'llm' ? mine[0] : undefined)
    if (first) setModel(first.id)
    setInput(ex?.defaultInput ?? '')
  }, [detail, kind, ex])

  useEffect(() => {
    api
      .providers()
      .then((d) => setConns(d.connections.filter((c) => c.provider === provider && c.isActive !== false)))
      .catch(() => setConns([]))
  }, [provider])

  if (!ex) return null

  const endpointRoot = useTunnel ? tunnelEndpoint : localEndpoint
  const modelFull = model ? `${alias}/${model}` : ''
  const buildBody = (): Record<string, unknown> => {
    const body: Record<string, unknown> = { ...(modelFull ? { model: modelFull } : {}), ...ex.extraBody }
    if (input.trim()) body[ex.bodyKey] = input.trim()
    for (const f of ex.extraFields ?? []) {
      const v = extra[f.key]
      if (v === '' || v === undefined) continue
      if (f.type === 'number') {
        const n = Number(v)
        if (n > 0) body[f.key] = n
      } else if (f.key !== 'response_format') body[f.key] = v
    }
    if (kind === 'tts' && extra.response_format) body.response_format = extra.response_format
    return body
  }

  const curlHeaders = [
    '-H "Content-Type: application/json"',
    `-H "Authorization: Bearer ${apiKey || 'YOUR_KEY'}"`,
    connectionId ? `-H "x-connection-id: ${connectionId}"` : '',
  ].filter(Boolean)
  const apiPath = `/api${cfg.endpoint}`
  const curlSnippet = kind === 'stt'
    ? `curl -X POST ${endpointRoot}${cfg.endpoint} \\
  -H "Authorization: Bearer ${apiKey || 'YOUR_KEY'}" \\
  -F "file=@${file?.name || 'audio.mp3'}"${modelFull ? ` \\
  -F "model=${modelFull}"` : ''}`
    : `curl -X POST ${endpointRoot}${apiPath.replace(/^\/api/, '')} \\
  ${curlHeaders.join(' \\\n  ')} \\
  -d '${JSON.stringify(buildBody())}'`

  const run = async () => {
    setRunning(true)
    setError('')
    setResult(null)
    setMediaUrl('')
    const start = Date.now()
    try {
      const headers: Record<string, string> = {}
      if (apiKey) headers.Authorization = `Bearer ${apiKey}`
      if (connectionId) headers['x-connection-id'] = connectionId
      let res: Response
      if (kind === 'stt') {
        const fd = new FormData()
        if (file) fd.append('file', file)
        if (modelFull) fd.append('model', modelFull)
        if (input.trim()) fd.append('language', input.trim())
        fd.append('response_format', String(extra.response_format || 'json'))
        res = await fetch(apiPath, { method: 'POST', headers, body: fd })
      } else {
        headers['Content-Type'] = 'application/json'
        res = await fetch(apiPath, { method: 'POST', headers, body: JSON.stringify(buildBody()) })
      }
      setLatency(Date.now() - start)
      const ctype = res.headers.get('content-type') || ''
      if (ctype.startsWith('image/')) {
        setMediaUrl(URL.createObjectURL(await res.blob()))
      } else if (ctype.startsWith('audio/') || ctype === 'application/octet-stream') {
        setMediaUrl(URL.createObjectURL(await res.blob()))
      } else if (ctype.includes('json') || ctype.includes('event-stream')) {
        const data = (await res.json()) as Record<string, unknown>
        if (!res.ok) {
          const err = data.error as { message?: string } | string | undefined
          setError(typeof err === 'string' ? err : err?.message || `HTTP ${res.status}`)
          return
        }
        setResult(data)
        const first = (data.data as Array<Record<string, unknown>> | undefined)?.[0]
        if (typeof first?.url === 'string') setMediaUrl(first.url)
        else if (typeof first?.b64_json === 'string') setMediaUrl(`data:image/png;base64,${first.b64_json}`)
        else if (typeof (data.video as { url?: string } | undefined)?.url === 'string') setMediaUrl(String((data.video as { url: string }).url))
      } else {
        const text = await res.text()
        setResult(text)
        if (!res.ok) setError(`HTTP ${res.status}`)
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Network error')
    } finally {
      setRunning(false)
    }
  }

  const download = () => {
    if (!mediaUrl) return
    const a = document.createElement('a')
    a.href = mediaUrl
    a.download = `${provider}-${kind}.${kind === 'tts' ? 'mp3' : kind === 'video' ? 'mp4' : 'png'}`
    a.click()
    notify.success('Download started')
  }

  return (
    <section className="overflow-hidden rounded-xl bg-surface">
      <div className="flex items-center justify-between gap-3 px-4 py-3">
        <div className="flex items-center gap-2">
          <Icon name="science" className="text-[15px] text-text-muted" />
          <h2 className="text-sm font-semibold text-text-main">Example</h2>
        </div>
        <button
          onClick={() => copy(curlSnippet, 'curl')}
          className="inline-flex shrink-0 cursor-pointer items-center gap-1.5 rounded-md bg-surface-2 px-2.5 py-1 text-[11px] font-medium text-text-main transition-colors hover:bg-card"
        >
          <Icon name={copied === 'curl' ? 'check' : 'content_copy'} className="text-[12px]" /> cURL
        </button>
      </div>

      <div className="flex flex-col gap-3 border-t border-border px-4 py-3">
      <Row label="Model">
        <Input value={modelFull} onChange={(e) => setModel(e.target.value.split('/').slice(1).join('/'))} className="border-0 bg-surface-2 font-mono text-xs shadow-none" />
      </Row>

      <Row label="Endpoint">
        <div className="flex w-full min-w-0 flex-col gap-2 sm:flex-row sm:items-center">
          <Input
            value={endpointRoot}
            onChange={(e) => (useTunnel ? setTunnelEndpoint(e.target.value) : setLocalEndpoint(e.target.value))}
            className="min-w-0 flex-1 border-0 bg-surface-2 font-mono text-xs shadow-none"
          />
          {isTunnelPublic(tunnelEndpoint) && (
            <button
              onClick={() => setUseTunnel((v) => !v)}
              title={useTunnel ? 'Using tunnel' : 'Using local'}
              aria-pressed={useTunnel}
              className={cn(
                'flex shrink-0 cursor-pointer items-center gap-1 rounded-lg px-2.5 py-1.5 text-[11px] font-medium transition-colors',
                useTunnel ? 'bg-text-main text-background' : 'bg-surface-2 text-text-muted hover:text-text-main',
              )}
            >
              <Icon name="wifi_tethering" className="text-[13px]" /> Tunnel
            </button>
          )}
        </div>
      </Row>

      <Row label="API key">
        <Input value={apiKey} onChange={(e) => setApiKey(e.target.value)} type="password" placeholder="sk-…" className="border-0 bg-surface-2 font-mono text-xs shadow-none" />
      </Row>

      {conns.length > 1 && (
        <Row label="Connection">
          <select
            value={connectionId}
            onChange={(e) => setConnectionId(e.target.value)}
            className="h-9 w-full cursor-pointer rounded-lg border-0 bg-surface-2 px-3 text-xs text-text-main outline-none"
          >
            <option value="">Auto (round robin)</option>
            {conns.map((c) => (
              <option key={c.id} value={c.id}>{c.name || c.email || c.id}</option>
            ))}
          </select>
        </Row>
      )}

      {kind === 'stt' ? (
        <>
          <Row label="Audio file">
            <input
              type="file"
              accept="audio/*,video/mp4,.m4a,.mp3,.wav,.ogg,.flac,.webm,.opus"
              onChange={(e) => setFile(e.target.files?.[0] ?? null)}
              className="w-full text-xs text-text-muted file:mr-2 file:cursor-pointer file:rounded-lg file:border-0 file:bg-surface-2 file:px-2.5 file:py-1.5 file:text-[11px] file:font-medium file:text-text-main hover:file:bg-card"
            />
          </Row>
          <Row label="Language">
            <Input value={input} onChange={(e) => setInput(e.target.value)} placeholder="optional" className="border-0 bg-surface-2 text-xs shadow-none" />
          </Row>
        </>
      ) : (
        <Row label={ex.inputLabel}>
          <div className="relative">
            <Input value={input} onChange={(e) => setInput(e.target.value)} className="border-0 bg-surface-2 pr-7 text-xs shadow-none" />
            {input && (
              <button
                type="button"
                onClick={() => setInput('')}
                className="absolute right-2 top-1/2 -translate-y-1/2 cursor-pointer text-text-muted hover:text-text-main"
              >
                <Icon name="close" className="text-[14px]" />
              </button>
            )}
          </div>
        </Row>
      )}

      {(ex.extraFields ?? []).map((f) =>
        f.type === 'image' ? (
          <Row key={f.key} label={f.label}>
            <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
              <Input
                value={String(extra[f.key] ?? '')}
                onChange={(e) => setExtra((p) => ({ ...p, [f.key]: e.target.value }))}
                placeholder="https://…"
                className="min-w-0 flex-1 border-0 bg-surface-2 font-mono text-xs shadow-none"
              />
              <input
                type="file"
                accept="image/*"
                onChange={(e) => {
                  const file = e.target.files?.[0]
                  if (!file) return
                  const reader = new FileReader()
                  reader.onload = () => setExtra((p) => ({ ...p, [f.key]: String(reader.result ?? '') }))
                  reader.readAsDataURL(file)
                }}
                className="text-xs text-text-muted file:mr-2 file:cursor-pointer file:rounded-lg file:border-0 file:bg-surface-2 file:px-2.5 file:py-1.5 file:text-[11px] file:font-medium file:text-text-main hover:file:bg-card"
              />
            </div>
          </Row>
        ) : f.type === 'select' ? (
          <Row key={f.key} label={f.label}>
            <select
              value={String(extra[f.key] ?? '')}
              onChange={(e) => setExtra((p) => ({ ...p, [f.key]: e.target.value }))}
              className="h-9 w-full cursor-pointer rounded-lg border-0 bg-surface-2 px-3 text-xs text-text-main outline-none"
            >
              {f.options.map((o) => (
                <option key={o} value={o}>{o || '(default)'}</option>
              ))}
            </select>
          </Row>
        ) : f.type === 'boolean' ? (
          <Row key={f.key} label={f.label}>
            <Switch checked={Boolean(extra[f.key])} onCheckedChange={(v) => setExtra((p) => ({ ...p, [f.key]: v }))} />
          </Row>
        ) : f.type === 'number' ? (
          <Row key={f.key} label={f.label}>
            <Input
              type="number"
              min={f.min}
              max={f.max}
              value={String(extra[f.key] ?? '')}
              onChange={(e) => setExtra((p) => ({ ...p, [f.key]: e.target.value }))}
              className="border-0 bg-surface-2 text-xs shadow-none"
            />
          </Row>
        ) : (
          <Row key={f.key} label={f.label}>
            <Input value={String(extra[f.key] ?? '')} onChange={(e) => setExtra((p) => ({ ...p, [f.key]: e.target.value }))} className="border-0 bg-surface-2 text-xs shadow-none" />
          </Row>
        ),
      )}

      <div className="flex flex-wrap items-center gap-2 pt-1">
        <Button size="sm" onClick={() => void run()} disabled={running || !model || (kind !== 'stt' && !input.trim()) || (kind === 'stt' && !file)}>
          {running ? 'Running…' : 'Run'}
        </Button>
        {latency !== null && !running && (
          <span className="inline-flex items-center gap-1 rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-text-muted">
            <Icon name="bolt" className="text-[12px]" /> {latency}ms
          </span>
        )}
      </div>

      {error && <p className="text-xs text-danger">{error}</p>}

      {mediaUrl && (kind === 'image' || kind === 'imageToText') && (
        <div className="flex flex-col gap-2">
          <img src={mediaUrl} alt="result" className="max-h-80 rounded-lg bg-surface-2 object-contain" />
          <div>
            <Button size="sm" variant="secondary" onClick={download}>
              <Icon name="download" className="text-[16px]" /> Download
            </Button>
          </div>
        </div>
      )}
      {mediaUrl && kind === 'tts' && (
        <div className="flex flex-col gap-2">
          <audio controls src={mediaUrl} className="w-full" />
          <div>
            <Button size="sm" variant="secondary" onClick={download}>
              <Icon name="download" className="text-[16px]" /> Download
            </Button>
          </div>
        </div>
      )}
      {mediaUrl && kind === 'video' && (
        <div className="flex flex-col gap-2">
          <video controls src={mediaUrl} className="max-h-80 w-full rounded-lg" />
          <div>
            <Button size="sm" variant="secondary" onClick={download}>
              <Icon name="download" className="text-[16px]" /> Download
            </Button>
          </div>
        </div>
      )}

      <pre className="custom-scrollbar max-h-72 overflow-auto rounded-lg bg-surface-2 p-3 font-mono text-[11px] leading-relaxed text-text-main">
        {result ? (typeof result === 'string' ? result : JSON.stringify(maskB64(result), null, 2)) : ex.defaultResponse}
      </pre>
      </div>
    </section>
  )
}

function Chip({ children, active, danger }: { children: React.ReactNode; active?: boolean; danger?: boolean }) {
  return (
    <span
      className={cn(
        'inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 font-mono text-[10px]',
        active ? 'bg-card text-text-main shadow-[0_1px_2px_rgba(0,0,0,0.06)]' : 'bg-surface-2 text-text-muted',
        danger && 'text-danger',
      )}
    >
      {children}
    </span>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5 sm:flex-row sm:items-center sm:gap-3">
      <span className="w-full text-[11px] text-text-muted sm:w-24 sm:shrink-0">{label}</span>
      <div className="w-full min-w-0 flex-1">{children}</div>
    </div>
  )
}

// ------------------------------------------------------------------ combo detail

const COMBO_KIND_LABELS: Record<string, string> = {
  webSearch: 'Web Search',
  webFetch: 'Web Fetch',
  image: 'Image',
  tts: 'Text To Speech',
}

const COMBO_EXAMPLE_PATHS: Record<string, string> = {
  webSearch: '/v1/search',
  webFetch: '/v1/web/fetch',
  image: '/v1/images/generations',
  tts: '/v1/audio/speech',
}
const COMBO_EXAMPLE_BODIES: Record<string, Record<string, unknown>> = {
  webSearch: { query: 'What is the latest news about AI?' },
  webFetch: { url: 'https://example.com' },
  image: { prompt: 'A cute cat wearing a hat' },
  tts: { input: 'Hello world' },
}

function parseModelEntry(entry: string): [string, string] {
  const idx = entry.indexOf('/')
  return idx < 0 ? [entry, ''] : [entry.slice(0, idx), entry.slice(idx + 1)]
}

/** `/dashboard/media-providers/combo/:id`: port of combo/[id]/page.js. */
export function MediaComboDetailPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const notify = useNotify()
  const [combo, setCombo] = useState<Combo | null>(null)
  const [notFound, setNotFound] = useState(false)
  const [name, setName] = useState('')
  const [models, setModels] = useState<string[]>([])
  const [strategies, setStrategies] = useState<Record<string, { fallbackStrategy?: string }>>({})
  const [providers, setProviders] = useState<ProviderSummary[]>([])
  const [aliases, setAliases] = useState<Record<string, string>>({})
  const [logs, setLogs] = useState<string[]>([])
  const [apiKey, setApiKey] = useState('')
  const [showPicker, setShowPicker] = useState(false)
  const [saving, setSaving] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [testResult, setTestResult] = useState<unknown>(null)
  const [testMedia, setTestMedia] = useState('')
  const [testLatency, setTestLatency] = useState<number | null>(null)
  const [testError, setTestError] = useState('')
  const [testing, setTesting] = useState(false)
  const { copied, copy } = useCopyToClipboard()

  const load = useCallback(async () => {
    setNotFound(false)
    try {
      const [c, s, p, a, k] = await Promise.all([api.combo(id), api.settings(), api.providers(), api.aliases(), api.keys()])
      setCombo(c)
      setName(c.name)
      setModels(c.models ?? [])
      setStrategies(((s.comboStrategies as Record<string, { fallbackStrategy?: string }>) ?? {}))
      setProviders(p.providers)
      setAliases(a.aliases ?? {})
      setApiKey((k.keys.find((x) => x.isActive !== false) ?? k.keys[0])?.key ?? '')
      api.usageRequestLogs().then(setLogs).catch(() => setLogs([]))
    } catch {
      setNotFound(true)
      notify.error('Combo not found')
    }
  }, [id, notify])
  useEffect(() => {
    void load()
  }, [load])

  if (notFound) return <p className="py-12 text-center text-sm text-text-muted">Combo not found.</p>
  if (!combo) return <p className="py-12 text-center text-sm text-text-muted">Loading…</p>

  const kind = combo.kind ?? 'llm'
  const kindLabel = COMBO_KIND_LABELS[kind] ?? kind
  const roundRobin = strategies[combo.name]?.fallbackStrategy === 'round-robin'
  const byId = new Map(providers.map((p) => [p.id, p]))
  const nameError = name && !VALID_NAME_REGEX.test(name) ? 'Only letters, numbers, -, _ and . allowed' : ''

  const saveName = async () => {
    if (!name.trim() || nameError) return
    setSaving(true)
    try {
      await api.updateCombo(id, { name: name.trim() })
      setCombo({ ...combo, name: name.trim() })
      notify.success('Combo saved')
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to rename combo')
    } finally {
      setSaving(false)
    }
  }

  const saveModels = async (next: string[]) => {
    setModels(next)
    try {
      await api.updateCombo(id, { models: next })
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to save combo')
    }
  }

  const toggleRoundRobin = async (next: boolean) => {
    const all = { ...strategies }
    if (next) all[combo.name] = { ...all[combo.name], fallbackStrategy: 'round-robin' }
    else delete all[combo.name]
    setStrategies(all)
    try {
      await api.patchSettings({ comboStrategies: all })
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to save strategy')
    }
  }

  const removeCombo = async () => {
    try {
      await api.deleteCombo(id)
      navigate('/dashboard/media-providers/web')
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to delete combo')
    }
  }

  const testPath = COMBO_EXAMPLE_PATHS[kind] ?? '/v1/chat/completions'
  const testBody = { model: combo.name, ...(COMBO_EXAMPLE_BODIES[kind] ?? { messages: [{ role: 'user', content: 'Hello' }] }) }
  const curl = `curl -X POST ${window.location.origin}${testPath} \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer ${apiKey || 'YOUR_KEY'}" \\
  -d '${JSON.stringify(testBody)}'`

  const runTest = async () => {
    setTesting(true)
    setTestError('')
    setTestResult(null)
    setTestMedia('')
    const start = Date.now()
    try {
      const res = await fetch(`/api${testPath}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...(apiKey ? { Authorization: `Bearer ${apiKey}` } : {}) },
        body: JSON.stringify(testBody),
      })
      setTestLatency(Date.now() - start)
      const ctype = res.headers.get('content-type') || ''
      if (ctype.startsWith('image/') || ctype.startsWith('audio/') || ctype === 'application/octet-stream') {
        setTestMedia(URL.createObjectURL(await res.blob()))
      } else {
        const data = (await res.json()) as Record<string, unknown>
        if (!res.ok) {
          const err = data.error as { message?: string } | string | undefined
          setTestError(typeof err === 'string' ? err : err?.message || `HTTP ${res.status}`)
          return
        }
        setTestResult(data)
        const first = (data.data as Array<Record<string, unknown>> | undefined)?.[0]
        if (typeof first?.b64_json === 'string') setTestMedia(`data:image/png;base64,${first.b64_json}`)
        else if (typeof first?.url === 'string') setTestMedia(first.url)
      }
    } catch (e) {
      setTestError(e instanceof Error ? e.message : 'Network error')
    } finally {
      setTesting(false)
    }
  }

  const comboLogs = logs.filter((l) => typeof l === 'string' && l.includes(combo.name)).slice(0, 50)

  return (
    <div className="flex flex-col gap-6">
      <Link to={kind === 'webSearch' || kind === 'webFetch' ? '/dashboard/media-providers/web' : `/dashboard/media-providers/${kind}`} className="inline-flex items-center gap-1 text-sm text-text-muted hover:text-primary">
        <Icon name="arrow_back" className="text-[18px]" /> {kindLabel}
      </Link>

      <div className="flex flex-wrap items-center gap-3">
        <Icon name="layers" className="text-[22px] text-primary" />
        <h1 className="text-2xl font-semibold tracking-tight text-text-main">{kindLabel} Combo</h1>
        <code className="font-mono text-sm text-text-muted">{combo.name}</code>
        <Button size="sm" variant="outline" className="ml-auto border-danger text-danger hover:bg-danger/10" onClick={() => setConfirmDelete(true)}>
          <Icon name="delete" className="text-[16px]" /> Delete
        </Button>
      </div>

      <Card className="flex flex-col gap-3 p-3">
        <h3 className="text-sm font-semibold text-text-main">Settings</h3>
        <label className="flex flex-col gap-1">
          <span className="text-xs text-text-muted">Name</span>
          <div className="flex gap-2">
            <Input value={name} onChange={(e) => setName(e.target.value)} className="font-mono text-xs" />
            <Button size="sm" onClick={() => void saveName()} disabled={saving || !name.trim() || !!nameError || name === combo.name}>
              {saving ? 'Saving…' : 'Save'}
            </Button>
          </div>
          {nameError ? <span className="text-[11px] text-danger">{nameError}</span> : <span className="text-[11px] text-text-muted">Only letters, numbers, -, _ and .</span>}
        </label>
        <label className="flex items-center justify-between gap-3 rounded-lg border border-border px-3 py-2">
          <span className="text-xs text-text-main">
            Round Robin
            <span className="ml-2 text-text-muted">rotate members instead of first-match fallback</span>
          </span>
          <Switch checked={roundRobin} onCheckedChange={(v) => void toggleRoundRobin(v)} />
        </label>
      </Card>

      <Card className="p-3">
        <div className="mb-2 flex items-center justify-between">
          <h3 className="text-sm font-semibold text-text-main">Providers</h3>
          <Button size="sm" variant="outline" onClick={() => setShowPicker(true)}>
            <Icon name="add" className="text-[16px]" /> Add Provider
          </Button>
        </div>
        {models.length === 0 ? (
          <p className="py-4 text-center text-xs text-text-muted">No providers yet.</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {models.map((entry, i) => {
              const [pid, mid] = parseModelEntry(entry)
              const p = byId.get(pid)
              return (
                <li key={`${entry}-${i}`} className="flex items-center gap-2 rounded-lg border border-border p-2">
                  <span className="w-5 shrink-0 text-center font-mono text-xs text-text-muted">{i + 1}</span>
                  <div className="flex size-7 shrink-0 items-center justify-center rounded" style={{ backgroundColor: hexTint(p?.display?.color) }}>
                    <ProviderIcon provider={pid} size={22} fallbackText={p?.display?.textIcon} />
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm text-text-main">{p?.display?.name ?? pid}</div>
                    <code className="font-mono text-[11px] text-text-muted">{mid ? `${pid}/${mid}` : entry}</code>
                  </div>
                  <button className="text-text-muted hover:text-primary disabled:opacity-30" disabled={i === 0} onClick={() => void saveModels(moveItem(models, i, i - 1))} title="Move up">
                    <Icon name="keyboard_arrow_up" className="text-[18px]" />
                  </button>
                  <button className="text-text-muted hover:text-primary disabled:opacity-30" disabled={i === models.length - 1} onClick={() => void saveModels(moveItem(models, i, i + 1))} title="Move down">
                    <Icon name="keyboard_arrow_down" className="text-[18px]" />
                  </button>
                  <button className="text-text-muted hover:text-danger" onClick={() => void saveModels(models.filter((_, j) => j !== i))} title="Remove">
                    <Icon name="close" className="text-[16px]" />
                  </button>
                </li>
              )
            })}
          </ul>
        )}
      </Card>

      <Card className="flex flex-col gap-3 p-3">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-text-main">Test Example</h3>
          <div className="flex items-center gap-2">
            {testLatency !== null && !testing && (
              <span className="text-xs text-text-muted">
                <Icon name="bolt" className="text-[14px]" /> {testLatency}ms
              </span>
            )}
            <Button variant="ghost" size="sm" onClick={() => copy(curl, 'curl')}>
              <Icon name={copied === 'curl' ? 'check' : 'content_copy'} className="text-[16px]" /> cURL
            </Button>
            <Button size="sm" onClick={() => void runTest()} disabled={testing}>
              {testing ? 'Running…' : 'Run'}
            </Button>
          </div>
        </div>
        <pre className="max-h-40 overflow-auto rounded-md border border-border bg-surface-2/50 p-2 font-mono text-[11px] text-text-main">{curl}</pre>
        {testError && <p className="text-xs text-danger">{testError}</p>}
        {testMedia && (kind === 'image' ? (
          <div className="flex flex-col gap-2">
            <img src={testMedia} alt="result" className="max-h-80 rounded-lg border border-border object-contain" />
            <a href={testMedia} download="combo.png" className="text-xs text-primary hover:underline">Download</a>
          </div>
        ) : (
          <div className="flex flex-col gap-2">
            <audio controls src={testMedia} className="w-full" />
            <a href={testMedia} download="combo.mp3" className="text-xs text-primary hover:underline">Download</a>
          </div>
        ))}
        {testResult ? (
          <pre className="max-h-72 overflow-auto rounded-md border border-border bg-surface-2/50 p-2 font-mono text-[11px] text-text-main">
            {JSON.stringify(maskB64(testResult), null, 2)}
          </pre>
        ) : (
          !testMedia && <p className="text-xs text-text-muted">Run the example to see a live response.</p>
        )}
      </Card>

      <Card className="p-3">
        <h3 className="mb-2 text-sm font-semibold text-text-main">Usage Logs</h3>
        <pre className="max-h-72 overflow-auto rounded-md border border-border bg-surface-2/50 p-2 font-mono text-[11px] text-text-muted">
          {comboLogs.length ? comboLogs.join('\n') : 'No usage yet.'}
        </pre>
      </Card>

      {showPicker && (
        <ModelSelectModal
          isOpen
          onClose={() => setShowPicker(false)}
          onSelect={(m: PickerModel) => {
            if (m.value && !models.includes(m.value)) void saveModels([...models, m.value])
          }}
          onDeselect={(m: PickerModel) => void saveModels(models.filter((x) => x !== m.value))}
          title={`Add ${kindLabel} Provider`}
          kindFilter={kind}
          addedModelValues={models}
          closeOnSelect={false}
          modelAliases={aliases}
        />
      )}

      <ConfirmModal
        isOpen={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        onConfirm={() => void removeCombo()}
        title="Delete combo"
        message={`Delete combo "${combo.name}"? This cannot be undone.`}
        confirmLabel="Delete"
        variant="danger"
      />
    </div>
  )
}

// ------------------------------------------------------------------ custom embedding modal

const DEFAULT_BASE_URL = 'https://api.openai.com/v1'

/** Port of AddCustomEmbeddingModal.js: create or edit a custom-embedding node. */
function AddCustomEmbeddingModal({
  node,
  onClose,
  onSaved,
}: {
  node?: ProviderNode | null
  onClose: () => void
  onSaved: (n: ProviderNode) => void
}) {
  const notify = useNotify()
  const edit = !!node
  const [name, setName] = useState(String(node?.name ?? ''))
  const [prefix, setPrefix] = useState(String(node?.prefix ?? 'ce'))
  const [baseUrl, setBaseUrl] = useState(String(node?.baseUrl ?? DEFAULT_BASE_URL))
  const [apiKey, setApiKey] = useState('')
  const [modelId, setModelId] = useState('')
  const [checking, setChecking] = useState<'idle' | 'ok' | 'bad'>('idle')
  const [checkResult, setCheckResult] = useState<{ dimensions?: number; error?: string }>({})
  const [busy, setBusy] = useState(false)

  const prefixError = prefix && !/^[a-zA-Z0-9_-]+$/.test(prefix) ? 'Letters, numbers, - and _ only' : ''
  const canSave = !!name.trim() && !!prefix.trim() && !prefixError && !!baseUrl.trim()

  const check = async () => {
    setChecking('idle')
    setCheckResult({})
    try {
      const r = await api.validateProviderNode({ baseUrl: baseUrl.trim(), apiKey: apiKey.trim(), type: 'custom-embedding', modelId: modelId.trim() })
      setChecking(r.valid ? 'ok' : 'bad')
      setCheckResult({ dimensions: r.dimensions, error: r.error })
    } catch (e) {
      setChecking('bad')
      setCheckResult({ error: e instanceof Error ? e.message : 'Validation failed' })
    }
  }

  const save = async () => {
    if (!canSave) return
    setBusy(true)
    try {
      const body = { name: name.trim(), prefix: prefix.trim(), baseUrl: baseUrl.trim(), type: 'custom-embedding' }
      const r = edit ? await api.updateProviderNode(node!.id, body) : await api.createProviderNode(body)
      notify.success(edit ? 'Node updated' : 'Node created')
      onSaved(r.node)
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to save node')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal isOpen onClose={onClose} title={edit ? 'Edit Custom Embedding' : 'Add Custom Embedding'} size="md">
      <div className="flex flex-col gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-xs text-text-muted">Name</span>
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="My embeddings" />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs text-text-muted">Prefix</span>
          <Input value={prefix} onChange={(e) => setPrefix(e.target.value)} className="font-mono text-xs" placeholder="ce" />
          {prefixError ? <span className="text-[11px] text-danger">{prefixError}</span> : <span className="text-[11px] text-text-muted">Used as the model prefix: {prefix || 'ce'}/model</span>}
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs text-text-muted">Base URL</span>
          <Input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} className="font-mono text-xs" placeholder={DEFAULT_BASE_URL} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs text-text-muted">API key</span>
          <div className="flex gap-2">
            <Input value={apiKey} onChange={(e) => setApiKey(e.target.value)} type="password" placeholder="sk-…" className="font-mono text-xs" />
            <Button size="sm" variant="outline" onClick={() => void check()} disabled={!apiKey.trim() || !baseUrl.trim() || !modelId.trim()}>
              Check
            </Button>
          </div>
          {checking === 'ok' && <Badge variant="success" size="sm">Valid{checkResult.dimensions ? ` · ${checkResult.dimensions} dims` : ''}</Badge>}
          {checking === 'bad' && <Badge variant="error" size="sm">Invalid{checkResult.error ? ` · ${checkResult.error}` : ''}</Badge>}
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs text-text-muted">Model ID (for the check)</span>
          <Input value={modelId} onChange={(e) => setModelId(e.target.value)} className="font-mono text-xs" placeholder="text-embedding-3-small" />
        </label>
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button onClick={() => void save()} disabled={busy || !canSave}>{busy ? 'Saving…' : edit ? 'Save' : 'Create'}</Button>
        </div>
      </div>
    </Modal>
  )
}
