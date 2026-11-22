import { useCallback, useEffect, useRef, useState } from 'react'
import { AlertTriangle, Cloud, Eye, EyeOff, ExternalLink, Plus, Power, Trash2 } from 'lucide-react'
import { api, type ApiKey, type TunnelStatus } from '@/lib/api'
import { maskSecret } from '@/lib/format'
import { PageHeader } from '@/components/layout'
import { CopyButton } from '@/components/copy-button'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Icon } from '@/components/icon'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogTitle,
} from '@/components/ui/dialog'
import { Input, Label } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
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

function TunnelRow({ label, url, children }: { label: string; url?: string; children?: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className={`shrink-0 rounded px-1.5 py-0.5 text-center font-mono text-xs ${url ? 'bg-primary/10 text-primary' : 'bg-muted text-muted-foreground'}`}>
        {label}
      </span>
      {url ? (
        <>
          <code className="min-w-0 flex-1 truncate rounded-md border border-border bg-muted/40 px-3 py-1.5 font-mono text-xs">
            {url}/v1
          </code>
          <CopyButton value={`${url}/v1`} label={`Copy ${label} URL`} />
        </>
      ) : (
        <div className="min-w-0 flex-1" />
      )}
      {children}
    </div>
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
      <TunnelRow
        label="Tunnel"
        url={tunnelEnabled && !tunnelBusy ? tunnelUrl : undefined}
      >
        {tunnelBusy ? (
          <>
            <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <span className="size-3 animate-spin rounded-full border border-current border-t-transparent" />
              {tunnelProgress || 'Creating tunnel…'}
            </span>
            <Button variant="ghost" size="icon" title="Stop" className="text-danger" onClick={() => setTunnelBusy(false)}>
              <Power className="size-3.5" />
            </Button>
          </>
        ) : tunnelEnabled && unreachable && !checking ? (
          <>
            <span className="flex items-center gap-1.5 rounded-md border border-warning/40 bg-warning/5 px-2 py-1 text-xs text-warning">
              <span className="size-3 animate-spin rounded-full border border-current border-t-transparent" />
              Tunnel reconnecting…
            </span>
            <Button variant="ghost" size="icon" title="Disable tunnel" className="text-danger" onClick={disableTunnel}>
              <Power className="size-3.5" />
            </Button>
          </>
        ) : checking ? (
          <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <span className="size-3 animate-spin rounded-full border border-current border-t-transparent" />
            Checking…
          </span>
        ) : tunnelEnabled ? (
          <Button variant="ghost" size="icon" title="Disable tunnel" className="text-danger" onClick={disableTunnel}>
            <Power className="size-3.5" />
          </Button>
        ) : (
          <Button size="sm" onClick={enableTunnel}>
            <Cloud className="size-3.5" /> Enable
          </Button>
        )}
      </TunnelRow>
      {tunnelError && !tunnelEnabled && (
        <div className="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-xs text-danger">{tunnelError}</div>
      )}

      <TunnelRow label="Tailscale" url={tsEnabled && !tsBusy ? tsUrl : undefined}>
        {tsBusy ? (
          <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <span className="size-3 animate-spin rounded-full border border-current border-t-transparent" />
            {tsProgress}
          </span>
        ) : tsEnabled ? (
          <Button variant="ghost" size="icon" title="Disable Tailscale" className="text-danger" onClick={disableTailscale}>
            <Power className="size-3.5" />
          </Button>
        ) : (
          <Button size="sm" onClick={openTailscale}>
            <ExternalLink className="size-3.5" /> Enable
          </Button>
        )}
        {tsAuthUrl && !tsBusy && (
          <Button size="sm" variant="outline" onClick={() => window.open(tsAuthUrl, 'tailscale_auth', 'width=600,height=700,noopener,noreferrer')}>
            Open login page
          </Button>
        )}
      </TunnelRow>
      {tsError && <div className="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-xs text-danger">{tsError}</div>}

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
    <div className="mt-2">
      <div className="flex items-center gap-2">
        <Switch
          checked={restricted}
          onCheckedChange={(c) => (c ? onRequestRestrict() : onChange({ restricted: false, allow }))}
          title="Restrict access"
        />
        <span className="text-xs font-medium text-muted-foreground">Restrict access</span>
      </div>
      {restricted && (
        <div className="mt-2 flex flex-wrap items-center gap-1.5">
          {allow.length === 0 && (
            <span className="text-xs text-warning">No combos or models allowed — this key can call nothing.</span>
          )}
          {allow.map((entry) => (
            <span key={entry} className="flex items-center gap-1 rounded-xl border border-primary/30 bg-primary/10 px-2 py-0.5 font-mono text-xs text-text-main">
              {entry}
              <button onClick={() => remove(entry)} className="rounded text-muted-foreground hover:text-danger" title={`Remove ${entry}`}>
                <Icon name="close" className="text-[12px]" />
              </button>
            </span>
          ))}
          <button
            onClick={() => setPickerOpen(true)}
            className="flex items-center gap-0.5 rounded-xl border border-dashed border-border px-2 py-0.5 text-xs font-medium text-primary hover:border-primary/50"
          >
            <Icon name="add" className="text-[14px]" />
            Add combo or model
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
    </div>
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
          <CardDescription>Use as the OpenAI base_url (or Anthropic/Gemini base URL).</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <TunnelRow label="Local" url={baseUrl} />
          <TunnelSection />
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
        <CardContent>
          <div className="mb-4 flex items-center justify-between gap-3 border-b border-border pb-4">
            <div>
              <p className="text-sm font-medium">Require API key</p>
              <p className="text-xs text-muted-foreground">Requests without a valid key will be rejected</p>
            </div>
            <Switch checked={requireApiKey} onCheckedChange={setRequire} />
          </div>
          {isRemoteHost && !requireApiKey && (
            <div className="mb-3">
              <SecurityWarning message="Endpoint is exposed without an API key." />
            </div>
          )}
          {error && <p className="mb-3 text-xs text-destructive">{error}</p>}
          {loading ? (
            <p className="py-8 text-center text-xs text-muted-foreground">Loading…</p>
          ) : keys.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-8 text-center text-xs text-muted-foreground">
              <Icon name="vpn_key" className="text-[32px] opacity-40" />
              <p>No API keys yet.</p>
              <p>Create your first API key to get started.</p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Key</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead className="w-20" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {keys.map((k) => (
                  <TableRow key={k.id}>
                    <TableCell className="font-medium">
                      {k.name || '—'}
                      <KeyAccessControls
                        apiKey={k}
                        onChange={(a) => updateAccess(k.id, a)}
                        onRequestRestrict={() => setRestricting(k)}
                      />
                    </TableCell>
                    <TableCell>
                      <code className="font-mono text-xs text-muted-foreground">
                        {visible.has(k.id) ? k.key : maskSecret(k.key)}
                      </code>
                    </TableCell>
                    <TableCell>
                      <button onClick={() => toggleActive(k)} className="cursor-pointer" title={k.isActive ? 'Pause key' : 'Resume key'}>
                        <Badge variant={k.isActive ? 'success' : 'secondary'}>
                          {k.isActive ? 'active' : 'paused'}
                        </Badge>
                      </button>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {new Date(k.createdAt).toLocaleDateString()}
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end gap-0.5">
                        <Button variant="ghost" size="icon" title="Show" onClick={() => toggleVisible(k.id)}>
                          {visible.has(k.id) ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
                        </Button>
                        <CopyButton value={k.key} label="Copy key" />
                        <Button
                          variant="ghost"
                          size="icon"
                          title="Delete"
                          className="text-muted-foreground hover:text-destructive"
                          onClick={() => setConfirmDelete(k)}
                        >
                          <Trash2 className="size-3.5" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
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
