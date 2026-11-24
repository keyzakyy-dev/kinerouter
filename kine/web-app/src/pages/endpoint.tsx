import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type ApiKey, type TunnelStatus } from '@/lib/api'
import { maskSecret } from '@/lib/format'
import { PageHeader } from '@/components/layout'
import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { Icon } from '@/components/icon'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogTitle,
} from '@/components/ui/dialog'
import { Input, Label } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { ConfirmModal } from '@/components/modal'
import { ModelSelectModal, type PickerModel } from '@/components/model-select-modal'

const PING_MAX_MS = 90_000
const PING_INTERVAL_MS = 2_000

/** Polls /api/health on each candidate URL until one answers or the budget runs out. */
async function waitReachable(urls: string[], live: () => boolean): Promise<boolean> {
  const start = Date.now()
  while (Date.now() - start < PING_MAX_MS) {
    await new Promise((r) => setTimeout(r, PING_INTERVAL_MS))
    if (!live()) return false
    for (const u of urls.filter(Boolean)) {
      try {
        const res = await fetch(`${u}/api/health`, { mode: 'cors', cache: 'no-store' })
        if (res.ok) return true
      } catch { /* not ready yet */ }
    }
  }
  return false
}

/** Polls `probe` every 3s for ~2min; true as soon as it answers true. */
async function pollUntil(probe: () => Promise<boolean>, live: () => boolean): Promise<boolean> {
  for (let i = 0; i < 40; i++) {
    await new Promise((r) => setTimeout(r, 3000))
    if (!live()) return false
    try { if (await probe()) return true } catch { /* retry */ }
  }
  return false
}

/**
 * One way in, other than the local address. These two sit side by side because
 * they are parallel choices, not a list: pick Tunnel or pick Tailscale. The
 * toggle lives in the tile header, the address (or the reason there is none)
 * in the body.
 */
function RouteTile({
  label,
  icon,
  action,
  url,
  status,
  enabled,
}: {
  label: string
  icon: string
  action?: React.ReactNode
  url?: string
  status?: React.ReactNode
  enabled: boolean
}) {
  return (
    <div className="flex flex-col gap-2.5 rounded-lg bg-surface-2 p-3.5">
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <Icon name={icon} className="shrink-0 text-[16px] text-text-muted" />
          <div className="flex min-w-0 flex-col gap-0.5">
            <span className="truncate text-sm font-medium text-text-main">{label}</span>
            <span className="inline-flex items-center gap-1.5 text-[11px] leading-none text-text-muted">
              <span aria-hidden className={enabled ? 'size-1.5 rounded-full bg-success' : 'size-1.5 rounded-full bg-text-muted/40'} />
              {enabled ? 'Connected' : 'Not enabled'}
            </span>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">{action}</div>
      </div>
      <div className="flex min-h-7 items-center">
        {url ? (
          <div className="flex min-w-0 flex-1 items-center gap-1 rounded-md bg-card px-2 py-1">
            <code className="min-w-0 flex-1 truncate font-mono text-[11px] text-text-muted">{url}/v1</code>
            <CopyButton value={`${url}/v1`} label={`Copy ${label} URL`} />
          </div>
        ) : (
          <div className="flex items-center text-[11px] text-text-muted">{status}</div>
        )}
      </div>
    </div>
  )
}

function Spinner({ children }: { children: React.ReactNode }) {
  return (
    <span className="flex items-center gap-1.5 text-[11px] text-text-muted">
      <span className="size-3 animate-spin rounded-full border border-current border-t-transparent" />
      {children}
    </span>
  )
}

function SecurityWarning({ message, action }: { message: string; action?: { label: string; href: string } }) {
  return (
    <div className="slide-in-top flex items-center gap-2.5 rounded-lg bg-surface-2 px-3 py-2 text-[11px] text-text-muted">
      <Icon name="warning" className="size-4 shrink-0 animate-pulse text-warning" />
      <span className="flex-1">{message}</span>
      {action && (
        <a
          href={action.href}
          className="shrink-0 rounded-md bg-card px-2.5 py-1 text-[11px] font-medium text-text-main shadow-[0_1px_2px_rgba(0,0,0,0.06)] transition-colors hover:bg-surface-3"
        >
          {action.label}
        </a>
      )}
    </div>
  )
}

function TunnelSection() {
  const [requireLogin, setRequireLogin] = useState(true)
  const [hasPassword, setHasPassword] = useState(true)
  const [requireApiKey, setRequireApiKey] = useState(false)
  const [dashAccess, setDashAccess] = useState(true)

  const [tunnelUrl, setTunnelUrl] = useState('')
  const [tunnelEnabled, setTunnelEnabled] = useState(false)
  const [tunnelBusy, setTunnelBusy] = useState(false)
  const [tunnelProgress, setTunnelProgress] = useState('')
  const [tunnelError, setTunnelError] = useState('')
  const [checking, setChecking] = useState(true)
  const [unreachable, setUnreachable] = useState(false)

  const [tsUrl, setTsUrl] = useState('')
  const [tsEnabled, setTsEnabled] = useState(false)
  const [tsBusy, setTsBusy] = useState(false)
  const [tsProgress, setTsProgress] = useState('')
  const [tsError, setTsError] = useState('')
  const [tsAuthUrl, setTsAuthUrl] = useState('')
  const [installOpen, setInstallOpen] = useState(false)
  const [sudoPassword, setSudoPassword] = useState('')
  const [installLog, setInstallLog] = useState<string[]>([])

  const alive = useRef(true)
  useEffect(() => () => { alive.current = false }, [])

  const applyStatus = useCallback((data: TunnelStatus) => {
    setTunnelUrl(data.tunnel.publicUrl || data.tunnel.tunnelUrl || '')
    setTunnelEnabled(data.tunnel.enabled)
    setTsUrl(data.tailscale.tunnelUrl || '')
    setTsEnabled(data.tailscale.enabled)
  }, [])

  const refresh = useCallback(async () => {
    try { applyStatus(await api.tunnelStatus()) } catch { /* not critical */ }
  }, [applyStatus])

  useEffect(() => {
    api.tunnelStatus()
      .then(async (data) => {
        applyStatus(data)
        const url = data.tunnel.publicUrl || data.tunnel.tunnelUrl || ''
        if (data.tunnel.enabled && url) {
          const ok = await waitReachable([url], () => alive.current)
          if (alive.current) setUnreachable(!ok)
        }
      })
      .catch(() => {})
      .finally(() => { if (alive.current) setChecking(false) })
    api.authStatus()
      .then((s) => {
        setRequireLogin(s.requireLogin !== false)
        setHasPassword(s.hasPassword !== false)
        setRequireApiKey(!!s.requireApiKey)
      })
      .catch(() => {})
    api.settings()
      .then((s) => setDashAccess(s.tunnelDashboardAccess !== false))
      .catch(() => {})
  }, [applyStatus])

  const unsafeReason = !requireLogin
    ? 'Enable "Require login" and set a custom password before activating the tunnel.'
    : 'Change the default dashboard password before activating the tunnel.'
  const loginUnsafe = !requireLogin || !hasPassword

  const gate = (): string => {
    if (loginUnsafe) return `Security required: ${unsafeReason}`
    if (!requireApiKey) return 'Security required: Enable "Require API key" before activating the tunnel.'
    return ''
  }

  const enableTunnel = async () => {
    const blocked = gate()
    if (blocked) { setTunnelError(blocked); return }
    setTunnelBusy(true)
    setTunnelError('')
    setTunnelProgress('Creating tunnel…')
    let polling = true
    void (async () => {
      while (polling && alive.current) {
        try {
          const s = await api.tunnelStatus()
          if (s.download.downloading) setTunnelProgress(`Downloading cloudflared… ${s.download.progress}%`)
        } catch { /* ignore */ }
        await new Promise((r) => setTimeout(r, 1000))
      }
    })()
    try {
      const res = await api.tunnelEnable()
      polling = false
      setTunnelUrl(res.publicUrl || res.tunnelUrl)
      setTunnelProgress('Waiting for tunnel…')
      const ok = await waitReachable([res.publicUrl, res.tunnelUrl], () => alive.current)
      if (ok) setTunnelEnabled(true)
      else setTunnelError('Tunnel created but not reachable. Please try again.')
    } catch (e) {
      setTunnelError(e instanceof Error ? e.message : 'Failed to enable tunnel')
    } finally {
      polling = false
      if (alive.current) { setTunnelBusy(false); setTunnelProgress('') }
    }
  }

  const disableTunnel = async () => {
    setTunnelBusy(true)
    try {
      await api.tunnelDisable()
      setTunnelEnabled(false)
      setTunnelUrl('')
      setTunnelError('')
    } catch (e) {
      setTunnelError(e instanceof Error ? e.message : 'Failed to disable tunnel')
    } finally {
      setTunnelBusy(false)
    }
  }

  const connectTailscale = async () => {
    setTsBusy(true)
    setTsError('')
    setTsAuthUrl('')
    setTsProgress('Connecting…')
    try {
      for (let attempt = 0; attempt < 2; attempt++) {
        const res = await api.tailscaleEnable()
        if (res.success) {
          setTsUrl(res.tunnelUrl ?? '')
          setTsProgress('Waiting for Tailscale…')
          const ok = await waitReachable([res.tunnelUrl ?? ''], () => alive.current)
          setTsEnabled(true)
          if (!ok) setTsError('Connected but not reachable yet.')
          return
        }
        if (res.needsLogin && res.authUrl) {
          setTsAuthUrl(res.authUrl)
          setTsProgress('Login required. Finish login to continue')
          const loggedIn = await pollUntil(async () => (await api.tailscaleCheck()).loggedIn, () => alive.current)
          if (!loggedIn) { setTsError('Login timed out. Please try again.'); return }
          continue
        }
        if (res.funnelNotEnabled && res.enableUrl) {
          setTsAuthUrl(res.enableUrl)
          setTsProgress('Enable Funnel in the Tailscale admin console')
          const ready = await pollUntil(async () => (await api.tailscaleEnable()).success === true, () => alive.current)
          if (!ready) { setTsError('Timed out waiting for Funnel to be enabled.'); return }
          await refresh()
          return
        }
        setTsError(res.error || 'Failed to connect')
        return
      }
    } catch (e) {
      setTsError(e instanceof Error ? e.message : 'Failed to connect')
    } finally {
      if (alive.current) { setTsBusy(false); setTsProgress(''); setTsAuthUrl('') }
    }
  }

  const openTailscale = async () => {
    const blocked = gate()
    if (blocked) { setTsError(blocked); return }
    setTsError('')
    try {
      const check = await api.tailscaleCheck()
      if (check.installed && check.loggedIn) void connectTailscale()
      else setInstallOpen(true)
    } catch {
      setInstallOpen(true)
    }
  }

  const installTailscale = async () => {
    setInstallLog([])
    setTsError('')
    try {
      const done = await api.tailscaleInstall(sudoPassword, (msg) =>
        setInstallLog((prev) => [...prev.slice(-50), msg]),
      )
      setSudoPassword('')
      setInstallOpen(false)
      if (done.authUrl) setTsAuthUrl(done.authUrl)
      void connectTailscale()
    } catch (e) {
      setTsError(e instanceof Error ? e.message : 'Install failed')
    }
  }

  const disableTailscale = async () => {
    setTsBusy(true)
    try {
      await api.tailscaleDisable()
      setTsEnabled(false)
      setTsUrl('')
    } catch (e) {
      setTsError(e instanceof Error ? e.message : 'Failed to disable Tailscale')
    } finally {
      setTsBusy(false)
    }
  }

  const setDashAccessAndSave = async (value: boolean) => {
    setDashAccess(value)
    try { await api.patchSettings({ tunnelDashboardAccess: value }) } catch { /* revert on failure */ setDashAccess(!value) }
  }

  const exposed = tunnelEnabled || tsEnabled

  return (
    <div className="flex flex-col gap-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <RouteTile
          label="Cloudflare Tunnel"
          icon="cloud"
          enabled={tunnelEnabled}
          url={tunnelEnabled && !tunnelBusy ? tunnelUrl : undefined}
          status={
            tunnelBusy ? (
              <Spinner>{tunnelProgress || 'Creating tunnel…'}</Spinner>
            ) : tunnelEnabled && unreachable && !checking ? (
              <span className="inline-flex items-center gap-1.5 text-xs text-warning">
                <span className="size-3 animate-spin rounded-full border border-current border-t-transparent" />
                Reconnecting…
              </span>
            ) : checking ? (
              <Spinner>Checking…</Spinner>
            ) : (
              'Not enabled'
            )
          }
          action={
            tunnelBusy ? (
              <button
                title="Stop"
                className="flex size-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-3 hover:text-danger"
                onClick={() => setTunnelBusy(false)}
              >
                <Icon name="power_off" className="text-[16px]" />
              </button>
            ) : tunnelEnabled ? (
              <button
                title="Disable tunnel"
                className="flex size-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-3 hover:text-danger"
                onClick={disableTunnel}
              >
                <Icon name="power_off" className="text-[16px]" />
              </button>
            ) : checking ? undefined : (
              <Button size="sm" variant="ghost" onClick={enableTunnel}>
                <Icon name="cloud" /> Enable
              </Button>
            )
          }
        />
        <RouteTile
          label="Tailscale"
          icon="globe"
          enabled={tsEnabled}
          url={tsEnabled && !tsBusy ? tsUrl : undefined}
          status={tsBusy ? <Spinner>{tsProgress}</Spinner> : 'Not enabled'}
          action={
            <>
              {tsAuthUrl && !tsBusy && (
                <Button size="sm" variant="ghost" onClick={() => window.open(tsAuthUrl, 'tailscale_auth', 'width=600,height=700,noopener,noreferrer')}>
                  Open login page
                </Button>
              )}
              {tsEnabled ? (
                <button
                  title="Disable Tailscale"
                  className="flex size-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-3 hover:text-danger"
                  onClick={disableTailscale}
                >
                  <Icon name="power_off" className="text-[16px]" />
                </button>
              ) : tsBusy ? undefined : (
                <Button size="sm" variant="ghost" onClick={openTailscale}>
                  <Icon name="open_in_new" /> Enable
                </Button>
              )}
            </>
          }
        />
      </div>

      {tunnelError && !tunnelEnabled && (
        <div className="slide-in-top rounded-lg bg-surface-2 px-3 py-2 text-[11px] text-danger">{tunnelError}</div>
      )}
      {tsError && (
        <div className="slide-in-top rounded-lg bg-surface-2 px-3 py-2 text-[11px] text-danger">{tsError}</div>
      )}

      {loginUnsafe && !exposed && (
        <SecurityWarning message={unsafeReason} action={{ label: 'Open settings', href: '/dashboard/settings' }} />
      )}

      {exposed && (
        <div className="flex flex-col gap-2">
          {!requireApiKey && (
            <SecurityWarning
              message="Require API key is disabled. Your endpoint is publicly accessible without authentication."
              action={{ label: 'Enable', href: '#require-api-key' }}
            />
          )}
          {loginUnsafe && (
            <SecurityWarning
              message={!requireLogin
                ? 'Require login is disabled. Anyone can access your dashboard via tunnel.'
                : 'Dashboard uses the default password. Change it in Profile settings.'}
              action={{
                label: !requireLogin ? 'Enable' : 'Change password',
                href: '/dashboard/settings',
              }}
            />
          )}
          <div className="flex items-center justify-between gap-3 rounded-lg bg-surface-2 px-3 py-2">
            <span className="text-xs text-text-main">Allow dashboard access via tunnel</span>
            <Switch checked={dashAccess} onCheckedChange={setDashAccessAndSave} />
          </div>
        </div>
      )}

      <Dialog open={installOpen} onOpenChange={setInstallOpen}>
        <DialogContent>
          <DialogTitle>Install Tailscale</DialogTitle>
          <DialogDescription>
            Tailscale is not installed (or not logged in). The installer downloads from tailscale.com and needs your sudo password.
          </DialogDescription>
          <div className="mt-4">
            <Label htmlFor="ts-sudo">Sudo password</Label>
            <Input
              id="ts-sudo"
              type="password"
              className="mt-1.5 border-0 bg-surface-2 shadow-none"
              autoFocus
              value={sudoPassword}
              onChange={(e) => setSudoPassword(e.target.value)}
            />
            <p className="mt-1.5 text-[11px] text-text-muted">
              Sent once to the local server for the install step; never stored in the browser.
            </p>
          </div>
          {installLog.length > 0 && (
            <pre className="mt-3 max-h-40 overflow-y-auto rounded-lg bg-surface-2 p-2 font-mono text-[11px]">
              {installLog.join('\n')}
            </pre>
          )}
          <DialogFooter>
            <Button variant="ghost" onClick={() => setInstallOpen(false)}>Cancel</Button>
            <Button onClick={installTailscale} disabled={!sudoPassword.trim()}>Install</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

/**
 * Per-key model ACL, ported from KeyAccessControls.js: a restricted key may
 * only call the combos/models listed here; images.go's authorizeRequest
 * enforces the same accessAllow list on the server.
 */
function KeyAccessControls({
  apiKey,
  onChange,
  onRequestRestrict,
}: {
  apiKey: ApiKey
  onChange: (access: { restricted: boolean; allow: string[] }) => void
  onRequestRestrict: () => void
}) {
  const [pickerOpen, setPickerOpen] = useState(false)
  const allow = apiKey.accessAllow ?? []
  const restricted = apiKey.accessRestricted
  const setAllow = (next: string[]) => onChange({ restricted: true, allow: next })
  const add = (m: PickerModel) => {
    const value = m?.value
    if (value && !allow.some((e) => e.toLowerCase() === value.toLowerCase())) setAllow([...allow, value])
  }
  const remove = (value: string) => setAllow(allow.filter((e) => e !== value))

  return (
    <>
      {/* The gate itself is one control; the list it opens is the detail. A
          switch plus a caption read as a second setting per row, which it is not. */}
      <button
        onClick={() => (restricted ? onChange({ restricted: false, allow }) : onRequestRestrict())}
        title={restricted ? 'Let this key call every model again' : 'Limit this key to chosen combos or models'}
        className={
          'inline-flex cursor-pointer items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] font-medium transition-colors ' +
          (restricted
            ? 'bg-card text-text-main shadow-[0_1px_2px_rgba(0,0,0,0.06)]'
            : 'bg-surface-2 text-text-muted hover:bg-surface-3 hover:text-text-main')
        }
      >
        <Icon name={restricted ? 'lock' : 'lock_open'} className="text-[13px]" />
        {restricted ? `${allow.length} allowed` : 'All models'}
      </button>
      {restricted && (
        <div className="mt-2 flex flex-wrap items-center gap-1.5">
          {allow.length === 0 && (
            <span className="text-[11px] text-warning">No combos or models allowed. This key can call nothing.</span>
          )}
          {allow.map((entry) => (
            <span key={entry} className="flex items-center gap-1 rounded bg-card px-1.5 py-0.5 font-mono text-[10px] text-text-main">
              {entry}
              <button onClick={() => remove(entry)} className="cursor-pointer rounded text-text-muted hover:text-danger" title={`Remove ${entry}`}>
                <Icon name="close" className="text-[12px]" />
              </button>
            </span>
          ))}
          <button
            onClick={() => setPickerOpen(true)}
            className="flex cursor-pointer items-center gap-0.5 rounded bg-surface-2 px-1.5 py-0.5 text-[10px] font-medium text-text-muted transition-colors hover:bg-surface-3 hover:text-text-main"
          >
            <Icon name="add" className="text-[13px]" />
            Add
          </button>
        </div>
      )}
      <ModelSelectModal
        isOpen={pickerOpen}
        onClose={() => setPickerOpen(false)}
        onSelect={add}
        onDeselect={(m) => remove(allow.find((e) => e.toLowerCase() === String(m?.value).toLowerCase()) ?? '')}
        title={`Allowed for "${apiKey.name}"`}
        addedModelValues={allow}
        closeOnSelect={false}
      />
    </>
  )
}

export default function EndpointPage() {
  const [keys, setKeys] = useState<ApiKey[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [visible, setVisible] = useState<Set<string>>(new Set())
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [created, setCreated] = useState<ApiKey | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<ApiKey | null>(null)
  const [restricting, setRestricting] = useState<ApiKey | null>(null)
  const [pausing, setPausing] = useState<ApiKey | null>(null)
  const [requireApiKey, setRequireApiKey] = useState(true)
  const [isRemoteHost, setIsRemoteHost] = useState(false)

  useEffect(() => {
    setIsRemoteHost(!['localhost', '127.0.0.1', '::1'].includes(window.location.hostname))
  }, [])

  useEffect(() => {
    api.settings().then((s) => setRequireApiKey(s.requireApiKey !== false)).catch(() => {})
  }, [])

  const setRequire = async (value: boolean) => {
    setRequireApiKey(value)
    try { await api.patchSettings({ requireApiKey: value }) } catch { setRequireApiKey(!value) }
  }

  const updateAccess = async (id: string, access: { restricted: boolean; allow: string[] }) => {
    try {
      await api.updateKey(id, { access })
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to update key access')
    }
    await load()
  }

  const baseUrl = typeof window !== 'undefined' ? window.location.origin : ''

  const load = useCallback(async () => {
    try {
      const data = await api.keys()
      setKeys(data.keys)
      setError('')
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load keys')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void load() }, [load])

  const createKey = async () => {
    try {
      const key = await api.createKey(newName.trim() || 'Default')
      setCreating(false)
      setNewName('')
      setCreated(key)
      await load()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to create key')
    }
  }

  // Pausing is a confirm step in the reference; resuming is immediate.
  const toggleActive = async (k: ApiKey) => {
    if (k.isActive) {
      setPausing(k)
      return
    }
    await api.updateKey(k.id, { isActive: true })
    await load()
  }

  const doDelete = async () => {
    if (!confirmDelete) return
    await api.deleteKey(confirmDelete.id)
    setConfirmDelete(null)
    await load()
  }

  const toggleVisible = (id: string) =>
    setVisible((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  return (
    <>
      <PageHeader title="Endpoint & Keys" />

      <section className="mb-4 overflow-hidden rounded-xl bg-surface">
        <div className="px-4 py-3">
          <h2 className="text-sm font-semibold text-text-main">Base URL</h2>
          <p className="mt-0.5 text-[11px] text-text-muted">Copy into your client's base_url.</p>
        </div>
        <div className="border-t border-border px-4 py-3">
          <div className="flex items-center gap-2 rounded-lg bg-surface-2 px-3 py-2">
            <code className="min-w-0 flex-1 truncate font-mono text-[13px] leading-none text-text-main">
              {baseUrl}/v1
            </code>
            <CopyButton value={`${baseUrl}/v1`} label="Copy base URL" className="-mr-1.5 size-7" />
          </div>
        </div>
      </section>

      <section className="mb-4 overflow-hidden rounded-xl bg-surface">
        <div className="px-4 py-3">
          <h2 className="text-sm font-semibold text-text-main">Remote access</h2>
          <p className="mt-0.5 text-[11px] text-text-muted">Reach this server from outside your network.</p>
        </div>
        <div className="border-t border-border px-4 py-3">
          <TunnelSection />
        </div>
      </section>

      <section id="require-api-key" className="overflow-hidden rounded-xl bg-surface">
        <div className="flex items-center justify-between gap-3 px-4 py-3">
          <div className="min-w-0">
            <div className="flex items-baseline gap-2">
              <h2 className="text-sm font-semibold text-text-main">API keys</h2>
              <span className="font-mono text-[11px] text-text-muted">{keys.length}</span>
            </div>
            <p className="mt-0.5 text-[11px] text-text-muted">Send the key as a Bearer token.</p>
          </div>
          <Button size="sm" variant="ghost" onClick={() => setCreating(true)}>
            <Icon name="add" /> New key
          </Button>
        </div>
        <div className="flex flex-col gap-3 border-t border-border px-4 py-3">
          <div className="flex items-center justify-between gap-3 rounded-lg bg-surface-2 px-3 py-2">
            <div className="min-w-0">
              <p className="text-xs font-medium text-text-main">Require API key</p>
              <p className="text-[11px] text-text-muted">
                {requireApiKey ? 'Requests without a valid key are rejected.' : 'Requests without a key are accepted.'}
              </p>
            </div>
            <Switch checked={requireApiKey} onCheckedChange={setRequire} />
          </div>
          {isRemoteHost && !requireApiKey && (
            <SecurityWarning message="Endpoint is exposed without an API key." />
          )}
          {error && <p className="text-[11px] text-danger">{error}</p>}
          {loading ? (
            <p className="py-8 text-center text-[11px] text-text-muted">Loading keys…</p>
          ) : keys.length === 0 ? (
            <div className="flex flex-col items-start gap-1 rounded-lg bg-surface-2 px-4 py-4 sm:flex-row sm:items-center">
              <div className="flex min-w-0 flex-1 items-center gap-2.5">
                <Icon name="vpn_key" className="shrink-0 text-[16px] text-text-muted" />
                <div className="min-w-0">
                  <p className="text-sm text-text-main">No keys yet</p>
                  <p className="text-[11px] text-text-muted">
                    Requests are matched to a key by its secret. Create one to start attributing usage.
                  </p>
                </div>
              </div>
              <Button size="sm" variant="ghost" onClick={() => setCreating(true)}>
                <Icon name="add" /> Create a key
              </Button>
            </div>
          ) : (
            <ul className="flex flex-col gap-2">
              {keys.map((k) => (
                <li key={k.id} className="flex flex-col gap-2 rounded-lg bg-surface-2 px-3 py-2.5">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                        <span className="truncate text-sm font-medium text-text-main">{k.name || 'Unnamed key'}</span>
                        <button
                          onClick={() => toggleActive(k)}
                          title={k.isActive ? 'Pause this key' : 'Resume this key'}
                          className="inline-flex cursor-pointer items-center gap-1.5 rounded px-1 py-0.5 text-[11px] text-text-muted transition-colors hover:text-text-main"
                        >
                          <span className={`size-1.5 rounded-full ${k.isActive ? 'bg-success' : 'bg-text-muted/40'}`} />
                          {k.isActive ? 'active' : 'paused'}
                        </button>
                      </div>
                      <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1">
                        <code className="font-mono text-[11px] text-text-muted">
                          {visible.has(k.id) ? k.key : maskSecret(k.key)}
                        </code>
                        <span className="text-[10px] text-text-muted/60">·</span>
                        <span className="text-[11px] text-text-muted">
                          created {new Date(k.createdAt).toLocaleDateString()}
                        </span>
                      </div>
                    </div>
                    <div className="flex shrink-0 items-center gap-0.5">
                      <button
                        title={visible.has(k.id) ? 'Hide key' : 'Show key'}
                        className="flex size-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-card hover:text-text-main"
                        onClick={() => toggleVisible(k.id)}
                      >
                        <Icon name={visible.has(k.id) ? 'visibility_off' : 'visibility'} className="text-[16px]" />
                      </button>
                      <CopyButton value={k.key} label="Copy key" />
                      <button
                        title="Delete key"
                        className="flex size-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-card hover:text-danger"
                        onClick={() => setConfirmDelete(k)}
                      >
                        <Icon name="delete" className="text-[16px]" />
                      </button>
                    </div>
                  </div>
                  <KeyAccessControls
                    apiKey={k}
                    onChange={(a) => updateAccess(k.id, a)}
                    onRequestRestrict={() => setRestricting(k)}
                  />
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>

      <Dialog open={creating} onOpenChange={setCreating}>
        <DialogContent>
          <DialogTitle>Create API key</DialogTitle>
          <DialogDescription>Give the key a name so you can tell it apart later.</DialogDescription>
          <div className="mt-4">
            <Label htmlFor="key-name">Name</Label>
            <Input
              id="key-name"
              className="mt-1.5 border-0 bg-surface-2 shadow-none"
              autoFocus
              value={newName}
              placeholder="Default"
              onChange={(e) => setNewName(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') void createKey() }}
            />
          </div>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setCreating(false)}>Cancel</Button>
            <Button onClick={createKey}>Create</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={!!created} onOpenChange={(o) => !o && setCreated(null)}>
        <DialogContent>
          <DialogTitle>Key created</DialogTitle>
          <DialogDescription>Copy it now. This is the only time it is shown in full here.</DialogDescription>
          <div className="mt-4 flex items-center gap-2">
            <code className="min-w-0 flex-1 break-all rounded-lg bg-surface-2 px-3 py-2 font-mono text-xs">
              {created?.key}
            </code>
            <CopyButton value={created?.key ?? ''} label="Copy key" />
          </div>
          <DialogFooter>
            <Button onClick={() => setCreated(null)}>Done</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmModal
        isOpen={!!restricting}
        onClose={() => setRestricting(null)}
        title="Restrict API Key"
        message={`Restrict API key "${restricting?.name ?? ''}"? It will only be able to call the combos and models you add. Until you add one, it can call nothing.`}
        confirmLabel="Restrict"
        onConfirm={async () => {
          if (restricting) await updateAccess(restricting.id, { restricted: true, allow: restricting.accessAllow ?? [] })
          setRestricting(null)
        }}
      />

      <ConfirmModal
        isOpen={!!pausing}
        onClose={() => setPausing(null)}
        title="Pause API Key"
        message={`Pause API key "${pausing?.name ?? ''}"? This key will stop working immediately but can be resumed later.`}
        confirmLabel="Pause"
        onConfirm={async () => {
          if (pausing) await api.updateKey(pausing.id, { isActive: false })
          setPausing(null)
          await load()
        }}
      />

      <Dialog open={!!confirmDelete} onOpenChange={(o) => !o && setConfirmDelete(null)}>
        <DialogContent>
          <DialogTitle>Delete key?</DialogTitle>
          <DialogDescription>
            “{confirmDelete?.name}” stops working immediately. This cannot be undone.
          </DialogDescription>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setConfirmDelete(null)}>Cancel</Button>
            <Button variant="destructive" onClick={doDelete}>Delete</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
