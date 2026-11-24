import { useEffect, useState } from 'react'
import { MetricChart, formatCompact, type MetricAccent, type SeriesPoint } from './metric-chart'

export interface ProgressMetricCardProps {
  title: string
  value: number
  data: SeriesPoint[]
  accent?: MetricAccent
  valueFormatter?: (value: number) => string
  className?: string
}

/** Counts up from zero with a decelerating tail; respects reduced motion. */
function useCountUp(target: number, duration = 1600) {
  const [display, setDisplay] = useState(0)
  useEffect(() => {
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      setDisplay(target)
      return
    }
    const start = performance.now()
    let raf = 0
    const step = (now: number) => {
      const p = Math.min(1, (now - start) / duration)
      // Quintic ease-out: fast off the mark, long slow settle at the top.
      setDisplay(target * (1 - (1 - p) ** 5))
      if (p < 1) raf = requestAnimationFrame(step)
    }
    raf = requestAnimationFrame(step)
    return () => cancelAnimationFrame(raf)
  }, [target, duration])
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
}: ProgressMetricCardProps) {
  const counted = useCountUp(value)

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
