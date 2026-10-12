// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import LibraryToolbar from './LibraryToolbar'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('LibraryToolbar', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  const filters = <label>Folder<select aria-label="Folder"><option>All</option></select></label>
  const fields = () => container.querySelector<HTMLElement>('.ux-library-filter-fields')!
  const toggle = () => container.querySelector<HTMLButtonElement>('.ux-library-filters-toggle')

  it('folds its filters behind a Filters toggle in the one row, keeping them mounted', async () => {
    await act(async () => root.render(<LibraryToolbar search={<input aria-label="Search" />} filters={filters} filterCount={0} />))
    expect(toggle()?.textContent).toBe('Filters')
    expect(toggle()?.getAttribute('aria-expanded')).toBe('false')
    expect(toggle()?.getAttribute('aria-controls')).toBe(fields().id)
    expect(fields().hidden).toBe(true)
    expect(fields().querySelector('select')).not.toBeNull()
    await act(async () => toggle()!.click())
    expect(fields().hidden).toBe(false)
    expect(toggle()?.getAttribute('aria-expanded')).toBe('true')
  })

  it('opens with a filter set and counts the filters set', async () => {
    await act(async () => root.render(<LibraryToolbar filters={filters} filterCount={2} />))
    expect(fields().hidden).toBe(false)
    expect(toggle()?.querySelector('.ux-library-filters-count')?.textContent).toBe('2')
    expect(toggle()?.textContent).toBe('Filters2, 2 set')
  })

  it('opens when a filter gets set from outside, and stays as the user left it after', async () => {
    await act(async () => root.render(<LibraryToolbar filters={filters} filterCount={0} />))
    expect(fields().hidden).toBe(true)
    await act(async () => root.render(<LibraryToolbar filters={filters} filterCount={1} />))
    expect(fields().hidden).toBe(false)
    await act(async () => toggle()!.click())
    await act(async () => root.render(<LibraryToolbar filters={filters} filterCount={2} />))
    expect(fields().hidden).toBe(true)
  })

  it('shows its filters as before when no count is given', async () => {
    await act(async () => root.render(<LibraryToolbar filters={filters} />))
    expect(toggle()).toBeNull()
    expect(fields().hidden).toBe(false)
  })
})
