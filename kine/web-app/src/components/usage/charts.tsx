import { useEffect, useMemo, useState, type ReactNode } from 'react'
import {
  Area, AreaChart, Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis,
} from 'recharts'
import type { UsageChartPoint, UsageStat } from '@/lib/api'
import { Icon } from '@/components/icon'
import { Segmented, fmt, fmtTokens } from './helpers'

const fmtCost4 = (n: number | null | undefined) => `$${(n || 0).toFixed(4)}`

const CHART_COLORS = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)', 'var(--color-text-muted)', 'var(--color-text-subtle)']

const TOOLTIP_STYLE = {
  backgroundColor: 'var(--card)',
  border: '1px solid var(--border)',
  borderRadius: 8,
  fontSize: 12,
  color: 'var(--foreground)',
}

const AXIS_TICK = { fontSize: 10, fill: 'var(--color-text-subtle)' }

/** Empty panel: inset ring + dimmed surface so it reads as a slot, not a card. */
function ChartEmpty({ icon, children }: { icon: string; children: ReactNode }) {
  return (
    <div className="flex h-56 flex-col items-center justify-center gap-3 rounded-xl bg-surface-2/60 ring-1 ring-inset ring-border-subtle">
      <span className="flex h-10 w-10 items-center justify-center rounded-full bg-brand-500/10 text-brand-500">
        <Icon name={icon} className="text-[20px]" />
      </span>
      <span className="max-w-[240px] text-center text-xs leading-relaxed text-text-muted">{children}</span>
    </div>
  )
}

type ChartMode = 'tokens' | 'requests' | 'cost'

const CHART_VIEW = {
  tokens: { dataKey: 'tokens', color: 'var(--chart-1)', gradId: 'gradTokens', formatter: fmtTokens, label: 'Tokens' },
  requests: { dataKey: 'requests', color: 'var(--chart-3)', gradId: 'gradRequests', formatter: fmt, label: 'Requests' },
  cost: { dataKey: 'cost', color: 'var(--chart-4)', gradId: 'gradCost', formatter: fmtCost4, label: 'Cost' },
} as const

/** Token / request / cost area chart over the selected period. */
export function UsageChart({ period }: { period: string }) {
  const [data, setData] = useState<UsageChartPoint[]>([])
  const [loading, setLoading] = useState(true)
  const [mode, setMode] = useState<ChartMode>('tokens')

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    fetch(`/api/usage/chart?period=${period}`, { credentials: 'include' })
      .then((r) => (r.ok ? r.json() : []))
      .then((json: UsageChartPoint[]) => { if (!cancelled) setData(json) })
      .catch(() => { if (!cancelled) setData([]) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [period])

  const cfg = CHART_VIEW[mode]
  const hasData = data.some((d) => (d[cfg.dataKey] || 0) > 0)

  return (
    <section className="flex min-w-0 flex-col overflow-hidden rounded-xl bg-surface">
      <div className="flex flex-wrap items-center gap-2 px-4 py-3">
        <Icon name="monitoring" className="text-[15px] text-text-muted" />
        <h2 className="text-sm font-semibold text-text-main">Usage over time</h2>
        <Segmented
          className="ml-auto"
          size="sm"
          options={[
            { value: 'tokens' as ChartMode, label: 'Tokens' },
            { value: 'requests' as ChartMode, label: 'Requests' },
            { value: 'cost' as ChartMode, label: 'Cost' },
          ]}
          value={mode}
          onChange={setMode}
        />
      </div>
      <div className="min-w-0 px-2 pb-2">
      {loading ? (
        <div className="flex h-56 items-center justify-center text-xs text-text-muted">Loading…</div>
      ) : !hasData ? (
        <ChartEmpty icon="monitoring">No {cfg.label.toLowerCase()} recorded for this period.</ChartEmpty>
      ) : (
        <ResponsiveContainer width="100%" height={220}>
          <AreaChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
            <defs>
              {Object.values(CHART_VIEW).map((c) => (
                <linearGradient key={c.gradId} id={c.gradId} x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor={c.color} stopOpacity={0.25} />
                  <stop offset="95%" stopColor={c.color} stopOpacity={0} />
                </linearGradient>
              ))}
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" strokeOpacity={0.8} />
            <XAxis
              dataKey="label"
              tick={AXIS_TICK}
              tickLine={false}
              axisLine={false}
              interval="preserveStartEnd"
            />
            <YAxis
              tick={AXIS_TICK}
              tickLine={false}
              axisLine={false}
              tickFormatter={cfg.formatter}
              width={50}
            />
            <Tooltip
              contentStyle={TOOLTIP_STYLE}
              formatter={(value) => [cfg.formatter(Number(value)), cfg.label]}
            />
            <Area
              type="monotone"
              dataKey={cfg.dataKey}
              stroke={cfg.color}
              strokeWidth={2}
              fill={`url(#${cfg.gradId})`}
              dot={false}
              activeDot={{ r: 4 }}
            />
          </AreaChart>
        </ResponsiveContainer>
      )}
      </div>
    </section>
  )
}

/** Horizontal-bar breakdown of usage by provider. */
export function ProviderBarChart({ byProvider }: { byProvider: Record<string, UsageStat> | undefined }) {
  const [mode, setMode] = useState<'tokens' | 'requests'>('tokens')
  const chartData = useMemo(() => {
    if (!byProvider) return []
    return Object.entries(byProvider)
      .map(([id, d]) => ({
        name: id,
        tokens: (d.promptTokens || 0) + (d.completionTokens || 0),
        requests: d.requests || 0,
      }))
      .filter((d) => d[mode] > 0)
      .sort((a, b) => b[mode] - a[mode])
  }, [byProvider, mode])

  const f = mode === 'tokens' ? fmtTokens : (v: number) => String(v || 0)

  return (
    <section className="flex min-w-0 flex-col overflow-hidden rounded-xl bg-surface">
      <div className="flex flex-wrap items-center gap-2 px-4 py-3">
        <Icon name="bar_chart" className="text-[15px] text-text-muted" />
        <h2 className="text-sm font-semibold text-text-main">By provider</h2>
        <Segmented
          className="ml-auto"
          size="sm"
          options={[
            { value: 'tokens' as const, label: 'Tokens' },
            { value: 'requests' as const, label: 'Requests' },
          ]}
          value={mode}
          onChange={setMode}
        />
      </div>
      <div className="min-w-0 px-2 pb-2">
      {chartData.length === 0 ? (
        <ChartEmpty icon="bar_chart">No provider usage for this period.</ChartEmpty>
      ) : (
        <ResponsiveContainer width="100%" height={180}>
          <BarChart data={chartData} margin={{ top: 4, right: 8, left: 0, bottom: 16 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" strokeOpacity={0.8} vertical={false} />
            <XAxis
              dataKey="name"
              tick={AXIS_TICK}
              tickLine={false}
              axisLine={false}
              interval={0}
              tickFormatter={(v: string) => (v.length > 10 ? `${v.slice(0, 10)}…` : v)}
            />
            <YAxis
              tick={AXIS_TICK}
              tickLine={false}
              axisLine={false}
              tickFormatter={f}
              width={44}
            />
            <Tooltip
              contentStyle={TOOLTIP_STYLE}
              formatter={(value) => [f(Number(value)), mode === 'tokens' ? 'Tokens' : 'Requests']}
            />
            <Bar dataKey={mode} radius={[4, 4, 0, 0]}>
              {chartData.map((_, i) => (
                <Cell key={i} fill={CHART_COLORS[i % CHART_COLORS.length]} fillOpacity={0.85} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      )}
      </div>
    </section>
  )
}

const truncate = (s: string | undefined, max = 22) => (s && s.length > max ? `${s.slice(0, max)}…` : s || '')

/** Vertical-bar breakdown of the top five models. */
export function TopModelsChart({ byModel }: { byModel: Record<string, UsageStat> | undefined }) {
  const [mode, setMode] = useState<'tokens' | 'requests'>('tokens')
  const chartData = useMemo(() => {
    if (!byModel) return []
    return Object.values(byModel)
      .map((d) => ({
        name: truncate(d.rawModel || 'Unknown'),
        tokens: (d.promptTokens || 0) + (d.completionTokens || 0),
        requests: d.requests || 0,
      }))
      .filter((d) => d[mode] > 0)
      .sort((a, b) => b[mode] - a[mode])
      .slice(0, 5)
  }, [byModel, mode])

  const f = mode === 'tokens' ? fmtTokens : (v: number) => String(v || 0)

  return (
    <section className="flex min-w-0 flex-col overflow-hidden rounded-xl bg-surface">
      <div className="flex flex-wrap items-center gap-2 px-4 py-3">
        <Icon name="smart_toy" className="text-[15px] text-text-muted" />
        <h2 className="text-sm font-semibold text-text-main">Top models</h2>
        <Segmented
          className="ml-auto"
          size="sm"
          options={[
            { value: 'tokens' as const, label: 'Tokens' },
            { value: 'requests' as const, label: 'Requests' },
          ]}
          value={mode}
          onChange={setMode}
        />
      </div>
      <div className="min-w-0 px-2 pb-2">
      {chartData.length === 0 ? (
        <ChartEmpty icon="smart_toy">No model usage for this period.</ChartEmpty>
      ) : (
        <ResponsiveContainer width="100%" height={180}>
          <BarChart data={chartData} layout="vertical" margin={{ top: 4, right: 40, left: 4, bottom: 16 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" strokeOpacity={0.8} horizontal={false} />
            <XAxis
              type="number"
              tick={AXIS_TICK}
              tickLine={false}
              axisLine={false}
              tickFormatter={f}
            />
            <YAxis
              type="category"
              dataKey="name"
              tick={AXIS_TICK}
              tickLine={false}
              axisLine={false}
              width={90}
            />
            <Tooltip
              contentStyle={TOOLTIP_STYLE}
              formatter={(value) => [f(Number(value)), mode === 'tokens' ? 'Tokens' : 'Requests']}
            />
            <Bar dataKey={mode} radius={[0, 4, 4, 0]}>
              {chartData.map((_, i) => (
                <Cell key={i} fill={CHART_COLORS[i % 5]} fillOpacity={0.85} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      )}
      </div>
    </section>
  )
}
