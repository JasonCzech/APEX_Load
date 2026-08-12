// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useAuthStatus } from './useAuthStatus'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const authApiMock = {
  getAuthStatus: vi.fn(),
}

vi.mock('./api/useAuthApi', () => ({
  useAuthApi: () => authApiMock,
}))

function Harness() {
  const state = useAuthStatus()

  if (state.status === 'loading') {
    return <div>loading</div>
  }

  if (state.status === 'error') {
    return <div>error:{state.message}</div>
  }

  return <div>ready:{state.auth.user?.subject || 'anonymous'}</div>
}

describe('useAuthStatus', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    vi.useRealTimers()
    container.remove()
    vi.clearAllMocks()
  })

  it('loads the current auth status', async () => {
    authApiMock.getAuthStatus.mockResolvedValue({
      enabled: true,
      mode: 'trusted_headers',
      authenticated: true,
      user: { subject: 'alice' },
    })

    await act(async () => {
      root.render(<Harness />)
    })

    await flushPromises()

    expect(container.textContent).toContain('ready:alice')
    expect(authApiMock.getAuthStatus).toHaveBeenCalledTimes(1)
  })

  it('exposes an error state when the auth lookup fails', async () => {
    authApiMock.getAuthStatus.mockRejectedValue(new Error('boom'))

    await act(async () => {
      root.render(<Harness />)
    })

    await flushPromises()

    expect(container.textContent).toContain('error:boom')
  })

  it('rechecks at local-session expiry and returns to anonymous access', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-11T12:00:00Z'))
    authApiMock.getAuthStatus
      .mockResolvedValueOnce({
        enabled: true,
        mode: 'local',
        authenticated: true,
        session_status: 'active',
        session_expires_at: '2026-08-11T12:00:05Z',
        session_ttl_seconds: 5,
        user: { subject: 'local:dev' },
      })
      .mockResolvedValueOnce({
        enabled: true,
        mode: 'local',
        authenticated: false,
        session_status: 'expired',
      })

    await act(async () => {
      root.render(<Harness />)
      await Promise.resolve()
    })
    expect(container.textContent).toContain('ready:local:dev')

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_250)
    })

    expect(authApiMock.getAuthStatus).toHaveBeenCalledTimes(2)
    expect(container.textContent).toContain('ready:anonymous')
  })

  it('polls active local sessions so external revocation re-arms the gate', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-11T12:00:00Z'))
    authApiMock.getAuthStatus
      .mockResolvedValueOnce({
        enabled: true,
        mode: 'local',
        authenticated: true,
        session_status: 'active',
        session_expires_at: '2026-08-12T00:00:00Z',
        session_ttl_seconds: 12 * 60 * 60,
        user: { subject: 'local:user' },
      })
      .mockResolvedValueOnce({
        enabled: true,
        mode: 'local',
        authenticated: false,
        session_status: 'revoked',
      })

    await act(async () => {
      root.render(<Harness />)
      await Promise.resolve()
    })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000)
    })

    expect(authApiMock.getAuthStatus).toHaveBeenCalledTimes(2)
    expect(container.textContent).toContain('ready:anonymous')
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}
