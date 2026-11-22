import { useCallback, useEffect, useState } from 'react'
import { api } from '@/lib/api'
import { Card } from '@/components/ui/card'

/** Raw pipe-delimited request log tail, polls every 3s while auto-refresh is on. */
export function RequestLogger() {
  const [logs, setLogs] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [autoRefresh, setAutoRefresh] = useState(true)

  const fetchLogs = useCallback(async (showLoading = true) => {
    if (showLoading) setLoading(true)
    try {
      setLogs(await api.usageRequestLogs())
    } catch { /* keep the last batch on a transient error */ }
    finally { if (showLoading) setLoading(false) }
  }, [])

  useEffect(() => { void fetchLogs() }, [fetchLogs])

  useEffect(() => {
    if (!autoRefresh) return
    const id = setInterval(() => { void fetchLogs(false) }, 3000)
    return () => clearInterval(id)
  }, [autoRefresh, fetchLogs])

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold">Request Logs</h2>
        <label className="flex cursor-pointer items-center gap-2 text-sm font-medium text-text-muted">
          <span>Auto Refresh (3s)</span>
          <button
            type="button"
            role="switch"
            aria-checked={autoRefresh}
            onClick={() => setAutoRefresh((v) => !v)}
            className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${autoRefresh ? 'bg-primary' : 'border border-border bg-bg-alt'}`}
          >
            <span className={`inline-block size-3 transform rounded-full bg-white transition-transform ${autoRefresh ? 'translate-x-5' : 'translate-x-1'}`} />
          </button>
        </label>
      </div>

      <Card className="overflow-hidden bg-black/5 dark:bg-black/20">
        <div className="max-h-[600px] overflow-x-auto overflow-y-auto p-0 font-mono text-xs">
          {loading && logs.length === 0 ? (
            <div className="p-8 text-center text-text-muted">Loading logs...</div>
          ) : logs.length === 0 ? (
            <div className="p-8 text-center text-text-muted">No logs recorded yet.</div>
          ) : (
            <table className="w-full border-collapse whitespace-nowrap text-left">
              <thead className="sticky top-0 z-10 border-b border-border bg-bg-alt">
                <tr>
                  {['DateTime', 'Model', 'Provider', 'Account', 'In', 'Out', 'Status'].map((h, i) => (
                    <th key={h} className={`px-3 py-2 ${i < 6 ? 'border-r border-border' : ''}`}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody className="divide-y divide-border/50">
                {logs.map((log, i) => {
                  const parts = log.split(' | ')
                  if (parts.length < 7) return null
                  const status = parts[6]
                  const isSuccess = status.includes('OK')
                  const isFailed = status.includes('FAILED')
                  return (
                    <tr key={i} className={`transition-colors hover:bg-primary/5 ${status.includes('PENDING') ? 'bg-primary/5' : ''}`}>
                      <td className="border-r border-border px-3 py-1.5 text-text-muted">{parts[0]}</td>
                      <td className="border-r border-border px-3 py-1.5 font-medium">{parts[1]}</td>
                      <td className="border-r border-border px-3 py-1.5">
                        <span className="rounded border border-border bg-bg-alt px-1.5 py-0.5 text-[10px] font-bold uppercase">{parts[2]}</span>
                      </td>
                      <td className="max-w-[150px] truncate border-r border-border px-3 py-1.5" title={parts[3]}>{parts[3]}</td>
                      <td className="border-r border-border px-3 py-1.5 text-right text-primary">{parts[4]}</td>
                      <td className="border-r border-border px-3 py-1.5 text-right text-success">{parts[5]}</td>
                      <td className={`px-3 py-1.5 font-bold ${isSuccess ? 'text-success' : isFailed ? 'text-danger' : 'animate-pulse text-primary'}`}>
                        {status}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </div>
      </Card>
      <div className="text-[10px] italic text-text-muted">Logs are loaded from the request history database.</div>
    </div>
  )
}
