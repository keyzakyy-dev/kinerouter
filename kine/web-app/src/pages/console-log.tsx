import { useEffect, useMemo, useRef, useState } from 'react'
import { PageHeader } from '@/components/layout'
import { Icon } from '@/components/icon'
import { Button } from '@/components/ui/button'
import { useNotify } from '@/lib/toast'
import { cn } from '@/lib/utils'

/** Matches CONSOLE_LOG_CONFIG.maxLines in the dashboard. */
const MAX_LINES = 200

type Level = 'debug' | 'info' | 'warn' | 'error'

interface Entry {
  seq: number
  time: string
  level: Level
  tag: string
  message: string
}

/**
 * Port of 9router-go's console rows: the logger's level and tag are split out
 * so a row reads time / level / tag / message. Lines that never went through a
 * level-aware logger fall back to a text prefix, then to "info".
 */
function parseLine(line: string, seq: number): Entry {
  const stamp = line.match(/^(\d{2}:\d{2}:\d{2})\s+(.*)$/)
  const time = stamp?.[1] ?? ''
  let body = stamp?.[2] ?? line

  const tagMatch = body.match(/^\[([^\]]+)\]\s*/)
  const tag = tagMatch?.[1] ?? ''
  if (tagMatch) body = body.slice(tagMatch[0].length)

  let level: Level = 'info'
  const prefix = body.match(/^(DBG|DEBUG|INF|INFO|WRN|WARN|ERR|ERROR)(?=\s)/)
  if (prefix) {
    const first = prefix[1][0]
    level = first === 'E' ? 'error' : first === 'W' ? 'warn' : first === 'D' ? 'debug' : 'info'
    body = body.slice(prefix[0].length)
  } else if (tag) {
    const lower = tag.toLowerCase()
    if (lower.startsWith('err')) level = 'error'
    else if (lower.startsWith('wrn') || lower.startsWith('warn')) level = 'warn'
    else if (lower.startsWith('dbg') || lower === 'debug') level = 'debug'
  }

  return { seq, time, level, tag, message: body }
}

/** Chips are icon-only: the tinted box carries the level, the tooltip names it. */
const LEVEL_META: Record<Level, { label: string; icon: string; text: string; on: string }> = {
  error: { label: 'ERR', icon: 'error', text: 'text-danger', on: 'bg-danger/15 text-danger hover:bg-danger/25' },
  warn: { label: 'WRN', icon: 'warning', text: 'text-warning', on: 'bg-warning/15 text-warning hover:bg-warning/25' },
  info: { label: 'INF', icon: 'info', text: 'text-text-main', on: 'bg-info/15 text-info hover:bg-info/25' },
  debug: { label: 'DBG', icon: 'code', text: 'text-text-muted', on: 'bg-surface-3 text-text-muted hover:bg-surface-3/80' },
}

const LEVEL_ORDER: Level[] = ['error', 'warn', 'info', 'debug']

export default function ConsoleLogPage() {
  const notify = useNotify()
  const [lines, setLines] = useState<string[]>([])
  const [connected, setConnected] = useState(false)
  const [query, setQuery] = useState('')
  const [wrap, setWrap] = useState(false)
  const [muted, setMuted] = useState<Set<Level>>(new Set())
  const [following, setFollowing] = useState(true)
  const logRef = useRef<HTMLDivElement>(null)

  const entries = useMemo(() => lines.map((line, i) => parseLine(line, i)), [lines])

  const counts = useMemo(() => {
    const out: Record<Level, number> = { debug: 0, info: 0, warn: 0, error: 0 }
    for (const entry of entries) out[entry.level]++
    return out
  }, [entries])

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return entries.filter(
      (entry) =>
        !muted.has(entry.level) &&
        (!needle ||
          entry.message.toLowerCase().includes(needle) ||
          entry.tag.toLowerCase().includes(needle)),
    )
  }, [entries, muted, query])

  const toggleLevel = (level: Level) => {
    setMuted((prev) => {
      const next = new Set(prev)
      if (next.has(level)) next.delete(level)
      else next.add(level)
      return next
    })
  }

  const clear = async () => {
    try {
      await fetch('/api/translator/console-logs', { method: 'DELETE' })
      // The UI empties itself on the SSE "clear" event.
    } catch {
      // Nothing to do: the next event will resync.
    }
  }

  const plainText = () =>
    visible.map((entry) => [entry.time, LEVEL_META[entry.level].label, entry.tag && `[${entry.tag}]`, entry.message].filter(Boolean).join(' ')).join('\n')

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(plainText())
      notify.success(`Copied ${visible.length} log ${visible.length === 1 ? 'line' : 'lines'}.`)
    } catch {
      notify.error('Clipboard is unavailable in this browser.')
    }
  }

  const exportLines = () => {
    const blob = new Blob([plainText()], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `kinerouter-console-${new Date().toISOString().replace(/[:.]/g, '-')}.log`
    link.click()
    URL.revokeObjectURL(url)
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
        setLines(msg.logs.slice(-MAX_LINES))
      } else if (msg.type === 'line') {
        setLines((prev) => [...prev, msg.line].slice(-MAX_LINES))
      } else if (msg.type === 'lines') {
        setLines((prev) => [...prev, ...msg.lines].slice(-MAX_LINES))
      } else if (msg.type === 'clear') {
        setLines([])
      }
    }
    es.onerror = () => setConnected(false)

    return () => es.close()
  }, [])

  // Follow the tail while the operator stays at the bottom; scrolling up pins
  // the line being read instead of yanking it away.
  useEffect(() => {
    const el = logRef.current
    if (el && following) el.scrollTop = el.scrollHeight
  }, [visible, following])

  return (
    <div className="space-y-5">
      <PageHeader
        title="Console Log"
        description="Live stdout from the gateway process, last 200 lines."
        actions={
          <Button size="sm" variant="ghost" onClick={clear} disabled={lines.length === 0}>
            <Icon name="delete" className="text-[16px]" />
            Clear
          </Button>
        }
      />

      <section className="overflow-hidden rounded-xl bg-surface">
        <div className="flex flex-wrap items-center gap-2 px-4 py-3">
          <div className="flex flex-wrap items-center gap-1.5">
            {LEVEL_ORDER.map((level) => {
              const meta = LEVEL_META[level]
              const off = muted.has(level)
              return (
                <button
                  key={level}
                  type="button"
                  aria-pressed={!off}
                  aria-label={`${off ? 'Show' : 'Hide'} ${meta.label} lines (${counts[level]})`}
                  title={`${off ? 'Show' : 'Hide'} ${meta.label} lines (${counts[level]})`}
                  onClick={() => toggleLevel(level)}
                  className={cn(
                    'grid size-7 cursor-pointer place-items-center rounded-md transition-colors',
                    off ? 'bg-surface-2 text-text-subtle hover:bg-surface-3' : meta.on,
                  )}
                >
                  <Icon name={meta.icon} className="text-[15px]" />
                </button>
              )
            })}
          </div>

          <span className="inline-flex items-center gap-1.5 rounded-md bg-surface-2 px-2 py-0.5 text-[11px] text-text-muted">
            <span
              className={cn('size-1.5 rounded-full', connected ? 'bg-success' : 'bg-text-subtle')}
              aria-hidden
            />
            {connected ? 'Live' : 'Disconnected'}
          </span>

          <div className="relative ml-auto w-full sm:w-64">
            <Icon
              name="search"
              className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-[14px] text-text-muted"
            />
            <input
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Escape' && query) setQuery('')
              }}
              placeholder="Search lines..."
              aria-label="Search console log lines"
              className="w-full rounded-md border border-border bg-surface py-1.5 pl-8 pr-7 font-mono text-xs text-text-main placeholder:text-text-muted focus:outline-none"
            />
            {query && (
              <button
                type="button"
                aria-label="Clear search"
                onClick={() => setQuery('')}
                className="absolute right-2 top-1/2 -translate-y-1/2 cursor-pointer text-text-muted hover:text-text-main"
              >
                <Icon name="close" className="text-[14px]" />
              </button>
            )}
          </div>

          <button
            type="button"
            aria-pressed={wrap}
            title={wrap ? 'Disable line wrap' : 'Wrap long lines'}
            onClick={() => setWrap((v) => !v)}
            className={cn(
              'h-7 cursor-pointer rounded-md border border-border px-2.5 text-xs text-text-main transition-colors hover:bg-surface-2',
              wrap && 'bg-surface-2',
            )}
          >
            Wrap
          </button>

          <Button size="sm" variant="outline" title="Copy visible lines" onClick={copy} disabled={visible.length === 0}>
            <Icon name="content_copy" />
            Copy
          </Button>
          <Button size="sm" variant="outline" title="Download visible lines" onClick={exportLines} disabled={visible.length === 0}>
            <Icon name="download" />
            Export
          </Button>
        </div>

        <div
          ref={logRef}
          onScroll={(e) => {
            const el = e.currentTarget
            setFollowing(el.scrollHeight - el.scrollTop - el.clientHeight < 24)
          }}
          className="relative h-[calc(100dvh-260px)] min-h-80 overflow-auto border-t border-border bg-surface-2/60 font-mono text-xs leading-relaxed"
        >
          {entries.length === 0 ? (
            <div className="flex h-full flex-col items-center justify-center gap-1.5 px-6 text-center">
              <Icon name="terminal" className="text-[22px] text-text-muted" />
              <p className="text-sm text-text-main">No console output yet</p>
              <p className="max-w-md text-xs text-text-muted">
                Nothing has been logged since the server started or the last clear. Send a request and rows appear
                here live.
              </p>
            </div>
          ) : visible.length === 0 ? (
            <div className="flex h-full flex-col items-center justify-center gap-1.5 px-6 text-center">
              <p className="text-sm text-text-main">Nothing matches the current filter</p>
              <p className="max-w-md text-xs text-text-muted">
                {muted.size > 0
                  ? `${[...muted].map((level) => LEVEL_META[level].label).join(', ')} rows are hidden by the level chips.`
                  : `No line contains "${query.trim()}".`}
              </p>
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setQuery('')
                  setMuted(new Set())
                }}
              >
                Show all {entries.length} lines
              </Button>
            </div>
          ) : (
            <div className="divide-y divide-border/60">
              {visible.map((entry) => {
                const meta = LEVEL_META[entry.level]
                return (
                  <div key={entry.seq} className="flex items-baseline gap-3 px-3 py-1 hover:bg-surface-3/40">
                    <span className="shrink-0 select-none tabular-nums text-[11px] text-text-muted">{entry.time}</span>
                    <span className={cn('w-8 shrink-0 text-[11px] font-bold', meta.text)}>{meta.label}</span>
                    <span
                      className={cn(
                        'min-w-0 flex-1',
                        wrap ? 'break-words' : 'whitespace-pre',
                        entry.level === 'error' ? 'text-danger' : 'text-text-main',
                      )}
                    >
                      {entry.tag && <span className="opacity-70">[{entry.tag}]</span>}{' '}
                      {entry.message}
                    </span>
                  </div>
                )
              })}
            </div>
          )}

          {!following && entries.length > 0 && (
            <button
              type="button"
              onClick={() => {
                setFollowing(true)
                if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight
              }}
              className="sticky bottom-3 left-1/2 inline-flex -translate-x-1/2 cursor-pointer items-center gap-1.5 rounded-full border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text-main shadow-[var(--shadow-elev)] transition-colors hover:bg-surface-2"
            >
              <Icon name="arrow_downward" className="text-[14px]" />
              Jump to latest
            </button>
          )}
        </div>

        <div className="flex items-center justify-between gap-3 border-t border-border px-4 py-2 text-[11px] text-text-muted">
          <span className="tabular-nums">
            Showing {visible.length} of {entries.length} buffered {entries.length === 1 ? 'line' : 'lines'}
          </span>
          <span className="tabular-nums">
            {counts.error} error / {counts.warn} warn / {counts.info} info / {counts.debug} debug
          </span>
        </div>
      </section>
    </div>
  )
}
