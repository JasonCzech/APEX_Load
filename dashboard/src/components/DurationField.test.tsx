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

  it('preserves fractional and sub-second duration precision', async () => {
    const onChange = vi.fn()
    await act(async () => {
      root.render(<DurationField label="SLA window" value="1500ms" onChange={onChange} />)
    })

    const input = container.querySelector<HTMLInputElement>('input')
    expect(input?.value).toBe('1.5')
    expect(input?.step).toBe('any')
    if (!input) return

    await act(async () => {
      setInputValue(input, '0.25')
    })
    expect(onChange).toHaveBeenCalledWith('0.25s')
  })

  it('keeps nanosecond durations editable and serializes them without exponent notation', async () => {
    const onChange = vi.fn()
    await act(async () => {
      root.render(<DurationField label="Polling interval" value="250ns" onChange={onChange} />)
    })

    const input = container.querySelector<HTMLInputElement>('input')
    const select = container.querySelector<HTMLSelectElement>('select')
    expect(Number(input?.value)).toBe(0.00000025)
    expect(select?.value).toBe('s')
    if (!select) return

    await act(async () => {
      setSelectValue(select, 's')
    })
    expect(onChange).toHaveBeenCalledWith('0.00000025s')
  })

  it.each([
    ['+1s', 1, '1s'],
    ['.5s', 0.5, '0.5s'],
    ['1.s', 1, '1s'],
    ['1µs', 0.000001, '0.000001s'],
  ])('normalizes editable Go duration %s without losing precision', async (source, numericValue, serialized) => {
    const onChange = vi.fn()
    await act(async () => {
      root.render(<DurationField label="Duration" value={source} onChange={onChange} />)
    })

    const input = container.querySelector<HTMLInputElement>('input')
    const select = container.querySelector<HTMLSelectElement>('select')
    expect(Number(input?.value)).toBe(numericValue)
    if (!select) return

    await act(async () => {
      setSelectValue(select, 's')
    })
    expect(onChange).toHaveBeenCalledWith(serialized)
  })

  it.each([
    ['17280000000000001ns', '17280000.000000001', '17280000.000000001s'],
    ['9223372036854775807ns', '9223372036.854775807', '9223372036.854775807s'],
  ])('round-trips exact large duration %s through selection and input', async (source, numericText, serialized) => {
    const onChange = vi.fn()
    await act(async () => {
      root.render(<DurationField label="Duration" value={source} onChange={onChange} />)
    })

    const input = container.querySelector<HTMLInputElement>('input')
    const select = container.querySelector<HTMLSelectElement>('select')
    expect(input?.value).toBe(numericText)
    expect(select?.value).toBe('s')
    if (!input || !select) return

    await act(async () => {
      setSelectValue(select, 's')
    })
    expect(onChange).toHaveBeenLastCalledWith(serialized)

    onChange.mockClear()
    await act(async () => {
      setInputValue(input, `${numericText}0`)
    })
    expect(onChange).toHaveBeenLastCalledWith(serialized)
  })

  it('retains the selected unit while the numeric value is cleared', async () => {
    let value = '30s'
    const onChange = vi.fn((next: string) => { value = next })
    await act(async () => root.render(<DurationField label="Duration" value={value} onChange={onChange} />))
    const input = container.querySelector<HTMLInputElement>('input')!
    await act(async () => setInputValue(input, ''))
    await act(async () => root.render(<DurationField label="Duration" value={value} onChange={onChange} />))
    expect(container.querySelector<HTMLSelectElement>('select')?.value).toBe('s')
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
