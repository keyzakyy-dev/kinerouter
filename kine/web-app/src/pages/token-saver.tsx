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
  const [pxpipeMinChars, setPxpipeMinChars] = useState<number | string>(25000)
  const [pxpipeStatus, setPxpipeStatus] = useState<PxpipeStatus>({
    installed: false,
    installing: false,
    running: false,
    version: null,
    loading: true,
  })
  const [pxpipeHealth, setPxpipeHealth] = useState<PxpipeHealth | null>(null)
  const [pxpipeActionLoading, setPxpipeActionLoading] = useState(false)
  const [pxpipeActionError, setPxpipeActionError] = useState('')

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
      console.log('Error updating setting:', error)
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
      console.log('Error updating rtkEnabled:', error)
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
      if (typeof data.minChars === 'number') setPxpipeMinChars(data.minChars)
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

  const pxpipeAction = useCallback(
    async (endpoint: string) => {
      setPxpipeActionError('')
      setPxpipeActionLoading(true)
      try {
        const res = await fetch(`/api/pxpipe/${endpoint}`, { method: 'POST' })
        const data = await res.json().catch(() => ({}))
        if (!res.ok) throw new Error(errorMessage(data, `PXPIPE ${endpoint} failed`))
        await refreshPxpipeStatus()
        await runPxpipeHealth()
      } catch (e) {
        setPxpipeActionError((e as Error).message)
      } finally {
        setPxpipeActionLoading(false)
      }
    },
    [refreshPxpipeStatus, runPxpipeHealth],
  )

  const handlePxpipeEnabled = (value: boolean) => {
    setPxpipeEnabled(value)
    patchSetting({ pxpipeEnabled: value })
  }

  const handlePxpipeMinCharsBlur = () => {
    const next = Math.max(0, Number(pxpipeMinChars) || 25000)
    setPxpipeMinChars(next)
    patchSetting({ pxpipeMinChars: next })
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
          if (typeof data.pxpipeMinChars === 'number') setPxpipeMinChars(data.pxpipeMinChars)
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
  const pxpipeChipClass =
    pxpipeHealthy || pxpipeStatus.running
      ? 'bg-success/15 text-success'
      : 'bg-warning/15 text-warning'

  return (
    <div className="space-y-6 p-6">
      <Card id="rtk">
        <div className="mb-2 flex items-center justify-between">
          <h2 className="flex items-center gap-2 text-lg font-semibold">
            <Icon name="bolt" className="text-[22px] text-primary" />
            Token Saver
          </h2>
        </div>
        <div className="flex items-center justify-between gap-4 border-b border-border pb-4 pt-2">
          <div className="min-w-0 flex-1">
            <p className="font-medium">
              Compress tool output{' '}
              <a
                href="https://github.com/rtk-ai/rtk"
                target="_blank"
                rel="noreferrer"
                className="text-xs font-normal text-primary underline hover:opacity-80"
              >
                (RTK)
              </a>
            </p>
            <p className="text-sm text-text-muted">git/grep/ls/tree/logs → 60-90% fewer input tokens</p>
          </div>
          <Switch
            checked={rtkEnabled}
            onCheckedChange={() => handleRtkEnabled(!rtkEnabled)}
          />
        </div>

        <div className="flex flex-wrap items-center justify-between gap-4 py-4">
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-3">
              <p className="font-medium">
                Compress context{' '}
                <a
                  href="https://github.com/chopratejas/headroom"
                  target="_blank"
                  rel="noreferrer"
                  className="text-xs font-normal text-primary underline hover:opacity-80"
                >
                  (Headroom)
                </a>
              </p>
              <span
                className={cn(
                  'rounded px-2 py-0.5 text-xs',
                  headroomRunning ? 'bg-success/15 text-success' : 'bg-warning/15 text-warning',
                )}
              >
                {headroomStatusLabel}
              </span>
              <button
                type="button"
                onClick={() => setShowHeadroomModal(true)}
                className="cursor-pointer text-xs text-primary underline hover:opacity-80"
              >
                {headroomRunning ? 'Manage' : 'Setup'}
              </button>
            </div>
            <p className="mt-1 text-sm text-text-muted">
              Compress prompts via /v1/compress before routing to the model
            </p>
          </div>
          <Switch
            checked={headroomEnabled}
            onCheckedChange={() => handleHeadroomEnabled(!headroomEnabled)}
          />
        </div>

        {headroomStatus.installed && (
          <div className="mb-3 ml-1 border-l-2 border-border pb-4 pl-3">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-xs text-text-muted">
                Compression extras
                {headroomExtras.version ? ` · v${headroomExtras.version}` : ''}:
              </span>
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
                      className="flex items-center gap-1.5 rounded border border-success/40 bg-success/5 px-2 py-1 text-xs text-text-main"
                      title={extraTitle}
                    >
                      <Switch
                        checked={active}
                        disabled={restartingProxy}
                        onCheckedChange={() => toggleExtraActive(extra, !active)}
                      />
                      <span className="font-medium">[{extra}]</span>
                      <button
                        type="button"
                        onClick={() => handleRemoveExtra(extra)}
                        disabled={removingExtra === extra}
                        className="ml-1 cursor-pointer text-danger underline hover:opacity-80 disabled:opacity-50"
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
                      'flex cursor-pointer items-center gap-1.5 rounded border px-2 py-1 text-xs transition-colors',
                      pending
                        ? 'border-primary bg-primary/10 text-primary'
                        : 'border-border text-text-muted hover:bg-surface-2',
                    )}
                    title={extraTitle}
                  >
                    <input
                      type="checkbox"
                      className="size-3"
                      checked={pending}
                      onChange={() => togglePendingExtra(extra)}
                    />
                    <span className="font-medium">[{extra}]</span>
                    <span className="opacity-70">not installed</span>
                  </label>
                )
              })}
              {pendingExtras.length > 0 && (
                <button
                  onClick={handleInstallExtras}
                  disabled={extrasActionLoading}
                  className="cursor-pointer rounded bg-primary px-2.5 py-1 text-xs text-white hover:opacity-90 disabled:opacity-50"
                >
                  {extrasActionLoading ? 'Installing…' : `Install [proxy,${pendingExtras.join(',')}]`}
                </button>
              )}
            </div>
            {extrasActionError && <p className="mt-1 text-xs text-danger">{extrasActionError}</p>}
            {restartingProxy && <p className="mt-1 text-xs text-text-muted">Restarting proxy…</p>}
            {(extrasActionLoading || removingExtra) && installLog && (
              <pre className="mt-2 max-h-32 overflow-auto whitespace-pre-wrap rounded bg-surface-2 p-2 text-[10px] leading-tight text-text-muted">
                {installLog}
              </pre>
            )}
            <p className="mt-1 text-xs text-text-muted">
              Installing adds the package; use <code>on</code>/<code>off</code> to activate it
              (restarts the proxy). Default install is <code>[proxy]</code> only (SmartCrusher for
              JSON). Adding <code>[code]</code> enables AST compression
              (Python/JS/TS/Go/Rust/Java/C/C++/Perl). Adding <code>[ml]</code> enables the
              Kompress-v2 HF model for prose/agentic traces but adds ~1 GB (torch +
              huggingface-hub).
            </p>
          </div>
        )}

        <div className="flex flex-wrap items-center justify-between gap-4 border-t border-border pt-4">
          <div className="min-w-0 flex-1">
            <p className="font-medium">
              Compress LLM output{' '}
              <a
                href="https://github.com/JuliusBrussee/caveman"
                target="_blank"
                rel="noreferrer"
                className="text-xs font-normal text-primary underline hover:opacity-80"
              >
                (Caveman)
              </a>
            </p>
            <p className="text-sm text-text-muted">
              Terse-style system prompt → ~65% fewer output tokens (up to 87%)
            </p>
          </div>
          <div className="flex shrink-0 items-center gap-3">
            {cavemanEnabled && (
              <div className="flex flex-col items-end gap-1">
                <div className="flex items-center gap-1.5">
                  {visibleCavemanLevels.map((lvl) => (
                    <button
                      key={lvl.id}
                      onClick={() => handleCavemanLevel(lvl.id)}
                      className={cn(
                        'cursor-pointer rounded border px-3 py-1.5 text-xs font-medium transition-colors',
                        cavemanLevel === lvl.id
                          ? 'border-primary bg-primary text-white'
                          : 'border-border bg-transparent text-text-muted hover:bg-surface-2',
                      )}
                      title={lvl.desc}
                    >
                      {lvl.label}
                    </button>
                  ))}
                </div>
                <p className="text-xs text-primary">
                  {CAVEMAN_LEVELS.find((lvl) => lvl.id === cavemanLevel)?.desc}
                </p>
              </div>
            )}
            <Switch
              checked={cavemanEnabled}
              onCheckedChange={() => {
                const next = !cavemanEnabled
                setCavemanEnabled(next)
                patchSetting({ cavemanEnabled: next })
              }}
            />
          </div>
        </div>

        <div className="mt-4 flex flex-wrap items-center justify-between gap-4 border-t border-border pt-4">
          <div className="min-w-0 flex-1">
            <p className="font-medium">
              Lazy senior dev{' '}
              <a
                href="https://github.com/DietrichGebert/ponytail"
                target="_blank"
                rel="noreferrer"
                className="text-xs font-normal text-primary underline hover:opacity-80"
              >
                (Ponytail)
              </a>
            </p>
            <p className="text-sm text-text-muted">
              Bias the model toward minimal code: YAGNI, reuse stdlib, deletion over addition
            </p>
          </div>
          <div className="flex shrink-0 items-center gap-3">
            {ponytailEnabled && (
              <div className="flex flex-col items-end gap-1">
                <div className="flex items-center gap-1.5">
                  {PONYTAIL_LEVELS.map((lvl) => (
                    <button
                      key={lvl.id}
                      onClick={() => handlePonytailLevel(lvl.id)}
                      className={cn(
                        'cursor-pointer rounded border px-3 py-1.5 text-xs font-medium transition-colors',
                        ponytailLevel === lvl.id
                          ? 'border-primary bg-primary text-white'
                          : 'border-border bg-transparent text-text-muted hover:bg-surface-2',
                      )}
                      title={lvl.desc}
                    >
                      {lvl.label}
                    </button>
                  ))}
                </div>
                <p className="text-xs text-primary">
                  {PONYTAIL_LEVELS.find((lvl) => lvl.id === ponytailLevel)?.desc}
                </p>
              </div>
            )}
            <Switch
              checked={ponytailEnabled}
              onCheckedChange={() => handlePonytailEnabled(!ponytailEnabled)}
            />
          </div>
        </div>

        <div className="mt-4 flex flex-wrap items-center justify-between gap-4 border-t border-border pt-4">
          <div className="min-w-0 flex-1">
            <p className="font-medium">Custom system prompt</p>
            <p className="text-sm text-text-muted">
              Appends your own instruction to every request, independently of the savers above
            </p>
          </div>
          <Switch
            checked={customPromptEnabled}
            onCheckedChange={() => handleCustomPromptEnabled(!customPromptEnabled)}
          />
        </div>
        {customPromptEnabled && (
          <div className="pt-3">
            <textarea
              id="custom-prompt"
              aria-label="Custom system prompt"
              value={customPrompt}
              onChange={(e) => setCustomPrompt(e.target.value)}
              onBlur={handleCustomPromptBlur}
              rows={4}
              placeholder="e.g. Prefer editing existing files over creating new ones."
              className="w-full rounded border border-border bg-transparent p-2 text-xs"
            />
            <p className="mt-1 text-xs text-text-muted">
              Applied to every provider and model. Sent as-is to upstream; an empty prompt injects
              nothing.
            </p>
          </div>
        )}

        {/* PXPIPE stays hidden from the UI — experimental, not exposed yet. */}
        {false && (
          <div className="mt-4 flex flex-wrap items-center justify-between gap-4 border-t border-border pt-4">
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-3">
                <p className="font-medium">
                  Compress prompts as images{' '}
                  <a
                    href="https://github.com/teamchong/pxpipe"
                    target="_blank"
                    rel="noreferrer"
                    className="text-xs font-normal text-primary underline hover:opacity-80"
                  >
                    (PXPIPE)
                  </a>
                </p>
                <span className={cn('rounded px-2 py-0.5 text-xs', pxpipeChipClass)}>
                  {pxpipeStatusLabel}
                </span>
              </div>
              <p className="mt-1 text-sm text-text-muted">
                Transforms large textual context into optimized images before sending to the LLM.
              </p>
            </div>
            <Switch
              checked={pxpipeEnabled}
              disabled={!pxpipeStatus.installed}
              onCheckedChange={() => handlePxpipeEnabled(!pxpipeEnabled)}
            />
          </div>
        )}
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
