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
      <div className="flex shrink-0 items-baseline gap-2 px-4 py-3">
        <h2 className="text-sm font-semibold text-text-main">Recent requests</h2>
        <span className="font-mono text-[10px] text-text-muted">
          {requests.length} {requests.length === 1 ? 'entry' : 'entries'}
        </span>
        <span className="ml-auto text-[10px] text-text-muted">in / out</span>
      </div>

      {!requests.length ? (
        <div className="m-4 flex flex-1 flex-col items-center justify-center gap-2 rounded-xl bg-surface-2 p-6 text-center text-xs text-text-muted">
          <Icon name="schedule" className="text-[24px] text-text-muted/60" />
          <span>No requests yet. Send one from Endpoint &amp; Key.</span>
        </div>
      ) : (
        <ul className="custom-scrollbar flex-1 divide-y divide-border overflow-y-auto border-t border-border">
          {requests.map((r, i) => {
            const ok = !r.status || r.status === 'ok' || r.status === 'success'
            return (
              <li
                key={i}
                className="flex items-start gap-3 px-4 py-2.5 transition-colors hover:bg-surface-2/40"
              >
                <span
                  className={`mt-1 h-7 w-0.5 shrink-0 rounded-full ${ok ? 'bg-emerald-500' : 'bg-danger'}`}
                />
                <div className="min-w-0 flex-1">
                  <code className="block truncate font-mono text-[11px] leading-4 text-text-main" title={r.model}>
                    {r.model}
                  </code>
                  <div className="mt-1 flex min-w-0 items-center gap-1.5 text-[10px] leading-3 text-text-muted">
                    {r.provider && (
                      <>
                        <span className="max-w-[45%] truncate">{r.provider}</span>
                        <span aria-hidden>·</span>
                      </>
                    )}
                    <span>
                      <TimeAgo timestamp={r.timestamp} />
                    </span>
                    {r.cachedTokens > 0 && (
                      <>
                        <span aria-hidden>·</span>
                        <span className="font-mono">{fmt(r.cachedTokens)} cached</span>
                      </>
                    )}
                  </div>
                </div>
                <div className="shrink-0 text-right font-mono text-[10px] leading-4">
                  <div className="text-text-main">{fmt(r.promptTokens)}</div>
                  <div className="text-text-muted">{fmt(r.completionTokens)}</div>
                </div>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
