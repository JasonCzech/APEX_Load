// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import DesktopStatusMenu from './DesktopStatusMenu'
import type { DesktopStatusResponse } from '../types/library'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('DesktopStatusMenu', () => {
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

  it('keeps desktop status and shortcuts behind one top-bar control', async () => {
    const onOpenLogs = vi.fn()
    const status = { desktop_mode: true, api_address: '127.0.0.1:51234', remote_libraries: { count: 2, statuses: { healthy: 1 } } } as unknown as DesktopStatusResponse
    await act(async () => root.render(<DesktopStatusMenu status={status} onOpenLogs={onOpenLogs} />))
    const toggle = container.querySelector<HTMLButtonElement>('.desktop-status-toggle')!
    expect(container.querySelectorAll('button')).toHaveLength(1)
    expect(toggle.getAttribute('aria-expanded')).toBe('false')

    await act(async () => toggle.click())
    const panel = container.querySelector('[role="group"]')!
    expect(panel.textContent).toContain('127.0.0.1:51234')
    // Named for what it counts: remote script libraries, not load generators.
    expect(panel.textContent).toContain('Remote script libraries')
    expect(panel.textContent).toContain('1 of 2 healthy')

    await act(async () => Array.from(panel.querySelectorAll('button')).find(button => button.textContent === 'Open logs folder')!.click())
    expect(onOpenLogs).toHaveBeenCalledTimes(1)
    expect(container.querySelector('[role="group"]')).toBeNull()

    await act(async () => toggle.click())
    await act(async () => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })))
    expect(container.querySelector('[role="group"]')).toBeNull()
    expect(document.activeElement).toBe(toggle)
  })

  it('says when no remote library is connected', async () => {
    await act(async () => root.render(<DesktopStatusMenu status={{ desktop_mode: true } as unknown as DesktopStatusResponse} />))
    await act(async () => container.querySelector<HTMLButtonElement>('.desktop-status-toggle')!.click())
    expect(container.textContent).toContain('None connected')
  })
})
