// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CreateTestModal from './CreateTestModal'
import { NavigationGuardProvider } from '../hooks/NavigationGuardProvider'

const apiMock = {
  estimateTest: vi.fn(),
  createTest: vi.fn(),
  getSystemInventory: vi.fn(),
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
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: { id: 'controller-1', status: 'ready', address: ':8080', version: 'dev', execution_mode: 'distributed' },
      generators: [],
    })
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

  it('prompts before closing a dirty queued-run draft', async () => {
    const onClose = vi.fn()

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <CreateTestModal
            scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]}
            onClose={onClose}
            onCreated={vi.fn()}
          />
        </NavigationGuardProvider>,
      )
    })

    const nameInput = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
      .find(input => input.placeholder?.includes('auto-generated'))
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Dirty queued run')
    })

    await click('Cancel')

    expect(onClose).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Unsaved Changes')

    await click('Keep editing')
    expect(container.textContent).not.toContain('Unsaved Changes')

    await click('Cancel')
    await click('Discard changes')

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('closes a clean queued-run draft without prompting', async () => {
    const onClose = vi.fn()

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <CreateTestModal
            scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]}
            onClose={onClose}
            onCreated={vi.fn()}
          />
        </NavigationGuardProvider>,
      )
    })

    await click('Cancel')

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('Unsaved Changes')
  })

  async function click(label: string) {
    const button = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(candidate => candidate.textContent?.includes(label))
    expect(button).toBeDefined()
    await act(async () => {
      button?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => Promise.resolve())
  }
})

function setInputValue(input: HTMLInputElement, value: string) {
  const prototype = Object.getPrototypeOf(input) as HTMLInputElement
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event(input.type === 'number' ? 'change' : 'input', { bubbles: true }))
}
