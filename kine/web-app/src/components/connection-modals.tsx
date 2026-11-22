import { useEffect, useState } from 'react'
import { Icon } from '@/components/icon'
import { Modal } from '@/components/modal'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { Conn } from '@/lib/quota'
import {
  PING_SCHEDULE_DEFAULTS,
  getLocalTimezone,
  getScheduleSlotLabels,
  maxSlotsPerDay,
  parseAnchor,
} from '@/lib/quota-ping-schedule'
import { cn } from '@/lib/utils'

/**
 * Edit a connection, ported from src/shared/components/EditConnectionModal.js:
 * name, priority, API-key re-key with validation, and a live connection test.
 *
 * The AWS / Azure / Cloudflare / region blocks are driven by the same fields
 * AddApiKeyModal writes, and pickSpecific in connections.go now allows them, so
 * what is typed here round-trips.
 */
export function EditConnectionModal({
  isOpen,
  connection,
  onSave,
  onClose,
}: {
  isOpen: boolean
  connection: Conn | null
  onSave: (updates: Record<string, unknown>) => Promise<void>
  onClose: () => void
}) {
  const [name, setName] = useState('')
  const [priority, setPriority] = useState(1)
  const [apiKey, setApiKey] = useState('')
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<'success' | 'failed' | null>(null)
  const [validating, setValidating] = useState(false)
  const [validationResult, setValidationResult] = useState<'success' | 'failed' | null>(null)
  const [saving, setSaving] = useState(false)
  const [extra, setExtra] = useState<Record<string, string>>({})

  const EXTRAS: Array<{ key: string; label: string; placeholder?: string; secret?: boolean }> = [
    { key: 'region', label: 'Region' },
    { key: 'profile', label: 'AWS Profile' },
    { key: 'accessKeyId', label: 'Access Key ID' },
    { key: 'sessionToken', label: 'Session Token', secret: true },
    { key: 'azureEndpoint', label: 'Azure Endpoint' },
    { key: 'apiVersion', label: 'API Version' },
    { key: 'deployment', label: 'Deployment' },
    { key: 'organization', label: 'Organization' },
    { key: 'accountId', label: 'Account ID' },
    { key: 'workspaceId', label: 'Workspace ID' },
  ]

  useEffect(() => {
    if (!connection) return
    setName(connection.name || '')
    setPriority(connection.priority || 1)
    setApiKey('')
    setTestResult(null)
    setValidationResult(null)
    const psd = (connection.providerSpecificData as Record<string, unknown> | undefined) ?? {}
    const next: Record<string, string> = {}
    for (const { key } of EXTRAS) if (typeof psd[key] === 'string') next[key] = psd[key] as string
    setExtra(next)
    // EXTRAS is a constant table; re-running per keystroke would reset the form.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connection, isOpen])

  if (!connection) return null

  const isOAuth = connection.authType === 'oauth'
  // Only surface fields this connection already uses, so an OpenAI-compatible
  // key does not grow an AWS block.
  const psdNow = (connection.providerSpecificData as Record<string, unknown> | undefined) ?? {}
  const isAWS = connection.provider === 'bedrock' || connection.provider === 'bedrock-xai'
  const extraKeys = EXTRAS.map((f) => f.key).filter(
    (k) => k in psdNow || (isAWS && ['region', 'profile', 'accessKeyId', 'sessionToken'].includes(k)) ||
      (connection.provider === 'cloudflare-ai' && k === 'accountId'),
  )

  const validateKey = async (key: string): Promise<boolean> => {
    const res = await fetch('/api/providers/validate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ provider: connection.provider, apiKey: key }),
    })
    const data = await res.json().catch(() => ({}))
    return !!data.valid
  }

  const handleValidate = async () => {
    if (!apiKey) return
    setValidating(true)
    setValidationResult(null)
    try {
      setValidationResult((await validateKey(apiKey)) ? 'success' : 'failed')
    } catch {
      setValidationResult('failed')
    } finally {
      setValidating(false)
    }
  }

  const handleTest = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      const res = await fetch(`/api/providers/${connection.id}/test`, { method: 'POST' })
      const data = await res.json().catch(() => ({}))
      setTestResult(data.valid ? 'success' : 'failed')
    } catch {
      setTestResult('failed')
    } finally {
      setTesting(false)
    }
  }

  const handleSubmit = async () => {
    setSaving(true)
    try {
      const updates: Record<string, unknown> = { name, priority }
      const specific: Record<string, unknown> = {}
      for (const [k, v] of Object.entries(extra)) if (v.trim()) specific[k] = v.trim()
      if (Object.keys(specific).length) updates.providerSpecificData = specific
      if (!isOAuth && apiKey) {
        updates.apiKey = apiKey
        let valid = validationResult === 'success'
        if (!valid) {
          setValidating(true)
          try {
            valid = await validateKey(apiKey)
            setValidationResult(valid ? 'success' : 'failed')
          } catch {
            setValidationResult('failed')
          } finally {
            setValidating(false)
          }
        }
        if (valid) {
          updates.testStatus = 'active'
          updates.lastError = null
          updates.lastErrorAt = null
        }
      }
      await onSave(updates)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal isOpen={isOpen} title="Edit Connection" onClose={onClose}>
      <div className="flex flex-col gap-4">
        <div className="flex flex-col gap-1">
          <p className="text-xs font-medium text-text-muted">Name</p>
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={isOAuth ? 'Account name' : 'Production Key'}
          />
        </div>

        {isOAuth && connection.email && (
          <div className="rounded-lg bg-sidebar/50 p-3">
            <p className="mb-1 text-sm text-text-muted">Email</p>
            <p className="font-medium">{connection.email}</p>
          </div>
        )}

        <div className="flex flex-col gap-1">
          <p className="text-xs font-medium text-text-muted">Priority</p>
          <Input
            type="number"
            value={priority}
            onChange={(e) => setPriority(Number.parseInt(e.target.value, 10) || 1)}
          />
        </div>

        {!isOAuth && (
          <>
            <div className="flex items-end gap-2">
              <div className="flex flex-1 flex-col gap-1">
                <p className="text-xs font-medium text-text-muted">API Key</p>
                <Input
                  type="password"
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  placeholder="Enter new API key"
                />
                <p className="text-[11px] text-text-muted">Leave blank to keep the current API key.</p>
              </div>
              <Button
                onClick={handleValidate}
                disabled={!apiKey || validating || saving}
                variant="secondary"
              >
                {validating ? 'Checking...' : 'Check'}
              </Button>
            </div>
            {validationResult && (
              <Badge variant={validationResult === 'success' ? 'success' : 'error'}>
                {validationResult === 'success' ? 'Valid' : 'Invalid'}
              </Badge>
            )}
          </>
        )}

        {extraKeys.length > 0 && (
          <div className="grid gap-2 sm:grid-cols-2">
            {EXTRAS.filter((f) => extraKeys.includes(f.key)).map((f) => (
              <div key={f.key} className="flex flex-col gap-1">
                <p className="text-xs font-medium text-text-muted">{f.label}</p>
                <Input
                  type={f.secret ? 'password' : 'text'}
                  value={extra[f.key] ?? ''}
                  placeholder={f.placeholder}
                  onChange={(e) => setExtra({ ...extra, [f.key]: e.target.value })}
                />
              </div>
            ))}
          </div>
        )}

        <div className="flex items-center gap-3">
          <Button onClick={handleTest} variant="secondary" disabled={testing}>
            {testing ? 'Testing...' : 'Test Connection'}
          </Button>
          {testResult && (
            <Badge variant={testResult === 'success' ? 'success' : 'error'}>
              {testResult === 'success' ? 'Valid' : 'Failed'}
            </Badge>
          )}
        </div>

        <div className="flex gap-2">
          <Button onClick={handleSubmit} className="w-full" disabled={saving}>
            {saving ? 'Saving...' : 'Save'}
          </Button>
          <Button onClick={onClose} variant="ghost" className="w-full">
            Cancel
          </Button>
        </div>
      </div>
    </Modal>
  )
}

/**
 * Auto-ping settings, ported from
 * src/app/(dashboard)/dashboard/providers/[id]/AutoPingScheduleModal.js.
 * Mounted only while open, so the form state is always fresh.
 */
export function AutoPingScheduleModal({
  connection,
  reactive = false,
  schedule = null,
  onSave,
  onClose,
}: {
  connection: Conn | null
  reactive?: boolean
  schedule: any
  onSave: (result: { reactive: boolean; schedule: Record<string, unknown> }) => void
  onClose: () => void
}) {
  const [form, setForm] = useState(() => ({
    mode: schedule?.enabled === true ? 'schedule' : reactive ? 'reset' : 'off',
    anchor: schedule?.anchor || PING_SCHEDULE_DEFAULTS.anchor,
    timezone: getLocalTimezone(),
    windowHours: String(schedule?.windowHours ?? PING_SCHEDULE_DEFAULTS.windowHours),
    slotsPerDay: String(schedule?.slotsPerDay ?? PING_SCHEDULE_DEFAULTS.slotsPerDay),
  }))

  const windowHours = Number(form.windowHours)
  const slotsPerDay = Number(form.slotsPerDay)
  const anchorValid = Boolean(parseAnchor(form.anchor))
  const windowValid = Number.isFinite(windowHours) && windowHours >= 0.5 && windowHours <= 24
  const slotsMax = windowValid ? maxSlotsPerDay(windowHours) : 24
  const slotsValid = Number.isInteger(slotsPerDay) && slotsPerDay >= 1 && slotsPerDay <= slotsMax
  const canSaveSchedule = anchorValid && windowValid && slotsValid
  const canSave = form.mode !== 'schedule' || canSaveSchedule

  const previewSlots = canSaveSchedule
    ? getScheduleSlotLabels({ enabled: true, ...form, windowHours, slotsPerDay })
    : []

  const coverageEnd =
    previewSlots.length && windowValid
      ? (() => {
          const { hour, minute } = parseAnchor(form.anchor)!
          const total = Math.round(hour * 60 + minute + slotsPerDay * windowHours * 60) % 1440
          return `${String(Math.floor(total / 60)).padStart(2, '0')}:${String(total % 60).padStart(2, '0')}`
        })()
      : ''

  const update = (patch: Partial<typeof form>) => setForm((prev) => ({ ...prev, ...patch }))

  const handleSave = () => {
    if (!canSave) return
    onSave({
      reactive: form.mode === 'reset',
      schedule:
        form.mode === 'schedule'
          ? {
              enabled: true,
              anchor: form.anchor,
              timezone: form.timezone,
              windowHours,
              slotsPerDay,
            }
          : { enabled: false },
    })
    onClose()
  }

  const MODES = [
    { value: 'off', label: 'Off', icon: 'block' },
    { value: 'reset', label: 'On reset', icon: 'bolt' },
    { value: 'schedule', label: 'Schedule', icon: 'schedule' },
  ]

  return (
    <Modal
      isOpen={!!connection}
      onClose={onClose}
      title="Auto-ping settings"
      size="lg"
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleSave} disabled={!canSave}>
            Save
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-4">
        <p className="text-sm text-text-muted">
          Configure automated quota pings{connection?.name ? ` for ${connection.name}` : ''} — pick
          one mode.
        </p>

        <div className="grid grid-cols-3 gap-1 rounded-[10px] bg-surface-2 p-1">
          {MODES.map((option) => (
            <button
              key={option.value}
              type="button"
              onClick={() => update({ mode: option.value })}
              className={cn(
                'flex h-9 cursor-pointer items-center justify-center gap-1.5 rounded-[8px] px-3 text-sm font-medium transition-all',
                form.mode === option.value
                  ? 'bg-surface text-text-main shadow-sm'
                  : 'text-text-muted hover:text-text-main',
              )}
            >
              <Icon name={option.icon} className="text-[15px] leading-none" />
              <span>{option.label}</span>
            </button>
          ))}
        </div>

        {form.mode === 'reset' && (
          <p className="text-xs text-text-muted">
            Pings right after the 5h quota window resets, so a new window starts immediately.
          </p>
        )}

        {form.mode === 'schedule' && (
          <div className="flex flex-col gap-4">
            <p className="text-xs text-text-muted">
              Pings at fixed local times so the 5h windows align with your workday.
            </p>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
              <Field label="Anchor time" error={anchorValid ? '' : 'Use HH:MM'}>
                <Input
                  type="time"
                  value={form.anchor}
                  onChange={(e) => update({ anchor: e.target.value })}
                />
              </Field>
              <Field label="Window (hours)" error={windowValid ? '' : '0.5 – 24'}>
                <Input
                  type="number"
                  min="0.5"
                  max="24"
                  step="0.5"
                  value={form.windowHours}
                  onChange={(e) => update({ windowHours: e.target.value })}
                />
              </Field>
              <Field
                label="Pings per day"
                error={slotsValid ? '' : `1 – ${slotsMax}`}
                hint={slotsValid ? `Max ${slotsMax}` : ''}
              >
                <Input
                  type="number"
                  min="1"
                  max={String(slotsMax)}
                  step="1"
                  value={form.slotsPerDay}
                  onChange={(e) => update({ slotsPerDay: e.target.value })}
                />
              </Field>
            </div>

            {previewSlots.length > 0 && (
              <div className="rounded-[10px] bg-surface-2 px-3 py-2.5">
                <p className="text-xs font-medium uppercase tracking-wide text-text-muted">
                  Daily slots ({form.timezone})
                </p>
                <p className="mt-1 text-sm font-medium text-text-main">{previewSlots.join('  →  ')}</p>
                <p className="mt-1 text-xs text-text-muted">
                  Covers {previewSlots[0]} – {coverageEnd}, then pauses until {previewSlots[0]} next
                  day.
                </p>
              </div>
            )}
          </div>
        )}
      </div>
    </Modal>
  )
}

function Field({
  label,
  children,
  error,
  hint,
}: {
  label: string
  children: React.ReactNode
  error?: string
  hint?: string
}) {
  return (
    <div className="flex flex-col gap-1">
      <p className="text-xs font-medium text-text-muted">{label}</p>
      {children}
      {error ? (
        <p className="text-[11px] text-danger">{error}</p>
      ) : hint ? (
        <p className="text-[11px] text-text-muted">{hint}</p>
      ) : null}
    </div>
  )
}
