import type { UsageStat } from '@/lib/api'

export const PERIODS = [
  { value: 'today', label: 'Today' },
  { value: '24h', label: '24h' },
  { value: '7d', label: '7D' },
  { value: '30d', label: '30D' },
  { value: '60d', label: '60D' },
  { value: 'all', label: 'All' },
] as const

export const fmt = (n: number | null | undefined) => new Intl.NumberFormat().format(n || 0)
export const fmtCost = (n: number | null | undefined) => `$${(n || 0).toFixed(2)}`

export function fmtTokens(n: number | null | undefined) {
  const v = n || 0
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`
  if (v >= 1000) return `${(v / 1000).toFixed(1)}K`
  return String(v)
}

/** UsageTable's relative formatter ("Just now", "5m ago", date). */
export function fmtTime(iso: string | null | undefined): string {
  if (!iso) return 'Never'
  const when = new Date(iso)
  if (Number.isNaN(when.getTime())) return iso
  const diffMins = Math.floor((Date.now() - when.getTime()) / 60000)
  if (diffMins < 1) return 'Just now'
  if (diffMins < 60) return `${diffMins}m ago`
  if (diffMins < 1440) return `${Math.floor(diffMins / 60)}h ago`
  return when.toLocaleDateString()
}

/** RecentRequests' ticking formatter ("3s ago" … "2d ago"). */
export function timeAgo(iso: string): string {
  const diff = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (diff < 60) return `${diff}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return `${Math.floor(diff / 86400)}d ago`
}

export type Row = UsageStat & {
  key: string
  totalTokens: number
  totalCost: number
  inputCost: number
  cachedCost: number
  outputCost: number
  pending: number
}

/**
 * Cost split is a token-share allocation of the rate-accurate server total,
 * not a per-rate recompute. `cached` is a subset of prompt, so peel it out of
 * the input share.
 * ponytail: upgrade to a stored per-component cost breakdown if exact
 * cached-rate cost display is ever needed.
 */
export function sortData(
  dataMap: Record<string, UsageStat> | undefined,
  pendingMap: Record<string, number> = {},
  sortBy: string,
  sortOrder: 'asc' | 'desc',
): Row[] {
  return Object.entries(dataMap || {})
    .map(([key, data]) => {
      const totalTokens = (data.promptTokens || 0) + (data.completionTokens || 0)
      const totalCost = data.cost || 0
      const cachedTokens = data.cachedTokens || 0
      const nonCachedInput = Math.max(0, (data.promptTokens || 0) - cachedTokens)
      const share = totalTokens > 0 ? totalCost / totalTokens : 0
      return {
        ...data,
        key,
        totalTokens,
        totalCost,
        inputCost: nonCachedInput * share,
        cachedCost: cachedTokens * share,
        outputCost: (data.completionTokens || 0) * share,
        pending: pendingMap[key] || 0,
      }
    })
    .sort((a, b) => {
      const av = a[sortBy as keyof Row]
      const bv = b[sortBy as keyof Row]
      const na = typeof av === 'string' ? av.toLowerCase() : av
      const nb = typeof bv === 'string' ? bv.toLowerCase() : bv
      if ((na ?? '') < (nb ?? '')) return sortOrder === 'asc' ? -1 : 1
      if ((na ?? '') > (nb ?? '')) return sortOrder === 'asc' ? 1 : -1
      return 0
    })
}

export type GroupField = 'rawModel' | 'accountName' | 'keyName' | 'endpoint'

export function groupKeyOf(item: Row, field: GroupField): string {
  switch (field) {
    case 'rawModel':
      return item.rawModel || 'Unknown Model'
    case 'accountName':
      return item.accountName || `Account ${item.connectionId?.slice(0, 8) ?? ''}...`
    case 'keyName':
      return item.apiKeyMasked ? `${item.keyName} (${item.apiKeyMasked})` : item.keyName || 'Unknown Key'
    default:
      return item.endpoint || 'Unknown Endpoint'
  }
}

export type GroupSummary = {
  requests: number
  promptTokens: number
  completionTokens: number
  cachedTokens: number
  totalTokens: number
  cost: number
  totalCost: number
  inputCost: number
  cachedCost: number
  outputCost: number
  pending: number
  lastUsed: string | null
}

export type Group = { groupKey: string; summary: GroupSummary; items: Row[] }

export function groupDataByKey(data: Row[], field: GroupField): Group[] {
  const groups = new Map<string, Group>()
  for (const item of data) {
    const gk = groupKeyOf(item, field)
    let group = groups.get(gk)
    if (!group) {
      group = {
        groupKey: gk,
        summary: {
          requests: 0, promptTokens: 0, completionTokens: 0, cachedTokens: 0,
          totalTokens: 0, cost: 0, inputCost: 0, cachedCost: 0, outputCost: 0,
          totalCost: 0, pending: 0, lastUsed: null,
        },
        items: [],
      }
      groups.set(gk, group)
    }
    const s = group.summary
    s.requests += item.requests || 0
    s.promptTokens += item.promptTokens || 0
    s.completionTokens += item.completionTokens || 0
    s.cachedTokens += item.cachedTokens || 0
    s.totalTokens += item.totalTokens || 0
    s.cost += item.cost || 0
    s.totalCost += item.totalCost || 0
    s.inputCost += item.inputCost || 0
    s.cachedCost += item.cachedCost || 0
    s.outputCost += item.outputCost || 0
    s.pending += item.pending || 0
    if (item.lastUsed && (!s.lastUsed || new Date(item.lastUsed) > new Date(s.lastUsed))) {
      s.lastUsed = item.lastUsed
    }
    group.items.push(item)
  }
  return [...groups.values()]
}

export type Column = { field: string; label: string; align?: 'right' }

export const MODEL_COLUMNS: Column[] = [
  { field: 'rawModel', label: 'Model' },
  { field: 'provider', label: 'Provider' },
  { field: 'requests', label: 'Requests', align: 'right' },
  { field: 'lastUsed', label: 'Last Used', align: 'right' },
]

export const ACCOUNT_COLUMNS: Column[] = [
  { field: 'rawModel', label: 'Model' },
  { field: 'provider', label: 'Provider' },
  { field: 'accountName', label: 'Account' },
  { field: 'requests', label: 'Requests', align: 'right' },
  { field: 'lastUsed', label: 'Last Used', align: 'right' },
]

export const API_KEY_COLUMNS: Column[] = [
  { field: 'keyName', label: 'API Key Name' },
  { field: 'rawModel', label: 'Model' },
  { field: 'provider', label: 'Provider' },
  { field: 'requests', label: 'Requests', align: 'right' },
  { field: 'lastUsed', label: 'Last Used', align: 'right' },
]

export const ENDPOINT_COLUMNS: Column[] = [
  { field: 'endpoint', label: 'Endpoint' },
  { field: 'rawModel', label: 'Model' },
  { field: 'provider', label: 'Provider' },
  { field: 'requests', label: 'Requests', align: 'right' },
  { field: 'lastUsed', label: 'Last Used', align: 'right' },
]

export const TABLE_OPTIONS = [
  { value: 'model', label: 'Usage by Model' },
  { value: 'account', label: 'Usage by Account' },
  { value: 'apiKey', label: 'Usage by API Key' },
  { value: 'endpoint', label: 'Usage by Endpoint' },
] as const

export type TableView = (typeof TABLE_OPTIONS)[number]['value']
export type ViewMode = 'costs' | 'tokens'

/** Segmented control matching KineRouter's period/table switchers. */
export function Segmented<T extends string>({
  options, value, onChange, disabled, className, size = 'md',
}: {
  options: readonly { value: T; label: string }[]
  value: T
  onChange: (v: T) => void
  disabled?: boolean
  className?: string
  size?: 'md' | 'sm'
}) {
  return (
    <div
      className={`grid items-center gap-1 rounded-lg border border-border bg-bg-alt p-1 ${className ?? ''}`}
      style={{ gridTemplateColumns: `repeat(${options.length}, minmax(0, 1fr))` }}
    >
      {options.map((o) => (
        <button
          key={o.value}
          onClick={() => onChange(o.value)}
          disabled={disabled}
          className={`rounded-md font-medium transition-colors disabled:opacity-60 ${
            size === 'sm' ? 'px-2.5 py-0.5 text-xs' : 'px-3 py-1 text-sm'
          } ${
            value === o.value
              ? 'bg-primary text-primary-foreground shadow-sm'
              : 'text-text-muted hover:bg-surface-2 hover:text-text-main'
          }`}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}
