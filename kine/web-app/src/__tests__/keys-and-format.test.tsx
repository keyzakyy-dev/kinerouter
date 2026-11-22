import { describe, expect, it, vi, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AuthProvider } from '@/lib/auth'
import App from '@/App'
import { fmtBucket, fmtCost, fmtInt, maskSecret, moveItem } from '@/lib/format'

afterEach(() => { vi.unstubAllGlobals() })

const key = {
  id: 'k1', key: 'sk-abcdef1234567890', name: 'CI pipeline', machineId: 'm-1',
  isActive: true, createdAt: '2026-11-01T10:00:00.000Z',
  accessRestricted: false, accessAllow: [],
}

describe('key list', () => {
  it('renders each key with a masked secret and active badge', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input).split('?')[0]
      const body =
        url === '/api/auth/status' ? { authenticated: true, requireLogin: true }
        : url === '/api/keys' ? { keys: [key], machineId: 'm-1' }
        : url === '/api/version' ? { version: '0.1.0', latest: '0.1.0', updateAvailable: false }
        : {}
      return new Response(JSON.stringify(body), { status: 200 })
    }))

    render(
      <MemoryRouter initialEntries={['/dashboard']}>
        <AuthProvider><App /></AuthProvider>
      </MemoryRouter>,
    )

    await waitFor(() => expect(screen.getByText('CI pipeline')).toBeTruthy())
    expect(screen.getByText(maskSecret(key.key))).toBeTruthy()
    expect(screen.queryByText(key.key)).toBeNull()
    expect(screen.getByText('active')).toBeTruthy()
  })
})

describe('usage formatting', () => {
  it('abbreviates token counts and costs', () => {
    expect(fmtInt(999)).toBe('999')
    expect(fmtInt(12_500)).toBe('12.5K')
    expect(fmtInt(3_200_000)).toBe('3.20M')
    expect(fmtCost(0)).toBe('$0.00')
    expect(fmtCost(0.0021)).toBe('$0.0021')
    expect(fmtCost(1.234)).toBe('$1.23')
  })

  it('labels chart buckets by day and by hour', () => {
    expect(fmtBucket('2026-11-04', false)).toBe('11/04')
    expect(fmtBucket('2026-11-04T07', true)).toBe('07:00')
  })
})

describe('combo ordering', () => {
  it('moves a model up, down, and keeps out-of-range moves inert', () => {
    const models = ['a', 'b', 'c']
    expect(moveItem(models, 2, 0)).toEqual(['c', 'a', 'b'])
    expect(moveItem(models, 0, 1)).toEqual(['b', 'a', 'c'])
    expect(moveItem(models, 0, 9)).toBe(models)
    expect(moveItem(models, -1, 0)).toBe(models)
    expect(moveItem(models, 1, 1)).toBe(models)
  })
})
