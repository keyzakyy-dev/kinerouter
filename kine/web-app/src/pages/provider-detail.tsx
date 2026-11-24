import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api, type Connection, type CustomModel, type ProviderDetail, type ProviderNode, type ProxyPool } from '@/lib/api'
import { fmtTime } from '@/lib/format'
import { useNotify } from '@/lib/toast'
import { ProviderIcon } from '@/components/provider-icon'
import { CapacityBadges } from '@/components/capacity-badges'
import { SponsoredAdBanner } from '@/components/sponsored-ad'
import { Icon } from '@/components/icon'
import { CopyButton } from '@/components/copy-button'
import { Modal, ConfirmModal } from '@/components/modal'
import { EditConnectionModal, AutoPingScheduleModal } from '@/components/connection-modals'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'

const ONE_BY_ONE_DELAY_MS = 1000
const AUTO_PING_SETTINGS_KEYS: Record<string, string> = { claude: 'claudeAutoPing', codex: 'codexAutoPing' }
const LIVE_MODEL_CATALOG_PROVIDERS = new Set(['cursor', 'zed', 'claude', 'commandcode'])

/**
 * Providers whose credential is imported rather than OAuth'd through a browser
 * (KineRouter's per-provider auth modals). One lane id per POST /api/oauth/{p}/import.
 */
type ImportLane = 'gitlab' | 'kiro' | 'cursor' | 'zed' | 'xiaomi'
const IMPORT_LANES: Record<string, ImportLane> = {
  gitlab: 'gitlab',
  kiro: 'kiro',
  cursor: 'cursor',
  zed: 'zed',
  'xiaomi-mimo': 'xiaomi',
}
const IMPORT_LABELS: Record<ImportLane, string> = {
  gitlab: 'Token / PAT',
  kiro: 'API Key',
  cursor: 'Import Token',
  zed: 'Import Session',
  xiaomi: 'API Key',
}
const IMPORT_TITLES: Record<ImportLane, string> = {
  gitlab: 'Paste a Personal Access Token (or OAuth app client id) from your GitLab instance',
  kiro: 'Paste a Kiro API key and its AWS region',
  cursor: 'Paste a Cursor access token and machine id',
  zed: 'Paste a Zed cloud access token',
  xiaomi: 'Paste a MiMo API key and pick its region',
}
const BUILTIN_BLOCKED_HEADERS = ['host', 'content-length', 'content-type', 'connection', 'transfer-encoding', 'authorization', 'cookie']
const HEADER_NAME_RE = /^[A-Za-z0-9-]+$/

type OneByOneState = { state: 'queued' | 'testing' | 'success' | 'failed'; error?: string }

/**
 * A region-refused models test carries an opencode.ai workspace link; the
 * reference turns it into the "/go" page that lifts the China-model block.
 */
function regionAllowUrl(err: string): string {
  const link = err.match(/https:\/\/opencode\.ai\/workspace\/[^\s"')]+/)
  if (link) return link[0].endsWith('/go') ? link[0] : `${link[0]}/go`
  const wrk = err.match(/wrk_[0-9A-Za-z]+/)
  if (wrk) return `https://opencode.ai/workspace/${wrk[0]}/go`
  return 'https://opencode.ai'
}

export function ProviderDetailPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const notify = useNotify()

  const [detail, setDetail] = useState<ProviderDetail | null>(null)
  const [error, setError] = useState('')
  const [proxyPools, setProxyPools] = useState<ProxyPool[]>([])
  const [settings, setSettings] = useState<Record<string, unknown>>({})
  const [aliases, setAliases] = useState<Record<string, string>>({})
  const [customModels, setCustomModels] = useState<CustomModel[]>([])
  const [disabledIds, setDisabledIds] = useState<string[]>([])
  const [suggested, setSuggested] = useState<Array<{ id: string; name?: string; contextLength?: number }>>([])
  const [liveModelsError, setLiveModelsError] = useState('')

  const [selected, setSelected] = useState<string[]>([])
  const [selectModels, setSelectModels] = useState(false)
  const [modelSelection, setModelSelection] = useState<string[]>([])
  const [oneByOne, setOneByOne] = useState<Record<string, OneByOneState>>({})
  const [oneByOneRunning, setOneByOneRunning] = useState(false)
  const [oneByOneStopping, setOneByOneStopping] = useState(false)
  const [oneByOneSummary, setOneByOneSummary] = useState<
    { total: number; completed: number; passed: number; failed: number; stopped: boolean } | null
  >(null)
  const stopRef = useRef(false)

  const [modelTestResults, setModelTestResults] = useState<Record<string, 'ok' | 'error'>>({})
  const [testingModels, setTestingModels] = useState<string[]>([])
  const [modelsTestError, setModelsTestError] = useState('')
  const [newModelId, setNewModelId] = useState('')
  const [addingModel, setAddingModel] = useState(false)
  const [importingModels, setImportingModels] = useState(false)

  const [showAddKey, setShowAddKey] = useState(false)
  const [showOAuth, setShowOAuth] = useState(false)
  const [showEditConnection, setShowEditConnection] = useState<Connection | null>(null)
  const [showEditNode, setShowEditNode] = useState(false)
  const [showBulkProxy, setShowBulkProxy] = useState(false)
  const [showCustomModel, setShowCustomModel] = useState(false)
  const [showIFlowCookie, setShowIFlowCookie] = useState(false)
  const [showBulkImport, setShowBulkImport] = useState(false)
  const [leaseBusy, setLeaseBusy] = useState(false)
  const [leaseError, setLeaseError] = useState('')
  const [importTarget, setImportTarget] = useState<'gitlab' | 'kiro' | 'cursor' | 'zed' | 'xiaomi' | null>(null)
  const [aliasTarget, setAliasTarget] = useState<string | null>(null)
  const [scheduleConnection, setScheduleConnection] = useState<Connection | null>(null)
  const [confirm, setConfirm] = useState<{ title: string; message: string; onConfirm: () => Promise<void> | void } | null>(null)

  const load = useCallback(async () => {
    try {
      const [d, pools, s, a, cm] = await Promise.all([
        api.provider(id),
        api.proxyPools().catch(() => ({ proxyPools: [] })),
        api.settings().catch(() => ({}) as Record<string, unknown>),
        api.aliases().catch(() => ({ aliases: {} })),
        api.customModels().catch(() => ({ models: [] })),
      ])
      setDetail(d)
      setProxyPools(pools.proxyPools)
      setSettings(s)
      setAliases(a.aliases)
      setCustomModels(cm.models)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load provider')
    }
  }, [id])

  useEffect(() => {
    setDetail(null)
    setError('')
    void load()
  }, [load])

  // MiniMax desktop lease: the server borrows the running desktop app's token
  // over its local pipe, so there is nothing to type here, just the attempt.
  const runLeaseConnect = () => {
    setLeaseBusy(true)
    setLeaseError('')
    void api
      .leaseConnect(id)
      .then((r) => {
        if (r.error) setLeaseError(r.error)
        else void load()
      })
      .catch((e) => setLeaseError(e instanceof Error ? e.message : 'Could not reach the MiniMax Code desktop app'))
      .finally(() => setLeaseBusy(false))
  }

  const node = (detail?.node ?? null) as ProviderNode | null
  const isCompatible = !!node
  const isAnthropicCompatible = isCompatible && node?.type === 'anthropic-compatible'
  const isNodeEmbedding = isCompatible && node?.type === 'custom-embedding'
  const storageAlias = isCompatible ? detail!.id : (detail?.alias ?? detail?.id ?? '')
  const displayAlias = isCompatible ? String(node?.prefix ?? detail?.id ?? '') : (detail?.uiAlias || detail?.alias || detail?.id || '')

  useEffect(() => {
    if (!detail) return
    void api.disabledFor(storageAlias).then((r) => setDisabledIds(r.ids ?? [])).catch(() => {})
  }, [detail, storageAlias])

  useEffect(() => {
    const fetcher = detail?.modelsFetcher
    if (!fetcher?.url || !fetcher?.type) return
    void api
      .suggestedModels(String(fetcher.url), String(fetcher.type))
      .then((r) => setSuggested(r.data ?? []))
      .catch(() => {})
  }, [detail])

  // Live model catalog (cursor / zed / claude / commandcode) through the first
  // active connection; the reference surfaces zed failures only.
  useEffect(() => {
    if (!detail || !LIVE_MODEL_CATALOG_PROVIDERS.has(detail.id)) return
    const conn = detail.connections.find((c) => c.isActive)
    if (!conn) return
    let alive = true
    void api
      .connectionModels(conn.id)
      .then((r) => {
        if (!alive || !r.models?.length) return
        setDetail((prev) =>
          prev
            ? {
                ...prev,
                models: [
                  ...prev.models,
                  ...r.models.filter((m) => !prev.models.some((x) => x.id === m.id)).map((m) => ({ id: m.id, name: m.name })),
                ],
              }
            : prev,
        )
        setLiveModelsError('')
      })
      .catch((e) => {
        if (alive && detail.id === 'zed') setLiveModelsError(e instanceof Error ? e.message : 'Failed to reach the Zed model catalog.')
      })
    return () => {
      alive = false
    }
  }, [detail])

  const providerStrategy =
    ((settings.providerStrategies as Record<string, { fallbackStrategy?: string; stickyRoundRobinLimit?: number }> | undefined)?.[
      detail?.id ?? ''
    ]?.fallbackStrategy) ?? 'priority'
  const stickyLimit =
    (settings.providerStrategies as Record<string, { stickyRoundRobinLimit?: number }> | undefined)?.[detail?.id ?? '']?.stickyRoundRobinLimit ??
    1
  const thinkingMode =
    (settings.providerThinking as Record<string, { mode?: string }> | undefined)?.[detail?.id ?? '']?.mode ?? 'auto'
  const autoPingKey = AUTO_PING_SETTINGS_KEYS[detail?.id ?? '']
  const autoPing =
    (autoPingKey ? (settings[autoPingKey] as { enabled?: boolean; connections?: Record<string, boolean>; schedules?: Record<string, unknown> }) : undefined) ?? {}
  autoPing.connections ??= {}
  autoPing.schedules ??= {}

  const saveStrategy = async (fallbackStrategy: string, sticky: number) => {
    if (!detail) return
    try {
      const fresh = await api.settings()
      const map = { ...((fresh.providerStrategies as Record<string, unknown>) ?? {}) }
      map[detail.id] =
        fallbackStrategy === 'round-robin'
          ? { fallbackStrategy, stickyRoundRobinLimit: Number(sticky) || 3 }
          : { fallbackStrategy }
      const saved = await api.patchSettings({ providerStrategies: map })
      setSettings(saved)
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to save strategy')
    }
  }

  const saveThinking = async (mode: string) => {
    if (!detail) return
    try {
      const map = { ...((settings.providerThinking as Record<string, unknown>) ?? {}) }
      if (mode === 'auto') delete map[detail.id]
      else map[detail.id] = { mode }
      const saved = await api.patchSettings({ providerThinking: map })
      setSettings(saved)
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to save thinking mode')
    }
  }

  const refreshConnections = useCallback(async () => {
    try {
      const d = await api.provider(id)
      setDetail(d)
      setSelected([])
    } catch {
      /* keep the page as-is */
    }
  }, [id])

  const oneByOneTest = async () => {
    if (!detail || oneByOneRunning) return
    stopRef.current = false
    setOneByOneRunning(true)
    setOneByOneStopping(false)
    const total = detail.connections.length
    setOneByOneSummary({ total, completed: 0, passed: 0, failed: 0, stopped: false })
    const next: Record<string, OneByOneState> = {}
    detail.connections.forEach((c) => (next[c.id] = { state: 'queued' }))
    setOneByOne(next)
    let completed = 0
    let passed = 0
    for (const c of detail.connections) {
      if (stopRef.current) break
      setOneByOne((prev) => ({ ...prev, [c.id]: { state: 'testing' } }))
      try {
        const r = await api.testConnection(c.id)
        completed++
        if (r.valid) passed++
        setOneByOne((prev) => ({ ...prev, [c.id]: { state: r.valid ? 'success' : 'failed', error: r.error } }))
      } catch (e) {
        completed++
        setOneByOne((prev) => ({ ...prev, [c.id]: { state: 'failed', error: e instanceof Error ? e.message : 'Failed' } }))
      }
      setOneByOneSummary({ total, completed, passed, failed: completed - passed, stopped: stopRef.current })
      if (stopRef.current) break
      await new Promise((r) => setTimeout(r, ONE_BY_ONE_DELAY_MS))
    }
    setOneByOneRunning(false)
    setOneByOneStopping(false)
    setOneByOneSummary((prev) => prev && { ...prev, stopped: stopRef.current })
    void refreshConnections()
  }

  const stopOneByOne = () => {
    stopRef.current = true
    setOneByOneStopping(true)
  }

  const bulkDelete = async () => {
    const ids = [...selected]
    try {
      await Promise.all(ids.map((cid) => api.deleteConnection(cid)))
      notify.success(`Deleted ${ids.length} connection${ids.length === 1 ? '' : 's'}`)
      setSelected([])
      await refreshConnections()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Delete failed')
    }
  }

  const applyProxyBulk = async (kind: 'none' | 'rotate' | string) => {
    if (!detail) return
    const active = proxyPools.filter((p) => p.isActive !== false)
    try {
      let i = 0
      for (const c of detail.connections) {
        const poolId = kind === 'none' ? '' : kind === 'rotate' ? active[i++ % Math.max(1, active.length)]?.id ?? '' : kind
        await api.updateConnection(c.id, { providerSpecificData: { proxyPoolId: poolId } })
      }
      setShowBulkProxy(false)
      notify.success('Proxy assignment saved')
      await refreshConnections()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Proxy assignment failed')
    }
  }

  const swapPriority = async (i: number) => {
    if (!detail) return
    const list = detail.connections
    const a = list[i]
    const b = list[i + 1]
    if (!a || !b) return
    const pa = a.priority ?? 0
    const pb = b.priority ?? 0
    setDetail((prev) => {
      if (!prev) return prev
      const conns = prev.connections.map((c) =>
        c.id === a.id ? { ...c, priority: pb } : c.id === b.id ? { ...c, priority: pa } : c,
      )
      return { ...prev, connections: conns }
    })
    try {
      await Promise.all([api.updateConnection(a.id, { priority: pb }), api.updateConnection(b.id, { priority: pa })])
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Priority update failed')
      await refreshConnections()
    }
  }

  const toggleActive = async (c: Connection, next: boolean) => {
    try {
      await api.updateConnection(c.id, { isActive: next })
      setDetail((prev) =>
        prev ? { ...prev, connections: prev.connections.map((x) => (x.id === c.id ? { ...x, isActive: next } : x)) } : prev,
      )
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Update failed')
    }
  }

  const testModel = async (modelId: string) => {
    setTestingModels((prev) => [...prev, modelId])
    setModelsTestError('')
    try {
      const r = await api.testModel(`${storageAlias}/${modelId}`)
      setModelTestResults((prev) => ({ ...prev, [modelId]: r.ok ? 'ok' : 'error' }))
      if (!r.ok && r.error) setModelsTestError(r.error)
    } catch (e) {
      setModelTestResults((prev) => ({ ...prev, [modelId]: 'error' }))
      setModelsTestError(e instanceof Error ? e.message : 'Test failed')
    } finally {
      setTestingModels((prev) => prev.filter((m) => m !== modelId))
    }
  }

  const disableModel = async (modelId: string) => {
    try {
      await api.disableModels(storageAlias, [...disabledIds, modelId])
      setDisabledIds((prev) => [...prev, modelId])
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to disable model')
    }
  }

  const enableModel = async (modelId: string) => {
    try {
      await api.enableModel(storageAlias, modelId)
      setDisabledIds((prev) => prev.filter((x) => x !== modelId))
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to enable model')
    }
  }

  const deleteCustomModel = async (modelId: string) => {
    try {
      await api.deleteCustomModel(storageAlias, modelId)
      await load()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to delete model')
    }
  }

  const bulkDisableModels = async (ids: string[]) => {
    try {
      await api.disableModels(storageAlias, [...disabledIds, ...ids])
      setDisabledIds((prev) => [...prev, ...ids])
      setModelSelection([])
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to disable models')
    }
  }

  const bulkDeleteCustomModels = async (ids: string[]) => {
    try {
      await Promise.all(ids.map((id) => api.deleteCustomModel(storageAlias, id)))
      setModelSelection([])
      await load()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to delete models')
    }
  }

  const deleteAlias = async (alias: string) => {
    try {
      await api.deleteAlias(alias)
      setAliases(await api.aliases().then((r) => r.aliases))
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to remove alias')
    }
  }

  const addCompatibleModel = async () => {
    const modelId = newModelId.trim()
    if (!modelId || addingModel) return
    setAddingModel(true)
    try {
      await api.addCustomModel({ providerAlias: storageAlias, id: modelId, type: 'llm' })
      setNewModelId('')
      await load()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to add model')
    } finally {
      setAddingModel(false)
    }
  }

  // Live /models catalogue for qoder / cline: adds every id not already in the
  // model list. Provider prefixes ("qoder/auto") are stripped like the reference.
  const importLiveModels = async (label: string) => {
    if (importingModels) return
    const conn = detail?.connections.find((c) => c.isActive !== false)
    if (!conn) {
      notify.error(`Please add an active connection first.`)
      return
    }
    setImportingModels(true)
    try {
      const r = await api.connectionModels(conn.id)
      const ids = (r.models ?? [])
        .map((m) => (m.id || m.name || '').replace(/^(qoder-cn|qoder)\//, ''))
        .filter(Boolean)
      if (!ids.length) {
        notify.error('No models returned.')
        return
      }
      const seen = new Set([...displayModels.map((m) => m.id), ...customModelRows.map((m) => m.id)])
      let added = 0
      for (const modelId of ids) {
        if (seen.has(modelId)) continue
        await api.addCustomModel({ providerAlias: storageAlias, id: modelId, type: 'llm' }).catch(() => {})
        seen.add(modelId)
        added++
      }
      await load()
      if (added === 0) notify.error('All models already exist, no new models added.')
      else notify.success(`${label}: added ${added} model${added === 1 ? '' : 's'}.`)
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to fetch models')
    } finally {
      setImportingModels(false)
    }
  }

  const importCompatibleModels = async () => {
    if (importingModels) return
    const conn = detail?.connections.find((c) => c.isActive !== false)
    if (!conn) return
    setImportingModels(true)
    try {
      const r = await api.connectionModels(conn.id)
      const models = (r.models ?? []).map((m) => m.id || m.name).filter(Boolean) as string[]
      if (!models.length) {
        notify.error('No models returned from /models.')
        return
      }
      let added = 0
      for (const modelId of models) {
        if (compatibleRows.some((m) => m.id === modelId)) continue
        await api.addCustomModel({ providerAlias: storageAlias, id: modelId, type: 'llm' }).catch(() => {})
        added++
      }
      if (added === 0) notify.error('No new models were added.')
      else await load()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to import models')
    } finally {
      setImportingModels(false)
    }
  }

  const thinkingLevels = useMemo(() => {
    const set = new Set<string>()
    Object.values(detail?.modelMeta ?? {}).forEach((m) => (m.thinkingLevels ?? []).forEach((l) => l !== 'none' && set.add(l)))
    return set.size ? ['auto', ...set] : []
  }, [detail])

  const thinkingSuffix = (modelId: string) => {
    if (!thinkingMode || thinkingMode === 'auto') return null
    const levels = detail?.modelMeta?.[modelId]?.thinkingLevels ?? []
    return levels.includes(thinkingMode) ? thinkingMode : null
  }

  if (error) {
    return (
      <div>
        <Button variant="ghost" size="sm" onClick={() => navigate('/dashboard/providers')}>
          <Icon name="arrow_back" /> Back
        </Button>
        <p className="mt-6 text-sm text-destructive">{error}</p>
      </div>
    )
  }
  if (!detail) return <p className="py-12 text-center text-xs text-muted-foreground">Loading…</p>

  const customModelRows = customModels.filter(
    (m) => m.providerAlias === storageAlias && (m.type ?? 'llm') === 'llm',
  )
  const aliasFor = (modelId: string) =>
    Object.entries(aliases).find(([, v]) => v === `${storageAlias}/${modelId}` || v === `${detail.id}/${modelId}`)?.[0]
  const llmModels = detail.models.filter((m) => !m.kind || m.kind === 'llm')
  const disabledSet = new Set(disabledIds)
  const displayModels = llmModels.filter((m) => !disabledSet.has(m.id))
  const disabledModels = llmModels.filter((m) => disabledSet.has(m.id))
  const suggestedNotAdded = suggested.filter(
    (m) => !llmModels.some((x) => x.id === m.id) && !customModelRows.some((x) => x.id === m.id),
  )
  const selectableModelIds = [...customModelRows.map((m) => m.id), ...displayModels.map((m) => m.id)]
  const selectedCustomIds = modelSelection.filter((mid) => customModelRows.some((m) => m.id === mid))
  const selectedCatalogIds = modelSelection.filter((mid) => displayModels.some((m) => m.id === mid))
  // Compatible-node model list: custom rows plus legacy aliases pointing at
  // this node's alias (getProviderCustomModelRows in the reference).
  // Plain computation, not useMemo: this sits after the early returns below, and
  // a hook there would change the hook count between renders.
  const compatibleRows: Array<{ id: string; source: 'custom' | 'legacyAlias'; alias?: string }> = []
  if (isCompatible) {
    const seen = new Set<string>()
    for (const m of customModelRows) {
      if (seen.has(m.id)) continue
      seen.add(m.id)
      compatibleRows.push({ id: m.id, source: 'custom' })
    }
    const prefix = `${storageAlias}/`
    for (const [alias, full] of Object.entries(aliases)) {
      if (!full.startsWith(prefix)) continue
      const mid = full.slice(prefix.length)
      if (!mid || seen.has(mid)) continue
      seen.add(mid)
      compatibleRows.push({ id: mid, source: 'legacyAlias', alias })
    }
  }
  const compatibleImportReady = detail.connections.some((c) => c.isActive !== false)
  const allSelected = detail.connections.length > 0 && selected.length === detail.connections.length
  const authModes = detail.authModes ?? []
  const isOAuth = authModes.includes('oauth') || detail.hasOAuth === true
  const supportsApiKey = authModes.includes('apikey') || detail.authType === 'apikey' || detail.authType === 'cookie'
  const dual = !isCompatible && isOAuth && supportsApiKey
  const website = detail.display?.notice?.apiKeyUrl || detail.display?.notice?.signupUrl || detail.display?.website
  const nodeEndpoint = isAnthropicCompatible
    ? 'messages'
    : node?.apiType === 'responses'
      ? 'responses'
      : isNodeEmbedding
        ? 'embeddings'
        : 'chat/completions'

  return (
    <>
      <button
        type="button"
        className="mb-4 -ml-1 inline-flex items-center gap-1.5 text-xs text-text-muted transition-colors hover:text-text-main"
        onClick={() => navigate('/dashboard/providers')}
      >
        <Icon name="arrow_back" className="text-[15px]" /> Back to Providers
      </button>

      <div className="mb-6 flex min-w-0 items-center gap-3">
        <div
          className="flex size-11 shrink-0 items-center justify-center overflow-hidden rounded-lg"
          style={{ backgroundColor: `${detail.display?.color ?? '#888'}15` }}
        >
          <ProviderIcon provider={detail.id} size={44} fallbackText={detail.display?.textIcon} />
        </div>
        <div className="min-w-0">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <h1 className="truncate text-2xl font-semibold tracking-tight text-text-main">{detail.display?.name ?? detail.id}</h1>
            {website && (
              <a
                href={website}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1 text-xs text-text-muted transition-colors hover:text-text-main hover:underline"
              >
                <Icon name="open_in_new" className="text-[13px]" />
                {detail.display?.notice?.apiKeyUrl ? 'Get API Key' : 'Sign up / Learn more'}
              </a>
            )}
          </div>
          <p className="mt-0.5 text-xs text-text-muted">
            {detail.connections.length} connection{detail.connections.length === 1 ? '' : 's'}
          </p>
        </div>
      </div>

      {detail.display?.deprecated && (
        <div className="mb-4 flex items-start gap-2.5 rounded-xl bg-surface-2 px-4 py-3">
          <Icon name="warning" className="mt-px shrink-0 text-[15px] text-warning" />
          <p className="text-xs leading-relaxed text-text-muted">{detail.display.deprecationNotice}</p>
        </div>
      )}

      {detail.display?.notice?.text && !detail.display?.deprecated && (
        <div className="mb-4 flex flex-col gap-2 rounded-xl bg-surface-2 px-4 py-3 sm:flex-row sm:items-center">
          <Icon name="info" className="shrink-0 text-[15px] text-text-muted" />
          <p className="min-w-0 flex-1 text-xs leading-relaxed text-text-muted">{detail.display.notice.text}</p>
          {detail.display.notice.apiKeyUrl && (
            <a
              href={detail.display.notice.apiKeyUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex shrink-0 items-center gap-1 rounded-lg bg-text-main px-2.5 py-1 text-[11px] font-medium text-background transition-opacity hover:opacity-90"
            >
              Get API Key <Icon name="arrow_forward" className="text-[12px]" />
            </a>
          )}
        </div>
      )}

      {isCompatible && node && (
        <section className="mb-4 overflow-hidden rounded-xl bg-surface">
          <div className="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="min-w-0">
              <h2 className="text-sm font-semibold text-text-main">
                {isAnthropicCompatible ? 'Anthropic Compatible Details' : isNodeEmbedding ? 'Custom Embedding Details' : 'OpenAI Compatible Details'}
              </h2>
              <p className="mt-0.5 break-all text-xs text-text-muted">
                {isAnthropicCompatible ? 'Messages API' : node.apiType === 'responses' ? 'Responses API' : isNodeEmbedding ? 'Embeddings API' : 'Chat Completions'} ·{' '}
                <span className="font-mono">
                  {String(node.baseUrl ?? '').replace(/\/$/, '')}/{nodeEndpoint}
                </span>
              </p>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Button size="sm" variant="secondary" onClick={() => setShowAddKey(true)}>
                <Icon name="add" /> Add API Key
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setShowEditNode(true)}>
                <Icon name="edit" /> Edit
              </Button>
              <Button
                size="sm"
                variant="ghost"
                className="text-text-muted hover:text-danger"
                onClick={() =>
                  setConfirm({
                    title: 'Delete Compatible Node',
                    message: `Delete this ${isAnthropicCompatible ? 'Anthropic' : 'OpenAI'} Compatible node?`,
                    onConfirm: async () => {
                      await api.deleteProviderNode(detail.id)
                      setConfirm(null)
                      navigate('/dashboard/providers')
                    },
                  })
                }
              >
                <Icon name="delete" /> Delete
              </Button>
            </div>
          </div>
        </section>
      )}

      {detail.noAuth && (
        <NoAuthProxyCard providerId={detail.id} proxyPools={proxyPools} settings={settings} onSaved={setSettings} />
      )}

      {!detail.noAuth && (
        <section className="mb-4 overflow-hidden rounded-xl bg-surface">
          <div className="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-center gap-2">
              <h2 className="text-sm font-semibold text-text-main">Connections</h2>
              <span className="font-mono text-[11px] text-text-muted">{detail.connections.length}</span>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              {detail.connections.length > 0 && proxyPools.length > 0 && (
                <Button size="sm" variant="ghost" onClick={() => setShowBulkProxy(true)}>
                  <Icon name="lan" /> Apply Proxy
                </Button>
              )}
              {selected.length > 0 && (
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-text-muted hover:text-danger"
                  onClick={() =>
                    setConfirm({
                      title: 'Delete Connections',
                      message: `Delete ${selected.length} selected connection${selected.length === 1 ? '' : 's'}? This cannot be undone.`,
                      onConfirm: async () => {
                        await bulkDelete()
                        setConfirm(null)
                      },
                    })
                  }
                >
                  <Icon name="delete" /> Delete ({selected.length})
                </Button>
              )}
              {detail.connections.length > 0 && (
                <Button size="sm" variant="ghost" onClick={oneByOneTest} disabled={oneByOneRunning}>
                  <Icon name="sync" /> {oneByOneRunning ? 'Testing…' : 'Test All'}
                </Button>
              )}
              {oneByOneRunning && (
                <Button size="sm" variant="ghost" onClick={stopOneByOne} disabled={oneByOneStopping}>
                  <Icon name="stop" /> {oneByOneStopping ? 'Stopping…' : 'Stop'}
                </Button>
              )}
              <button
                type="button"
                aria-pressed={providerStrategy === 'round-robin'}
                onClick={() => void saveStrategy(providerStrategy === 'round-robin' ? 'priority' : 'round-robin', stickyLimit)}
                className={cn(
                  'inline-flex h-7 items-center rounded-lg px-2.5 text-[11px] font-medium transition-colors',
                  providerStrategy === 'round-robin'
                    ? 'bg-text-main text-background'
                    : 'bg-surface-2 text-text-muted hover:text-text-main',
                )}
              >
                Round Robin
              </button>
              {providerStrategy === 'round-robin' && (
                <label className="flex items-center gap-1.5 text-[11px] text-text-muted">
                  Sticky
                  <Input
                    type="number"
                    min={1}
                    className="h-7 w-14 border-0 bg-surface-2 px-2 text-xs shadow-none"
                    value={stickyLimit}
                    onChange={(e) => void saveStrategy('round-robin', Number.parseInt(e.target.value, 10) || 1)}
                  />
                </label>
              )}
            </div>
          </div>

          <div className="border-t border-border px-2 py-2">
            {detail.connections.length === 0 ? (
              <div className="flex flex-col gap-3 rounded-lg bg-surface-2 px-4 py-4 sm:flex-row sm:items-center sm:justify-between">
                <div className="flex min-w-0 items-center gap-2.5">
                  <Icon name={isOAuth ? 'lock' : 'key'} className="shrink-0 text-[16px] text-text-muted" />
                  <div className="min-w-0">
                    <p className="text-sm text-text-main">No connections yet</p>
                    {dual && <p className="text-xs text-text-muted">Choose OAuth or API Key.</p>}
                  </div>
                </div>
                <div className="flex flex-wrap gap-2">
                  {dual ? (
                    <>
                      <Button size="sm" variant="secondary" onClick={() => setShowOAuth(true)}>
                        <Icon name="lock" /> OAuth
                      </Button>
                      <Button size="sm" onClick={() => setShowAddKey(true)}>
                        <Icon name="key" /> API Key
                      </Button>
                    </>
                  ) : (
                    <>
                      {detail.id === 'iflow' && (
                        <Button size="sm" variant="secondary" onClick={() => setShowIFlowCookie(true)}>
                          <Icon name="cookie" /> Cookie
                        </Button>
                      )}
                      {detail.id === 'codex' && (
                        <Button size="sm" variant="secondary" onClick={() => setShowBulkImport(true)}>
                          <Icon name="playlist_add" /> Bulk Add
                        </Button>
                      )}
                      {detail.id === 'grok-cli' && (
                        <Button size="sm" variant="secondary" onClick={() => setShowBulkImport(true)}>
                          <Icon name="playlist_add" /> Bulk Add
                        </Button>
                      )}
                      {IMPORT_LANES[detail.id] && (
                        <Button
                          size="sm"
                          variant="secondary"
                          onClick={() => setImportTarget(IMPORT_LANES[detail.id])}
                          title={IMPORT_TITLES[IMPORT_LANES[detail.id]]}
                        >
                          <Icon name="download" /> {IMPORT_LABELS[IMPORT_LANES[detail.id]]}
                        </Button>
                      )}
                      {(detail.id === 'minimax-code' || detail.id === 'minimax-code-global') && (
                        <Button
                          size="sm"
                          variant="secondary"
                          disabled={leaseBusy}
                          onClick={runLeaseConnect}
                          title="Borrow the signed-in MiniMax Code desktop app's login, no second sign-in, refresh stays with the desktop app"
                        >
                          <Icon name="desktop_windows" /> {leaseBusy ? 'Connecting…' : 'Use Desktop App'}
                        </Button>
                      )}
                      {isOAuth && !supportsApiKey ? (
                        <Button size="sm" onClick={() => setShowOAuth(true)}>
                          <Icon name="lock" /> {isCompatible ? 'Add API Key' : 'Add Connection'}
                        </Button>
                      ) : (
                        <Button size="sm" onClick={() => setShowAddKey(true)}>
                          <Icon name="add" /> {isCompatible ? 'Add API Key' : 'Add Connection'}
                        </Button>
                      )}
                    </>
                  )}
                </div>
              </div>
            ) : (
              <>
                {oneByOneSummary && (
                  <div className="mb-2 rounded-lg bg-surface-2 px-3 py-2 text-[11px] text-text-muted">
                    <div className="flex flex-wrap items-center gap-3">
                      <span>Total {oneByOneSummary.total}</span>
                      <span>Completed {oneByOneSummary.completed}</span>
                      <span>Passed {oneByOneSummary.passed}</span>
                      <span>Failed {oneByOneSummary.failed}</span>
                      {oneByOneSummary.stopped && <span className="text-warning">Stopped</span>}
                    </div>
                  </div>
                )}
                <div className="mb-2 flex items-center gap-2 px-2">
                  <label className="flex cursor-pointer items-center gap-1.5 text-xs text-text-muted transition-colors hover:text-text-main">
                    <input
                      type="checkbox"
                      className="size-3.5 rounded border-border"
                      checked={allSelected}
                      onChange={(e) =>
                        setSelected(e.target.checked ? detail.connections.map((c) => c.id) : [])
                      }
                    />
                    Select all
                  </label>
                </div>
                {(detail.id === 'free-pi' || detail.id === 'freebuff') && detail.connections[0] && (
                  <SponsoredAdBanner
                    provider={detail.id}
                    connectionId={detail.connections[0].id}
                  />
                )}
                <div className="flex max-h-[1132px] flex-col gap-2 overflow-y-auto overscroll-contain pr-1">
                  {detail.connections.map((c, i) => (
                    <ConnectionRow
                      key={c.id}
                      conn={c}
                      proxyPools={proxyPools}
                      checked={selected.includes(c.id)}
                      onCheck={(v) =>
                        setSelected((prev) => (v ? [...prev, c.id] : prev.filter((x) => x !== c.id)))
                      }
                      onToggleActive={(v) => void toggleActive(c, v)}
                      onEdit={() => setShowEditConnection(c)}
                      onDelete={() =>
                        setConfirm({
                          title: 'Delete Connection',
                          message: `Delete connection "${c.name || c.email || c.provider}"?`,
                          onConfirm: async () => {
                            await api.deleteConnection(c.id)
                            setConfirm(null)
                            await refreshConnections()
                          },
                        })
                      }
                      onProxyChange={(poolId) => {
                        void api
                          .updateConnection(c.id, { providerSpecificData: { proxyPoolId: poolId } })
                          .then(refreshConnections)
                          .catch((e) => notify.error(e instanceof Error ? e.message : 'Proxy update failed'))
                      }}
                      onMoveUp={i > 0 ? () => void swapPriority(i - 1) : undefined}
                      onMoveDown={i < detail.connections.length - 1 ? () => void swapPriority(i) : undefined}
                      testState={oneByOne[c.id]}
                      onAutoPing={autoPingKey ? () => setScheduleConnection(c) : undefined}
                    />
                  ))}
                </div>
                <div className="mt-2 flex flex-wrap gap-2 px-2 pb-1">
                  {(detail.id === 'minimax-code' || detail.id === 'minimax-code-global') && (
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={leaseBusy}
                      onClick={runLeaseConnect}
                      title="Borrow the signed-in MiniMax Code desktop app's login, no second sign-in, refresh stays with the desktop app"
                    >
                      <Icon name="desktop_windows" /> {leaseBusy ? 'Connecting…' : 'Use Desktop App'}
                    </Button>
                  )}
                  {detail.id === 'iflow' && (
                    <Button size="sm" variant="ghost" onClick={() => setShowIFlowCookie(true)}>
                      <Icon name="cookie" /> Cookie
                    </Button>
                  )}
                  {detail.id === 'codex' && (
                    <Button size="sm" variant="ghost" onClick={() => setShowBulkImport(true)}>
                      <Icon name="playlist_add" /> Bulk Add
                    </Button>
                  )}
                  {detail.id === 'grok-cli' && (
                    <Button size="sm" variant="ghost" onClick={() => setShowBulkImport(true)}>
                      <Icon name="playlist_add" /> Bulk Add
                    </Button>
                  )}
                  {IMPORT_LANES[detail.id] && (
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => setImportTarget(IMPORT_LANES[detail.id])}
                      title={IMPORT_TITLES[IMPORT_LANES[detail.id]]}
                    >
                      <Icon name="download" /> {IMPORT_LABELS[IMPORT_LANES[detail.id]]}
                    </Button>
                  )}
                  {dual ? (
                    <>
                      <Button size="sm" variant="ghost" onClick={() => setShowOAuth(true)}>
                        <Icon name="lock" /> OAuth
                      </Button>
                      <Button size="sm" variant="secondary" onClick={() => setShowAddKey(true)}>
                        <Icon name="key" /> API Key
                      </Button>
                    </>
                  ) : isOAuth && !supportsApiKey ? (
                    <Button size="sm" variant="ghost" onClick={() => setShowOAuth(true)}>
                      <Icon name="lock" /> Add
                    </Button>
                  ) : (
                    <Button size="sm" variant="ghost" onClick={() => setShowAddKey(true)}>
                      <Icon name="add" /> Add
                    </Button>
                  )}
                </div>
              </>
            )}
            {leaseError && <p className="mt-3 px-2 text-xs text-danger">{leaseError}</p>}
          </div>
        </section>
      )}

      {!isCompatible && !detail.noAuth && <CustomConfigCard providerId={detail.id} />}

      <section className="mt-4 overflow-hidden rounded-xl bg-surface">
          <div className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-sm font-semibold text-text-main">Available Models</h2>
              <span className="font-mono text-[11px] text-text-muted">{displayModels.length + customModelRows.length}</span>
              {thinkingLevels.length > 0 && (
                <Select value={thinkingMode} onValueChange={(v) => void saveThinking(v)}>
                  <SelectTrigger
                    aria-label="Thinking level"
                    title="Appends (level) suffix to copied model names"
                    className="h-7 w-auto gap-1.5 rounded-lg border-0 bg-surface-2 px-2 text-[11px] text-text-muted hover:bg-surface-3 hover:text-text-main"
                  >
                    <span className="flex items-center gap-1.5">
                      <Icon name="psychology" className="text-[14px] text-violet-600 dark:text-violet-400" />
                      <span>Thinking</span>
                      <SelectValue className="font-medium text-text-main" />
                    </span>
                  </SelectTrigger>
                  <SelectContent className="min-w-36">
                    {thinkingLevels.map((opt) => (
                      <SelectItem key={opt} value={opt} className="text-xs">
                        {opt.charAt(0).toUpperCase() + opt.slice(1)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </div>
            {!isCompatible && (
              <div className="flex flex-wrap items-center justify-end gap-1.5">
                {selectModels ? (
                  <>
                    <label className="flex cursor-pointer items-center gap-1.5 text-xs text-text-muted hover:text-text-main">
                      <input
                        type="checkbox"
                        className="size-3.5 rounded border-border"
                        checked={modelSelection.length > 0 && modelSelection.length === selectableModelIds.length}
                        onChange={(e) => setModelSelection(e.target.checked ? selectableModelIds : [])}
                      />
                      {modelSelection.length > 0 ? `${modelSelection.length} selected` : 'Select all'}
                    </label>
                    {selectedCatalogIds.length > 0 && (
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() =>
                          setConfirm({
                            title: 'Disable Selected Models',
                            message: `Disable ${selectedCatalogIds.length} selected model${selectedCatalogIds.length === 1 ? '' : 's'}?`,
                            onConfirm: async () => {
                              await bulkDisableModels(selectedCatalogIds)
                              setConfirm(null)
                            },
                          })
                        }
                      >
                        <Icon name="block" /> Disable ({selectedCatalogIds.length})
                      </Button>
                    )}
                    {selectedCustomIds.length > 0 && (
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-text-muted hover:text-danger"
                        onClick={() =>
                          setConfirm({
                            title: 'Delete Selected Models',
                            message: `Delete ${selectedCustomIds.length} custom model${selectedCustomIds.length === 1 ? '' : 's'}? This cannot be undone.`,
                            onConfirm: async () => {
                              await bulkDeleteCustomModels(selectedCustomIds)
                              setConfirm(null)
                            },
                          })
                        }
                      >
                        <Icon name="delete" /> Delete ({selectedCustomIds.length})
                      </Button>
                    )}
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => {
                        setSelectModels(false)
                        setModelSelection([])
                      }}
                    >
                      Cancel
                    </Button>
                  </>
                ) : (
                  <>
                    {disabledIds.length > 0 && (
                      <Button size="sm" variant="ghost" onClick={async () => {
                        await Promise.all(disabledIds.map((mid) => api.enableModel(storageAlias, mid).catch(() => {})))
                        setDisabledIds([])
                      }}>
                        <Icon name="restart_alt" /> Active All
                      </Button>
                    )}
                    {selectableModelIds.length > 0 && (
                      <Button size="sm" variant="ghost" aria-label="Select models" onClick={() => setSelectModels(true)}>
                        <Icon name="checklist" /> Select
                      </Button>
                    )}
                    {displayModels.length > 0 && (
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() =>
                          setConfirm({
                            title: 'Disable All Models',
                            message: `Disable all ${displayModels.length} models for this provider?`,
                            onConfirm: async () => {
                              const ids = displayModels.map((m) => m.id)
                              await api.disableModels(storageAlias, [...disabledIds, ...ids])
                              setDisabledIds((prev) => [...prev, ...ids])
                              setConfirm(null)
                            },
                          })
                        }
                      >
                        <Icon name="block" /> Disable All
                      </Button>
                    )}
                    <Button size="sm" variant="secondary" onClick={() => setShowCustomModel(true)}>
                      <Icon name="add" /> Add Model
                    </Button>
                  </>
                )}
              </div>
            )}
          </div>

          <div className="border-t border-border px-2 py-2">

          {!!modelsTestError && (
            <div className="mb-3">
              <p className="break-words text-xs text-danger">{modelsTestError}</p>
              {/RegionError|hosted in China|regionNotAllowed/i.test(modelsTestError) && (
                <a
                  href={regionAllowUrl(modelsTestError)}
                  target="_blank"
                  rel="noreferrer"
                  className="mt-1.5 inline-flex items-center gap-1 rounded-md bg-warning/10 px-2 py-0.5 text-xs font-medium text-warning transition-colors hover:bg-warning/20"
                >
                  Allow China-hosted models
                  <Icon name="open_in_new" className="text-[13px]" />
                </a>
              )}
            </div>
          )}
          {detail.id === 'zed' && !!liveModelsError && (
            <p className="mb-3 break-words text-xs text-danger">{liveModelsError}</p>
          )}

          {isCompatible ? (
            <div className="flex flex-col gap-3">
              <p className="text-xs text-text-muted">
                Add {isAnthropicCompatible ? 'Anthropic' : 'OpenAI'}-compatible models manually or import them from the /models endpoint.
              </p>
              <div className="flex flex-wrap items-end gap-2">
                <div className="min-w-[240px] flex-1">
                  <label htmlFor="new-compatible-model" className="mb-1 block text-[11px] text-text-muted">Model ID</label>
                  <Input
                    id="new-compatible-model"
                    className="border-0 bg-surface-2 shadow-none"
                    value={newModelId}
                    onChange={(e) => setNewModelId(e.target.value)}
                    onKeyDown={(e) => e.key === 'Enter' && void addCompatibleModel()}
                    placeholder={isAnthropicCompatible ? 'claude-3-opus-20240229' : 'gpt-4o'}
                  />
                </div>
                <Button size="sm" onClick={() => void addCompatibleModel()} disabled={!newModelId.trim() || addingModel}>
                  <Icon name="add" /> {addingModel ? 'Adding…' : 'Add'}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => void importCompatibleModels()} disabled={!compatibleImportReady || importingModels}>
                  <Icon name="download" /> {importingModels ? 'Importing…' : 'Import from /models'}
                </Button>
              </div>
              {!compatibleImportReady && (
                <p className="text-[11px] text-text-muted">Add a connection to enable importing models.</p>
              )}
              {compatibleRows.length > 0 && (
                <div className="flex flex-col gap-2">
                  {compatibleRows.map((m) => (
                    <div
                      key={`${m.source}-${m.id}`}
                      className="flex items-center gap-3 rounded-lg bg-surface-2 px-3 py-2 transition-colors hover:bg-surface-3"
                    >
                      <Icon
                        name={modelTestResults[m.id] === 'ok' ? 'check_circle' : modelTestResults[m.id] === 'error' ? 'cancel' : 'science'}
                        className={cn('shrink-0 text-[15px]', modelTestResults[m.id] === 'ok' ? 'text-emerald-600 dark:text-emerald-400' : modelTestResults[m.id] === 'error' ? 'text-danger' : 'text-text-muted')}
                      />
                      <div className="min-w-0 flex-1">
                        <p className="truncate text-sm font-medium text-text-main">{m.id}</p>
                        <div className="mt-0.5 flex items-center gap-1">
                          <code className="rounded bg-card px-1.5 py-0.5 font-mono text-[11px] text-text-muted">
                            {displayAlias}/{m.id}
                          </code>
                          <CopyButton value={`${displayAlias}/${m.id}`} />
                          {detail.connections.length > 0 && (
                            <button
                              className="flex size-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-3 hover:text-text-main"
                              onClick={() => void testModel(m.id)}
                              title="Test"
                            >
                              <Icon
                                name={testingModels.includes(m.id) ? 'progress_activity' : 'science'}
                                className={testingModels.includes(m.id) ? 'animate-spin text-[16px]' : 'text-[16px]'}
                              />
                            </button>
                          )}
                        </div>
                      </div>
                      <button
                        className="flex size-7 shrink-0 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-3 hover:text-danger"
                        title="Remove model"
                        onClick={() => void (m.source === 'custom' ? deleteCustomModel(m.id) : deleteAlias(m.alias!))}
                      >
                        <Icon name="delete" className="text-[16px]" />
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ) : (
          <div className="grid grid-cols-1 gap-2 lg:grid-cols-2">
            {customModelRows.map((m) => (
              <ModelRow
                key={`custom-${m.id}`}
                fullModel={`${displayAlias}/${m.id}`}
                alias={aliasFor(m.id)}
                testStatus={modelTestResults[m.id]}
                testing={testingModels.includes(m.id)}
                onTest={
                  detail.connections.length > 0 || detail.noAuth ? () => void testModel(m.id) : undefined
                }
                onDelete={() => void deleteCustomModel(m.id)}
                onSetAlias={() => setAliasTarget(m.id)}
                caps={detail.modelMeta?.[m.id]?.caps}
                thinkingSuffix={thinkingSuffix(m.id)}
                custom
                selected={modelSelection.includes(m.id)}
                onSelect={selectModels ? (v) => setModelSelection((prev) => (v ? [...prev, m.id] : prev.filter((x) => x !== m.id))) : undefined}
              />
            ))}

            {displayModels.map((m) => (
              <ModelRow
                key={m.id}
                fullModel={`${displayAlias}/${m.id}`}
                alias={aliasFor(m.id)}
                testStatus={modelTestResults[m.id]}
                testing={testingModels.includes(m.id)}
                onTest={
                  detail.connections.length > 0 || detail.noAuth ? () => void testModel(m.id) : undefined
                }
                onDisable={() => void disableModel(m.id)}
                onSetAlias={() => setAliasTarget(m.id)}
                caps={detail.modelMeta?.[m.id]?.caps}
                thinkingSuffix={thinkingSuffix(m.id)}
                selected={modelSelection.includes(m.id)}
                onSelect={selectModels ? (v) => setModelSelection((prev) => (v ? [...prev, m.id] : prev.filter((x) => x !== m.id))) : undefined}
              />
            ))}

            {(detail.id === 'qoder' || detail.id === 'qoder-cn') &&
              detail.connections.some((c) => c.isActive !== false) && (
                <button
                  onClick={() => void importLiveModels('Fetch Qoder Models')}
                  disabled={importingModels}
                  className="flex items-center justify-center gap-1.5 rounded-lg bg-surface-2 px-3 py-2 text-xs text-text-muted transition-colors hover:bg-surface-3 hover:text-text-main disabled:opacity-50 lg:col-span-full"
                >
                  <Icon name={importingModels ? 'progress_activity' : 'download'} className={importingModels ? 'animate-spin text-[15px]' : 'text-[15px]'} />
                  {importingModels ? 'Fetching…' : 'Fetch Qoder Models'}
                </button>
              )}

            {(detail.id === 'cline' || detail.id === 'clinepass') &&
              detail.connections.some((c) => c.isActive !== false) && (
                <button
                  onClick={() => void importLiveModels('Import from /models')}
                  disabled={importingModels}
                  className="flex items-center justify-center gap-1.5 rounded-lg bg-surface-2 px-3 py-2 text-xs text-text-muted transition-colors hover:bg-surface-3 hover:text-text-main disabled:opacity-50 lg:col-span-full"
                >
                  <Icon name={importingModels ? 'progress_activity' : 'download'} className={importingModels ? 'animate-spin text-[15px]' : 'text-[15px]'} />
                  {importingModels ? 'Fetching…' : 'Import from /models'}
                </button>
              )}

            {suggestedNotAdded.length > 0 && (
              <div className="lg:col-span-full">
                <p className="mb-1.5 px-1 text-[11px] text-text-muted">Suggested free models (≥200k context)</p>
                <div className="flex flex-wrap gap-1.5">
                  {suggestedNotAdded.map((m) => (
                    <button
                      key={m.id}
                      onClick={async () => {
                        await api.addCustomModel({ providerAlias: storageAlias, id: m.id, type: 'llm' })
                        await load()
                      }}
                      className="flex items-center gap-1 rounded-lg bg-surface-2 px-2 py-1 font-mono text-[11px] text-text-muted transition-colors hover:bg-surface-3 hover:text-text-main"
                      title={`${m.name ?? m.id} · ${Math.round((m.contextLength ?? 0) / 1000)}k ctx`}
                    >
                      <Icon name="add" className="text-[13px]" />
                      {m.id.split('/').pop()}
                    </button>
                  ))}
                </div>
              </div>
            )}

            {disabledModels.length > 0 && (
              <div className="lg:col-span-full">
                <p className="mb-1.5 px-1 text-[11px] text-text-muted">Disabled models ({disabledModels.length})</p>
                <div className="flex flex-wrap gap-1.5">
                  {disabledModels.map((m) => (
                    <button
                      key={m.id}
                      onClick={() => void enableModel(m.id)}
                      className="flex items-center gap-1 rounded-lg bg-surface-2 px-2 py-1 font-mono text-[11px] text-text-muted transition-colors hover:bg-surface-3 hover:text-text-main"
                      title="Restore model"
                    >
                      <Icon name="add" className="text-[13px]" />
                      {m.id}
                    </button>
                  ))}
                </div>
              </div>
            )}
          </div>
          )}
          </div>
        </section>

      {showAddKey && (
        <AddApiKeyModal
          provider={detail.id}
          providerName={detail.display?.name ?? detail.id}
          isCompatible={isCompatible}
          isAnthropic={isAnthropicCompatible}
          authType={detail.authType}
          authHint={detail.authHint}
          website={detail.display?.website}
          credentialForm={detail.credentialForm}
          regions={detail.regions}
          defaultRegion={detail.defaultRegion}
          existingNames={detail.connections.map((c) => c.name).filter(Boolean) as string[]}
          proxyPools={proxyPools}
          onClose={() => setShowAddKey(false)}
          onSaved={async () => {
            setShowAddKey(false)
            await refreshConnections()
          }}
        />
      )}

      {showOAuth && (
        <OAuthModal
          provider={detail.id}
          onClose={() => setShowOAuth(false)}
          onSuccess={async () => {
            setShowOAuth(false)
            notify.success('Connection added')
            await refreshConnections()
          }}
        />
      )}

      {showIFlowCookie && (
        <IFlowCookieModal
          onClose={() => setShowIFlowCookie(false)}
          onSaved={async () => {
            setShowIFlowCookie(false)
            await refreshConnections()
          }}
        />
      )}

      {importTarget && (
        <CredentialImportModal
          lane={importTarget}
          onClose={() => setImportTarget(null)}
          onSaved={async () => {
            setImportTarget(null)
            await refreshConnections()
          }}
        />
      )}

      {showBulkImport && (
        <BulkImportModal
          provider={detail.id === 'grok-cli' ? 'grok-cli' : 'codex'}
          onClose={() => setShowBulkImport(false)}
          onSaved={async () => {
            setShowBulkImport(false)
            await refreshConnections()
          }}
        />
      )}

      {showBulkProxy && (
        <Modal isOpen onClose={() => setShowBulkProxy(false)} title="Apply Proxy Pool">
          <p className="mb-3 text-xs text-text-muted">Assign a proxy pool to every connection of this provider.</p>
          <div className="flex flex-col gap-2">
            <Button variant="secondary" onClick={() => void applyProxyBulk('rotate')}>
              One-to-one (rotate)
            </Button>
            <Button variant="secondary" onClick={() => void applyProxyBulk('none')}>
              None (unbind all)
            </Button>
            {proxyPools.map((p) => (
              <Button key={p.id} variant="outline" onClick={() => void applyProxyBulk(p.id)}>
                {p.name ?? p.id}
              </Button>
            ))}
          </div>
        </Modal>
      )}

      {showEditConnection && (
        <EditConnectionModal
          isOpen
          connection={showEditConnection}
          onClose={() => setShowEditConnection(null)}
          onSave={async (updates) => {
            await api.updateConnection(showEditConnection.id, updates)
            setShowEditConnection(null)
            await refreshConnections()
          }}
        />
      )}

      {showEditNode && node && (
        <EditNodeModal
          node={node}
          onClose={() => setShowEditNode(false)}
          onSaved={async () => {
            setShowEditNode(false)
            await load()
          }}
        />
      )}

      {showCustomModel && (
        <AddCustomModelModal
          providerAlias={storageAlias}
          displayAlias={displayAlias}
          onClose={() => setShowCustomModel(false)}
          onSaved={async () => {
            setShowCustomModel(false)
            await load()
          }}
        />
      )}

      {aliasTarget && (
        <AliasModal
          modelId={aliasTarget}
          fullModel={`${storageAlias}/${aliasTarget}`}
          current={aliasFor(aliasTarget)}
          onClose={() => setAliasTarget(null)}
          onSaved={async () => {
            setAliasTarget(null)
            setAliases((await api.aliases()).aliases)
          }}
        />
      )}

      {scheduleConnection && detail && autoPingKey && (
        <AutoPingScheduleModal
          connection={scheduleConnection}
          reactive={autoPing.connections?.[scheduleConnection.id] === true}
          schedule={autoPing.schedules?.[scheduleConnection.id] ?? null}
          onClose={() => setScheduleConnection(null)}
          onSave={(result) => {
            const next = {
              ...autoPing,
              enabled: autoPing.enabled ?? true,
              connections: { ...(autoPing.connections ?? {}), [scheduleConnection.id]: result.reactive },
              schedules: { ...(autoPing.schedules ?? {}), [scheduleConnection.id]: result.schedule },
            }
            void api
              .patchSettings({ [autoPingKey]: next })
              .then(setSettings)
              .catch((e) => notify.error(e instanceof Error ? e.message : 'Failed to save auto-ping'))
          }}
        />
      )}

      <ConfirmModal
        isOpen={!!confirm}
        title={confirm?.title}
        message={confirm?.message}
        confirmLabel="Delete"
        variant="danger"
        onConfirm={() => void confirm?.onConfirm()}
        onClose={() => setConfirm(null)}
      />
    </>
  )
}

// ---------------------------------------------------------------- no-auth proxy

/**
 * Ported from src/shared/components/NoAuthProxyCard.js: a credential-free
 * provider still needs a way to reach the upstream through a proxy pool, so the
 * pool and its rotation live in settings.providerStrategies[alias] rather than
 * on a connection. chat.go's providerProxyURL reads the same two fields.
 */
export function NoAuthProxyCard({
  providerId,
  proxyPools,
  settings,
  onSaved,
}: {
  providerId: string
  proxyPools: ProxyPool[]
  settings: Record<string, unknown>
  onSaved: (s: Record<string, unknown>) => void
}) {
  const notify = useNotify()
  const entry = ((settings.providerStrategies as Record<string, Record<string, unknown>> | undefined)?.[providerId] ?? {}) as {
    proxyPoolId?: string
    rotateStrategy?: string
  }
  const poolId = entry.proxyPoolId ?? ''
  const strategy = entry.rotateStrategy ?? 'none'
  const active = proxyPools.filter((p) => p.isActive !== false)
  const canRotate = active.length >= 2
  const rotating = strategy !== 'none'

  const save = async (nextPool: string, nextStrategy: string) => {
    try {
      const fresh = await api.settings()
      const all = { ...((fresh.providerStrategies as Record<string, Record<string, unknown>> | undefined) ?? {}) }
      const merged: Record<string, unknown> = { ...all[providerId] }
      if (nextPool) merged.proxyPoolId = nextPool
      else delete merged.proxyPoolId
      if (nextStrategy && nextStrategy !== 'none') merged.rotateStrategy = nextStrategy
      else delete merged.rotateStrategy
      if (Object.keys(merged).length === 0) delete all[providerId]
      else all[providerId] = merged
      onSaved(await api.patchSettings({ providerStrategies: all }))
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to save proxy config')
    }
  }

  return (
    <section className="mb-4 overflow-hidden rounded-xl bg-surface">
      <div className="flex items-center gap-2.5 px-4 py-3">
        <Icon name="lock_open" className="shrink-0 text-[16px] text-text-muted" />
        <div className="min-w-0 flex-1">
          <p className="text-sm font-semibold text-text-main">No authentication required</p>
          <p className="text-[11px] text-text-muted">
            Ready to use. Route through a proxy pool to bypass IP limits.
          </p>
        </div>
      </div>

      <div className="grid gap-3 border-t border-border px-4 py-3 sm:grid-cols-2">
          <div className="flex flex-col gap-1">
            <p className="text-[11px] text-text-muted">Proxy Pool</p>
            <Select value={poolId || 'none'} onValueChange={(v) => void save(v === 'none' ? '' : v, strategy)} disabled={rotating}>
              <SelectTrigger className="h-9 rounded-lg border-0 bg-surface-2 px-3">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">None (direct)</SelectItem>
                {active.map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {p.name ?? p.id}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {rotating && (
              <p className="text-[11px] text-text-muted">Rotation active. All pools used.</p>
            )}
          </div>

          <div className="flex flex-col gap-1">
            <p className="text-[11px] text-text-muted">Rotation Strategy</p>
            <Select value={strategy} onValueChange={(v) => void save(poolId, v)}>
              <SelectTrigger className="h-9 rounded-lg border-0 bg-surface-2 px-3">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">None (single pool)</SelectItem>
                <SelectItem value="round-robin" disabled={!canRotate}>
                  Round-robin
                </SelectItem>
                <SelectItem value="random" disabled={!canRotate}>
                  Random
                </SelectItem>
              </SelectContent>
            </Select>
            {!canRotate && (
              <p className="text-[11px] text-text-muted">Need at least 2 active proxy pools.</p>
            )}
          </div>
      </div>
    </section>
  )
}

// ---------------------------------------------------------------- connection row

function ConnectionRow({
  conn,
  proxyPools,
  checked,
  onCheck,
  onToggleActive,
  onEdit,
  onDelete,
  onProxyChange,
  onMoveUp,
  onMoveDown,
  testState,
  onAutoPing,
}: {
  conn: Connection
  proxyPools: ProxyPool[]
  checked: boolean
  onCheck: (v: boolean) => void
  onToggleActive: (v: boolean) => void
  onEdit: () => void
  onDelete: () => void
  onProxyChange: (poolId: string) => void
  onMoveUp?: () => void
  onMoveDown?: () => void
  testState?: OneByOneState
  onAutoPing?: () => void
}) {
  const psd = (conn.providerSpecificData as Record<string, unknown> | undefined) ?? {}
  const poolId = String(psd.proxyPoolId ?? '')
  const pool = proxyPools.find((p) => p.id === poolId)
  const proxyEnabled = psd.connectionProxyEnabled === true
  const testStatus = String(conn.testStatus ?? '')
  const isLease = conn.authType === 'mcode-lease'
  const authIcon = isLease ? 'desktop_windows' : conn.authType === 'oauth' ? 'lock' : conn.authType === 'cookie' ? 'cookie' : 'key'
  const statusVariant = testStatus === 'error' ? 'error' : testStatus === 'active' ? 'success' : 'outline'

  return (
    <div className="flex flex-wrap items-center gap-2 rounded-lg bg-surface-2 px-3 py-2.5 transition-colors hover:bg-surface-3">
      <input
        type="checkbox"
        className="size-3.5 rounded border-border"
        checked={checked}
        onChange={(e) => onCheck(e.target.checked)}
      />

      <div className="flex flex-col">
        <button
          className="text-text-muted transition-colors hover:text-text-main disabled:opacity-30"
          onClick={onMoveUp}
          disabled={!onMoveUp}
          title="Move up"
        >
          <Icon name="keyboard_arrow_up" className="text-[16px]" />
        </button>
        <button
          className="text-text-muted transition-colors hover:text-text-main disabled:opacity-30"
          onClick={onMoveDown}
          disabled={!onMoveDown}
          title="Move down"
        >
          <Icon name="keyboard_arrow_down" className="text-[16px]" />
        </button>
      </div>

      <Icon name={authIcon} className="text-[16px] text-text-muted" />

      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="truncate text-sm font-medium text-text-main">{conn.name || conn.email || conn.id}</span>
          <Badge variant="outline" size="sm">
            {isLease ? 'Desktop Lease' : conn.authType === 'oauth' ? 'OAuth' : conn.authType === 'cookie' ? 'Cookie' : 'API Key'}
          </Badge>
          {testStatus && (
            <Badge variant={statusVariant} size="sm">
              {testStatus}
            </Badge>
          )}
          {testState && (
            <Badge
              variant={testState.state === 'success' ? 'success' : testState.state === 'failed' ? 'error' : 'secondary'}
              size="sm"
            >
              {testState.state}
            </Badge>
          )}
          {conn.priority !== undefined && conn.priority !== 0 && (
            <span className="font-mono text-[10px] text-text-muted">priority {conn.priority}</span>
          )}
        </div>
        <div className="mt-0.5 flex flex-wrap gap-x-3 text-[11px] text-text-muted">
          {conn.email && conn.name && <span>{conn.email}</span>}
          <span>updated {fmtTime(conn.updatedAt)}</span>
          {testState?.error && <span className="text-danger">{testState.error}</span>}
        </div>
      </div>

      <select
        value={poolId}
        onChange={(e) => onProxyChange(e.target.value)}
        className="h-7 rounded-lg border-0 bg-card px-2 text-[11px] text-text-muted"
        title="Proxy pool"
      >
        <option value="">No proxy</option>
        {proxyPools.map((p) => (
          <option key={p.id} value={p.id}>
            {p.name ?? p.id}
          </option>
        ))}
      </select>
      {poolId && !pool && <Badge variant="error" size="sm">missing pool</Badge>}
      {proxyEnabled && <Badge variant="success" size="sm">proxy</Badge>}

      <div className="flex shrink-0 items-center">
        {onAutoPing && (
          <button
            className="flex size-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-card hover:text-text-main"
            onClick={onAutoPing}
            title="Auto-ping schedule"
          >
            <Icon name="schedule" className="text-[16px]" />
          </button>
        )}
        <button
          className="flex size-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-card hover:text-text-main"
          onClick={onEdit}
          title="Edit"
        >
          <Icon name="edit" className="text-[16px]" />
        </button>
        <button
          className="flex size-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-card hover:text-danger"
          onClick={onDelete}
          title="Delete"
        >
          <Icon name="delete" className="text-[16px]" />
        </button>
      </div>
      <Switch checked={conn.isActive} onCheckedChange={onToggleActive} />
    </div>
  )
}

// ---------------------------------------------------------------- model row

function ModelRow({
  fullModel,
  alias,
  testStatus,
  testing,
  onTest,
  onDelete,
  onDisable,
  onSetAlias,
  caps,
  thinkingSuffix,
  custom,
  selected,
  onSelect,
}: {
  fullModel: string
  alias?: string
  testStatus?: 'ok' | 'error'
  testing?: boolean
  onTest?: () => void
  onDelete?: () => void
  onDisable?: () => void
  onSetAlias?: () => void
  caps?: { vision?: boolean; reasoning?: boolean; contextWindow?: number; maxOutput?: number }
  thinkingSuffix?: string | null
  custom?: boolean
  selected?: boolean
  onSelect?: (v: boolean) => void
}) {
  const displayModel = thinkingSuffix ? `${fullModel}(${thinkingSuffix})` : fullModel
  return (
    <div
      className={cn(
        'group flex min-w-0 items-center gap-2 rounded-lg bg-surface-2 py-1.5 pl-3 pr-1.5 text-xs transition-colors hover:bg-surface-3',
        selected && 'bg-surface-3',
      )}
    >
      {onSelect && (
        <input
          type="checkbox"
          className="size-3.5 shrink-0 cursor-pointer rounded border-border"
          checked={!!selected}
          onChange={(e) => onSelect(e.target.checked)}
          aria-label={`Select ${displayModel}`}
        />
      )}
      <span className="min-w-0 flex-1 truncate font-medium" title={displayModel}>
        {displayModel}
      </span>
      <CapacityBadges caps={caps} />
      {alias && (
        <Badge variant="outline" size="sm" className="shrink-0" title={`alias: ${alias}`}>
          {alias}
        </Badge>
      )}
      {testStatus && (
        <Icon
          name={testStatus === 'ok' ? 'check_circle' : 'cancel'}
          className={cn('shrink-0 text-[15px]', testStatus === 'ok' ? 'text-emerald-600 dark:text-emerald-400' : 'text-danger')}
        />
      )}
      <div className="flex shrink-0 items-center">
        <CopyButton value={displayModel} className="size-7 shrink-0 [&_svg]:size-4" />
        {onTest && (
          <button
            className="flex size-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-3 hover:text-text-main"
            onClick={onTest}
            title="Test model"
          >
            <Icon name={testing ? 'progress_activity' : 'science'} className={testing ? 'animate-spin text-[16px]' : 'text-[16px]'} />
          </button>
        )}
        {onSetAlias && (
          <button
            className="flex size-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-3 hover:text-text-main"
            onClick={onSetAlias}
            title="Set alias"
          >
            <Icon name="label" className="text-[16px]" />
          </button>
        )}
        {custom && onDelete ? (
          <button
            className="flex size-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-3 hover:text-danger"
            onClick={onDelete}
            title="Delete custom model"
          >
            <Icon name="close" className="text-[16px]" />
          </button>
        ) : (
          onDisable && (
            <button
              className="flex size-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-3 hover:text-danger"
              onClick={onDisable}
              title="Disable model"
            >
              <Icon name="block" className="text-[16px]" />
            </button>
          )
        )}
      </div>
    </div>
  )
}

// ---------------------------------------------------------------- add api key

export function AddApiKeyModal({
  provider,
  providerName,
  isCompatible,
  isAnthropic,
  authType,
  authHint,
  website,
  credentialForm,
  regions,
  defaultRegion,
  existingNames,
  proxyPools,
  onClose,
  onSaved,
}: {
  provider: string
  providerName: string
  isCompatible: boolean
  isAnthropic?: boolean
  authType: string
  authHint?: string
  website?: string
  credentialForm?: string
  regions?: Array<{ id: string; label: string }>
  defaultRegion?: string
  existingNames: string[]
  proxyPools: ProxyPool[]
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const notify = useNotify()
  const [name, setName] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [priority, setPriority] = useState(1)
  const [proxyPoolId, setProxyPoolId] = useState('')
  const [bulk, setBulk] = useState(false)
  const [aws, setAws] = useState({ accessKeyId: '', sessionToken: '', region: '', profile: '' })
  const [azure, setAzure] = useState({ azureEndpoint: '', apiVersion: '2024-10-01-preview', deployment: '', organization: '' })
  const [cloudflareAccountId, setCloudflareAccountId] = useState('')
  const [ollamaHostUrl, setOllamaHostUrl] = useState('')
  const [region, setRegion] = useState(defaultRegion || regions?.[0]?.id || '')
  const [defaultModel, setDefaultModel] = useState('')
  const [bulkText, setBulkText] = useState('')
  const [busy, setBusy] = useState(false)
  const [validation, setValidation] = useState<'idle' | 'ok' | 'bad'>('idle')
  const [cliDetecting, setCliDetecting] = useState(false)
  const [cliDetectError, setCliDetectError] = useState('')

  const isCookie = authType === 'cookie'
  const usesAws = credentialForm === 'aws'
  const isAzure = provider === 'azure' || provider === 'azure-ai'
  const isCloudflare = provider === 'cloudflare-ai'
  const isOllama = provider === 'ollama-local'
  const label = isCookie ? 'Cookie Value' : usesAws ? 'AWS Secret Access Key' : 'API Key'

  // Mirrors AddApiKeyModal.js apiKeySatisfied: AWS static keys are a pair, and
  // the Azure / Cloudflare forms carry fields the create route must receive.
  const satisfied =
    bulk || isOllama
      ? true
      : usesAws
        ? !!apiKey.trim() && !!aws.accessKeyId.trim()
        : !!apiKey.trim() && (!isCompatible || !!defaultModel.trim())

  const specificData = (): Record<string, unknown> | undefined => {
    const out: Record<string, unknown> = {}
    if (proxyPoolId) out.proxyPoolId = proxyPoolId
    if (isOllama && ollamaHostUrl.trim()) out.baseUrl = ollamaHostUrl.trim()
    if (isAzure) {
      out.azureEndpoint = azure.azureEndpoint.trim()
      out.apiVersion = azure.apiVersion.trim()
      out.deployment = azure.deployment.trim()
      out.organization = azure.organization.trim()
    }
    if (isCloudflare) out.accountId = cloudflareAccountId.trim()
    if (regions?.length && region) out.region = region
    if (isCompatible && defaultModel.trim()) out.defaultModel = defaultModel.trim()
    if (usesAws) {
      // Empty strings are omitted: an empty `profile` would select profile mode
      // and shadow the static keys (aws.go prefers a profile).
      for (const [k, v] of Object.entries(aws)) if (v.trim()) out[k] = v.trim()
    }
    return Object.keys(out).length ? out : undefined
  }

  // commandcode CLI keeps its key in ~/.commandcode/auth.json; the server
  // reads it, the form only re-posts it as an API key.
  const commandCodeAutoImport = async () => {
    setCliDetecting(true)
    setCliDetectError('')
    try {
      const d = (await api.autoImport('commandcode')) as Record<string, unknown>
      if (d.found && d.apiKey) {
        setName((n) => n || String(d.userName || d.keyName || 'CommandCode CLI'))
        setApiKey(String(d.apiKey))
        setValidation('idle')
      } else {
        setCliDetectError(String(d.error || 'Could not find Command Code credentials'))
      }
    } catch {
      setCliDetectError('Failed to auto-import from Command Code CLI')
    } finally {
      setCliDetecting(false)
    }
  }

  const validate = async () => {
    if (!apiKey.trim()) return
    setValidation('idle')
    try {
      const r = await api.validateProvider({ provider, apiKey: apiKey.trim(), providerSpecificData: specificData() })
      setValidation(r.valid ? 'ok' : 'bad')
    } catch {
      setValidation('bad')
    }
  }

  const submit = async () => {
    setBusy(true)
    try {
      if (bulk) {
        const lines = bulkText.split('\n').map((l) => l.trim()).filter(Boolean)
        const used = new Set(existingNames.map((n) => n.toLowerCase()))
        let added = 0
        for (const line of lines) {
          // name|apiKey, or name|apiKey|accountId for the cloudflare lane.
          // Names are gap-filled against the ones already on screen: the server
          // upserts apikey connections by name, so a collision would overwrite.
          const parts = line.split('|').map((p) => p.trim())
          const named = parts.length >= 2
          const base = named ? parts[0] || 'Key' : 'Key'
          const key = named ? parts[1] : parts[0]
          if (!key) continue
          let n = 1
          let candidate = `${base} ${n}`
          while (used.has(candidate.toLowerCase())) candidate = `${base} ${++n}`
          used.add(candidate.toLowerCase())
          await api.createConnection({
            provider,
            apiKey: key,
            name: candidate,
            priority,
            providerSpecificData: parts[2] ? { accountId: parts[2] } : cloudflareAccountId ? { accountId: cloudflareAccountId } : undefined,
          })
          added++
        }
        notify.success(`Added ${added} connection${added === 1 ? '' : 's'}`)
      } else {
        await api.createConnection({
          provider,
          apiKey: apiKey.trim(),
          name: name.trim() || undefined,
          priority,
          providerSpecificData: specificData(),
        })
        notify.success('Connection added')
      }
      await onSaved()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to save connection')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      isOpen
      onClose={onClose}
      title={`Add ${label}: ${providerName}`}
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={busy || !satisfied}>
            {busy ? 'Saving…' : 'Save'}
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-3">
        {authHint && <p className="text-[11px] text-text-muted">{authHint}</p>}
        {!isCompatible && (
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="inline-flex items-center gap-0.5 rounded-md bg-surface-2 p-0.5">
              {([false, true] as const).map((v) => (
                <button
                  key={String(v)}
                  type="button"
                  onClick={() => setBulk(v)}
                  className={cn(
                    'rounded px-2.5 py-1 text-xs font-medium transition-colors',
                    bulk === v ? 'bg-surface text-text-main shadow-sm' : 'text-text-muted hover:text-text-main',
                  )}
                >
                  {v ? 'Bulk add' : 'Single key'}
                </button>
              ))}
            </div>
            {bulk && (
              <span className="font-mono text-[11px] text-text-muted">
                {isCloudflare ? 'name|key|accountId' : 'name|key'} per line
              </span>
            )}
          </div>
        )}
        {bulk ? (
          <textarea
            className="h-40 w-full rounded-md border border-input bg-background p-2 font-mono text-xs"
            placeholder={'Production|sk-...\nStaging|sk-...'}
            value={bulkText}
            onChange={(e) => setBulkText(e.target.value)}
          />
        ) : (
          <>
            <div className="flex flex-col gap-1">
              <p className="text-xs font-medium text-text-muted">{label}</p>
              <div className="flex items-end gap-2">
                <Input
                  type="password"
                  value={apiKey}
                  onChange={(e) => {
                    setApiKey(e.target.value)
                    setValidation('idle')
                  }}
                  placeholder={isCookie ? 'Cookie header value' : 'sk-…'}
                />
                <Button variant="secondary" onClick={() => void validate()} disabled={!apiKey.trim()}>
                  Check
                </Button>
              </div>
              {validation !== 'idle' && (
                <Badge variant={validation === 'ok' ? 'success' : 'error'}>{validation === 'ok' ? 'Valid' : 'Invalid'}</Badge>
              )}
            </div>
            {provider === 'commandcode' && (
              <div className="flex items-center justify-between gap-2 rounded border border-accent/20 bg-accent/10 p-3">
                <div>
                  <p className="text-xs font-medium text-text">Command Code CLI detected?</p>
                  <p className="text-xs text-text-muted">Import API key directly from ~/.commandcode/auth.json</p>
                  {cliDetectError && <p className="mt-1 text-xs text-red-400">{cliDetectError}</p>}
                </div>
                <Button size="sm" variant="secondary" onClick={() => void commandCodeAutoImport()} disabled={cliDetecting || busy}>
                  {cliDetecting ? 'Importing…' : 'Import from CLI'}
                </Button>
              </div>
            )}
            {usesAws && (
              <div className="grid gap-2 sm:grid-cols-2">
                <div className="flex flex-col gap-1">
                  <p className="text-xs font-medium text-text-muted">Access Key ID</p>
                  <Input value={aws.accessKeyId} onChange={(e) => setAws({ ...aws, accessKeyId: e.target.value })} placeholder="AKIA…" />
                </div>
                <div className="flex flex-col gap-1">
                  <p className="text-xs font-medium text-text-muted">Session Token (optional)</p>
                  <Input type="password" value={aws.sessionToken} onChange={(e) => setAws({ ...aws, sessionToken: e.target.value })} />
                </div>
                <div className="flex flex-col gap-1">
                  <p className="text-xs font-medium text-text-muted">Region (optional)</p>
                  <Input value={aws.region} onChange={(e) => setAws({ ...aws, region: e.target.value })} placeholder="us-east-1" />
                </div>
                <div className="flex flex-col gap-1">
                  <p className="text-xs font-medium text-text-muted">AWS Profile (optional)</p>
                  <Input value={aws.profile} onChange={(e) => setAws({ ...aws, profile: e.target.value })} placeholder="default" />
                </div>
                <p className="text-[11px] text-text-muted sm:col-span-2">
                  A named profile wins over the static keys. Temporary (ASIA…) keys need the session token.
                </p>
              </div>
            )}
            {isAzure && (
              <div className="grid gap-2 sm:grid-cols-2">
                <div className="flex flex-col gap-1 sm:col-span-2">
                  <p className="text-xs font-medium text-text-muted">Azure Endpoint</p>
                  <Input value={azure.azureEndpoint} onChange={(e) => setAzure({ ...azure, azureEndpoint: e.target.value })} placeholder="https://x.openai.azure.com" />
                </div>
                <div className="flex flex-col gap-1">
                  <p className="text-xs font-medium text-text-muted">Deployment</p>
                  <Input value={azure.deployment} onChange={(e) => setAzure({ ...azure, deployment: e.target.value })} />
                </div>
                <div className="flex flex-col gap-1">
                  <p className="text-xs font-medium text-text-muted">API Version</p>
                  <Input value={azure.apiVersion} onChange={(e) => setAzure({ ...azure, apiVersion: e.target.value })} />
                </div>
                <div className="flex flex-col gap-1 sm:col-span-2">
                  <p className="text-xs font-medium text-text-muted">Organization</p>
                  <Input value={azure.organization} onChange={(e) => setAzure({ ...azure, organization: e.target.value })} />
                </div>
              </div>
            )}
            {isCloudflare && !bulk && (
              <div className="flex flex-col gap-1">
                <p className="text-xs font-medium text-text-muted">Account ID</p>
                <Input value={cloudflareAccountId} onChange={(e) => setCloudflareAccountId(e.target.value)} placeholder="Find it in the dash.cloudflare.com sidebar" />
              </div>
            )}
            {isOllama && (
              <div className="flex flex-col gap-1">
                <p className="text-xs font-medium text-text-muted">Ollama Host URL (optional)</p>
                <Input value={ollamaHostUrl} onChange={(e) => setOllamaHostUrl(e.target.value)} placeholder="http://192.168.1.10:11434" />
                <p className="text-[11px] text-text-muted">
                  Leave blank for <code>http://localhost:11434</code>. For remote Ollama enter the full host URL.
                </p>
              </div>
            )}
            {isCookie && (authHint || website) && (
              <p className="text-[11px] text-text-muted">
                {authHint}
                {website && (
                  <>
                    {' '}
                    <a href={website} target="_blank" rel="noopener noreferrer" className="text-primary underline">
                      Open {website.replace(/^https?:\/\//, '')}
                    </a>
                  </>
                )}
              </p>
            )}
            {!isCookie && provider === 'xai' && (
              <p className="text-[11px] text-text-muted">
                Use a direct xAI API key from console.x.ai. This is separate from Grok Build OAuth.
              </p>
            )}
            {!!regions?.length && (
              <div className="flex flex-col gap-1">
                <label htmlFor="conn-region" className="text-xs font-medium text-text-muted">
                  Region
                </label>
                <select
                  id="conn-region"
                  value={region}
                  onChange={(e) => setRegion(e.target.value)}
                  className="rounded-md border border-border bg-background px-2 py-1.5 text-sm"
                >
                  {regions.map((r) => (
                    <option key={r.id} value={r.id}>
                      {r.label}
                    </option>
                  ))}
                </select>
              </div>
            )}
            {isCompatible && (
              <div className="flex flex-col gap-1">
                <p className="text-xs font-medium text-text-muted">Default Model</p>
                <Input
                  value={defaultModel}
                  onChange={(e) => setDefaultModel(e.target.value)}
                  placeholder={isAnthropic ? 'claude-3-5-sonnet-latest' : 'gpt-4o-mini'}
                />
                <p className="text-[11px] text-text-muted">
                  Enter the model ID exactly as your compatible endpoint expects it. This model is saved as the connection default.
                </p>
              </div>
            )}
            <div className="flex flex-col gap-1">
              <p className="text-xs font-medium text-text-muted">Name</p>
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Production Key" />
            </div>
            <div className="flex flex-col gap-1">
              <p className="text-xs font-medium text-text-muted">Priority</p>
              <Input type="number" value={priority} onChange={(e) => setPriority(Number.parseInt(e.target.value, 10) || 1)} />
            </div>
            {proxyPools.length > 0 && (
              <div className="flex flex-col gap-1">
                <p className="text-xs font-medium text-text-muted">Proxy pool</p>
                <select
                  value={proxyPoolId}
                  onChange={(e) => setProxyPoolId(e.target.value)}
                  className="rounded-md border border-border bg-background px-2 py-1.5 text-sm"
                >
                  <option value="">No proxy</option>
                  {proxyPools.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name ?? p.id}
                    </option>
                  ))}
                </select>
              </div>
            )}
          </>
        )}
      </div>
    </Modal>
  )
}

// ------------------------------------------------------ credential import

/** MiMo Desktop's cluster list; the key is bound to the cluster it was issued in. */
const MIMO_REGIONS = [
  { id: 'sgp', label: 'Singapore' },
  { id: 'cn', label: 'China' },
  { id: 'ams', label: 'Amsterdam' },
  { id: 'ru', label: 'Russia' },
  { id: 'in', label: 'India' },
]

/**
 * Per-provider credential import (KineRouter's GitLabAuthModal / KiroAuthModal /
 * CursorAuthModal / ZedAuthModal / XiaomiMimoAuthModal). The browser-login
 * lanes stay unported; the token/PAT import posts the same JSON the reference
 * posts, so an already-signed-in IDE can be hooked up from a pasted token.
 */
function CredentialImportModal({
  lane,
  onClose,
  onSaved,
}: {
  lane: ImportLane
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const notify = useNotify()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [baseUrl, setBaseUrl] = useState('https://gitlab.com')
  const [token, setToken] = useState('')
  const [region, setRegion] = useState(lane === 'kiro' ? 'us-east-1' : 'sgp')
  const [machineId, setMachineId] = useState('')
  const [userId, setUserId] = useState('')

  const titles: Record<ImportLane, string> = {
    gitlab: 'GitLab Token',
    kiro: 'Kiro API Key',
    cursor: 'Cursor Token',
    zed: 'Zed Session',
    xiaomi: 'MiMo API Key',
  }

  const submit = async () => {
    setBusy(true)
    setError('')
    try {
      if (lane === 'gitlab') {
        if (!token.trim()) throw new Error('Personal Access Token is required')
        await api.importGitlabPAT(token.trim(), baseUrl.trim() || undefined)
      } else if (lane === 'kiro') {
        if (!token.trim()) throw new Error('API key is required')
        await api.importKiroAPIKey(token.trim(), region)
      } else if (lane === 'cursor') {
        if (!token.trim()) throw new Error('Access token is required')
        await api.importCursor(token.trim(), machineId.trim())
      } else if (lane === 'zed') {
        if (!token.trim()) throw new Error('Access token is required')
        await api.importZed(token.trim(), userId.trim())
      } else {
        if (!token.trim()) throw new Error('API key is required')
        await api.importXiaomiKey({ apiKey: token.trim(), region })
      }
      notify.success('Connection added')
      await onSaved()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Import failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      isOpen
      onClose={onClose}
      title={titles[lane]}
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => void submit()} disabled={busy || !token.trim()}>
            {busy ? 'Importing…' : 'Import'}
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-3">
        {lane === 'gitlab' && (
          <div className="flex flex-col gap-1">
            <p className="text-xs font-medium text-text-muted">Instance base URL</p>
            <Input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder="https://gitlab.com" />
            <p className="text-[11px] text-text-muted">
              The token needs <code>api</code> scope. Self-managed instances must be reachable from this server.
            </p>
          </div>
        )}
        <div className="flex flex-col gap-1">
          <p className="text-xs font-medium text-text-muted">
            {lane === 'gitlab' ? 'Personal Access Token' : lane === 'xiaomi' ? 'API key (sk-…)' : 'Access token'}
          </p>
          <Input
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder={lane === 'gitlab' ? 'glpat-…' : lane === 'xiaomi' ? 'sk-…' : 'Paste the token'}
          />
        </div>
        {lane === 'kiro' && (
          <div className="flex flex-col gap-1">
            <p className="text-xs font-medium text-text-muted">AWS region</p>
            <Input value={region} onChange={(e) => setRegion(e.target.value)} placeholder="us-east-1" />
          </div>
        )}
        {lane === 'xiaomi' && (
          <div className="flex flex-col gap-1">
            <p className="text-xs font-medium text-text-muted">Region</p>
            <select
              value={region}
              onChange={(e) => setRegion(e.target.value)}
              className="rounded-md border border-border bg-background px-2 py-1.5 text-sm"
            >
              {MIMO_REGIONS.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.label}
                </option>
              ))}
            </select>
          </div>
        )}
        {lane === 'cursor' && (
          <div className="flex flex-col gap-1">
            <p className="text-xs font-medium text-text-muted">Machine ID (UUID)</p>
            <Input value={machineId} onChange={(e) => setMachineId(e.target.value)} placeholder="xxxxxxxx-xxxx-…" />
          </div>
        )}
        {lane === 'zed' && (
          <div className="flex flex-col gap-1">
            <p className="text-xs font-medium text-text-muted">Zed user id</p>
            <Input value={userId} onChange={(e) => setUserId(e.target.value)} placeholder="user_…" />
          </div>
        )}
        {error && <p className="break-words text-xs text-danger">{error}</p>}
      </div>
    </Modal>
  )
}

// ---------------------------------------------------------------- oauth

function OAuthModal({
  provider,
  onClose,
  onSuccess,
}: {
  provider: string
  onClose: () => void
  onSuccess: () => Promise<void>
}) {
  const notify = useNotify()
  const [device, setDevice] = useState<{ userCode: string; verificationUri: string; verificationUriComplete?: string } | null>(null)
  const [pasteUrl, setPasteUrl] = useState('')
  const [error, setError] = useState('')
  const stateRef = useRef('')
  const pollRef = useRef<number | null>(null)
  const doneRef = useRef(false)

  const finish = useCallback(async () => {
    if (doneRef.current) return
    doneRef.current = true
    await onSuccess()
  }, [onSuccess])

  const exchange = useCallback(
    async (code: string, state: string) => {
      try {
        await api.oauthExchange(provider, code, state)
        notify.success('Authorization complete')
        await finish()
      } catch (e) {
        setError(e instanceof Error ? e.message : 'Exchange failed')
      }
    },
    [provider, notify, finish],
  )

  useEffect(() => {
    let cancelled = false
    const isDevice = ['grok-cli', 'kimi', 'muse', 'nous', 'github', 'minimax-code', 'minimax-code-global', 'zcode', 'free-pi', 'freebuff'].includes(provider)

    const run = async () => {
      try {
        if (isDevice) {
          const d = await api.oauthDevice(provider)
          if (cancelled) return
          stateRef.current = d.state
          setDevice({ userCode: d.userCode, verificationUri: d.verificationUri, verificationUriComplete: d.verificationUriComplete })
          window.open(d.verificationUriComplete || d.verificationUri, '_blank', 'noopener,noreferrer')
          const interval = Math.max(3, d.interval || 5) * 1000
          const tick = async () => {
            try {
              const r = await api.oauthPoll(provider, d.state)
              if (doneRef.current) return
              if (r.status === 'connected') {
                notify.success('Authorization complete')
                await finish()
                return
              }
              if (r.error) {
                setError(r.error)
                return
              }
            } catch (e) {
              setError(e instanceof Error ? e.message : 'Poll failed')
              return
            }
            pollRef.current = window.setTimeout(tick, interval)
          }
          pollRef.current = window.setTimeout(tick, interval)
          return
        }

        // kiro forks into a social sign-in wrapper in the reference; the plain
        // authorize URL is the same upstream, so it is used for every provider.
        const start = await api.oauthAuthorize(provider)
        if (cancelled) return
        stateRef.current = start.state
        const popup = window.open(start.authorizeUrl, 'oauth_popup', 'width=600,height=700')
        if (!popup) setError('Popup blocked. Paste the callback URL below instead.')

        const handler = (e: MessageEvent) => {
          if (e.origin !== window.location.origin) return
          const data = e.data as { type?: string; data?: { code?: string; state?: string; error?: string } } | null
          if (data?.type !== 'oauth_callback' || !data.data) return
          const d = data.data
          if (d.error) {
            setError(d.error)
            return
          }
          if (d.code) void exchange(d.code, d.state || stateRef.current)
        }
        window.addEventListener('message', handler)
        const channel = 'BroadcastChannel' in window ? new BroadcastChannel('oauth_callback') : null
        const onChannel = (e: MessageEvent) => {
          const d = e.data as { code?: string; state?: string; error?: string } | null
          if (!d) return
          if (d.error) setError(d.error)
          else if (d.code) void exchange(d.code, d.state || stateRef.current)
        }
        channel?.addEventListener('message', onChannel)
        const onStorage = (e: StorageEvent) => {
          if (e.key !== 'oauth_callback' || !e.newValue) return
          try {
            const d = JSON.parse(e.newValue) as { code?: string; state?: string; error?: string }
            localStorage.removeItem('oauth_callback')
            if (d.error) setError(d.error)
            else if (d.code) void exchange(d.code, d.state || stateRef.current)
          } catch {
            /* ignore */
          }
        }
        window.addEventListener('storage', onStorage)

        return () => {
          window.removeEventListener('message', handler)
          window.removeEventListener('storage', onStorage)
          channel?.removeEventListener('message', onChannel)
          channel?.close()
        }
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : 'Failed to start authorization')
      }
    }
    let cleanup: (() => void) | undefined
    void run().then((fn) => {
      if (typeof fn === 'function') cleanup = fn
    })
    return () => {
      cancelled = true
      if (pollRef.current) window.clearTimeout(pollRef.current)
      cleanup?.()
    }
    // The flow starts once per mount; provider is fixed for the lifetime of the modal.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [provider])

  const submitPaste = () => {
    try {
      const url = new URL(pasteUrl.trim(), window.location.origin)
      const code = url.searchParams.get('code')
      const state = url.searchParams.get('state') || stateRef.current
      if (!code) throw new Error('No code in URL')
      void exchange(code, state)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Invalid callback URL')
    }
  }

  return (
    <Modal isOpen onClose={onClose} title="OAuth Authorization">
      <div className="flex flex-col gap-3">
        {device ? (
          <>
            <p className="text-xs text-text-muted">Open the link and enter this code:</p>
            <div className="rounded-md border border-border bg-surface-2 p-3 text-center">
              <code className="text-lg font-semibold tracking-widest">{device.userCode}</code>
            </div>
            <a
              href={device.verificationUriComplete || device.verificationUri}
              target="_blank"
              rel="noopener noreferrer"
              className="text-xs text-primary hover:underline"
            >
              {device.verificationUriComplete || device.verificationUri}
            </a>
            <p className="text-xs text-text-muted">Waiting for authorization…</p>
          </>
        ) : (
          <p className="text-xs text-text-muted">Complete the sign-in in the popup. It closes automatically.</p>
        )}

        <div className="flex flex-col gap-1">
          <p className="text-xs font-medium text-text-muted">Or paste the callback URL</p>
          <div className="flex gap-2">
            <Input value={pasteUrl} onChange={(e) => setPasteUrl(e.target.value)} placeholder="/callback?code=…&state=…" />
            <Button variant="secondary" onClick={submitPaste} disabled={!pasteUrl.trim()}>
              Submit
            </Button>
          </div>
        </div>

        {error && <p className="text-xs text-danger">{error}</p>}
      </div>
    </Modal>
  )
}

function IFlowCookieModal({ onClose, onSaved }: { onClose: () => void; onSaved: () => Promise<void> }) {
  const notify = useNotify()
  const [cookie, setCookie] = useState('')
  const [busy, setBusy] = useState(false)
  return (
    <Modal
      isOpen
      onClose={onClose}
      title="Add Connection via Cookie"
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={busy || !cookie.includes('BXAuth=')}
            onClick={async () => {
              setBusy(true)
              try {
                await api.iflowCookie(cookie.trim())
                notify.success('Connection added')
                await onSaved()
              } catch (e) {
                notify.error(e instanceof Error ? e.message : 'Cookie import failed')
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy ? 'Importing…' : 'Import'}
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-2">
        <p className="text-xs text-text-muted">Copy the full Cookie header from a logged-in iflow session. It must contain BXAuth.</p>
        <textarea
          className="h-32 w-full rounded-md border border-input bg-background p-2 font-mono text-xs"
          value={cookie}
          onChange={(e) => setCookie(e.target.value)}
          placeholder="BXAuth=…; …"
        />
      </div>
    </Modal>
  )
}

function BulkImportModal({
  provider,
  onClose,
  onSaved,
}: {
  provider: 'codex' | 'grok-cli'
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const notify = useNotify()
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  return (
    <Modal
      isOpen
      onClose={onClose}
      title={`Bulk Import ${provider === 'codex' ? 'Codex' : 'Grok CLI'} Accounts`}
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={busy || !text.trim()}
            onClick={async () => {
              setBusy(true)
              try {
                let accounts: unknown
                try {
                  accounts = JSON.parse(text)
                } catch {
                  accounts = text
                }
                const r = await api.bulkImport(provider, { accounts })
                notify.success(`Imported ${r.success}, failed ${r.failed}`)
                await onSaved()
              } catch (e) {
                notify.error(e instanceof Error ? e.message : 'Import failed')
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy ? 'Importing…' : 'Import'}
          </Button>
        </div>
      }
    >
      <textarea
        className="h-48 w-full rounded-md border border-input bg-background p-2 font-mono text-xs"
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder='[{"accessToken": "…", "email": "…"}]'
      />
    </Modal>
  )
}

// ---------------------------------------------------------------- node edit

function EditNodeModal({ node, onClose, onSaved }: { node: ProviderNode; onClose: () => void; onSaved: () => Promise<void> }) {
  const notify = useNotify()
  const [name, setName] = useState(String(node.name ?? ''))
  const [prefix, setPrefix] = useState(String(node.prefix ?? ''))
  const [baseUrl, setBaseUrl] = useState(String(node.baseUrl ?? ''))
  const [apiType, setApiType] = useState(String(node.apiType ?? 'chat'))
  const [busy, setBusy] = useState(false)
  return (
    <Modal
      isOpen
      onClose={onClose}
      title="Edit Compatible Node"
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={busy || !name.trim() || !prefix.trim() || !baseUrl.trim()}
            onClick={async () => {
              setBusy(true)
              try {
                await api.updateProviderNode(node.id, { name, prefix, baseUrl, apiType })
                await onSaved()
              } catch (e) {
                notify.error(e instanceof Error ? e.message : 'Update failed')
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy ? 'Saving…' : 'Save'}
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <p className="text-xs font-medium text-text-muted">Name</p>
          <Input value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1">
          <p className="text-xs font-medium text-text-muted">Prefix</p>
          <Input value={prefix} onChange={(e) => setPrefix(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1">
          <p className="text-xs font-medium text-text-muted">Base URL</p>
          <Input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
        </div>
        {node.type === 'openai-compatible' && (
          <div className="flex flex-col gap-1">
            <p className="text-xs font-medium text-text-muted">API type</p>
            <select
              value={apiType}
              onChange={(e) => setApiType(e.target.value)}
              className="rounded-md border border-border bg-background px-2 py-1.5 text-sm"
            >
              <option value="chat">Chat Completions</option>
              <option value="responses">Responses</option>
            </select>
          </div>
        )}
      </div>
    </Modal>
  )
}

// ---------------------------------------------------------------- custom model

function AddCustomModelModal({
  providerAlias,
  displayAlias,
  onClose,
  onSaved,
}: {
  providerAlias: string
  displayAlias: string
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const notify = useNotify()
  const [id, setId] = useState('')
  const [type, setType] = useState('llm')
  const [vision, setVision] = useState(false)
  const [reasoning, setReasoning] = useState(false)
  const [testResult, setTestResult] = useState<'idle' | 'ok' | 'bad'>('idle')
  const [busy, setBusy] = useState(false)

  const cleanId = id.trim().startsWith(`${providerAlias}/`) ? id.trim().slice(providerAlias.length + 1) : id.trim()

  return (
    <Modal
      isOpen
      onClose={onClose}
      title="Add Model"
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={busy || !cleanId}
            onClick={async () => {
              setBusy(true)
              try {
                await api.addCustomModel({
                  providerAlias,
                  id: cleanId,
                  type,
                  caps: { vision, reasoning },
                })
                await onSaved()
              } catch (e) {
                notify.error(e instanceof Error ? e.message : 'Failed to add model')
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy ? 'Saving…' : 'Add'}
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <p className="text-xs font-medium text-text-muted">Model ID</p>
          <Input value={id} onChange={(e) => setId(e.target.value)} placeholder="gpt-5.6-sol" />
          {cleanId && <p className="text-[11px] text-text-muted">routed as {displayAlias}/{cleanId}</p>}
        </div>
        <div className="flex flex-col gap-1">
          <p className="text-xs font-medium text-text-muted">Type</p>
          <select
            value={type}
            onChange={(e) => setType(e.target.value)}
            className="rounded-md border border-border bg-background px-2 py-1.5 text-sm"
          >
            <option value="llm">LLM</option>
            <option value="stt">Speech to text</option>
          </select>
        </div>
        {type === 'llm' && (
          <div className="flex items-center gap-4 text-xs text-text-muted">
            <label className="flex items-center gap-1.5">
              <input type="checkbox" checked={vision} onChange={(e) => setVision(e.target.checked)} /> Vision
            </label>
            <label className="flex items-center gap-1.5">
              <input type="checkbox" checked={reasoning} onChange={(e) => setReasoning(e.target.checked)} /> Reasoning
            </label>
          </div>
        )}
        <div className="flex items-center gap-2">
          <Button
            variant="secondary"
            disabled={!cleanId}
            onClick={async () => {
              try {
                const r = await api.testModel(`${providerAlias}/${cleanId}`, type)
                setTestResult(r.ok ? 'ok' : 'bad')
              } catch {
                setTestResult('bad')
              }
            }}
          >
            Test
          </Button>
          {testResult !== 'idle' && (
            <Badge variant={testResult === 'ok' ? 'success' : 'error'}>{testResult === 'ok' ? 'Works' : 'Failed'}</Badge>
          )}
        </div>
      </div>
    </Modal>
  )
}

// ---------------------------------------------------------------- alias

function AliasModal({
  modelId,
  fullModel,
  current,
  onClose,
  onSaved,
}: {
  modelId: string
  fullModel: string
  current?: string
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const notify = useNotify()
  const [alias, setAlias] = useState(current ?? '')
  return (
    <Modal
      isOpen
      onClose={onClose}
      title={`Alias for ${modelId}`}
      footer={
        <div className="flex justify-end gap-2">
          {current && (
            <Button
              variant="destructive"
              onClick={async () => {
                try {
                  await api.deleteAlias(current)
                  await onSaved()
                } catch (e) {
                  notify.error(e instanceof Error ? e.message : 'Failed to remove alias')
                }
              }}
            >
              Remove
            </Button>
          )}
          <Button
            disabled={!alias.trim()}
            onClick={async () => {
              try {
                await api.setAlias(alias.trim(), fullModel)
                await onSaved()
              } catch (e) {
                notify.error(e instanceof Error ? e.message : 'Failed to set alias')
              }
            }}
          >
            Save
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-1">
        <p className="text-xs font-medium text-text-muted">Alias (used as model id when routing)</p>
        <Input value={alias} onChange={(e) => setAlias(e.target.value)} placeholder="fast-model" />
      </div>
    </Modal>
  )
}

// ---------------------------------------------------------------- overrides

function CustomConfigCard({ providerId }: { providerId: string }) {
  const notify = useNotify()
  const [builtin, setBuiltin] = useState<Record<string, string>>({})
  const [saved, setSaved] = useState<Record<string, string>>({})
  const [rows, setRows] = useState<Array<{ name: string; value: string }>>([])
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    void api
      .providerOverrides(providerId)
      .then((r) => {
        setBuiltin(r.builtinHeaders ?? {})
        setSaved(r.headers ?? {})
        setRows(Object.entries(r.headers ?? {}).map(([name, value]) => ({ name, value })))
      })
      .catch(() => {})
  }, [providerId])

  const save = async () => {
    const headers: Record<string, string> = {}
    for (const { name, value } of rows) {
      if (!name.trim() || !value.trim()) continue
      if (!HEADER_NAME_RE.test(name.trim())) {
        notify.error(`Invalid header name: ${name}`)
        return
      }
      if (BUILTIN_BLOCKED_HEADERS.includes(name.trim().toLowerCase())) {
        notify.error(`Header ${name} cannot be overridden`)
        return
      }
      headers[name.trim()] = value
    }
    setBusy(true)
    try {
      const r = await api.saveProviderOverrides(providerId, headers)
      setSaved(r.headers ?? {})
      setRows(Object.entries(r.headers ?? {}).map(([name, value]) => ({ name, value })))
      notify.success('Headers saved')
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Save failed')
    } finally {
      setBusy(false)
    }
  }

  const dirty = JSON.stringify(Object.fromEntries(rows.filter((r) => r.name && r.value).map((r) => [r.name, r.value]))) !== JSON.stringify(saved)

  return (
    <section className="mt-4 overflow-hidden rounded-xl bg-surface">
      <div className="flex items-center justify-between gap-2 px-4 py-3">
        <h2 className="text-sm font-semibold text-text-main">Custom Headers</h2>
        <div className="flex items-center gap-1">
          <Button
            size="sm"
            variant="ghost"
            onClick={() => setRows(Object.entries(saved).map(([name, value]) => ({ name, value })))}
            disabled={!dirty}
          >
            Reset
          </Button>
          <Button size="sm" variant="secondary" onClick={() => void save()} disabled={busy || !dirty}>
            {busy ? 'Saving…' : 'Save'}
          </Button>
        </div>
      </div>
      <div className="border-t border-border px-4 py-3">
        {Object.keys(builtin).length > 0 && (
          <div className="mb-3">
            <p className="mb-1.5 text-[11px] text-text-muted">Built-in headers</p>
            <div className="flex flex-wrap gap-1.5">
              {Object.entries(builtin).map(([k, v]) => (
                <span
                  key={k}
                  title={v}
                  className="inline-flex items-center rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-text-muted"
                >
                  {k}
                </span>
              ))}
            </div>
          </div>
        )}
        <div className="flex flex-col gap-2">
          {rows.map((row, i) => (
            <div key={i} className="flex gap-2">
              <Input
                className="w-1/3 border-0 bg-surface-2 shadow-none"
                value={row.name}
                placeholder="X-Header"
                onChange={(e) => setRows((prev) => prev.map((r, j) => (j === i ? { ...r, name: e.target.value } : r)))}
              />
              <Input
                className="flex-1 border-0 bg-surface-2 shadow-none"
                value={row.value}
                placeholder="value"
                onChange={(e) => setRows((prev) => prev.map((r, j) => (j === i ? { ...r, value: e.target.value } : r)))}
              />
              <button
                className="flex size-9 shrink-0 items-center justify-center rounded-lg text-text-muted transition-colors hover:bg-surface-2 hover:text-danger"
                onClick={() => setRows((prev) => prev.filter((_, j) => j !== i))}
                title="Remove header"
              >
                <Icon name="close" className="text-[16px]" />
              </button>
            </div>
          ))}
          <Button
            size="sm"
            variant="ghost"
            className="self-start"
            onClick={() => setRows((prev) => [...prev, { name: '', value: '' }])}
          >
            <Icon name="add" /> Add header
          </Button>
        </div>
      </div>
    </section>
  )
}
