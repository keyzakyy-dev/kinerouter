import { useEffect, useState } from 'react'

/**
 * OAuth relay, ported from src/app/callback/page.js. The popup lands here after
 * the provider redirect; it hands the code back to the opener three ways
 * (postMessage, BroadcastChannel, localStorage) and then closes itself.
 *
 * Security: postMessage targets are a fixed allowlist (never "*"), so the code
 * cannot reach a third-party frame that opened us.
 */
const EXPECTED_ORIGINS = [window.location.origin, 'http://localhost:1455']

export default function CallbackPage() {
  const [manual, setManual] = useState(false)
  const [status, setStatus] = useState<'processing' | 'success' | 'error'>('processing')
  const [error, setError] = useState('')

  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const data = {
      code: params.get('code') || params.get('token') || undefined,
      state: params.get('state') || undefined,
      error: params.get('error') || undefined,
      error_description: params.get('error_description') || undefined,
      fullUrl: window.location.href,
    }

    if (data.error) {
      setError(data.error_description || data.error)
      setStatus('error')
      return
    }

    for (const origin of EXPECTED_ORIGINS) {
      try {
        window.opener?.postMessage({ type: 'oauth_callback', data }, origin)
      } catch {
        /* opener gone */
      }
    }
    try {
      new BroadcastChannel('oauth_callback').postMessage(data)
    } catch {
      /* unsupported */
    }
    try {
      localStorage.setItem('oauth_callback', JSON.stringify({ ...data, timestamp: Date.now() }))
    } catch {
      /* storage blocked */
    }

    setStatus('success')
    // No opener (the flow was started from another tab): show the URL instead
    // of closing a window the user is looking at.
    if (!window.opener) {
      setManual(true)
      return
    }
    const t = window.setTimeout(() => window.close(), 1500)
    return () => window.clearTimeout(t)
  }, [])

  return (
    <div className="flex min-h-screen items-center justify-center bg-bg p-6">
      <div className="w-full max-w-md rounded-xl border border-border bg-card p-6 text-center">
        {status === 'processing' && <p className="text-sm text-text-muted">Authorizing…</p>}
        {status === 'error' && (
          <>
            <p className="text-sm font-medium text-danger">Authorization failed</p>
            <p className="mt-1 break-words text-xs text-text-muted">{error}</p>
          </>
        )}
        {status === 'success' && (
          <>
            <p className="text-sm font-medium text-success">Authorization complete</p>
            <p className="mt-1 text-xs text-text-muted">
              {manual ? 'Copy this URL back into the dashboard.' : 'You can close this window.'}
            </p>
            {manual && (
              <code className="mt-3 block break-all rounded-md border border-border bg-surface-2 p-2 text-left text-[11px]">
                {window.location.href}
              </code>
            )}
          </>
        )}
      </div>
    </div>
  )
}
