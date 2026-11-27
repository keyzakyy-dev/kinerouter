import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AuthProvider } from '@/lib/auth'
import App from '@/App'

const mockFetch = (handlers: Record<string, unknown>) =>
  vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    const body = handlers[url.split('?')[0]] ?? {}
    return new Response(JSON.stringify(body), { status: 200 })
  })

describe('auth guard', () => {
  beforeEach(() => { vi.restoreAllMocks() })
  afterEach(() => { vi.unstubAllGlobals() })

  it('redirects unauthenticated visitors from /dashboard to /login', async () => {
    vi.stubGlobal('fetch', mockFetch({
      '/api/auth/status': { authenticated: false, requireLogin: true },
      '/api/auth/login': { ok: true },
    }))

    render(
      <MemoryRouter initialEntries={['/dashboard/combos']}>
        <AuthProvider><App /></AuthProvider>
      </MemoryRouter>,
    )

    await waitFor(() => {
      expect(screen.getByLabelText(/dashboard password/i)).toBeTruthy()
    })
  })

  it('renders the dashboard when the session cookie is valid', async () => {
    vi.stubGlobal('fetch', mockFetch({
      '/api/auth/status': { authenticated: true, requireLogin: true },
      '/api/keys': { keys: [], machineId: 'm-1' },
      '/api/version': { version: '0.1.0', latest: '0.1.0', updateAvailable: false },
    }))

    render(
      <MemoryRouter initialEntries={['/dashboard']}>
        <AuthProvider><App /></AuthProvider>
      </MemoryRouter>,
    )

    // The keys fetch settles after the heading renders, so wait on it too.
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Endpoint & Keys' })).toBeTruthy()
      expect(screen.getByText('No keys yet')).toBeTruthy()
    })
    expect(screen.queryByLabelText(/dashboard password/i)).toBeNull()
  })

  it('toggles the rail on Cmd/Ctrl+B, from Animate UI\'s sidebar provider', async () => {
    vi.stubGlobal('fetch', mockFetch({
      '/api/auth/status': { authenticated: true, requireLogin: true },
      '/api/keys': { keys: [], machineId: 'm-1' },
      '/api/version': { version: '0.1.0', latest: '0.1.0' },
    }))

    render(
      <MemoryRouter initialEntries={['/dashboard/endpoint']}>
        <AuthProvider><App /></AuthProvider>
      </MemoryRouter>,
    )

    const rail = await screen.findByRole('navigation', { name: 'Dashboard' })
    const before = rail.getAttribute('data-collapsed')
    fireEvent.keyDown(window, { key: 'b', ctrlKey: true })
    await waitFor(() => expect(rail.getAttribute('data-collapsed')).not.toBe(before))
  })
})

describe('login setup hint', () => {
  beforeEach(() => { vi.restoreAllMocks() })
  afterEach(() => { vi.unstubAllGlobals() })

  const renderLogin = (status: Record<string, unknown>) => {
    vi.stubGlobal('fetch', mockFetch({
      '/api/auth/status': { authenticated: false, requireLogin: true, needsSetup: true, ...status },
    }))
    render(
      <MemoryRouter initialEntries={['/login']}>
        <AuthProvider><App /></AuthProvider>
      </MemoryRouter>,
    )
  }

  it('names INITIAL_PASSWORD instead of the 123456 fallback when the server sets it', async () => {
    renderLogin({ initialPasswordSet: true })
    expect(await screen.findByText('INITIAL_PASSWORD')).toBeTruthy()
    expect(screen.queryByText('123456')).toBeNull()
  })

  it('still shows the 123456 fallback on a bare fresh install', async () => {
    renderLogin({ initialPasswordSet: false })
    expect(await screen.findByText('123456')).toBeTruthy()
  })
})
