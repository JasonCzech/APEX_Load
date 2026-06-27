// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import DesktopHome from './DesktopHome'
import type { DesktopStatusResponse } from '../types/library'
import type { RecentItem } from '../desktop/desktopRecents'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const status: DesktopStatusResponse = {
  desktop_mode: true,
  os: 'darwin',
  api_address: '127.0.0.1:54321',
  browser_url: 'http://127.0.0.1:54321',
  log_path: '/data/logs',
  app_data_path: '/data',
  storage: { type: 'sqlite' },
  remote_libraries: { count: 2, statuses: { healthy: 1 } },
}

describe('DesktopHome', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  it('renders engine + remote health and fires quick actions', async () => {
    const onQuickAction = vi.fn()
    await act(async () => {
      root.render(
        <DesktopHome
          status={status}
          recents={[]}
          onQuickAction={onQuickAction}
          onOpenRecent={() => {}}
          onClearRecents={() => {}}
        />,
      )
    })
    const text = container.textContent || ''
    expect(text).toContain('sqlite')
    expect(text).toContain('1/2 remote workspaces healthy')

    const newTest = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes('New Test'))
    await act(async () => { newTest?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(onQuickAction).toHaveBeenCalledWith('new-test')
  })

  it('renders recents and opens them', async () => {
    const recents: RecentItem[] = [{ kind: 'script', id: 's1', name: 'Order flow', route: '#/scripts/s1' }]
    const onOpenRecent = vi.fn()
    await act(async () => {
      root.render(
        <DesktopHome status={status} recents={recents} onQuickAction={() => {}} onOpenRecent={onOpenRecent} onClearRecents={() => {}} />,
      )
    })
    const recent = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes('Order flow'))
    await act(async () => { recent?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(onOpenRecent).toHaveBeenCalledWith(recents[0])
  })

  it('surfaces a startup error', async () => {
    await act(async () => {
      root.render(
        <DesktopHome status={null} startupError="failed to initialize local storage: disk full" recents={[]} onQuickAction={() => {}} onOpenRecent={() => {}} onClearRecents={() => {}} />,
      )
    })
    expect(container.textContent).toContain('Local engine failed to start')
    expect(container.textContent).toContain('disk full')
  })
})
