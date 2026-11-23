import { useCallback, useEffect, useRef, useState } from 'react'
import { Icon } from '@/components/icon'
import { ConfirmModal, Modal } from '@/components/modal'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { errorMessage } from '@/lib/errors'
import { useCopyToClipboard } from '@/lib/use-copy-to-clipboard'
import { cn } from '@/lib/utils'

/**
 * Token Saver levels.
 *
 * The wenyan variants are gated on the active locale; there is no i18n layer
 * yet, so the locale is always `en` and those levels stay hidden.
 */

type Level = { id: string; label: string; desc: string; wenyan?: boolean }

const CAVEMAN_LEVELS: Level[] = [
  { id: 'lite', label: 'Lite', desc: 'Drop filler, keep grammar' },
  { id: 'full', label: 'Full', desc: 'Drop articles, fragments OK' },
  { id: 'ultra', label: 'Ultra', desc: 'Telegraphic, max compression' },
  { id: 'wenyan-lite', label: '文 Lite', desc: 'Classical Chinese, light compression', wenyan: true },
  { id: 'wenyan', label: '文 Full', desc: 'Maximum 文言文, 80-90% reduction', wenyan: true },
  { id: 'wenyan-ultra', label: '文 Ultra', desc: 'Extreme classical compression', wenyan: true },
]

const PONYTAIL_LEVELS: Level[] = [
  { id: 'lite', label: 'Lite', desc: 'Build asked, name lazier option' },
  { id: 'full', label: 'Full', desc: 'Ladder enforced: stdlib/native first' },
  { id: 'ultra', label: 'Ultra', desc: 'YAGNI extremist, deletion first' },
]

type HeadroomStatus = {
  installed?: boolean
  running?: boolean
  python?: string | null
  loading: boolean
  localUrl?: boolean
  canStart?: boolean
  managedPid?: number | null
}

type HeadroomExtras = {
  version: string | null
  extras: Record<string, boolean>
  available: string[]
  loading: boolean
}

type PxpipeStatus = {
  installed: boolean
  installing: boolean
  running: boolean
  version: string | null
  loading: boolean
}

type PxpipeHealth = {
  healthy?: boolean
  checks?: { id: string; label: string; ok: boolean; detail?: string }[]
  error?: string
}

type Confirm = {
  title: string
  message: string
  confirmText?: string
  variant?: 'primary' | 'danger'
  onConfirm: () => void
}

function StatusChip({ label, ok }: { label: string; ok: boolean }) {
  return (
    <span className="inline-flex items-center gap-1.5 rounded-md bg-surface-2 px-2 py-0.5 text-[11px] text-text-muted">
      <span className={cn('size-1.5 rounded-full', ok ? 'bg-success' : 'bg-warning')} />
      {label}
    </span>
  )
}

function LevelPicker({
  levels,
  value,
  onChange,
}: {
  levels: Level[]
  value: string
  onChange: (id: string) => void
}) {
  return (
    <div className="mt-2.5">
      <div className="inline-flex flex-wrap items-center gap-0.5 rounded-lg bg-surface-2 p-1">
        {levels.map((lvl) => (
          <button
            key={lvl.id}
            type="button"
            onClick={() => onChange(lvl.id)}
            aria-pressed={value === lvl.id}
            title={lvl.desc}
            className={cn(
              'cursor-pointer rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
              value === lvl.id
                ? 'bg-card text-text-main shadow-[0_1px_2px_rgba(0,0,0,0.08)]'
                : 'text-text-muted hover:text-text-main',
            )}
          >
            {lvl.label}
          </button>
        ))}
      </div>
      <p className="mt-1.5 text-[11px] text-text-muted">
        {levels.find((lvl) => lvl.id === value)?.desc}
      </p>
    </div>
  )
}

function SaverRow({
  icon,
  title,
  href,
  desc,
  chip,
  children,
  right,
}: {
  icon: string
  title: string
  href?: string
  desc: string
  chip?: React.ReactNode
  children?: React.ReactNode
  right: React.ReactNode
}) {
  return (
    <div className="flex items-start gap-3 p-4 sm:p-5">
      <Icon name={icon} className="text-[18px] text-text-muted" />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <p className="text-sm font-medium text-text-main">{title}</p>
          {href && (
            <a
              href={href}
              target="_blank"
              rel="noreferrer"
              title="Source"
              aria-label={`${title} source`}
              className="text-text-muted transition-colors hover:text-text-main"
            >
              <Icon name="open_in_new" className="text-[13px]" />
            </a>
          )}
          {chip}
        </div>
        <p className="mt-0.5 text-[13px] leading-relaxed text-text-muted">{desc}</p>
        {children}
      </div>
      <div className="flex shrink-0 items-center gap-2 pt-1">{right}</div>
    </div>
  )
}

export default function TokenSaverPage() {
  const [rtkEnabled, setRtkEnabled] = useState(true)
  const [headroomEnabled, setHeadroomEnabled] = useState(false)
  const [headroomUrl, setHeadroomUrl] = useState('http://localhost:8787')
  const [headroomTimeoutMs, setHeadroomTimeoutMs] = useState<number | string>(3000)
  const [headroomStatus, setHeadroomStatus] = useState<HeadroomStatus>({
    installed: false,
    running: false,
    python: null,
    loading: true,
  })
  const [showHeadroomModal, setShowHeadroomModal] = useState(false)
  const [headroomActionLoading, setHeadroomActionLoading] = useState(false)
  const [headroomActionError, setHeadroomActionError] = useState('')
  const [headroomExtras, setHeadroomExtras] = useState<HeadroomExtras>({
    version: null,
    extras: { code: false, ml: false },
    available: ['code', 'ml'],
    loading: true,
  })
  const [pendingExtras, setPendingExtras] = useState<string[]>([])
  const [installLog, setInstallLog] = useState('')
  const [extrasActionLoading, setExtrasActionLoading] = useState(false)
  const [extrasActionError, setExtrasActionError] = useState('')
  const [removingExtra, setRemovingExtra] = useState<string | null>(null)
  const [extrasConfirm, setExtrasConfirm] = useState<Confirm | null>(null)
  const [codeAware, setCodeAware] = useState(false)
  const [kompress, setKompress] = useState(true)
  const [restartingProxy, setRestartingProxy] = useState(false)
  const logPollRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const [cavemanEnabled, setCavemanEnabled] = useState(false)
  const [cavemanLevel, setCavemanLevel] = useState('full')
  const [ponytailEnabled, setPonytailEnabled] = useState(false)
  const [ponytailLevel, setPonytailLevel] = useState('full')
  const [customPromptEnabled, setCustomPromptEnabled] = useState(false)
  const [customPrompt, setCustomPrompt] = useState('')
  const [pxpipeEnabled, setPxpipeEnabled] = useState(false)
  const [pxpipeStatus, setPxpipeStatus] = useState<PxpipeStatus>({
    installed: false,
    installing: false,
    running: false,
    version: null,
    loading: true,
  })
  const [pxpipeHealth, setPxpipeHealth] = useState<PxpipeHealth | null>(null)

  const { copied, copy } = useCopyToClipboard()

  // i18n is not ported; the dashboard locale is always `en`, so the wenyan
  // Caveman levels stay hidden exactly as they do for an English user.
  const visibleCavemanLevels = CAVEMAN_LEVELS.filter((lvl) => !lvl.wenyan)

  const patchSetting = async (patch: Record<string, unknown>) => {
    try {
      await fetch('/api/settings', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(patch),
      })
    } catch (error) {
      console.error('Error updating setting:', error)
    }
  }

  const handleRtkEnabled = async (value: boolean) => {
    try {
      const res = await fetch('/api/settings', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ rtkEnabled: value }),
      })
      if (res.ok) setRtkEnabled(value)
    } catch (error) {
      console.error('Error updating rtkEnabled:', error)
    }
  }

  const handleHeadroomEnabled = (value: boolean) => {
    const nextUrl = headroomUrl.trim() || 'http://localhost:8787'
    setHeadroomUrl(nextUrl)
    setHeadroomEnabled(value)
    patchSetting({ headroomEnabled: value, headroomUrl: nextUrl })
  }

  const handleHeadroomUrlBlur = async () => {
    const next = headroomUrl.trim() || 'http://localhost:8787'
    setHeadroomUrl(next)
    await patchSetting({ headroomUrl: next })
    refreshHeadroomStatus()
  }

  const refreshHeadroomStatus = useCallback(async () => {
    setHeadroomStatus((s) => ({ ...s, loading: true }))
    try {
      const res = await fetch('/api/headroom/status', { headers: { 'Cache-Control': 'no-store' } })
      const data = await res.json()
      setHeadroomStatus({ ...data, loading: false })
      if (!data?.installed) {
        setHeadroomExtras({
          version: null,
          extras: { code: false, ml: false },
          available: ['code', 'ml'],
          loading: false,
        })
        setPendingExtras([])
        return
      }
      try {
        const er = await fetch('/api/headroom/extras', { headers: { 'Cache-Control': 'no-store' } })
        if (!er.ok) throw new Error('extras status failed')
        const ed = await er.json()
        setHeadroomExtras((s) => ({
          ...s,
          version: ed.version ?? null,
          extras: ed.extras || { code: false, ml: false },
          available: ed.available || ['code', 'ml'],
          loading: false,
        }))
        setPendingExtras([])
      } catch {
        setHeadroomExtras({
          version: null,
          extras: { code: false, ml: false },
          available: ['code', 'ml'],
          loading: false,
        })
        setPendingExtras([])
      }
    } catch {
      setHeadroomStatus({ installed: false, running: false, python: null, loading: false })
      setHeadroomExtras({
        version: null,
        extras: { code: false, ml: false },
        available: ['code', 'ml'],
        loading: false,
      })
      setPendingExtras([])
    }
  }, [])

  const handleHeadroomStart = useCallback(async () => {
    setHeadroomActionError('')
    setHeadroomActionLoading(true)
    try {
      const res = await fetch('/api/headroom/start', { method: 'POST' })
      const data = await res.json().catch(() => ({}))
      if (!res.ok) throw new Error(errorMessage(data, 'Failed to start proxy'))
      await refreshHeadroomStatus()
    } catch (e) {
      setHeadroomActionError((e as Error).message)
    } finally {
      setHeadroomActionLoading(false)
    }
  }, [refreshHeadroomStatus])

  const handleHeadroomStop = useCallback(async () => {
    setHeadroomActionLoading(true)
    try {
      await fetch('/api/headroom/stop', { method: 'POST' })
      await refreshHeadroomStatus()
    } finally {
      setHeadroomActionLoading(false)
    }
  }, [refreshHeadroomStatus])

  const togglePendingExtra = (extra: string) => {
    setPendingExtras((cur) =>
      cur.includes(extra) ? cur.filter((e) => e !== extra) : [...cur, extra],
    )
  }

  // Poll the install log tail while a pip install/uninstall is running.
  const startLogPolling = useCallback(() => {
    setInstallLog('')
    if (logPollRef.current) clearInterval(logPollRef.current)
    const tick = async () => {
      try {
        const r = await fetch('/api/headroom/extras?log=1', {
          headers: { 'Cache-Control': 'no-store' },
        })
        const d = await r.json().catch(() => ({}))
        if (typeof d.log === 'string') setInstallLog(d.log)
      } catch {
        // ignore transient poll errors
      }
    }
    tick()
    logPollRef.current = setInterval(tick, 1500)
  }, [])

  const stopLogPolling = useCallback(() => {
    if (logPollRef.current) {
      clearInterval(logPollRef.current)
      logPollRef.current = null
    }
  }, [])

  useEffect(() => () => stopLogPolling(), [stopLogPolling])

  const installExtrasConfirmed = useCallback(async () => {
    if (pendingExtras.length === 0) return
    setExtrasActionLoading(true)
    setExtrasActionError('')
    startLogPolling()
    try {
      const res = await fetch('/api/headroom/extras', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ extras: pendingExtras }),
      })
      const data = await res.json().catch(() => ({}))
      if (!res.ok) throw new Error(errorMessage(data, 'Install failed'))
      setHeadroomExtras((s) => ({
        ...s,
        version: data.version ?? s.version,
        extras: data.extras || s.extras,
      }))
      setPendingExtras([])
    } catch (e) {
      setExtrasActionError((e as Error).message)
    } finally {
      stopLogPolling()
      setExtrasActionLoading(false)
    }
  }, [pendingExtras, startLogPolling, stopLogPolling])

  const removeExtraConfirmed = useCallback(
    async (extra: string) => {
      setRemovingExtra(extra)
      setExtrasActionError('')
      startLogPolling()
      try {
        const res = await fetch('/api/headroom/extras', {
          method: 'DELETE',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ extras: [extra] }),
        })
        const data = await res.json().catch(() => ({}))
        if (!res.ok) throw new Error(errorMessage(data, 'Remove failed'))
        setHeadroomExtras((s) => ({
          ...s,
          version: data.version ?? s.version,
          extras: data.extras || s.extras,
        }))
      } catch (e) {
        setExtrasActionError((e as Error).message)
      } finally {
        stopLogPolling()
        setRemovingExtra(null)
      }
    },
    [startLogPolling, stopLogPolling],
  )

  const handleInstallExtras = useCallback(() => {
    if (pendingExtras.length === 0) return
    // Warn about the heavy ~1GB torch download before installing [ml].
    if (pendingExtras.includes('ml')) {
      setExtrasConfirm({
        title: 'Install [ml]',
        message: '[ml] downloads ~1 GB (torch + huggingface-hub). Continue?',
        confirmText: 'Install',
        variant: 'primary',
        onConfirm: installExtrasConfirmed,
      })
      return
    }
    installExtrasConfirmed()
  }, [pendingExtras, installExtrasConfirmed])

  const handleRemoveExtra = useCallback(
    (extra: string) => {
      setExtrasConfirm({
        title: `Remove [${extra}]`,
        message: `Remove [${extra}] and its packages?`,
        confirmText: 'Remove',
        variant: 'danger',
        onConfirm: () => removeExtraConfirmed(extra),
      })
    },
    [removeExtraConfirmed],
  )

  // Toggle an extra's active state (persist setting), then restart the proxy so
  // the new --code-aware / --disable-kompress flags take effect.
  const toggleExtraActive = useCallback(
    async (extra: string, value: boolean) => {
      setExtrasActionError('')
      if (extra === 'code') setCodeAware(value)
      if (extra === 'ml') setKompress(value)
      const key = extra === 'code' ? 'headroomCodeAware' : 'headroomKompress'
      await patchSetting({ [key]: value })
      if (!headroomStatus.running) return
      setRestartingProxy(true)
      try {
        const res = await fetch('/api/headroom/restart', { method: 'POST' })
        const data = await res.json().catch(() => ({}))
        if (!res.ok) throw new Error(errorMessage(data, 'Restart failed'))
        await refreshHeadroomStatus()
      } catch (e) {
        setExtrasActionError((e as Error).message)
      } finally {
        setRestartingProxy(false)
      }
    },
    [headroomStatus.running, refreshHeadroomStatus],
  )

  const handleCavemanLevel = (level: string) => {
    setCavemanLevel(level)
    patchSetting({ cavemanLevel: level })
  }

  const handlePonytailEnabled = (value: boolean) => {
    setPonytailEnabled(value)
    patchSetting({ ponytailEnabled: value })
  }

  const handlePonytailLevel = (level: string) => {
    setPonytailLevel(level)
    patchSetting({ ponytailLevel: level })
  }

  const handleCustomPromptEnabled = (value: boolean) => {
    setCustomPromptEnabled(value)
    patchSetting({ customPromptEnabled: value })
  }

  const handleCustomPromptBlur = () => {
    patchSetting({ customPrompt })
  }

  const refreshPxpipeStatus = useCallback(async () => {
    setPxpipeStatus((s) => ({ ...s, loading: true }))
    try {
      const res = await fetch('/api/pxpipe/status', { headers: { 'Cache-Control': 'no-store' } })
      const data = await res.json()
      setPxpipeStatus({ ...data, loading: false })
    } catch {
      setPxpipeStatus({
        installed: false,
        installing: false,
        running: false,
        version: null,
        loading: false,
      })
    }
  }, [])

  const runPxpipeHealth = useCallback(async () => {
    try {
      const res = await fetch('/api/pxpipe/health', { method: 'POST' })
      setPxpipeHealth(await res.json())
    } catch (e) {
      setPxpipeHealth({ healthy: false, checks: [], error: (e as Error).message })
    }
  }, [])

  const handlePxpipeEnabled = (value: boolean) => {
    setPxpipeEnabled(value)
    patchSetting({ pxpipeEnabled: value })
  }

  const handleHeadroomTimeoutBlur = () => {
    const raw = Math.round(Number(headroomTimeoutMs))
    const next = Number.isFinite(raw) && raw > 0 ? raw : 3000
    setHeadroomTimeoutMs(next)
    patchSetting({ headroomTimeoutMs: next })
  }

  useEffect(() => {
    const loadSettings = async () => {
      try {
        const res = await fetch('/api/settings')
        if (res.ok) {
          const data = await res.json()
          setRtkEnabled(data.rtkEnabled !== false)
          setHeadroomEnabled(!!data.headroomEnabled)
          setHeadroomUrl(data.headroomUrl || 'http://localhost:8787')
          if (typeof data.headroomTimeoutMs === 'number') setHeadroomTimeoutMs(data.headroomTimeoutMs)
          setCodeAware(data.headroomCodeAware === true)
          setKompress(data.headroomKompress !== false)
          setCavemanEnabled(!!data.cavemanEnabled)
          setCavemanLevel(data.cavemanLevel || 'full')
          setPonytailEnabled(!!data.ponytailEnabled)
          setPonytailLevel(data.ponytailLevel || 'full')
          setCustomPromptEnabled(!!data.customPromptEnabled)
          setCustomPrompt(data.customPrompt || '')
          setPxpipeEnabled(!!data.pxpipeEnabled)
          refreshHeadroomStatus()
          // Run the PXPIPE health check automatically when the page opens.
          refreshPxpipeStatus().then(runPxpipeHealth)
        }
      } catch {
        // keep the defaults; each section renders its own state
      }
    }
    loadSettings()
  }, [refreshHeadroomStatus, refreshPxpipeStatus, runPxpipeHealth])

  const headroomRunning = !!headroomStatus.running
  const headroomStatusLabel = headroomStatus.loading
    ? 'Checking…'
    : headroomRunning
      ? 'Running'
      : headroomStatus.localUrl !== false && !headroomStatus.installed
        ? 'Not installed'
        : headroomStatus.localUrl !== false
          ? 'Stopped'
          : 'External'
  const headroomLocalUrl = headroomStatus.localUrl !== false
  const headroomCanStart = !!headroomStatus.canStart
  const headroomManaged = headroomLocalUrl && !!headroomStatus.managedPid

  const pxpipeHealthy = pxpipeHealth?.healthy === true
  const pxpipeStatusLabel = pxpipeStatus.loading
    ? 'Checking…'
    : pxpipeStatus.installing
      ? 'Installing…'
      : !pxpipeStatus.installed
        ? 'Not installed'
        : pxpipeHealthy
          ? 'Healthy'
          : pxpipeStatus.running
            ? 'Running'
            : 'Stopped'

  return (
    <div className="space-y-6 p-6">
      <Card id="rtk" className="overflow-hidden">
        <div className="flex items-start gap-3 border-b border-border p-5">
          <Icon name="savings" className="text-[20px] text-text-main" />
          <div className="min-w-0">
            <h2 className="text-base font-semibold tracking-tight">Token Saver</h2>
            <p className="mt-0.5 text-[13px] text-text-muted">
              Cut tokens in tool output and context, then bias replies toward brevity.
            </p>
          </div>
        </div>

        <div className="divide-y divide-border">
          <SaverRow
            icon="terminal"
            title="Compress tool output"
            href="https://github.com/rtk-ai/rtk"
            desc="git/grep/ls/tree/logs → 60–90% fewer input tokens"
            right={
              <Switch
                checked={rtkEnabled}
                onCheckedChange={() => handleRtkEnabled(!rtkEnabled)}
              />
            }
          />

          <SaverRow
            icon="layers"
            title="Compress context"
            href="https://github.com/chopratejas/headroom"
            desc="Compress prompts via /v1/compress before routing to the model"
            chip={<StatusChip label={headroomStatusLabel} ok={headroomRunning} />}
            right={
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  onClick={() => setShowHeadroomModal(true)}
                  className="inline-flex h-7 cursor-pointer items-center rounded-md px-2 text-[11px] font-medium text-text-muted transition-colors hover:bg-surface-2 hover:text-text-main"
                >
                  {headroomRunning ? 'Manage' : 'Setup'}
                </button>
                <Switch
                  checked={headroomEnabled}
                  onCheckedChange={() => handleHeadroomEnabled(!headroomEnabled)}
                />
              </div>
            }
          >
            {headroomStatus.installed && (
              <div className="mt-3 rounded-lg bg-surface-2 p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <p className="text-[11px] font-medium uppercase tracking-wide text-text-muted">
                    Compression extras
                    {headroomExtras.version ? ` · v${headroomExtras.version}` : ''}
                  </p>
                  {pendingExtras.length > 0 && (
                    <button
                      onClick={handleInstallExtras}
                      disabled={extrasActionLoading}
                      className="cursor-pointer rounded-md bg-primary px-2.5 py-1 text-[11px] font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:opacity-50"
                    >
                      {extrasActionLoading
                        ? 'Installing…'
                        : `Install [proxy,${pendingExtras.join(',')}]`}
                    </button>
                  )}
                </div>

                <div className="mt-2 flex flex-wrap items-center gap-2">
                  {headroomExtras.available.map((extra) => {
                    const installed = !!headroomExtras.extras[extra]
                    const pending = pendingExtras.includes(extra)
                    const extraTitle =
                      extra === 'code'
                        ? 'tree-sitter AST compression for code responses'
                        : 'Kompress-v2 HF model for prose/agentic traces (~+1GB)'

                    if (installed) {
                      const active = extra === 'code' ? codeAware : kompress
                      return (
                        <div
                          key={extra}
                          className="flex items-center gap-2 rounded-md bg-card px-2 py-1 text-xs text-text-main"
                          title={extraTitle}
                        >
                          <Switch
                            checked={active}
                            disabled={restartingProxy}
                            onCheckedChange={() => toggleExtraActive(extra, !active)}
                          />
                          <span className="font-mono text-[11px] font-medium">[{extra}]</span>
                          <button
                            type="button"
                            onClick={() => handleRemoveExtra(extra)}
                            disabled={removingExtra === extra}
                            className="cursor-pointer text-[11px] text-danger underline-offset-2 hover:underline disabled:opacity-50"
                            title={`Uninstall [${extra}]`}
                          >
                            {removingExtra === extra ? 'Uninstalling…' : 'Uninstall'}
                          </button>
                        </div>
                      )
                    }

                    return (
                      <label
                        key={extra}
                        className={cn(
                          'flex cursor-pointer items-center gap-2 rounded-md px-2 py-1 text-xs transition-colors',
                          pending
                            ? 'bg-card text-text-main'
                            : 'text-text-muted hover:bg-card hover:text-text-main',
                        )}
                        title={extraTitle}
                      >
                        <input
                          type="checkbox"
                          className="size-3 accent-primary"
                          checked={pending}
                          onChange={() => togglePendingExtra(extra)}
                        />
                        <span className="font-mono text-[11px] font-medium">[{extra}]</span>
                        <span className="text-[11px] opacity-70">
                          {pending ? 'selected' : 'install'}
                        </span>
                      </label>
                    )
                  })}
                </div>

                {extrasActionError && (
                  <p className="mt-2 text-[11px] text-danger">{extrasActionError}</p>
                )}
                {restartingProxy && (
                  <p className="mt-2 text-[11px] text-text-muted">Restarting proxy…</p>
                )}
                {(extrasActionLoading || removingExtra) && installLog && (
                  <pre className="mt-2 max-h-32 overflow-auto whitespace-pre-wrap rounded-md bg-card p-2 text-[10px] leading-tight text-text-muted">
                    {installLog}
                  </pre>
                )}
                <p className="mt-2 text-[11px] leading-relaxed text-text-muted">
                  Default install is <code className="font-mono">[proxy]</code> (SmartCrusher for
                  JSON). <code className="font-mono">[code]</code> adds AST compression for
                  Python/JS/TS/Go/Rust/Java/C/C++/Perl. <code className="font-mono">[ml]</code> adds
                  the Kompress-v2 HF model (~1 GB with torch).
                </p>
              </div>
            )}
          </SaverRow>

          <SaverRow
            icon="chat"
            title="Compress LLM output"
            href="https://github.com/JuliusBrussee/caveman"
            desc="Terse-style system prompt → ~65% fewer output tokens (up to 87%)"
            right={
              <Switch
                checked={cavemanEnabled}
                onCheckedChange={() => {
                  const next = !cavemanEnabled
                  setCavemanEnabled(next)
                  patchSetting({ cavemanEnabled: next })
                }}
              />
            }
          >
            {cavemanEnabled && (
              <LevelPicker
                levels={visibleCavemanLevels}
                value={cavemanLevel}
                onChange={handleCavemanLevel}
              />
            )}
          </SaverRow>

          <SaverRow
            icon="codeblock"
            title="Lazy senior dev"
            href="https://github.com/DietrichGebert/ponytail"
            desc="Bias the model toward minimal code: YAGNI, reuse stdlib, deletion over addition"
            right={
              <Switch
                checked={ponytailEnabled}
                onCheckedChange={() => handlePonytailEnabled(!ponytailEnabled)}
              />
            }
          >
            {ponytailEnabled && (
              <LevelPicker
                levels={PONYTAIL_LEVELS}
                value={ponytailLevel}
                onChange={handlePonytailLevel}
              />
            )}
          </SaverRow>

          <SaverRow
            icon="edit"
            title="Custom system prompt"
            href=""
            desc="Appends your own instruction to every request, independently of the savers above"
            right={
              <Switch
                checked={customPromptEnabled}
                onCheckedChange={() => handleCustomPromptEnabled(!customPromptEnabled)}
              />
            }
          >
            {customPromptEnabled && (
              <div className="mt-3">
                <textarea
                  id="custom-prompt"
                  aria-label="Custom system prompt"
                  value={customPrompt}
                  onChange={(e) => setCustomPrompt(e.target.value)}
                  onBlur={handleCustomPromptBlur}
                  rows={4}
                  placeholder="e.g. Prefer editing existing files over creating new ones."
                  className="w-full resize-y rounded-lg bg-surface-2 p-2.5 text-xs text-text-main outline-none placeholder:text-text-muted focus-visible:ring-1 focus-visible:ring-ring"
                />
                <p className="mt-1.5 text-[11px] text-text-muted">
                  Applied to every provider and model. Sent as-is upstream; an empty prompt injects
                  nothing.
                </p>
              </div>
            )}
          </SaverRow>

          {/* PXPIPE stays hidden from the UI — experimental, not exposed yet. */}
          {false && (
            <SaverRow
              icon="image"
              title="Compress prompts as images"
              href="https://github.com/teamchong/pxpipe"
              desc="Transforms large textual context into optimized images before sending to the LLM."
              chip={
                <StatusChip
                  label={pxpipeStatusLabel}
                  ok={pxpipeHealthy || pxpipeStatus.running}
                />
              }
              right={
                <Switch
                  checked={pxpipeEnabled}
                  disabled={!pxpipeStatus.installed}
                  onCheckedChange={() => handlePxpipeEnabled(!pxpipeEnabled)}
                />
              }
            />
          )}
        </div>
      </Card>

      <Modal
        isOpen={showHeadroomModal}
        title={headroomRunning ? 'Headroom' : 'Setup Headroom'}
        onClose={() => setShowHeadroomModal(false)}
      >
        <div className="flex flex-col gap-4">
          <div className="flex items-center justify-between text-sm">
            <span>Status</span>
            <span className={headroomRunning ? 'text-success' : 'text-warning'}>
              {headroomStatusLabel}
            </span>
          </div>
          {headroomRunning && (
            <a
              href="/api/headroom/proxy/dashboard"
              target="_blank"
              rel="noreferrer"
              className="w-full rounded border border-border px-4 py-2 text-center text-sm hover:bg-surface-2"
            >
              Open Headroom Dashboard
            </a>
          )}
          <div className="flex flex-col gap-1">
            <p className="text-sm font-medium">Proxy URL</p>
            <Input
              value={headroomUrl}
              onChange={(e) => setHeadroomUrl(e.target.value)}
              onBlur={handleHeadroomUrlBlur}
              placeholder="http://localhost:8787"
              className="font-mono text-sm"
            />
            <p className="text-xs text-text-muted">
              Use a local proxy for Start/Stop, or an external Docker sidecar like
              http://headroom:8787.
            </p>
          </div>
          <div className="flex flex-col gap-1">
            <p className="text-sm font-medium">Timeout (ms)</p>
            <Input
              value={String(headroomTimeoutMs)}
              onChange={(e) => setHeadroomTimeoutMs(e.target.value)}
              onBlur={handleHeadroomTimeoutBlur}
              placeholder="3000"
              className="font-mono text-sm"
            />
            <p className="text-xs text-text-muted">
              Request timeout in milliseconds. Defaults to 3000 ms.
            </p>
          </div>
          {headroomManaged ? (
            <Button
              onClick={handleHeadroomStop}
              variant="ghost"
              className="w-full"
              disabled={headroomActionLoading}
            >
              {headroomActionLoading ? 'Stopping…' : 'Stop Headroom'}
            </Button>
          ) : headroomRunning ? (
            <p className="text-sm text-success">
              Headroom proxy is reachable. You can enable the token saver.
            </p>
          ) : headroomCanStart ? (
            <Button onClick={handleHeadroomStart} className="w-full" disabled={headroomActionLoading}>
              {headroomActionLoading ? 'Starting…' : 'Start Headroom'}
            </Button>
          ) : !headroomLocalUrl ? (
            <p className="text-sm text-warning">
              Start Headroom separately at the configured URL, then recheck.
            </p>
          ) : !headroomStatus.python ? (
            <p className="text-sm text-warning">
              Python ≥ 3.10 required for local managed mode. Install Python first, or use an
              external proxy URL.
            </p>
          ) : (
            <div className="flex flex-col gap-1">
              <p className="text-sm font-medium">Install then click Start:</p>
              <div className="flex items-center gap-2">
                <pre className="flex-1 overflow-x-auto rounded bg-black/5 p-2 font-mono text-xs dark:bg-white/5">
                  {'pip install "headroom-ai[proxy]"'}
                </pre>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => copy('pip install "headroom-ai[proxy]"')}
                >
                  {copied ? 'Copied' : 'Copy'}
                </Button>
              </div>
            </div>
          )}
          {headroomActionError && <p className="text-sm text-warning">{headroomActionError}</p>}
          <div className="flex gap-2">
            <Button onClick={() => refreshHeadroomStatus()} variant="ghost" className="w-full">
              Recheck
            </Button>
            <Button onClick={() => setShowHeadroomModal(false)} className="w-full">
              Done
            </Button>
          </div>
        </div>
      </Modal>

      <ConfirmModal
        isOpen={!!extrasConfirm}
        onClose={() => setExtrasConfirm(null)}
        onConfirm={() => {
          const fn = extrasConfirm?.onConfirm
          setExtrasConfirm(null)
          fn?.()
        }}
        title={extrasConfirm?.title}
        message={extrasConfirm?.message}
        confirmLabel={extrasConfirm?.confirmText}
        variant={extrasConfirm?.variant}
      />
    </div>
  )
}
