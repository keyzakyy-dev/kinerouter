import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Icon } from '@/components/icon'
import { ConfirmModal, Modal } from '@/components/modal'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  filterProxyPools,
  getVisibleProxyPoolSelection,
  updateProxyPoolSelection,
} from '@/lib/proxy-pool-selection'
import { errorMessage } from '@/lib/errors'
import { useNotify } from '@/lib/toast'
import { cn } from '@/lib/utils'

type ProxyPool = {
  id: string
  name?: string
  proxyUrl?: string
  noProxy?: string
  isActive?: boolean
  strictProxy?: boolean
  type?: string
  testStatus?: string
  lastTestedAt?: string
  lastError?: string
  boundConnectionCount?: number
}

type FormData = { name: string; proxyUrl: string; noProxy: string; isActive: boolean; strictProxy: boolean }

const emptyForm = (data: Partial<ProxyPool> = {}): FormData => ({
  name: data.name ?? '',
  proxyUrl: data.proxyUrl ?? '',
  noProxy: data.noProxy ?? '',
  isActive: data.isActive !== false,
  strictProxy: data.strictProxy === true,
})

function statusVariant(status?: string) {
  if (status === 'active') return 'success' as const
  if (status === 'error') return 'destructive' as const
  return 'secondary' as const
}

function formatDateTime(value?: string) {
  if (!value) return 'Never'
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? 'Never' : d.toLocaleString()
}

/** Parses one batch-import line: a full URL, or host:port:user:pass. */
function parseProxyLine(line: string): { proxyUrl: string; name: string } | null {
  const trimmed = line.trim()
  if (!trimmed) return null
  if (trimmed.includes('://')) {
    const parsed = new URL(trimmed)
    const hostLabel = parsed.port ? `${parsed.hostname}:${parsed.port}` : parsed.hostname
    return { proxyUrl: parsed.toString(), name: `Imported ${hostLabel}` }
  }
  const parts = trimmed.split(':')
  if (parts.length === 4) {
    const [host, port, username, password] = parts
    if (!host || !port || !username || !password) throw new Error('Invalid host:port:user:pass format')
    const proxyUrl = `http://${encodeURIComponent(username)}:${encodeURIComponent(password)}@${host}:${port}`
    return { proxyUrl: new URL(proxyUrl).toString(), name: `Imported ${host}:${port}` }
  }
  throw new Error('Unsupported format')
}

export default function ProxyPoolsPage() {
  const notify = useNotify()
  const [pools, setPools] = useState<ProxyPool[]>([])
  const [loading, setLoading] = useState(true)
  const [statusFilter, setStatusFilter] = useState('all')
  const [selectedIds, setSelectedIds] = useState<string[]>([])
  const [bulkBusy, setBulkBusy] = useState(false)
  const [testingId, setTestingId] = useState<string | null>(null)
  const [healthChecking, setHealthChecking] = useState(false)
  const [healthProgress, setHealthProgress] = useState({ current: 0, total: 0 })
  const [confirm, setConfirm] = useState<{ title: string; message: string; run: () => void } | null>(null)

  // form modal
  const [showForm, setShowForm] = useState(false)
  const [editing, setEditing] = useState<ProxyPool | null>(null)
  const [form, setForm] = useState<FormData>(emptyForm())
  const [saving, setSaving] = useState(false)

  // batch import modal
  const [showImport, setShowImport] = useState(false)
  const [importText, setImportText] = useState('')
  const [importing, setImporting] = useState(false)

  // relay deploy modals
  const [relay, setRelay] = useState<null | 'vercel' | 'cloudflare' | 'deno' | 'netlify'>(null)
  const [relayMenu, setRelayMenu] = useState(false)
  const [deploying, setDeploying] = useState(false)
  const [vercelForm, setVercelForm] = useState({ vercelToken: '', projectName: 'vercel-relay' })
  const [cloudflareForm, setCloudflareForm] = useState({
    accountId: '',
    apiToken: '',
    projectName: 'cloudflare-relay',
  })
  const [denoForm, setDenoForm] = useState({ denoToken: '', orgDomain: '', projectName: '' })
  const [netlifyForm, setNetlifyForm] = useState({ netlifyToken: '', projectName: 'netlify-relay' })

  const relayMenuRef = useRef<HTMLDivElement>(null)
  const anchorRef = useRef<string | null>(null)

  const visiblePools = useMemo(() => filterProxyPools(pools, statusFilter), [pools, statusFilter])
  const selected = getVisibleProxyPoolSelection(visiblePools, selectedIds)
  const allSelected = visiblePools.length > 0 && selected.length === visiblePools.length
  const activeCount = pools.filter((p) => p.isActive === true).length

  const visiblePoolsRef = useRef<ProxyPool[]>([])
  useEffect(() => {
    visiblePoolsRef.current = visiblePools
  }, [visiblePools])

  const fetchPools = useCallback(async () => {
    try {
      const res = await fetch('/api/proxy-pools?includeUsage=true', { cache: 'no-store' })
      const data = await res.json()
      if (res.ok) setPools(data.proxyPools ?? [])
    } catch {
      // keep the previous list; the header still renders
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchPools()
  }, [fetchPools])

  // Drop selections for pools that no longer exist.
  useEffect(() => {
    setSelectedIds((prev) => prev.filter((id) => pools.some((p) => p.id === id)))
  }, [pools])

  useEffect(() => {
    if (!relayMenu) return
    const onClick = (e: MouseEvent) => {
      if (relayMenuRef.current && !relayMenuRef.current.contains(e.target as Node)) setRelayMenu(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [relayMenu])

  const openCreate = () => {
    setEditing(null)
    setForm(emptyForm())
    setShowForm(true)
  }

  const openEdit = (pool: ProxyPool) => {
    setEditing(pool)
    setForm(emptyForm(pool))
    setShowForm(true)
  }

  const save = async () => {
    const payload = {
      name: form.name.trim(),
      proxyUrl: form.proxyUrl.trim(),
      noProxy: form.noProxy.trim(),
      isActive: form.isActive === true,
      strictProxy: form.strictProxy === true,
    }
    if (!payload.name || !payload.proxyUrl) return
    setSaving(true)
    try {
      const res = await fetch(editing ? `/api/proxy-pools/${editing.id}` : '/api/proxy-pools', {
        method: editing ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      })
      if (res.ok) {
        await fetchPools()
        setShowForm(false)
        notify.success(editing ? 'Proxy pool updated' : 'Proxy pool created')
      } else {
        const data = await res.json().catch(() => ({}))
        notify.error(errorMessage(data, 'Failed to save proxy pool'))
      }
    } catch {
      notify.error('Failed to save proxy pool')
    } finally {
      setSaving(false)
    }
  }

  const remove = (pool: ProxyPool) => {
    setConfirm({
      title: 'Delete Proxy Pool',
      message: `Delete proxy pool "${pool.name}"?`,
      run: async () => {
        setConfirm(null)
        try {
          const res = await fetch(`/api/proxy-pools/${pool.id}`, { method: 'DELETE' })
          if (res.ok) {
            setPools((prev) => prev.filter((p) => p.id !== pool.id))
            notify.success('Proxy pool deleted')
            return
          }
          const data = await res.json().catch(() => ({}))
          if (res.status === 409) {
            notify.warning(
              `Cannot delete: ${data.boundConnectionCount || 0} connection(s) are still using this pool.`,
            )
          } else {
            notify.error(errorMessage(data, 'Failed to delete proxy pool'))
          }
        } catch {
          notify.error('Failed to delete proxy pool')
        }
      },
    })
  }

  const test = async (id: string) => {
    setTestingId(id)
    try {
      const res = await fetch(`/api/proxy-pools/${id}/test`, { method: 'POST' })
      const data = await res.json().catch(() => ({}))
      if (!res.ok) {
        notify.error(errorMessage(data, 'Failed to test proxy'))
        return
      }
      await fetchPools()
      notify.success(data.ok ? 'Proxy test passed' : 'Proxy test failed')
    } catch {
      notify.error('Failed to test proxy')
    } finally {
      setTestingId(null)
    }
  }

  const toggleActive = async (pool: ProxyPool) => {
    const next = !pool.isActive
    setPools((prev) => prev.map((p) => (p.id === pool.id ? { ...p, isActive: next } : p)))
    try {
      const res = await fetch(`/api/proxy-pools/${pool.id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ isActive: next }),
      })
      if (!res.ok) throw new Error('failed')
    } catch {
      setPools((prev) => prev.map((p) => (p.id === pool.id ? { ...p, isActive: pool.isActive } : p)))
      notify.error('Failed to update active state')
    }
  }

  const bulkSetActive = async (isActive: boolean) => {
    const targets = selected.length > 0 ? selected : visiblePools.map((p) => p.id)
    if (targets.length === 0) return
    setBulkBusy(true)
    try {
      let ok = 0
      let failed = 0
      for (const id of targets) {
        try {
          const res = await fetch(`/api/proxy-pools/${id}`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ isActive }),
          })
          if (res.ok) ok += 1
          else failed += 1
        } catch {
          failed += 1
        }
      }
      await fetchPools()
      notify.success(`${isActive ? 'Activated' : 'Deactivated'} ${ok}${failed ? `, failed ${failed}` : ''}`)
    } finally {
      setBulkBusy(false)
    }
  }

  const bulkDelete = () => {
    if (selected.length === 0) return
    setConfirm({
      title: 'Delete Proxy Pools',
      message: `Delete ${selected.length} proxy pool(s)?`,
      run: async () => {
        // A pending test can change a pool's status while the dialog is open.
        const targets = getVisibleProxyPoolSelection(visiblePoolsRef.current, selected)
        setConfirm(null)
        setBulkBusy(true)
        try {
          let ok = 0
          let blocked = 0
          let failed = 0
          for (const id of targets) {
            try {
              const res = await fetch(`/api/proxy-pools/${id}`, { method: 'DELETE' })
              if (res.ok) ok += 1
              else if (res.status === 409) blocked += 1
              else failed += 1
            } catch {
              failed += 1
            }
          }
          await fetchPools()
          setSelectedIds([])
          notify.success(
            `Deleted ${ok}${blocked ? `, ${blocked} bound` : ''}${failed ? `, ${failed} failed` : ''}`,
          )
        } finally {
          setBulkBusy(false)
        }
      },
    })
  }

  const healthCheck = async () => {
    const targets =
      selected.length > 0 ? visiblePools.filter((p) => selected.includes(p.id)) : visiblePools
    if (targets.length === 0) return
    setHealthChecking(true)
    setHealthProgress({ current: 0, total: targets.length })
    let alive = 0
    const dead: string[] = []
    let done = 0
    const queue = [...targets]

    const worker = async () => {
      for (;;) {
        const pool = queue.shift()
        if (!pool) break
        try {
          const res = await fetch(`/api/proxy-pools/${pool.id}/test`, { method: 'POST' })
          const data = await res.json().catch(() => ({}))
          if (res.ok && data.ok) alive += 1
          else dead.push(pool.id)
        } catch {
          dead.push(pool.id)
        } finally {
          done += 1
          setHealthProgress({ current: done, total: targets.length })
        }
      }
    }

    await Promise.all(Array.from({ length: Math.min(10, targets.length) }, worker))
    await fetchPools()
    setHealthChecking(false)
    setHealthProgress({ current: 0, total: 0 })

    if (dead.length > 0) {
      setConfirm({
        title: 'Disable Dead Proxies',
        message: `Alive: ${alive}, Dead: ${dead.length}.\n\nDisable ${dead.length} dead proxies?`,
        run: async () => {
          setConfirm(null)
          setBulkBusy(true)
          try {
            for (const id of dead) {
              await fetch(`/api/proxy-pools/${id}`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ isActive: false }),
              }).catch(() => {})
            }
            await fetchPools()
            notify.success(`Disabled ${dead.length} dead proxies`)
          } finally {
            setBulkBusy(false)
          }
        },
      })
    } else {
      notify.success(`Health check done. Alive: ${alive}, Dead: ${dead.length}`)
    }
  }

  const batchImport = async () => {
    const lines = importText
      .split(/\r?\n/)
      .map((l) => l.trim())
      .filter(Boolean)
    if (lines.length === 0) {
      notify.warning('Please paste at least one proxy line.')
      return
    }

    const parsed: { proxyUrl: string; name: string }[] = []
    const invalid: string[] = []
    lines.forEach((line, i) => {
      try {
        const entry = parseProxyLine(line)
        if (entry) parsed.push(entry)
      } catch (err) {
        invalid.push(`Line ${i + 1}: ${(err as Error).message}`)
      }
    })
    if (invalid.length > 0) {
      notify.error(`Invalid proxy format:\n${invalid.join('\n')}`)
      return
    }

    setImporting(true)
    try {
      const existing = new Set(pools.map((p) => `${(p.proxyUrl ?? '').trim()}|||${(p.noProxy ?? '').trim()}`))
      let created = 0
      let skipped = 0
      let failed = 0
      for (const entry of parsed) {
        const key = `${entry.proxyUrl}|||`
        if (existing.has(key)) {
          skipped += 1
          continue
        }
        const res = await fetch('/api/proxy-pools', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: entry.name, proxyUrl: entry.proxyUrl, noProxy: '', isActive: true }),
        })
        if (res.ok) {
          created += 1
          existing.add(key)
        } else {
          failed += 1
        }
      }
      await fetchPools()
      setShowImport(false)
      notify.success(`Batch import completed: Created ${created}, Skipped ${skipped}, Failed ${failed}`)
    } catch {
      notify.error('Batch import failed')
    } finally {
      setImporting(false)
    }
  }

  const deployRelay = async (
    path: string,
    body: Record<string, unknown>,
    close: () => void,
  ) => {
    setDeploying(true)
    try {
      const res = await fetch(path, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      const data = await res.json().catch(() => ({}))
      if (res.ok) {
        await fetchPools()
        close()
        notify.success(`Deployed: ${data.deployUrl}`)
      } else {
        notify.error(errorMessage(data, 'Deploy failed'))
      }
    } catch {
      notify.error('Deploy failed')
    } finally {
      setDeploying(false)
    }
  }

  if (loading) {
    return (
      <div className="flex flex-col gap-4">
        {[0, 1].map((i) => (
          <div key={i} className="h-28 animate-pulse rounded-xl bg-surface-2" />
        ))}
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <h1 className="text-xl font-semibold text-text-main sm:text-2xl">Proxy Pools</h1>
        <div className="grid grid-cols-1 gap-2 sm:flex sm:items-center">
          <div className="relative" ref={relayMenuRef}>
            <Button size="sm" variant="secondary" onClick={() => setRelayMenu((v) => !v)}>
              <Icon name="rocket_launch" className="text-[16px]" />
              Deploy Relay
              <Icon name={relayMenu ? 'expand_less' : 'expand_more'} className="text-[18px]" />
            </Button>
            {relayMenu && (
              <div className="absolute left-0 top-full z-50 mt-1 w-48 rounded-xl border border-border bg-surface p-1 shadow-xl sm:left-auto sm:right-0">
                {[
                  { id: 'cloudflare', label: 'Cloudflare Relay', icon: 'cloud', tone: 'text-orange-500' },
                  { id: 'vercel', label: 'Vercel Relay', icon: 'cloud_upload', tone: 'text-blue-500' },
                  { id: 'deno', label: 'Deno Relay', icon: 'terminal', tone: 'text-green-500' },
                  { id: 'netlify', label: 'Netlify Relay', icon: 'deployed_code', tone: 'text-teal-500' },
                ].map((item) => (
                  <button
                    key={item.id}
                    onClick={() => {
                      setRelay(item.id as 'vercel')
                      setRelayMenu(false)
                    }}
                    className="flex w-full cursor-pointer items-center gap-2 rounded-lg px-3 py-2 text-sm text-text-main transition-colors hover:bg-surface-2"
                  >
                    <Icon name={item.icon} className={cn('text-[20px]', item.tone)} />
                    {item.label}
                  </button>
                ))}
              </div>
            )}
          </div>
          <Button
            size="sm"
            variant="secondary"
            onClick={() => {
              setImportText('')
              setShowImport(true)
            }}
          >
            <Icon name="upload" className="text-[16px]" />
            Batch Import
          </Button>
          <Button size="sm" onClick={openCreate}>
            <Icon name="add" className="text-[16px]" />
            Add Proxy Pool
          </Button>
        </div>
      </div>

      <Card className="p-4">
        <div className="mb-4 flex flex-wrap items-center gap-2">
          <label className="flex items-center gap-2 text-xs text-text-muted">
            Status
            <select
              value={statusFilter}
              disabled={bulkBusy || healthChecking}
              onChange={(e) => {
                setStatusFilter(e.target.value)
                setSelectedIds([])
              }}
              className="rounded-lg border border-border bg-surface px-2 py-1.5 text-sm text-text-main"
            >
              <option value="all">All</option>
              <option value="enabled">Enabled</option>
              <option value="disabled">Disabled</option>
            </select>
          </label>
          {visiblePools.length > 0 && (
            <label className="flex cursor-pointer items-center gap-1.5 text-xs text-text-muted">
              <input
                type="checkbox"
                checked={allSelected}
                disabled={bulkBusy || healthChecking}
                onChange={() => {
                  setSelectedIds(allSelected ? [] : visiblePools.map((p) => p.id))
                  anchorRef.current = null
                }}
                className="size-4 rounded border-border"
              />
              {allSelected ? 'Unselect visible' : 'Select all visible'}
            </label>
          )}
          <Badge variant="secondary">Total: {pools.length}</Badge>
          <Badge variant="success">Active: {activeCount}</Badge>
          <span className="text-xs text-text-muted">
            Showing {visiblePools.length} · Shift+click to select a range
          </span>
        </div>

        {(selected.length > 0 || healthChecking) && (
          <div className="mb-4 flex flex-wrap items-center gap-2 rounded-lg border border-primary/30 bg-primary/5 px-3 py-2">
            <Icon name="checklist" className="text-[18px] text-primary" />
            <span className="text-xs font-medium text-primary">
              {selected.length > 0 ? `${selected.length} selected` : 'Visible pools'}
            </span>
            <div className="ml-auto flex flex-wrap items-center gap-2">
              <Button
                size="sm"
                onClick={healthCheck}
                disabled={healthChecking || bulkBusy || visiblePools.length === 0}
              >
                <Icon
                  name={healthChecking ? 'progress_activity' : 'health_and_safety'}
                  className={cn('text-[16px]', healthChecking && 'animate-spin')}
                />
                {healthChecking
                  ? `Checking ${healthProgress.current}/${healthProgress.total}`
                  : 'Health Check'}
              </Button>
              {selected.length > 0 && (
                <>
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => bulkSetActive(true)}
                    disabled={bulkBusy || healthChecking}
                  >
                    Activate
                  </Button>
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => bulkSetActive(false)}
                    disabled={bulkBusy || healthChecking}
                  >
                    Deactivate
                  </Button>
                  <Button size="sm" variant="secondary" onClick={bulkDelete} disabled={bulkBusy || healthChecking}>
                    Delete
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => setSelectedIds([])}
                    disabled={bulkBusy || healthChecking}
                  >
                    Clear
                  </Button>
                </>
              )}
            </div>
          </div>
        )}

        {pools.length === 0 ? (
          <div className="py-10 text-center">
            <p className="mb-1 font-medium text-text-main">No proxy pool entries yet</p>
            <p className="mb-4 text-sm text-text-muted">
              Create a proxy pool entry, then assign it to connections.
            </p>
            <Button onClick={openCreate}>
              <Icon name="add" className="text-[16px]" />
              Add Proxy Pool
            </Button>
          </div>
        ) : visiblePools.length === 0 ? (
          <p className="py-10 text-center text-sm text-text-muted">
            No proxy pools match this status.
          </p>
        ) : (
          <div className="flex flex-col divide-y divide-border/50">
            {visiblePools.map((pool) => (
              <div
                key={pool.id}
                className="flex flex-col gap-3 py-3 sm:flex-row sm:items-center sm:justify-between"
              >
                <div className="flex min-w-0 flex-1 items-start gap-3">
                  <input
                    type="checkbox"
                    checked={selected.includes(pool.id)}
                    onChange={(e) =>
                      setSelectedIds(
                        updateProxyPoolSelection(visiblePools, selectedIds, {
                          id: pool.id,
                          anchorId: anchorRef.current,
                          checked: e.target.checked,
                          shiftKey: (e.nativeEvent as MouseEvent).shiftKey === true,
                        }),
                      )
                    }
                    onFocus={() => {
                      anchorRef.current = pool.id
                    }}
                    disabled={bulkBusy || healthChecking}
                    aria-label={`Select ${pool.name}`}
                    className="mt-1 size-4 shrink-0 rounded border-border"
                  />
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <p className="max-w-full truncate text-sm font-medium text-text-main sm:max-w-[18rem]">
                        {pool.name}
                      </p>
                      <Badge variant={statusVariant(pool.testStatus)}>{pool.testStatus || 'unknown'}</Badge>
                      <Badge variant={pool.isActive ? 'success' : 'secondary'}>
                        {pool.isActive ? 'active' : 'inactive'}
                      </Badge>
                      {pool.type && pool.type !== 'http' && (
                        <Badge variant="secondary">{pool.type} relay</Badge>
                      )}
                      <Badge variant="secondary">{pool.boundConnectionCount || 0} bound</Badge>
                    </div>
                    <p className="mt-1 truncate text-xs text-text-muted">{pool.proxyUrl}</p>
                    {pool.noProxy ? (
                      <p className="truncate text-xs text-text-muted">No proxy: {pool.noProxy}</p>
                    ) : null}
                    <p className="mt-1 text-[11px] text-text-muted">
                      Last tested: {formatDateTime(pool.lastTestedAt)}
                      {pool.lastError ? ` · ${pool.lastError}` : ''}
                    </p>
                  </div>
                </div>

                <div className="flex items-center justify-end gap-1">
                  <Switch
                    checked={pool.isActive === true}
                    onCheckedChange={() => toggleActive(pool)}
                    title={pool.isActive ? 'Disable' : 'Enable'}
                  />
                  <button
                    onClick={() => test(pool.id)}
                    disabled={testingId === pool.id}
                    title="Test proxy"
                    className="cursor-pointer rounded p-2 text-text-muted hover:bg-surface-2 hover:text-primary"
                  >
                    <Icon
                      name={testingId === pool.id ? 'progress_activity' : 'science'}
                      className={cn('text-[18px]', testingId === pool.id && 'animate-spin')}
                    />
                  </button>
                  <button
                    onClick={() => openEdit(pool)}
                    title="Edit"
                    className="cursor-pointer rounded p-2 text-text-muted hover:bg-surface-2 hover:text-primary"
                  >
                    <Icon name="edit" className="text-[18px]" />
                  </button>
                  <button
                    onClick={() => remove(pool)}
                    title="Delete"
                    className="cursor-pointer rounded p-2 text-danger hover:bg-danger/10"
                  >
                    <Icon name="delete" className="text-[18px]" />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      {/* Add / edit */}
      <Modal
        isOpen={showForm}
        onClose={() => setShowForm(false)}
        title={editing ? 'Edit Proxy Pool' : 'Add Proxy Pool'}
      >
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-text-muted">Name</label>
            <Input
              value={form.name}
              placeholder="Office Proxy"
              onChange={(e) => setForm((p) => ({ ...p, name: e.target.value }))}
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-text-muted">Proxy URL</label>
            <Input
              value={form.proxyUrl}
              placeholder="http://127.0.0.1:7897"
              onChange={(e) => setForm((p) => ({ ...p, proxyUrl: e.target.value }))}
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-text-muted">No Proxy</label>
            <Input
              value={form.noProxy}
              placeholder="localhost,127.0.0.1,.internal"
              onChange={(e) => setForm((p) => ({ ...p, noProxy: e.target.value }))}
            />
            <p className="text-xs text-text-muted">Comma-separated hosts/domains to bypass proxy</p>
          </div>

          {(
            [
              {
                key: 'isActive' as const,
                label: 'Active',
                hint: 'Inactive pools are ignored by runtime resolution.',
              },
              {
                key: 'strictProxy' as const,
                label: 'Strict Proxy',
                hint: 'Fail request if proxy is unreachable instead of falling back to direct.',
              },
            ]
          ).map((row) => (
            <div
              key={row.key}
              className="flex flex-col gap-3 rounded-lg border border-border/50 p-3 sm:flex-row sm:items-center sm:justify-between"
            >
              <div>
                <p className="text-sm font-medium text-text-main">{row.label}</p>
                <p className="text-xs text-text-muted">{row.hint}</p>
              </div>
              <Switch
                checked={form[row.key] === true}
                disabled={saving}
                onCheckedChange={() => setForm((p) => ({ ...p, [row.key]: !p[row.key] }))}
              />
            </div>
          ))}

          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <Button
              onClick={save}
              disabled={!form.name.trim() || !form.proxyUrl.trim() || saving}
            >
              {saving ? 'Saving…' : 'Save'}
            </Button>
            <Button variant="ghost" onClick={() => setShowForm(false)} disabled={saving}>
              Cancel
            </Button>
          </div>
        </div>
      </Modal>

      {/* Batch import */}
      <Modal isOpen={showImport} onClose={() => !importing && setShowImport(false)} title="Batch Import Proxies">
        <div className="flex flex-col gap-4">
          <div>
            <label className="mb-1 block text-sm font-medium text-text-main">
              Paste Proxy List (One per line)
            </label>
            <textarea
              value={importText}
              onChange={(e) => setImportText(e.target.value)}
              placeholder={'http://user:pass@127.0.0.1:7897\n127.0.0.1:7897:user:pass'}
              className="min-h-[180px] w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-text-main transition-all focus:border-primary/50 focus:outline-none focus:ring-1 focus:ring-primary/30"
            />
            <p className="mt-1 text-xs text-text-muted">
              Supported formats: protocol://user:pass@host:port, host:port:user:pass
            </p>
          </div>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <Button onClick={batchImport} disabled={!importText.trim() || importing}>
              {importing ? 'Importing…' : 'Import'}
            </Button>
            <Button variant="ghost" onClick={() => setShowImport(false)} disabled={importing}>
              Cancel
            </Button>
          </div>
        </div>
      </Modal>

      {/* Vercel relay */}
      <Modal isOpen={relay === 'vercel'} onClose={() => !deploying && setRelay(null)} title="Deploy Vercel Relay">
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5 rounded-lg border border-blue-500/10 bg-blue-500/5 p-3">
            <p className="text-sm font-medium text-text-main">What is Vercel Relay?</p>
            <p className="text-xs text-text-muted">
              Deploys an edge relay function to Vercel. All AI provider requests will be forwarded
              through Vercel&apos;s edge network, masking your real IP from providers.
            </p>
            <ul className="list-disc space-y-0.5 pl-4 text-xs text-text-muted">
              <li>Your IP is replaced by Vercel&apos;s dynamic edge IPs (hundreds of IPs across 20+ global regions)</li>
              <li>Vercel serves millions of apps — providers can&apos;t block Vercel IPs without affecting legitimate traffic</li>
              <li>Free tier: 100GB bandwidth/month, 500K edge invocations</li>
              <li>Deploy multiple relays on different accounts for more IP diversity</li>
            </ul>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-text-muted">Vercel API Token</label>
            <Input
              type="password"
              value={vercelForm.vercelToken}
              placeholder="your-vercel-api-token"
              onChange={(e) => setVercelForm((p) => ({ ...p, vercelToken: e.target.value }))}
            />
            <p className="text-xs text-text-muted">
              Token is used once for deployment and not stored.{' '}
              <a
                href="https://vercel.com/account/tokens"
                target="_blank"
                rel="noopener noreferrer"
                className="text-primary hover:underline"
              >
                Get token →
              </a>
            </p>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-text-muted">Project Name</label>
            <Input
              value={vercelForm.projectName}
              placeholder="my-relay"
              onChange={(e) => setVercelForm((p) => ({ ...p, projectName: e.target.value }))}
            />
            <p className="text-xs text-text-muted">
              Unique name for your Vercel project. Leave empty for auto-generated name.
            </p>
          </div>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <Button
              onClick={() => deployRelay('/api/proxy-pools/vercel-deploy', vercelForm, () => setRelay(null))}
              disabled={!vercelForm.vercelToken.trim() || deploying}
            >
              {deploying ? 'Deploying… (may take ~1 min)' : 'Deploy'}
            </Button>
            <Button variant="ghost" onClick={() => setRelay(null)} disabled={deploying}>
              Cancel
            </Button>
          </div>
        </div>
      </Modal>

      {/* Cloudflare relay */}
      <Modal
        isOpen={relay === 'cloudflare'}
        onClose={() => !deploying && setRelay(null)}
        title="Deploy Cloudflare Relay"
      >
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5 rounded-lg border border-orange-500/10 bg-orange-500/5 p-3">
            <p className="text-sm font-medium text-text-main">What is Cloudflare Relay?</p>
            <p className="text-xs text-text-muted">
              Deploys a Cloudflare Worker as a proxy relay. All AI provider requests will be
              forwarded through Cloudflare&apos;s global edge network.
            </p>
            <ul className="list-disc space-y-0.5 pl-4 text-xs text-text-muted">
              <li>High performance global routing and IP masking via Cloudflare Workers</li>
              <li>Free tier: 100,000 requests per day</li>
              <li>Requires Cloudflare Account ID and a Workers API Token (Edit Workers permission)</li>
            </ul>
            <div className="mt-2 border-t border-orange-500/10 pt-2 text-xs text-text-muted">
              <p className="mb-1 font-medium text-text-main">How to generate your API Token:</p>
              <ol className="list-decimal space-y-0.5 pl-4">
                <li>Go to <b>My Profile</b> → <b>API Tokens</b> → <b>Create Token</b></li>
                <li>Scroll down to <b>Custom Token</b> and click <b>Get started</b></li>
                <li>Under <b>Permissions</b>: Account | Workers Scripts | Edit</li>
                <li>Under <b>Account Resources</b>: Include | Account | <i>Your Account Name</i></li>
                <li>Click <b>Continue to summary</b> → <b>Create Token</b></li>
              </ol>
            </div>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-text-muted">Account ID</label>
            <Input
              value={cloudflareForm.accountId}
              placeholder="your-cloudflare-account-id"
              onChange={(e) => setCloudflareForm((p) => ({ ...p, accountId: e.target.value }))}
            />
            <p className="text-xs text-text-muted">
              Found on the right side of the Cloudflare dashboard overview page.
            </p>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-text-muted">API Token</label>
            <Input
              type="password"
              value={cloudflareForm.apiToken}
              placeholder="your-cloudflare-api-token"
              onChange={(e) => setCloudflareForm((p) => ({ ...p, apiToken: e.target.value }))}
            />
            <p className="text-xs text-text-muted">
              Requires &quot;Workers Scripts: Edit&quot; permission.{' '}
              <a
                href="https://dash.cloudflare.com/profile/api-tokens"
                target="_blank"
                rel="noopener noreferrer"
                className="text-primary hover:underline"
              >
                Get token →
              </a>
            </p>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-text-muted">Worker Name</label>
            <Input
              value={cloudflareForm.projectName}
              placeholder="my-relay"
              onChange={(e) => setCloudflareForm((p) => ({ ...p, projectName: e.target.value }))}
            />
            <p className="text-xs text-text-muted">
              Unique name for your Cloudflare Worker. Leave empty for auto-generated name.
            </p>
          </div>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <Button
              onClick={() =>
                deployRelay('/api/proxy-pools/cloudflare-deploy', cloudflareForm, () => setRelay(null))
              }
              disabled={
                !cloudflareForm.accountId.trim() || !cloudflareForm.apiToken.trim() || deploying
              }
            >
              {deploying ? 'Deploying…' : 'Deploy Worker'}
            </Button>
            <Button variant="ghost" onClick={() => setRelay(null)} disabled={deploying}>
              Cancel
            </Button>
          </div>
        </div>
      </Modal>

      {/* Deno relay */}
      <Modal isOpen={relay === 'deno'} onClose={() => !deploying && setRelay(null)} title="Deploy Deno Relay">
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5 rounded-lg border border-border bg-surface-2 p-3">
            <p className="text-sm font-medium text-text-main">What is Deno Relay?</p>
            <p className="text-xs text-text-muted">
              Deploys a relay worker to Deno Deploy&apos;s global edge network. All AI provider
              requests are forwarded through Deno&apos;s edge, masking your real IP.
            </p>
            <ul className="list-disc space-y-0.5 pl-4 text-xs text-text-muted">
              <li>Deno Deploy v2 runs on a high-performance global edge network</li>
              <li>Free tier: 1M requests &amp; 100GiB outbound traffic per month</li>
              <li>No per-request CPU time limits (unlike Vercel/Cloudflare)</li>
              <li>Support up to 20 active apps &amp; 50 custom domains</li>
              <li>Deploy multiple relays for maximum IP diversity</li>
            </ul>
            <div className="mt-2 border-t border-border pt-2 text-xs text-text-muted">
              <p className="mb-1 font-medium text-text-main">How to generate API token:</p>
              <ol className="list-decimal space-y-0.5 pl-4">
                <li>Go to <b>console.deno.com</b></li>
                <li>Select your <b>Organization</b> → <b>Settings</b> → <b>Organization Tokens</b></li>
                <li>Create an <b>Organization Token</b> (prefix <b>ddo_</b>)</li>
              </ol>
            </div>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-text-muted">Deno Deploy API Token</label>
            <Input
              type="password"
              value={denoForm.denoToken}
              placeholder="ddo_xxxxxxxxxxxxxxxx"
              onChange={(e) => setDenoForm((p) => ({ ...p, denoToken: e.target.value }))}
            />
            <p className="text-xs text-text-muted">
              Token is used once for deployment, not stored. Found in Organization Settings.
            </p>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-text-muted">Organization Domain</label>
            <Input
              value={denoForm.orgDomain}
              placeholder="your-org.deno.net"
              onChange={(e) => setDenoForm((p) => ({ ...p, orgDomain: e.target.value }))}
            />
            <p className="text-xs text-text-muted">
              Organization&apos;s default domain. Your relay URL will be in the format:
              https://my-relay.your-org.deno.net
            </p>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-text-muted">App Name</label>
            <Input
              value={denoForm.projectName}
              placeholder="deno-relay"
              onChange={(e) => setDenoForm((p) => ({ ...p, projectName: e.target.value }))}
            />
            <p className="text-xs text-text-muted">Unique app name. Leave empty for auto-generated name.</p>
          </div>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <Button
              onClick={() => deployRelay('/api/proxy-pools/deno-deploy', denoForm, () => setRelay(null))}
              disabled={!denoForm.denoToken.trim() || !denoForm.orgDomain.trim() || deploying}
            >
              {deploying ? 'Deploying…' : 'Deploy Relay'}
            </Button>
            <Button variant="ghost" onClick={() => setRelay(null)} disabled={deploying}>
              Cancel
            </Button>
          </div>
        </div>
      </Modal>

      {/* Netlify relay */}
      <Modal
        isOpen={relay === 'netlify'}
        onClose={() => !deploying && setRelay(null)}
        title="Deploy Netlify Relay"
      >
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5 rounded-lg border border-teal-500/10 bg-teal-500/5 p-3">
            <p className="text-sm font-medium text-text-main">What is Netlify Relay?</p>
            <p className="text-xs text-text-muted">
              Deploys a serverless relay function to Netlify&apos;s global edge network. All AI
              provider requests are forwarded through Netlify&apos;s edge, masking your real IP.
            </p>
            <ul className="list-disc space-y-0.5 pl-4 text-xs text-text-muted">
              <li>Runs on Netlify Functions with a 60-second execution limit and streaming support</li>
              <li>Free tier: 125,000 function invocations and 100GB bandwidth per month</li>
              <li>Relay URL format: https://your-site.netlify.app/.netlify/functions/relay</li>
              <li>Deploy multiple relays on different accounts for more IP diversity</li>
            </ul>
            <div className="mt-2 border-t border-teal-500/10 pt-2 text-xs text-text-muted">
              <p className="mb-1 font-medium text-text-main">How to generate API token:</p>
              <ol className="list-decimal space-y-0.5 pl-4">
                <li>Go to <b>User Settings</b> → <b>Applications</b> → <b>Personal access tokens</b></li>
                <li>Select <b>New access token</b> and generate a token</li>
                <li>Copy the token — you won&apos;t see it again after leaving the page</li>
              </ol>
            </div>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-text-muted">Netlify API Token</label>
            <Input
              type="password"
              value={netlifyForm.netlifyToken}
              placeholder="nfp_xxxxxxxxxxxxxxxx"
              onChange={(e) => setNetlifyForm((p) => ({ ...p, netlifyToken: e.target.value }))}
            />
            <p className="text-xs text-text-muted">
              Token is used once for deployment and not stored.{' '}
              <a
                href="https://app.netlify.com/user/applications#personal-access-tokens"
                target="_blank"
                rel="noopener noreferrer"
                className="text-primary hover:underline"
              >
                Get token →
              </a>
            </p>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-text-muted">Site Name</label>
            <Input
              value={netlifyForm.projectName}
              placeholder="netlify-relay"
              onChange={(e) => setNetlifyForm((p) => ({ ...p, projectName: e.target.value }))}
            />
            <p className="text-xs text-text-muted">
              Unique site name (lowercase letters, numbers, hyphens). Your relay URL will be
              https://site-name.netlify.app/.netlify/functions/relay
            </p>
          </div>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <Button
              onClick={() => deployRelay('/api/proxy-pools/netlify-deploy', netlifyForm, () => setRelay(null))}
              disabled={!netlifyForm.netlifyToken.trim() || deploying}
            >
              {deploying ? 'Deploying… (may take ~1 min)' : 'Deploy'}
            </Button>
            <Button variant="ghost" onClick={() => setRelay(null)} disabled={deploying}>
              Cancel
            </Button>
          </div>
        </div>
      </Modal>

      <ConfirmModal
        isOpen={!!confirm}
        onClose={() => setConfirm(null)}
        onConfirm={() => confirm?.run()}
        title={confirm?.title}
        message={confirm?.message}
        variant="danger"
      />
    </div>
  )
}
