import { useState, type FormEvent } from 'react'
import { Navigate, useNavigate } from 'react-router-dom'
import { api, ApiError } from '@/lib/api'
import { useAuth, FullPageSpinner, peerAllowed } from '@/lib/auth'
import { Button } from '@/components/ui/button'
import { Input, Label } from '@/components/ui/input'
import { Icon } from '@/components/icon'

const MARK = 'size-8 bg-text-main mask-[url(/favicon.svg)] [mask-size:145%] mask-center mask-no-repeat'

const FEATURES = [
  { icon: 'route', title: 'Combo routing', text: 'Try providers in order, fall back when one fails.' },
  { icon: 'monitoring', title: 'Usage and cost', text: 'Tokens, spend and latency per model, per key.' },
  { icon: 'vpn_key', title: 'Many keys, one endpoint', text: 'Rotate accounts without touching client config.' },
]

export default function LoginPage() {
  const { status, loading, refresh } = useAuth()
  const navigate = useNavigate()
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [reveal, setReveal] = useState(false)

  if (loading) return <FullPageSpinner />
  if (status?.authenticated || peerAllowed(status)) return <Navigate to="/dashboard" replace />

  const needsSetup = Boolean(status?.needsSetup)

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
    <div className="grid min-h-screen lg:grid-cols-[1.02fr_1fr]">
      <aside className="relative hidden overflow-hidden p-10 lg:flex lg:flex-col">
        <div
          aria-hidden
          className="pointer-events-none absolute inset-0"
          style={{
            backgroundImage:
              'radial-gradient(115% 85% at 0% 0%, color-mix(in oklab, var(--color-brand-500) 22%, transparent), transparent 62%), radial-gradient(90% 70% at 100% 100%, color-mix(in oklab, var(--color-brand-500) 8%, transparent), transparent 70%)',
            maskImage: 'linear-gradient(to right, #000 50%, transparent 100%)',
          }}
        />
        <div className="relative flex items-center gap-2.5">
          <span aria-hidden className={MARK} />
          <span className="text-[15px] font-semibold tracking-tight">KineRouter</span>
        </div>

        <div className="relative my-auto max-w-md">
          <h2 className="text-3xl font-semibold leading-[1.15] tracking-tight text-balance">
            One endpoint for every provider you already pay for.
          </h2>
          <ul className="mt-8 space-y-4">
            {FEATURES.map((f) => (
              <li key={f.icon} className="flex gap-3">
                <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md bg-brand-500/10 text-brand-500">
                  <Icon name={f.icon} className="text-[15px]" />
                </span>
                <div>
                  <p className="text-[13px] font-medium text-text-main">{f.title}</p>
                  <p className="mt-0.5 text-xs leading-relaxed text-text-muted">{f.text}</p>
                </div>
              </li>
            ))}
          </ul>
        </div>
      </aside>

      <main className="flex flex-col justify-center px-5 py-10 sm:px-10">
        <div className="mx-auto w-full max-w-sm">
          <div className="mb-7 flex items-center gap-2.5 lg:hidden">
            <span aria-hidden className={MARK} />
            <span className="text-[15px] font-semibold tracking-tight">KineRouter</span>
          </div>

          <h1 className="text-xl font-semibold tracking-tight">Sign in</h1>
          <p className="mt-1.5 text-xs leading-relaxed text-text-muted">
            Enter the dashboard password for this instance.
          </p>

          <form onSubmit={submit} className="mt-6">
            <Label htmlFor="password">Dashboard password</Label>
            <div className="relative mt-1.5">
              <Input
                id="password"
                type={reveal ? 'text' : 'password'}
                autoFocus
                autoComplete="current-password"
                className="pr-11"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
              />
              <button
                type="button"
                onClick={() => setReveal((v) => !v)}
                aria-label={reveal ? 'Hide password' : 'Show password'}
                aria-pressed={reveal}
                className="absolute inset-y-0 right-1 flex w-9 items-center justify-center rounded-md text-text-muted transition-colors hover:text-text-main focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-border"
              >
                <Icon name={reveal ? 'visibility_off' : 'visibility'} className="text-[16px]" />
              </button>
            </div>

            {error && (
              <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>
            )}

            <Button
              type="submit"
              className="mt-4 w-full disabled:bg-surface-2 disabled:text-text-muted disabled:opacity-100"
              disabled={busy || !password}
            >
              {busy ? 'Signing in…' : 'Sign in'}
            </Button>
          </form>

          {needsSetup ? (
            <p className="mt-6 flex flex-wrap items-center gap-x-1.5 gap-y-1 text-[11px] leading-relaxed text-text-muted">
              <Icon name="info" className="text-[13px]" />
              Fresh install: the password is
              <code className="rounded bg-surface-3/70 px-1 py-0.5 font-mono text-text-main">123456</code>
              until you change it in Settings.
            </p>
          ) : null}
        </div>
      </main>
    </div>
  )
}
