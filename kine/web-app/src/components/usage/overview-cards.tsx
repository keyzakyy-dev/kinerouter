import { Card } from '@/components/ui/card'
import type { UsageStats } from '@/lib/api'
import { fmt, fmtCost } from './helpers'

/** Five period summary tiles, mirroring KineRouter's OverviewCards. */
export function OverviewCards({ stats }: { stats: UsageStats }) {
  const cells = [
    { label: 'Total Requests', value: fmt(stats.totalRequests), cls: '' },
    { label: 'Total Input Tokens', value: fmt(stats.totalPromptTokens), cls: 'text-primary' },
    { label: 'Cached Tokens', value: fmt(stats.totalCachedTokens), cls: 'text-info' },
    { label: 'Output Tokens', value: fmt(stats.totalCompletionTokens), cls: 'text-success' },
    { label: 'Est. Cost', value: `~${fmtCost(stats.totalCost)}`, cls: 'text-warning', sub: 'Estimated, not actual billing' },
  ]
  return (
    <div className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2 sm:gap-4 md:grid-cols-3 lg:grid-cols-5">
      {cells.map((c) => (
        <Card key={c.label} className="flex min-w-0 flex-col items-center gap-1 px-3 py-3 text-center sm:px-4">
          <span className="text-xs font-semibold uppercase text-text-muted sm:text-sm">{c.label}</span>
          <span className={`w-full truncate text-lg font-bold xl:text-xl ${c.cls}`} title={c.value}>
            {c.value}
          </span>
          {c.sub && <span className="text-[10px] text-text-muted">{c.sub}</span>}
        </Card>
      ))}
    </div>
  )
}
