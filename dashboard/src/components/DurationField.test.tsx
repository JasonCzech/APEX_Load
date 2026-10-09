// @vitest-environment jsdom

import { act, useState } from 'react'
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
  it('keeps the unit and text the user typed instead of re-splitting them', async () => {
    const values: string[] = []
    function Harness() {
      const [value, setValue] = useState('')
      return <DurationField label="Ramp" value={value} onChange={next => { values.push(next); setValue(next) }} />
    }
    await act(async () => root.render(<Harness />))
    const input = container.querySelector<HTMLInputElement>('input')!
    const unit = container.querySelector<HTMLSelectElement>('select')!
    const type = async (text: string) => act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, text)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => { unit.value = 's'; unit.dispatchEvent(new Event('change', { bubbles: true })) })
    await type('60')
    // 60 s stays "60 Seconds"; it does not become "1 Minutes" under the cursor.
    expect(input.value).toBe('60')
    expect(unit.value).toBe('s')
    await type('600')
    expect(values[values.length - 1]).toBe('600s')
    await act(async () => { unit.value = 'm'; unit.dispatchEvent(new Event('change', { bubbles: true })) })
    await type('0.5')
    expect(input.value).toBe('0.5')
    expect(unit.value).toBe('m')
    expect(values[values.length - 1]).toBe('0.5m')
  })

  it('rejects exponent notation instead of reading 1e2 as 100', async () => {
    const onChange = vi.fn()
    function Harness() {
      const [value, setValue] = useState('5m')
      return <DurationField label="Duration" value={value} onChange={next => { onChange(next); setValue(next) }} />
    }
    await act(async () => root.render(<Harness />))
    const input = container.querySelector<HTMLInputElement>('input')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, '1e2')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onChange).toHaveBeenLastCalledWith('')
    expect(input.getAttribute('aria-invalid')).toBe('true')
    expect(container.textContent).toContain('Enter a positive duration.')
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
