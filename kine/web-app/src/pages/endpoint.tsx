import { useCallback, useEffect, useRef, useState } from 'react'
import { AlertTriangle, Cloud, Eye, EyeOff, ExternalLink, Plus, Power, Trash2 } from 'lucide-react'
import { api, type ApiKey, type TunnelStatus } from '@/lib/api'
import { maskSecret } from '@/lib/format'
import { PageHeader } from '@/components/layout'
import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
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
  action,
  url,
  status,
}: {
  label: string
  action?: React.ReactNode
  url?: string
  status?: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-border p-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs font-semibold text-text-main">{label}</span>
        <div className="flex shrink-0 items-center gap-1.5">{action}</div>
      </div>
      {url ? (
        <div className="flex items-center gap-1">
          <code className="min-w-0 flex-1 truncate font-mono text-xs text-text-muted">{url}/v1</code>
          <CopyButton value={`${url}/v1`} label={`Copy ${label} URL`} />
        </div>
      ) : (
        // Same height as the code row so the two tiles stay the same shape.
        <div className="flex min-h-8 items-center text-xs text-muted-foreground">{status}</div>
      )}
    </div>
  )
}

function Spinner({ children }: { children: React.ReactNode }) {
  return (
    <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
      <span className="size-3 animate-spin rounded-full border border-current border-t-transparent" />
      {children}
    </span>
  )
}

function SecurityWarning({ message, action }: { message: string; action?: { label: string; href: string } }) {
  return (
    <div className="flex items-start gap-2 rounded-md border border-warning/40 bg-warning/5 px-3 py-2 text-xs text-warning">
      <AlertTriangle className="mt-px size-3.5 shrink-0" />
      <span className="flex-1">{message}</span>
      {action && (
        <a href={action.href} className="shrink-0 font-medium underline underline-offset-2 hover:opacity-80">
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
          setTsProgress('Login required — finish login to continue')
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
              <Button variant="ghost" size="icon" title="Stop" className="text-danger" onClick={() => setTunnelBusy(false)}>
                <Power className="size-3.5" />
              </Button>
            ) : tunnelEnabled ? (
              <Button variant="ghost" size="icon" title="Disable tunnel" className="text-danger" onClick={disableTunnel}>
                <Power className="size-3.5" />
              </Button>
            ) : checking ? undefined : (
              <Button size="sm" onClick={enableTunnel}>
                <Cloud className="size-3.5" /> Enable
              </Button>
            )
          }
        />
        <RouteTile
          label="Tailscale"
          url={tsEnabled && !tsBusy ? tsUrl : undefined}
          status={tsBusy ? <Spinner>{tsProgress}</Spinner> : 'Not enabled'}
          action={
            <>
              {tsAuthUrl && !tsBusy && (
                <Button size="sm" variant="outline" onClick={() => window.open(tsAuthUrl, 'tailscale_auth', 'width=600,height=700,noopener,noreferrer')}>
                  Open login page
                </Button>
              )}
              {tsEnabled ? (
                <Button variant="ghost" size="icon" title="Disable Tailscale" className="text-danger" onClick={disableTailscale}>
                  <Power className="size-3.5" />
                </Button>
              ) : tsBusy ? undefined : (
                <Button size="sm" onClick={openTailscale}>
                  <ExternalLink className="size-3.5" /> Enable
                </Button>
              )}
            </>
          }
        />
      </div>

      {tunnelError && !tunnelEnabled && (
        <div className="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-xs text-danger">{tunnelError}</div>
      )}
      {tsError && (
        <div className="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-xs text-danger">{tsError}</div>
      )}

      {loginUnsafe && !exposed && (
        <SecurityWarning message={unsafeReason} action={{ label: 'Open settings', href: '/dashboard/settings' }} />
      )}

      {exposed && (
        <div className="flex flex-col gap-2">
          {!requireApiKey && (
            <SecurityWarning
              message="Require API key is disabled — your endpoint is publicly accessible without authentication."
              action={{ label: 'Enable', href: '#require-api-key' }}
            />
          )}
          {loginUnsafe && (
            <SecurityWarning
              message={!requireLogin
                ? 'Require login is disabled — anyone can access your dashboard via tunnel.'
                : 'Dashboard uses the default password — change it in Profile settings.'}
              action={{
                label: !requireLogin ? 'Enable' : 'Change password',
                href: '/dashboard/settings',
              }}
            />
          )}
          <div className="flex items-center gap-3 border-t border-border pt-3">
            <Switch checked={dashAccess} onCheckedChange={setDashAccessAndSave} />
            <span className="text-sm font-medium">Allow dashboard access via tunnel</span>
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
              className="mt-1.5"
              autoFocus
              value={sudoPassword}
              onChange={(e) => setSudoPassword(e.target.value)}
            />
            <p className="mt-1.5 text-[11px] text-muted-foreground">
              Sent once to the local server for the install step; never stored in the browser.
            </p>
          </div>
          {installLog.length > 0 && (
            <pre className="mt-3 max-h-40 overflow-y-auto rounded-md border border-border bg-muted/40 p-2 font-mono text-[11px]">
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
          'inline-flex cursor-pointer items-center gap-1 rounded-md border px-2 py-1 text-xs font-medium transition-colors ' +
          (restricted
            ? 'border-primary/30 bg-primary/10 text-primary hover:bg-primary/15'
            : 'border-border text-muted-foreground hover:border-border hover:text-text-main')
        }
      >
        <Icon name={restricted ? 'lock' : 'lock_open'} className="text-[14px]" />
        {restricted ? `${allow.length} allowed` : 'All models'}
      </button>
      {restricted && (
        <div className="mt-2 flex flex-wrap items-center gap-1.5">
          {allow.length === 0 && (
            <span className="text-xs text-warning">No combos or models allowed — this key can call nothing.</span>
          )}
          {allow.map((entry) => (
            <span key={entry} className="flex items-center gap-1 rounded-md border border-border bg-surface-2 px-1.5 py-0.5 font-mono text-xs text-text-main">
              {entry}
              <button onClick={() => remove(entry)} className="cursor-pointer rounded text-muted-foreground hover:text-danger" title={`Remove ${entry}`}>
                <Icon name="close" className="text-[12px]" />
              </button>
            </span>
          ))}
          <button
            onClick={() => setPickerOpen(true)}
            className="flex cursor-pointer items-center gap-0.5 rounded-md border border-dashed border-border px-1.5 py-0.5 text-xs font-medium text-primary hover:border-primary/50"
          >
            <Icon name="add" className="text-[14px]" />
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
      <PageHeader
        title="Endpoint & Keys"
        description="Point any OpenAI-compatible client at this server. Authenticate with an API key."
      />

      <Card className="mb-5">
        <CardHeader className="pb-2">
          <CardTitle>Base URL</CardTitle>
          <CardDescription>Paste into any client as its OpenAI base_url (or Anthropic/Gemini base URL).</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {/* The local address is what most visits are for: give it the width
              and the weight, and let remote access sit below as options. */}
          <div className="flex items-center gap-2 rounded-lg border border-border bg-muted/40 px-3 py-2.5">
            <code className="min-w-0 flex-1 truncate font-mono text-sm text-text-main">{baseUrl}/v1</code>
            <CopyButton value={`${baseUrl}/v1`} label="Copy base URL" size="sm" />
          </div>
          <div className="flex flex-col gap-2">
            <p className="text-xs text-muted-foreground">Reach it from elsewhere</p>
            <TunnelSection />
          </div>
        </CardContent>
      </Card>

      <Card id="require-api-key">
        <CardHeader className="flex-row items-center justify-between gap-3">
          <div>
            <CardTitle>API keys</CardTitle>
            <CardDescription>
              Send as <code className="font-mono">Authorization: Bearer &lt;key&gt;</code>.
            </CardDescription>
          </div>
          <Button size="sm" onClick={() => setCreating(true)}>
            <Plus /> New key
          </Button>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {/* The gate is one setting, not a section: a single strip keeps it out
              of the way of the keys it guards. */}
          <div className="flex items-center justify-between gap-3 rounded-lg border border-border bg-muted/30 px-3 py-2">
            <div className="min-w-0">
              <p className="text-xs font-medium text-text-main">Require API key</p>
              <p className="text-xs text-muted-foreground">
                {requireApiKey ? 'Requests without a valid key are rejected.' : 'Requests without a key are accepted.'}
              </p>
            </div>
            <Switch checked={requireApiKey} onCheckedChange={setRequire} />
          </div>
          {isRemoteHost && !requireApiKey && (
            <SecurityWarning message="Endpoint is exposed without an API key." />
          )}
          {error && <p className="text-xs text-destructive">{error}</p>}
          {loading ? (
            <p className="py-8 text-center text-xs text-muted-foreground">Loading keys…</p>
          ) : keys.length === 0 ? (
            <div className="flex flex-col items-center gap-1.5 rounded-lg border border-dashed border-border px-4 py-8 text-center">
              <Icon name="vpn_key" className="text-[28px] text-muted-foreground/50" />
              <p className="mt-1 text-sm font-medium text-text-main">No keys yet</p>
              <p className="text-xs text-muted-foreground">
                Requests are matched to a key by its secret. Create one to start attributing usage.
              </p>
              <Button size="sm" variant="outline" className="mt-2" onClick={() => setCreating(true)}>
                <Plus /> Create a key
              </Button>
            </div>
          ) : (
            <ul className="divide-y divide-border-subtle overflow-hidden rounded-lg border border-border">
              {keys.map((k) => (
                <li key={k.id} className="flex flex-col gap-2 px-3 py-3">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                        <span className="truncate text-sm font-medium text-text-main">{k.name || 'Unnamed key'}</span>
                        {/* Clicking the dot pauses or resumes the key; the dot is
                            the real state, so it carries the colour, not a badge. */}
                        <button
                          onClick={() => toggleActive(k)}
                          title={k.isActive ? 'Pause this key' : 'Resume this key'}
                          className="inline-flex cursor-pointer items-center gap-1.5 rounded px-1 py-0.5 text-xs text-muted-foreground transition-colors hover:bg-surface-2 hover:text-text-main"
                        >
                          <span className={`size-1.5 rounded-full ${k.isActive ? 'bg-success' : 'bg-muted-foreground'}`} />
                          {k.isActive ? 'active' : 'paused'}
                        </button>
                      </div>
                      <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1">
                        <code className="font-mono text-xs text-muted-foreground">
                          {visible.has(k.id) ? k.key : maskSecret(k.key)}
                        </code>
                        <span className="text-[10px] text-muted-foreground/60">·</span>
                        <span className="text-[11px] text-muted-foreground">
                          created {new Date(k.createdAt).toLocaleDateString()}
                        </span>
                      </div>
                    </div>
                    <div className="flex shrink-0 items-center gap-0.5">
                      <Button variant="ghost" size="icon" title="Show key" onClick={() => toggleVisible(k.id)}>
                        {visible.has(k.id) ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
                      </Button>
                      <CopyButton value={k.key} label="Copy key" />
                      <Button
                        variant="ghost"
                        size="icon"
                        title="Delete key"
                        className="text-muted-foreground hover:text-danger"
                        onClick={() => setConfirmDelete(k)}
                      >
                        <Trash2 className="size-3.5" />
                      </Button>
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
        </CardContent>
      </Card>

      <Dialog open={creating} onOpenChange={setCreating}>
        <DialogContent>
          <DialogTitle>Create API key</DialogTitle>
          <DialogDescription>Give the key a name so you can tell it apart later.</DialogDescription>
          <div className="mt-4">
            <Label htmlFor="key-name">Name</Label>
            <Input
              id="key-name"
              className="mt-1.5"
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
          <DialogDescription>Copy it now — this is the only time it is shown in full here.</DialogDescription>
          <div className="mt-4 flex items-center gap-2">
            <code className="min-w-0 flex-1 break-all rounded-md border border-border bg-muted/40 px-3 py-2 font-mono text-xs">
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
