import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
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
      expect(screen.getByText('No API keys yet.')).toBeTruthy()
    })
    expect(screen.queryByLabelText(/dashboard password/i)).toBeNull()
  })
})
