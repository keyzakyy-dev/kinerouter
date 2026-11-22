import { useState, type FormEvent } from 'react'
import { Navigate, useNavigate } from 'react-router-dom'
import { api, ApiError } from '@/lib/api'
import { useAuth, FullPageSpinner, peerAllowed } from '@/lib/auth'
import { Button } from '@/components/ui/button'
import { Input, Label } from '@/components/ui/input'

export default function LoginPage() {
  const { status, loading, refresh } = useAuth()
  const navigate = useNavigate()
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  if (loading) return <FullPageSpinner />
  if (status?.authenticated || peerAllowed(status)) return <Navigate to="/dashboard" replace />

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.login(password)
      await refresh()
      navigate('/dashboard', { replace: true })
    } catch (err) {
      setError(err instanceof ApiError && err.status === 429
        ? 'Too many attempts. Wait a moment and try again.'
        : 'Invalid password.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center px-4">
      <form onSubmit={submit} className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <img src="/favicon.svg" alt="" className="h-9 w-auto" />
          <div>
            <h1 className="text-lg font-semibold tracking-tight">KineRouter</h1>
            <p className="mt-1 text-xs text-muted-foreground">
              {status?.needsSetup ? 'Set a password to secure this instance' : 'Sign in to the dashboard'}
            </p>
          </div>
        </div>

        <div className="rounded-lg border border-border bg-card p-5">
          <Label htmlFor="password">Dashboard password</Label>
          <Input
            id="password"
            type="password"
            autoFocus
            autoComplete="current-password"
            className="mt-1.5"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="••••••••"
          />
          {error && <p className="mt-2 text-xs text-destructive">{error}</p>}
          <Button type="submit" className="mt-4 w-full" disabled={busy || !password}>
            {busy ? 'Signing in…' : 'Sign in'}
          </Button>
          <p className="mt-3 text-center text-[11px] text-muted-foreground">
            Fresh install: sign in locally with <code className="font-mono">123456</code> (or your{' '}
            <code className="font-mono">INITIAL_PASSWORD</code>), then change it in Settings. Remote
            access stays locked until the password is set.
          </p>
        </div>
      </form>
    </div>
  )
}
