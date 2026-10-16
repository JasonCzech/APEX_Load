// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TimezoneSelect from './TimezoneSelect'
import { resolveTimezone, timezoneChoices } from './timezoneChoices'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function type(input: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('TimezoneSelect', () => {
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

  it('lists every zone with its offset while the field is edited, the current, viewer and common zones first', async () => {
    const now = new Date('2026-07-01T12:34:00Z')
    await act(async () => root.render(<TimezoneSelect ariaLabel="Zone" value="America/Chicago" now={now} onChange={() => {}} />))
    const input = container.querySelector<HTMLInputElement>('input[aria-label="Zone"]')!
    expect(document.getElementById(input.getAttribute('list')!)).toBeNull()
    await act(async () => input.focus())
    // The field empties so the whole list can be browsed; the zone stays as its placeholder.
    expect(input.value).toBe('')
    expect(input.placeholder).toBe('America/Chicago')
    const options = Array.from(document.getElementById(input.getAttribute('list')!)!.querySelectorAll('option'))
    expect(options[0].value).toBe('America/Chicago')
    // A host running in UTC (CI) also marks UTC as the viewer's zone.
    expect(options.find(option => option.value === 'UTC')?.label).toMatch(/^UTC\+00:00 · 12:34 now( · your zone)?$/)
    expect(options.find(option => option.value === 'America/Chicago')?.label).toMatch(/^UTC-05:00 · 07:34 now/)
    expect(options.some(option => option.value === 'Africa/Abidjan')).toBe(true)
    expect(document.getElementById(input.getAttribute('aria-describedby')!)?.textContent).toMatch(/^UTC-05:00 · 07:34 now/)
    await act(async () => input.blur())
    expect(input.value).toBe('America/Chicago')
    expect(document.getElementById(input.getAttribute('list')!)).toBeNull()
  })

  it('chooses only a real zone, and drops other text when left', async () => {
    const onChange = vi.fn()
    function Harness() {
      const [value, setValue] = useState('UTC')
      return <TimezoneSelect ariaLabel="Zone" value={value} onChange={zone => { onChange(zone); setValue(zone) }} />
    }
    await act(async () => root.render(<Harness />))
    const input = container.querySelector<HTMLInputElement>('input[aria-label="Zone"]')!
    await act(async () => { input.focus(); type(input, 'chicago') })
    expect(onChange).not.toHaveBeenCalled()
    await act(async () => type(input, 'america/new york'))
    expect(onChange).toHaveBeenLastCalledWith('America/New_York')
    await act(async () => type(input, 'not a zone'))
    await act(async () => { input.blur() })
    expect(input.value).toBe('America/New_York')
  })

  it('resolves typed names exactly', () => {
    const choices = timezoneChoices('UTC', 'Europe/London')
    expect(choices.slice(0, 3)).toEqual(['UTC', 'Europe/London', 'America/New_York'])
    expect(resolveTimezone(' asia/kolkata ', choices)).toBe('Asia/Kolkata')
    expect(resolveTimezone('Kolkata', choices)).toBeNull()
  })
})
