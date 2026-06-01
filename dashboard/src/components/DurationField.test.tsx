// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import DurationField from './DurationField'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('DurationField', () => {
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

  it('splits compound duration values into guided value and unit controls', async () => {
    await act(async () => {
      root.render(<DurationField label="Duration" value="1h30m" onChange={vi.fn()} />)
    })

    expect(container.querySelector<HTMLInputElement>('input')?.value).toBe('90')
    expect(container.querySelector<HTMLSelectElement>('select')?.value).toBe('m')
  })

  it('serializes edited values back to canonical duration strings', async () => {
    const onChange = vi.fn()
    await act(async () => {
      root.render(<DurationField label="Ramp Up" value="2m" onChange={onChange} />)
    })

    const input = container.querySelector<HTMLInputElement>('input')
    const select = container.querySelector<HTMLSelectElement>('select')
    expect(input).not.toBeNull()
    expect(select).not.toBeNull()
    if (!input || !select) return

    await act(async () => {
      setInputValue(input, '5')
    })
    expect(onChange).toHaveBeenCalledWith('5m')

    await act(async () => {
      setSelectValue(select, 's')
    })
    expect(onChange).toHaveBeenCalledWith('2s')
  })

  it('supports compact preset selection', async () => {
    const onChange = vi.fn()
    await act(async () => {
      root.render(<DurationField compact showPresets label="Target" value="5m" onChange={onChange} />)
    })

    const tenMinutePreset = Array.from(container.querySelectorAll('button')).find(button => button.textContent === '10m')
    expect(tenMinutePreset).toBeDefined()
    if (!tenMinutePreset) return

    await act(async () => {
      tenMinutePreset.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onChange).toHaveBeenCalledWith('10m')
  })
})

function setInputValue(input: HTMLInputElement, value: string) {
  const prototype = Object.getPrototypeOf(input) as HTMLInputElement
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function setSelectValue(select: HTMLSelectElement, value: string) {
  const prototype = Object.getPrototypeOf(select) as HTMLSelectElement
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
}
