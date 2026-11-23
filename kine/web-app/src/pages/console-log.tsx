import { useEffect, useRef, useState } from 'react'
import { PageHeader } from '@/components/layout'
import { Icon } from '@/components/icon'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

/** Matches CONSOLE_LOG_CONFIG.maxLines in the dashboard. */
const MAX_LINES = 200

/** Only errors keep a hue; everything else stays monochrome. */
function LevelLine({ line }: { line: string }) {
  const tags = line.match(/\[(\w+)\]/g)
  const level = tags?.[1]?.replace(/[[\]]/g, '') ?? ''
  const stamp = line.match(/^(\d{2}:\d{2}:\d{2})\s+(.*)$/)

  return (
    <div
      className={cn(
        'flex gap-3 rounded-md px-3 py-[3px] transition-colors hover:bg-surface',
        level === 'ERROR' && 'text-danger',
      )}
    >
      {stamp && <span className="shrink-0 select-none text-text-muted/70">{stamp[1]}</span>}
      <span className={cn('min-w-0 break-words', level === 'ERROR' ? 'text-danger' : 'text-text-main')}>
        {stamp ? stamp[2] : line}
      </span>
    </div>
  )
}

export default function ConsoleLogPage() {
  const [logs, setLogs] = useState<string[]>([])
  const [connected, setConnected] = useState(false)
  const logRef = useRef<HTMLDivElement>(null)

  const clear = async () => {
    try {
      await fetch('/api/translator/console-logs', { method: 'DELETE' })
      // The UI empties itself on the SSE "clear" event.
    } catch {
      // Nothing to do: the next event will resync.
    }
  }

  useEffect(() => {
    const es = new EventSource('/api/translator/console-logs/stream')
    es.onopen = () => setConnected(true)

    es.onmessage = (e) => {
      const msg = JSON.parse(e.data) as
        | { type: 'init'; logs: string[] }
        | { type: 'line'; line: string }
        | { type: 'lines'; lines: string[] }
        | { type: 'clear' }

      if (msg.type === 'init') {
        setLogs(msg.logs.slice(-MAX_LINES))
      } else if (msg.type === 'line') {
        setLogs((prev) => [...prev, msg.line].slice(-MAX_LINES))
      } else if (msg.type === 'lines') {
        setLogs((prev) => [...prev, ...msg.lines].slice(-MAX_LINES))
      } else if (msg.type === 'clear') {
        setLogs([])
      }
    }
    es.onerror = () => setConnected(false)

    return () => es.close()
  }, [])

  // Follow the tail as lines arrive.
  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight
  }, [logs])

  return (
    <div className="space-y-5">
      <PageHeader
        title="Console Log"
        description="Live stdout from the gateway process, last 200 lines."
        actions={
          <Button size="sm" variant="ghost" onClick={clear} disabled={logs.length === 0}>
            <Icon name="delete" className="text-[16px]" />
            Clear
          </Button>
        }
      />

      <section className="overflow-hidden rounded-xl bg-surface">
        <div className="flex items-center justify-between gap-3 px-4 py-3">
          <span className="inline-flex items-center gap-1.5 rounded-md bg-surface-2 px-2 py-0.5 text-[11px] text-text-muted">
            <span
              className={cn('size-1.5 rounded-full', connected ? 'bg-success' : 'bg-text-subtle')}
              aria-hidden
            />
            {connected ? 'Live' : 'Disconnected'}
          </span>
          <span className="font-mono text-[11px] text-text-muted">
            {logs.length}/{MAX_LINES}
          </span>
        </div>

        <div
          ref={logRef}
          className="h-[calc(100dvh-260px)] min-h-80 overflow-y-auto border-t border-border bg-surface-2/60 p-2 font-mono text-xs leading-relaxed"
        >
          {logs.length === 0 ? (
            <div className="flex h-full flex-col items-center justify-center gap-2 text-text-muted">
              <Icon name="terminal" className="text-[22px]" />
              <p className="text-xs">No console output yet.</p>
            </div>
          ) : (
            logs.map((line, i) => <LevelLine key={i} line={line} />)
          )}
        </div>
      </section>
    </div>
  )
}
