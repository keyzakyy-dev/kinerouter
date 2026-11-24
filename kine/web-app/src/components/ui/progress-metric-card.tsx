import { useEffect, useState } from 'react'
import { MetricChart, formatCompact, type MetricAccent, type SeriesPoint } from './metric-chart'

export interface ProgressMetricCardProps {
  title: string
  value: number
  data: SeriesPoint[]
  accent?: MetricAccent
  valueFormatter?: (value: number) => string
  className?: string
  /** Stagger so a row of cards counts up one after another. */
  delay?: number
}

/** Counts up from zero, decelerating into the target; respects reduced motion. */
function useCountUp(target: number, duration = 1300, delay = 0) {
  const [display, setDisplay] = useState(0)
  useEffect(() => {
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      setDisplay(target)
      return
    }
    // Quadratic ease-out: gentle enough that small totals (a few dozen
    // requests) still tick through most values instead of snapping.
    let raf = 0
    let start = 0
    const tick = (now: number) => {
      if (!start) start = now
      const p = Math.min(1, (now - start) / duration)
      setDisplay(target * (1 - (1 - p) ** 2))
      if (p < 1) raf = requestAnimationFrame(tick)
    }
    const timer = setTimeout(() => { raf = requestAnimationFrame(tick) }, delay)
    return () => { clearTimeout(timer); cancelAnimationFrame(raf) }
  }, [target, duration, delay])
  return display
}

/** Light mode draws every sparkline in one neutral grey; dark keeps the accent. */
const ACCENT_CLASS: Record<MetricAccent, string> = {
  emerald: '[--metric-accent:var(--chart-2)] dark:[--metric-accent:var(--chart-1)]',
  rose: '[--metric-accent:var(--chart-2)] dark:[--metric-accent:var(--chart-2)]',
  amber: '[--metric-accent:var(--chart-2)] dark:[--metric-accent:var(--chart-3)]',
  sky: '[--metric-accent:var(--chart-2)] dark:[--metric-accent:var(--chart-4)]',
  violet: '[--metric-accent:var(--chart-2)] dark:[--metric-accent:var(--chart-5)]',
}

/** Count + sparkline only: no controls, no footer. */
export default function ProgressMetricCard({
  title,
  value,
  data,
  accent = 'emerald',
  valueFormatter,
  className = '',
  delay = 0,
}: ProgressMetricCardProps) {
  const counted = useCountUp(value, 1300, delay)

  return (
    <div
      className={`relative flex min-h-[168px] w-full flex-col overflow-hidden rounded-lg bg-card ${className}`}
    >
      <div className="relative z-10 flex flex-col gap-1.5 px-5 pt-4">
        <span className="text-[13px] font-medium text-muted-foreground">{title}</span>
        <span className="text-[34px] font-semibold leading-none tracking-tight tabular-nums text-foreground">
          {(valueFormatter ?? formatCompact)(counted)}
        </span>
      </div>

      <div className={`mt-auto h-16 w-full ${ACCENT_CLASS[accent]}`}>
        {(data?.length ?? 0) >= 2 && (
          <MetricChart data={data} color="var(--metric-accent)" valueFormatter={valueFormatter} />
        )}
      </div>
    </div>
  )
}
