import { useEffect, useMemo, useState } from 'react'
import {
  api, type Combo, type Connection, type CustomModel, type ProviderNode, type ProviderSummary,
} from '@/lib/api'
import { matchesSearch, parseSearchQuery } from '@/lib/model-search'
import { resolveCaps, useModelCaps } from '@/lib/use-model-caps'
import { CapacityBadges, type Caps } from '@/components/capacity-badges'
import { Icon } from '@/components/icon'
import { ProviderIcon } from '@/components/provider-icon'
import { Modal } from '@/components/modal'
import { cn } from '@/lib/utils'

/**
 * Port of src/shared/components/ModelSelectModal.js against this router's own
 * APIs: provider + model tables come from /api/providers (one round trip) and
 * capabilities from /api/models. Grouping, search semantics (regex, `!term`),
 * batch select and provider wildcards follow the reference.
 */
export type PickerModel = {
  id: string
  name: string
  value: string
  kind?: string
  isCustom?: boolean
  isPlaceholder?: boolean
}

type Group = {
  name: string
  alias: string
  color?: string
  models: PickerModel[]
}

const PROVIDER_AS_MODEL_KINDS = new Set(['webSearch', 'webFetch'])
const TYPED_KINDS = new Set(['image', 'tts', 'stt', 'embedding', 'imageToText', 'systemone'])
const ALLOW_PROVIDER_FALLBACK_KINDS = new Set(['tts', 'image', 'webFetch'])
const LIVE_CATALOG_PROVIDERS = ['cursor', 'cline', 'clinepass', 'zed']

export function ModelSelectModal({
  isOpen,
  onClose,
  onSelect,
  onDeselect,
  onSelectBatch,
  onDeselectBatch,
  selectedModel,
  title = 'Select Model',
  modelAliases = {},
  kindFilter = null,
  capFilter = null,
  addedModelValues = [],
  closeOnSelect = true,
  allowProviderWildcard = false,
}: {
  isOpen: boolean
  onClose: () => void
  onSelect: (m: PickerModel) => void
  onDeselect?: (m: PickerModel) => void
  onSelectBatch?: (models: PickerModel[]) => void
  onDeselectBatch?: (models: PickerModel[]) => void
  selectedModel?: string
  title?: string
  modelAliases?: Record<string, string>
  kindFilter?: string | null
  capFilter?: (keyof Caps) | null
  addedModelValues?: string[]
  closeOnSelect?: boolean
  allowProviderWildcard?: boolean
}) {
  const [search, setSearch] = useState('')
  const [providers, setProviders] = useState<ProviderSummary[]>([])
  const [connections, setConnections] = useState<Connection[]>([])
  const [combos, setCombos] = useState<Combo[]>([])
  const [nodes, setNodes] = useState<ProviderNode[]>([])
  const [customModels, setCustomModels] = useState<CustomModel[]>([])
  const [disabled, setDisabled] = useState<Record<string, string[]>>({})
  const [liveModels, setLiveModels] = useState<Record<string, PickerModel>>({})
  const capsTable = useModelCaps()

  useEffect(() => {
    if (!isOpen) return
    api.providers().then((d) => { setProviders(d.providers); setConnections(d.connections) }).catch(() => {})
    api.combos().then((d) => setCombos(d.combos.filter((c) => !c.kind || c.kind === 'llm'))).catch(() => {})
    api.providerNodes().then((d) => setNodes(d.nodes)).catch(() => {})
    api.customModels().then((d) => setCustomModels(d.models ?? [])).catch(() => {})
    api.disabledModels().then((d) => setDisabled(d.disabled ?? {})).catch(() => {})
  }, [isOpen])

  const byId = useMemo(() => Object.fromEntries(providers.map((p) => [p.id, p])), [providers])
  const activeProviderIds = useMemo(() => {
    const ids = new Set(connections.filter((c) => c.isActive !== false).map((c) => c.provider))
    return [...ids]
  }, [connections])

  // Per-account catalogues for providers whose static list goes stale.
  const liveIds = useMemo(
    () => connections.filter((c) => c.isActive !== false && LIVE_CATALOG_PROVIDERS.includes(c.provider)).map((c) => c.id),
    [connections],
  )
  useEffect(() => {
    if (!isOpen || liveIds.length === 0) return
    let cancelled = false
    void Promise.all(
      liveIds.map(async (id) => {
        try {
          const r = await fetch(`/api/providers/${id}/models`, { cache: 'no-store' })
          if (!r.ok) return []
          const body = (await r.json()) as { models?: { id: string; name?: string }[] }
          return body.models ?? []
        } catch {
          return []
        }
      }),
    ).then((lists) => {
      if (cancelled) return
      const out: Record<string, PickerModel> = {}
      const conns = connections.filter((c) => liveIds.includes(c.id))
      lists.forEach((models, i) => {
        const alias = byId[conns[i]?.provider]?.alias ?? conns[i]?.provider ?? ''
        for (const m of models) {
          const key = `${alias}/${m.id}`
          out[key] = { id: m.id, name: m.name || m.id, value: key }
        }
      })
      setLiveModels(out)
    })
    return () => { cancelled = true }
  }, [isOpen, liveIds, connections, byId])

  const grouped = useMemo(() => {
    const groups: Record<string, Group> = {}
    const enabledIds = [
      ...activeProviderIds,
      ...providers.filter((p) => p.noAuth).map((p) => p.id),
    ]
    for (const id of enabledIds) {
      const p = byId[id]
      if (!p) continue
      const kinds = p.serviceKinds ?? ['llm']
      if (kindFilter && !kinds.includes(kindFilter)) continue
      const alias = p.alias || id
      const isNode = nodes.some((n) => n.id === id)

      if (kindFilter && PROVIDER_AS_MODEL_KINDS.has(kindFilter)) {
        groups[id] = { name: p.display.name, alias, color: p.display.color, models: [{ id, name: p.display.name, value: id }] }
        continue
      }

      const node = nodes.find((n) => n.id === id)
      const prefix = (node?.prefix as string) || alias
      const displayName = node?.name || p.display.name

      const aliasModels = Object.entries(modelAliases)
        .filter(([, full]) => full.startsWith(`${alias}/`))
        .map(([name, full]) => ({ id: full.slice(alias.length + 1), name, value: full }))
      const registered = customModels
        .filter((m) => m.providerAlias === (isNode ? id : alias))
        .map((m) => ({
          id: m.id,
          name: m.name || m.id,
          value: isNode ? `${prefix}/${m.id}` : `${alias}/${m.id}`,
          kind: m.type,
          isCustom: true,
        }))

      let models: PickerModel[]
      if (isNode) {
        const merged = [
          ...Object.entries(modelAliases)
            .filter(([, full]) => full.startsWith(`${id}/`))
            .map(([name, full]) => ({ id: full.slice(id.length + 1), name, value: `${prefix}/${full.slice(id.length + 1)}` })),
          ...registered,
        ]
        models = merged.length > 0
          ? merged
          : [{ id: `__placeholder__${id}`, name: `${prefix}/model-id`, value: `${prefix}/model-id`, isPlaceholder: true }]
      } else {
        const live: PickerModel[] = Object.values(liveModels).filter((m) => m.value.startsWith(`${alias}/`))
        const table: PickerModel[] = (p.models ?? [])
          .filter((m) => !kindFilter || m.kind === kindFilter || (!m.kind && !kindFilter))
          .map((m) => ({ id: m.id, name: m.name || m.id, value: `${alias}/${m.id}`, kind: m.kind }))
        const base = live.length > 0 ? live : table
        const seen = new Set(base.map((m) => m.value))
        const hardcodedIds = new Set(base.map((m) => m.id))
        const extraAliases = aliasModels.filter(
          (m) => !seen.has(m.value) && (!base.length || m.name === m.id) && !hardcodedIds.has(m.id),
        )
        const extraRegistered = registered.filter((m) => !seen.has(m.value) && !hardcodedIds.has(m.id))
        models = [...base, ...extraAliases, ...extraRegistered]
      }

      if (kindFilter && TYPED_KINDS.has(kindFilter) && !isNode) {
        models = models.filter((m) => m.isPlaceholder || m.kind === kindFilter)
      }
      if (models.length === 0 && kindFilter && ALLOW_PROVIDER_FALLBACK_KINDS.has(kindFilter) && kinds.includes(kindFilter)) {
        models = [{ id, name: p.display.name, value: alias }]
      }

      // Disabled ids are stored under the alias or the provider id.
      const off = new Set([...(disabled[alias] ?? []), ...(disabled[id] ?? [])])
      if (off.size > 0) models = models.filter((m) => !off.has(m.id))
      // A connection may pin its own enabledModels list (codex does on KineRouter).
      const pinned = connections
        .filter((c) => c.provider === id && c.isActive !== false)
        .flatMap((c) => {
          const e = (c.providerSpecificData as { enabledModels?: unknown } | undefined)?.enabledModels
          return Array.isArray(e) ? (e as string[]) : []
        })
      if (pinned.length > 0) {
        const set = new Set(pinned)
        models = models.filter((m) => set.has(m.id) || set.has(m.value) || set.has(`${alias}/${m.id}`))
      }

      if (models.length > 0 || isNode) {
        groups[id] = { name: displayName, alias: prefix, color: p.display.color, models }
      }
    }
    return groups
  }, [activeProviderIds, byId, providers, nodes, modelAliases, customModels, disabled, kindFilter, connections, liveModels])

  const parsed = useMemo(() => parseSearchQuery(search), [search])

  const filteredCombos = useMemo(() => {
    if (kindFilter || capFilter) return []
    if (parsed.isEmpty) return combos
    return combos.filter((c) => matchesSearch(c.name, parsed))
  }, [combos, parsed, kindFilter, capFilter])

  const filteredGroups = useMemo(() => {
    const add = (m: PickerModel) => addedModelValues.includes(m.value)
    const sortModels = (ms: PickerModel[]) => [
      ...ms.filter(add).sort((a, b) => a.name.localeCompare(b.name)),
      ...ms.filter((m) => !add(m)).sort((a, b) => a.name.localeCompare(b.name)),
    ]
    const out: Record<string, Group> = {}
    for (const [id, group] of Object.entries(grouped)) {
      let models = group.models
      if (capFilter) models = models.filter((m) => resolveCaps(capsTable, m.value, modelAliases)?.[capFilter] === true)
      if (!parsed.isEmpty) {
        const groupMatches = matchesSearch(group.name, parsed)
        const wildcardMatches =
          allowProviderWildcard && (matchesSearch(group.alias, parsed) || matchesSearch(id, parsed) || matchesSearch(`${group.alias}/*`, parsed))
        models = models.filter((m) => {
          if (matchesSearch(m, parsed)) return true
          if (groupMatches || wildcardMatches) {
            return parsed.negativeTerms.length > 0
              ? matchesSearch(m, { ...parsed, positiveTerms: [] })
              : true
          }
          return false
        })
      }
      if (models.length === 0) continue
      out[id] = { ...group, models: sortModels(models) }
    }
    return out
  }, [grouped, parsed, addedModelValues, allowProviderWildcard, capFilter, capsTable, modelAliases])

  const visible = useMemo(
    () => Object.values(filteredGroups).flatMap((g) => g.models.filter((m) => !m.isPlaceholder)),
    [filteredGroups],
  )
  const unadded = visible.filter((m) => !addedModelValues.includes(m.value))
  const added = visible.filter((m) => addedModelValues.includes(m.value))

  const pick = (model: PickerModel) => {
    if (addedModelValues.includes(model.value)) onDeselect?.(model)
    else onSelect(model)
    if (closeOnSelect) {
      onClose()
      setSearch('')
    }
  }

  const chip = (m: PickerModel) => {
    const isAdded = addedModelValues.includes(m.value)
    return (
      <button
        key={m.value}
        onClick={() => pick(m)}
        className={cn(
          'flex items-center gap-1 rounded-xl border px-2 py-1 text-xs font-medium transition-colors',
          m.isPlaceholder
            ? 'border-dashed border-border bg-surface italic text-text-muted hover:text-primary'
            : isAdded || selectedModel === m.value
              ? 'border-primary bg-primary text-white'
              : 'border-border bg-surface text-text-main hover:border-primary/50 hover:bg-primary/5',
        )}
      >
        {isAdded && !m.isPlaceholder && <Icon name="check" className="text-[10px]" />}
        {m.name}
        {m.isCustom && <span className="text-[9px] opacity-60">custom</span>}
        <CapacityBadges caps={resolveCaps(capsTable, m.value, modelAliases)} />
      </button>
    )
  }

  return (
    <Modal isOpen={isOpen} onClose={() => { onClose(); setSearch('') }} title={title} size="xl">
      <div className="mb-3 flex items-start gap-2 rounded-lg border border-primary/20 bg-primary/10 px-2.5 py-2 text-xs text-text-muted">
        <Icon name="info" className="shrink-0 text-[14px] text-primary" />
        <span>
          {allowProviderWildcard
            ? 'Click a model to add or remove it, or a provider name for a wildcard (provider/*).'
            : 'Click a model to add, click again to remove.'}{' '}
          Search supports exclusions (claude -haiku) and RegEx (/pattern/).
        </span>
      </div>

      <div className="mb-2">
        <div className="relative">
          <Icon name="search" className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[16px] text-text-muted" />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search models (e.g. claude -haiku, /regex/)…"
            className="w-full rounded border border-border bg-surface py-1.5 pl-8 pr-20 text-xs focus:outline-none focus:ring-1 focus:ring-primary/50"
          />
          <div className="absolute right-2 top-1/2 flex -translate-y-1/2 items-center gap-1">
            {parsed.isRegex && <span className="rounded bg-primary/10 px-1 font-mono text-[10px] text-primary">regex</span>}
            {!parsed.isRegex && parsed.negativeTerms.length > 0 && (
              <span className="rounded bg-warning/10 px-1 font-mono text-[10px] text-warning">!exclude</span>
            )}
            {search && (
              <button onClick={() => setSearch('')} title="Clear search" className="p-0.5 text-text-muted hover:text-text-main">
                <Icon name="close" className="text-[14px]" />
              </button>
            )}
          </div>
        </div>
        {!closeOnSelect && visible.length > 0 && (
          <div className="mt-1.5 flex items-center justify-between px-1 text-[11px] text-text-muted">
            <span>
              {visible.length} models
              {addedModelValues.length > 0 && <span className="ml-1.5 font-medium text-primary">({addedModelValues.length} selected)</span>}
            </span>
            <div className="flex gap-1.5">
              {unadded.length > 0 && (
                <button
                  onClick={() => (onSelectBatch ? onSelectBatch(unadded) : unadded.forEach(onSelect))}
                  className="rounded bg-primary/10 px-2 py-0.5 font-medium text-primary hover:bg-primary/20"
                >
                  Select All ({unadded.length})
                </button>
              )}
              {added.length > 0 && (
                <button
                  onClick={() => (onDeselectBatch ? onDeselectBatch(added) : added.forEach((m) => onDeselect?.(m)))}
                  className="rounded bg-border/40 px-2 py-0.5 hover:text-text-main"
                >
                  Deselect All ({added.length})
                </button>
              )}
            </div>
          </div>
        )}
      </div>

      <div className="max-h-[400px] space-y-3 overflow-y-auto">
        {filteredCombos.length > 0 && (
          <div>
            <div className="sticky top-0 flex items-center gap-1.5 bg-surface py-0.5">
              <Icon name="layers" className="text-[14px] text-primary" />
              <span className="text-xs font-medium text-primary">Combos</span>
              <span className="text-[10px] text-text-muted">({filteredCombos.length})</span>
            </div>
            <div className="flex flex-wrap gap-1.5">
              {filteredCombos.map((c) => chip({ id: c.name, name: c.name, value: c.name }))}
            </div>
          </div>
        )}

        {Object.entries(filteredGroups).map(([id, group]) => {
          const wildcardValue = `${group.alias}/*`
          const wildcardAdded = allowProviderWildcard && addedModelValues.includes(wildcardValue)
          return (
            <div key={id}>
              <div className="sticky top-0 z-10 flex items-center gap-1.5 bg-surface py-0.5">
                <ProviderIcon provider={id} size={14} fallbackText={group.name.slice(0, 2).toUpperCase()} />
                {allowProviderWildcard ? (
                  <button
                    onClick={() => pick({ id: wildcardValue, name: wildcardValue, value: wildcardValue })}
                    className={cn(
                      'inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-xs font-medium',
                      wildcardAdded
                        ? 'border-primary bg-primary text-white'
                        : 'border-transparent text-primary hover:border-primary/20 hover:bg-primary/10',
                    )}
                  >
                    {wildcardAdded && <Icon name="check" className="text-[12px]" />}
                    {group.name}
                    <span className="rounded bg-surface-2 px-1 font-mono text-[10px] text-text-muted">{wildcardValue}</span>
                  </button>
                ) : (
                  <span className="text-xs font-medium text-primary">{group.name}</span>
                )}
                <span className="text-[10px] text-text-muted">({group.models.length})</span>
              </div>
              <div className="flex flex-wrap gap-1.5">{group.models.map(chip)}</div>
            </div>
          )
        })}

        {Object.keys(filteredGroups).length === 0 && filteredCombos.length === 0 && (
          <div className="py-4 text-center text-text-muted">
            <Icon name="search_off" className="block text-2xl" />
            <p className="text-xs">No models found</p>
          </div>
        )}
      </div>
    </Modal>
  )
}
