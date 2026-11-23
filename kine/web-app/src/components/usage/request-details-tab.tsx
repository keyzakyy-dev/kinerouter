import { useCallback, useEffect, useState } from 'react'
import { api, type RequestDetail, type RequestDetailsPagination } from '@/lib/api'
import { fmtInt as fmt } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Modal } from '@/components/modal'
import { Icon } from '@/components/icon'

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
    <div className="overflow-hidden rounded-xl bg-surface-2">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full cursor-pointer items-center justify-between px-3 py-2.5 text-left transition-colors hover:bg-black/[0.03] dark:hover:bg-white/[0.04]"
      >
        <span className="flex items-center gap-2">
          {icon && <Icon name={icon} className="text-[15px] text-text-muted" />}
          <span className="text-xs font-semibold text-text-main">{title}</span>
        </span>
        <Icon name={open ? 'expand_less' : 'expand_more'} className="text-[16px] text-text-muted" />
      </button>
      {open && <div className="border-t border-border p-3">{children}</div>}
    </div>
  )
}

function Pre({ children }: { children: React.ReactNode }) {
  return (
    <pre className="custom-scrollbar max-h-[300px] max-w-full overflow-auto rounded-lg bg-card p-3 font-mono text-[11px] leading-relaxed text-text-main">
      {children}
    </pre>
  )
}

const PAGE_SIZES = [10, 20, 50, 100]

const iconBtn =
  'flex size-7 cursor-pointer items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-2 hover:text-text-main disabled:pointer-events-none disabled:opacity-30'

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
  const fieldCls =
    'h-9 w-full min-w-0 cursor-pointer rounded-lg border-0 bg-surface-2 px-3 text-sm text-text-main focus:outline-none focus:ring-1 focus:ring-ring'
  const labelCls = 'text-[11px] font-medium uppercase tracking-wide text-text-muted'

  const from = pagination.totalItems ? (pagination.page - 1) * pagination.pageSize + 1 : 0
  const to = Math.min(pagination.page * pagination.pageSize, pagination.totalItems)

  return (
    <div className="flex min-w-0 flex-col gap-2">
      <section className="flex min-w-0 flex-col overflow-hidden rounded-xl bg-surface">
        <div className="flex items-center gap-2 px-4 py-3">
          <Icon name="tune" className="text-[15px] text-text-muted" />
          <h2 className="text-sm font-semibold text-text-main">Filters</h2>
          <button
            type="button"
            onClick={() => setFilters({ provider: '', startDate: '', endDate: '' })}
            disabled={!hasFilters}
            className="ml-auto cursor-pointer rounded-md px-2 py-1 text-[11px] font-medium text-text-muted transition-colors hover:bg-surface-2 hover:text-text-main disabled:pointer-events-none disabled:opacity-40"
          >
            Clear
          </button>
        </div>
        <div className="grid grid-cols-1 gap-3 border-t border-border px-4 py-4 sm:grid-cols-2 lg:grid-cols-3">
          <label className="flex min-w-0 flex-col gap-1.5">
            <span className={labelCls}>Provider</span>
            <select
              value={filters.provider}
              onChange={(e) => setFilters({ ...filters, provider: e.target.value })}
              className={fieldCls}
            >
              <option value="">All Providers</option>
              {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
          </label>
          <label className="flex min-w-0 flex-col gap-1.5">
            <span className={labelCls}>Start date</span>
            <input
              type="datetime-local" value={filters.startDate}
              onChange={(e) => setFilters({ ...filters, startDate: e.target.value })}
              className={fieldCls}
            />
          </label>
          <label className="flex min-w-0 flex-col gap-1.5">
            <span className={labelCls}>End date</span>
            <input
              type="datetime-local" value={filters.endDate}
              onChange={(e) => setFilters({ ...filters, endDate: e.target.value })}
              className={fieldCls}
            />
          </label>
        </div>
      </section>

      <section className="flex min-w-0 flex-col overflow-hidden rounded-xl bg-surface">
        <div className="flex items-center gap-2 px-4 py-3">
          <Icon name="receipt_long" className="text-[15px] text-text-muted" />
          <h2 className="text-sm font-semibold text-text-main">Request details</h2>
          <span className="ml-auto font-mono text-[11px] text-text-muted">{fmt(pagination.totalItems)}</span>
        </div>

        <div className="min-w-0 px-2 pb-2">
          <div className="min-w-0 overflow-x-auto">
            <table className="w-full min-w-[900px] text-left">
              <thead className="text-[11px] uppercase tracking-wide text-text-muted">
                <tr className="border-b border-border">
                  <th className="whitespace-nowrap px-3 py-2 font-medium">Timestamp</th>
                  <th className="px-3 py-2 font-medium">Model</th>
                  <th className="px-3 py-2 font-medium">Provider</th>
                  <th className="px-3 py-2 text-right font-medium">Input</th>
                  <th className="px-3 py-2 text-right font-medium">Cached</th>
                  <th className="px-3 py-2 text-right font-medium">Cache write</th>
                  <th className="px-3 py-2 text-right font-medium">Output</th>
                  <th className="px-3 py-2 font-medium">Latency</th>
                  <th className="px-3 py-2" />
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {loading ? (
                  <tr>
                    <td colSpan={9} className="p-2">
                      <div className="flex items-center justify-center gap-2 rounded-lg bg-surface-2 py-10 text-xs text-text-muted">
                        <Icon name="progress_activity" className="animate-spin text-[16px]" />
                        Loading requests…
                      </div>
                    </td>
                  </tr>
                ) : details.length === 0 ? (
                  <tr>
                    <td colSpan={9} className="p-2">
                      <div className="flex flex-col items-center justify-center gap-2 rounded-lg bg-surface-2 py-10 text-center text-xs text-text-muted">
                        <Icon name="receipt_long" className="text-[24px] text-text-muted/60" />
                        <span>No request details found for this range.</span>
                      </div>
                    </td>
                  </tr>
                ) : details.map((d, i) => {
                  const t = tokens(d.tokens)
                  return (
                    <tr key={`${d.id}-${i}`} className="group transition-colors hover:bg-surface-2">
                      <td className="whitespace-nowrap px-3 py-2.5 font-mono text-[11px] text-text-muted">
                        {new Date(d.timestamp).toLocaleString()}
                      </td>
                      <td className="max-w-[220px] truncate px-3 py-2.5 font-mono text-xs text-text-main" title={d.model}>
                        {d.model}
                      </td>
                      <td className="max-w-[160px] truncate px-3 py-2.5 text-xs text-text-main" title={nameById[d.provider] || d.provider}>
                        {nameById[d.provider] || d.provider}
                      </td>
                      <td className="px-3 py-2.5 text-right font-mono text-[11px] tabular-nums text-text-main">{fmt(inputOf(t))}</td>
                      <td className="px-3 py-2.5 text-right font-mono text-[11px] tabular-nums text-text-muted">{cachedOf(t) > 0 ? fmt(cachedOf(t)) : '—'}</td>
                      <td className="px-3 py-2.5 text-right font-mono text-[11px] tabular-nums text-text-muted">{cacheCreateOf(t) > 0 ? fmt(cacheCreateOf(t)) : '—'}</td>
                      <td className="px-3 py-2.5 text-right font-mono text-[11px] tabular-nums text-text-main">{fmt(outputOf(t))}</td>
                      <td className="whitespace-nowrap px-3 py-2.5 font-mono text-[10px] leading-tight text-text-muted">
                        <div>TTFT {d.latency?.ttft || 0}ms</div>
                        <div>Total {d.latency?.total || 0}ms</div>
                      </td>
                      <td className="px-3 py-2.5 text-right">
                        <button
                          type="button"
                          onClick={() => setSelected(d)}
                          aria-label="Open request details"
                          className={cn(iconBtn, 'ml-auto')}
                        >
                          <Icon name="arrow_forward" className="text-[15px]" />
                        </button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </div>

        {!loading && details.length > 0 && (
          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border px-4 py-3 text-xs text-text-muted">
            <div className="flex items-center gap-2">
              <span>Rows</span>
              <select
                value={pagination.pageSize}
                onChange={(e) => setPagination((p) => ({ ...p, pageSize: Number(e.target.value), page: 1 }))}
                className="h-7 cursor-pointer rounded-md border-0 bg-surface-2 px-2 font-mono text-[11px] text-text-main focus:outline-none"
              >
                {PAGE_SIZES.map((n) => <option key={n} value={n}>{n}</option>)}
              </select>
              <span className="font-mono text-[11px]">{from}–{to} of {fmt(pagination.totalItems)}</span>
            </div>
            <div className="flex items-center gap-1">
              <button
                type="button" aria-label="Previous page" disabled={pagination.page <= 1}
                onClick={() => setPagination((p) => ({ ...p, page: p.page - 1 }))}
                className={iconBtn}
              >
                <Icon name="chevron_left" className="text-[16px]" />
              </button>
              <span className="px-1 font-mono text-[11px]">{pagination.page} / {Math.max(1, pagination.totalPages)}</span>
              <button
                type="button" aria-label="Next page" disabled={pagination.page >= pagination.totalPages}
                onClick={() => setPagination((p) => ({ ...p, page: p.page + 1 }))}
                className={iconBtn}
              >
                <Icon name="chevron_right" className="text-[16px]" />
              </button>
            </div>
          </div>
        )}
      </section>

      <Modal isOpen={!!selected} onClose={() => setSelected(null)} title="Request details" size="full">
        {selected && <DetailBody detail={selected} nameById={nameById} />}
      </Modal>
    </div>
  )
}

function DetailBody({ detail, nameById }: { detail: RequestDetail; nameById: Record<string, string> }) {
  const t = tokens(detail.tokens)
  const response = (detail.response || {}) as { content?: string; thinking?: string; error?: string }
  const rows: [string, React.ReactNode][] = [
    ['ID', <span className="break-all font-mono text-[11px]">{detail.id}</span>],
    ['Timestamp', new Date(detail.timestamp).toLocaleString()],
    ['Provider', nameById[detail.provider] || detail.provider],
    ['Model', <span className="font-mono text-[11px]">{detail.model}</span>],
    [
      'Status',
      <span className={detail.status === 'success' ? 'font-medium text-success' : 'font-medium text-danger'}>
        {detail.status}
      </span>,
    ],
    ['Latency', <span className="font-mono text-[11px]">TTFT {detail.latency?.ttft || 0}ms · total {detail.latency?.total || 0}ms</span>],
    ['Input tokens', <span className="font-mono text-[11px] tabular-nums">{fmt(inputOf(t))}</span>],
  ]
  if (cachedOf(t) > 0) rows.push(['Cached tokens', <span className="font-mono text-[11px] tabular-nums">{fmt(cachedOf(t))}</span>])
  if (cacheCreateOf(t) > 0) rows.push(['Cache write', <span className="font-mono text-[11px] tabular-nums">{fmt(cacheCreateOf(t))}</span>])
  rows.push(['Output tokens', <span className="font-mono text-[11px] tabular-nums">{fmt(outputOf(t))}</span>])

  const pxpipe = detail.pxpipe

  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-1 gap-x-6 gap-y-3 rounded-xl bg-surface-2 p-4 sm:grid-cols-2">
        {rows.map(([k, v]) => (
          <div key={k} className="flex min-w-0 flex-col gap-0.5">
            <span className="text-[10px] font-medium uppercase tracking-wide text-text-muted">{k}</span>
            <span className="min-w-0 text-xs text-text-main">{v}</span>
          </div>
        ))}
      </div>

      {pxpipe && (
        <div className="rounded-xl bg-surface-2 p-4">
          <div className="mb-3 flex items-center gap-2">
            <Icon name="image" className="text-[15px] text-text-muted" />
            <span className="text-xs font-semibold text-text-main">PXPIPE</span>
            <span className="ml-auto rounded-md bg-card px-2 py-0.5 text-[10px] font-medium text-text-muted shadow-[0_1px_2px_rgba(0,0,0,0.06)]">
              {pxpipe.applied ? 'Activated' : 'Skipped'}
            </span>
          </div>
          {pxpipe.applied ? (
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              {[
                ['Original (est.)', `${fmt(pxpipe.tokensBeforeEst)} tokens`],
                ['Compressed (est.)', `${fmt(pxpipe.tokensAfterEst)} tokens`],
                ['Saved', `${pxpipe.savedPct || 0}%`],
                ['Images', `${pxpipe.imageCount || 0} · ${pxpipe.durationMs || 0}ms`],
              ].map(([k, v]) => (
                <div key={k} className="flex min-w-0 flex-col gap-0.5">
                  <span className="text-[10px] font-medium uppercase tracking-wide text-text-muted">{k}</span>
                  <span className="font-mono text-[11px] text-text-main">{v}</span>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-xs text-text-muted">
              Reason: <span className="font-mono text-[11px] text-text-main">{pxpipe.reason}</span>
              {pxpipe.detail ? ` — ${pxpipe.detail}` : ''}
            </p>
          )}
        </div>
      )}

      <div className="flex flex-col gap-2">
        <CollapsibleSection title="1. Client request (input)" defaultOpen icon="input">
          <Pre>{JSON.stringify(detail.request, null, 2)}</Pre>
        </CollapsibleSection>
        {!!detail.providerRequest && (
          <CollapsibleSection title="2. Provider request (translated)" icon="translate">
            <Pre>{JSON.stringify(detail.providerRequest, null, 2)}</Pre>
          </CollapsibleSection>
        )}
        {!!detail.providerResponse && (
          <CollapsibleSection title="3. Provider response (raw)" icon="data_object">
            <Pre>{typeof detail.providerResponse === 'object' ? JSON.stringify(detail.providerResponse, null, 2) : String(detail.providerResponse)}</Pre>
          </CollapsibleSection>
        )}
        <CollapsibleSection title="4. Client response (final)" defaultOpen icon="output">
          {response.thinking && (
            <div className="mb-3">
              <h4 className="mb-1.5 flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-wide text-text-muted">
                <Icon name="psychology" className="text-[14px]" />
                Thinking process
              </h4>
              <pre className="custom-scrollbar max-h-[200px] max-w-full overflow-auto rounded-lg bg-card p-3 font-mono text-[11px] leading-relaxed text-text-main">
                {response.thinking}
              </pre>
            </div>
          )}
          <h4 className="mb-1.5 text-[10px] font-medium uppercase tracking-wide text-text-muted">Content</h4>
          <Pre>{response.content || response.error || '[No content]'}</Pre>
        </CollapsibleSection>
      </div>
    </div>
  )
}
