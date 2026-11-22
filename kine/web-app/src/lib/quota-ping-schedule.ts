/**
 * Anchored auto-ping schedule helpers, ported from
 * src/shared/services/quotaPingSchedule.js.
 *
 * With anchor 09:30, a 5h window and 3 slots/day the slots are 09:30 / 14:30 /
 * 19:30 local time; the overnight gap re-aligns the next day onto the anchor
 * because 24h is not a multiple of 5h.
 *
 * ponytail: only the pieces the settings modal renders are ported — the actual
 * ping scheduler lives in the server (quotaAutoPing.js equivalent is not
 * ported yet), so `getDuePingSlot` has no client caller.
 */

export function getLocalTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

export const PING_SCHEDULE_DEFAULTS = {
  anchor: '09:30',
  timezone: getLocalTimezone(),
  windowHours: 5,
  slotsPerDay: 3,
}

export type PingSchedule = {
  enabled: boolean
  anchor: string
  timezone: string
  windowHours: number
  slotsPerDay: number
}

export function parseAnchor(anchor: string | null | undefined): { hour: number; minute: number } | null {
  const match = /^(\d{1,2}):(\d{2})$/.exec(String(anchor ?? '').trim())
  if (!match) return null
  const hour = Number(match[1])
  const minute = Number(match[2])
  if (hour > 23 || minute > 59) return null
  return { hour, minute }
}

export function maxSlotsPerDay(windowHours: number): number {
  return Math.max(1, Math.floor(24 / windowHours))
}

function clampNumber(value: unknown, fallback: number, min: number, max: number): number {
  const parsed = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(parsed)) return fallback
  return Math.min(max, Math.max(min, parsed))
}

function isValidTimezone(timezone: string | null | undefined): boolean {
  if (!timezone || typeof timezone !== 'string') return false
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: timezone })
    return true
  } catch {
    return false
  }
}

/** Fully-defaulted schedule, or null when absent / disabled / invalid. */
export function normalizePingSchedule(raw: any): PingSchedule | null {
  if (!raw || raw.enabled !== true) return null
  const parts = parseAnchor(raw.anchor) || parseAnchor(PING_SCHEDULE_DEFAULTS.anchor)!
  const anchor = `${String(parts.hour).padStart(2, '0')}:${String(parts.minute).padStart(2, '0')}`
  const timezone = isValidTimezone(raw.timezone) ? raw.timezone : PING_SCHEDULE_DEFAULTS.timezone
  const windowHours = clampNumber(raw.windowHours, PING_SCHEDULE_DEFAULTS.windowHours, 0.5, 24)
  const slotsPerDay = Math.round(
    clampNumber(raw.slotsPerDay, PING_SCHEDULE_DEFAULTS.slotsPerDay, 1, maxSlotsPerDay(windowHours)),
  )
  return { enabled: true, anchor, timezone, windowHours, slotsPerDay }
}

/** "09:30 → 14:30 → 19:30" preview for the modal. */
export function getScheduleSlotLabels(schedule: any): string[] {
  const normal = normalizePingSchedule(schedule)
  if (!normal) return []
  const { hour, minute } = parseAnchor(normal.anchor)!
  const startMinutes = hour * 60 + minute
  return Array.from({ length: normal.slotsPerDay }, (_, index) => {
    const total = Math.round(startMinutes + index * normal.windowHours * 60) % 1440
    return `${String(Math.floor(total / 60)).padStart(2, '0')}:${String(total % 60).padStart(2, '0')}`
  })
}
