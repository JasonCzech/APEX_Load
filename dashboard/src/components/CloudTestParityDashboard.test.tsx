// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { CloudTestCapability, ParityStatusResponse } from '../types/system'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const getParityStatus = vi.fn()

vi.mock('../hooks/api/useSystemApi', () => ({
  useSystemApi: () => ({ getParityStatus }),
}))

import CloudTestParityDashboard from './CloudTestParityDashboard'

function capability(overrides: Partial<CloudTestCapability>): CloudTestCapability {
  return {
    id: 'regional_analytics',
    category: 'analytics',
    name: 'Live and archived regional analytics',
    cloudtest_evidence_url: 'https://example.com/evidence',
    evidence_confidence: 'current_api',
    apex_status: 'partial',
    workstream: 'CT-7',
    acceptance_test: 'A two-region run reconciles.',
    implementation_status: 'in_progress',
    ...overrides,
  }
}

function response(capabilities: CloudTestCapability[]): ParityStatusResponse {
  return {
    generated_at: '2026-08-25T00:00:00Z',
    overall: 'partial',
    areas: [],
    cloudtest_capabilities: capabilities,
  }
}

describe('CloudTestParityDashboard', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    getParityStatus.mockReset().mockResolvedValue(response([
      capability({}),
      capability({ id: 'http2', name: 'HTTP/2', category: 'protocols', apex_status: 'supported', implementation_status: 'implemented' }),
      capability({ id: 'native_mobile', name: 'Native mobile application automation', category: 'non_goals', apex_status: 'non_goal', implementation_status: 'non_goal' }),
    ]))
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  it('renders canonical counts and filters by status', async () => {
    await act(async () => {
      root.render(<CloudTestParityDashboard />)
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(getParityStatus).toHaveBeenCalledTimes(1)
    expect(container.querySelector('[aria-label="CloudTest capability total"]')?.textContent).toBe('3')
    expect(container.textContent).toContain('Live and archived regional analytics')
    expect(container.textContent).toContain('HTTP/2')

    const status = container.querySelector('[aria-label="CloudTest parity status"]') as HTMLSelectElement
    await act(async () => {
      status.value = 'supported'
      status.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(container.textContent).toContain('HTTP/2')
    expect(container.textContent).not.toContain('Live and archived regional analytics')
  })

  it('surfaces endpoint failures without rendering stale rows', async () => {
    getParityStatus.mockRejectedValueOnce(new Error('network unavailable'))
    await act(async () => {
      root.render(<CloudTestParityDashboard />)
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('network unavailable')
    expect(container.querySelectorAll('tbody tr')).toHaveLength(0)
  })
})
