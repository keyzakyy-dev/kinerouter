export function fmtInt(n: number | null | undefined): string {
  const v = Number(n ?? 0)
  if (!Number.isFinite(v)) return '0'
  if (Math.abs(v) >= 1e9) return `${(v / 1e9).toFixed(2)}B`
  if (Math.abs(v) >= 1e6) return `${(v / 1e6).toFixed(2)}M`
  if (Math.abs(v) >= 1e4) return `${(v / 1e3).toFixed(1)}K`
  return v.toLocaleString()
}

export function fmtCost(n: number | null | undefined): string {
  const v = Number(n ?? 0)
  if (!Number.isFinite(v) || v === 0) return '$0.00'
  if (v < 0.01) return `$${v.toFixed(4)}`
  return `$${v.toFixed(2)}`
}

export function fmtTime(iso: string | null | undefined): string {
  if (!iso) return '-'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString(undefined, {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

// "2026-11-22" (daily bucket) or "2026-11-22T14" (hourly bucket)
export function fmtBucket(bucket: string, byHour: boolean): string {
  if (!byHour) {
    const [, m, d] = bucket.split('-')
    return `${m}/${d}`
  }
  return `${bucket.slice(11, 13)}:00`
}

export function maskSecret(key: string): string {
  if (!key) return ''
  if (key.length <= 12) return key
  return `${key.slice(0, 8)}••••${key.slice(-4)}`
}

export function moveItem<T>(list: T[], from: number, to: number): T[] {
  if (from === to || from < 0 || to < 0 || from >= list.length || to >= list.length) return list
  const next = list.slice()
  const [item] = next.splice(from, 1)
  next.splice(to, 0, item)
  return next
}

/** KineRouter's getErrorCode: the HTTP status buried in an error message, or "ERR". */
export function getErrorCode(lastError: string | null | undefined): string | null {
  if (!lastError) return null
  const m = lastError.match(/\b([45]\d{2})\b/)
  return m ? m[1] : 'ERR'
}

/** KineRouter's getRelativeTime ("5m ago"). */
export function getRelativeTime(iso: string | null | undefined): string {
  if (!iso) return ''
  const diff = Date.now() - new Date(iso).getTime()
  const mins = Math.floor(diff / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}
