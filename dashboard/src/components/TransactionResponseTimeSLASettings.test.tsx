// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TransactionResponseTimeSLASettings from './TransactionResponseTimeSLASettings'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('TransactionResponseTimeSLASettings', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  it.each([0.001, 1250, 1500])('keeps admitted %sms native controls valid without an artificial step or one-millisecond floor', async milliseconds => {
    await act(async () => root.render(<TransactionResponseTimeSLASettings defaultMs={milliseconds} groups={[{ name: 'Buyers', overrideMs: milliseconds }]} onDefaultChange={vi.fn()} onGroupOverrideChange={vi.fn()} />))
    for (const input of container.querySelectorAll<HTMLInputElement>('input')) {
      expect(input.value).toBe(String(milliseconds / 1000))
      expect(input.checkValidity()).toBe(true)
    }
  })

  it('shows inheritance and emits default and group override values in milliseconds', async () => {
    const onDefaultChange = vi.fn()
    const onGroupOverrideChange = vi.fn()
    await act(async () => root.render(
      <TransactionResponseTimeSLASettings
        defaultMs={3000}
        groups={[{ name: 'Buyers' }, { name: 'Sellers', overrideMs: 1500 }]}
        onDefaultChange={onDefaultChange}
        onGroupOverrideChange={onGroupOverrideChange}
      />,
    ))
    expect(container.textContent).toContain('2 of 2 groups covered')
    expect(container.textContent).toContain('Inherits 3s')
    expect(container.textContent).toContain('95% of successful named business transactions')

    const defaultInput = container.querySelector<HTMLInputElement>('input[aria-label="Default transaction SLA in seconds"]')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(defaultInput, '2.25')
      defaultInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onDefaultChange).toHaveBeenCalledWith(2250)

		const selectors = container.querySelectorAll<HTMLSelectElement>('select')
		await act(async () => {
			selectors[0].value = 'override'
			selectors[0].dispatchEvent(new Event('change', { bubbles: true }))
		})
    expect(onGroupOverrideChange).toHaveBeenCalledWith(0, 3000)
		await act(async () => {
			selectors[1].value = 'default'
			selectors[1].dispatchEvent(new Event('change', { bubbles: true }))
		})
    expect(onGroupOverrideChange).toHaveBeenCalledWith(1, undefined)
  })
})
