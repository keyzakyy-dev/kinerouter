/**
 * Quota-tracker helpers, ported from
 * src/app/(dashboard)/dashboard/usage/components/ProviderLimits/utils.js.
 *
 * Providers report quota in ~20 different shapes; `parseQuotaData` flattens all
 * of them into the row the table renders. Where a provider's payload is already
 * row-shaped the generic branch just forwards it.
 *
 * ponytail: rows are not re-ordered by the provider's model catalogue order
 * (KineRouter sorts with getModelsByProviderId). The Go server builds `quotas` as
 * a JSON object, so rows arrive in key order; add the catalogue sort when the
 * registry order is available on this side.
 */

export const REFRESH_INTERVAL_MS = 60_000
export const CLAUDE_REFRESH_INTERVAL_MS = 600_000
export const DEPLETED_QUOTA_THRESHOLD = 5
export const AUTO_REFRESH_STORAGE_KEY = 'quotaAutoRefresh'
export const CONNECTIONS_PAGE_SIZE = 20
export const ACCOUNT_PAGE_SIZE_OPTIONS = [10, 20, 50, 100]
export const ACCOUNT_PAGE_SIZE_MAX = 500
export const QUOTA_CACHE_KEY = 'quotaCacheData'

export const ACCOUNT_FILTER_OPTIONS = [
  { value: 'all', label: 'All accounts' },
  { value: 'active', label: 'Active' },
  { value: 'inactive', label: 'Turned off' },
]

export const QUOTA_SORT_OPTIONS = [
  { value: 'default', label: 'Default quota order' },
  { value: 'remaining-asc', label: '% quota: low to high' },
  { value: 'remaining-desc', label: '% quota: high to low' },
]

export type Psd = Record<string, unknown>

export type Conn = {
  id: string
  provider: string
  authType?: string
  name?: string
  email?: string
  displayName?: string
  priority?: number
  isActive?: boolean
  testStatus?: string
  providerSpecificData?: Psd
}

export type QuotaRow = {
  name: string
  modelKey?: string
  quotaType?: string
  used: number
  total: number
  remaining?: number
  remainingPercentage?: number
  percentage?: number
  colorPercentage?: number
  percentageIsUsed?: boolean
  resetAt?: string | null
  recurring?: boolean
  neverExpires?: boolean
  detailUrl?: string | null
  emptyMessage?: string | null
  unlimited?: boolean
  isCreditBalance?: boolean
  currency?: string
  unit?: string
  message?: string
}

export type QuotaEntry = {
  quotas: QuotaRow[]
  plan?: string | null
  message?: string | null
  raw?: Record<string, unknown> | null
}

export type Pagination = { page: number; pageSize: number; total: number; totalPages: number }
export type Totals = { eligibleConnections: number; providerFilteredConnections: number }

export function getConnectionLabel(c: Conn): string | null {
  return c.name?.trim() || c.email?.trim() || c.displayName?.trim() || null
}

export function getConnectionQuotaRemaining(c: Conn, data: Record<string, QuotaEntry>): number {
  const q = data[c.id]?.quotas?.[0]
  if (q && typeof q.remaining === 'number') return q.remaining
  return Number.POSITIVE_INFINITY
}

function groupByProviderStable(connections: Conn[]): Conn[] {
  const seen = new Map<string, Conn[]>()
  for (const c of connections) {
    const key = c.provider || ''
    if (!seen.has(key)) seen.set(key, [])
    seen.get(key)!.push(c)
  }
  return Array.from(seen.values()).flat()
}

export function sortVisibleConnections(
  connections: Conn[],
  data: Record<string, QuotaEntry>,
  expiringFirst: boolean,
  providerFilter: string,
  sortMode: string,
): Conn[] {
  if (providerFilter === 'codex' && sortMode !== 'default') {
    return [...connections].sort((a, b) => {
      const ra = getConnectionQuotaRemaining(a, data)
      const rb = getConnectionQuotaRemaining(b, data)
      const diff = sortMode === 'remaining-asc' ? ra - rb : rb - ra
      if (diff !== 0) return diff
      return (getConnectionLabel(a) || '').localeCompare(getConnectionLabel(b) || '')
    })
  }

  if (!expiringFirst) return groupByProviderStable(connections)

  const earliestReset = (c: Conn) => {
    const times = (data[c.id]?.quotas || [])
      .map((q) => (q.resetAt ? new Date(q.resetAt).getTime() : Number.POSITIVE_INFINITY))
      .filter((t) => Number.isFinite(t))
    return times.length ? Math.min(...times) : Number.POSITIVE_INFINITY
  }

  return [...connections].sort((a, b) => {
    const diff = earliestReset(a) - earliestReset(b)
    if (diff !== 0) return diff
    return (
      (a.provider || '').localeCompare(b.provider || '') ||
      (getConnectionLabel(a) || '').localeCompare(getConnectionLabel(b) || '')
    )
  })
}

export function buildLoadingState(connections: Conn[]): Record<string, boolean> {
  const next: Record<string, boolean> = {}
  connections.forEach((c) => {
    next[c.id] = true
  })
  return next
}

export function filterQuotaStateByConnections<T>(state: Record<string, T>, connections: Conn[]) {
  const ids = new Set(connections.map((c) => c.id))
  return Object.fromEntries(Object.entries(state).filter(([id]) => ids.has(id)))
}

function getConnectionsPageRange(p: Pagination) {
  if (!p.total) return { start: 0, end: 0 }
  return {
    start: (p.page - 1) * p.pageSize + 1,
    end: Math.min(p.page * p.pageSize, p.total),
  }
}

export function getConnectionsEmptyMessage(
  totals: Totals,
  providerFilter: string,
  accountFilter: string,
) {
  if (!totals.eligibleConnections) {
    return {
      icon: 'cloud_off',
      title: 'No Providers Connected',
      description:
        'Connect to providers with OAuth to track your API quota limits and usage.',
    }
  }
  if (!totals.providerFilteredConnections) {
    return {
      icon: 'filter_alt_off',
      title: 'No Accounts Match Current Filters',
      description:
        providerFilter === 'all'
          ? 'Try changing the account status filter to see more quota trackers.'
          : `No ${accountFilter === 'inactive' ? 'turned off' : accountFilter === 'active' ? 'active' : 'matching'} accounts found for ${providerFilter}.`,
    }
  }
  return {
    icon: 'filter_alt_off',
    title: 'No Accounts On This Page',
    description: 'Try moving to another page or refreshing the current filters.',
  }
}

export function getPageSizeLabel(pageSize: number, isCustom: boolean): string {
  return isCustom ? `Custom: ${pageSize} / page` : `${pageSize} / page`
}

export function getConnectionsPaginationSummary(p: Pagination): string {
  const { start, end } = getConnectionsPageRange(p)
  return `Showing ${start}-${end} of ${p.total}`
}

export function getSafePagination(p: Pagination | null | undefined, fallbackPageSize: number): Pagination {
  return p || { page: 1, pageSize: fallbackPageSize, total: 0, totalPages: 1 }
}

export function getSafeTotals(t: Totals | null | undefined, fallbackTotal = 0): Totals {
  return t || { eligibleConnections: fallbackTotal, providerFilteredConnections: fallbackTotal }
}

export function getQuotaCache(): Record<string, QuotaEntry & { cachedAt?: string }> {
  if (typeof window === 'undefined') return {}
  try {
    const raw = window.localStorage.getItem(QUOTA_CACHE_KEY)
    return raw ? JSON.parse(raw) : {}
  } catch {
    return {}
  }
}

export function setQuotaCache(connectionId: string, entry: QuotaEntry) {
  if (typeof window === 'undefined') return
  try {
    const cache = getQuotaCache()
    cache[connectionId] = { ...entry, cachedAt: new Date().toISOString() }
    window.localStorage.setItem(QUOTA_CACHE_KEY, JSON.stringify(cache))
  } catch {
    // storage full / disabled — the cache is only a nicety
  }
}

/** "2d 5h 30m" / "4h 40m" / "15m" until `date`; "-" when past or invalid. */
export function formatResetTime(date: string | Date | null | undefined): string {
  if (!date) return '-'
  const resetDate = typeof date === 'string' ? new Date(date) : date
  const diffMs = resetDate.getTime() - Date.now()
  if (!Number.isFinite(diffMs) || diffMs <= 0) return '-'
  const totalMinutes = Math.ceil(diffMs / 60_000)
  if (totalMinutes < 60) return `${totalMinutes}m`
  const totalHours = Math.floor(totalMinutes / 60)
  const remainingMinutes = totalMinutes % 60
  if (totalHours < 24) return `${totalHours}h ${remainingMinutes}m`
  const days = Math.floor(totalHours / 24)
  return `${days}d ${totalHours % 24}h ${remainingMinutes}m`
}

/** Used/total → remaining percentage. Mirrors KineRouter's inverted math. */
export function calculatePercentage(used: number, total: number): number {
  if (!total || total === 0) return 0
  if (!used || used < 0) return 100
  if (used >= total) return 0
  return Math.round(((total - used) / total) * 100)
}

export function getRemainingPercentage(q: QuotaRow): number {
  if (q.percentage !== undefined) {
    const value = q.percentageIsUsed === true ? 100 - q.percentage : q.percentage
    return Math.max(0, Math.round(value))
  }
  if (q.remaining !== undefined) return Math.max(0, Math.round(q.remaining))
  if (q.remainingPercentage !== undefined) return Math.round(q.remainingPercentage)
  return calculatePercentage(q.used, q.total)
}

export function getQuotaVisibilityKey(q: QuotaRow | null | undefined): string {
  if (!q || typeof q !== 'object') return ''
  return String(q.modelKey || q.name || '').trim()
}

function trimHiddenQuotaKeys(hidden: string[] = [], quotas: QuotaRow[] = []): string[] {
  if (!hidden.length) return []
  const valid = new Set(quotas.map(getQuotaVisibilityKey).filter(Boolean))
  return [...new Set(hidden.map((k) => String(k).trim()).filter((k) => valid.has(k)))]
}

function hiddenSet(provider: string, visibility: Record<string, { hidden?: string[] }>, quotas: QuotaRow[]) {
  const hidden = visibility?.[provider]?.hidden
  if (!Array.isArray(hidden) || !hidden.length) return new Set<string>()
  const trimmed = quotas.length ? trimHiddenQuotaKeys(hidden, quotas) : hidden
  return new Set(trimmed.map(String))
}

export function filterQuotasByVisibility(
  provider: string,
  quotas: QuotaRow[] = [],
  visibility: Record<string, { hidden?: string[] }> = {},
): QuotaRow[] {
  if (!quotas.length) return []
  const hidden = hiddenSet(provider, visibility, quotas)
  if (!hidden.size) return quotas
  return quotas.filter((q) => !hidden.has(getQuotaVisibilityKey(q)))
}

export function getHiddenQuotaRows(
  provider: string,
  quotas: QuotaRow[] = [],
  visibility: Record<string, { hidden?: string[] }> = {},
): QuotaRow[] {
  if (!quotas.length) return []
  const hidden = hiddenSet(provider, visibility, quotas)
  if (!hidden.size) return []
  return quotas.filter((q) => hidden.has(getQuotaVisibilityKey(q)))
}

// ---------------------------------------------------------------- parsing

type RawQuota = Record<string, any>

function num(v: unknown, fallback = 0): number {
  const n = typeof v === 'number' ? v : Number(v)
  return Number.isFinite(n) ? n : fallback
}

/** Row shape every provider branch builds.
 * `keepRemaining` is false where the API returns an absolute credit count in
 * `remaining` (Qoder, Grok Build) — the table reads it as a percentage. */
function row(
  name: string,
  quota: RawQuota,
  extra: Partial<QuotaRow> = {},
  keepRemaining = false,
): QuotaRow {
  const used = num(quota.used)
  const total = num(quota.total)
  const out: QuotaRow = {
    name,
    used,
    total,
    resetAt: quota.resetAt || null,
  }
  if (keepRemaining && quota.remaining !== undefined) out.remaining = num(quota.remaining)
  if (quota.remainingPercentage !== undefined) out.remainingPercentage = num(quota.remainingPercentage)
  return Object.assign(out, extra)
}

function entries(quotas: unknown): [string, RawQuota][] {
  if (!quotas || typeof quotas !== 'object') return []
  return Object.entries(quotas as Record<string, any>).filter(
    (e): e is [string, RawQuota] => !!e[1] && typeof e[1] === 'object',
  )
}

const CODEX_NAMES: Record<string, string> = {
  spark_session: 'Spark (5h)',
  spark_weekly: 'Spark (Weekly)',
  session: '5h',
  weekly: 'Weekly',
  review_session: 'Review (5h)',
  review_weekly: 'Review (Weekly)',
}

function parseAntigravity(data: RawQuota): QuotaRow[] {
  const all = entries(data.quotas)
  const weeklyKeys = new Set(['gemini_weekly', 'claude_gpt_weekly', 'gemini_argon_weekly'])
  const sessionKeys = new Set(['gemini_session', 'claude_gpt_session', 'gemini_argon_session'])
  const summaryKeys = new Set([...weeklyKeys, ...sessionKeys])

  const argonModels = all.filter(([k]) => k.startsWith('gemini-') && k.includes('argon'))
  const geminiModels = all.filter(
    ([k]) => k.startsWith('gemini-') && !k.includes('image') && !k.includes('argon'),
  )
  const claudeModels = all.filter(([k]) => k.startsWith('claude-'))
  const imageModels = all.filter(([k]) => k.includes('image'))
  const summary = all.filter(([k]) => summaryKeys.has(k))
  const otherModels = all.filter(
    ([k]) => !k.startsWith('gemini-') && !k.startsWith('claude-') && !k.includes('image') && !summaryKeys.has(k),
  )

  const has = (k: string) => Boolean(data.quotas[k])
  const hasGeminiWeekly = has('gemini_weekly')
  const hasGeminiSession = has('gemini_session')
  const hasArgonWeekly = has('gemini_argon_weekly')
  const hasArgonSession = has('gemini_argon_session')
  const hasClaudeWeekly = has('claude_gpt_weekly')
  const hasClaudeSession = has('claude_gpt_session')

  const out: QuotaRow[] = []
  const pushSummary = (key: string) => {
    for (const [modelKey, quota] of summary) {
      if (modelKey !== key) continue
      out.push(row(quota.displayName || modelKey, quota, { modelKey }))
    }
  }
  const pushWorst = (list: [string, RawQuota][], name: string, modelKey: string) => {
    const rep = list.reduce((min, cur) =>
      (cur[1].remainingPercentage ?? 100) < (min[1].remainingPercentage ?? 100) ? cur : min,
    )[1]
    out.push(row(name, rep, { modelKey }))
  }

  if (hasGeminiSession) {
    pushSummary('gemini_session')
  } else if (geminiModels.length) {
    const rep = geminiModels.reduce((min, cur) =>
      (cur[1].remainingPercentage ?? 100) < (min[1].remainingPercentage ?? 100) ? cur : min,
    )[1]
    // Only a separate row when it is not the weekly window reported twice.
    const dup =
      hasGeminiWeekly &&
      rep.resetAt === data.quotas.gemini_weekly?.resetAt &&
      (rep.remainingPercentage ?? 0) === 0
    if (!dup) pushWorst(geminiModels, 'Gemini (Flash / Pro)', 'gemini')
  }
  if (hasGeminiWeekly) pushSummary('gemini_weekly')

  if (hasArgonSession) {
    pushSummary('gemini_argon_session')
  } else if (!hasArgonWeekly && argonModels.length) {
    const rep = argonModels.reduce((min, cur) =>
      (cur[1].remainingPercentage ?? 100) < (min[1].remainingPercentage ?? 100) ? cur : min,
    )[1]
    const sharesMain =
      (hasGeminiSession &&
        rep.resetAt === data.quotas.gemini_session?.resetAt &&
        rep.remainingPercentage === data.quotas.gemini_session?.remainingPercentage) ||
      (hasGeminiWeekly &&
        rep.resetAt === data.quotas.gemini_weekly?.resetAt &&
        rep.remainingPercentage === data.quotas.gemini_weekly?.remainingPercentage)
    if (!sharesMain) pushWorst(argonModels, 'Gemini 4 Argon', 'gemini_argon')
  }
  if (hasArgonWeekly) pushSummary('gemini_argon_weekly')

  if (hasClaudeSession) {
    pushSummary('claude_gpt_session')
  } else if (claudeModels.length) {
    const rep = claudeModels.reduce((min, cur) =>
      (cur[1].remainingPercentage ?? 100) < (min[1].remainingPercentage ?? 100) ? cur : min,
    )[1]
    const dup =
      hasClaudeWeekly &&
      rep.resetAt === data.quotas.claude_gpt_weekly?.resetAt &&
      (rep.remainingPercentage ?? 0) === 0
    if (!dup) pushWorst(claudeModels, 'Claude (Sonnet / Opus)', 'claude')
  }
  if (hasClaudeWeekly) pushSummary('claude_gpt_weekly')

  for (const [modelKey, quota] of imageModels) {
    out.push(row(quota.displayName || modelKey, quota, { modelKey }))
  }
  // GPT-OSS is already inside the Claude & GPT family when a summary exists.
  if (!hasClaudeWeekly && !hasClaudeSession) {
    for (const [modelKey, quota] of otherModels) {
      out.push(row(quota.displayName || modelKey, quota, { modelKey }))
    }
  }
  return out
}

const CLAUDE_QUOTA_ORDER: Record<string, number> = {
  'session (5h)': 0,
  'weekly (7d)': 1,
  'weekly fable (7d)': 2,
  'weekly opus (7d)': 3,
  'weekly sonnet (7d)': 4,
}

/** Normalises a provider's usage payload into table rows. */
export function parseQuotaData(provider: string, data: RawQuota | null | undefined): QuotaRow[] {
  if (!data || typeof data !== 'object') return []
  const id = String(provider || '').toLowerCase()
  const out: QuotaRow[] = []

  switch (id) {
    case 'antigravity':
      out.push(...parseAntigravity(data))
      break

    case 'codex':
      for (const [quotaType, q] of entries(data.quotas)) {
        out.push(
          row(
            CODEX_NAMES[quotaType] || quotaType,
            q,
            { quotaType },
            q.remaining !== undefined,
          ),
        )
      }
      break

    case 'qoder':
    case 'qoder-cn':
      for (const [quotaType, q] of entries(data.quotas)) {
        // A 0/0 bucket is a placeholder, not a grant.
        if (num(q.total) === 0) continue
        const name =
          quotaType === 'user'
            ? 'Personal'
            : quotaType === 'organization'
              ? 'Organization'
              : quotaType === 'addOn'
                ? 'Add-on'
                : quotaType
        out.push(
          row(name, q, {
            unit: q.unit,
            neverExpires: q.neverExpires === true,
            detailUrl: q.detailUrl || null,
          }),
        )
      }
      break

    case 'claude': {
      if (data.message) {
        out.push({ name: 'error', used: 0, total: 0, resetAt: null, message: String(data.message) })
        break
      }
      for (const [name, q] of entries(data.quotas)) {
        const r = row(name, q, {}, true)
        r.remaining = q.remaining !== undefined ? num(q.remaining) : Math.max(0, (num(q.total) || 100) - num(q.used))
        r.remainingPercentage =
          q.remainingPercentage !== undefined
            ? num(q.remainingPercentage)
            : calculatePercentage(num(q.used), num(q.total))
        out.push(r)
      }
      break
    }

    case 'codebuddy-cn':
    case 'codebuddy-intl':
      // Bonus packs are one-shot: resetAt is a hard expiry, `recurring:false`.
      for (const [name, q] of entries(data.quotas)) {
        out.push(row(name, q, { recurring: q.recurring !== false }))
      }
      break

    case 'minimax-code':
    case 'minimax-code-global':
      for (const [name, q] of entries(data.quotas)) {
        out.push(
          row(name, q, {
            message: q.message,
            isCreditBalance: q.isCreditBalance,
            currency: q.currency,
          }),
        )
      }
      break

    case 'deepseek':
      for (const [name, q] of entries(data.quotas)) {
        const paren = name.includes('(') ? name.slice(name.indexOf('(') + 1, name.indexOf(')')) : 'USD'
        out.push(
          row(name, q, {
            isCreditBalance: q.isCreditBalance ?? true,
            currency: q.currency || paren,
          }),
        )
      }
      break

    case 'ollama':
    case 'zed':
    case 'vercel-ai-gateway':
      for (const [name, q] of entries(data.quotas)) {
        out.push(
          row(name, q, {
            isCreditBalance: q.isCreditBalance,
            currency: q.currency,
            unit: q.unit,
            unlimited: q.unlimited,
          }),
        )
      }
      break

    case 'kiro':
      for (const [quotaType, q] of entries(data.quotas)) out.push(row(quotaType, q))
      break

    default:
      // Everything else already reports row-shaped quotas.
      for (const [name, q] of entries(data.quotas)) {
        out.push(
          row(q.name || name, q, {
            percentage: q.percentage,
            colorPercentage: q.colorPercentage,
            percentageIsUsed: q.percentageIsUsed === true,
            remainingPercentage: q.remainingPercentage,
            unlimited: q.unlimited,
            isCreditBalance: q.isCreditBalance,
            currency: q.currency,
            unit: q.unit,
            emptyMessage: q.emptyMessage || null,
          }),
        )
      }
  }

  if (id === 'claude') {
    out.sort(
      (a, b) =>
        (CLAUDE_QUOTA_ORDER[a.name] ?? 99) - (CLAUDE_QUOTA_ORDER[b.name] ?? 99),
    )
  }
  return out
}
