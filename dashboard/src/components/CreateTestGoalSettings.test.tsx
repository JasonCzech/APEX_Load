// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CreateTestGoalSettings from './CreateTestGoalSettings'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('CreateTestGoalSettings', () => {
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
  })

  it('renders max error count controls and dispatches updates', async () => {
    const onGoalConfigChange = vi.fn()

    await act(async () => {
      root.render(
        <CreateTestGoalSettings
          goalConfig={{
            target_average_tps: 300,
            max_error_count: 8,
            stop_on_max_errors: false,
            sla_breach_window: '30s',
          }}
          onGoalConfigChange={onGoalConfigChange}
        />,
      )
    })

    const maxErrorCountInput = container.querySelector('input[placeholder="e.g. 25"]') as HTMLInputElement | null
    expect(maxErrorCountInput).not.toBeNull()
    expect(maxErrorCountInput?.value).toBe('8')

    const checkboxes = Array.from(container.querySelectorAll('input[type="checkbox"]')) as HTMLInputElement[]
    expect(checkboxes).toHaveLength(2)
    expect(checkboxes[1]?.checked).toBe(false)

    if (!maxErrorCountInput || !checkboxes[1]) {
      return
    }

    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      valueSetter?.call(maxErrorCountInput, '15')
      maxErrorCountInput.dispatchEvent(new Event('input', { bubbles: true }))
      maxErrorCountInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await act(async () => {
      checkboxes[1].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onGoalConfigChange).toHaveBeenCalledWith('max_error_count', 15)
    expect(onGoalConfigChange).toHaveBeenCalledWith('stop_on_max_errors', true)
  })
})