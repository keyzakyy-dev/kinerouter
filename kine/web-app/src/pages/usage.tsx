import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, type Connection, type ProviderSummary, type UsageStat, type UsageStats } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { Segmented } from '@/components/usage/helpers'
import type { TableView, ViewMode } from '@/components/usage/helpers'
import {
  ACCOUNT_COLUMNS, API_KEY_COLUMNS, ENDPOINT_COLUMNS, MODEL_COLUMNS,
  PERIODS, TABLE_OPTIONS, fmt, fmtTime, groupDataByKey, sortData,
} from '@/components/usage/helpers'
import type { Group, Row } from '@/components/usage/helpers'
import { UsageTable } from '@/components/usage/usage-table'
import { OverviewCards } from '@/components/usage/overview-cards'
import { RecentRequests } from '@/components/usage/recent-requests'
import { ProviderTopology, type TopoProvider } from '@/components/usage/topology'
import { ProviderBarChart, TopModelsChart, UsageChart } from '@/components/usage/charts'
import { RequestLogger } from '@/components/usage/request-logger'
import { RequestDetailsTab } from '@/components/usage/request-details-tab'
import { Icon } from '@/components/icon'

const TABS = [
  { value: 'overview', label: 'Overview' },
  { value: 'details', label: 'Details' },
] as const
type Tab = 'overview' | 'details' | 'logs'

function isLLM(c: ProviderSummary) {
  return !c.serviceKinds || c.serviceKinds.includes('llm')
}

export default function UsagePage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const tabParam = searchParams.get('tab')
  const activeTab: Tab = tabParam === 'details' || tabParam === 'logs' ? tabParam : 'overview'
  const [period, setPeriod] = useState('today')

  const setTab = (v: string) => {
    if (v === activeTab) return
    const next = new URLSearchParams(searchParams)
    next.set('tab', v)
    setSearchParams(next, { replace: true })
  }

  return (
    <div className="flex min-w-0 flex-col gap-6 px-1 sm:px-0">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <Segmented className="w-full sm:w-auto" options={TABS} value={activeTab === 'logs' ? 'overview' : activeTab} onChange={setTab} />
        {activeTab === 'overview' && (
          <Segmented className="w-full sm:w-auto" size="sm" options={PERIODS} value={period} onChange={setPeriod} />
        )}
      </div>

      {activeTab === 'overview' && <UsageStats period={period} />}
      {activeTab === 'logs' && <RequestLogger />}
      {activeTab === 'details' && <RequestDetailsTab />}
    </div>
  )
}

function UsageStats({ period }: { period: string }) {
  const [searchParams, setSearchParams] = useSearchParams()
  const sortBy = searchParams.get('sortBy') || 'rawModel'
  const sortOrder = (searchParams.get('sortOrder') === 'desc' ? 'desc' : 'asc') as 'asc' | 'desc'

  const [stats, setStats] = useState<UsageStats | null>(null)
  const [loading, setLoading] = useState(true)
  const [tableView, setTableView] = useState<TableView>('model')
  const [viewMode, setViewMode] = useState<ViewMode>('costs')
  const [providers, setProviders] = useState<TopoProvider[]>([])
  const isInitialLoad = useRef(true)

  // Connected LLM providers, one node per provider type.
  useEffect(() => {
    Promise.all([api.providers(), api.providerNodes()])
      .then(([p, n]) => {
        const nodeName: Record<string, string> = {}
        for (const node of n.nodes || []) if (node.name) nodeName[node.id] = node.name
        const seen = new Set<string>()
        const unique = ((p.connections || []) as Connection[])
          .filter((c) => {
            if (c.isActive === false || seen.has(c.provider)) return false
            const summary = (p.providers || []).find((s) => s.id === c.provider)
            if (!summary || summary.hidden || !isLLM(summary)) return false
            seen.add(c.provider)
            return true
          })
          .map((c) => {
            const summary = (p.providers || []).find((s) => s.id === c.provider)
            return {
              provider: c.provider,
              name: summary?.display?.name || c.provider,
              nodeName: nodeName[c.provider] || null,
              color: summary?.display?.color,
              textIcon: summary?.display?.textIcon,
            }
          }) as TopoProvider[]
        const free = (p.providers || [])
          .filter((s) => s.noAuth && !s.hidden && !seen.has(s.id) && isLLM(s))
          .map((s) => ({ provider: s.id, name: s.display?.name || s.id, color: s.display?.color, textIcon: s.display?.textIcon }))
        setProviders([...unique, ...free])
      })
      .catch(() => {})
  }, [])

  // Period stats via REST; the stream only carries live fields.
  useEffect(() => {
    if (isInitialLoad.current) {
      isInitialLoad.current = false
      setLoading(true)
    }
    api.usageStats(period)
      .then((data) => setStats((prev) => ({ ...prev, ...data })))
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [period])

  useEffect(() => {
    const es = new EventSource('/api/usage/stream')
    es.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data) as Partial<UsageStats>
        setStats((prev) => prev && {
          ...prev,
          activeRequests: data.activeRequests ?? prev.activeRequests,
          recentRequests: data.recentRequests ?? prev.recentRequests,
          errorProvider: data.errorProvider ?? prev.errorProvider,
          pending: data.pending ?? prev.pending,
        })
      } catch { /* ignore a torn frame */ }
    }
    es.onerror = () => setLoading(false)
    return () => es.close()
  }, [])

  const toggleSort = useCallback((_table: string, field: string) => {
    const next = new URLSearchParams(searchParams)
    if (next.get('sortBy') === field) {
      next.set('sortOrder', next.get('sortOrder') === 'asc' ? 'desc' : 'asc')
    } else {
      next.set('sortBy', field)
      next.set('sortOrder', 'asc')
    }
    setSearchParams(next, { replace: true })
  }, [searchParams, setSearchParams])

  const table = useMemo(() => {
    if (!stats) return null
    const sort = (map: Record<string, UsageStat> | undefined, pending: Record<string, number> = {}) =>
      sortData(map, pending, sortBy, sortOrder)

    const summaryCells = (group: Group) => (
      <>
        <td className="px-6 py-3 text-text-muted">—</td>
        <td className="px-6 py-3 text-right">{fmt(group.summary.requests)}</td>
        <td className="whitespace-nowrap px-6 py-3 text-right text-text-muted">{fmtTime(group.summary.lastUsed)}</td>
      </>
    )

    switch (tableView) {
      case 'account': {
        const pending: Record<string, number> = {}
        for (const [key, data] of Object.entries(stats.byAccount || {})) {
          const connPending = data.connectionId ? stats.pending?.byAccount?.[data.connectionId] : undefined
          if (connPending) {
            const modelKey = data.provider ? `${data.rawModel} (${data.provider})` : data.rawModel
            pending[key] = connPending[modelKey as string] || 0
          }
        }
        return {
          columns: ACCOUNT_COLUMNS,
          groupedData: groupDataByKey(sort(stats.byAccount, pending), 'accountName'),
          storageKey: 'usage-stats:expanded-accounts',
          emptyMessage: 'No account-specific usage recorded yet.',
          renderSummaryCells: summaryCells,
          renderDetailCells: (item: Row) => (
            <>
              <td className={`px-6 py-3 font-medium ${item.pending > 0 ? 'text-primary' : ''}`}>{item.accountName || `Account ${item.connectionId?.slice(0, 8)}...`}</td>
              <td className={`px-6 py-3 font-medium ${item.pending > 0 ? 'text-primary' : ''}`}>{item.rawModel}</td>
              <td className="px-6 py-3"><Badge variant={item.pending > 0 ? 'default' : 'secondary'} size="sm">{item.provider}</Badge></td>
              <td className="px-6 py-3 text-right">{fmt(item.requests)}</td>
              <td className="whitespace-nowrap px-6 py-3 text-right text-text-muted">{fmtTime(item.lastUsed)}</td>
            </>
          ),
        }
      }
      case 'apiKey':
        return {
          columns: API_KEY_COLUMNS,
          groupedData: groupDataByKey(sort(stats.byApiKey), 'keyName'),
          storageKey: 'usage-stats:expanded-apikeys',
          emptyMessage: 'No API key usage recorded yet.',
          renderSummaryCells: summaryCells,
          renderDetailCells: (item: Row) => (
            <>
              <td className="px-6 py-3 font-mono text-xs font-medium">{item.apiKeyMasked || item.keyName}</td>
              <td className="px-6 py-3">{item.rawModel}</td>
              <td className="px-6 py-3"><Badge variant="secondary" size="sm">{item.provider}</Badge></td>
              <td className="px-6 py-3 text-right">{fmt(item.requests)}</td>
              <td className="whitespace-nowrap px-6 py-3 text-right text-text-muted">{fmtTime(item.lastUsed)}</td>
            </>
          ),
        }
      case 'endpoint':
        return {
          columns: ENDPOINT_COLUMNS,
          groupedData: groupDataByKey(sort(stats.byEndpoint), 'endpoint'),
          storageKey: 'usage-stats:expanded-endpoints',
          emptyMessage: 'No endpoint usage recorded yet.',
          renderSummaryCells: summaryCells,
          renderDetailCells: (item: Row) => (
            <>
              <td className="px-6 py-3 font-mono text-sm font-medium">{item.endpoint}</td>
              <td className="px-6 py-3">{item.rawModel}</td>
              <td className="px-6 py-3"><Badge variant="secondary" size="sm">{item.provider}</Badge></td>
              <td className="px-6 py-3 text-right">{fmt(item.requests)}</td>
              <td className="whitespace-nowrap px-6 py-3 text-right text-text-muted">{fmtTime(item.lastUsed)}</td>
            </>
          ),
        }
      case 'model':
      default:
        return {
          columns: MODEL_COLUMNS,
          groupedData: groupDataByKey(sort(stats.byModel, stats.pending?.byModel || {}), 'rawModel'),
          storageKey: 'usage-stats:expanded-models',
          emptyMessage: 'No usage recorded yet.',
          renderSummaryCells: summaryCells,
          renderDetailCells: (item: Row) => (
            <>
              <td className={`px-6 py-3 font-medium ${item.pending > 0 ? 'text-primary' : ''}`}>{item.rawModel}</td>
              <td className="px-6 py-3"><Badge variant={item.pending > 0 ? 'default' : 'secondary'} size="sm">{item.provider}</Badge></td>
              <td className="px-6 py-3 text-right">{fmt(item.requests)}</td>
              <td className="whitespace-nowrap px-6 py-3 text-right text-text-muted">{fmtTime(item.lastUsed)}</td>
            </>
          ),
        }
    }
  }, [stats, tableView, sortBy, sortOrder])

  if (!stats && !loading) return <div className="text-text-muted">Failed to load usage statistics.</div>

  const spinner = (
    <div className="flex items-center justify-center py-12 text-text-muted">
      <Icon name="progress_activity" className="animate-spin text-[32px]" />
    </div>
  )

  return (
    <div className="flex min-w-0 flex-col gap-6">
      {loading || !stats ? spinner : <OverviewCards stats={stats} period={period} />}

      {loading || !stats ? spinner : (
        <div className="grid min-w-0 grid-cols-1 items-stretch gap-2 lg:grid-cols-[minmax(0,2fr)_minmax(280px,1fr)]">
          <ProviderTopology
            providers={providers}
            activeRequests={stats.activeRequests || []}
            lastProvider={stats.recentRequests?.[0]?.provider || ''}
            errorProvider={stats.errorProvider || ''}
          />
          <RecentRequests requests={stats.recentRequests || []} />
        </div>
      )}

      {loading ? spinner : <UsageChart period={period} />}

      {!loading && stats && (
        <div className="grid min-w-0 grid-cols-1 gap-2 lg:grid-cols-2">
          <ProviderBarChart byProvider={stats.byProvider} />
          <TopModelsChart byModel={stats.byModel} />
        </div>
      )}

      <section className="flex min-w-0 flex-col overflow-hidden rounded-xl bg-surface">
        <div className="flex flex-wrap items-center gap-2 px-4 py-3">
          <Icon name="table_chart" className="text-[15px] text-text-muted" />
          <select
            value={tableView}
            onChange={(e) => setTableView(e.target.value as TableView)}
            className="-ml-1 cursor-pointer rounded-lg bg-transparent py-1 pl-1 pr-1 text-sm font-semibold text-text-main focus:outline-none"
          >
            {TABLE_OPTIONS.map((opt) => <option key={opt.value} value={opt.value}>{opt.label}</option>)}
          </select>
          <Segmented
            className="ml-auto grid-cols-2 sm:flex"
            size="sm"
            options={[
              { value: 'costs' as ViewMode, label: 'Costs' },
              { value: 'tokens' as ViewMode, label: 'Tokens' },
            ]}
            value={viewMode}
            onChange={setViewMode}
          />
        </div>
        <div className="min-w-0 px-2 pb-2">
          {loading || !table ? spinner : (
            <UsageTable
              columns={table.columns}
              groupedData={table.groupedData}
              tableType={tableView}
              sortBy={sortBy}
              sortOrder={sortOrder}
              onToggleSort={toggleSort}
              viewMode={viewMode}
              storageKey={table.storageKey}
              renderDetailCells={table.renderDetailCells}
              renderSummaryCells={table.renderSummaryCells}
              emptyMessage={table.emptyMessage}
            />
          )}
        </div>
      </section>
    </div>
  )
}
