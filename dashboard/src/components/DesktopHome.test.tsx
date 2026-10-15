// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import DesktopHome from './DesktopHome'
import type { DesktopStatusResponse } from '../types/library'
import type { RecentItem } from '../desktop/desktopRecents'

const desktopMocks = vi.hoisted(() => ({
  filesFromDroppedPaths: vi.fn(),
  dropHandler: undefined as ((paths: string[]) => void) | undefined,
}))

vi.mock('../desktop/desktopBridge', () => ({ isDesktopSurface: () => true }))
vi.mock('../desktop/desktopFiles', () => ({
  filesFromDroppedPaths: desktopMocks.filesFromDroppedPaths,
  nativeFileReadErrorMessage: (error: unknown) => error instanceof Error ? error.message : 'Unable to read dropped file.',
}))
vi.mock('../desktop/useDesktopFileDrop', () => ({
  useDesktopFileDrop: (_ref: unknown, handler: (paths: string[]) => void, enabled: boolean) => {
    desktopMocks.dropHandler = enabled ? handler : undefined
  },
}))

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
    desktopMocks.dropHandler = undefined
    desktopMocks.filesFromDroppedPaths.mockResolvedValue([])
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

    const newTest = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes('New run'))
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

  it('renders running-test cards and opens them', async () => {
    const onOpenRun = vi.fn()
    await act(async () => {
      root.render(
        <DesktopHome
          status={status}
          recents={[]}
          runningTests={[{ id: 'run-1', name: 'Checkout ramp', status: 'RUNNING' }]}
          onQuickAction={() => {}}
          onOpenRecent={() => {}}
          onOpenRun={onOpenRun}
          onClearRecents={() => {}}
        />,
      )
    })
    expect(container.textContent).toContain('Running now')
    const card = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes('Checkout ramp'))
    await act(async () => { card?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(onOpenRun).toHaveBeenCalledWith('run-1')
  })

  it('shows a relative timestamp on recent items', async () => {
    const recents: RecentItem[] = [{ kind: 'script', id: 's1', name: 'Order flow', route: '#/scripts/s1', openedAt: new Date(Date.now() - 5 * 60_000).toISOString() }]
    await act(async () => {
      root.render(<DesktopHome status={status} recents={recents} onQuickAction={() => {}} onOpenRecent={() => {}} onClearRecents={() => {}} />)
    })
    expect(container.textContent).toContain('5m ago')
  })

  it('sorts pinned recents first and toggles pin', async () => {
    const onTogglePin = vi.fn()
    const recents: RecentItem[] = [
      { kind: 'script', id: 'a', name: 'Alpha', route: '#/scripts/a' },
      { kind: 'script', id: 'b', name: 'Bravo', route: '#/scripts/b', pinned: true },
    ]
    await act(async () => {
      root.render(<DesktopHome status={status} recents={recents} onQuickAction={() => {}} onOpenRecent={() => {}} onClearRecents={() => {}} onTogglePin={onTogglePin} />)
    })
    // Pinned "Bravo" renders before "Alpha".
    const names = Array.from(container.querySelectorAll('.desktop-home-recent-name')).map(n => n.textContent)
    expect(names).toEqual(['Bravo', 'Alpha'])

    const pinBtn = Array.from(container.querySelectorAll('button')).find(b => b.getAttribute('aria-label')?.startsWith('Pin Alpha'))
    await act(async () => { pinBtn?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(onTogglePin).toHaveBeenCalledWith(recents[0])
  })

  // Clearing keeps pinned items, so Clear is offered only when an unpinned one exists.
  it('offers Clear only when something unpinned can be cleared', async () => {
    const pinnedOnly: RecentItem[] = [{ kind: 'script', id: 'b', name: 'Bravo', route: '#/scripts/b', pinned: true }]
    await act(async () => {
      root.render(<DesktopHome status={status} recents={pinnedOnly} onQuickAction={() => {}} onOpenRecent={() => {}} onClearRecents={() => {}} />)
    })
    expect(Array.from(container.querySelectorAll('button')).some(b => b.textContent === 'Clear')).toBe(false)
    await act(async () => {
      root.render(<DesktopHome status={status} recents={[...pinnedOnly, { kind: 'script', id: 'a', name: 'Alpha', route: '#/scripts/a' }]} onQuickAction={() => {}} onOpenRecent={() => {}} onClearRecents={() => {}} />)
    })
    expect(Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Clear')?.getAttribute('title')).toBe('Remove unpinned items')
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

  it('reports a nonempty desktop drop that contains no supported files', async () => {
    const onDropFiles = vi.fn()
    const onDropError = vi.fn()
    await act(async () => {
      root.render(
        <DesktopHome
          status={status}
          recents={[]}
          onQuickAction={() => {}}
          onOpenRecent={() => {}}
          onClearRecents={() => {}}
          onDropFiles={onDropFiles}
          onDropError={onDropError}
        />,
      )
    })
    await act(async () => {
      desktopMocks.dropHandler?.(['/tmp/unsupported.exe'])
      await Promise.resolve()
    })

    expect(onDropFiles).not.toHaveBeenCalled()
    expect(onDropError).toHaveBeenCalledWith('The dropped selection did not contain a supported import file.')
  })
})
