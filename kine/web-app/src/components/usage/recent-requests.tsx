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
export function RecentRequests({ requests = [] }: { requests?: RecentRequest[] }) {
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
            return (
              <li key={i} className="flex items-center gap-2 px-4 py-2 transition-colors hover:bg-surface-2/40">
                <span className={`size-1.5 shrink-0 rounded-full ${ok ? 'bg-success' : 'bg-danger'}`} />
                <code className="min-w-0 flex-1 truncate font-mono text-[11px] text-text-main" title={r.model}>
                  {r.model}
                </code>
                <span className="shrink-0 font-mono text-[10px] text-text-muted">
                  {fmt(r.promptTokens)}↑ {fmt(r.completionTokens)}↓
                </span>
                <span className="w-14 shrink-0 text-right font-mono text-[10px] text-text-muted">
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
