import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Plus, Search, X } from 'lucide-react'
import { api, type Combo, type Connection, type ProviderSummary, type Settings } from '@/lib/api'
import { aggregateComboCapabilities, fmtK, type FullCaps } from '@/lib/combo-caps'
import { moveItem } from '@/lib/format'
import { resolveCaps, useModelCaps } from '@/lib/use-model-caps'
import { useCopyToClipboard } from '@/lib/use-copy-to-clipboard'
import { useNotify } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { CapacityBadges, type Caps } from '@/components/capacity-badges'
import { Icon } from '@/components/icon'
import { ModelSelectModal, type PickerModel } from '@/components/model-select-modal'
import { ConfirmModal, Modal } from '@/components/modal'
import { SmartPolicyModal, type SmartPolicy } from '@/components/smart-policy-modal'
import { PageHeader } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input, Label } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

/** Port of src/app/(dashboard)/dashboard/combos/page.js. */
const VALID_NAME_REGEX = /^[a-zA-Z0-9_.\-]+$/

// pdf/videoInput are hidden in the reference UI: no translator support yet.
const CAPACITY_ADAPTER_CAPS = [
  { key: 'vision', label: 'Vision', icon: 'visibility', desc: 'images (png, jpg, webp, …)' },
  { key: 'audioInput', label: 'Audio', icon: 'graphic_eq', desc: 'audio input' },
] as const

const DEFAULT_FALLBACK_MODEL = 'oc/mimo-v2.6-flash-free'
const DEFAULT_ROUTER_MODEL = 'oc/jev-1.13-free'
const EMPTY_CAP_ENTRY = { enabled: true, roundRobin: false, models: [] as string[] }
const CAP_KEYS = ['vision', 'pdf', 'audioInput', 'videoInput'] as const

const STRATEGY_OPTIONS = [
  { value: 'fallback', label: 'Fallback — try in order' },
  { value: 'round-robin', label: 'Round Robin — rotate' },
  { value: 'smart', label: 'Smart Router — System One' },
  { value: 'fusion', label: 'Fusion — panel + judge' },
]

type CapEntry = { enabled: boolean; roundRobin: boolean; models: string[] }
type CapacityAdapter = Record<string, CapEntry>
type ComboStrategy = {
  fallbackStrategy?: string
  judgeModel?: string
  routerModel?: string
  smartPolicy?: string
  sessionSticky?: boolean
  confidenceThreshold?: number
}

const upgradeLegacyModel = (m: string) => (m === 'oc/mimo-v2.5-free' ? DEFAULT_FALLBACK_MODEL : m)

/** Legacy stored form was an array of {model, enabled}. */
function normalizeCapEntry(entry: unknown): CapEntry {
  if (Array.isArray(entry)) {
    return {
      enabled: true,
      roundRobin: false,
      models: entry.map((e) => upgradeLegacyModel((e as { model?: string })?.model ?? String(e))).filter(Boolean),
    }
  }
  if (entry && typeof entry === 'object') {
    const e = entry as { enabled?: boolean; roundRobin?: boolean; models?: unknown }
    return {
      enabled: e.enabled !== false,
      roundRobin: !!e.roundRobin,
      models: Array.isArray(e.models) ? e.models.map((m) => upgradeLegacyModel(String(m))).filter(Boolean) : [],
    }
  }
  return { ...EMPTY_CAP_ENTRY }
}

function emptyAdapter(): CapacityAdapter {
  const out: CapacityAdapter = {}
  for (const k of CAP_KEYS) out[k] = { ...EMPTY_CAP_ENTRY }
  return out
}

function policyLabel(strategy: ComboStrategy): string {
  const policy = strategy.smartPolicy || (strategy.sessionSticky === false ? 'per-turn' : 'smart')
  if (policy === 'strict') return 'Strict Lock'
  if (policy === 'per-turn') return 'Per-Turn'
  if (policy === 'custom') {
    const raw = strategy.confidenceThreshold
    const pct = Math.round((raw || 0.8) * (raw && raw <= 1 ? 100 : 1))
    return `Custom (≥${pct || 80}%)`
  }
  return 'Smart (≥80%)'
}

export default function CombosPage() {
  const [combos, setCombos] = useState<Combo[]>([])
  const [connections, setConnections] = useState<Connection[]>([])
  const [providers, setProviders] = useState<ProviderSummary[]>([])
  const [strategies, setStrategies] = useState<Record<string, ComboStrategy>>({})
  const [adapter, setAdapter] = useState<CapacityAdapter>(emptyAdapter)
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')
  const [selectedIds, setSelectedIds] = useState<string[]>([])
  const [bulkBusy, setBulkBusy] = useState(false)
  const [draft, setDraft] = useState<{ editing: Combo | null } | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<Combo[] | null>(null)
  const notify = useNotify()
  const capsTable = useModelCaps()

  const load = useCallback(async () => {
    try {
      const [c, p, s] = await Promise.all([api.combos(), api.providers(), api.settings()])
      setCombos(c.combos.filter((x) => !x.kind || x.kind === 'llm'))
      setConnections(p.connections.filter((x) => x.isActive !== false))
      setProviders(p.providers)
      setStrategies(((s as Settings).comboStrategies as Record<string, ComboStrategy>) ?? {})
      const raw = ((s as Settings).capacityAdapter as Record<string, unknown>) ?? {}
      const next = emptyAdapter()
      for (const k of CAP_KEYS) next[k] = normalizeCapEntry(raw[k])
      setAdapter(next)
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to load combos')
    } finally {
      setLoading(false)
    }
  }, [notify])

  useEffect(() => { void load() }, [load])

  // Drop stale selection when the list changes (delete / refresh).
  useEffect(() => {
    const alive = new Set(combos.map((c) => c.id))
    setSelectedIds((prev) => prev.filter((id) => alive.has(id)))
  }, [combos])

  const getCaps = useCallback((model: string): FullCaps | undefined => resolveCaps(capsTable, model), [capsTable])
  const comboByName = useMemo(() => Object.fromEntries(combos.map((c) => [c.name, c.models])), [combos])

  const q = search.trim().toLowerCase()
  const filtered = q
    ? combos.filter((c) => c.name.toLowerCase().includes(q) || c.models.some((m) => m.toLowerCase().includes(q)))
    : combos
  const allSelected = filtered.length > 0 && filtered.every((c) => selectedIds.includes(c.id))
  const selectedCombos = combos.filter((c) => selectedIds.includes(c.id))

  const persistStrategies = async (next: Record<string, ComboStrategy>) => {
    await api.patchSettings({ comboStrategies: next })
    setStrategies(next)
  }

  const prune = (names: string[], base = strategies) => {
    const next = { ...base }
    for (const n of names) delete next[n]
    return next
  }

  // Passing an empty patch (strategy back to default) drops the entry entirely.
  const setStrategy = async (name: string, patch: ComboStrategy) => {
    const next = { ...strategies }
    const merged = { ...(next[name] ?? {}), ...patch }
    if (!merged.fallbackStrategy || merged.fallbackStrategy === 'fallback') delete next[name]
    else next[name] = merged
    try {
      await persistStrategies(next)
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to save strategy')
    }
  }

  const bulkSetStrategy = async (value: string) => {
    if (!value || selectedCombos.length === 0) return
    setBulkBusy(true)
    const next = { ...strategies }
    for (const c of selectedCombos) {
      if (value === 'fallback') delete next[c.name]
      else next[c.name] = { ...(next[c.name] ?? {}), fallbackStrategy: value }
    }
    try {
      await persistStrategies(next)
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to update strategy')
    } finally {
      setBulkBusy(false)
    }
  }

  const doDelete = async () => {
    if (!confirmDelete) return
    const ids = confirmDelete.map((c) => c.id)
    const names = confirmDelete.map((c) => c.name)
    setBulkBusy(true)
    try {
      const results = await Promise.allSettled(ids.map((id) => api.deleteCombo(id)))
      const failed = results.filter((r) => r.status === 'rejected').length
      await persistStrategies(prune(names))
      setCombos((prev) => prev.filter((c) => !ids.includes(c.id)))
      setSelectedIds((prev) => prev.filter((id) => !ids.includes(id)))
      if (failed > 0) notify.warning(`Deleted with ${failed} failure${failed === 1 ? '' : 's'}`)
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to delete combos')
    } finally {
      setBulkBusy(false)
      setConfirmDelete(null)
    }
  }

  const saveAdapter = async (next: CapacityAdapter) => {
    setAdapter(next)
    try {
      await api.patchSettings({ capacityAdapter: next })
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to save vision adapter')
    }
  }

  const saveCombo = async (name: string, models: string[], id?: string) => {
    if (id) await api.updateCombo(id, { name, models })
    else await api.createCombo({ name, models })
    setDraft(null)
    await load()
  }

  if (loading) return <p className="py-12 text-center text-xs text-muted-foreground">Loading combos…</p>

  return (
    <>
      <PageHeader
        title="Combo & Vision Adapter"
        description="Group models under one name, then pick a strategy per combo."
      />

      <ul className="mb-5 flex flex-col gap-1 text-sm text-text-muted">
        <li><span className="font-medium text-text-main">Fallback</span> — tries models in order (next on failure)</li>
        <li><span className="font-medium text-text-main">Round Robin</span> — rotates models across requests to spread load</li>
        <li>
          <span className="font-medium text-text-main">Smart Router</span> — queries a System One decision model to pick
          the optimal model before generation; Smart Switch / Strict Lock / Per-Turn modes maximise prompt-cache retention
        </li>
        <li>
          <span className="font-medium text-text-main">Fusion</span> — queries all models in parallel, then a judge
          synthesises one answer. Best quality, but every request bills all panel models + the judge (N+1 calls)
        </li>
      </ul>

      <div className="mb-4 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="relative w-full sm:w-64">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-text-muted" />
          <Input className="pl-9" placeholder="Search combos…" value={search} onChange={(e) => setSearch(e.target.value)} />
        </div>
        <Button size="sm" onClick={() => setDraft({ editing: null })}><Plus /> Create Combo</Button>
      </div>

      {combos.length === 0 ? (
        <Card className="py-12 text-center">
          <Icon name="layers" className="mx-auto mb-3 block text-[32px] text-primary" />
          <p className="mb-1 font-medium text-text-main">No combos yet</p>
          <p className="mb-4 text-sm text-text-muted">Create model combos with fallback support</p>
          <Button onClick={() => setDraft({ editing: null })}><Plus /> Create Combo</Button>
        </Card>
      ) : (
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-2 rounded-lg border border-border/60 bg-surface-2/40 px-3 py-2 sm:flex-row sm:items-center sm:justify-between">
            <label className="flex cursor-pointer select-none items-center gap-2 text-xs text-text-muted hover:text-primary">
              <input
                type="checkbox"
                className="size-3.5 accent-primary"
                checked={allSelected}
                ref={(el) => {
                  if (el) el.indeterminate = selectedIds.length > 0 && !allSelected
                }}
                onChange={() =>
                  setSelectedIds(allSelected ? [] : filtered.map((c) => c.id))
                }
              />
              <span>{selectedIds.length ? `${selectedIds.length} selected` : `Select all (${filtered.length})`}</span>
            </label>

            <div className="flex flex-wrap items-center gap-2">
              {q && <span className="text-xs text-text-muted">{filtered.length} of {combos.length} shown</span>}
              {selectedIds.length > 0 && (
                <>
                  <Select value="" onValueChange={bulkSetStrategy} disabled={bulkBusy}>
                    <SelectTrigger className="h-8 w-[200px] text-xs"><SelectValue placeholder="Set strategy…" /></SelectTrigger>
                    <SelectContent>
                      {STRATEGY_OPTIONS.map((o) => <SelectItem key={o.value} value={o.value}>{o.label}</SelectItem>)}
                    </SelectContent>
                  </Select>
                  <Button size="sm" variant="destructive" disabled={bulkBusy} onClick={() => setConfirmDelete(selectedCombos)}>
                    Delete ({selectedIds.length})
                  </Button>
                  <Button size="sm" variant="ghost" disabled={bulkBusy} onClick={() => setSelectedIds([])}>Clear</Button>
                </>
              )}
            </div>
          </div>

          {filtered.length === 0 ? (
            <Card className="py-10 text-center">
              <p className="mb-1 font-medium text-text-main">No combos match “{search.trim()}”</p>
              <p className="mb-4 text-sm text-text-muted">
                Try a shorter query, or clear the search to see all {combos.length} combos.
              </p>
              <Button variant="secondary" size="sm" onClick={() => setSearch('')}>Clear search</Button>
            </Card>
          ) : (
            filtered.map((combo) => (
              <ComboCard
                key={combo.id}
                combo={combo}
                providers={providers}
                connections={connections}
                strategies={strategies}
                comboByName={comboByName}
                getCaps={getCaps}
                selected={selectedIds.includes(combo.id)}
                onToggleSelect={() =>
                  setSelectedIds((prev) =>
                    prev.includes(combo.id) ? prev.filter((x) => x !== combo.id) : [...prev, combo.id],
                  )
                }
                onEdit={() => setDraft({ editing: combo })}
                onDelete={() => setConfirmDelete([combo])}
                onSetStrategy={(patch) => setStrategy(combo.name, patch)}
              />
            ))
          )}
        </div>
      )}

      <CapacityAdapterSection adapter={adapter} onChange={saveAdapter} getCaps={getCaps} />

      {draft && (
        <ComboFormModal
          key={draft.editing?.id ?? 'create'}
          combo={draft.editing}
          onClose={() => setDraft(null)}
          onSave={saveCombo}
        />
      )}

      <ConfirmModal
        isOpen={!!confirmDelete}
        onClose={() => setConfirmDelete(null)}
        onConfirm={doDelete}
        title={confirmDelete && confirmDelete.length > 1 ? 'Delete Selected Combos' : 'Delete Combo'}
        message={
          confirmDelete
            ? confirmDelete.length > 1
              ? `Delete ${confirmDelete.length} selected combos? This cannot be undone.`
              : `Delete combo “${confirmDelete[0].name}”?`
            : ''
        }
        confirmLabel="Delete"
        variant="danger"
        loading={bulkBusy}
      />
    </>
  )
}

function ComboCard({
  combo, providers, connections, strategies, comboByName, getCaps, selected, onToggleSelect, onEdit, onDelete, onSetStrategy,
}: {
  combo: Combo
  providers: ProviderSummary[]
  connections: Connection[]
  strategies: Record<string, ComboStrategy>
  comboByName: Record<string, string[]>
  getCaps: (m: string) => FullCaps | undefined
  selected: boolean
  onToggleSelect: () => void
  onEdit: () => void
  onDelete: () => void
  onSetStrategy: (patch: ComboStrategy) => void
}) {
  const [showJudge, setShowJudge] = useState(false)
  const [showRouter, setShowRouter] = useState(false)
  const [showPolicy, setShowPolicy] = useState(false)
  const { copied, copy } = useCopyToClipboard()
  const navigate = useNavigate()

  const strategy = strategies[combo.name] ?? {}
  const current = strategy.fallbackStrategy || 'fallback'
  const judge = strategy.judgeModel || ''
  const routerModel = strategy.routerModel || ''
  const isFusion = current === 'fusion'
  const isSmart = current === 'smart'
  const comboCaps = aggregateComboCapabilities(combo.models, comboByName, getCaps)

  const routerStatus = useMemo(() => {
    const model = routerModel.trim() || DEFAULT_ROUTER_MODEL
    const prefix = model.includes('/') ? model.slice(0, model.indexOf('/')) : model
    const p = providers.find((x) => x.alias === prefix || x.id === prefix)
    const providerId = p?.id ?? prefix
    if (p?.noAuth) return { ready: true, label: 'Ready (Free)', providerId }
    if (connections.some((c) => c.provider === providerId)) return { ready: true, label: 'Connected', providerId }
    return { ready: false, label: 'Not configured', providerId }
  }, [routerModel, providers, connections])

  return (
    <Card className={cn('p-3', selected && 'bg-primary/5 ring-1 ring-primary/40')}>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 flex-1 items-start gap-3">
          <input
            type="checkbox"
            checked={selected}
            onChange={onToggleSelect}
            aria-label={`Select ${combo.name}`}
            className="mt-1 size-4 shrink-0 accent-primary sm:mt-0"
          />
          <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary/10">
            <Icon name="layers" className="text-[18px] text-primary" />
          </div>
          <div className="min-w-0 flex-1">
            <code className="block truncate font-mono text-sm font-medium">{combo.name}</code>
            <div className="mt-1 flex min-w-0 flex-wrap items-center gap-1">
              {combo.models.length === 0 ? (
                <span className="text-xs italic text-text-muted">No models</span>
              ) : (
                combo.models.slice(0, 3).map((m, i) => (
                  <code key={`${m}-${i}`} className="inline-flex items-center gap-1 rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs text-text-muted">
                    <span>{m}</span>
                    <CapacityBadges caps={comboByName[m] ? aggregateComboCapabilities(comboByName[m], comboByName, getCaps) : getCaps(m)} />
                  </code>
                ))
              )}
              {combo.models.length > 3 && <span className="text-[10px] text-text-muted">+{combo.models.length - 3} more</span>}
            </div>
            {comboCaps && (
              <div className="mt-1 flex items-center gap-2 text-[10px] text-text-muted">
                <span>ctx {fmtK(comboCaps.contextWindow)}</span>
                <span className="opacity-40">·</span>
                <span>max {fmtK(comboCaps.maxOutput)}</span>
              </div>
            )}

            {isFusion && (
              <div className="mt-2 flex min-w-0 flex-wrap items-center gap-1.5">
                <span className="text-[11px] font-medium text-text-muted">Judge</span>
                <button
                  onClick={() => setShowJudge(true)}
                  title="Pick the model that fuses panel answers"
                  className="inline-flex max-w-full items-center gap-1 rounded border border-dashed border-primary/40 px-1.5 py-0.5 font-mono text-[11px] text-primary hover:border-primary hover:bg-primary/5"
                >
                  <Icon name="gavel" className="text-[13px]" />
                  <span className="truncate">{judge || `Auto — ${combo.models[0] || 'first model'}`}</span>
                </button>
                {judge && (
                  <button
                    onClick={() => onSetStrategy({ judgeModel: '' })}
                    title="Reset judge to Auto"
                    className="rounded p-0.5 text-text-muted hover:bg-danger/10 hover:text-danger"
                  >
                    <Icon name="close" className="text-[13px]" />
                  </button>
                )}
              </div>
            )}

            {isSmart && (
              <div className="mt-2 flex min-w-0 flex-wrap items-center gap-1.5">
                <span className="text-[11px] font-medium text-text-muted">Router</span>
                <button
                  onClick={() => setShowRouter(true)}
                  title="Pick the System One model that decides model routing"
                  className="inline-flex max-w-full items-center gap-1 rounded border border-dashed border-primary/40 px-1.5 py-0.5 font-mono text-[11px] text-primary hover:border-primary hover:bg-primary/5"
                >
                  <Icon name="psychology" className="text-[13px]" />
                  <span className="truncate">{routerModel || `Auto — ${DEFAULT_ROUTER_MODEL}`}</span>
                </button>
                {routerStatus.ready ? (
                  <Badge variant="success" size="sm" dot>{routerStatus.label}</Badge>
                ) : (
                  <span className="inline-flex items-center gap-1">
                    <Badge variant="error" size="sm" dot>{routerStatus.label}</Badge>
                    <button
                      onClick={() => navigate(`/dashboard/media-providers/systemone/${routerStatus.providerId}`)}
                      className="text-[10px] font-medium text-primary hover:underline"
                    >
                      Configure
                    </button>
                  </span>
                )}
                <button
                  onClick={() => setShowPolicy(true)}
                  title="Configure routing policy and prompt cache retention"
                  className="inline-flex items-center gap-1 rounded border border-border/70 bg-surface-2/50 px-2 py-0.5 text-[11px] text-text-main hover:border-primary/50 hover:bg-primary/5"
                >
                  <Icon name="tune" className="text-[13px] text-text-muted" />
                  <span className="font-medium">{policyLabel(strategy)}</span>
                </button>
                {routerModel && (
                  <button
                    onClick={() => onSetStrategy({ routerModel: '' })}
                    title="Reset router to Auto"
                    className="rounded p-0.5 text-text-muted hover:bg-danger/10 hover:text-danger"
                  >
                    <Icon name="close" className="text-[13px]" />
                  </button>
                )}
              </div>
            )}
          </div>
        </div>

        <div className="flex w-full flex-col gap-2 sm:w-auto sm:flex-row sm:items-center sm:gap-3">
          <Select value={current} onValueChange={(v) => onSetStrategy({ fallbackStrategy: v })}>
            <SelectTrigger className="h-8 w-full text-xs sm:w-[200px]"><SelectValue /></SelectTrigger>
            <SelectContent>
              {STRATEGY_OPTIONS.map((o) => <SelectItem key={o.value} value={o.value}>{o.label}</SelectItem>)}
            </SelectContent>
          </Select>

          <div className="grid grid-cols-3 gap-1 sm:flex">
            <IconAction
              icon={copied === `combo-${combo.id}` ? 'check' : 'content_copy'}
              label="Copy"
              onClick={() => copy(combo.name, `combo-${combo.id}`)}
            />
            <IconAction icon="edit" label="Edit" onClick={onEdit} />
            <IconAction icon="delete" label="Delete" danger onClick={onDelete} />
          </div>
        </div>
      </div>

      {showJudge && (
        <ModelSelectModal
          isOpen
          onClose={() => setShowJudge(false)}
          onSelect={(m) => { onSetStrategy({ judgeModel: m.value }); setShowJudge(false) }}
          title="Select Judge Model"
          addedModelValues={judge ? [judge] : []}
        />
      )}
      {showRouter && (
        <ModelSelectModal
          isOpen
          onClose={() => setShowRouter(false)}
          onSelect={(m) => { onSetStrategy({ routerModel: m.value }); setShowRouter(false) }}
          title="Select System One Router Model"
          kindFilter="systemone"
          addedModelValues={routerModel ? [routerModel] : []}
        />
      )}
      {showPolicy && (
        <SmartPolicyModal
          isOpen
          onClose={() => setShowPolicy(false)}
          comboName={combo.name}
          currentPolicy={strategy.smartPolicy || (strategy.sessionSticky === false ? 'per-turn' : 'smart')}
          currentThreshold={strategy.confidenceThreshold ?? 0.8}
          onSave={(p: SmartPolicy) => onSetStrategy(p)}
        />
      )}
    </Card>
  )
}

function IconAction({ icon, label, onClick, danger }: { icon: string; label: string; onClick: () => void; danger?: boolean }) {
  return (
    <button
      onClick={onClick}
      title={label}
      className={cn(
        'flex flex-col items-center rounded px-2 py-1 transition-colors',
        danger ? 'text-danger hover:bg-danger/10' : 'text-text-muted hover:bg-surface-2 hover:text-primary',
      )}
    >
      <Icon name={icon} className="text-[18px]" />
      <span className="text-[10px] leading-tight">{label}</span>
    </button>
  )
}

function CapacityAdapterSection({
  adapter, onChange, getCaps,
}: {
  adapter: CapacityAdapter
  onChange: (next: CapacityAdapter) => void
  getCaps: (m: string) => FullCaps | undefined
}) {
  return (
    <div className="mt-8 flex flex-col gap-3">
      <div>
        <p className="text-sm font-medium">Vision Adapter</p>
        <p className="mt-0.5 text-xs text-text-muted">
          Your model can’t read image/audio? Auto-switches to a model in the pool below.
        </p>
      </div>
      {CAPACITY_ADAPTER_CAPS.map((cap) => (
        <CapacityAdapterCap
          key={cap.key}
          cap={cap}
          entry={adapter[cap.key] ?? EMPTY_CAP_ENTRY}
          onChange={(entry) => onChange({ ...adapter, [cap.key]: entry })}
          getCaps={getCaps}
        />
      ))}
    </div>
  )
}

function CapacityAdapterCap({
  cap, entry, onChange, getCaps,
}: {
  cap: { key: string; label: string; icon: string; desc: string }
  entry: CapEntry
  onChange: (e: CapEntry) => void
  getCaps: (m: string) => FullCaps | undefined
}) {
  const [showPicker, setShowPicker] = useState(false)
  const { enabled, roundRobin, models } = entry
  const patch = (p: Partial<CapEntry>) => onChange({ ...entry, ...p })

  // An empty pool would silently disable the adapter, so it re-seeds the free default.
  const dropTo = (next: string[]) => patch({ models: next.length === 0 ? [DEFAULT_FALLBACK_MODEL] : next })

  return (
    <Card className={cn('p-3', !enabled && 'opacity-50')}>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 flex-1 items-center gap-2.5">
          <Switch checked={enabled} onCheckedChange={(v) => patch({ enabled: v })} aria-label={`Enable ${cap.label} adapter`} />
          <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary/10">
            <Icon name={cap.icon} className="text-[18px] text-primary" />
          </div>
          <div className="min-w-0 flex-1">
            <code className="font-mono text-sm font-medium">{cap.label}</code>
            <span className="ml-1.5 text-[10px] text-text-muted">— {cap.desc}</span>
          </div>
        </div>
        <div className="flex items-center gap-3">
          <label className="flex cursor-pointer select-none items-center gap-1.5 text-xs text-text-muted">
            <Switch checked={roundRobin} disabled={!enabled} onCheckedChange={(v) => patch({ roundRobin: v })} aria-label={`Round-robin ${cap.label}`} />
            Round
          </label>
          <Button variant="ghost" size="sm" disabled={!enabled} onClick={() => setShowPicker(true)} title={`Add ${cap.label} model`}>
            <Plus /> Add Model
          </Button>
        </div>
      </div>

      {models.length === 0 ? (
        <p className="mt-3 py-2 text-center text-xs italic text-text-muted">
          No models in pool (will fallback to {DEFAULT_FALLBACK_MODEL})
        </p>
      ) : (
        <div className="mt-3 overflow-hidden rounded-lg border border-border/50 text-xs">
          <table className="w-full text-left">
            <thead>
              <tr className="border-b border-border/40 bg-surface-2/60 text-text-muted">
                <th className="w-12 px-3 py-1.5 text-center font-medium">#</th>
                <th className="px-3 py-1.5 font-medium">Model</th>
                <th className="w-24 px-3 py-1.5 text-center font-medium">Order</th>
                <th className="w-12 px-3 py-1.5" />
              </tr>
            </thead>
            <tbody className="divide-y divide-border/30 font-mono">
              {models.map((model, i) => (
                <tr key={`${model}-${i}`} className="hover:bg-surface-2/40">
                  <td className="px-3 py-2 text-center font-sans text-[11px] text-text-muted">#{i + 1}</td>
                  <td className="px-3 py-2">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="truncate">{model}</span>
                      <CapacityBadges caps={getCaps(model)} />
                      {model === DEFAULT_FALLBACK_MODEL && (
                        <span className="rounded bg-success/10 px-1.5 py-0.5 font-sans text-[10px] font-medium text-success">
                          free default
                        </span>
                      )}
                    </div>
                  </td>
                  <td className="px-3 py-2 text-center">
                    <div className="inline-flex items-center gap-1">
                      <button
                        disabled={!enabled || i === 0}
                        onClick={() => dropTo(moveItem(models, i, i - 1))}
                        title="Move up"
                        className="rounded p-1 text-text-muted hover:text-primary disabled:opacity-30"
                      >
                        <Icon name="arrow_upward" className="text-[16px]" />
                      </button>
                      <button
                        disabled={!enabled || i === models.length - 1}
                        onClick={() => dropTo(moveItem(models, i, i + 1))}
                        title="Move down"
                        className="rounded p-1 text-text-muted hover:text-primary disabled:opacity-30"
                      >
                        <Icon name="arrow_downward" className="text-[16px]" />
                      </button>
                    </div>
                  </td>
                  <td className="px-3 py-2 text-right">
                    <button
                      disabled={!enabled}
                      onClick={() => dropTo(models.filter((_, j) => j !== i))}
                      title="Remove model"
                      className="rounded p-1 text-text-muted hover:bg-danger/10 hover:text-danger disabled:opacity-30"
                    >
                      <X className="size-4" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showPicker && (
        <ModelSelectModal
          isOpen
          onClose={() => setShowPicker(false)}
          onSelect={(m: PickerModel) => {
            if (m.value && !models.includes(m.value)) patch({ models: [...models, m.value] })
          }}
          onDeselect={(m: PickerModel) => dropTo(models.filter((x) => x !== m.value))}
          title={`Add ${cap.label} Model`}
          addedModelValues={models}
          capFilter={cap.key as keyof Caps}
          closeOnSelect={false}
        />
      )}
    </Card>
  )
}

function ComboFormModal({
  combo, onClose, onSave,
}: {
  combo: Combo | null
  onClose: () => void
  onSave: (name: string, models: string[], id?: string) => Promise<void>
}) {
  const [name, setName] = useState(combo?.name ?? '')
  const [models, setModels] = useState<string[]>(combo?.models ?? [])
  const [aliases, setAliases] = useState<Record<string, string>>({})
  const [showPicker, setShowPicker] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    api.aliases().then((d) => setAliases(d.aliases ?? {})).catch(() => {})
  }, [])

  const validate = (value: string) => {
    if (!value.trim()) return 'Name is required'
    if (!VALID_NAME_REGEX.test(value)) return 'Only letters, numbers, -, _ and . allowed'
    return ''
  }

  const save = async () => {
    const err = validate(name)
    setError(err)
    if (err) return
    setSaving(true)
    try {
      await onSave(name.trim(), models, combo?.id)
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <Modal
        isOpen
        onClose={onClose}
        title={combo ? 'Edit Combo' : 'Create Combo'}
        footer={
          <div className="flex gap-2">
            <Button variant="ghost" size="sm" className="flex-1" onClick={onClose}>Cancel</Button>
            <Button size="sm" className="flex-1" onClick={save} disabled={!name.trim() || !!error || saving}>
              {saving ? 'Saving…' : combo ? 'Save' : 'Create'}
            </Button>
          </div>
        }
      >
        <div className="flex flex-col gap-3">
          <div>
            <Label htmlFor="combo-name">Combo Name</Label>
            <Input
              id="combo-name"
              className="mt-1"
              autoFocus
              placeholder="my-combo"
              value={name}
              onChange={(e) => { setName(e.target.value); setError(e.target.value ? validate(e.target.value) : '') }}
            />
            <p className="mt-0.5 text-[10px] text-text-muted">
              {error || 'Only letters, numbers, -, _ and . allowed'}
            </p>
          </div>

          <div>
            <Label>Models</Label>
            {models.length === 0 ? (
              <div className="mt-1.5 rounded-lg border border-dashed border-border/60 py-4 text-center">
                <Icon name="layers" className="mb-1 block text-xl text-text-muted" />
                <p className="text-xs text-text-muted">No models added yet</p>
              </div>
            ) : (
              <div className="mt-1.5 flex max-h-[350px] flex-col gap-1 overflow-y-auto">
                {models.map((model, i) => (
                  <div key={`${model}-${i}`} className="group flex min-w-0 items-center gap-1.5 rounded-md bg-surface-2/50 px-2 py-1">
                    <span className="w-3 shrink-0 text-center text-[10px] font-medium text-text-muted">{i + 1}</span>
                    <span className="min-w-0 flex-1 truncate font-mono text-xs" title={model}>{model}</span>
                    <button
                      disabled={i === 0}
                      onClick={() => setModels(moveItem(models, i, i - 1))}
                      className="rounded p-0.5 text-text-muted hover:text-primary disabled:opacity-20"
                      title="Move up"
                    >
                      <Icon name="arrow_upward" className="text-[12px]" />
                    </button>
                    <button
                      disabled={i === models.length - 1}
                      onClick={() => setModels(moveItem(models, i, i + 1))}
                      className="rounded p-0.5 text-text-muted hover:text-primary disabled:opacity-20"
                      title="Move down"
                    >
                      <Icon name="arrow_downward" className="text-[12px]" />
                    </button>
                    <button
                      onClick={() => setModels(models.filter((_, j) => j !== i))}
                      className="rounded p-0.5 text-text-muted hover:bg-danger/10 hover:text-danger"
                      title="Remove"
                    >
                      <X className="size-3" />
                    </button>
                  </div>
                ))}
              </div>
            )}
            {/* ponytail: arrows instead of dnd-kit; add drag when the kit is already a dep. */}
            <button
              onClick={() => setShowPicker(true)}
              className="mt-2 flex w-full items-center justify-center gap-1 rounded-lg border border-dashed border-border/60 py-2 text-xs font-medium text-primary hover:border-primary/50"
            >
              <Plus className="size-4" /> Add Model
            </button>
          </div>
        </div>
      </Modal>

      {showPicker && (
        <ModelSelectModal
          isOpen
          onClose={() => setShowPicker(false)}
          onSelect={(m) => { if (!models.includes(m.value)) setModels([...models, m.value]) }}
          onDeselect={(m) => setModels(models.filter((x) => x !== m.value))}
          modelAliases={aliases}
          title="Add Model to Combo"
          addedModelValues={models}
          closeOnSelect={false}
        />
      )}
    </>
  )
}
