import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { Icon } from '@/components/icon'
import { ManualConfigModal, McpMarketplaceModal, type McpPick } from '@/components/cli-tool-modals'
import { PageHeader } from '@/components/layout'
import { ConfirmModal } from '@/components/modal'
import { ModelSelectModal } from '@/components/model-select-modal'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input, Label } from '@/components/ui/input'
import { api, type ApiKey, type CliToolStatus, type CodexProfile, type Combo } from '@/lib/api'
import { CLAUDE_MODEL_KEYS, cliTool, type CliNote, type CliTool } from '@/lib/cli-tools'
import { cn } from '@/lib/utils'
import {
  deleteEndpointPreset, deleteKeyPreset, readEndpointPresets, readKeyPresets, rememberEndpoint,
  upsertEndpointPreset, upsertKeyPreset, type EndpointPreset, type KeyPreset,
} from '@/lib/cli-presets'
import { errorMessage } from '@/lib/errors'
import { useCopyToClipboard } from '@/lib/use-copy-to-clipboard'
import { useNotify } from '@/lib/toast'

/** Grok Build subagent slots, mirroring GrokBuildToolCard.js SUBAGENT_TYPES. */
const GROK_SUBAGENTS = [
  { id: 'general-purpose', label: 'General-purpose' },
  { id: 'explore', label: 'Explore' },
  { id: 'plan', label: 'Plan' },
]

/** Auto-compact window presets (CLAUDE_CODE_AUTO_COMPACT_WINDOW, valid 100K–1M). */
const CONTEXT_OPTIONS = [
  { label: 'Default', value: '' },
  { label: '200K', value: '198000' },
  { label: '300K', value: '298000' },
  { label: '500K', value: '498000' },
  { label: '700K', value: '698000' },
]

const INSTALL_COMMANDS: Record<string, string> = {
  claude: 'npm install -g @anthropic-ai/claude-code',
  codex: 'npm install -g @openai/codex',
  opencode: 'npm install -g opencode-ai',
  droid: 'curl -fsSL https://app.factory.ai/cli | sh',
  'deepseek-tui': 'npm install -g deepseek-tui',
  'grok-build': 'curl -fsSL https://x.ai/cli/install.sh | bash',
  hermes: 'curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh | bash',
  jcode: 'curl -fsSL https://raw.githubusercontent.com/1jehuang/jcode/master/scripts/install.sh | bash',
  pi: 'curl -fsSL https://pi.dev/install.sh | sh  # or: npm install -g --ignore-scripts @earendil-works/pi-coding-agent',
  omp: 'npm install -g oh-my-pi',
  crush: 'brew install charmbracelet/tap/crush  # or go install github.com/charmbracelet/crush@latest',
  forge: 'cargo install forgecode',
  smelt: 'cargo install smelt',
  codewhale: 'cargo install codewhale',
}

export default function CliToolDetailPage() {
  const { toolId = '' } = useParams()
  const tool = cliTool(toolId)
  if (!tool) {
    return (
      <div className="flex flex-col gap-4">
        <BackLink />
        <p className="text-sm text-text-muted">Tool not found: {toolId}</p>
      </div>
    )
  }
  return <ToolDetail key={tool.id} tool={tool} />
}

function BackLink() {
  return (
    <Link
      to="/dashboard/cli-tools"
      className="inline-flex w-fit items-center gap-1 text-sm text-text-muted hover:text-primary"
    >
      <Icon name="arrow_back" className="text-[18px]" /> Back to CLI Tools
    </Link>
  )
}

type Plugin = { name: string; title?: string; description?: string; url: string; transport?: string; oauth?: boolean; toolNames?: string[]; extensionUrl?: string }
type CustomPlugin = { name: string; url: string; transport: string; custom: true }

function ToolDetail({ tool }: { tool: CliTool }) {
  const notify = useNotify()
  const { copied, copy } = useCopyToClipboard()
  const [status, setStatus] = useState<CliToolStatus | null>(null)
  const [keys, setKeys] = useState<ApiKey[]>([])
  const [models, setModels] = useState<string[]>([])
  const [aliases, setAliases] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [confirmReset, setConfirmReset] = useState(false)
  const [showInstall, setShowInstall] = useState(false)
  const [manualOpen, setManualOpen] = useState(false)
  const [pickerFor, setPickerFor] = useState<string | null>(null)

  const [apiKey, setApiKey] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const [selected, setSelected] = useState<string[]>([])
  const [claudeModels, setClaudeModels] = useState<Record<string, string>>({})
  const [endpoints, setEndpoints] = useState<{ label: string; url: string }[]>([])
  const [endpointMode, setEndpointMode] = useState('local')
  const [endpointPresets, setEndpointPresets] = useState<EndpointPreset[]>(readEndpointPresets)
  const [keyPresets, setKeyPresets] = useState<KeyPreset[]>(readKeyPresets)

  // Claude extras
  const [autoCompact, setAutoCompact] = useState('')
  const [oneM, setOneM] = useState(false)
  const [exaMcp, setExaMcp] = useState(false)
  const [ccFilterNaming, setCcFilterNaming] = useState(false)

  // Grok Build subagent overrides (general-purpose / explore / plan)
  const [grokSubagents, setGrokSubagents] = useState<Record<string, string>>({})

  // Codex extras
  const [subagentModel, setSubagentModel] = useState('')
  const [profiles, setProfiles] = useState<CodexProfile[]>([])
  const [profileAlias, setProfileAlias] = useState('')
  const [profileModel, setProfileModel] = useState('')

  // Cowork extras
  const [plugins, setPlugins] = useState<Plugin[]>([])
  const [localPlugins, setLocalPlugins] = useState<string[]>([])
  const [customPlugins, setCustomPlugins] = useState<CustomPlugin[]>([])
  const [marketplaceOpen, setMarketplaceOpen] = useState(false)
  const [customMcp, setCustomMcp] = useState<{ name: string; url: string } | null>(null)
  const [comboOpen, setComboOpen] = useState(false)

  const origin = useMemo(() => window.location.origin, [])
  const routeId = tool.routeId ?? tool.id

  const load = useCallback(() => {
    if (tool.form === 'guide') return
    api.cliTool(routeId).then(setStatus).catch(() => setStatus({ installed: false }))
    if (tool.id === 'codex') api.codexProfiles().then((d) => setProfiles(d.profiles ?? [])).catch(() => {})
  }, [routeId, tool.form, tool.id])

  useEffect(() => {
    Promise.all([
      (tool.form === 'guide'
        ? Promise.resolve({ installed: true } as CliToolStatus)
        : api.cliTool(routeId)
      ).catch(() => ({ installed: false }) as CliToolStatus),
      api.keys().catch(() => ({ keys: [] as ApiKey[], machineId: '' })),
      api.models().catch(() => ({ object: 'list', data: [] })),
      api.tunnelStatus().catch(() => null),
      api.settings().catch(() => ({} as Record<string, unknown>)),
      api.aliases().catch(() => ({ aliases: {} as Record<string, string> })),
      tool.id === 'codex' ? api.codexProfiles().catch(() => ({ profiles: [] as CodexProfile[] })) : Promise.resolve({ profiles: [] as CodexProfile[] }),
    ])
      .then(([st, k, m, tun, settings, al, prof]) => {
        setStatus(st)
        const opts: { label: string; url: string }[] = [{ label: `Local · ${origin}`, url: origin }]
        if (tun?.tunnel?.enabled && tun.tunnel.publicUrl) opts.push({ label: `Tunnel · ${tun.tunnel.publicUrl}`, url: tun.tunnel.publicUrl })
        if (tun?.tailscale?.enabled && tun.tailscale.tunnelUrl) opts.push({ label: `Tailscale · ${tun.tailscale.tunnelUrl}`, url: tun.tailscale.tunnelUrl })
        if (settings.cloudEnabled && settings.cloudUrl) {
          opts.push({ label: `Cloud · ${String(settings.cloudUrl)}`, url: String(settings.cloudUrl) })
        }
        opts.push({ label: 'Custom URL…', url: '' })
        setEndpoints(opts)
        setKeys(k.keys)
        setApiKey((prev) => prev || k.keys[0]?.key || '')
        setModels(m.data.map((x) => x.id))
        setAliases(al.aliases)
        setBaseUrl((prev) => prev || statusBaseUrl(st) || origin)

        const env = ((st?.settings as Record<string, unknown> | undefined)?.env ?? {}) as Record<string, string>
        setClaudeModels(statusClaudeModels(st))
        setAutoCompact(env.CLAUDE_CODE_AUTO_COMPACT_WINDOW || '')
        setOneM(CLAUDE_MODEL_KEYS.some((c) => env[c.env]?.endsWith('[1m]')))
        setExaMcp(Boolean(st?.exaMcpEnabled))
        setCcFilterNaming(Boolean(settings.ccFilterNaming))

        const cfg = (st?.config ?? st?.settings) as Record<string, any> | undefined
        setSelected((prev) => (prev.length ? prev : statusModels(st)))
        setSubagentModel(cfg?.agents?.default_subagent_model ?? '')
        if (tool.id === 'grok-build') {
          const m = (cfg?.subagentMappings ?? {}) as Record<string, string | null>
          setGrokSubagents(Object.fromEntries(Object.entries(m).filter(([, v]) => v)) as Record<string, string>)
        }

        const cowork = st?.cowork as Record<string, any> | undefined
        if (cowork) {
          setSelected((prev) => (prev.length ? prev : (cowork.models ?? []).map((x: any) => (typeof x === 'string' ? x : x?.name)).filter(Boolean)))
          setPlugins((cowork.plugins ?? []).map((p: any) => ({ ...p })))
          setLocalPlugins(Array.isArray(cowork.localPlugins) ? cowork.localPlugins : [])
          setCustomPlugins((cowork.customPlugins ?? []).map((p: any) => ({ ...p, transport: p.transport || 'sse', custom: true })))
        }
        setProfiles(prof.profiles ?? [])
      })
      .finally(() => setLoading(false))
  }, [routeId, origin, tool.form, tool.id])

  const stripMarker = (v: string) => v.replace(/\[1m\]$/, '')
  const toggleOneM = (enabled: boolean) => {
    setOneM(enabled)
    setClaudeModels((prev) => {
      const next: Record<string, string> = {}
      for (const [k, v] of Object.entries(prev)) next[k] = enabled && v ? `${stripMarker(v)}[1m]` : stripMarker(v)
      return next
    })
  }

  const toggleFilterNaming = async (enabled: boolean) => {
    setCcFilterNaming(enabled)
    try {
      await api.patchSettings({ ccFilterNaming: enabled })
    } catch (e) {
      setCcFilterNaming(!enabled)
      notify.error(errorMessage(e instanceof Error ? e.message : e, 'Failed to save setting'))
    }
  }

  const submit = async () => {
    setBusy(true)
    try {
      let body: Record<string, unknown>
      switch (tool.form) {
        case 'env':
          body = {
            env: {
              ANTHROPIC_BASE_URL: withV1(baseUrl),
              ANTHROPIC_AUTH_TOKEN: apiKey,
              ...Object.fromEntries(
                CLAUDE_MODEL_KEYS.filter((k) => claudeModels[k.env]).map((k) => [k.env, claudeModels[k.env]]),
              ),
            },
            exaMcpEnabled: exaMcp,
            autoCompactWindow: autoCompact,
          }
          break
        case 'multi':
          body = tool.id === 'cowork'
            ? {
              baseUrl: withV1(baseUrl), apiKey, models: selected,
              plugins: plugins.map((p) => ({
                name: p.name, url: p.url, transport: p.transport, oauth: p.oauth, toolNames: p.toolNames,
              })),
              localPlugins, customPlugins,
            }
            : { baseUrl: withV1(baseUrl), apiKey, models: selected }
          break
        case 'baseUrl':
          body = { baseUrl: withV1(baseUrl), apiKey, model: selected[0] || '' }
          break
        default:
          body = { baseUrl: withV1(baseUrl), apiKey, model: selected[0] || '' }
          if (tool.id === 'codex') body.subagentModel = subagentModel || selected[0] || ''
          if (tool.id === 'grok-build') {
            body.subagentModels = Object.fromEntries(
              Object.entries(grokSubagents).filter(([, v]) => v.trim()).map(([k, v]) => [k, { model: v.trim() }]),
            )
          }
      }
      await api.cliApply(routeId, body)
      rememberEndpoint(
        baseUrl,
        [origin, ...(endpoints.filter((e) => e.url).map((e) => e.url))],
      )
      setEndpointPresets(readEndpointPresets())
      notify.success(`${tool.name} is now routed through this server.`)
      load()
    } catch (e) {
      notify.error(errorMessage(e instanceof Error ? e.message : e, 'Failed to apply settings.'))
    } finally {
      setBusy(false)
    }
  }

  const reset = async () => {
    setBusy(true)
    try {
      await api.cliReset(routeId)
      notify.success('Configuration removed.')
      load()
    } catch (e) {
      notify.error(errorMessage(e instanceof Error ? e.message : e, 'Failed to reset settings.'))
    } finally {
      setBusy(false)
      setConfirmReset(false)
    }
  }

  const addProfile = async () => {
    if (!profileAlias.trim() || !profileModel.trim()) return
    setBusy(true)
    try {
      await api.createCodexProfile(profileAlias.trim(), profileModel.trim())
      setProfileAlias('')
      setProfileModel('')
      setProfiles((await api.codexProfiles()).profiles ?? [])
    } catch (e) {
      notify.error(errorMessage(e instanceof Error ? e.message : e, 'Failed to add model'))
    } finally {
      setBusy(false)
    }
  }

  const removeProfile = async (name: string) => {
    try {
      await api.deleteCodexProfile(name)
      setProfiles((await api.codexProfiles()).profiles ?? [])
    } catch (e) {
      notify.error(errorMessage(e instanceof Error ? e.message : e, 'Failed to delete profile'))
    }
  }

  const pickModel = (value: string) => {
    if (pickerFor?.startsWith('grok:')) setGrokSubagents((prev) => ({ ...prev, [pickerFor.slice(5)]: value }))
    else if (pickerFor === '__subagent__') setSubagentModel(value)
    else if (pickerFor === '__profile__') setProfileModel(value)
    else if (pickerFor) setClaudeModels((prev) => ({ ...prev, [pickerFor]: value }))
    setPickerFor(null)
  }

  const preview = `ANTHROPIC_BASE_URL=${withV1(baseUrl)}\nANTHROPIC_AUTH_TOKEN=${apiKey || '<api key>'}`
  const installCommand = INSTALL_COMMANDS[tool.id] ?? `npm install -g ${tool.id}`

  return (
    <div className="flex flex-col gap-4">
      <BackLink />
      <PageHeader
        title={tool.name}
        description={tool.description}
        actions={
          <>
            <BadgeState status={status} loading={loading} guide={tool.form === 'guide'} />
            {tool.form !== 'guide' && status?.installed !== false && (
              <Button variant="outline" size="sm" onClick={() => setConfirmReset(true)} disabled={busy}>
                <Icon name="restart_alt" className="text-[16px]" /> Reset
              </Button>
            )}
          </>
        }
      />

      {tool.form !== 'guide' && status?.installed === false && (
        <div className="flex flex-col gap-3 rounded-lg border border-warning/30 bg-warning/10 p-4">
          <div className="flex items-start gap-3">
            <Icon name="warning" className="text-warning" />
            <div className="flex-1">
              <p className="font-medium text-warning">{tool.name} not detected locally</p>
              <p className="text-sm text-text-muted">
                Manual configuration is still available if this server runs on a remote machine.
              </p>
            </div>
          </div>
          <div className="flex items-center gap-2 pl-9">
            <Button variant="secondary" size="sm" onClick={() => setManualOpen(true)}>
              <Icon name="content_copy" className="text-[18px]" /> Manual Config
            </Button>
            <Button variant="outline" size="sm" onClick={() => setShowInstall(!showInstall)}>
              <Icon name={showInstall ? 'expand_less' : 'help'} className="text-[18px]" />
              {showInstall ? 'Hide' : 'How to Install'}
            </Button>
          </div>
          {showInstall && (
            <div className="rounded-lg border border-border bg-surface p-4">
              <h4 className="mb-2 font-medium">Installation Guide</h4>
              <p className="mb-1 text-sm text-text-muted">macOS / Linux / Windows:</p>
              <code className="block rounded bg-surface-2 px-3 py-2 font-mono text-xs">{installCommand}</code>
            </div>
          )}
        </div>
      )}

      {tool.form === 'guide' ? (
        <GuideCard
          tool={tool}
          baseUrl={withV1(baseUrl)}
          apiKey={apiKey}
          onApiKey={setApiKey}
          keys={keys}
          models={models}
          hasExternalUrl={endpoints.some(
            (e) => e.label.startsWith('Tunnel') || e.label.startsWith('Cloud') || e.label.startsWith('Tailscale'),
          )}
        />
      ) : (
        <Card>
          <CardHeader>
            <CardTitle>Route {tool.name} through this server</CardTitle>
            <CardDescription>
              Writes {configPath(tool, status)}. The key is stored in the tool&apos;s own config file.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <Notes notes={tool.notes} />
            <Field label="Server URL">
              <div className="flex flex-col gap-1.5">
                <select
                  className="h-9 w-full rounded-md border border-border bg-transparent px-2 text-sm"
                  value={endpointMode}
                  onChange={(e) => {
                    const mode = e.target.value
                    setEndpointMode(mode)
                    const preset = endpointPresets.find((p) => `preset:${p.name}` === mode)
                    const pick = endpoints[Number(mode)]
                    setBaseUrl(preset ? preset.baseUrl : mode.startsWith('preset:') || mode === '__custom' ? '' : (pick?.url ?? ''))
                  }}
                >
                  {endpoints.map((opt, i) =>
                    opt.label === 'Custom URL…' ? (
                      <option key="__custom" value="__custom">Custom URL…</option>
                    ) : (
                      <option key={opt.label} value={String(i)}>{opt.label}</option>
                    ),
                  )}
                  {endpointPresets.map((p) => (
                    <option key={`preset:${p.name}`} value={`preset:${p.name}`}>Saved · {p.name}</option>
                  ))}
                </select>
                {(endpointMode === '__custom' || endpointMode.startsWith('preset:')) && (
                  <Input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder={origin} />
                )}
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      const name = window.prompt('Preset name', '')
                      if (!name?.trim()) return
                      upsertEndpointPreset(baseUrl || origin, name.trim())
                      setEndpointPresets(readEndpointPresets())
                    }}
                  >
                    <Icon name="bookmark_add" className="text-[16px]" /> Save current as…
                  </Button>
                  {endpointMode.startsWith('preset:') && (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        deleteEndpointPreset(endpointMode.slice('preset:'.length))
                        setEndpointMode('local')
                        setEndpointPresets(readEndpointPresets())
                      }}
                    >
                      <Icon name="delete" className="text-[16px]" /> Delete preset
                    </Button>
                  )}
                </div>
              </div>
            </Field>

            {tool.form !== 'baseUrl' && (
              <Field label="API key">
                <div className="flex flex-col gap-1.5">
                  <select
                    className="h-9 w-full rounded-md border border-border bg-transparent px-2 text-sm"
                    value={apiKey}
                    onChange={(e) => setApiKey(e.target.value)}
                  >
                    <option value="">sk_kinerouter (placeholder)</option>
                    {keys.map((k) => (
                      <option key={k.id} value={k.key}>
                        {k.name || k.key.slice(0, 12)}
                      </option>
                    ))}
                    {keyPresets.map((p) => (
                      <option key={`preset:${p.name}`} value={p.key}>Saved · {p.name}</option>
                    ))}
                  </select>
                  <div className="flex flex-wrap items-center gap-2">
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={!apiKey}
                      onClick={() => {
                        const name = window.prompt('Preset name', '')
                        if (!name?.trim()) return
                        upsertKeyPreset(apiKey, name.trim())
                        setKeyPresets(readKeyPresets())
                      }}
                    >
                      <Icon name="bookmark_add" className="text-[16px]" /> Save current as…
                    </Button>
                    {keyPresets.some((p) => p.key === apiKey) && (
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          deleteKeyPreset(keyPresets.find((p) => p.key === apiKey)!.name)
                          setKeyPresets(readKeyPresets())
                        }}
                      >
                        <Icon name="delete" className="text-[16px]" /> Delete preset
                      </Button>
                    )}
                  </div>
                </div>
              </Field>
            )}

            {tool.form === 'env' ? (
              <>
                {CLAUDE_MODEL_KEYS.map((k) => (
                  <Field key={k.env} label={k.alias}>
                    <div className="flex items-center gap-2">
                      <div className="relative flex-1">
                        <Input
                          className="pr-7 font-mono text-xs"
                          value={claudeModels[k.env] ?? ''}
                          onChange={(e) => setClaudeModels((prev) => ({ ...prev, [k.env]: e.target.value }))}
                          placeholder="provider/model-id"
                        />
                        {claudeModels[k.env] && (
                          <button
                            className="absolute right-1 top-1/2 -translate-y-1/2 rounded p-0.5 text-text-muted hover:text-danger"
                            onClick={() => setClaudeModels((prev) => ({ ...prev, [k.env]: '' }))}
                            title="Clear"
                          >
                            <Icon name="close" className="text-[14px]" />
                          </button>
                        )}
                      </div>
                      <Button variant="outline" size="sm" onClick={() => setPickerFor(k.env)}>
                        Select
                      </Button>
                    </div>
                  </Field>
                ))}

                <Field label="Auto-compact">
                  <select
                    className="h-9 w-full rounded-md border border-border bg-transparent px-2 text-sm"
                    value={autoCompact}
                    onChange={(e) => setAutoCompact(e.target.value)}
                  >
                    {CONTEXT_OPTIONS.map((o) => (
                      <option key={o.label} value={o.value}>{o.label}</option>
                    ))}
                  </select>
                </Field>

                <label className="flex cursor-pointer items-center gap-2 text-xs text-text-muted">
                  <input type="checkbox" checked={oneM} onChange={(e) => toggleOneM(e.target.checked)} />
                  <span>
                    1M context — append <code className="font-mono">[1m]</code> to every mapped model name.
                    Claude Code otherwise assumes 200K, which clamps the auto-compact window.
                  </span>
                </label>

                <label className="flex cursor-pointer items-center gap-2 text-xs text-text-muted">
                  <input type="checkbox" checked={ccFilterNaming} onChange={(e) => toggleFilterNaming(e.target.checked)} />
                  <span>
                    Filter naming requests — answer Claude Code&apos;s topic-naming calls locally to save tokens.
                  </span>
                </label>

                <label className="flex cursor-pointer items-center gap-2 text-xs text-text-muted">
                  <input type="checkbox" checked={exaMcp} onChange={(e) => setExaMcp(e.target.checked)} />
                  <span>
                    Exa MCP — inject web search into <code className="font-mono">~/.claude.json</code> for non-Claude
                    models. Restart Claude Code after Apply.
                  </span>
                </label>
              </>
            ) : tool.form === 'multi' ? (
              tool.id === 'cowork' ? (
                <CoworkFields
                  models={selected}
                  setModels={setSelected}
                  status={status}
                  plugins={plugins}
                  setPlugins={setPlugins}
                  localPlugins={localPlugins}
                  setLocalPlugins={setLocalPlugins}
                  customPlugins={customPlugins}
                  setCustomPlugins={setCustomPlugins}
                  onBrowse={() => setMarketplaceOpen(true)}
                  onCustom={() => setCustomMcp({ name: '', url: '' })}
                  onCombo={() => setComboOpen(true)}
                  allModels={models}
                />
              ) : (
                <Field label={`Models (${selected.length} selected)`}>
                  <div className="custom-scrollbar max-h-56 overflow-y-auto rounded-md border border-border p-1">
                    {models.length === 0 && (
                      <p className="p-3 text-xs text-text-muted">No models — add a provider connection first.</p>
                    )}
                    {models.map((m) => (
                      <label
                        key={m}
                        className="flex cursor-pointer items-center gap-2 rounded px-2 py-1 text-xs hover:bg-surface-2"
                      >
                        <input
                          type="checkbox"
                          checked={selected.includes(m)}
                          onChange={() =>
                            setSelected((prev) => (prev.includes(m) ? prev.filter((x) => x !== m) : [...prev, m]))
                          }
                        />
                        <code className="font-mono">{m}</code>
                      </label>
                    ))}
                  </div>
                </Field>
              )
            ) : (
              <>
                <Field label="Model">
                  <div className="flex items-center gap-2">
                    <div className="relative flex-1">
                      <Input
                        className="pr-7 font-mono text-xs"
                        value={selected[0] ?? ''}
                        onChange={(e) => setSelected([e.target.value])}
                        placeholder="provider/model"
                      />
                      {selected[0] && (
                        <button
                          className="absolute right-1 top-1/2 -translate-y-1/2 rounded p-0.5 text-text-muted hover:text-danger"
                          onClick={() => setSelected([])}
                          title="Clear"
                        >
                          <Icon name="close" className="text-[14px]" />
                        </button>
                      )}
                    </div>
                    <Button variant="outline" size="sm" onClick={() => setPickerFor('__main__')}>
                      Select
                    </Button>
                  </div>
                </Field>

                {tool.id === 'codex' && (
                  <>
                    <Field label="Subagent model">
                      <div className="flex items-center gap-2">
                        <div className="relative flex-1">
                          <Input
                            className="pr-7 font-mono text-xs"
                            value={subagentModel}
                            onChange={(e) => setSubagentModel(e.target.value)}
                            placeholder="falls back to the main model"
                          />
                          {subagentModel && (
                            <button
                              className="absolute right-1 top-1/2 -translate-y-1/2 rounded p-0.5 text-text-muted hover:text-danger"
                              onClick={() => setSubagentModel('')}
                              title="Clear"
                            >
                              <Icon name="close" className="text-[14px]" />
                            </button>
                          )}
                        </div>
                        <Button variant="outline" size="sm" onClick={() => setPickerFor('__subagent__')}>
                          Select
                        </Button>
                      </div>
                    </Field>

                    <CodexProfiles
                      profiles={profiles}
                      alias={profileAlias}
                      setAlias={setProfileAlias}
                      model={profileModel}
                      setModel={setProfileModel}
                      busy={busy}
                      onPickModel={() => setPickerFor('__profile__')}
                      onAdd={addProfile}
                      onDelete={removeProfile}
                      copied={copied}
                      copy={copy}
                    />
                  </>
                )}

                {tool.id === 'grok-build' && (
                  <>
                    {GROK_SUBAGENTS.map((sub) => (
                      <Field key={sub.id} label={sub.label}>
                        <div className="flex items-center gap-2">
                          <div className="relative flex-1">
                            <Input
                              className="pr-7 font-mono text-xs"
                              value={grokSubagents[sub.id] ?? ''}
                              onChange={(e) => setGrokSubagents((p) => ({ ...p, [sub.id]: e.target.value }))}
                              placeholder="falls back to the main model"
                            />
                            {grokSubagents[sub.id] && (
                              <button
                                className="absolute right-1 top-1/2 -translate-y-1/2 rounded p-0.5 text-text-muted hover:text-danger"
                                onClick={() => setGrokSubagents((p) => ({ ...p, [sub.id]: '' }))}
                                title="Clear"
                              >
                                <Icon name="close" className="text-[14px]" />
                              </button>
                            )}
                          </div>
                          <Button variant="outline" size="sm" onClick={() => setPickerFor(`grok:${sub.id}`)}>
                            Select
                          </Button>
                        </div>
                      </Field>
                    ))}
                  </>
                )}
              </>
            )}

            <div className="flex flex-wrap items-center gap-2 pt-1">
              <Button size="sm" onClick={submit} disabled={busy || !baseUrl}>
                {busy ? 'Applying…' : 'Apply'}
              </Button>
              {tool.form === 'env' && (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => copy(preview, 'preview')}
                  title="Copy the env block"
                >
                  <Icon name={copied === 'preview' ? 'check' : 'content_copy'} className="text-[16px]" />
                  Copy env
                </Button>
              )}
              <Button variant="ghost" size="sm" onClick={() => setManualOpen(true)}>
                <Icon name="content_copy" className="text-[16px]" /> Manual Config
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      <ManualConfigModal
        isOpen={manualOpen}
        onClose={() => setManualOpen(false)}
        title={`${tool.name} — Manual Configuration`}
        configs={manualConfigs(tool, withV1(baseUrl), apiKey, selected, claudeModels, autoCompact, subagentModel)}
      />

      <ModelSelectModal
        isOpen={pickerFor !== null}
        onClose={() => setPickerFor(null)}
        onSelect={(m) => pickModel(m.value)}
        selectedModel={
          pickerFor?.startsWith('grok:') ? grokSubagents[pickerFor.slice(5)]
            : pickerFor === '__subagent__' ? subagentModel
            : pickerFor === '__profile__' ? profileModel
              : pickerFor === '__main__' ? selected[0]
                : pickerFor ? claudeModels[pickerFor] : undefined
        }
        modelAliases={aliases}
        title="Select Model"
      />

      {tool.id === 'cowork' && (
        <>
          <McpMarketplaceModal
            isOpen={marketplaceOpen}
            onClose={() => setMarketplaceOpen(false)}
            addedNames={plugins.map((p) => p.name)}
            onAdd={(pick: McpPick) =>
              setPlugins((prev) => [...prev.filter((p) => p.name !== pick.name), pick])
            }
          />
          {customMcp && (
            <AddCustomMcpModal
              value={customMcp}
              onChange={setCustomMcp}
              onAdd={() => {
                const name = customMcp.name.trim()
                if (!name || !customMcp.url.trim()) return
                setCustomPlugins((prev) => [
                  ...prev.filter((p) => p.name !== name),
                  { name, url: customMcp.url.trim(), transport: 'sse', custom: true },
                ])
                setCustomMcp(null)
              }}
              onClose={() => setCustomMcp(null)}
            />
          )}
          {comboOpen && (
            <CoworkComboModal
              onClose={() => setComboOpen(false)}
              onCreated={(combo) => {
                setSelected((prev) => (prev.includes(combo.name) ? prev : [...prev, combo.name]))
                setComboOpen(false)
                notify.success(`Combo "${combo.name}" created and added.`)
              }}
            />
          )}
        </>
      )}

      <ConfirmModal
        isOpen={confirmReset}
        onClose={() => setConfirmReset(false)}
        onConfirm={reset}
        title={`Reset ${tool.name}`}
        message="This removes the router's entry from the tool's config file. Any other settings in that file are kept."
        confirmLabel="Reset"
        variant="danger"
        loading={busy}
      />
    </div>
  )
}

function CoworkFields({
  models, setModels, status, plugins, setPlugins, localPlugins, setLocalPlugins,
  customPlugins, setCustomPlugins, onBrowse, onCustom, onCombo, allModels,
}: {
  models: string[]
  setModels: (updater: (prev: string[]) => string[]) => void
  status: CliToolStatus | null
  plugins: Plugin[]
  setPlugins: (updater: (prev: Plugin[]) => Plugin[]) => void
  localPlugins: string[]
  setLocalPlugins: (updater: (prev: string[]) => string[]) => void
  customPlugins: CustomPlugin[]
  setCustomPlugins: (updater: (prev: CustomPlugin[]) => CustomPlugin[]) => void
  onBrowse: () => void
  onCustom: () => void
  onCombo: () => void
  allModels: string[]
}) {
  const defaults = (status?.defaultPlugins as Plugin[] | undefined) ?? []
  const localDefs = (status?.localStdioPlugins as Plugin[] | undefined) ?? []
  const exa = defaults.find((p) => p.name === 'exa')
  const exaEnabled = plugins.some((p) => p.name === 'exa')
  const browser = localDefs.find((p) => p.name === 'browsermcp')
  const others = localDefs.filter((p) => p.name !== 'browsermcp')

  return (
    <>
      <Field label="Models">
        <div className="flex flex-col gap-1.5">
          <div className="flex flex-wrap gap-1.5 rounded-md border border-border bg-surface px-2 py-1.5">
            {models.length === 0 && <span className="text-xs text-text-muted">No models selected</span>}
            {models.map((m) => (
              <span key={m} className="inline-flex items-center gap-1 rounded bg-surface-2 px-2 py-0.5 text-xs">
                {m}
                <button className="hover:text-danger" onClick={() => setModels((prev) => prev.filter((x) => x !== m))}>
                  <Icon name="close" className="text-[12px]" />
                </button>
              </span>
            ))}
          </div>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={onCombo}>+ Combo</Button>
            <select
              className="h-8 flex-1 rounded-md border border-border bg-transparent px-2 text-xs"
              value=""
              onChange={(e) => {
                const v = e.target.value
                if (v) setModels((prev) => (prev.includes(v) ? prev : [...prev, v]))
              }}
            >
              <option value="">Add model…</option>
              {allModels.map((m) => <option key={m} value={m}>{m}</option>)}
            </select>
          </div>
        </div>
      </Field>

      <Field label="MCP">
        <div className="flex flex-col gap-1">
          {plugins.filter((p) => p.name !== 'exa').map((p) => (
            <div key={p.name} className="flex items-center gap-2 rounded border border-border bg-surface px-2 py-1">
              <span className="shrink-0 truncate text-xs font-medium">{p.title || p.name}</span>
              {p.oauth && <span className="shrink-0 text-[8px] text-warning">OAuth</span>}
              <div className="flex flex-1 flex-wrap gap-1 overflow-hidden">
                {(p.toolNames ?? []).slice(0, 6).map((t) => (
                  <span key={t} className="whitespace-nowrap rounded bg-surface-2 px-1 py-0.5 text-[9px] text-text-muted">{t}</span>
                ))}
                {(p.toolNames?.length ?? 0) > 6 && (
                  <span className="rounded bg-surface-2 px-1 py-0.5 text-[9px] text-text-muted">
                    +{(p.toolNames?.length ?? 0) - 6}
                  </span>
                )}
              </div>
              <button
                className="ml-auto shrink-0 hover:text-danger"
                onClick={() => setPlugins((prev) => prev.filter((x) => x.name !== p.name))}
              >
                <Icon name="close" className="text-[12px]" />
              </button>
            </div>
          ))}
          {customPlugins.map((p) => (
            <div key={p.name} className="flex items-center gap-2 rounded border border-border bg-surface px-2 py-1">
              <span className="shrink-0 truncate text-xs font-medium">{p.name}</span>
              <span className="shrink-0 rounded bg-primary/10 px-1 py-0.5 text-[8px] text-primary">custom</span>
              <span className="flex-1 truncate text-[9px] text-text-muted">{p.url}</span>
              <button
                className="ml-auto shrink-0 hover:text-danger"
                onClick={() => setCustomPlugins((prev) => prev.filter((x) => x.name !== p.name))}
              >
                <Icon name="close" className="text-[12px]" />
              </button>
            </div>
          ))}
          {plugins.filter((p) => p.name !== 'exa').length === 0 && customPlugins.length === 0 && (
            <div className="rounded border border-border bg-surface px-2 py-1.5 text-xs text-text-muted">No MCPs added</div>
          )}
          <div className="mt-0.5 flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={onBrowse}>+ Browse</Button>
            <Button variant="ghost" size="sm" onClick={onCustom}>+ Custom</Button>
            <a
              href="https://mcp.so"
              target="_blank"
              rel="noreferrer"
              className="ml-auto text-[10px] text-text-muted underline hover:text-primary"
            >
              Find MCPs →
            </a>
          </div>
        </div>
      </Field>

      <Field label="Tools">
        <div className="flex flex-col gap-1.5">
          {exa && (
            <label className="flex cursor-pointer items-start gap-2 rounded border border-border bg-surface px-2 py-1.5">
              <input
                type="checkbox"
                className="mt-0.5"
                checked={exaEnabled}
                onChange={(e) =>
                  setPlugins((prev) =>
                    e.target.checked ? [...prev.filter((p) => p.name !== 'exa'), exa] : prev.filter((p) => p.name !== 'exa'),
                  )
                }
              />
              <span className="flex-1">
                <span className="block text-xs font-medium">Web Search &amp; Fetch (Exa)</span>
                <span className="block text-[10px] leading-snug text-text-muted">
                  Replaces built-in WebSearch/WebFetch.
                </span>
              </span>
            </label>
          )}
          {browser && (
            <label className="flex cursor-pointer items-start gap-2 rounded border border-border bg-surface px-2 py-1.5">
              <input
                type="checkbox"
                className="mt-0.5"
                checked={localPlugins.includes('browsermcp')}
                onChange={(e) =>
                  setLocalPlugins((prev) =>
                    e.target.checked ? [...prev, 'browsermcp'] : prev.filter((n) => n !== 'browsermcp'),
                  )
                }
              />
              <span className="flex-1">
                <span className="block text-xs font-medium">Browser Control (Browser MCP)</span>
                <span className="block text-[10px] leading-snug text-text-muted">
                  Controls your running Chrome.{' '}
                  {browser.extensionUrl && (
                    <a href={browser.extensionUrl} target="_blank" rel="noreferrer" className="text-primary underline">
                      Install Chrome extension
                    </a>
                  )}
                </span>
              </span>
            </label>
          )}
        </div>
      </Field>

      {others.length > 0 && (
        <Field label="Local Plugins">
          <div className="flex flex-col gap-1.5 rounded border border-border bg-surface px-2 py-1.5">
            {others.map((p) => (
              <label key={p.name} className="flex cursor-pointer items-start gap-2">
                <input
                  type="checkbox"
                  className="mt-0.5"
                  checked={localPlugins.includes(p.name)}
                  onChange={(e) =>
                    setLocalPlugins((prev) =>
                      e.target.checked ? [...prev, p.name] : prev.filter((n) => n !== p.name),
                    )
                  }
                />
                <span className="flex-1">
                  <span className="flex flex-wrap items-center gap-1.5">
                    <span className="text-xs font-medium">{p.title}</span>
                    <span className="text-[8px] text-warning">stdio</span>
                  </span>
                  <span className="block text-[10px] leading-snug text-text-muted">{p.description}</span>
                </span>
              </label>
            ))}
          </div>
        </Field>
      )}
    </>
  )
}

function CodexProfiles({
  profiles, alias, setAlias, model, setModel, busy, onPickModel, onAdd, onDelete, copied, copy,
}: {
  profiles: CodexProfile[]
  alias: string
  setAlias: (v: string) => void
  model: string
  setModel: (v: string) => void
  busy: boolean
  onPickModel: () => void
  onAdd: () => void
  onDelete: (name: string) => void
  copied: string | null
  copy: (text: string, key?: string) => void
}) {
  return (
    <div className="mt-2 flex flex-col gap-3 border-t border-border pt-3">
      <div className="flex items-center gap-2">
        <span className="flex items-center gap-1.5 text-xs font-semibold text-text-main">
          <Icon name="layers" className="text-[16px] text-primary" /> Additional Models
        </span>
        {profiles.length > 0 && (
          <span className="rounded-full bg-primary/10 px-1.5 py-0.5 text-[10px] text-primary">{profiles.length}</span>
        )}
      </div>

      <div className="flex flex-col gap-1.5 rounded-lg border border-border bg-surface-2/40 p-2.5">
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-[10rem_1fr_auto_auto] sm:items-center">
          <Input
            className="font-mono text-xs"
            value={alias}
            onChange={(e) => setAlias(e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, ''))}
            placeholder="Alias (e.g. claude)"
            onKeyDown={(e) => e.key === 'Enter' && onAdd()}
          />
          <Input
            className="font-mono text-xs"
            value={model}
            onChange={(e) => {
              const v = e.target.value
              setModel(v)
              const provider = v.includes('/') ? v.split('/')[0] : v
              const taken = profiles.map((p) => p.name)
              let name = provider.toLowerCase().replace(/[^a-z0-9_-]/g, '')
              let i = 2
              while (taken.includes(name)) name = `${provider.toLowerCase().replace(/[^a-z0-9_-]/g, '')}-${i++}`
              setAlias(name)
            }}
            placeholder="provider/model-id"
            onKeyDown={(e) => e.key === 'Enter' && onAdd()}
          />
          <Button variant="outline" size="sm" onClick={onPickModel}>Select Model</Button>
          <Button size="sm" onClick={onAdd} disabled={!alias.trim() || !model.trim() || busy}>
            <Icon name="add" className="text-[15px]" /> Add
          </Button>
        </div>
      </div>

      {profiles.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-lg border border-dashed border-border bg-surface/30 p-4 text-center">
          <Icon name="terminal" className="mb-1 text-[20px] text-text-muted" />
          <p className="text-xs text-text-muted">
            Only the main model is active. Add an alias above to configure more models for Codex CLI.
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          {profiles.map((p) => (
            <div
              key={p.name}
              className="group flex items-center justify-between gap-2 rounded-lg border border-border bg-surface px-2.5 py-2"
            >
              <div className="flex min-w-0 flex-col">
                <span className="truncate text-xs font-medium text-text-main">{p.name}</span>
                <span className="truncate font-mono text-[11px] text-text-muted">{p.model}</span>
              </div>
              <div className="flex shrink-0 items-center gap-1">
                <button
                  onClick={() => copy(p.command, p.name)}
                  className={`flex items-center gap-1 rounded border px-2 py-1 font-mono text-[11px] ${
                    copied === p.name
                      ? 'border-success/30 bg-success/10 text-success'
                      : 'border-border bg-surface text-text-muted hover:text-text-main'
                  }`}
                  title="Click to copy command"
                >
                  <Icon name={copied === p.name ? 'check' : 'terminal'} className="text-[13px]" />
                  {p.command}
                </button>
                <button
                  onClick={() => onDelete(p.name)}
                  className="rounded p-1 text-text-muted opacity-0 transition-opacity hover:text-danger group-hover:opacity-100"
                  title="Delete model"
                >
                  <Icon name="close" className="text-[15px]" />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function AddCustomMcpModal({
  value, onChange, onAdd, onClose,
}: {
  value: { name: string; url: string }
  onChange: (v: { name: string; url: string }) => void
  onAdd: () => void
  onClose: () => void
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onClick={onClose}>
      <div
        className="flex w-full max-w-sm flex-col gap-4 rounded-xl border border-border bg-surface p-5 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold">Add Custom MCP</h3>
          <button className="text-text-muted hover:text-text-main" onClick={onClose}>
            <Icon name="close" className="text-[18px]" />
          </button>
        </div>
        <Field label="Name">
          <Input
            placeholder="my-mcp"
            value={value.name}
            onChange={(e) => onChange({ ...value, name: e.target.value.replace(/\s+/g, '-').toLowerCase() })}
          />
        </Field>
        <Field label="SSE URL">
          <Input
            placeholder="https://your-mcp-server.com/sse"
            value={value.url}
            onChange={(e) => onChange({ ...value, url: e.target.value })}
          />
        </Field>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={onClose}>Cancel</Button>
          <Button size="sm" onClick={onAdd}>Add</Button>
        </div>
      </div>
    </div>
  )
}

function CoworkComboModal({ onClose, onCreated }: { onClose: () => void; onCreated: (c: Combo) => void }) {
  const [name, setName] = useState('')
  const [models, setModels] = useState<string[]>([])
  const [aliases, setAliases] = useState<Record<string, string>>({})
  const [pick, setPick] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const valid = /^[a-zA-Z0-9_.-]+$/.test(`claude-${name}`)

  useEffect(() => {
    api.aliases().then((d) => setAliases(d.aliases ?? {})).catch(() => {})
  }, [])

  const create = async () => {
    if (models.length === 0) return
    setBusy(true)
    setError('')
    try {
      const combo = await api.createCombo({ name: `claude-${name.trim()}`, models })
      onCreated(combo)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to create combo')
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onClick={onClose}>
        <div
          className="flex w-full max-w-md flex-col gap-3 rounded-xl border border-border bg-surface p-5 shadow-xl"
          onClick={(e) => e.stopPropagation()}
        >
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold">Create Cowork Combo</h3>
            <button className="text-text-muted hover:text-text-main" onClick={onClose}>
              <Icon name="close" className="text-[18px]" />
            </button>
          </div>
          <Field label="Combo Name">
            <div className="flex items-stretch">
              <span className="inline-flex items-center rounded-l border border-r-0 border-border bg-surface-2 px-2 font-mono text-sm text-text-muted">
                claude-
              </span>
              <Input
                className="rounded-l-none font-mono"
                placeholder="my-combo"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </div>
            <p className="mt-0.5 text-[10px] text-text-muted">
              {valid ? 'Only letters, numbers, -, _ and . allowed' : 'Invalid name'}
            </p>
          </Field>
          <div>
            <Label>Models</Label>
            <div className="mt-1 flex flex-wrap gap-1.5 rounded-md border border-border bg-surface px-2 py-1.5">
              {models.length === 0 && <span className="text-xs text-text-muted">No models added yet</span>}
              {models.map((m) => (
                <span key={m} className="inline-flex items-center gap-1 rounded bg-surface-2 px-2 py-0.5 text-xs">
                  {m}
                  <button className="hover:text-danger" onClick={() => setModels((p) => p.filter((x) => x !== m))}>
                    <Icon name="close" className="text-[12px]" />
                  </button>
                </span>
              ))}
            </div>
            <button
              className="mt-2 flex w-full items-center justify-center gap-1 rounded-lg border border-dashed border-border py-2 text-xs font-medium text-primary hover:border-primary/50"
              onClick={() => setPick(true)}
            >
              <Icon name="add" className="text-[16px]" /> Add Model
            </button>
          </div>
          {error && <p className="text-[11px] text-danger">{error}</p>}
          <div className="flex gap-2 pt-1">
            <Button variant="ghost" size="sm" className="flex-1" onClick={onClose}>Cancel</Button>
            <Button size="sm" className="flex-1" onClick={create} disabled={!name.trim() || !valid || models.length === 0 || busy}>
              {busy ? 'Creating…' : 'Create'}
            </Button>
          </div>
        </div>
      </div>
      <ModelSelectModal
        isOpen={pick}
        onClose={() => setPick(false)}
        onSelect={(m) => setModels((p) => (p.includes(m.value) ? p : [...p, m.value]))}
        onDeselect={(m) => setModels((p) => p.filter((x) => x !== m.value))}
        modelAliases={aliases}
        title="Add Model to Combo"
        addedModelValues={models}
        closeOnSelect={false}
      />
    </>
  )
}

function manualConfigs(
  tool: CliTool,
  baseV1: string,
  key: string,
  models: string[],
  claudeModels: Record<string, string>,
  autoCompact: string,
  subagentModel: string,
): { filename: string; content: string }[] {
  const k = key || 'sk_kinerouter'
  if (tool.form === 'env') {
    const env: Record<string, string> = { ANTHROPIC_BASE_URL: baseV1, ANTHROPIC_AUTH_TOKEN: k }
    for (const c of CLAUDE_MODEL_KEYS) if (claudeModels[c.env]) env[c.env] = claudeModels[c.env]
    if (autoCompact) env.CLAUDE_CODE_AUTO_COMPACT_WINDOW = autoCompact
    return [{ filename: '~/.claude/settings.json', content: JSON.stringify({ hasCompletedOnboarding: true, env }, null, 2) }]
  }
  if (tool.id === 'codex') {
    const model = models[0] || 'provider/model-id'
    return [{
      filename: '~/.codex/config.toml',
      content: `# KineRouter Configuration for Codex CLI
model = "${model}"
model_provider = "kinerouter"

[model_providers.kinerouter]
name = "KineRouter"
base_url = "${baseV1}"
wire_api = "responses"

[model_providers.kinerouter.http_headers]
Authorization = "Bearer ${k}"

[agents]
default_subagent_model = "${subagentModel || model}"
`,
    }]
  }
  return [{
    filename: `${tool.name} settings`,
    content: `Base URL: ${baseV1}\nAPI Key:  ${k}\nModel:    ${models[0] || 'provider/model-id'}\n`,
  }]
}

function GuideCard({
  tool,
  baseUrl,
  apiKey,
  onApiKey,
  keys,
  models,
  hasExternalUrl,
}: {
  tool: CliTool
  baseUrl: string
  apiKey: string
  onApiKey: (v: string) => void
  keys: ApiKey[]
  models: string[]
  hasExternalUrl: boolean
}) {
  const { copied, copy } = useCopyToClipboard()
  const [model, setModel] = useState('')
  const [picker, setPicker] = useState(false)

  // Mirrors DefaultToolCard.replaceVars(): unresolved model falls back to a hint.
  const replaceVars = (text: string) =>
    text
      .replace(/\{\{baseUrl\}\}/g, baseUrl)
      .replace(/\{\{apiKey\}\}/g, apiKey || 'your-api-key')
      .replace(/\{\{model\}\}/g, model || 'provider/model-id')

  // requiresExternalUrl tools (Cursor) write into their own cloud, so a
  // localhost endpoint cannot work — the steps stay hidden until a tunnel or
  // cloud endpoint exists in the endpoint list.
  const canShowGuide = !tool.requiresExternalUrl || hasExternalUrl

  return (
    <Card>
      <CardHeader>
        <CardTitle>Setup</CardTitle>
        <CardDescription>
          {tool.guideSteps
            ? 'This client has no config file the router can write. Follow these steps in its own settings.'
            : 'This client has no config file the router can write. Paste these values into its settings.'}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <Notes notes={tool.notes} hideCloudCheck={hasExternalUrl} />

        {!tool.guideSteps && (
          <>
            <Field label="Base URL">
              <div className="flex gap-2">
                <Input readOnly value={baseUrl} />
                <Button variant="outline" size="sm" onClick={() => copy(baseUrl, 'url')}>
                  <Icon name={copied === 'url' ? 'check' : 'content_copy'} className="text-[16px]" />
                </Button>
              </div>
            </Field>
            <Field label="API key">
              <div className="flex gap-2">
                <Input readOnly value={apiKey} />
                <Button variant="outline" size="sm" onClick={() => copy(apiKey, 'key')}>
                  <Icon name={copied === 'key' ? 'check' : 'content_copy'} className="text-[16px]" />
                </Button>
              </div>
            </Field>
          </>
        )}

        {canShowGuide &&
          (tool.guideSteps ?? []).map((item) => {
            const copyKey = `${item.step}-${item.title}`
            return (
              <div key={item.step} className="flex items-start gap-3">
                <span
                  className="flex size-7 shrink-0 items-center justify-center rounded-full text-xs font-semibold text-white"
                  style={{ backgroundColor: tool.color }}
                >
                  {item.step}
                </span>
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-text-main">{item.title}</p>
                  {item.desc && <p className="mt-0.5 text-xs text-text-muted">{item.desc}</p>}
                  {item.docsUrl && (
                    <a
                      href={item.docsUrl}
                      target="_blank"
                      rel="noreferrer"
                      className="mt-1 inline-flex w-fit items-center gap-1 text-xs text-primary hover:underline"
                    >
                      <Icon name="open_in_new" className="text-[14px]" /> docs
                    </a>
                  )}
                  {item.type === 'apiKeySelector' && (
                    <div className="mt-2 flex gap-2">
                      <Input
                        list={`guide-keys-${tool.id}`}
                        value={apiKey}
                        onChange={(e) => onApiKey(e.target.value)}
                        placeholder="sk_kinerouter"
                        className="font-mono text-xs"
                      />
                      <datalist id={`guide-keys-${tool.id}`}>
                        {keys.map((k) => (
                          <option key={k.id} value={k.key}>
                            {k.name || k.key}
                          </option>
                        ))}
                      </datalist>
                    </div>
                  )}
                  {item.type === 'modelSelector' && (
                    <div className="mt-2 flex items-center gap-2">
                      <Input
                        value={model}
                        onChange={(e) => setModel(e.target.value)}
                        placeholder="provider/model-id"
                        className="font-mono text-xs"
                      />
                      <Button variant="outline" size="sm" onClick={() => setPicker(true)} disabled={models.length === 0}>
                        Select Model
                      </Button>
                      {model && (
                        <>
                          <Button variant="ghost" size="sm" onClick={() => copy(model, `m-${copyKey}`)} title="Copy">
                            <Icon name={copied === `m-${copyKey}` ? 'check' : 'content_copy'} className="text-[16px]" />
                          </Button>
                          <Button variant="ghost" size="sm" onClick={() => setModel('')} title="Clear">
                            <Icon name="close" className="text-[16px]" />
                          </Button>
                        </>
                      )}
                    </div>
                  )}
                  {item.value && (
                    <div className="mt-2 flex gap-2">
                      <code className="min-w-0 flex-1 truncate rounded border border-border bg-surface-2 px-3 py-2 font-mono text-xs">
                        {replaceVars(item.value)}
                      </code>
                      {item.copyable && (
                        <Button variant="outline" size="sm" onClick={() => copy(item.value!, copyKey)}>
                          <Icon name={copied === copyKey ? 'check' : 'content_copy'} className="text-[16px]" />
                        </Button>
                      )}
                    </div>
                  )}
                </div>
              </div>
            )
          })}

        {canShowGuide && tool.codeBlock && (
          <div>
            <div className="mb-2 flex items-center justify-between">
              <span className="text-[10px] uppercase tracking-wide text-text-muted">{tool.codeBlock.language}</span>
              <Button variant="ghost" size="sm" onClick={() => copy(tool.codeBlock!.code, 'codeblock')}>
                <Icon name={copied === 'codeblock' ? 'check' : 'content_copy'} className="text-[14px]" />
                {copied === 'codeblock' ? 'Copied!' : 'Copy'}
              </Button>
            </div>
            <pre className="custom-scrollbar overflow-x-auto rounded border border-border bg-surface-2 p-3 font-mono text-xs">
              {replaceVars(tool.codeBlock.code)}
            </pre>
          </div>
        )}

        {tool.docsUrl && (
          <a
            href={tool.docsUrl}
            target="_blank"
            rel="noreferrer"
            className="inline-flex w-fit items-center gap-1 text-xs text-primary hover:underline"
          >
            <Icon name="open_in_new" className="text-[14px]" /> Documentation
          </a>
        )}
      </CardContent>

      <ModelSelectModal
        isOpen={picker}
        onClose={() => setPicker(false)}
        onSelect={(m) => {
          setModel(m.value)
          setPicker(false)
        }}
        selectedModel={model}
        title="Select Model"
      />
    </Card>
  )
}

/** Reference note callouts; cloudCheck notes disappear once a public URL exists. */
function Notes({ notes, hideCloudCheck }: { notes?: CliNote[]; hideCloudCheck?: boolean }) {
  if (!notes?.length) return null
  const shown = notes.filter((n) => !(n.type === 'cloudCheck' && hideCloudCheck))
  if (!shown.length) return null
  return (
    <div className="flex flex-col gap-2">
      {shown.map((n, i) => {
        const style =
          n.type === 'warning'
            ? 'border-warning/30 bg-warning/10 text-warning'
            : n.type === 'error' || n.type === 'cloudCheck'
              ? 'border-danger/30 bg-danger/10 text-danger'
              : 'border-primary/30 bg-primary/10 text-primary'
        const icon = n.type === 'warning' ? 'warning' : n.type === 'info' ? 'info' : 'error'
        return (
          <div key={i} className={cn('flex items-start gap-2 rounded-lg border px-3 py-2', style)}>
            <Icon name={icon} className="mt-0.5 shrink-0 text-[16px]" />
            <p className="text-xs leading-relaxed">{n.text}</p>
          </div>
        )
      })}
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <Label className="text-xs text-text-muted">{label}</Label>
      {children}
    </div>
  )
}

function BadgeState({
  status,
  loading,
  guide,
}: {
  status: CliToolStatus | null
  loading: boolean
  guide: boolean
}) {
  const { label, cls } = (() => {
    if (guide) return { label: 'Guide', cls: 'bg-muted/60 text-text-muted' }
    if (loading) return { label: 'Loading', cls: 'bg-muted/60 text-text-muted' }
    if (!status) return { label: 'Unknown', cls: 'bg-muted/60 text-text-muted' }
    if (status.installed === false) return { label: 'Not installed', cls: 'bg-muted/60 text-text-muted' }
    if (status.hasKineRouter) return { label: 'Connected', cls: 'bg-success/15 text-success' }
    return { label: 'Not configured', cls: 'bg-warning/15 text-warning' }
  })()
  return <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${cls}`}>{label}</span>
}

function withV1(url: string) {
  const trimmed = url.trim().replace(/\/+$/, '')
  return trimmed.endsWith('/v1') ? trimmed : `${trimmed}/v1`
}

function configPath(tool: CliTool, status: CliToolStatus | null) {
  return (
    (status?.settingsPath as string) ||
    (status?.configPath as string) ||
    (status?.globalStatePath as string) ||
    (status?.authPath as string) ||
    `${tool.name} config`
  )
}

function statusBaseUrl(status: CliToolStatus | null) {
  const v =
    (status?.config as Record<string, unknown> | undefined) ??
    (status?.settings as Record<string, unknown> | undefined)
  const direct = (v?.baseUrl ?? v?.base_url) as string | undefined
  if (direct) return direct.replace(/\/v1$/, '')
  return ''
}

function statusModels(status: CliToolStatus | null): string[] {
  const cfg = (status?.config ?? status?.settings) as Record<string, any> | undefined
  if (!cfg) return []
  const fromProvider = cfg.providers?.['kinerouter']?.models
  if (Array.isArray(fromProvider)) {
    return fromProvider.map((m: any) => (typeof m === 'string' ? m : m?.id)).filter(Boolean)
  }
  const single = cfg.model || cfg.openai?.model
  return typeof single === 'string' && single ? [single] : []
}

function statusClaudeModels(status: CliToolStatus | null): Record<string, string> {
  const env = ((status?.settings as Record<string, any> | undefined)?.env ?? {}) as Record<string, string>
  const out: Record<string, string> = {}
  for (const k of CLAUDE_MODEL_KEYS) {
    if (env[k.env]) out[k.env] = env[k.env]
  }
  return out
}
