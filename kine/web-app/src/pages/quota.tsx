import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Icon } from '@/components/icon'
import { AutoPingScheduleModal, EditConnectionModal } from '@/components/connection-modals'
import { ProviderIcon } from '@/components/provider-icon'
import { QuotaTable } from '@/components/quota-table'
import { ConfirmModal, Modal } from '@/components/modal'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { errorMessage } from '@/lib/errors'
import {
  ACCOUNT_FILTER_OPTIONS,
  ACCOUNT_PAGE_SIZE_MAX,
  ACCOUNT_PAGE_SIZE_OPTIONS,
  AUTO_REFRESH_STORAGE_KEY,
  CLAUDE_REFRESH_INTERVAL_MS,
  CONNECTIONS_PAGE_SIZE,
  DEPLETED_QUOTA_THRESHOLD,
  QUOTA_SORT_OPTIONS,
  REFRESH_INTERVAL_MS,
  buildLoadingState,
  calculatePercentage,
  filterQuotaStateByConnections,
  filterQuotasByVisibility,
  getConnectionLabel,
  getConnectionsEmptyMessage,
  getConnectionsPaginationSummary,
  getHiddenQuotaRows,
  getPageSizeLabel,
  getQuotaVisibilityKey,
  getSafePagination,
  getSafeTotals,
  parseQuotaData,
  setQuotaCache,
  sortVisibleConnections,
  type Conn,
  type Pagination,
  type QuotaEntry,
  type QuotaRow,
  type Totals,
} from '@/lib/quota'
import { useCopyToClipboard } from '@/lib/use-copy-to-clipboard'
import { getScheduleSlotLabels } from '@/lib/quota-ping-schedule'
import { cn } from '@/lib/utils'

/**
 * Quota Tracker, ported from
 * src/app/(dashboard)/dashboard/usage/components/ProviderLimits/index.js.
 *
 * Every account card reads `/api/usage/{connectionId}`, which the Go server
 * answers with the same provider-shaped payload KineRouter does, so the parsers in
 * lib/quota.ts stay identical.
 *
 * ponytail: the auto-ping scheduler itself (quotaAutoPing.js) is not ported;
 * this page only writes its settings. Pings happen when the matching server
 * loop lands. The edit modal also drops the Azure/Cloudflare/AWS blocks because
 * the Go update allowlist would discard those fields.
 */

const KIRO_METHOD_LABELS: Record<string, string> = {
  'builder-id': 'AWS Builder ID',
  idc: 'IAM Identity Center',
  google: 'Google',
  github: 'GitHub',
  imported: 'Imported Token',
  api_key: 'API Key',
}

const AUTO_PING_SETTINGS_KEYS: Record<string, string> = {
  claude: 'claudeAutoPing',
  codex: 'codexAutoPing',
}

const CLAUDE_RESET_LIMIT_NAMES: Record<string, string> = {
  five_hour: 'session',
  seven_day: 'weekly',
  seven_day_overage_included: 'weekly',
  seven_day_opus: 'Opus weekly',
  seven_day_sonnet: 'Sonnet weekly',
}

function kiroMethodLabel(conn: Conn): string {
  const m = (conn.providerSpecificData?.authMethod as string) || ''
  if (m && KIRO_METHOD_LABELS[m]) return KIRO_METHOD_LABELS[m]
  return conn.authType === 'apikey' ? 'API Key' : 'OAuth'
}

function kiroRegion(conn: Conn): string {
  const region = conn.providerSpecificData?.region as string
  if (region) return region
  const arn = conn.providerSpecificData?.profileArn
  const seg = typeof arn === 'string' ? arn.split(':')[3] : ''
  return seg || ''
}

function secondaryLabel(c: Conn): string | null {
  const name = c.name?.trim()
  const email = c.email?.trim()
  if (name && email && name !== email) return email
  const display = c.displayName?.trim()
  if (name && display && name !== display) return display
  return null
}

function codexResetCreditCount(entry?: QuotaEntry): number {
  const value = (entry?.raw as any)?.resetCredits?.availableCount
  const count = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(count) ? Math.max(0, count) : 0
}

function formatClaudeResetClears(clears: string[] | undefined): string {
  const names = [
    ...new Set((clears || []).map((c) => CLAUDE_RESET_LIMIT_NAMES[c]).filter(Boolean)),
  ]
  return names.length ? `${names.join(' + ')} limits` : 'limits'
}

function claudeGrantStatus(grant: any): string {
  if (grant.resetsLeft <= 0) return 'used'
  if (grant.paused) return 'paused'
  if (grant.endsAt && new Date(grant.endsAt).getTime() <= Date.now()) return 'expired'
  if (grant.usableNow) return 'usable now'
  if (grant.startsAt && new Date(grant.startsAt).getTime() > Date.now()) return 'not started'
  return grant.useRequiresLimit ? 'at limit only' : 'unavailable'
}

function formatCreditDate(value: string | null | undefined): string {
  if (!value) return 'N/A'
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return 'N/A'
  return date.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  })
}

function formatTimeRemaining(value: string | null | undefined): string {
  if (!value) return 'N/A'
  const diffMs = new Date(value).getTime() - Date.now()
  if (!Number.isFinite(diffMs)) return 'N/A'
  if (diffMs <= 0) return 'Expired'
  const totalHours = Math.ceil(diffMs / 3_600_000)
  const days = Math.floor(totalHours / 24)
  const hours = totalHours % 24
  return days > 0 ? `${days}d ${hours}h` : `${hours}h`
}

const selectClass =
  'h-8 cursor-pointer rounded-lg border border-black/10 bg-black/[0.02] px-2 text-xs text-text-main outline-none transition-colors hover:bg-black/5'
const iconButtonClass =
  'flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-lg border border-black/10 text-text-main transition-colors hover:bg-black/5 disabled:cursor-not-allowed disabled:opacity-50'

export default function QuotaPage() {
  const { copied, copy } = useCopyToClipboard()
  const [searchParams, setSearchParams] = useSearchParams()

  const [connections, setConnections] = useState<Conn[]>([])
  const [quotaData, setQuotaData] = useState<Record<string, QuotaEntry>>({})
  const [loading, setLoading] = useState<Record<string, boolean>>({})
  const [errors, setErrors] = useState<Record<string, string | null>>({})
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [hasHydrated, setHasHydrated] = useState(false)
  const [autoPingConfigs, setAutoPingConfigs] = useState<Record<string, any>>({ claude: {}, codex: {} })
  const [scheduleConnection, setScheduleConnection] = useState<Conn | null>(null)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)
  const [refreshingAll, setRefreshingAll] = useState(false)
  const [countdown, setCountdown] = useState(60)
  const [connectionsLoading, setConnectionsLoading] = useState(true)

  const [deletingId, setDeletingId] = useState<string | null>(null)
  const [togglingId, setTogglingId] = useState<string | null>(null)
  const [resettingLimitId, setResettingLimitId] = useState<string | null>(null)
  const [resetConfirmState, setResetConfirmState] = useState<{ connection: Conn; resetCreditCount: number } | null>(null)
  const [resetCreditsState, setResetCreditsState] = useState<{
    connection: Conn
    loading: boolean
    error: string | null
    data: any
  } | null>(null)
  const [showEditModal, setShowEditModal] = useState(false)
  const [selectedConnection, setSelectedConnection] = useState<Conn | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Conn | null>(null)

  const [providerFilter, setProviderFilter] = useState(() => searchParams.get('provider') || 'all')
  const [providerOptions, setProviderOptions] = useState<string[]>([])
  const [providerNames, setProviderNames] = useState<Record<string, string>>({})
  const [accountFilter, setAccountFilter] = useState('all')
  const [quotaSortMode, setQuotaSortMode] = useState('default')
  const [quotaVisibility, setQuotaVisibility] = useState<Record<string, { hidden?: string[] }>>({})
  const [expiringFirst, setExpiringFirst] = useState(false)
  const [providerMenuOpen, setProviderMenuOpen] = useState(false)
  const [bulkToggling, setBulkToggling] = useState(false)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(CONNECTIONS_PAGE_SIZE)
  const [customPageSizeInput, setCustomPageSizeInput] = useState(String(CONNECTIONS_PAGE_SIZE))
  const [pagination, setPagination] = useState<Pagination>({
    page: 1,
    pageSize: CONNECTIONS_PAGE_SIZE,
    total: 0,
    totalPages: 1,
  })
  const [totals, setTotals] = useState<Totals>({ eligibleConnections: 0, providerFilteredConnections: 0 })

  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const countdownRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const tickCountRef = useRef(0)

  // Keep ?provider= in the URL so the view can be bookmarked.
  const changeProviderFilter = useCallback(
    (value: string) => {
      setProviderFilter(value)
      const next = new URLSearchParams(searchParams)
      if (value === 'all') next.delete('provider')
      else next.set('provider', value)
      setSearchParams(next, { replace: true })
    },
    [searchParams, setSearchParams],
  )

  const fetchConnections = useCallback(
    async (targetPage = page) => {
      try {
        const params = new URLSearchParams({
          page: String(targetPage),
          pageSize: String(pageSize),
          accountStatus: accountFilter,
          sort: 'priority',
        })
        if (providerFilter !== 'all') params.set('provider', providerFilter)

        const res = await fetch(`/api/providers/client?${params.toString()}`)
        if (!res.ok) throw new Error('Failed to fetch connections')
        const data = await res.json()
        const list: Conn[] = data.connections || []
        setConnections(list)
        setProviderOptions(data.providerOptions || [])
        const next = getSafePagination(data.pagination, pageSize)
        setPagination(next)
        setTotals(getSafeTotals(data.totals, list.length))
        setPage(data.pagination?.page || targetPage)
        return list
      } catch {
        setConnections([])
        setProviderOptions([])
        setPagination({ page: 1, pageSize, total: 0, totalPages: 1 })
        setTotals({ eligibleConnections: 0, providerFilteredConnections: 0 })
        return [] as Conn[]
      }
    },
    [accountFilter, page, pageSize, providerFilter],
  )

  const fetchQuota = useCallback(
    async (connectionId: string, provider: string, opts: { force?: boolean } = {}) => {
      setLoading((prev) => ({ ...prev, [connectionId]: true }))
      setErrors((prev) => ({ ...prev, [connectionId]: null }))
      try {
        const res = await fetch(`/api/usage/${connectionId}${opts.force ? '?force=1' : ''}`)
        if (!res.ok) {
          const data = await res.json().catch(() => ({}))
          const msg = errorMessage(data, res.statusText)
          if (res.status === 404) return
          if (res.status === 401) {
            const entry: QuotaEntry = { quotas: [], message: msg }
            setQuotaData((prev) => ({ ...prev, [connectionId]: entry }))
            setQuotaCache(connectionId, entry)
            return
          }
          throw new Error(`HTTP ${res.status}: ${msg}`)
        }
        const data = await res.json()
        const entry: QuotaEntry = {
          quotas: parseQuotaData(provider, data),
          plan: data.plan || null,
          message: data.message || null,
          raw: data,
        }
        setQuotaData((prev) => ({ ...prev, [connectionId]: entry }))
        setQuotaCache(connectionId, entry)
      } catch (e) {
        setErrors((prev) => ({
          ...prev,
          [connectionId]: (e as Error).message || 'Failed to fetch quota',
        }))
      } finally {
        setLoading((prev) => ({ ...prev, [connectionId]: false }))
      }
    },
    [],
  )

  const refreshProvider = useCallback(
    async (connectionId: string, provider: string) => {
      await fetchQuota(connectionId, provider, { force: true })
      setLastUpdated(new Date())
    },
    [fetchQuota],
  )

  const refreshAll = useCallback(
    async (force = false) => {
      if (refreshingAll) return
      setRefreshingAll(true)
      setCountdown(60)
      const tick = (tickCountRef.current += 1)
      const claudeEvery = Math.round(CLAUDE_REFRESH_INTERVAL_MS / REFRESH_INTERVAL_MS)
      const shouldFetch = (c: Conn) => force || c.provider !== 'claude' || tick % claudeEvery === 0

      try {
        const visible = await fetchConnections(page)
        setLoading(buildLoadingState(visible))
        setErrors((prev) => filterQuotaStateByConnections(prev, visible))
        setQuotaData((prev) => filterQuotaStateByConnections(prev, visible))
        await Promise.all(visible.filter(shouldFetch).map((c) => fetchQuota(c.id, c.provider)))
        setLastUpdated(new Date())
      } finally {
        setRefreshingAll(false)
      }
    },
    [refreshingAll, fetchConnections, fetchQuota, page],
  )

  useEffect(() => {
    let cancelled = false
    const init = async () => {
      setConnectionsLoading(true)
      const visible = await fetchConnections(page)
      if (cancelled) return
      setConnectionsLoading(false)
      setLoading(buildLoadingState(visible))
      setErrors((prev) => filterQuotaStateByConnections(prev, visible))
      setQuotaData((prev) => filterQuotaStateByConnections(prev, visible))
      await Promise.all(visible.map((c) => fetchQuota(c.id, c.provider)))
      if (!cancelled) setLastUpdated(new Date())
    }
    init()
    return () => {
      cancelled = true
    }
  }, [fetchConnections, fetchQuota, page])

  useEffect(() => {
    const stored = window.localStorage.getItem(AUTO_REFRESH_STORAGE_KEY)
    setAutoRefresh(stored === null ? true : stored === 'true')
    setHasHydrated(true)
  }, [])

  useEffect(() => {
    if (!hasHydrated) return
    window.localStorage.setItem(AUTO_REFRESH_STORAGE_KEY, String(autoRefresh))
  }, [autoRefresh, hasHydrated])

  // Provider display names for the filter menu and card headers.
  useEffect(() => {
    fetch('/api/providers')
      .then((r) => (r.ok ? r.json() : { providers: [] }))
      .then((d) => {
        const map: Record<string, string> = {}
        for (const p of d.providers || []) {
          if (p?.id) map[p.id] = p.display?.name || p.id
        }
        setProviderNames(map)
      })
      .catch(() => {})
  }, [])

  useEffect(() => {
    fetch('/api/settings', { cache: 'no-store' })
      .then((r) => (r.ok ? r.json() : {}))
      .then((s: any) => {
        setAutoPingConfigs({ claude: s?.claudeAutoPing || {}, codex: s?.codexAutoPing || {} })
        setQuotaVisibility(s?.quotaVisibility || {})
      })
      .catch(() => {})
  }, [])

  // Auto-refresh + countdown.
  useEffect(() => {
    const clear = () => {
      if (intervalRef.current) clearInterval(intervalRef.current)
      if (countdownRef.current) clearInterval(countdownRef.current)
      intervalRef.current = null
      countdownRef.current = null
    }
    if (!hasHydrated || !autoRefresh) {
      clear()
      return
    }
    intervalRef.current = setInterval(() => refreshAll(), REFRESH_INTERVAL_MS)
    countdownRef.current = setInterval(() => setCountdown((p) => (p <= 1 ? 60 : p - 1)), 1000)
    return clear
  }, [autoRefresh, refreshAll, hasHydrated])

  // Pause while the tab is hidden.
  useEffect(() => {
    const onVisibility = () => {
      if (document.hidden) {
        if (intervalRef.current) clearInterval(intervalRef.current)
        if (countdownRef.current) clearInterval(countdownRef.current)
        intervalRef.current = null
        countdownRef.current = null
      } else if (autoRefresh && hasHydrated) {
        intervalRef.current = setInterval(() => refreshAll(), REFRESH_INTERVAL_MS)
        countdownRef.current = setInterval(() => setCountdown((p) => (p <= 1 ? 60 : p - 1)), 1000)
      }
    }
    document.addEventListener('visibilitychange', onVisibility)
    return () => document.removeEventListener('visibilitychange', onVisibility)
  }, [autoRefresh, refreshAll, hasHydrated])

  const providerLabel = (id: string) => providerNames[id] || id

  const sortedConnections = useMemo(
    () => sortVisibleConnections(connections, quotaData, expiringFirst, providerFilter, quotaSortMode),
    [connections, quotaData, expiringFirst, providerFilter, quotaSortMode],
  )

  const isConnectionDepleted = (conn: Conn) => {
    const quotas = quotaData[conn.id]?.quotas
    if (!quotas?.length) return false
    return quotas.some(
      (q) => !!q.total && q.total > 0 && calculatePercentage(q.used, q.total) <= DEPLETED_QUOTA_THRESHOLD,
    )
  }

  const bulkSetActive = useCallback(
    async (ids: string[], isActive: boolean) => {
      if (!ids.length || bulkToggling) return
      setBulkToggling(true)
      try {
        await Promise.all(
          ids.map((id) =>
            fetch(`/api/connections/${id}`, {
              method: 'PUT',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ isActive }),
            }),
          ),
        )
        await fetchConnections(page)
      } finally {
        setBulkToggling(false)
      }
    },
    [bulkToggling, fetchConnections, page],
  )

  const handleToggleActive = async (conn: Conn, isActive: boolean) => {
    setTogglingId(conn.id)
    try {
      await fetch(`/api/connections/${conn.id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ isActive }),
      })
      await fetchConnections(page)
    } finally {
      setTogglingId(null)
    }
  }

  const handleDelete = async (conn: Conn) => {
    setDeletingId(conn.id)
    try {
      const res = await fetch(`/api/connections/${conn.id}`, { method: 'DELETE' })
      if (res.ok) {
        setQuotaData((prev) => {
          const next = { ...prev }
          delete next[conn.id]
          return next
        })
        await fetchConnections(page)
      }
    } finally {
      setDeletingId(null)
    }
  }

  const handleResetLimit = async (connectionId: string, provider: string) => {
    if ((provider !== 'codex' && provider !== 'claude') || resettingLimitId) return
    setResettingLimitId(connectionId)
    setErrors((prev) => ({ ...prev, [connectionId]: null }))
    try {
      const res =
        provider === 'claude'
          ? await fetch(`/api/usage/${connectionId}/claude-reset`, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ grantId: (quotaData[connectionId]?.raw as any)?.resetCredits?.nextGrantId }),
            })
          : await fetch(`/api/usage/${connectionId}/codex-reset-credits`, { method: 'POST' })
      const result = await res.json().catch(() => ({}))
      if (!res.ok) {
        throw new Error(
          result.message || result.code || errorMessage(result, 'Failed to reset limit'),
        )
      }
      await fetchQuota(connectionId, provider, { force: true })
      setLastUpdated(new Date())
    } catch (e) {
      setErrors((prev) => ({ ...prev, [connectionId]: (e as Error).message }))
    } finally {
      setResettingLimitId(null)
    }
  }

  const viewClaudeResets = (conn: Conn, resetCredits: any) => {
    setResetCreditsState({ connection: conn, loading: false, error: null, data: { kind: 'claude', ...resetCredits } })
  }

  const viewCodexResetCredits = async (conn: Conn) => {
    setResetCreditsState({ connection: conn, loading: true, error: null, data: null })
    try {
      const res = await fetch(`/api/usage/${conn.id}/codex-reset-credits`, { cache: 'no-store' })
      const result = await res.json().catch(() => ({}))
      if (!res.ok) throw new Error(result.message || errorMessage(result, 'Failed to load Codex reset credits'))
      const credits = Array.isArray(result.credits) ? [...result.credits] : []
      credits.sort((a: any, b: any) => {
        const at = a.expiresAt ? new Date(a.expiresAt).getTime() : Number.POSITIVE_INFINITY
        const bt = b.expiresAt ? new Date(b.expiresAt).getTime() : Number.POSITIVE_INFINITY
        return at - bt
      })
      setResetCreditsState({ connection: conn, loading: false, error: null, data: { ...result, credits } })
    } catch (e) {
      setResetCreditsState({
        connection: conn,
        loading: false,
        error: (e as Error).message || 'Failed to load Codex reset credits',
        data: null,
      })
    }
  }

  const updateQuotaVisibility = async (
    next: Record<string, { hidden?: string[] }>,
    previous: Record<string, { hidden?: string[] }>,
  ) => {
    setQuotaVisibility(next)
    try {
      const res = await fetch('/api/settings', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ quotaVisibility: next }),
      })
      if (!res.ok) throw new Error('Failed')
    } catch {
      setQuotaVisibility(previous)
    }
  }

  const toggleQuotaVisibility = (provider: string, rowToChange: QuotaRow, hide: boolean) => {
    const key = getQuotaVisibilityKey(rowToChange)
    if (!provider || !key) return
    const previous = quotaVisibility
    const hidden = new Set(previous[provider]?.hidden || [])
    if (hide) hidden.add(key)
    else hidden.delete(key)
    // Antigravity family rows hide/show their member models together.
    if (provider === 'antigravity') {
      if (key === 'gemini') {
        for (const k of hidden) if (k.startsWith('gemini-') && !k.includes('image')) hidden.delete(k)
      } else if (key === 'claude') {
        for (const k of hidden) if (k.startsWith('claude-')) hidden.delete(k)
      }
    }
    updateQuotaVisibility({ ...previous, [provider]: { ...previous[provider], hidden: [...hidden] } }, previous)
  }

  const handleSaveAutoPing = async (
    connectionId: string,
    provider: string,
    result: { reactive: boolean; schedule: Record<string, unknown> },
  ) => {
    const settingsKey = AUTO_PING_SETTINGS_KEYS[provider]
    if (!settingsKey) return
    const previous = autoPingConfigs
    const prevCfg = autoPingConfigs[provider] || {}
    const nextProviderCfg = {
      ...prevCfg,
      connections: { ...(prevCfg.connections || {}), [connectionId]: result.reactive },
      schedules: { ...(prevCfg.schedules || {}), [connectionId]: result.schedule },
    }
    setAutoPingConfigs({ ...autoPingConfigs, [provider]: nextProviderCfg })
    try {
      const r = await fetch('/api/settings', { cache: 'no-store' })
      const s = r.ok ? await r.json() : {}
      await fetch('/api/settings', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          [settingsKey]: {
            ...(s[settingsKey] || {}),
            connections: nextProviderCfg.connections,
            schedules: nextProviderCfg.schedules,
          },
        }),
      })
    } catch {
      setAutoPingConfigs(previous)
    }
  }

  const handleUpdateConnection = async (updates: Record<string, unknown>) => {
    if (!selectedConnection) return
    const conn = selectedConnection
    try {
      const res = await fetch(`/api/connections/${conn.id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(updates),
      })
      if (res.ok) {
        await fetchConnections()
        setShowEditModal(false)
        setSelectedConnection(null)
        await fetchQuota(conn.id, conn.provider)
      }
    } catch {
      // the modal keeps its state so the user can retry
    }
  }

  const hasEligible = totals.eligibleConnections > 0
  const hasVisible = sortedConnections.length > 0
  const emptyState = getConnectionsEmptyMessage(totals, providerFilter, accountFilter)
  const pageSummary = getConnectionsPaginationSummary(pagination)
  const isCustomPageSize = !ACCOUNT_PAGE_SIZE_OPTIONS.includes(pageSize)

  const applyPageSize = (raw: string) => {
    const parsed = Number.parseInt(raw, 10)
    if (!Number.isFinite(parsed)) {
      setCustomPageSizeInput(String(pageSize))
      return
    }
    const next = Math.min(ACCOUNT_PAGE_SIZE_MAX, Math.max(1, parsed))
    setPage(1)
    setPageSize(next)
    setCustomPageSizeInput(String(next))
  }

  if (!connectionsLoading && !hasEligible) {
    return <EmptyCard icon="cloud_off" title="No Providers Connected" description={emptyState.description} />
  }

  if (!connectionsLoading && !hasVisible) {
    return <EmptyCard icon={emptyState.icon} title={emptyState.title} description={emptyState.description} />
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-end">
        <div className="flex flex-wrap items-center gap-1.5">
          <div className="relative">
            <button
              type="button"
              onClick={() => setProviderMenuOpen((p) => !p)}
              className="flex h-8 cursor-pointer items-center justify-between gap-1 rounded-lg border border-black/10 bg-black/[0.02] px-2 text-xs text-text-main transition-colors hover:bg-black/5"
              aria-haspopup="menu"
              aria-expanded={providerMenuOpen}
              title="Filter quota providers"
            >
              <span className="flex min-w-0 items-center gap-1.5">
                {providerFilter === 'all' ? (
                  <Icon name="apps" className="text-[14px] text-text-muted" />
                ) : (
                  <ProviderIcon provider={providerFilter} size={18} className="size-[18px] rounded object-contain" />
                )}
                <span className="hidden truncate lg:inline">
                  {providerFilter === 'all' ? 'All providers' : providerLabel(providerFilter)}
                </span>
              </span>
              <Icon name="expand_more" className="text-[14px] text-text-muted" />
            </button>

            {providerMenuOpen && (
              <>
                <button
                  type="button"
                  className="fixed inset-0 z-30 bg-transparent"
                  aria-label="Close provider filter"
                  onClick={() => setProviderMenuOpen(false)}
                />
                <div className="absolute left-0 z-40 mt-2 w-64 overflow-hidden rounded-2xl border border-black/10 bg-surface/95 p-1.5 shadow-xl shadow-black/10 backdrop-blur sm:w-72">
                  <button
                    type="button"
                    onClick={() => {
                      if (providerFilter !== 'all') setPage(1)
                      changeProviderFilter('all')
                      setProviderMenuOpen(false)
                    }}
                    className={cn(
                      'flex w-full cursor-pointer items-center gap-3 rounded-xl px-3 py-2.5 text-left text-sm transition-colors',
                      providerFilter === 'all'
                        ? 'bg-primary/10 text-primary'
                        : 'text-text-main hover:bg-black/5',
                    )}
                  >
                    <Icon name="apps" className="text-[22px]" />
                    <span className="font-medium">All providers</span>
                    {providerFilter === 'all' && <Icon name="check" className="ml-auto text-[20px]" />}
                  </button>
                  <div className="my-1 h-px bg-black/10" />
                  <div className="max-h-72 overflow-y-auto pr-1">
                    {providerOptions.map((provider) => (
                      <button
                        key={provider}
                        type="button"
                        onClick={() => {
                          if (providerFilter !== provider) setPage(1)
                          changeProviderFilter(provider)
                          setProviderMenuOpen(false)
                        }}
                        className={cn(
                          'flex w-full cursor-pointer items-center gap-3 rounded-xl px-3 py-2.5 text-left text-sm transition-colors',
                          providerFilter === provider
                            ? 'bg-primary/10 text-primary'
                            : 'text-text-main hover:bg-black/5',
                        )}
                      >
                        <ProviderIcon provider={provider} size={24} className="size-6 rounded-md object-contain" />
                        <span className="font-medium">{providerLabel(provider)}</span>
                        {providerFilter === provider && (
                          <Icon name="check" className="ml-auto text-[20px]" />
                        )}
                      </button>
                    ))}
                  </div>
                </div>
              </>
            )}
          </div>

          <select
            value={accountFilter}
            onChange={(e) => {
              if (accountFilter !== e.target.value) setPage(1)
              setAccountFilter(e.target.value)
            }}
            className={selectClass}
            aria-label="Filter accounts by status"
          >
            {ACCOUNT_FILTER_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>

          {providerFilter === 'codex' && (
            <select
              value={quotaSortMode}
              onChange={(e) => setQuotaSortMode(e.target.value)}
              className={selectClass}
              aria-label="Sort Codex quotas by remaining"
            >
              {QUOTA_SORT_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          )}

          <button
            type="button"
            onClick={() => setExpiringFirst((p) => !p)}
            aria-pressed={expiringFirst}
            className={cn(
              'flex h-8 shrink-0 cursor-pointer items-center gap-1 rounded-lg border px-2 text-xs transition-colors',
              expiringFirst
                ? 'border-border bg-foreground/5 text-muted-foreground'
                : 'border-black/10 text-text-main hover:bg-black/5',
            )}
            title="Sort accounts by earliest quota reset time"
          >
            <Icon name="hourglass_top" className="text-[14px]" />
            <span className="hidden sm:inline">Expiring first</span>
          </button>

          <button
            type="button"
            onClick={() =>
              bulkSetActive(
                sortedConnections.filter((c) => (c.isActive ?? true) && isConnectionDepleted(c)).map((c) => c.id),
                false,
              )
            }
            disabled={bulkToggling}
            className="flex h-8 shrink-0 cursor-pointer items-center gap-1 rounded-lg border border-red-500/30 px-2 text-xs text-red-500 transition-colors hover:bg-red-500/10 disabled:opacity-50"
            title="Disable connections with depleted quota on the current page"
          >
            <Icon name="block" className="text-[14px]" />
            <span className="hidden sm:inline">Turn off Empty</span>
          </button>

          <button
            type="button"
            onClick={() =>
              bulkSetActive(
                sortedConnections.filter((c) => !(c.isActive ?? true) && !isConnectionDepleted(c)).map((c) => c.id),
                true,
              )
            }
            disabled={bulkToggling}
            className="flex h-8 shrink-0 cursor-pointer items-center gap-1 rounded-lg border border-border px-2 text-xs text-muted-foreground transition-colors hover:bg-foreground/5 disabled:opacity-50"
            title="Enable connections that still have quota on the current page"
          >
            <Icon name="check_circle" className="text-[14px]" />
            <span className="hidden sm:inline">Turn on Available</span>
          </button>

          <button
            onClick={() => setAutoRefresh((p) => !p)}
            className="flex h-8 shrink-0 cursor-pointer items-center gap-1 rounded-lg border border-black/10 px-2 text-xs transition-colors hover:bg-black/5"
            title={autoRefresh ? 'Disable auto-refresh' : 'Enable auto-refresh'}
          >
            <Icon
              name={autoRefresh ? 'toggle_on' : 'toggle_off'}
              className={cn('text-[14px]', autoRefresh ? 'text-primary' : 'text-text-muted')}
            />
            <span className="hidden text-text-main sm:inline">Auto-refresh</span>
            {autoRefresh && <span className="text-[10px] tabular-nums text-text-muted">({countdown}s)</span>}
          </button>

          <button
            type="button"
            onClick={() => refreshAll(true)}
            disabled={refreshingAll}
            className={iconButtonClass}
            title="Refresh all"
          >
            <Icon name="refresh" className={cn('text-[14px]', refreshingAll && 'animate-spin')} />
          </button>
        </div>
      </div>

      {expiringFirst && (
        <div className="rounded-xl border border-border bg-foreground/5 px-3 py-2 text-xs text-muted-foreground">
          Expiring-first currently reorders accounts inside the current page. Cross-page ordering
          still follows backend pagination.
        </div>
      )}

      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        {sortedConnections.map((conn) => (
          <QuotaCard
            key={conn.id}
            conn={conn}
            quota={quotaData[conn.id]}
            loading={!!loading[conn.id]}
            error={errors[conn.id] || null}
            providerLabel={providerLabel(conn.provider)}
            copied={copied}
            onCopy={copy}
            quotaVisibility={quotaVisibility}
            quotaSortMode={quotaSortMode}
            autoPingCfg={autoPingConfigs[conn.provider] || {}}
            busy={
              deletingId === conn.id ||
              togglingId === conn.id ||
              resettingLimitId === conn.id
            }
            isResetting={resettingLimitId === conn.id}
            isDeleting={deletingId === conn.id}
            onRefresh={() => refreshProvider(conn.id, conn.provider)}
            onToggleActive={(v) => handleToggleActive(conn, v)}
            onEdit={() => {
              setSelectedConnection(conn)
              setShowEditModal(true)
            }}
            onDelete={() => setDeleteTarget(conn)}
            onResetRequest={(count) => setResetConfirmState({ connection: conn, resetCreditCount: count })}
            onViewCodexResets={() => viewCodexResetCredits(conn)}
            onViewClaudeResets={(rc) => viewClaudeResets(conn, rc)}
            onSchedule={() => setScheduleConnection(conn)}
            onHideQuota={(row) => toggleQuotaVisibility(conn.provider, row, true)}
            onShowQuota={(row) => toggleQuotaVisibility(conn.provider, row, false)}
          />
        ))}
      </div>

      <div className="rounded-xl border border-black/10 bg-black/[0.02] px-3 py-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="text-xs text-text-muted">{pageSummary}</span>
          <div className="flex flex-wrap items-center gap-2">
            <select
              value={isCustomPageSize ? 'custom' : String(pageSize)}
              onChange={(e) => {
                const v = e.target.value
                if (v === 'custom') return
                const n = Number.parseInt(v, 10)
                if (Number.isFinite(n)) {
                  setPage(1)
                  setPageSize(n)
                  setCustomPageSizeInput(String(n))
                }
              }}
              className={selectClass}
              aria-label="Accounts per page"
            >
              {ACCOUNT_PAGE_SIZE_OPTIONS.map((o) => (
                <option key={o} value={String(o)}>
                  {o} / page
                </option>
              ))}
              <option value="custom">Custom</option>
            </select>
            <Input
              type="number"
              min="1"
              max={String(ACCOUNT_PAGE_SIZE_MAX)}
              inputMode="numeric"
              value={customPageSizeInput}
              onChange={(e) => setCustomPageSizeInput(e.target.value)}
              onBlur={() => applyPageSize(customPageSizeInput)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') applyPageSize(customPageSizeInput)
              }}
              className="h-8 w-20 text-xs"
              aria-label="Custom accounts per page"
              placeholder={getPageSizeLabel(pageSize, isCustomPageSize)}
            />
            <span className="text-xs text-text-muted">
              Page {pagination.page} / {pagination.totalPages}
            </span>
          </div>
          <div className="flex items-center gap-1.5">
            <button
              type="button"
              onClick={() => setPage(1)}
              disabled={pagination.page <= 1 || connectionsLoading || refreshingAll}
              className={cn(iconButtonClass, 'w-auto px-3 text-xs')}
            >
              First Page
            </button>
            <button
              type="button"
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={pagination.page <= 1 || connectionsLoading || refreshingAll}
              className={iconButtonClass}
              aria-label="Previous accounts page"
            >
              <Icon name="chevron_left" className="text-[16px]" />
            </button>
            <button
              type="button"
              onClick={() => setPage((p) => Math.min(pagination.totalPages, p + 1))}
              disabled={pagination.page >= pagination.totalPages || connectionsLoading || refreshingAll}
              className={iconButtonClass}
              aria-label="Next accounts page"
            >
              <Icon name="chevron_right" className="text-[16px]" />
            </button>
            <button
              type="button"
              onClick={() => setPage(pagination.totalPages)}
              disabled={pagination.page >= pagination.totalPages || connectionsLoading || refreshingAll}
              className={cn(iconButtonClass, 'w-auto px-3 text-xs')}
            >
              Last Page
            </button>
          </div>
        </div>
        {lastUpdated && (
          <p className="mt-1 text-[10px] text-text-muted">
            Updated {lastUpdated.toLocaleTimeString()}
          </p>
        )}
      </div>

      <ConfirmModal
        isOpen={!!resetConfirmState}
        onClose={() => {
          if (!resettingLimitId) setResetConfirmState(null)
        }}
        onConfirm={async () => {
          const conn = resetConfirmState?.connection
          if (!conn) return
          await handleResetLimit(conn.id, conn.provider)
          setResetConfirmState(null)
        }}
        title={resetConfirmState?.connection?.provider === 'claude' ? 'Reset Claude limits?' : 'Reset Codex limit?'}
        message={
          resetConfirmState?.connection?.provider === 'claude'
            ? `Refills your ${formatClaudeResetClears(
                (quotaData[resetConfirmState.connection.id]?.raw as any)?.resetCredits?.clears,
              )} now for ${getConnectionLabel(resetConfirmState.connection) || 'this account'} · your weekly reset day stays ${formatCreditDate(
                (quotaData[resetConfirmState.connection.id]?.raw as any)?.resetCredits?.weeklyResetsAt,
              )}. This cannot be undone. Resets left: ${resetConfirmState.resetCreditCount ?? 0}.`
            : `Use 1 Codex reset credit for ${getConnectionLabel(resetConfirmState?.connection || ({} as Conn)) || 'this account'}. This cannot be undone. Remaining credits: ${resetConfirmState?.resetCreditCount ?? 0}.`
        }
        confirmLabel="Reset limit"
        variant="danger"
        loading={!!resettingLimitId}
      />

      <ConfirmModal
        isOpen={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          const conn = deleteTarget
          setDeleteTarget(null)
          if (conn) handleDelete(conn)
        }}
        title="Delete connection"
        message={`Delete ${getConnectionLabel(deleteTarget ?? ({} as Conn)) || 'this connection'}? This cannot be undone.`}
        confirmLabel="Delete"
        variant="danger"
      />

      <ResetCreditsModal state={resetCreditsState} onClose={() => setResetCreditsState(null)} />

      <EditConnectionModal
        isOpen={showEditModal}
        connection={selectedConnection}
        onSave={handleUpdateConnection}
        onClose={() => {
          setShowEditModal(false)
          setSelectedConnection(null)
        }}
      />

      {scheduleConnection && (
        <AutoPingScheduleModal
          connection={scheduleConnection}
          reactive={autoPingConfigs[scheduleConnection.provider]?.connections?.[scheduleConnection.id] === true}
          schedule={autoPingConfigs[scheduleConnection.provider]?.schedules?.[scheduleConnection.id] || null}
          onSave={(result) => handleSaveAutoPing(scheduleConnection.id, scheduleConnection.provider, result)}
          onClose={() => setScheduleConnection(null)}
        />
      )}
    </div>
  )
}

function EmptyCard({ icon, title, description }: { icon: string; title: string; description: string }) {
  return (
    <Card className="p-8">
      <div className="py-12 text-center">
        <Icon name={icon} className="text-[64px] text-text-muted opacity-20" />
        <h3 className="mt-4 text-lg font-semibold text-text-main">{title}</h3>
        <p className="mx-auto mt-2 max-w-md text-sm text-text-muted">{description}</p>
      </div>
    </Card>
  )
}

function QuotaCard({
  conn,
  quota,
  loading,
  error,
  providerLabel,
  copied,
  onCopy,
  quotaVisibility,
  quotaSortMode,
  autoPingCfg,
  busy,
  isResetting,
  isDeleting,
  onRefresh,
  onToggleActive,
  onEdit,
  onDelete,
  onResetRequest,
  onViewCodexResets,
  onViewClaudeResets,
  onSchedule,
  onHideQuota,
  onShowQuota,
}: {
  conn: Conn
  quota?: QuotaEntry
  loading: boolean
  error: string | null
  providerLabel: string
  copied: string | null
  onCopy: (text: string, id?: string) => void
  quotaVisibility: Record<string, { hidden?: string[] }>
  quotaSortMode: string
  autoPingCfg: any
  busy: boolean
  isResetting: boolean
  isDeleting: boolean
  onRefresh: () => void
  onToggleActive: (v: boolean) => void
  onEdit: () => void
  onDelete: () => void
  onResetRequest: (count: number) => void
  onViewCodexResets: () => void
  onViewClaudeResets: (resetCredits: any) => void
  onSchedule: () => void
  onHideQuota: (row: QuotaRow) => void
  onShowQuota: (row: QuotaRow) => void
}) {
  const isInactive = conn.isActive === false
  const isCodex = conn.provider === 'codex'
  const claudeReset = conn.provider === 'claude' ? (quota?.raw as any)?.resetCredits : null
  const resetLabel = isCodex ? 'Codex reset credit' : 'Claude limit reset'
  const resetCreditCount = codexResetCreditCount(quota)
  const raw = quota?.quotas || []
  const visible = filterQuotasByVisibility(conn.provider, raw, quotaVisibility)
  const hidden = getHiddenQuotaRows(conn.provider, raw, quotaVisibility)

  const isReactive = autoPingCfg?.connections?.[conn.id] === true
  const schedule = autoPingCfg?.schedules?.[conn.id]
  const isScheduled = schedule?.enabled === true
  const autoPingActive = isReactive || isScheduled

  const tooltip = (text: string) => ({ title: text })

  return (
    <Card className={cn('min-w-0', isInactive && 'opacity-60')}>
      <div className="border-b border-black/10 px-3 py-2">
        <div className="flex items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2">
            <div className="flex size-8 shrink-0 items-center justify-center overflow-hidden rounded-md">
              <ProviderIcon provider={conn.provider} size={32} className="object-contain" />
            </div>
            <div className="min-w-0">
              <h3 className="truncate text-sm font-semibold text-text-main">{providerLabel}</h3>
              {getConnectionLabel(conn) && (
                <p className="truncate text-xs text-text-muted">{getConnectionLabel(conn)}</p>
              )}
              {secondaryLabel(conn) && (
                <p className="truncate text-[11px] text-text-muted/80">{secondaryLabel(conn)}</p>
              )}
              {conn.provider === 'kiro' && (
                <div className="mt-1 flex flex-wrap items-center gap-1">
                  <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[10px] font-semibold text-primary">
                    {kiroMethodLabel(conn)}
                  </span>
                  {kiroRegion(conn) && (
                    <span className="rounded-full bg-foreground/5 px-2 py-0.5 text-[10px] font-semibold text-muted-foreground">
                      {kiroRegion(conn)}
                    </span>
                  )}
                  <span
                    className={cn(
                      'rounded-full px-2 py-0.5 text-[10px] font-semibold',
                      isInactive
                        ? 'bg-surface-2 text-text-muted'
                        : conn.testStatus === 'active' || conn.testStatus === 'success'
                          ? 'bg-foreground/5 text-muted-foreground'
                          : conn.testStatus === 'error' ||
                              conn.testStatus === 'expired' ||
                              conn.testStatus === 'unavailable'
                            ? 'bg-red-500/10 text-red-600'
                            : 'bg-surface-2 text-text-muted',
                    )}
                  >
                    {isInactive ? 'disabled' : conn.testStatus || 'unknown'}
                  </span>
                  {Boolean(conn.providerSpecificData?.profileArn) && (
                    <button
                      type="button"
                      onClick={() => onCopy(String(conn.providerSpecificData?.profileArn), conn.id)}
                      title={String(conn.providerSpecificData?.profileArn)}
                      className="inline-flex max-w-full cursor-pointer items-center gap-1 rounded-full border border-border px-2 py-0.5 text-[10px] text-text-muted transition-colors hover:text-primary"
                    >
                      <Icon name={copied === conn.id ? 'check' : 'content_copy'} className="text-[12px]" />
                      <code className="truncate font-mono">{String(conn.providerSpecificData?.profileArn)}</code>
                    </button>
                  )}
                </div>
              )}
            </div>
          </div>

          <div className="flex shrink-0 items-center gap-1">
            {(isCodex || claudeReset) && (
              <>
                <button
                  type="button"
                  onClick={() => onResetRequest(resetCreditCount)}
                  disabled={resetCreditCount <= 0 || loading || busy}
                  aria-label={
                    resetCreditCount > 0
                      ? `Use one ${resetLabel}. ${resetCreditCount} available.`
                      : `No ${resetLabel}s available`
                  }
                  {...tooltip(
                    resetCreditCount > 0
                      ? claudeReset
                        ? `Use your reset now (${resetCreditCount} left, use by ${formatCreditDate(claudeReset.expiresAt)}) · refills ${formatClaudeResetClears(claudeReset.clears)}`
                        : `Use one ${resetLabel}. Available: ${resetCreditCount}`
                      : `No ${resetLabel}s available`,
                  )}
                  className={cn(
                    'flex h-8 min-w-10 cursor-pointer items-center justify-center gap-1 rounded-lg border px-2 text-[11px] font-medium tabular-nums transition-colors disabled:cursor-not-allowed disabled:opacity-60',
                    resetCreditCount > 0
                      ? 'border-primary/30 bg-primary/5 text-primary hover:bg-primary/10'
                      : 'border-black/10 bg-black/[0.02] text-text-muted',
                  )}
                >
                  <Icon
                    name={isResetting ? 'progress_activity' : 'restart_alt'}
                    className={cn('text-[15px]', isResetting && 'animate-spin')}
                  />
                  <span>{resetCreditCount}</span>
                </button>
                <button
                  type="button"
                  onClick={() => (isCodex ? onViewCodexResets() : onViewClaudeResets(claudeReset))}
                  disabled={loading || busy}
                  aria-label={isCodex ? 'View Codex reset credit expiry' : 'View Claude Code reset expiry'}
                  className={iconButtonClass}
                  {...tooltip(isCodex ? 'View Codex reset credit expiry' : 'View Claude Code reset expiry')}
                >
                  <Icon name="schedule" className="text-[17px]" />
                </button>
              </>
            )}

            {AUTO_PING_SETTINGS_KEYS[conn.provider] && conn.authType === 'oauth' && (
              <button
                type="button"
                onClick={onSchedule}
                aria-label="Configure auto-ping"
                className={cn(iconButtonClass, 'border-transparent hover:bg-black/5', autoPingActive ? 'text-primary' : 'text-text-muted')}
                title={(() => {
                  const modes: string[] = []
                  if (isReactive) modes.push('On reset')
                  if (isScheduled) {
                    const summary = getScheduleSlotLabels(schedule).join(' / ')
                    modes.push(`Scheduled (${summary || 'active'})`)
                  }
                  return modes.length
                    ? `Auto-ping active: ${modes.join(' + ')}. Click to configure.`
                    : conn.provider === 'codex'
                      ? 'Auto-starts the next 5h Codex window after reset or on schedule. Click to configure.'
                      : 'Auto-starts a new window on quota reset or fixed schedule. Click to configure.'
                })()}
              >
                <Icon name="bolt" className="text-[18px]" />
              </button>
            )}

            <button
              type="button"
              onClick={onRefresh}
              disabled={loading || busy}
              aria-label="Refresh quota"
              className={cn(iconButtonClass, 'border-transparent hover:bg-black/5')}
              title="Refresh quota"
            >
              <Icon name="refresh" className={cn('text-[18px] text-text-muted', loading && 'animate-spin')} />
            </button>
            <button
              type="button"
              onClick={onEdit}
              disabled={busy}
              aria-label="Edit connection"
              className={cn(iconButtonClass, 'border-transparent text-text-muted hover:bg-black/5 hover:text-primary')}
              title="Edit connection"
            >
              <Icon name="edit" className="text-[18px]" />
            </button>
            <button
              type="button"
              onClick={onDelete}
              disabled={busy}
              aria-label="Delete connection"
              className={cn(iconButtonClass, 'border-transparent text-red-500 hover:bg-red-500/10')}
              title="Delete connection"
            >
              <Icon name="delete" className={cn('text-[18px]', isDeleting && 'animate-pulse')} />
            </button>
            <div
              className="inline-flex items-center pl-0.5"
              title={(conn.isActive ?? true) ? 'Disable connection' : 'Enable connection'}
            >
              <Switch checked={conn.isActive ?? true} disabled={busy} onCheckedChange={onToggleActive} />
            </div>
          </div>
        </div>
      </div>

      <div className="px-2 py-1.5">
        {loading ? (
          <div className="py-5 text-center text-text-muted">
            <Icon name="progress_activity" className="animate-spin text-[28px]" />
          </div>
        ) : error ? (
          <div className="py-5 text-center">
            <Icon name="error" className="text-[28px] text-red-500" />
            <p className="mt-1.5 text-xs text-text-muted">{error}</p>
          </div>
        ) : quota?.message ? (
          <div className="py-5 text-center">
            <p className="text-xs text-text-muted">{quota.message}</p>
          </div>
        ) : (
          <QuotaTable
            quotas={visible}
            compact
            sortMode="default"
            showSortLabel={conn.provider === 'codex' && quotaSortMode !== 'default'}
            onHideQuota={onHideQuota}
          />
        )}

        {quota?.message && !error && !loading && (
          <p className="mt-2 px-1 text-[10px] leading-relaxed text-text-muted">{quota.message}</p>
        )}

        {hidden.length > 0 && (
          <div className="mt-1.5 flex min-w-0 items-center gap-1.5 text-[10px] text-text-muted">
            <Icon name="visibility_off" className="shrink-0 text-[13px]" />
            <span className="shrink-0">{hidden.length} hidden</span>
            <div className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto whitespace-nowrap">
              {hidden.map((row) => (
                <button
                  key={getQuotaVisibilityKey(row)}
                  type="button"
                  onClick={() => onShowQuota(row)}
                  className="shrink-0 cursor-pointer rounded bg-black/[0.04] px-1.5 py-0.5 text-text-muted transition-colors hover:bg-black/[0.08] hover:text-text-main dark:bg-white/[0.06] dark:hover:bg-white/[0.12]"
                  title={`Show ${row.name}`}
                >
                  {row.name}
                </button>
              ))}
            </div>
          </div>
        )}
      </div>
    </Card>
  )
}

function ResetCreditsModal({
  state,
  onClose,
}: {
  state: { connection: Conn; loading: boolean; error: string | null; data: any } | null
  onClose: () => void
}) {
  const isClaude = state?.data?.kind === 'claude'
  return (
    <Modal
      isOpen={!!state}
      onClose={onClose}
      size="full"
      title={isClaude ? 'Claude Code Limit Resets' : 'Codex Reset Credit Expiry'}
    >
      {state?.loading ? (
        <div className="flex items-center justify-center gap-2 py-10 text-sm text-text-muted">
          <Icon name="progress_activity" className="animate-spin text-[20px]" />
          Loading reset credits...
        </div>
      ) : state?.error ? (
        <div className="rounded-xl border border-red-500/20 bg-red-500/10 px-3 py-2 text-sm text-red-600">
          {state.error}
        </div>
      ) : isClaude && state?.data?.grants?.length ? (
        <div className="space-y-3">
          <div className="flex items-center justify-between rounded-xl border border-black/10 bg-black/[0.02] px-3 py-2 text-xs text-text-muted">
            <span>
              {state.data.availableCount ?? 0} reset{state.data.availableCount === 1 ? '' : 's'} left
            </span>
            <span>Weekly reset day: {formatCreditDate(state.data.weeklyResetsAt)}</span>
          </div>
          <div className="overflow-x-auto rounded-xl border border-black/10">
            <table className="w-full min-w-[560px] text-left text-sm">
              <thead className="bg-black/[0.03] text-xs uppercase tracking-wide text-text-muted">
                <tr>
                  <th className="px-3 py-2 font-medium">Reset</th>
                  <th className="px-3 py-2 font-medium">Left</th>
                  <th className="px-3 py-2 font-medium">Refills</th>
                  <th className="px-3 py-2 font-medium">Use By</th>
                  <th className="px-3 py-2 font-medium">Remaining</th>
                </tr>
              </thead>
              <tbody>
                {(state.data.grants || []).map((grant: any) => (
                  <tr key={grant.id} className="border-t border-black/5">
                    <td className="px-3 py-2">
                      <div className="text-text-main">{grant.label || grant.id}</div>
                      <span className="mt-1 inline-block rounded-full bg-primary/10 px-2 py-0.5 text-xs font-medium text-primary">
                        {claudeGrantStatus(grant)}
                      </span>
                    </td>
                    <td className="whitespace-nowrap px-3 py-2 font-medium tabular-nums text-text-main">
                      {grant.resetsLeft} / {grant.resetsTotal}
                    </td>
                    <td className="px-3 py-2 text-text-muted">{formatClaudeResetClears(grant.clears)}</td>
                    <td className="px-3 py-2 text-text-main">{formatCreditDate(grant.endsAt)}</td>
                    <td className="whitespace-nowrap px-3 py-2 font-medium text-text-main">
                      {formatTimeRemaining(grant.endsAt)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : state?.data?.credits?.length ? (
        <div className="space-y-3">
          <div className="flex items-center justify-between rounded-xl border border-black/10 bg-black/[0.02] px-3 py-2 text-xs text-text-muted">
            <span>
              {state.data.credits.length} reset credit{state.data.credits.length === 1 ? '' : 's'}
            </span>
            <span>{state.data.availableCount ?? 0} available</span>
          </div>
          <div className="overflow-x-auto rounded-xl border border-black/10">
            <table className="w-full min-w-[560px] text-left text-sm">
              <thead className="bg-black/[0.03] text-xs uppercase tracking-wide text-text-muted">
                <tr>
                  <th className="px-3 py-2 font-medium">Status</th>
                  <th className="px-3 py-2 font-medium">Granted At</th>
                  <th className="px-3 py-2 font-medium">Expires At</th>
                  <th className="px-3 py-2 font-medium">Remaining</th>
                </tr>
              </thead>
              <tbody>
                {state.data.credits.map((credit: any, index: number) => (
                  <tr key={`${credit.status}-${credit.expiresAt || index}`} className="border-t border-black/5">
                    <td className="px-3 py-2">
                      <span className="rounded-full bg-primary/10 px-2 py-0.5 text-xs font-medium text-primary">
                        {credit.status || 'unknown'}
                      </span>
                    </td>
                    <td className="px-3 py-2 text-text-muted">{formatCreditDate(credit.grantedAt)}</td>
                    <td className="px-3 py-2 text-text-main">{formatCreditDate(credit.expiresAt)}</td>
                    <td className="px-3 py-2 font-medium text-text-main">{formatTimeRemaining(credit.expiresAt)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : (
        <div className="rounded-xl border border-black/10 bg-black/[0.02] px-3 py-8 text-center text-sm text-text-muted">
          {isClaude
            ? 'No limit resets available for this account.'
            : 'No reset credit details returned for this account.'}
        </div>
      )}
    </Modal>
  )
}
