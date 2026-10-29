// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { DebugVariableWatches } from './DebuggerSessionTools'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

it('does not treat an absent pinned prototype-like name as a changed value', async () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  const props = { events: [], seed: {}, selected: null, onSelect: () => {}, mode: 'retained' as const }
  try {
    await act(async () => root.render(<DebugVariableWatches {...props} latest={JSON.parse('{"constructor":"value"}')} />))
    const pin = host.querySelector<HTMLButtonElement>('button[aria-label="Pin constructor"]')!
    expect(pin).toBeTruthy()
    await act(async () => pin.click())
    await act(async () => root.render(<DebugVariableWatches {...props} latest={{}} />))
    expect(host.textContent).toContain('Not available')
    const changedOnly = Array.from(host.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))[0]
    await act(async () => changedOnly.click())
    expect(host.querySelector('button[aria-label="Pin constructor"]')).toBeNull()
  } finally {
    await act(async () => root.unmount())
  }
})
