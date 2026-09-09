// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { PERSONAL_PREFERENCES_KEY, readPersonalPreferences, usePersonalAppearance, usePersonalPreferences } from './usePersonalPreferences'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
function Harness() {
  const [preferences, update] = usePersonalPreferences()
  return <button onClick={() => update({ tableRows: 25, motion: 'reduce' })}>{JSON.stringify(preferences)}</button>
}
function Appearance() { usePersonalAppearance(); return null }
describe('personal preferences', () => {
  let host: HTMLDivElement
  let root: Root
  beforeEach(() => { localStorage.clear(); host = document.createElement('div'); document.body.append(host); root = createRoot(host) })
  afterEach(() => { act(() => root.unmount()); host.remove(); localStorage.clear(); vi.restoreAllMocks() })
  it.each(['null', '{', '{}', '{"tableRows":100,"motion":"always"}'])('normalizes unsupported storage: %s', value => {
    localStorage.setItem(PERSONAL_PREFERENCES_KEY, value)
    expect(readPersonalPreferences()).toEqual({ tableRows: 10, motion: 'system' })
  })
  it('synchronizes mounted consumers, persists preferences, and applies reduced motion', () => {
    act(() => root.render(<><Harness /><Harness /><Appearance /></>))
    act(() => host.querySelector('button')!.click())
    expect([...host.querySelectorAll('button')].map(button => button.textContent)).toEqual(Array(2).fill('{"tableRows":25,"motion":"reduce"}'))
    expect(readPersonalPreferences()).toEqual({ tableRows: 25, motion: 'reduce' })
    expect(document.documentElement.dataset.motion).toBe('reduce')
    localStorage.removeItem(PERSONAL_PREFERENCES_KEY)
    act(() => window.dispatchEvent(new StorageEvent('storage', { key: PERSONAL_PREFERENCES_KEY })))
    expect(document.documentElement.dataset.motion).toBe('system')
  })
  it('keeps session preferences working when storage is unavailable', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('unavailable') })
    act(() => root.render(<><Harness /><Appearance /></>))
    act(() => host.querySelector('button')!.click())
    expect(document.documentElement.dataset.motion).toBe('reduce')
  })
})
