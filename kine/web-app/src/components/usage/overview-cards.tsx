import { useEffect, useState } from 'react'
import ProgressMetricCard from '@/components/ui/progress-metric-card'
import type { MetricAccent } from '@/components/ui/metric-chart'
import { api, type UsageChartPoint, type UsageStats } from '@/lib/api'
import { fmt, fmtTokens } from './helpers'

/** Four period summary cards: count + trend, nothing else. */
export function OverviewCards({ stats, period }: { stats: UsageStats; period: string }) {
  const [points, setPoints] = useState<UsageChartPoint[]>([])

  useEffect(() => {
    let cancelled = false
    api.usageChart(period)
      .then((rows) => { if (!cancelled) setPoints(rows) })
      .catch(() => { if (!cancelled) setPoints([]) })
    return () => { cancelled = true }
  }, [period])

  const realSeries = (key: 'requests' | 'tokens' | 'cost') =>
    points.map((p) => ({ date: p.label, value: p[key] || 0 }))

  const cards: {
    title: string
    value: number
    format: (n: number) => string
    data: { date: string; value: number }[]
    accent: MetricAccent
  }[] = [
    {
      title: 'Total requests',
      value: stats.totalRequests,
      format: fmt,
      data: realSeries('requests'),
      accent: 'violet',
    },
    {
      title: 'Input tokens',
      value: stats.totalPromptTokens,
      format: fmtTokens,
      data: realSeries('tokens'),
      accent: 'sky',
    },
    {
      title: 'Output tokens',
      value: stats.totalCompletionTokens,
      format: fmtTokens,
      data: realSeries('tokens'),
      accent: 'emerald',
    },
    {
      title: 'Est. cost',
      value: stats.totalCost || 0,
      format: (n) => `~$${n.toFixed(2)}`,
      data: realSeries('cost'),
      accent: 'amber',
    },
  ]

  return (
    <div className="grid min-w-0 grid-cols-2 gap-3 lg:grid-cols-4">
      {cards.map((c, i) => (
        <ProgressMetricCard
          key={c.title}
          title={c.title}
          value={c.value}
          valueFormatter={c.format}
          data={c.data}
          accent={c.accent}
          delay={i * 120}
        />
      ))}
    </div>
  )
}
