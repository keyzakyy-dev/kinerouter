import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type ModelAvailability } from '@/lib/api'
import { Icon } from '@/components/icon'
import { Button } from '@/components/ui/button'
import { useNotify } from '@/lib/toast'

/**
 * Ported from src/app/(dashboard)/dashboard/providers/components/ModelAvailabilityBadge.js.
 * Polls the cooldown/lock view every 30s and lets the user clear one.
 *
 * The reference hides the trigger button and renders only on hover-expand, so
 * here the badge is the trigger: same popover body, no dead UI when healthy.
 */
const STATUS = {
  available: { icon: 'check_circle', color: '#22c55e' },
  cooldown: { icon: 'schedule', color: '#f59e0b' },
  unavailable: { icon: 'error', color: '#ef4444' },
  unknown: { icon: 'help', color: '#6b7280' },
} as const

export function ModelAvailabilityBadge() {
  const [data, setData] = useState<ModelAvailability | null>(null)
  const [loading, setLoading] = useState(true)
  const [expanded, setExpanded] = useState(false)
  const [clearing, setClearing] = useState<string | null>(null)
  const ref = useRef<HTMLDivElement>(null)
  const notify = useNotify()

  const load = useCallback(async () => {
    try {
      setData(await api.modelsAvailability())
    } catch {
      /* silent — retried on the next poll */
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
    const t = setInterval(() => void load(), 30000)
    return () => clearInterval(t)
  }, [load])

  useEffect(() => {
    if (!expanded) return
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setExpanded(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [expanded])

  const clear = async (provider: string, model: string) => {
    setClearing(`${provider}:${model}`)
    try {
      await api.clearCooldown({ provider, model })
      notify.success(`Cooldown cleared for ${model}`)
      await load()
    } catch {
      notify.error('Failed to clear cooldown')
    } finally {
      setClearing(null)
    }
  }

  if (loading || !data) return null

  const models = data.models ?? []
  const unhealthy = models.filter((m) => m.status !== 'available')
  const healthy = unhealthy.length === 0

  return (
    <div className="relative" ref={ref}>
      <button
        onClick={() => setExpanded((v) => !v)}
        aria-label="Model availability"
        className={`inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs font-medium transition-colors ${
          healthy
            ? 'border-success/20 bg-success/10 text-success hover:bg-success/15'
            : 'border-warning/20 bg-warning/10 text-warning hover:bg-warning/15'
        }`}
      >
        <Icon name={healthy ? 'verified' : 'warning'} className="text-[14px]" />
        {healthy ? 'All models operational' : `${unhealthy.length} model${unhealthy.length === 1 ? '' : 's'} with issues`}
      </button>

      {expanded && (
        <div className="absolute right-0 top-full z-50 mt-2 w-80 overflow-hidden rounded-xl border border-border bg-surface shadow-2xl">
          <div className="flex items-center justify-between border-b border-border bg-bg px-4 py-3">
            <div className="flex items-center gap-2">
              <span
                aria-hidden
                className="material-symbols-outlined text-[16px]"
                style={{ color: healthy ? '#22c55e' : '#f59e0b' }}
              >
                {healthy ? 'verified' : 'warning'}
              </span>
              <span className="text-sm font-semibold text-text-main">Model Status</span>
            </div>
            <button
              onClick={() => void load()}
              title="Refresh"
              aria-label="Refresh"
              className="rounded-lg p-1 text-text-muted transition-colors hover:bg-surface hover:text-text-main"
            >
              <Icon name="refresh" className="text-[14px]" />
            </button>
          </div>
          <div className="max-h-60 overflow-y-auto px-4 py-3">
            {healthy ? (
              <p className="py-2 text-center text-sm text-text-muted">All models are responding normally.</p>
            ) : (
              <div className="flex flex-col gap-2.5">
                {Object.entries(groupBy(unhealthy)).map(([provider, list]) => (
                  <div key={provider}>
                    <p className="mb-1.5 text-xs font-semibold capitalize text-text-main">{provider}</p>
                    <div className="flex flex-col gap-1">
                      {list.map((m) => {
                        const s = STATUS[m.status] ?? STATUS.unknown
                        const busy = clearing === `${m.provider}:${m.model}`
                        return (
                          <div
                            key={`${m.connectionId}-${m.model}`}
                            className="flex items-center justify-between rounded-lg bg-surface-2 px-2.5 py-1.5"
                          >
                            <div className="flex min-w-0 items-center gap-1.5">
                              <span
                                aria-hidden
                                className="material-symbols-outlined shrink-0 text-[14px]"
                                style={{ color: s.color }}
                              >
                                {s.icon}
                              </span>
                              <span className="truncate font-mono text-xs text-text-main">{m.model}</span>
                            </div>
                            {m.status === 'cooldown' && (
                              <Button
                                size="sm"
                                variant="ghost"
                                onClick={() => void clear(m.provider, m.model)}
                                disabled={busy}
                                className="ml-2 px-1.5 py-0.5 text-[10px]"
                              >
                                {busy ? '...' : 'Clear'}
                              </Button>
                            )}
                          </div>
                        )
                      })}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function groupBy(list: ModelAvailability['models']) {
  const out: Record<string, ModelAvailability['models']> = {}
  for (const m of list) {
    const k = m.provider || 'unknown'
    ;(out[k] ??= []).push(m)
  }
  return out
}
