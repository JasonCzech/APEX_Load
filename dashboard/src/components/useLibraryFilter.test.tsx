// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useLibraryFilter } from './useLibraryFilter'
import { useLibraryPagination } from './useLibraryPagination'
import LibraryPagination from './LibraryPagination'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const choices = ['all', 'blocked'] as const
function Pager({ total, scope }: { total: number; scope: string | null }) {
  return <LibraryPagination label="Scenarios" pagination={useLibraryPagination(total, 'rows', scope)} />
}
function Filter({ scope }: { scope: string | null }) {
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

  it('keeps a null-scoped filter in memory only', () => {
    act(() => root.render(<Filter scope={null} />))
    act(() => host.querySelector('button')!.click())
    expect(host.textContent).toBe('blocked')
    expect(sessionStorage.length).toBe(0)
  })

  it('restores rows per page with the library filters and hides the pager for an empty table', () => {
    act(() => root.render(<Pager total={40} scope="scenarios:a" />))
    const rows = () => host.querySelector<HTMLSelectElement>('select')!
    act(() => {
      rows().value = '25'
      rows().dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(host.textContent).toContain('Showing 1–25 of 40')
    act(() => root.unmount())
    root = createRoot(host)
    act(() => root.render(<Pager total={40} scope="scenarios:a" />))
    expect(rows().value).toBe('25')
    act(() => root.render(<Pager total={0} scope="scenarios:a" />))
    expect(host.querySelector('nav')).toBeNull()
  })
})
