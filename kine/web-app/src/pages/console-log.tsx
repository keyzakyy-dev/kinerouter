import { useEffect, useRef, useState } from 'react'
import { Icon } from '@/components/icon'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'

/** Matches CONSOLE_LOG_CONFIG.maxLines in the dashboard. */
const MAX_LINES = 200

const LEVEL_COLORS: Record<string, string> = {
  LOG: 'text-green-400',
  INFO: 'text-blue-400',
  WARN: 'text-yellow-400',
  ERROR: 'text-red-400',
  DEBUG: 'text-purple-400',
}

/** Colours a line by its first bracketed tag, like the KineRouter viewer. */
function ColorLine({ line }: { line: string }) {
  const tags = line.match(/\[(\w+)\]/g)
  const level = tags?.[1]?.replace(/[[\]]/g, '') ?? ''
  return <span className={LEVEL_COLORS[level] ?? 'text-green-400'}>{line}</span>
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
    <Card>
      <div className="flex items-center justify-between px-4 pt-3 pb-2">
        <span className="flex items-center gap-1.5 text-xs text-text-muted">
          <span
            className={`size-2 rounded-full ${connected ? 'bg-success' : 'bg-text-subtle'}`}
            aria-hidden
          />
          {connected ? 'Live' : 'Disconnected'}
        </span>
        <Button size="sm" variant="outline" onClick={clear}>
          <Icon name="delete" className="text-[16px]" />
          Clear
        </Button>
      </div>
      <div
        ref={logRef}
        className="h-[calc(100vh-220px)] overflow-y-auto rounded-b-lg bg-black p-4 font-mono text-xs"
      >
        {logs.length === 0 ? (
          <span className="text-text-muted">No console logs yet.</span>
        ) : (
          <div className="space-y-0.5">
            {logs.map((line, i) => (
              <div key={i}>
                <ColorLine line={line} />
              </div>
            ))}
          </div>
        )}
      </div>
    </Card>
  )
}
