import { useCallback, useEffect, useState } from 'react'
import { api, type RequestDetail, type RequestDetailsPagination } from '@/lib/api'
import { fmtInt as fmt } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Modal } from '@/components/modal'

function tokens(rec: unknown): Record<string, number> {
  return (rec && typeof rec === 'object' ? rec : {}) as Record<string, number>
}
const cachedOf = (t: Record<string, number>) => t.cached_tokens || t.cache_read_input_tokens || 0
const cacheCreateOf = (t: Record<string, number>) => t.cache_creation_input_tokens || 0
const inputOf = (t: Record<string, number>) => {
  const p = t.prompt_tokens || t.input_tokens || 0
  const c = cachedOf(t)
  // Legacy Claude rows stored prompt cache-exclusive; fall back to cache.
  return p < c ? c : p
}
const outputOf = (t: Record<string, number>) => t.completion_tokens || t.output_tokens || 0

function CollapsibleSection({
  title, children, defaultOpen = false, icon,
}: { title: string; children: React.ReactNode; defaultOpen?: boolean; icon?: string }) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="overflow-hidden rounded-lg border border-border/60">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between bg-black/[0.02] p-3 transition-colors hover:bg-black/[0.04] dark:bg-white/[0.02] dark:hover:bg-white/[0.04]"
      >
        <div className="flex items-center gap-2">
          {icon && <span className="material-symbols-outlined text-[18px] text-text-muted">{icon}</span>}
          <span className="text-sm font-semibold text-text-main">{title}</span>
        </div>
        <span className={cn('material-symbols-outlined text-[20px] text-text-muted transition-transform duration-200', open && 'rotate-90')}>
          chevron_right
        </span>
      </button>
      {open && <div className="border-t border-border/60 p-4">{children}</div>}
    </div>
  )
}

function Pre({ children }: { children: React.ReactNode }) {
  return (
    <pre className="max-h-[300px] max-w-full overflow-auto rounded-lg border border-border/60 bg-black/5 p-3 font-mono text-xs text-text-main sm:p-4 dark:bg-white/5">
      {children}
    </pre>
  )
}

const PAGE_SIZES = [10, 20, 50, 100]

/** Paginated request history with filters and a detail drawer. */
export function RequestDetailsTab() {
  const [details, setDetails] = useState<RequestDetail[]>([])
  const [pagination, setPagination] = useState<RequestDetailsPagination>({
    page: 1, pageSize: 20, totalItems: 0, totalPages: 0, hasNext: false, hasPrev: false,
  })
  const [loading, setLoading] = useState(false)
  const [selected, setSelected] = useState<RequestDetail | null>(null)
  const [providers, setProviders] = useState<{ id: string; name: string }[]>([])
  const [nameById, setNameById] = useState<Record<string, string>>({})
  const [filters, setFilters] = useState({ provider: '', startDate: '', endDate: '' })

  useEffect(() => {
    api.usageProviders().then((d) => {
      setProviders(d.providers || [])
      setNameById(Object.fromEntries((d.providers || []).map((p) => [p.id, p.name])))
    }).catch(() => {})
    api.providers().then((d) => {
      setNameById((prev) => {
        const next = { ...prev }
        for (const s of d.providers || []) if (s.display?.name) next[s.id] ??= s.display.name
        return next
      })
    }).catch(() => {})
  }, [])

  const fetchDetails = useCallback(async () => {
    setLoading(true)
    try {
      const params: Record<string, string> = {
        page: String(pagination.page),
        pageSize: String(pagination.pageSize),
      }
      if (filters.provider) params.provider = filters.provider
      if (filters.startDate) params.startDate = filters.startDate
      if (filters.endDate) params.endDate = filters.endDate
      const data = await api.requestDetails(params)
      setDetails(data.details || [])
      setPagination((prev) => ({ ...prev, ...data.pagination }))
    } catch { /* leave the last page on a transient error */ }
    finally { setLoading(false) }
  }, [pagination.page, pagination.pageSize, filters])

  useEffect(() => { void fetchDetails() }, [fetchDetails])

  const hasFilters = !!(filters.provider || filters.startDate || filters.endDate)
  const inputCls = 'h-9 w-full min-w-0 rounded-lg border border-border bg-surface px-3 text-sm text-text-main focus:outline-none focus:ring-2 focus:ring-primary/20'

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <Card className="p-4">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div className="flex min-w-0 flex-col gap-2">
            <label htmlFor="provider-filter" className="text-sm font-medium text-text-main">Provider</label>
            <select
              id="provider-filter"
              value={filters.provider}
              onChange={(e) => setFilters({ ...filters, provider: e.target.value })}
              className={cn(inputCls, 'cursor-pointer')}
            >
              <option value="">All Providers</option>
              {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
          </div>
          <div className="flex min-w-0 flex-col gap-2">
            <label htmlFor="start-date-filter" className="text-sm font-medium text-text-main">Start Date</label>
            <input
              id="start-date-filter" type="datetime-local" value={filters.startDate}
              onChange={(e) => setFilters({ ...filters, startDate: e.target.value })} className={inputCls}
            />
          </div>
          <div className="flex min-w-0 flex-col gap-2">
            <label htmlFor="end-date-filter" className="text-sm font-medium text-text-main">End Date</label>
            <input
              id="end-date-filter" type="datetime-local" value={filters.endDate}
              onChange={(e) => setFilters({ ...filters, endDate: e.target.value })} className={inputCls}
            />
          </div>
          <div className="flex min-w-0 flex-col gap-2 sm:col-span-2 lg:col-span-1">
            <span className="hidden text-sm font-medium opacity-0 lg:block" aria-hidden="true">Clear</span>
            <Button variant="ghost" onClick={() => setFilters({ provider: '', startDate: '', endDate: '' })} disabled={!hasFilters} className="w-full">
              Clear Filters
            </Button>
          </div>
        </div>
      </Card>

      <Card className="overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[880px]">
            <thead>
              <tr className="border-b border-border/60 text-sm font-semibold text-text-main">
                <th className="p-4 text-left">Timestamp</th>
                <th className="p-4 text-left">Model</th>
                <th className="p-4 text-left">Provider</th>
                <th className="p-4 text-right">Input Tokens</th>
                <th className="p-4 text-right">Cached</th>
                <th className="p-4 text-right">Cache Creation</th>
                <th className="p-4 text-right">Output Tokens</th>
                <th className="p-4 text-left">Latency</th>
                <th className="p-4 text-center">Action</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr>
                  <td colSpan={9} className="p-8 text-center text-text-muted">
                    <div className="flex items-center justify-center gap-2">
                      <span className="material-symbols-outlined animate-spin text-[20px]">progress_activity</span>
                      Loading...
                    </div>
                  </td>
                </tr>
              ) : details.length === 0 ? (
                <tr><td colSpan={9} className="p-8 text-center text-text-muted">No request details found</td></tr>
              ) : details.map((d, i) => {
                const t = tokens(d.tokens)
                return (
                  <tr key={`${d.id}-${i}`} className="border-b border-border/60 transition-colors last:border-b-0 hover:bg-black/[0.02] dark:hover:bg-white/[0.02]">
                    <td className="whitespace-nowrap p-4 text-sm text-text-main">{new Date(d.timestamp).toLocaleString()}</td>
                    <td className="max-w-[260px] truncate p-4 font-mono text-sm text-text-main">{d.model}</td>
                    <td className="max-w-[180px] truncate p-4 text-sm"><span className="font-medium">{nameById[d.provider] || d.provider}</span></td>
                    <td className="p-4 text-right font-mono text-sm text-text-main">{fmt(inputOf(t))}</td>
                    <td className="p-4 text-right font-mono text-sm text-text-main">{cachedOf(t) > 0 ? fmt(cachedOf(t)) : '—'}</td>
                    <td className="p-4 text-right font-mono text-sm text-text-main">{cacheCreateOf(t) > 0 ? fmt(cacheCreateOf(t)) : '—'}</td>
                    <td className="p-4 text-right font-mono text-sm text-text-main">{fmt(outputOf(t))}</td>
                    <td className="p-4 text-sm text-text-muted">
                      <div className="flex flex-col gap-0.5">
                        <div>TTFT: <span className="font-mono">{d.latency?.ttft || 0}ms</span></div>
                        <div>Total: <span className="font-mono">{d.latency?.total || 0}ms</span></div>
                      </div>
                    </td>
                    <td className="p-4 text-center">
                      <Button variant="outline" size="sm" onClick={() => setSelected(d)}>Detail</Button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>

        {!loading && details.length > 0 && (
          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border/60 p-4 text-sm">
            <div className="flex items-center gap-2 text-text-muted">
              <span>Rows</span>
              <select
                value={pagination.pageSize}
                onChange={(e) => setPagination((p) => ({ ...p, pageSize: Number(e.target.value), page: 1 }))}
                className="h-8 cursor-pointer rounded-md border border-border bg-surface px-2 text-sm"
              >
                {PAGE_SIZES.map((n) => <option key={n} value={n}>{n}</option>)}
              </select>
              <span>
                {(pagination.page - 1) * pagination.pageSize + 1}–
                {Math.min(pagination.page * pagination.pageSize, pagination.totalItems)} of {fmt(pagination.totalItems)}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <Button variant="outline" size="sm" disabled={pagination.page <= 1} onClick={() => setPagination((p) => ({ ...p, page: p.page - 1 }))}>
                Prev
              </Button>
              <span className="text-text-muted">{pagination.page} / {Math.max(1, pagination.totalPages)}</span>
              <Button variant="outline" size="sm" disabled={pagination.page >= pagination.totalPages} onClick={() => setPagination((p) => ({ ...p, page: p.page + 1 }))}>
                Next
              </Button>
            </div>
          </div>
        )}
      </Card>

      <Modal isOpen={!!selected} onClose={() => setSelected(null)} title="Request Details" size="full">
        {selected && <DetailBody detail={selected} nameById={nameById} />}
      </Modal>
    </div>
  )
}

function DetailBody({ detail, nameById }: { detail: RequestDetail; nameById: Record<string, string> }) {
  const t = tokens(detail.tokens)
  const response = (detail.response || {}) as { content?: string; thinking?: string; error?: string }
  const rows: [string, React.ReactNode][] = [
    ['ID', <span className="break-all font-mono text-text-main">{detail.id}</span>],
    ['Timestamp', new Date(detail.timestamp).toLocaleString()],
    ['Provider', <span className="font-medium">{nameById[detail.provider] || detail.provider}</span>],
    ['Model', <span className="font-mono">{detail.model}</span>],
    ['Status', <span className={detail.status === 'success' ? 'font-medium text-success' : 'font-medium text-danger'}>{detail.status}</span>],
    ['Latency', <span className="font-mono">TTFT {detail.latency?.ttft || 0}ms / Total {detail.latency?.total || 0}ms</span>],
    ['Input Tokens', <span className="font-mono">{fmt(inputOf(t))}</span>],
  ]
  if (cachedOf(t) > 0) rows.push(['Cached Tokens', <span className="font-mono">{fmt(cachedOf(t))}</span>])
  if (cacheCreateOf(t) > 0) rows.push(['Cache Creation', <span className="font-mono">{fmt(cacheCreateOf(t))}</span>])
  rows.push(['Output Tokens', <span className="font-mono">{fmt(outputOf(t))}</span>])

  const pxpipe = detail.pxpipe

  return (
    <div className="space-y-6">
      <div className="grid min-w-0 grid-cols-1 gap-4 text-sm sm:grid-cols-2">
        {rows.map(([k, v]) => (
          <div key={k}>
            <span className="text-text-muted">{k}:</span> <span className="text-text-main">{v}</span>
          </div>
        ))}
      </div>

      {pxpipe && (
        <div className="rounded-lg border border-border/60 p-4">
          <div className="mb-2 flex items-center gap-2">
            <span className="material-symbols-outlined text-[18px] text-text-muted">image</span>
            <span className="text-sm font-semibold text-text-main">PXPIPE</span>
            <span className={cn('rounded px-2 py-0.5 text-xs', pxpipe.applied ? 'bg-success/15 text-success' : 'bg-warning/15 text-warning')}>
              {pxpipe.applied ? 'Activated' : 'Skipped'}
            </span>
          </div>
          {pxpipe.applied ? (
            <div className="grid grid-cols-2 gap-2 text-sm sm:grid-cols-4">
              <div><span className="block text-xs text-text-muted">Original (est.)</span><span className="font-mono">{fmt(pxpipe.tokensBeforeEst)} tokens</span></div>
              <div><span className="block text-xs text-text-muted">Compressed (est.)</span><span className="font-mono">{fmt(pxpipe.tokensAfterEst)} tokens</span></div>
              <div><span className="block text-xs text-text-muted">Saved</span><span className="font-mono text-success">{pxpipe.savedPct || 0}%</span></div>
              <div><span className="block text-xs text-text-muted">Images</span><span className="font-mono">{pxpipe.imageCount || 0} ({pxpipe.durationMs || 0}ms)</span></div>
            </div>
          ) : (
            <p className="text-sm text-text-muted">
              Reason: <span className="font-mono">{pxpipe.reason}</span>
              {pxpipe.detail ? ` — ${pxpipe.detail}` : ''}
            </p>
          )}
        </div>
      )}

      <div className="space-y-4">
        <CollapsibleSection title="1. Client Request (Input)" defaultOpen icon="input">
          <Pre>{JSON.stringify(detail.request, null, 2)}</Pre>
        </CollapsibleSection>
        {!!detail.providerRequest && (
          <CollapsibleSection title="2. Provider Request (Translated)" icon="translate">
            <Pre>{JSON.stringify(detail.providerRequest, null, 2)}</Pre>
          </CollapsibleSection>
        )}
        {!!detail.providerResponse && (
          <CollapsibleSection title="3. Provider Response (Raw)" icon="data_object">
            <Pre>{typeof detail.providerResponse === 'object' ? JSON.stringify(detail.providerResponse, null, 2) : String(detail.providerResponse)}</Pre>
          </CollapsibleSection>
        )}
        <CollapsibleSection title="4. Client Response (Final)" defaultOpen icon="output">
          {response.thinking && (
            <div className="mb-4">
              <h4 className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-text-main opacity-70">
                <span className="material-symbols-outlined text-[16px]">psychology</span>
                Thinking Process
              </h4>
              <pre className="max-h-[200px] max-w-full overflow-auto rounded-lg border border-warning/40 bg-warning/10 p-3 font-mono text-xs sm:p-4">{response.thinking}</pre>
            </div>
          )}
          <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-text-main opacity-70">Content</h4>
          <Pre>{response.content || response.error || '[No content]'}</Pre>
        </CollapsibleSection>
      </div>
    </div>
  )
}
