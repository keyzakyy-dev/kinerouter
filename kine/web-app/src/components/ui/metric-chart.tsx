import { useId } from 'react'
import { Area, AreaChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

export type SeriesPoint = { date: string; value: number }

export const ACCENTS = {
  emerald: 'var(--metric-emerald)',
  rose: 'var(--metric-rose)',
  amber: 'var(--metric-amber)',
  sky: 'var(--metric-sky)',
  violet: 'var(--metric-violet)',
} as const

export type MetricAccent = keyof typeof ACCENTS

/** 1234 → 1.2K, 1234567 → 1.2M. */
export function formatCompact(n: number): string {
  const abs = Math.abs(n)
  if (abs >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(1)}B`
  if (abs >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (abs >= 1000) return `${(n / 1000).toFixed(1)}K`
  return new Intl.NumberFormat().format(n)
}

export function MetricChart({
  data,
  color,
  valueFormatter,
}: {
  data: SeriesPoint[]
  color: string
  valueFormatter?: (value: number) => string
}) {
  const uid = useId().replace(/:/g, '')
  // Fixed domain: Recharts recalcs dataMin/dataMax every animation frame, which
  // makes the line jump while animating. Pin it from the data instead.
  const values = data.map((d) => d.value)
  const domain: [number, number] = [Math.min(...values, 0), Math.max(...values, 1)]

  return (
    <ResponsiveContainer width="100%" height="100%">
      <AreaChart data={data} margin={{ top: 2, right: 0, left: 0, bottom: 0 }}>
        <defs>
          <linearGradient id={uid} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity={0.16} />
            <stop offset="100%" stopColor={color} stopOpacity={0} />
          </linearGradient>
        </defs>
        <XAxis dataKey="date" hide />
        <YAxis hide domain={domain} />
        <Tooltip
          contentStyle={{
            backgroundColor: 'var(--card)',
            border: 'none',
            borderRadius: 8,
            boxShadow: '0 4px 16px rgba(0,0,0,0.10)',
            fontSize: 11,
            padding: '5px 8px',
          }}
          labelStyle={{ color: 'var(--muted-foreground)', marginBottom: 1 }}
          itemStyle={{ color: 'var(--foreground)' }}
          cursor={{ stroke: 'var(--border)' }}
          formatter={(value) => [
            valueFormatter ? valueFormatter(Number(value ?? 0)) : formatCompact(Number(value ?? 0)),
            '',
          ]}
        />
        <Area
          type="monotone"
          dataKey="value"
          stroke={color}
          strokeWidth={1.5}
          fill={`url(#${uid})`}
          dot={false}
          isAnimationActive
          animationDuration={1400}
          animationEasing="ease-out"
          activeDot={{ r: 3, strokeWidth: 1.5, stroke: 'var(--card)' }}
        />
      </AreaChart>
    </ResponsiveContainer>
  )
}
