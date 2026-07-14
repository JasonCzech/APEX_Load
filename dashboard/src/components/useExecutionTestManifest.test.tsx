// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { TestRunManifest, TestStatus, TestSummary } from '../types'
import { useExecutionTestManifest } from './useExecutionTestManifest'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMocks = vi.hoisted(() => ({ getTestManifest: vi.fn() }))

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMocks,
}))

function testSummary(id: string, status: TestStatus = 'RUNNING'): TestSummary {
  return {
    id,
    name: id,
    status,
    groups: [],
    total_vusers: 1,
    duration: '1m',
    created_at: '2026-07-14T00:00:00Z',
  }
}

function manifest(testID: string): TestRunManifest {
  return {
    schema_version: '1',
    test_id: testID,
    test_name: testID,
    status: 'RUNNING',
    source: 'live',
    generated_at: '2026-07-14T00:00:00Z',
    identity: {},
    initial_configuration: {},
    current_configuration: {},
    load_generators: {},
  }
}

function Harness({ test }: { test: TestSummary | null }) {
  const result = useExecutionTestManifest(test)
  return (
    <div>
      <span data-testid="manifest">{result.manifest?.test_id || 'none'}</span>
      <span data-testid="loading">{String(result.manifestLoading)}</span>
      <button type="button" onClick={() => { void result.reloadManifest() }}>Reload</button>
    </div>
  )
}

describe('useExecutionTestManifest', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    apiMocks.getTestManifest.mockReset()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.useRealTimers()
  })

  it('does not let a manual reload from the previous test overwrite the current manifest', async () => {
    let resolveReloadA: ((value: TestRunManifest) => void) | undefined
    apiMocks.getTestManifest
      .mockResolvedValueOnce(manifest('test-a'))
      .mockImplementationOnce(() => new Promise(resolve => { resolveReloadA = resolve }))
      .mockResolvedValueOnce(manifest('test-b'))

    await act(async () => {
      root.render(<Harness test={testSummary('test-a')} />)
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.querySelector('[data-testid="manifest"]')?.textContent).toBe('test-a')

    await act(async () => {
      container.querySelector('button')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    await act(async () => {
      root.render(<Harness test={testSummary('test-b')} />)
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.querySelector('[data-testid="manifest"]')?.textContent).toBe('test-b')

    await act(async () => {
      resolveReloadA?.(manifest('test-a'))
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.querySelector('[data-testid="manifest"]')?.textContent).toBe('test-b')
    expect(container.querySelector('[data-testid="loading"]')?.textContent).toBe('false')
  })

  it('polls the manifest while a run is in shakeout', async () => {
    vi.useFakeTimers()
    apiMocks.getTestManifest.mockResolvedValue(manifest('shakeout-test'))

    await act(async () => {
      root.render(<Harness test={testSummary('shakeout-test', 'SHAKEOUT')} />)
      await Promise.resolve()
    })
    expect(apiMocks.getTestManifest).toHaveBeenCalledTimes(1)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    expect(apiMocks.getTestManifest).toHaveBeenCalledTimes(2)
  })
})
