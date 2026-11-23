import { useCallback, useEffect, useState } from 'react'
import { Icon } from '@/components/icon'
import { PageHeader } from '@/components/layout'
import { Modal } from '@/components/modal'
import { ModelSelectModal } from '@/components/model-select-modal'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { api, ApiError, type ApiKey, type MitmStatus } from '@/lib/api'
import { MITM_TOOLS, type MitmTool } from '@/lib/cli-tools'
import { cn } from '@/lib/utils'
import { useNotify } from '@/lib/toast'

const DEFAULT_BASE = 'http://localhost:20128'

/**
 * MITM page, ported from src/app/(dashboard)/dashboard/mitm/MitmPageClient.js
 * + cli-tools/components/MitmServerCard.js + MitmToolCard.js.
 *
 * Privilege: every privileged action (cert, server start, DNS) needs a sudo
 * password unless the server reports a cached one or runs as root. The password
 * only ever travels in the request body to the local Go server; nothing is
 * persisted in the browser.
 */
export default function MitmPage() {
  const notify = useNotify()
  const [status, setStatus] = useState<MitmStatus | null>(null)
  const [keys, setKeys] = useState<ApiKey[]>([])
  const [baseUrl, setBaseUrl] = useState(DEFAULT_BASE)
  const [apiKey, setApiKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // sudo prompt: remembers which action to retry once the password arrives
  const [pending, setPending] = useState<null | ((pwd: string) => Promise<void>)>(null)
  const [password, setPassword] = useState('')
  const [port443, setPort443] = useState<string | null>(null)

  const load = useCallback(() => {
    api
      .mitmStatus()
      .then((st) => {
        setStatus(st)
        if (st.mitmRouterBaseUrl) setBaseUrl(st.mitmRouterBaseUrl)
      })
      .catch(() => setStatus(null))
  }, [])

  useEffect(() => {
    load()
    api
      .keys()
      .then((k) => {
        setKeys(k.keys)
        setApiKey((prev) => prev || k.keys[0]?.key || '')
      })
      .catch(() => setKeys([]))
  }, [load])

  const canRunWithoutPassword =
    !status || status.isWin || status.hasCachedPassword || status.needsSudoPassword === false

  const run = async (fn: (pwd: string) => Promise<void>, pwd = '') => {
    setBusy(true)
    setError(null)
    try {
      await fn(pwd)
      setPassword('')
      setPending(null)
      load()
    } catch (e) {
      if (e instanceof ApiError && e.status === 400 && /sudoPassword/i.test(e.message)) {
        setPending(() => fn)
        return
      }
      if (e instanceof ApiError && /port 443/i.test(e.message)) {
        setPort443(e.message)
        return
      }
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  /** Runs immediately when no password is required, else asks for one first. */
  const withPassword = (fn: (pwd: string) => Promise<void>) => {
    if (canRunWithoutPassword) void run(fn)
    else setPending(() => fn)
  }

  // The password is read at Confirm time; the prompt opens with the field empty
  // so the captured closure must not include it.
  const confirmPassword = () => {
    if (!pending) return
    const fn = pending
    setPending(null)
    void run(fn, password)
  }

  const start = (pwd: string, force = false) =>
    api
      .mitmStart({
        apiKey: apiKey.trim() || keys[0]?.key || 'sk_kinerouter',
        sudoPassword: pwd,
        mitmRouterBaseUrl: baseUrl.trim() || DEFAULT_BASE,
        forceKillPort443: force,
      })
      .then(() => notify.success('MITM server started.'))

  const running = status?.running
  const done = [
    { label: 'Cert', ok: status?.certExists },
    { label: 'Trusted', ok: status?.certTrusted },
    { label: 'Server', ok: running },
  ]

  return (
    <>
      <PageHeader title="MITM" description="Redirect IDE traffic through this router with a local CA" />

      <div className="mb-4 flex items-start gap-2 rounded-lg border border-warning/30 bg-warning/10 px-3 py-2">
        <Icon name="warning" className="mt-0.5 shrink-0 text-[16px] text-warning" />
        <p className="text-xs leading-relaxed text-text-muted">
          <strong className="text-warning">Security:</strong> MITM terminates TLS for the listed hosts with a local CA
          and needs root to edit <code>/etc/hosts</code>. Enabling it may violate a provider&apos;s terms of service and
          can get accounts banned. Only use it on a machine you control.
        </p>
      </div>

      <Card className="mb-4 border-primary/20 bg-primary/5 p-3">
        <div className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <Icon name="security" className="text-[20px] text-primary" />
            <span className="text-sm font-semibold text-text-main">MITM Server</span>
            <Badge variant={running ? 'success' : 'secondary'} size="sm">
              {running ? 'Running' : 'Stopped'}
            </Badge>
            <div className="ml-auto flex flex-wrap items-center gap-2 text-[11px]">
              {done.map((d) => (
                <span key={d.label} className={cn('flex items-center gap-0.5', d.ok ? 'text-success' : 'text-text-muted')}>
                  <Icon name={d.ok ? 'check_circle' : 'cancel'} className="text-[12px]" />
                  {d.label}
                </span>
              ))}
            </div>
          </div>

          <div className="flex flex-col gap-1.5 rounded-lg border border-border/50 bg-surface/50 px-2 py-2">
            <p className="text-[11px] leading-relaxed text-text-muted">
              <span className="font-medium text-text-main">Purpose:</span> Let Antigravity IDE and GitHub Copilot use any
              provider or model from this router.
            </p>
            <p className="text-[11px] leading-relaxed text-text-muted">
              <span className="font-medium text-text-main">How it works:</span> IDE request → DNS redirect to
              localhost:443 → MITM proxy intercepts → router → response back to the IDE.
            </p>
          </div>

          <div className="grid gap-2 sm:grid-cols-[9rem_1fr] sm:items-center">
            <span className="text-xs text-text-muted sm:text-right">Router base URL</span>
            <Input
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              placeholder={DEFAULT_BASE}
              disabled={running}
              className="text-xs"
            />
            {!running && (
              <>
                <span className="text-xs text-text-muted sm:text-right">API key</span>
                <div>
                  <Input
                    list="mitm-api-keys"
                    value={apiKey}
                    onChange={(e) => setApiKey(e.target.value)}
                    placeholder="sk_kinerouter (default)"
                    className="text-xs"
                  />
                  <datalist id="mitm-api-keys">
                    {keys.map((k) => (
                      <option key={k.id} value={k.key}>
                        {k.name || k.key}
                      </option>
                    ))}
                  </datalist>
                </div>
              </>
            )}
          </div>

          <div className="flex flex-wrap items-center gap-2">
            {status?.certExists && !status.certTrusted && (
              <Button
                variant="outline"
                size="sm"
                disabled={busy}
                onClick={() => withPassword((pwd) => api.mitmAction('', 'trust-cert', pwd).then(() => undefined))}
              >
                <Icon name="verified_user" className="text-[16px]" /> Trust cert
              </Button>
            )}
            {running ? (
              <Button
                variant="destructive"
                size="sm"
                disabled={busy}
                onClick={() => withPassword((pwd) => api.mitmStop(pwd).then(() => undefined))}
              >
                <Icon name="stop_circle" className="text-[16px]" /> Stop server
              </Button>
            ) : (
              <Button
                size="sm"
                disabled={busy || !status || (status.isWin && !status.isAdmin)}
                title={status?.isWin && !status.isAdmin ? 'Administrator required' : undefined}
                onClick={() => withPassword((pwd) => start(pwd))}
              >
                <Icon name="play_circle" className="text-[16px]" /> Start server
              </Button>
            )}
            {running && <p className="text-xs text-text-muted">Enable DNS per tool below to activate interception</p>}
          </div>

          {error && (
            <div className="flex items-start gap-2 rounded border border-danger/20 bg-danger/10 px-2 py-1.5 text-xs text-danger">
              <Icon name="error" className="mt-0.5 shrink-0 text-[14px]" />
              <span>{error}</span>
            </div>
          )}
          {status?.isWin && !status.isAdmin && (
            <div className="flex items-center gap-2 rounded border border-danger/20 bg-danger/10 px-2 py-1.5 text-xs text-danger">
              <Icon name="shield_lock" className="shrink-0 text-[14px]" />
              <span>Administrator required — restart KineRouter as Administrator to use MITM</span>
            </div>
          )}
          {status && !status.isAdmin && status.needsSudoPassword && (
            <p className="text-xs text-text-muted">
              Not running as root — the sudo password is required for every privileged action.
            </p>
          )}
        </div>
      </Card>

      <div className="flex flex-col gap-3">
        {MITM_TOOLS.map((tool) => (
          <MitmToolCard
            key={tool.id}
            tool={tool}
            status={status}
            serverRunning={!!running}
            withPassword={withPassword}
            onChanged={load}
          />
        ))}
      </div>

      <Modal
        isOpen={!!pending}
        onClose={() => {
          setPending(null)
          setPassword('')
        }}
        title="Sudo password required"
      >
        <p className="mb-3 text-xs text-text-muted">
          Needed to write the CA certificate, bind port 443, and edit <code>/etc/hosts</code>.
        </p>
        <Input
          type="password"
          autoFocus
          placeholder="Enter sudo password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && password.trim() && !busy) confirmPassword()
          }}
        />
        <div className="mt-4 flex justify-end gap-2">
          <Button
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={() => {
              setPending(null)
              setPassword('')
            }}
          >
            Cancel
          </Button>
          <Button size="sm" disabled={busy || !password.trim()} onClick={confirmPassword}>
            Confirm
          </Button>
        </div>
      </Modal>

      <Modal isOpen={!!port443} onClose={() => setPort443(null)} title="Port 443 already in use">
        <p className="text-sm text-text-muted">{port443}</p>
        <p className="mt-2 text-xs text-text-muted">Kill the process holding port 443 and start the server?</p>
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={() => setPort443(null)}>
            Cancel
          </Button>
          <Button
            size="sm"
            disabled={busy}
            onClick={() => {
              setPort443(null)
              void run((pwd) => start(pwd, true), password)
            }}
          >
            Kill &amp; start
          </Button>
        </div>
      </Modal>
    </>
  )
}

function MitmToolCard({
  tool,
  status,
  serverRunning,
  withPassword,
  onChanged,
}: {
  tool: MitmTool
  status: MitmStatus | null
  serverRunning: boolean
  withPassword: (fn: (pwd: string) => Promise<void>) => void
  onChanged: () => void
}) {
  const notify = useNotify()
  const [expanded, setExpanded] = useState(false)
  const [mappings, setMappings] = useState<Record<string, string>>({})
  const dnsActive = !!status?.dnsStatus?.[tool.id]

  useEffect(() => {
    if (!expanded) return
    api
      .mitmAliases()
      .then((r) => setMappings(r.aliases?.[tool.id] ?? {}))
      .catch(() => setMappings({}))
  }, [expanded, tool.id])

  // Model ids for the mapping datalist come from /api/models; the mapping
  // itself is free text so a user can also pin a combo name.
  const [modelIds, setModelIds] = useState<string[]>([])
  const [pickerAlias, setPickerAlias] = useState<string | null>(null)
  useEffect(() => {
    if (!expanded) return
    api
      .models()
      .then((m) => setModelIds(m.data.map((x) => x.id)))
      .catch(() => setModelIds([]))
  }, [expanded])

  const save = async (next: Record<string, string>) => {
    setMappings(next)
    try {
      await api.mitmSetAliases(tool.id, next)
    } catch (e) {
      notify.error(e instanceof Error ? e.message : 'Failed to save mapping.')
    }
  }

  const toggleDns = () => {
    const action = dnsActive ? 'disable' : 'enable'
    withPassword(async (pwd) => {
      await api.mitmAction(tool.id, action, pwd)
      notify.success(action === 'enable' ? `Restart ${tool.name} to apply changes.` : 'DNS mapping removed.')
      onChanged()
    })
  }

  return (
    <Card className="p-3">
      <button className="flex w-full items-center gap-3 text-left" onClick={() => setExpanded(!expanded)}>
        <img
          src={`/providers/${tool.image}.png`}
          alt=""
          width={32}
          height={32}
          loading="lazy"
          decoding="async"
          className="size-8 shrink-0 rounded-lg object-contain"
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-sm font-medium text-text-main">{tool.name}</h3>
            {!serverRunning ? (
              <Badge variant="secondary" size="sm">
                Server off
              </Badge>
            ) : dnsActive ? (
              <Badge variant="success" size="sm">
                Active
              </Badge>
            ) : (
              <Badge variant="warning" size="sm">
                DNS off
              </Badge>
            )}
          </div>
          <p className="truncate text-xs text-text-muted">{tool.description}</p>
        </div>
        <Icon
          name="expand_more"
          className={cn('text-[20px] text-text-muted transition-transform', expanded && 'rotate-180')}
        />
      </button>

      {expanded && (
        <div className="mt-3 flex flex-col gap-3 border-t border-border-subtle pt-3">
          <div className="rounded-md border border-border-subtle bg-surface-2/50 px-2 py-1.5">
            <p className="mb-1 text-[10px] font-medium text-text-main/80">
              Edit the hosts file manually to add these entries:
            </p>
            <ul className="space-y-0.5 break-all font-mono text-[10px] text-text-muted">
              {tool.hosts.map((h) => (
                <li key={h}>127.0.0.1 {h}</li>
              ))}
            </ul>
          </div>

          {!dnsActive && <p className="text-[10px] text-warning">Enable DNS to edit model mappings.</p>}

          {tool.defaultModels.map((m) => (
            <div key={m.alias} className="grid gap-1.5 sm:grid-cols-[9rem_1fr_auto_auto] sm:items-center sm:gap-2">
              <span className="text-xs font-medium text-text-main sm:text-right">{m.name}</span>
              <div className="relative">
                <Input
                  list="mitm-model-options"
                  value={mappings[m.alias] ?? ''}
                  onChange={(e) => setMappings((prev) => ({ ...prev, [m.alias]: e.target.value }))}
                  onBlur={(e) => void save({ ...mappings, [m.alias]: e.target.value })}
                  placeholder="provider/model-id"
                  disabled={!dnsActive}
                  className="pr-7 font-mono text-xs"
                />
                {mappings[m.alias] && (
                  <button
                    className="absolute right-1 top-1/2 -translate-y-1/2 p-0.5 text-text-muted hover:text-danger"
                    title="Clear"
                    onClick={() => void save({ ...mappings, [m.alias]: '' })}
                  >
                    <Icon name="close" className="text-[14px]" />
                  </button>
                )}
              </div>
              <Button
                variant="outline"
                size="sm"
                disabled={!dnsActive}
                onClick={() => setPickerAlias(m.alias)}
                title="Pick a model"
              >
                Select
              </Button>
              <span className="text-[10px] text-text-muted">{m.id}</span>
            </div>
          ))}
          <ModelSelectModal
            isOpen={pickerAlias !== null}
            onClose={() => setPickerAlias(null)}
            onSelect={(m) => {
              if (pickerAlias && !m.isPlaceholder) void save({ ...mappings, [pickerAlias]: m.value })
            }}
            title={`Select model for ${pickerAlias ?? ''}`}
            addedModelValues={pickerAlias ? [mappings[pickerAlias] ?? ''].filter(Boolean) : []}
          />

          <datalist id="mitm-model-options">
            {modelIds.map((m) => (
              <option key={m} value={m} />
            ))}
          </datalist>

          <div>
            <Button
              variant={dnsActive ? 'destructive' : 'outline'}
              size="sm"
              disabled={!serverRunning}
              onClick={toggleDns}
            >
              <Icon name={dnsActive ? 'stop_circle' : 'play_circle'} className="text-[16px]" />
              {dnsActive ? 'Stop DNS' : 'Start DNS'}
            </Button>
            {!serverRunning && <p className="mt-1 text-[10px] text-text-muted">Start the MITM server first.</p>}
          </div>
        </div>
      )}
    </Card>
  )
}
