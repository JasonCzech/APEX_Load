// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import NewTestPage from './NewTestPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listScenarios: vi.fn(),
  createTest: vi.fn(),
  startTest: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

describe('NewTestPage', () => {
  let container: HTMLDivElement
  let root: Root
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined)

    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Multi-group (3 groups)',
          duration: '30m0s',
          scenario_mode: 'vuser_driven',
          scripts: [
            {
              order: 1,
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
            },
          ],
          script_count: 1,
          created_at: '2026-05-12T20:00:00Z',
          updated_at: '2026-05-12T20:00:00Z',
          associated_releases: [
            { id: 'release-1', name: 'PI29', application_id: 'app-1' },
            { id: 'release-2', name: 'PI29', application_id: 'app-1' },
          ],
        },
      ],
    })
    apiMock.createTest.mockResolvedValue({ id: 'test-1' })
    apiMock.startTest.mockResolvedValue({})
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    consoleErrorSpy.mockRestore()
    vi.clearAllMocks()
  })

  it('renders duplicate release names without duplicate key warnings', async () => {
    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1' }]}
          onSelectTest={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()

    const duplicateKeyWarning = consoleErrorSpy.mock.calls.some(([message]) =>
      typeof message === 'string' && message.includes('Encountered two children with the same key'),
    )

    expect(duplicateKeyWarning).toBe(false)

    const releaseTags = Array.from(container.querySelectorAll('.new-test-tag')).map((element) => element.textContent)
    expect(releaseTags).toEqual(['PI29', 'PI29'])
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}