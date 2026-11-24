import { useEffect, useState } from 'react'
import type { RecentRequest } from '@/lib/api'
import { Icon } from '@/components/icon'
import { fmt } from './helpers'

/** Ticks every second without re-rendering the parent list. */
function TimeAgo({ timestamp }: { timestamp: string }) {
  const [, setTick] = useState(0)
  useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), 1000)
    return () => clearInterval(id)
  }, [])
  const diff = Math.floor((Date.now() - new Date(timestamp).getTime()) / 1000)
  const text =
    diff < 60 ? `${diff}s ago` : diff < 3600 ? `${Math.floor(diff / 60)}m ago` : diff < 86400 ? `${Math.floor(diff / 3600)}h ago` : `${Math.floor(diff / 86400)}d ago`
  return <>{text}</>
}

/** Live tail of the last few completed requests. */
export function RecentRequests({
  requests = [],
  providerNames = {},
}: {
  requests?: RecentRequest[]
  providerNames?: Record<string, string>
}) {
  return (
    <section className="flex min-w-0 flex-col overflow-hidden rounded-xl bg-surface" style={{ height: 480 }}>
      <div className="flex shrink-0 items-center gap-2 px-4 py-3">
        <Icon name="schedule" className="text-[15px] text-text-muted" />
        <h2 className="text-sm font-semibold text-text-main">Recent requests</h2>
        <span className="ml-auto font-mono text-[11px] text-text-muted">{requests.length}</span>
      </div>

      {!requests.length ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 rounded-xl bg-surface-2 p-6 text-center text-xs text-text-muted">
          <Icon name="schedule" className="text-[24px] text-text-muted/60" />
          <span>No requests yet. Send one from Endpoint &amp; Key.</span>
        </div>
      ) : (
        <ul className="custom-scrollbar flex-1 divide-y divide-border overflow-y-auto border-t border-border">
          {requests.map((r, i) => {
            const ok = !r.status || r.status === 'ok' || r.status === 'success'
            const provider = providerNames[r.provider] || r.provider
            return (
              <li key={i} className="flex items-start gap-2 px-4 py-2 transition-colors hover:bg-surface-2/40">
                <span className={`mt-1.5 size-1.5 shrink-0 rounded-full ${ok ? 'bg-success' : 'bg-danger'}`} />
                <div className="min-w-0 flex-1">
                  <code className="block truncate font-mono text-[11px] leading-4 text-text-main" title={r.model}>
                    {r.model}
                  </code>
                  {provider && (
                    <span className="block truncate text-[10px] leading-3 text-text-muted" title={provider}>
                      {provider}
                    </span>
                  )}
                </div>
                <span className="shrink-0 pt-0.5 pl-4 text-right font-mono text-[10px]">
                  <span className="text-sky-600 dark:text-sky-400">{fmt(r.promptTokens)}↑</span>{' '}
                  <span className="text-emerald-600 dark:text-emerald-400">{fmt(r.completionTokens)}↓</span>
                </span>
                <span className="w-14 shrink-0 pt-0.5 text-right font-mono text-[10px] text-text-muted">
                  <TimeAgo timestamp={r.timestamp} />
                </span>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
