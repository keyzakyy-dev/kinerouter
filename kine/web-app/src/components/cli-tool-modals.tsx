import { useEffect, useMemo, useState } from 'react'
import { Icon } from '@/components/icon'
import { Modal } from '@/components/modal'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { api, type McpRegistryServer } from '@/lib/api'
import { useCopyToClipboard } from '@/lib/use-copy-to-clipboard'

/**
 * Manual-config modal, ported from src/shared/components/ManualConfigModal.js:
 * one or more {filename, content} blocks with a copy button each.
 */
export function ManualConfigModal({
  isOpen,
  onClose,
  title = 'Manual Configuration',
  configs,
}: {
  isOpen: boolean
  onClose: () => void
  title?: string
  configs: { filename: string; content: string }[]
}) {
  const { copied, copy } = useCopyToClipboard()
  return (
    <Modal isOpen={isOpen} onClose={onClose} title={title} size="xl">
      <div className="flex flex-col gap-4">
        {configs.map((config, i) => (
          <div key={config.filename} className="flex flex-col gap-2">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium text-text-main">{config.filename}</span>
              <Button variant="ghost" size="sm" onClick={() => copy(config.content, String(i))}>
                <Icon name={copied === String(i) ? 'check' : 'content_copy'} className="text-[14px]" />
                {copied === String(i) ? 'Copied!' : 'Copy'}
              </Button>
            </div>
            <pre className="custom-scrollbar max-h-60 overflow-auto whitespace-pre-wrap break-all rounded border border-border bg-surface-2 px-3 py-2 font-mono text-xs">
              {config.content}
            </pre>
          </div>
        ))}
      </div>
    </Modal>
  )
}

export type McpPick = {
  name: string
  title: string
  description: string
  url: string
  transport: string
  oauth: boolean
  toolNames: string[]
}

/**
 * Cowork MCP marketplace, ported from src/shared/components/McpMarketplaceModal.js
 * and backed by /api/cli-tools/cowork-mcp-registry + cowork-mcp-tools.
 */
export function McpMarketplaceModal({
  isOpen,
  onClose,
  onAdd,
  addedNames,
}: {
  isOpen: boolean
  onClose: () => void
  onAdd: (pick: McpPick) => void
  addedNames: string[]
}) {
  const [servers, setServers] = useState<McpRegistryServer[]>([])
  const [error, setError] = useState('')
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState('all')
  const [expanded, setExpanded] = useState<string | null>(null)
  const [tools, setTools] = useState<Record<string, { names: string[]; requiresAuth?: boolean; error?: string }>>({})
  const [loadingTools, setLoadingTools] = useState<Record<string, boolean>>({})
  const [selection, setSelection] = useState<Record<string, Record<string, boolean>>>({})

  useEffect(() => {
    if (!isOpen || servers.length > 0) return
    api.mcpRegistry()
      .then((d) => setServers(d.servers ?? []))
      .catch((e) => setError(e instanceof Error ? e.message : 'Failed to load registry'))
  }, [isOpen, servers.length])

  const loading = servers.length === 0 && !error

  const added = useMemo(() => new Set(addedNames), [addedNames])
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return servers.filter((s) => {
      if (filter === 'authless' && s.oauth) return false
      if (filter === 'oauth' && !s.oauth) return false
      if (!q) return true
      return `${s.title} ${s.description} ${s.name}`.toLowerCase().includes(q)
    })
  }, [servers, search, filter])

  const expand = (server: McpRegistryServer) => {
    if (expanded === server.url) {
      setExpanded(null)
      return
    }
    setExpanded(server.url)
    if (tools[server.url]) return
    setLoadingTools((p) => ({ ...p, [server.url]: true }))
    api.mcpTools(server.url)
      .then((d) => {
        const names = d.tools?.length ? d.tools.map((t) => t.name) : server.toolNames
        setTools((p) => ({ ...p, [server.url]: { names, requiresAuth: d.requiresAuth, error: d.error } }))
        setSelection((p) => ({
          ...p,
          [server.url]: Object.fromEntries(names.map((n) => [n, true])),
        }))
      })
      .catch((e) =>
        setTools((p) => ({
          ...p,
          [server.url]: { names: [], error: e instanceof Error ? e.message : 'Probe failed' },
        })),
      )
      .finally(() => setLoadingTools((p) => ({ ...p, [server.url]: false })))
  }

  return (
    <Modal isOpen={isOpen} onClose={onClose} title="Browse MCP Marketplace" size="lg">
      <div className="flex flex-col gap-3">
        <div className="flex items-center gap-2">
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search by name or description…"
          />
          <select
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="h-9 rounded-md border border-border bg-transparent px-2 text-xs"
          >
            <option value="all">All</option>
            <option value="authless">Authless</option>
            <option value="oauth">OAuth</option>
          </select>
        </div>

        {error && <div className="rounded bg-danger/10 px-2 py-1.5 text-xs text-danger">{error}</div>}

        {loading ? (
          <div className="flex items-center justify-center gap-2 py-4 text-xs text-text-muted">
            <Icon name="progress_activity" className="animate-spin text-[18px]" /> Loading registry…
          </div>
        ) : (
          <div className="custom-scrollbar flex max-h-[60vh] flex-col gap-1 overflow-y-auto">
            {filtered.length === 0 && (
              <div className="py-6 text-center text-xs text-text-muted">No servers match filter</div>
            )}
            {filtered.map((s) => {
              const isAdded = added.has(s.slug || s.name)
              const open = expanded === s.url
              const cache = tools[s.url]
              const sel = selection[s.url] ?? {}
              const keys = Object.keys(sel)
              const enabled = Object.values(sel).filter(Boolean).length
              return (
                <div key={s.url} className="rounded">
                  <div className="flex items-start gap-2 px-2 py-2 hover:bg-surface-2">
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className="text-xs font-medium">{s.title}</span>
                        <span
                          className={`rounded px-1 py-0.5 text-[9px] ${
                            s.oauth ? 'bg-warning/10 text-warning' : 'bg-success/10 text-success'
                          }`}
                        >
                          {s.oauth ? 'OAuth' : 'Authless'}
                        </span>
                        {s.toolCount > 0 && <span className="text-[10px] text-text-muted">{s.toolCount} tools</span>}
                      </div>
                      {s.description && (
                        <p className="mt-0.5 line-clamp-2 text-[10px] text-text-muted">{s.description}</p>
                      )}
                    </div>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={isAdded}
                      onClick={() => expand(s)}
                      className={isAdded ? 'text-success' : ''}
                    >
                      {isAdded ? 'Added' : open ? 'Cancel' : '+ Add'}
                    </Button>
                  </div>
                  {open && (
                    <div className="flex flex-col gap-2 border-t border-border bg-surface-2/40 px-3 py-2">
                      {loadingTools[s.url] && (
                        <div className="flex items-center gap-2 py-1 text-[10px] text-text-muted">
                          <Icon name="progress_activity" className="animate-spin text-[14px]" /> Probing server for tools…
                        </div>
                      )}
                      {!loadingTools[s.url] && cache?.requiresAuth && (
                        <p className="rounded bg-warning/10 px-2 py-1 text-[10px] text-warning">
                          OAuth required. Add now and authenticate after Apply.
                        </p>
                      )}
                      {!loadingTools[s.url] && cache?.error && !cache.requiresAuth && (
                        <p className="rounded bg-danger/10 px-2 py-1 text-[10px] text-danger">Probe failed: {cache.error}</p>
                      )}
                      {keys.length > 0 && (
                        <>
                          <div className="flex items-center justify-between">
                            <span className="text-[10px] text-text-muted">
                              {enabled}/{keys.length} tools enabled
                            </span>
                            <div className="flex gap-1 text-[10px]">
                              <button
                                className="text-primary hover:underline"
                                onClick={() =>
                                  setSelection((p) => ({
                                    ...p,
                                    [s.url]: Object.fromEntries(keys.map((k) => [k, true])),
                                  }))
                                }
                              >
                                All
                              </button>
                              <span className="text-text-muted">·</span>
                              <button
                                className="text-primary hover:underline"
                                onClick={() =>
                                  setSelection((p) => ({
                                    ...p,
                                    [s.url]: Object.fromEntries(keys.map((k) => [k, false])),
                                  }))
                                }
                              >
                                None
                              </button>
                            </div>
                          </div>
                          <div className="custom-scrollbar grid max-h-40 grid-cols-2 gap-1 overflow-y-auto">
                            {keys.map((t) => (
                              <label key={t} className="flex cursor-pointer items-center gap-1.5 rounded px-1 text-[10px] hover:bg-surface-2">
                                <input
                                  type="checkbox"
                                  checked={!!sel[t]}
                                  onChange={() =>
                                    setSelection((p) => ({
                                      ...p,
                                      [s.url]: { ...p[s.url], [t]: !p[s.url]?.[t] },
                                    }))
                                  }
                                />
                                <span className="truncate">{t}</span>
                              </label>
                            ))}
                          </div>
                        </>
                      )}
                      <Button
                        size="sm"
                        className="self-end"
                        onClick={() => {
                          onAdd({
                            name: s.slug || s.name,
                            title: s.title,
                            description: s.description,
                            url: s.url,
                            transport: s.transport,
                            oauth: s.oauth,
                            toolNames: keys.filter((k) => sel[k]),
                          })
                          setExpanded(null)
                        }}
                      >
                        Confirm Add
                      </Button>
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}

        <div className="text-right text-[10px] text-text-muted">
          {filtered.length} of {servers.length} servers
        </div>
      </div>
    </Modal>
  )
}
