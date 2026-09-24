// @vitest-environment jsdom

import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

import JMeterScenarioRuntimeEditor from './JMeterScenarioRuntimeEditor'

it('applies a finite per-user iteration rule to every imported thread group', () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const onApply = vi.fn()
  try {
    act(() => root.render(<JMeterScenarioRuntimeEditor scenario={{
      name: 'Checkout scenario',
      duration: '',
      scenario_mode: 'thread_groups',
      groups: [
        { source_group_id: 'one', name: 'Buyers', script_name: 'Buyers', vusers: 10, ramp_up: '30s', initialization: 'parallel' },
        { source_group_id: 'two', name: 'Admins', script_name: 'Admins', vusers: 2, initialization: 'parallel' },
      ],
    }} onApply={onApply} />))
    const select = container.querySelector('select')!
    act(() => {
      select.value = 'iterations'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const input = container.querySelector<HTMLInputElement>('input[type=number]')!
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '4')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    act(() => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Apply runtime rule')!.click())
    expect(onApply).toHaveBeenCalledWith(expect.objectContaining({
      termination_mode: 'iterations',
      groups: [expect.objectContaining({ iterations: 4 }), expect.objectContaining({ iterations: 4 })],
    }))
    expect(container.textContent).toContain('Applied 4 iteration(s)')
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})

it('does not apply an empty or zero duration as a finite runtime rule', () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const onApply = vi.fn()
  try {
    act(() => root.render(<JMeterScenarioRuntimeEditor scenario={{
      name: 'Unbounded scenario', duration: '', scenario_mode: 'thread_groups',
      groups: [{ name: 'Users', script_name: 'Users', vusers: 1, initialization: 'parallel' }],
    }} onApply={onApply} />))
    const input = container.querySelector<HTMLInputElement>('input[aria-label="Duration"]')!
    const apply = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Apply runtime rule')!
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '0')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(apply.disabled).toBe(true)
    act(() => apply.click())
    expect(onApply).not.toHaveBeenCalled()
    expect(input.getAttribute('aria-invalid')).toBe('true')
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})
