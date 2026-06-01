// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CreateTestModal from './CreateTestModal'

const apiMock = {
  estimateTest: vi.fn(),
  createTest: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('./CreateTestCapacityPreview', () => ({
  default: () => <div data-testid="capacity-preview" />,
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('CreateTestModal', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.useFakeTimers()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.estimateTest.mockResolvedValue({
      duration: '1m',
      groups: [],
      assignment: { mode: 'auto' },
      footprint: {
        total_vusers: 10,
        duration: '1m',
        complexity_score: 1.2,
        estimated_cpu_cores: 0.1,
        estimated_memory_mb: 32,
        estimated_network_mbps: 1,
      },
      assignment_recommendation: {
        mode: 'auto',
        recommended_targets: ['gen-a'],
        summary: 'Auto-assignment recommends one host.',
      },
    })
    apiMock.createTest.mockResolvedValue({ id: 'test-1' })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
    vi.useRealTimers()
  })

  it('submits richer runtime overrides in the create payload', async () => {
    const onCreated = vi.fn()

    await act(async () => {
      root.render(
        <CreateTestModal
          scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]}
          onClose={vi.fn()}
          onCreated={onCreated}
        />,
      )
    })

    await act(async () => {
      vi.advanceTimersByTime(300)
      await Promise.resolve()
    })

    const pacingSelect = Array.from(container.querySelectorAll('select')).find(
      select => select.parentElement?.textContent?.includes('Pacing'),
    ) as HTMLSelectElement | undefined
    const thinkTimeInput = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('type') === 'number' && input.parentElement?.textContent?.includes('Think Time Override'),
    ) as HTMLInputElement | undefined
    const continueOnErrorCheckbox = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('type') === 'checkbox' && input.parentElement?.textContent?.includes('Continue on error'),
    ) as HTMLInputElement | undefined
    const snapshotOnErrorCheckbox = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('type') === 'checkbox' && input.parentElement?.textContent?.includes('Snapshot on error'),
    ) as HTMLInputElement | undefined

    expect(pacingSelect).toBeDefined()
    expect(thinkTimeInput).toBeDefined()
    expect(continueOnErrorCheckbox).toBeDefined()
    expect(snapshotOnErrorCheckbox).toBeDefined()
    if (!pacingSelect || !thinkTimeInput || !continueOnErrorCheckbox || !snapshotOnErrorCheckbox) {
      return
    }

    await act(async () => {
      pacingSelect.value = 'fixed_delay'
      pacingSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const delayInput = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('type') === 'number' && input.parentElement?.textContent?.includes('Pacing delay'),
    ) as HTMLInputElement | undefined

    expect(delayInput).toBeDefined()
    if (!delayInput) {
      return
    }

    await act(async () => {
      setInputValue(delayInput, '1200')
      setInputValue(thinkTimeInput, '900')
      continueOnErrorCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      snapshotOnErrorCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const submitButton = Array.from(container.querySelectorAll('button')).find(
      button => button.textContent?.includes('Create Queued Run'),
    ) as HTMLButtonElement | undefined

    expect(submitButton).toBeDefined()
    if (!submitButton) {
      return
    }

    await act(async () => {
      submitButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      duration: '1m',
      scenario_mode: 'vuser_driven',
      runtime_settings: {
        pacing: { type: 'fixed_delay', delay_ms: 1200 },
        think_time: { type: 'fixed', mean_ms: 900, ignore: false },
        error_handling: { continue_on_error: true, snapshot_on_error: true },
      },
    }))
    expect(onCreated).toHaveBeenCalledWith({ id: 'test-1' })
  })
})

function setInputValue(input: HTMLInputElement, value: string) {
  const prototype = Object.getPrototypeOf(input) as HTMLInputElement
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event(input.type === 'number' ? 'change' : 'input', { bubbles: true }))
}