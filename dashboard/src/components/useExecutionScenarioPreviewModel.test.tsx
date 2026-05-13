// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useExecutionScenarioPreviewModel } from './useExecutionScenarioPreviewModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getScript: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

function PreviewHarness({ test }: { test: unknown }) {
  const model = useExecutionScenarioPreviewModel(test as never)

  return (
    <div>
      <span data-testid="loading">{model.scriptDetailsLoading ? 'loading' : 'idle'}</span>
      <span data-testid="error">{model.scriptDetailsError || 'none'}</span>
      <span data-testid="rows">{model.queuedScenarioRows.length}</span>
      <span data-testid="actions">{model.queuedScenarioRows[0]?.actionCount ?? 0}</span>
    </div>
  )
}

describe('useExecutionScenarioPreviewModel', () => {
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
    container.remove()
    vi.clearAllMocks()
  })

  it('loads distinct script details and exposes queued preview rows', async () => {
    apiMock.getScript.mockImplementation(async (scriptId: string) => ({
      id: scriptId,
      name: `Script ${scriptId}`,
      protocol: 'http',
      script: {
        actions: [{}, {}],
      },
    }))

    await act(async () => {
      root.render(
        <PreviewHarness
          test={{
            id: 'test-1',
            name: 'Queued Checkout',
            status: 'QUEUED',
            total_vusers: 20,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            groups: [
              {
                name: 'Checkout',
                script_id: 'script-1',
                script_name: 'Checkout Script',
                protocol: 'http',
                vusers: 10,
              },
              {
                name: 'Search',
                script_id: 'script-1',
                script_name: 'Checkout Script',
                protocol: 'http',
                vusers: 10,
              },
            ],
          }}
        />,
      )
    })

    await flushPromises()

    expect(apiMock.getScript).toHaveBeenCalledTimes(1)
    expect(readTestId('loading')).toBe('idle')
    expect(readTestId('error')).toBe('none')
    expect(readTestId('rows')).toBe('2')
    expect(readTestId('actions')).toBe('2')
  })

  it('surfaces fetch failures as preview errors', async () => {
    apiMock.getScript.mockRejectedValue(new Error('boom'))

    await act(async () => {
      root.render(
        <PreviewHarness
          test={{
            id: 'test-1',
            name: 'Queued Checkout',
            status: 'QUEUED',
            total_vusers: 20,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            groups: [{
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
            }],
          }}
        />,
      )
    })

    await flushPromises()

    expect(readTestId('loading')).toBe('idle')
    expect(readTestId('error')).toBe('boom')
  })

  function readTestId(testId: string) {
    return container.querySelector(`[data-testid="${testId}"]`)?.textContent
  }
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}