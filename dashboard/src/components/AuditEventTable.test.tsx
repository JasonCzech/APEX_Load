// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { AuditEventTable, LoadOlderEvents } from './AuditEventTable'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('AuditEventTable', () => {
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

  it('makes a scrolling event list reachable by keyboard', async () => {
    await act(async () => root.render(<AuditEventTable paginated events={[]} />))
    const region = container.querySelector('.access-table-wrap')
    expect(region?.getAttribute('tabindex')).toBe('0')
    expect(region?.getAttribute('role')).toBe('region')
    expect(region?.getAttribute('aria-label')).toBe('Audit events')
  })

  it('keeps keyboard focus while older events load and when the last batch arrives', async () => {
    const onLoad = vi.fn()
    const render = (loading: boolean, hasMore: boolean, loaded = 20) => act(async () => root.render(<LoadOlderEvents loaded={loaded} hasMore={hasMore} loading={loading} onLoad={onLoad} />))
    await render(false, true)
    const button = container.querySelector('button')!
    button.focus()
    await act(async () => button.click())
    expect(onLoad).toHaveBeenCalledTimes(1)
    await render(true, true)
    // Still the same, focused element: not disabled, so focus does not drop to the page.
    expect(document.activeElement).toBe(button)
    expect(button.getAttribute('aria-disabled')).toBe('true')
    await act(async () => button.click())
    expect(onLoad).toHaveBeenCalledTimes(1)
    await render(false, false, 31)
    expect(document.activeElement?.textContent).toBe('All 31 matching events loaded')
  })

  it('does not move focus when a later reload, not the click, finishes the list', async () => {
    const onLoad = vi.fn()
    const render = (loading: boolean, hasMore: boolean, loaded = 20) => act(async () => root.render(<LoadOlderEvents loaded={loaded} hasMore={hasMore} loading={loading} onLoad={onLoad} />))
    await render(false, true)
    const button = container.querySelector('button')!
    button.focus()
    await act(async () => button.click())
    await render(true, true)
    await render(false, true, 40)
    // The user moves on (for example to a filter) and a new query fits in one batch.
    const filter = document.createElement('input')
    document.body.appendChild(filter)
    filter.focus()
    await render(false, false, 12)
    expect(document.activeElement).toBe(filter)
    filter.remove()
  })
})
