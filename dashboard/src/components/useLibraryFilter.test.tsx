// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useLibraryFilter } from './useLibraryFilter'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const choices = ['all', 'blocked'] as const
function Filter({ scope }: { scope: string }) {
  const [value, setValue] = useLibraryFilter(scope, 'health', 'all', choices)
  return <button onClick={() => setValue('blocked')}>{value}</button>
}

describe('library filter restoration', () => {
  let host: HTMLDivElement
  let root: Root
  beforeEach(() => { sessionStorage.clear(); host = document.createElement('div'); root = createRoot(host) })
  afterEach(() => { act(() => root.unmount()); vi.restoreAllMocks(); sessionStorage.clear() })

  it('restores filters on remount and isolates projects', () => {
    act(() => root.render(<Filter scope="scripts:project-a" />))
    act(() => host.querySelector('button')!.click())
    act(() => root.render(<Filter scope="scripts:project-b" />))
    expect(host.textContent).toBe('all')
    act(() => root.render(<Filter scope="scripts:project-a" />))
    expect(host.textContent).toBe('blocked')
    act(() => root.unmount())
    root = createRoot(host)
    act(() => root.render(<Filter scope="scripts:project-a" />))
    expect(host.textContent).toBe('blocked')
  })

  it.each(['null', '42', '"unsupported"', '{bad json'])('ignores invalid saved data: %s', saved => {
    sessionStorage.setItem('apexload.library-filter:scripts:a:health', saved)
    act(() => root.render(<Filter scope="scripts:a" />))
    expect(host.textContent).toBe('all')
  })

  it('keeps filtering usable when session storage is unavailable', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
    act(() => root.render(<Filter scope="scripts:a" />))
    act(() => host.querySelector('button')!.click())
    expect(host.textContent).toBe('blocked')
  })
})
