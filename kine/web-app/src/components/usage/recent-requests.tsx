import { useEffect, useState } from 'react'
import type { RecentRequest } from '@/lib/api'
import { Card } from '@/components/ui/card'
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
    <Card className="flex min-w-0 flex-col overflow-hidden p-3" style={{ height: 480 }}>
      <div className="shrink-0 border-b border-border px-1 py-2">
        <span className="text-xs font-semibold uppercase tracking-wide text-text-muted">Recent Requests</span>
      </div>
      {!requests.length ? (
        <div className="flex flex-1 items-center justify-center text-sm text-text-muted">No requests yet.</div>
      ) : (
        <div className="flex-1 overflow-y-auto">
          <table className="w-full min-w-[300px] border-collapse text-xs">
            <thead className="sticky top-0 z-10 bg-bg">
              <tr className="border-b border-border">
                <th className="w-2 py-1.5 text-left font-semibold text-text-muted" />
                <th className="py-1.5 text-left font-semibold text-text-muted">Model</th>
                <th className="whitespace-nowrap py-1.5 text-right font-semibold text-text-muted">In / Out</th>
                <th className="py-1.5 text-right font-semibold text-text-muted">When</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border/50">
              {requests.map((r, i) => {
                const ok = !r.status || r.status === 'ok' || r.status === 'success'
                return (
                  <tr key={i} className="transition-colors hover:bg-bg-alt">
                    <td className="py-1.5">
                      <span className={`block size-1.5 rounded-full ${ok ? 'bg-success' : 'bg-danger'}`} />
                    </td>
                    <td className="max-w-[120px] truncate py-1.5 font-mono" title={r.model}>{r.model}</td>
                    <td className="whitespace-nowrap py-1.5 text-right">
                      <span className="text-primary">{fmt(r.promptTokens)}↑</span>{' '}
                      <span className="text-success">{fmt(r.completionTokens)}↓</span>
                    </td>
                    <td className="whitespace-nowrap py-1.5 text-right text-text-muted">
                      <TimeAgo timestamp={r.timestamp} />
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  )
}
