import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { Icon } from '@/components/icon'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { cn } from '@/lib/utils'

const fmtTokens = (n: number) => {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`
  return String(n || 0)
}

const fmtUptime = (ms: number) => {
  if (!ms || ms <= 0) return '-'
  const m = Math.floor(ms / 60000)
  const h = Math.floor(m / 60)
  return h > 0 ? `${h}h${String(m % 60).padStart(2, '0')}m` : `${m}m`
}

const WINDOW_TABS = [
  { id: 'today', label: 'Today' },
  { id: 'yesterday', label: 'Yesterday' },
  { id: 'last7d', label: '7 days' },
  { id: 'last30d', label: '30 days' },
  { id: 'all', label: 'All time' },
]

const REASON_LABELS: Record<string, string> = {
  applied: 'Prompt exceeded threshold',
  below_threshold: 'Below size threshold',
  not_profitable: 'Compression not profitable',
  below_min_chars: 'Below minimum chars',
  below_min_tokens: 'Below minimum tokens',
  unsupported_model: 'Model not in allowlist',
  unsupported_format: 'Non-Claude request format',
  timeout: 'Compression timed out',
  transform_error: 'Transform error',
  passthrough: 'Passthrough',
  disabled: 'Disabled',
  not_installed: 'Not installed',
}

type Window = {
  requests: number
  compressed: number
  bypassed: number
  tokensBeforeEst: number
  tokensAfterEst: number
  tokensSavedEst: number
  savedPct: number
  imagesGenerated: number
  avgCompressionMs: number
  errors: number
}

type PxStatus = {
  installed: boolean
  enabled: boolean
  running: boolean
  version: string | null
  uptimeMs: number
}

type PxEvent = {
  ts: string | number
  provider?: string
  model?: string
  applied: boolean
  reason?: string
  detail?: string
  tokensBeforeEst: number
  tokensAfterEst: number
  tokensSavedEst: number
  savedPct: number
  durationMs?: number | null
}

type PxStats = {
  windows?: Record<string, Window>
  timeline?: { date: string; tokensSavedEst: number }[]
  recent?: PxEvent[]
}

function SummaryCard({
  label,
  value,
  sub,
  tone,
}: {
  label: string
  value: string
  sub?: string
  tone?: string
}) {
  return (
    <Card className="p-4">
      <p className="text-xs uppercase tracking-wide text-text-muted">{label}</p>
      <p className={cn('mt-1 text-xl font-semibold', tone)}>{value}</p>
      {sub && <p className="mt-0.5 text-xs text-text-muted">{sub}</p>}
    </Card>
  )
}

export default function PxpipePage() {
  const [status, setStatus] = useState<PxStatus | null>(null)
  const [health, setHealth] = useState<{ healthy?: boolean } | null>(null)
  const [stats, setStats] = useState<PxStats | null>(null)
  const [logs, setLogs] = useState<{ installLog?: string } | null>(null)
  const [windowId, setWindowId] = useState('last7d')
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const [statusRes, statsRes, logsRes] = await Promise.all([
        fetch('/api/pxpipe/status', { headers: { 'Cache-Control': 'no-store' } }),
        fetch('/api/pxpipe/stats'),
        fetch('/api/pxpipe/logs?limit=50'),
      ])
      setStatus(await statusRes.json())
      setStats(await statsRes.json())
      setLogs(await logsRes.json())
      const healthRes = await fetch('/api/pxpipe/health', { method: 'POST' })
      setHealth(await healthRes.json())
    } catch {
      // Each section renders its own placeholder.
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  const w = stats?.windows?.[windowId]
  const statusLabel = !status
    ? '-'
    : !status.installed
      ? 'Not installed'
      : health?.healthy
        ? 'Healthy'
        : status.running
          ? 'Running'
          : 'Stopped'

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-lg font-semibold text-text-main">
          <Icon name="image" className="text-[22px] text-primary" />
          PXPIPE Dashboard
        </h2>
        <div className="flex items-center gap-2">
          <Link
            to="/dashboard/token-saver"
            className="text-xs text-primary underline hover:opacity-80"
          >
            Token Saver settings
          </Link>
          <Button size="sm" variant="ghost" onClick={refresh} disabled={loading}>
            {loading ? 'Refreshing…' : 'Refresh'}
          </Button>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6">
        <SummaryCard
          label="Status"
          value={statusLabel}
          tone={
            health?.healthy
              ? 'text-success'
              : status?.installed
                ? 'text-warning'
                : 'text-text-muted'
          }
          sub={status?.enabled ? 'Enabled in pipeline' : 'Disabled in pipeline'}
        />
        <SummaryCard
          label="Version"
          value={status?.version ? `v${status.version}` : '-'}
          sub="pxpipe-proxy"
        />
        <SummaryCard label="Uptime" value={fmtUptime(status?.uptimeMs ?? 0)} sub="module loaded" />
        <SummaryCard label="Requests" value={w ? w.requests.toLocaleString() : '-'} />
        <SummaryCard
          label="Compressed"
          value={w ? w.compressed.toLocaleString() : '-'}
          tone="text-success"
        />
        <SummaryCard label="Bypassed" value={w ? w.bypassed.toLocaleString() : '-'} />
      </div>

      <Card className="p-4">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <h3 className="font-medium text-text-main">Token savings (estimated)</h3>
          <div className="flex items-center gap-1 rounded-lg border border-border bg-surface-2 p-1">
            {WINDOW_TABS.map((tab) => (
              <button
                key={tab.id}
                onClick={() => setWindowId(tab.id)}
                className={cn(
                  'rounded-md px-3 py-1 text-xs font-medium transition-colors',
                  windowId === tab.id
                    ? 'bg-primary text-primary-foreground shadow-sm'
                    : 'text-text-muted hover:bg-surface-3 hover:text-text-main',
                )}
              >
                {tab.label}
              </button>
            ))}
          </div>
        </div>
        <div className="grid grid-cols-2 gap-4 text-center md:grid-cols-4">
          <div>
            <p className="text-xs text-text-muted">Original tokens</p>
            <p className="text-lg font-semibold text-text-main">
              {w ? fmtTokens(w.tokensBeforeEst) : '-'}
            </p>
          </div>
          <div>
            <p className="text-xs text-text-muted">After PXPIPE</p>
            <p className="text-lg font-semibold text-text-main">
              {w ? fmtTokens(w.tokensAfterEst) : '-'}
            </p>
          </div>
          <div>
            <p className="text-xs text-text-muted">Saved</p>
            <p className="text-lg font-semibold text-success">
              {w ? fmtTokens(w.tokensSavedEst) : '-'}
            </p>
          </div>
          <div>
            <p className="text-xs text-text-muted">Reduction</p>
            <p className="text-lg font-semibold text-success">{w ? `${w.savedPct}%` : '-'}</p>
          </div>
        </div>
        <p className="mt-3 text-xs text-text-muted">
          Estimates from body size before/after imaging; billed usage per request (recorded on the
          Usage page) remains the ground truth. Images generated:{' '}
          {w ? w.imagesGenerated.toLocaleString() : '-'} · avg compression time:{' '}
          {w ? `${w.avgCompressionMs}ms` : '-'} · errors: {w ? w.errors : '-'}
        </p>
      </Card>

      <Card className="p-4">
        <h3 className="mb-3 font-medium text-text-main">Tokens saved, last 30 days</h3>
        {stats?.timeline?.some((d) => d.tokensSavedEst > 0) ? (
          <ResponsiveContainer width="100%" height={220}>
            <AreaChart data={stats.timeline} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
              <defs>
                <linearGradient id="gradPxpipe" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--chart-3)" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="var(--chart-3)" stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid strokeDasharray="3 3" strokeOpacity={0.2} />
              <XAxis
                dataKey="date"
                tick={{ fontSize: 11 }}
                tickFormatter={(d: string) => d.slice(5)}
              />
              <YAxis tick={{ fontSize: 11 }} tickFormatter={fmtTokens} width={48} />
              <Tooltip
                formatter={(v) => [fmtTokens(Number(v)), 'Tokens saved']}
                labelFormatter={(d) => String(d)}
              />
              <Area
                type="monotone"
                dataKey="tokensSavedEst"
                stroke="var(--chart-3)"
                fill="url(#gradPxpipe)"
                strokeWidth={2}
              />
            </AreaChart>
          </ResponsiveContainer>
        ) : (
          <div className="flex h-32 items-center justify-center text-sm text-text-muted">
            No savings recorded yet. Enable PXPIPE in the Token Saver and route a large
            Claude-format request.
          </div>
        )}
      </Card>

      <Card className="p-4">
        <h3 className="mb-3 font-medium text-text-main">History</h3>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs text-text-muted">
                <th className="py-2 pr-3">Time</th>
                <th className="py-2 pr-3">Model</th>
                <th className="py-2 pr-3 text-right">Original</th>
                <th className="py-2 pr-3 text-right">Compressed</th>
                <th className="py-2 pr-3 text-right">Saved</th>
                <th className="py-2 pr-3 text-right">%</th>
                <th className="py-2 pr-3 text-right">Duration</th>
                <th className="py-2">Status</th>
              </tr>
            </thead>
            <tbody>
              {(stats?.recent ?? []).slice(0, 50).map((ev, i) => (
                <tr key={`${ev.ts}-${i}`} className="border-b border-border/50">
                  <td className="whitespace-nowrap py-1.5 pr-3 text-text-muted">
                    {new Date(ev.ts).toLocaleString()}
                  </td>
                  <td className="py-1.5 pr-3 font-mono text-xs">
                    {ev.provider ? `${ev.provider}/${ev.model}` : ev.model || '-'}
                  </td>
                  <td className="py-1.5 pr-3 text-right font-mono text-xs">
                    {ev.applied ? fmtTokens(ev.tokensBeforeEst) : '-'}
                  </td>
                  <td className="py-1.5 pr-3 text-right font-mono text-xs">
                    {ev.applied ? fmtTokens(ev.tokensAfterEst) : '-'}
                  </td>
                  <td className="py-1.5 pr-3 text-right font-mono text-xs text-success">
                    {ev.applied ? fmtTokens(ev.tokensSavedEst) : '-'}
                  </td>
                  <td className="py-1.5 pr-3 text-right font-mono text-xs">
                    {ev.applied ? `${ev.savedPct}%` : '-'}
                  </td>
                  <td className="py-1.5 pr-3 text-right font-mono text-xs">
                    {ev.durationMs != null ? `${ev.durationMs}ms` : '-'}
                  </td>
                  <td className="py-1.5">
                    <span
                      title={ev.detail || ''}
                      className={cn(
                        'rounded px-2 py-0.5 text-xs',
                        ev.applied
                          ? 'bg-success/15 text-success'
                          : ev.reason === 'transform_error' || ev.reason === 'timeout'
                            ? 'bg-danger/15 text-danger'
                            : 'bg-warning/15 text-warning',
                      )}
                    >
                      {ev.applied ? 'Compressed' : (REASON_LABELS[ev.reason ?? ''] ?? ev.reason)}
                    </span>
                  </td>
                </tr>
              ))}
              {(!stats?.recent || stats.recent.length === 0) && (
                <tr>
                  <td colSpan={8} className="py-6 text-center text-sm text-text-muted">
                    No PXPIPE activity yet
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </Card>

      <Card className="p-4" id="logs">
        <h3 className="mb-3 font-medium text-text-main">PXPIPE Logs</h3>
        {logs?.installLog ? (
          <pre className="max-h-64 overflow-x-auto overflow-y-auto whitespace-pre-wrap rounded bg-black/5 p-3 font-mono text-xs dark:bg-white/5">
            {logs.installLog}
          </pre>
        ) : (
          <p className="text-sm text-text-muted">No install log yet.</p>
        )}
      </Card>
    </div>
  )
}
