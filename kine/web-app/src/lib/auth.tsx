import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { api, type AuthStatus } from './api'

type AuthState = {
  status: AuthStatus | null
  loading: boolean
  refresh: () => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthStatus | null>(null)
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(async () => {
    try {
      setStatus(await api.authStatus())
    } catch {
      setStatus({ authenticated: false, requireLogin: true })
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const logout = useCallback(async () => {
    try {
      await api.logout()
    } finally {
      setStatus((s) => (s ? { ...s, authenticated: false } : s))
    }
  }, [])

  const value = useMemo(() => ({ status, loading, refresh, logout }), [status, loading, refresh, logout])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used inside AuthProvider')
  return ctx
}

export function RequireAuth({ children }: { children: ReactNode }) {
  const { status, loading } = useAuth()
  const location = useLocation()
  if (loading) return <FullPageSpinner />
  if (!status?.authenticated && !peerAllowed(status)) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }
  return <>{children}</>
}

// With requireLogin off the backend accepts loopback peers with no session cookie,
// so /api/auth/status reports authenticated:false even though requests succeed.
export function peerAllowed(status: AuthStatus | null): boolean {
  if (!status || status.requireLogin !== false) return false
  return ['localhost', '127.0.0.1', '::1'].includes(window.location.hostname)
}

export function FullPageSpinner() {
  return (
    <div className="flex min-h-screen items-center justify-center">
      <div className="size-5 animate-spin rounded-full border-2 border-muted border-t-primary" />
    </div>
  )
}
