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

  it('renders goal telemetry controls and dispatches updates', async () => {
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
    const serverProcessingInput = container.querySelector('input[placeholder="e.g. 450"]') as HTMLInputElement | null
    const transferInput = container.querySelector('input[placeholder="e.g. 120"]') as HTMLInputElement | null
    expect(maxErrorCountInput).not.toBeNull()
    expect(serverProcessingInput).not.toBeNull()
    expect(transferInput).not.toBeNull()
    expect(maxErrorCountInput?.value).toBe('8')

    const checkboxes = Array.from(container.querySelectorAll('input[type="checkbox"]')) as HTMLInputElement[]
    expect(checkboxes).toHaveLength(2)
    expect(checkboxes[1]?.checked).toBe(false)

    if (!maxErrorCountInput || !serverProcessingInput || !transferInput || !checkboxes[1]) {
      return
    }

    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      valueSetter?.call(serverProcessingInput, '450')
      serverProcessingInput.dispatchEvent(new Event('input', { bubbles: true }))
      serverProcessingInput.dispatchEvent(new Event('change', { bubbles: true }))
      valueSetter?.call(transferInput, '120')
      transferInput.dispatchEvent(new Event('input', { bubbles: true }))
      transferInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      valueSetter?.call(maxErrorCountInput, '15')
      maxErrorCountInput.dispatchEvent(new Event('input', { bubbles: true }))
      maxErrorCountInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await act(async () => {
      checkboxes[1].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onGoalConfigChange).toHaveBeenCalledWith('max_avg_server_processing_ms', 450)
    expect(onGoalConfigChange).toHaveBeenCalledWith('max_avg_transfer_ms', 120)
    expect(onGoalConfigChange).toHaveBeenCalledWith('max_error_count', 15)
    expect(onGoalConfigChange).toHaveBeenCalledWith('stop_on_max_errors', true)
  })

  it('renders Web Vitals thresholds and dispatches updates', async () => {
    const onGoalConfigChange = vi.fn()

    await act(async () => {
      root.render(
        <CreateTestGoalSettings
          goalConfig={{ max_lcp_ms: 2500, max_cls: 0.1 }}
          onGoalConfigChange={onGoalConfigChange}
        />,
      )
    })

    const lcpInput = container.querySelector('input[placeholder="e.g. 2500"]') as HTMLInputElement | null
    const inpInput = container.querySelector('input[placeholder="e.g. 200"]') as HTMLInputElement | null
    const clsInput = container.querySelector('input[placeholder="e.g. 0.1"]') as HTMLInputElement | null
    expect(lcpInput).not.toBeNull()
    expect(inpInput).not.toBeNull()
    expect(clsInput).not.toBeNull()
    expect(lcpInput?.value).toBe('2500')
    expect(clsInput?.value).toBe('0.1')
    if (!lcpInput || !inpInput || !clsInput) return

    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      valueSetter?.call(inpInput, '200')
      inpInput.dispatchEvent(new Event('input', { bubbles: true }))
      inpInput.dispatchEvent(new Event('change', { bubbles: true }))
      valueSetter?.call(clsInput, '0.25')
      clsInput.dispatchEvent(new Event('input', { bubbles: true }))
      clsInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(onGoalConfigChange).toHaveBeenCalledWith('max_inp_ms', 200)
    expect(onGoalConfigChange).toHaveBeenCalledWith('max_cls', 0.25)

    // Clearing a vitals threshold removes it.
    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      valueSetter?.call(lcpInput, '')
      lcpInput.dispatchEvent(new Event('input', { bubbles: true }))
      lcpInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onGoalConfigChange).toHaveBeenCalledWith('max_lcp_ms', undefined)
  })
})