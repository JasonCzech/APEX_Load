// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AboutDialog from './AboutDialog'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const getVersion = vi.fn()
vi.mock('../hooks/api/useSystemApi', () => ({
  useSystemApi: () => ({ getVersion }),
}))

describe('AboutDialog', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    getVersion.mockReset()
    getVersion.mockResolvedValue({
      version: '1.4.2',
      display_version: '1.4.2',
      commit: 'abc1234',
      build_time: '2026-06-27T10:00:00Z',
      go_version: 'go1.26.4',
      environment: 'desktop',
    })
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.restoreAllMocks()
  })

  it('renders nothing when closed', async () => {
    await act(async () => { root.render(<AboutDialog open={false} onClose={() => {}} />) })
    expect(container.querySelector('[role="dialog"]')).toBeNull()
    expect(getVersion).not.toHaveBeenCalled()
  })

  it('fetches and shows live build metadata when open', async () => {
    await act(async () => { root.render(<AboutDialog open onClose={() => {}} />) })
    // allow the version promise to resolve
    await act(async () => { await Promise.resolve() })

    const text = container.textContent || ''
    expect(text).toContain('1.4.2')
    expect(text).toContain('abc1234')
    expect(text).toContain('go1.26.4')
    expect(getVersion).toHaveBeenCalledTimes(1)
  })

  it('invokes onClose from the Close button', async () => {
    const onClose = vi.fn()
    await act(async () => { root.render(<AboutDialog open onClose={onClose} />) })
    const closeBtn = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Close')
    await act(async () => { closeBtn?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(onClose).toHaveBeenCalled()
  })
})
