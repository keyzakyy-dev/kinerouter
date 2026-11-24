import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type TerminalInfo } from '@/lib/api'
import { useNotify } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { PageHeader } from '@/components/layout'
import { Icon } from '@/components/icon'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'

type Entry = { id: number; command: string; output: string; exitCode: number; cwd: string }

const PROMPT_HINT = 'type a command and press Enter'

/**
 * Web terminal on the gateway host. Each command is one authenticated request;
 * the browser owns the transcript and the working directory, the server keeps
 * no shell session. `cd` navigates, everything else runs through /bin/sh.
 */
export default function TerminalPage() {
  const notify = useNotify()
  const [info, setInfo] = useState<TerminalInfo | null>(null)
  const [cwd, setCwd] = useState('')
  const [entries, setEntries] = useState<Entry[]>([])
  const [command, setCommand] = useState('')
  const [busy, setBusy] = useState(false)
  const [history, setHistory] = useState<string[]>([])
  const [historyAt, setHistoryAt] = useState(-1)
  const inputRef = useRef<HTMLInputElement>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const nextId = useRef(1)

  const loadInfo = useCallback(async () => {
    const res = await api.terminalInfo()
    setInfo(res)
    setCwd(res.cwd)
  }, [])

  useEffect(() => {
    loadInfo().catch((err: Error) => notify.error(err.message))
  }, [loadInfo, notify])

  useEffect(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [entries, busy])

  const run = async () => {
    const cmd = command.trim()
    if (!cmd || busy) return
    if (cmd === 'clear') {
      setEntries([])
      setCommand('')
      return
    }
    setCommand('')
    setHistory((h) => [...h, cmd])
    setHistoryAt(-1)
    setBusy(true)
    const at = cwd
    try {
      const res = await api.terminalExec(cmd, cwd)
      setCwd(res.cwd)
      setEntries((prev) => [
        ...prev,
        { id: nextId.current++, command: cmd, output: res.output.replace(/\n$/, ''), exitCode: res.exitCode, cwd: at },
      ])
    } catch (err) {
      setEntries((prev) => [
        ...prev,
        {
          id: nextId.current++,
          command: cmd,
          output: err instanceof Error ? err.message : String(err),
          exitCode: 1,
          cwd: at,
        },
      ])
    } finally {
      setBusy(false)
      inputRef.current?.focus()
    }
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      void run()
      return
    }
    if (e.ctrlKey && e.key.toLowerCase() === 'l') {
      e.preventDefault()
      setEntries([])
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      if (history.length === 0) return
      const at = historyAt < 0 ? history.length - 1 : Math.max(0, historyAt - 1)
      setHistoryAt(at)
      setCommand(history[at])
      return
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      if (historyAt < 0) return
      const at = historyAt + 1
      if (at >= history.length) {
        setHistoryAt(-1)
        setCommand('')
        return
      }
      setHistoryAt(at)
      setCommand(history[at])
    }
  }

  const allowRemote = async () => {
    try {
      await api.patchSettings({ terminalAllowRemote: true })
      await loadInfo()
      notify.success('Remote terminal access enabled')
    } catch (err) {
      notify.error(err instanceof Error ? err.message : String(err))
    }
  }

  const prompt = `${info?.user ?? 'user'}@${info?.host ?? 'host'}:${shortPath(cwd, info?.user)}$`

  return (
    <div className="px-4 py-5 sm:px-6">
      <PageHeader
        title="Terminal"
        description="Shell on the gateway host, run as the gateway's own OS user. Every command is a separate request."
        actions={
          <div className="flex items-center gap-1.5">
            <Button size="sm" variant="ghost" onClick={() => setEntries([])} disabled={entries.length === 0}>
              <Icon name="delete_sweep" /> Clear
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                if (!info) return
                setCwd(info.cwd)
                inputRef.current?.focus()
              }}
            >
              <Icon name="home" /> Home
            </Button>
          </div>
        }
      />

      {info && !info.allowed && (
        <Card className="mb-3 border-warning/50 bg-warning/5 p-3">
          <p className="text-xs text-text-main">
            This dashboard is open from a remote address, so the terminal is refused. Enable it only if you
            trust every dashboard session.
          </p>
          <Button size="sm" variant="secondary" className="mt-2" onClick={allowRemote}>
            Allow remote terminal
          </Button>
        </Card>
      )}

      <Card className="overflow-hidden p-0">
        <div className="flex items-center justify-between border-b border-border px-3 py-2">
          <span className="flex items-center gap-1.5 text-[11px] text-text-muted">
            <Icon name="terminal" className="text-[13px]" /> {info?.shell ?? '/bin/sh'}
          </span>
          <span className="truncate font-mono text-[11px] text-text-muted">{cwd || '…'}</span>
        </div>

        <div ref={scrollRef} className="h-[26rem] overflow-y-auto bg-code-background px-3 py-2">
          {entries.length === 0 && !busy && (
            <p className="text-[11px] text-code-foreground/60">
              {PROMPT_HINT}. Try <span className="font-mono">ls</span>, <span className="font-mono">git status</span>,{' '}
              <span className="font-mono">docker ps</span>. Press ↑ for history, Ctrl+L to clear.
            </p>
          )}
          {entries.map((entry) => (
            <div key={entry.id} className="mb-2 font-mono text-xs leading-relaxed">
              <p className="text-code-foreground/70">
                <span className="text-primary">{promptAt(entry.cwd, info)}</span>{' '}
                <span className="text-code-foreground">{entry.command}</span>
              </p>
              {entry.output && <pre className="whitespace-pre-wrap break-words text-code-foreground">{entry.output}</pre>}
              {entry.exitCode !== 0 && (
                <p className="text-[10px] text-danger">exit {entry.exitCode}</p>
              )}
            </div>
          ))}
          {busy && (
            <p className="font-mono text-xs text-code-foreground/60">
              <span className="text-primary">{prompt}</span> <span className="animate-pulse">running…</span>
            </p>
          )}
        </div>

        <div className="flex items-center gap-2 border-t border-border bg-code-background px-3 py-2">
          <span className="shrink-0 font-mono text-xs text-primary">{prompt}</span>
          <input
            ref={inputRef}
            value={command}
            onChange={(e) => setCommand(e.target.value)}
            onKeyDown={onKeyDown}
            disabled={busy || (info ? !info.allowed : false)}
            autoFocus
            spellCheck={false}
            autoComplete="off"
            placeholder={busy ? '' : 'command'}
            aria-label="Terminal command"
            className={cn(
              'min-w-0 flex-1 bg-transparent font-mono text-xs text-code-foreground placeholder:text-code-foreground/40',
              'outline-none disabled:opacity-50',
            )}
          />
          <Button size="sm" variant="ghost" disabled={busy || !command.trim()} onClick={() => void run()}>
            <Icon name="keyboard_return" /> Run
          </Button>
        </div>
      </Card>

      <p className="mt-3 text-[11px] text-text-muted">
        Non-interactive commands only. Full-screen programs (vim, top) need a PTY and are not supported.
      </p>
    </div>
  )
}

/** promptAt renders the prompt that was live when the command was typed. */
function promptAt(cwd: string, info: TerminalInfo | null): string {
  return `${info?.user ?? 'user'}@${info?.host ?? 'host'}:${shortPath(cwd, info?.user)}$`
}

/** shortPath collapses the home prefix to ~ and keeps the tail readable. */
function shortPath(cwd: string, user?: string): string {
  if (!cwd) return '~'
  const parts = cwd.split('/').filter(Boolean)
  const home = user ? `/home/${user}` : ''
  if (home && cwd === home) return '~'
  if (home && cwd.startsWith(home + '/')) return '~/' + cwd.slice(home.length + 1)
  if (parts.length <= 3) return cwd
  return '…/' + parts.slice(-2).join('/')
}
