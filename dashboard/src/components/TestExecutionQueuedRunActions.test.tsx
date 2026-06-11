// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TestExecutionQueuedRunActions from './TestExecutionQueuedRunActions'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const editorMock = vi.fn()

vi.mock('./TestExecutionScenarioEditor', () => ({
  default: ({ open, test, onClose, onSelectTest }: { open: boolean, test: { name: string }, onClose: () => void, onSelectTest: (id: string) => void }) => {
    editorMock({ open, test })
    return open ? (
      <div data-testid="scenario-editor">
        <span>{test.name}</span>
        <button onClick={onClose}>close-editor</button>
        <button onClick={() => onSelectTest('revised-test-1')}>select-created</button>
      </div>
    ) : null
  },
}))

describe('TestExecutionQueuedRunActions', () => {
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
    editorMock.mockReset()
    vi.clearAllMocks()
  })

  it('owns queued-run revise modal state and keeps start/select wiring intact', async () => {
    const onStart = vi.fn()
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(
        <TestExecutionQueuedRunActions
          test={{
            id: 'test-1',
            name: 'Queued Checkout Run',
            status: 'QUEUED',
            total_vusers: 10,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            groups: [],
          } as never}
          onStart={onStart}
          onSelectTest={onSelectTest}
        />,
      )
    })

    const buttons = Array.from(container.querySelectorAll('button'))
    const reviseButton = buttons.find((button) => button.textContent?.includes('Revise Queued Run'))
    const startButton = buttons.find((button) => button.textContent?.includes('Start Test'))

    expect(reviseButton).toBeDefined()
    expect(startButton).toBeDefined()
    expect(container.textContent).not.toContain('close-editor')

    await act(async () => {
      reviseButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Queued Checkout Run')
    expect(container.textContent).toContain('close-editor')

    const selectButton = Array.from(container.querySelectorAll('button')).find((button) => button.textContent?.includes('select-created'))
    await act(async () => {
      selectButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onSelectTest).toHaveBeenCalledWith('revised-test-1')

    await act(async () => {
      startButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onStart).toHaveBeenCalledTimes(1)
  })

  it('renders nothing for non-queued tests', async () => {
    await act(async () => {
      root.render(
        <TestExecutionQueuedRunActions
          test={{
            id: 'test-1',
            name: 'Running Checkout Run',
            status: 'RUNNING',
            total_vusers: 10,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            groups: [],
          } as never}
          onStart={vi.fn()}
          onSelectTest={vi.fn()}
        />,
      )
    })

    expect(container.textContent).toBe('')
  })

  it('shows the start action without queued-run revision for reserved tests', async () => {
    const onStart = vi.fn()

    await act(async () => {
      root.render(
        <TestExecutionQueuedRunActions
          test={{
            id: 'test-1',
            name: 'Reserved Checkout Run',
            status: 'RESERVED',
            total_vusers: 10,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            scheduled_start_at: '2026-05-12T20:15:00Z',
            groups: [],
          } as never}
          onStart={onStart}
          onSelectTest={vi.fn()}
        />,
      )
    })

    expect(container.textContent).toContain('Start Test')
    expect(container.textContent).not.toContain('Revise Queued Run')

    const startButton = Array.from(container.querySelectorAll('button')).find((button) => button.textContent?.includes('Start Test'))
    await act(async () => {
      startButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onStart).toHaveBeenCalledTimes(1)
  })
})
