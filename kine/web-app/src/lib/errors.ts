/**
 * Reads the message out of an error response body.
 *
 * KineRouter's API answers `{"error":{"message":…,"type":…}}` (the
 * [OI]-compatible shape) while KineRouter's own routes answer `{"error":"…"}`.
 * Both are accepted so ported pages can call this instead of touching
 * `data.error` directly — passing the object to a toast crashes React.
 */
export function errorMessage(data: unknown, fallback: string): string {
  if (typeof data === 'string' && data) return data
  if (data && typeof data === 'object') {
    const err = (data as { error?: unknown }).error
    if (typeof err === 'string' && err) return err
    if (err && typeof err === 'object') {
      const msg = (err as { message?: unknown }).message
      if (typeof msg === 'string' && msg) return msg
    }
    const msg = (data as { message?: unknown }).message
    if (typeof msg === 'string' && msg) return msg
  }
  return fallback
}
