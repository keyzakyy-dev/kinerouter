import { useCallback, useEffect, useMemo, useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import {
  api,
  type ApiKey,
  type JailbreakBinding,
  type JailbreakKeyword,
  type JailbreakPreset,
  type JailbreakSyncSource,
} from '@/lib/api'
import { useNotify } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { PageHeader } from '@/components/layout'
import { Icon } from '@/components/icon'
import { Modal } from '@/components/modal'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input, Label } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

const EMPTY: JailbreakPreset = {
  id: '',
  name: '',
  prompt: '',
  geminiSafety: false,
  position: 'system',
  mode: '',
  keywords: [],
  excludes: [],
}

const POSITIONS = [
  { value: 'system', label: 'Prepend as system prompt' },
  { value: 'append', label: 'Append after existing system text' },
]

const MODES = [
  { value: '', label: 'System prompt' },
  { value: 'chat', label: 'Trailing user turn' },
]

const KEYWORD_MODES = [
  { value: '', label: 'Word' },
  { value: 'partial', label: 'Substring' },
  { value: 'regex', label: 'Regex' },
]

function RuleEditor({
  rules,
  onChange,
  placeholder,
}: {
  rules: JailbreakKeyword[]
  onChange: (next: JailbreakKeyword[]) => void
  placeholder: string
}) {
  return (
    <div className="flex flex-col gap-1.5">
      {rules.map((rule, i) => (
        <div key={i} className="flex items-center gap-1.5">
          <Input
            value={rule.text}
            placeholder={placeholder}
            onChange={(e) => onChange(rules.map((r, j) => (j === i ? { ...r, text: e.target.value } : r)))}
          />
          <Select
            value={rule.mode || 'word'}
            onValueChange={(v) =>
              onChange(rules.map((r, j) => (j === i ? { ...r, mode: (v === 'word' ? '' : v) as JailbreakKeyword['mode'] } : r)))
            }
          >
            <SelectTrigger className="w-28 shrink-0">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {KEYWORD_MODES.map((m) => (
                <SelectItem key={m.label} value={m.value || 'word'}>
                  {m.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button
            size="icon"
            variant="ghost"
            onClick={() => onChange(rules.filter((_, j) => j !== i))}
            title="Remove rule"
          >
            <Trash2 className="size-3.5" />
          </Button>
        </div>
      ))}
      <Button size="sm" variant="ghost" className="self-start" onClick={() => onChange([...rules, { text: '', mode: '' }])}>
        <Plus className="size-3.5" /> Add rule
      </Button>
    </div>
  )
}

type SyncResult = { source: string; imported: number; removed: number; error?: string }

export default function JailbreakPage() {
  const notify = useNotify()
  const [tab, setTab] = useState('presets')
  const [presets, setPresets] = useState<JailbreakPreset[]>([])
  const [usage, setUsage] = useState<Record<string, number>>({})
  const [bindings, setBindings] = useState<Record<string, JailbreakBinding>>({})
  const [catalog, setCatalog] = useState<string[]>([])
  const [keys, setKeys] = useState<ApiKey[]>([])
  const [excludes, setExcludes] = useState<JailbreakKeyword[]>([])
  const [sources, setSources] = useState<JailbreakSyncSource[]>([])
  const [github, setGithub] = useState({ hasToken: false, apiBase: '' })
  const [tokenDraft, setTokenDraft] = useState('')
  const [selected, setSelected] = useState<string>('')
  const [draft, setDraft] = useState<JailbreakPreset>({ ...EMPTY })
  const [busy, setBusy] = useState(false)
  const [preview, setPreview] = useState<{ open: boolean; id: string; presetId?: string; reason: string; messages: unknown[] }>({
    open: false,
    id: '',
    reason: '',
    messages: [],
  })
  const [previewModel, setPreviewModel] = useState('')
  const [importURL, setImportURL] = useState('')
  const [syncResults, setSyncResults] = useState<SyncResult[]>([])
  const [newModel, setNewModel] = useState('')
  const [newPresetID, setNewPresetID] = useState('default')
  const [keyID, setKeyID] = useState('')
  const [keyModel, setKeyModel] = useState('')
  const [keyPresetID, setKeyPresetID] = useState('default')
  const [keyBindings, setKeyBindings] = useState<Record<string, JailbreakBinding>>({})

  const load = useCallback(async () => {
    const [p, u, b, c, k, e, s, gh] = await Promise.all([
      api.jailbreakPresets(),
      api.jailbreakUsage(),
      api.jailbreakBindings(),
      api.jailbreakModelCatalog(),
      api.keys(),
      api.jailbreakExcludes(),
      api.jailbreakSources(),
      api.jailbreakGithub(),
    ])
    setPresets(p)
    setUsage(u.usage ?? {})
    setBindings(b)
    setCatalog(c.models)
    setKeys(k.keys)
    setExcludes(e.excludes)
    setSources(s.sources)
    setGithub(gh)
    setSelected((current) => current || p[0]?.id || '')
    setNewPresetID((current) => (current === 'default' && !p.some((x) => x.id === 'default') ? p[0]?.id || current : current))
    setKeyPresetID((current) => (current === 'default' && !p.some((x) => x.id === 'default') ? p[0]?.id || current : current))
  }, [])

  useEffect(() => {
    load().catch((err: Error) => notify.error(err.message))
  }, [load, notify])

  useEffect(() => {
    const preset = presets.find((p) => p.id === selected)
    if (preset) setDraft({ ...preset, keywords: preset.keywords ?? [], excludes: preset.excludes ?? [] })
  }, [presets, selected])

  useEffect(() => {
    if (!keyID) {
      setKeyBindings({})
      return
    }
    api
      .jailbreakKeyBindings(keyID)
      .then(setKeyBindings)
      .catch(() => setKeyBindings({}))
  }, [keyID])

  const presetOptions = useMemo(
    () => presets.map((p) => ({ value: p.id ?? '', label: p.name || p.id || 'untitled' })),
    [presets],
  )

  const run = async (fn: () => Promise<void>, ok?: string) => {
    setBusy(true)
    try {
      await fn()
      if (ok) notify.success(ok)
    } catch (err) {
      notify.error(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const savePreset = () =>
    run(async () => {
      const res = await api.jailbreakSavePreset(draft)
      setSelected(res.preset.id ?? '')
      await load()
    }, 'Preset saved')

  const deletePreset = () =>
    run(async () => {
      if (!draft.id) return
      await api.jailbreakDeletePreset(draft.id)
      setSelected('')
      await load()
    }, 'Preset deleted')

  const duplicatePreset = () =>
    run(async () => {
      if (!draft.id) return
      const res = await api.jailbreakDuplicatePreset(draft.id)
      await load()
      setSelected(res.preset.id ?? '')
    }, 'Preset duplicated')

  const addBinding = () =>
    run(async () => {
      if (!newModel.trim()) return
      await api.jailbreakSetModel({ id: newModel.trim(), enabled: true, presetId: newPresetID })
      setNewModel('')
      await load()
    }, 'Model armed')

  const toggleBinding = (id: string, cfg: JailbreakBinding) =>
    run(async () => {
      await api.jailbreakSetModel({ id, enabled: !cfg.enabled, presetId: cfg.presetId || 'default' })
      await load()
    })

  const deleteBinding = (id: string) =>
    run(async () => {
      await api.jailbreakDeleteModel(id)
      await load()
    }, 'Binding removed')

  const addKeyBinding = () =>
    run(async () => {
      if (!keyID || !keyModel.trim()) return
      await api.jailbreakSetKeyModel({ keyId: keyID, id: keyModel.trim(), enabled: true, presetId: keyPresetID })
      setKeyModel('')
      setKeyBindings(await api.jailbreakKeyBindings(keyID))
    }, 'Key-scoped binding armed')

  const saveExcludes = () => run(async () => {
    const res = await api.jailbreakSaveExcludes(excludes)
    setExcludes(res.excludes)
  }, 'Exclude list saved')

  const importPresets = () =>
    run(async () => {
      const res = await api.jailbreakImport({ url: importURL.trim() })
      setImportURL('')
      await load()
      notify.success(`${res.imported} preset(s) imported from ${res.repo ?? 'source'}`)
    })

  const saveSources = () => run(async () => {
    const res = await api.jailbreakSaveSources(sources)
    setSources(res.sources)
  }, 'Sync sources saved')

  const runSync = () =>
    run(async () => {
      const res = await api.jailbreakSyncRun()
      setSyncResults(res.results)
      await load()
    }, 'Sync finished')

  const saveGithub = () => run(async () => {
    const res = await api.jailbreakSaveGithub({ token: tokenDraft || 'keep', apiBase: github.apiBase })
    setGithub((g) => ({ ...g, hasToken: res.hasToken }))
    setTokenDraft('')
  }, 'GitHub settings saved')

  const armedModel = Object.entries(bindings).find(([, cfg]) => cfg.enabled)?.[0] || ''
  const runPreview = () =>
    run(async () => {
      const model = previewModel.trim() || armedModel || draft.id || ''
      if (!model) return
      const res = await api.jailbreakPreview(
        model,
        [{ role: 'user', content: 'Explain what you can do in one line.' }],
        keyID || undefined,
      )
      setPreview({ open: true, id: model, presetId: res.presetId, reason: res.reason, messages: res.messages })
    })

  const bindingRows = Object.entries(bindings)

  return (
    <div>
      <PageHeader
        title="Persona / Jailbreak"
        description="Prompts injected into outbound requests. Off until a model or key is bound."
        actions={
          <Button
            size="sm"
            onClick={() => {
              setSelected('')
              setDraft({ ...EMPTY })
              setTab('presets')
            }}
          >
            <Plus className="size-3.5" /> New preset
          </Button>
        }
      />

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="presets">Presets</TabsTrigger>
          <TabsTrigger value="bindings">Bindings</TabsTrigger>
          <TabsTrigger value="rules">Rules &amp; Sync</TabsTrigger>
        </TabsList>

        <TabsContent value="presets">
          <div className="grid gap-4 lg:grid-cols-[minmax(0,17rem)_minmax(0,1fr)]">
            <Card className="max-h-[38rem] overflow-y-auto p-2">
              {presets.length === 0 && <p className="px-2 py-6 text-center text-xs text-text-muted">No presets yet.</p>}
              {presets.map((p) => (
                <button
                  key={p.id}
                  onClick={() => setSelected(p.id ?? '')}
                  className={cn(
                    'flex w-full items-start gap-2 rounded-md px-2 py-2 text-left transition-colors',
                    selected === p.id ? 'bg-surface-2' : 'hover:bg-surface-2/60',
                  )}
                >
                  <div className="min-w-0 flex-1">
                    <p className="flex items-center gap-1.5 truncate text-xs font-medium text-text-main">
                      {p.name || p.id}
                      {p.kind && <Badge variant="outline">{p.kind}</Badge>}
                    </p>
                    <p className="truncate text-[11px] text-text-muted">
                      {p.id} · {p.prompt.length.toLocaleString()} chars
                    </p>
                  </div>
                  <span className="shrink-0 text-[10px] tabular-nums text-text-muted">{usage[p.id ?? ''] ?? 0}×</span>
                </button>
              ))}
            </Card>

            <Card className="p-4">
              <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                <p className="text-sm font-medium text-text-main">{draft.id ? `Editing ${draft.id}` : 'New preset'}</p>
                <div className="flex flex-wrap items-center gap-1.5">
                  <Button size="sm" variant="ghost" disabled={!draft.id || busy} onClick={runPreview}>
                    <Icon name="visibility" /> Preview
                  </Button>
                  <Button size="sm" variant="ghost" disabled={!draft.id || busy} onClick={duplicatePreset}>
                    <Icon name="content_copy" /> Duplicate
                  </Button>
                  <Button size="sm" variant="ghost" disabled={!draft.id || busy} onClick={deletePreset}>
                    <Icon name="delete" /> Delete
                  </Button>
                  <Button size="sm" disabled={busy || !draft.prompt.trim()} onClick={savePreset}>
                    Save
                  </Button>
                </div>
              </div>

              <div className="grid gap-3 sm:grid-cols-2">
                <div>
                  <Label>Name</Label>
                  <Input value={draft.name ?? ''} onChange={(e) => setDraft({ ...draft, name: e.target.value })} placeholder="Security reviewer" />
                </div>
                <div>
                  <Label>ID</Label>
                  <Input
                    value={draft.id ?? ''}
                    onChange={(e) => setDraft({ ...draft, id: e.target.value })}
                    placeholder="audit-persona"
                    disabled={Boolean(selected)}
                  />
                </div>
                <div>
                  <Label>Placement</Label>
                  <Select value={draft.position || 'system'} onValueChange={(v) => setDraft({ ...draft, position: v as JailbreakPreset['position'] })}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {POSITIONS.map((p) => (
                        <SelectItem key={p.value} value={p.value}>
                          {p.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div>
                  <Label>Turn type</Label>
                  <Select value={draft.mode || 'system'} onValueChange={(v) => setDraft({ ...draft, mode: (v === 'system' ? '' : v) as JailbreakPreset['mode'] })}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {MODES.map((m) => (
                        <SelectItem key={m.label} value={m.value || 'system'}>
                          {m.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div>
                  <Label>Kind</Label>
                  <Input value={draft.kind ?? ''} onChange={(e) => setDraft({ ...draft, kind: e.target.value })} placeholder="PERSONA, AGENTS, …" />
                </div>
                <label className="flex items-end gap-2 pb-1 text-xs text-text-muted">
                  <Switch
                    checked={Boolean(draft.geminiSafety)}
                    onCheckedChange={(v) => setDraft({ ...draft, geminiSafety: v })}
                  />
                  Disable Gemini safety filters
                </label>
              </div>

              <div className="mt-3">
                <Label>Prompt</Label>
                <textarea
                  value={draft.prompt}
                  onChange={(e) => setDraft({ ...draft, prompt: e.target.value })}
                  rows={12}
                  placeholder={'You are a senior security reviewer. Before answering, list the assumptions you would test.'}
                  className="w-full resize-y rounded-md border border-input bg-background px-3 py-2 font-mono text-xs leading-relaxed text-text-main focus:outline-none focus:ring-1 focus:ring-ring"
                />
              </div>

              <div className="mt-3 grid gap-4 sm:grid-cols-2">
                <div>
                  <Label>Arm when model id matches</Label>
                  <RuleEditor
                    rules={draft.keywords ?? []}
                    onChange={(keywords) => setDraft({ ...draft, keywords })}
                    placeholder="deepseek"
                  />
                </div>
                <div>
                  <Label>Never inject on these ids</Label>
                  <RuleEditor
                    rules={draft.excludes ?? []}
                    onChange={(next) => setDraft({ ...draft, excludes: next })}
                    placeholder="gpt-5"
                  />
                </div>
              </div>
            </Card>
          </div>
        </TabsContent>

        <TabsContent value="bindings">
          <div className="grid gap-4 lg:grid-cols-2">
            <Card className="p-4">
              <p className="mb-1 text-sm font-medium text-text-main">Model bindings</p>
              <p className="mb-3 text-xs text-text-muted">Exact binding beats keyword rules. Unlisted models stay off.</p>
              <div className="mb-3 flex flex-wrap items-end gap-2">
                <div className="min-w-0 flex-1">
                  <Label>Model id</Label>
                  <Input list="jailbreak-models" value={newModel} onChange={(e) => setNewModel(e.target.value)} placeholder="deepseek/deepseek-v4" />
                  <datalist id="jailbreak-models">
                    {catalog.map((m) => (
                      <option key={m} value={m} />
                    ))}
                  </datalist>
                </div>
                <div className="w-40">
                  <Label>Preset</Label>
                  <Select value={newPresetID} onValueChange={setNewPresetID}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {presetOptions.map((p) => (
                        <SelectItem key={p.value} value={p.value}>
                          {p.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <Button size="sm" disabled={busy || !newModel.trim()} onClick={addBinding}>
                  Arm
                </Button>
              </div>

              <div className="flex flex-col divide-y divide-border rounded-md border border-border">
                {bindingRows.length === 0 && <p className="px-3 py-4 text-xs text-text-muted">No model is armed.</p>}
                {bindingRows.map(([id, cfg]) => (
                  <div key={id} className="flex items-center gap-2 px-3 py-2">
                    <Switch
                      checked={cfg.enabled}
                      onCheckedChange={() => toggleBinding(id, cfg)}
                      aria-label={`Toggle ${id}`}
                    />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-xs text-text-main">{id}</p>
                      <p className="text-[11px] text-text-muted">preset: {cfg.presetId || '-'}</p>
                    </div>
                    <Button size="icon" variant="ghost" onClick={() => deleteBinding(id)} title="Remove binding">
                      <Trash2 className="size-3.5" />
                    </Button>
                  </div>
                ))}
              </div>
            </Card>

            <Card className="p-4">
              <p className="mb-1 text-sm font-medium text-text-main">Per-key bindings</p>
              <p className="mb-3 text-xs text-text-muted">
                Overrides the model binding, so a resold client gets its own persona.
              </p>
              <div className="mb-3 grid gap-2 sm:grid-cols-2">
                <div>
                  <Label>API key</Label>
                  <Select value={keyID || 'none'} onValueChange={(v) => setKeyID(v === 'none' ? '' : v)}>
                    <SelectTrigger>
                      <SelectValue placeholder="Select a key" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="none">Select a key</SelectItem>
                      {keys.map((k) => (
                        <SelectItem key={k.id} value={k.id}>
                          {k.name || k.id}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div>
                  <Label>Preset</Label>
                  <Select value={keyPresetID} onValueChange={setKeyPresetID}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {presetOptions.map((p) => (
                        <SelectItem key={p.value} value={p.value}>
                          {p.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="sm:col-span-2">
                  <Label>Model id</Label>
                  <div className="flex gap-2">
                    <Input list="jailbreak-models" value={keyModel} onChange={(e) => setKeyModel(e.target.value)} placeholder="deepseek/deepseek-v4" />
                    <Button size="sm" disabled={busy || !keyID || !keyModel.trim()} onClick={addKeyBinding}>
                      Arm
                    </Button>
                  </div>
                </div>
              </div>

              <div className="flex flex-col divide-y divide-border rounded-md border border-border">
                {Object.keys(keyBindings).length === 0 && (
                  <p className="px-3 py-4 text-xs text-text-muted">{keyID ? 'No binding for this key.' : 'Pick a key to see its bindings.'}</p>
                )}
                {Object.entries(keyBindings).map(([id, cfg]) => (
                  <div key={id} className="flex items-center gap-2 px-3 py-2">
                    <Switch
                      checked={cfg.enabled}
                      onCheckedChange={() =>
                        run(async () => {
                          await api.jailbreakSetKeyModel({ keyId: keyID, id, enabled: !cfg.enabled, presetId: cfg.presetId || 'default' })
                          setKeyBindings(await api.jailbreakKeyBindings(keyID))
                        })
                      }
                    />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-xs text-text-main">{id}</p>
                      <p className="text-[11px] text-text-muted">preset: {cfg.presetId || '-'}</p>
                    </div>
                  </div>
                ))}
              </div>
            </Card>
          </div>
        </TabsContent>

        <TabsContent value="rules">
          <div className="grid gap-4 lg:grid-cols-2">
            <Card className="p-4">
              <p className="mb-1 text-sm font-medium text-text-main">Global excludes</p>
              <p className="mb-3 text-xs text-text-muted">Never injected when the id matches, whatever a binding says.</p>
              <RuleEditor rules={excludes} onChange={setExcludes} placeholder="gpt-4o-mini" />
              <Button size="sm" className="mt-3" disabled={busy} onClick={saveExcludes}>
                Save excludes
              </Button>
            </Card>

            <Card className="p-4">
              <p className="mb-1 text-sm font-medium text-text-main">Import from GitHub</p>
              <p className="mb-3 text-xs text-text-muted">
                Repo, folder, or raw URL. Each .md/.txt becomes a preset; existing ids are overwritten.
              </p>
              <div className="flex gap-2">
                <Input
                  value={importURL}
                  onChange={(e) => setImportURL(e.target.value)}
                  placeholder="https://github.com/owner/prompts"
                />
                <Button size="sm" disabled={busy || !importURL.trim()} onClick={importPresets}>
                  Import
                </Button>
              </div>
            </Card>

            <Card className="p-4">
              <p className="mb-1 text-sm font-medium text-text-main">GitHub credential</p>
              <p className="mb-3 text-xs text-text-muted">
                Token lifts the API limit from 60 to 5,000 an hour. API base targets a mirror.
              </p>
              <div className="grid gap-2 sm:grid-cols-2">
                <div>
                  <Label>Token {github.hasToken && <Badge variant="success">stored</Badge>}</Label>
                  <Input type="password" value={tokenDraft} onChange={(e) => setTokenDraft(e.target.value)} placeholder={github.hasToken ? 'keep current token' : 'ghp_…'} />
                </div>
                <div>
                  <Label>API base</Label>
                  <Input
                    value={github.apiBase}
                    onChange={(e) => setGithub({ ...github, apiBase: e.target.value })}
                    placeholder="https://api.github.com"
                  />
                </div>
              </div>
              <Button size="sm" className="mt-3" disabled={busy} onClick={saveGithub}>
                Save
              </Button>
            </Card>

            <Card className="p-4">
              <p className="mb-1 text-sm font-medium text-text-main">Auto-sync sources</p>
              <p className="mb-3 text-xs text-text-muted">
                Synced presets are rewritten each run; hand-written ones are left alone.
              </p>
              <div className="flex flex-col gap-2">
                {sources.map((src, i) => (
                  <div key={i} className="flex flex-wrap items-center gap-2">
                    <Input
                      className="min-w-0 flex-1"
                      value={src.repo}
                      onChange={(e) => setSources(sources.map((s, j) => (j === i ? { ...s, repo: e.target.value } : s)))}
                      placeholder="owner/prompts"
                    />
                    <Input
                      className="w-24"
                      value={src.ref ?? ''}
                      onChange={(e) => setSources(sources.map((s, j) => (j === i ? { ...s, ref: e.target.value } : s)))}
                      placeholder="main"
                    />
                    <Input
                      className="w-32"
                      value={src.path ?? ''}
                      onChange={(e) => setSources(sources.map((s, j) => (j === i ? { ...s, path: e.target.value } : s)))}
                      placeholder="personas"
                    />
                    <Switch
                      checked={src.enabled}
                      onCheckedChange={(v) => setSources(sources.map((s, j) => (j === i ? { ...s, enabled: v } : s)))}
                    />
                    <Button size="icon" variant="ghost" onClick={() => setSources(sources.filter((_, j) => j !== i))}>
                      <Trash2 className="size-3.5" />
                    </Button>
                  </div>
                ))}
                <Button
                  size="sm"
                  variant="ghost"
                  className="self-start"
                  onClick={() => setSources([...sources, { repo: '', ref: 'main', path: '', enabled: true }])}
                >
                  <Plus className="size-3.5" /> Add source
                </Button>
              </div>
              <div className="mt-3 flex gap-2">
                <Button size="sm" variant="secondary" disabled={busy} onClick={saveSources}>
                  Save sources
                </Button>
                <Button size="sm" disabled={busy} onClick={runSync}>
                  <Icon name="sync" /> Sync now
                </Button>
              </div>
              {syncResults.length > 0 && (
                <div className="mt-3 divide-y divide-border rounded-md border border-border text-xs">
                  {syncResults.map((r, i) => (
                    <div key={i} className="flex items-center justify-between gap-2 px-3 py-1.5">
                      <span className="truncate text-text-main">{r.source}</span>
                      {r.error ? (
                        <span className="shrink-0 text-danger">{r.error}</span>
                      ) : (
                        <span className="shrink-0 text-text-muted">
                          +{r.imported} / −{r.removed}
                        </span>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </Card>
          </div>
        </TabsContent>
      </Tabs>

      <Modal
        isOpen={preview.open}
        onClose={() => setPreview((p) => ({ ...p, open: false }))}
        title="Resolved request preview"
        size="full"
      >
        <div className="flex flex-col gap-3">
          <div className="flex gap-2">
            <Input
              value={previewModel}
              onChange={(e) => setPreviewModel(e.target.value)}
              placeholder={armedModel || 'provider/model'}
            />
            <Button size="sm" variant="secondary" disabled={busy} onClick={runPreview}>
              Resolve
            </Button>
          </div>
          <p className="text-xs text-text-muted">
            {preview.id ? `model ${preview.id}: ` : ''}
            {preview.reason}
          </p>
          <pre className="max-h-96 overflow-auto rounded-md bg-code-background p-3 text-[11px] leading-relaxed text-code-foreground">
            {JSON.stringify(preview.messages, null, 2)}
          </pre>
        </div>
      </Modal>
    </div>
  )
}
