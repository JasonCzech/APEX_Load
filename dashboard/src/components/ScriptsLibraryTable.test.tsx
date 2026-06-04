// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ScriptsLibraryTable, { type ScriptLibraryFederation } from './ScriptsLibraryTable'
import type { StoredScript } from '../types'
import type { LibraryCatalogItem } from '../types/library'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const script: StoredScript = {
  id: 'local-script',
  name: 'Local Checkout',
  protocol: 'http',
  version: '1.0.0',
  created_at: '2026-05-01T00:00:00Z',
  scenario_count: 0,
  associated_releases: [],
  scenario_run_releases: [],
  last_run_at: '',
} as StoredScript

const remoteItem: LibraryCatalogItem = {
  kind: 'script',
  id: 'host-script',
  name: 'Host Only Script',
  project_id: 'project-1',
  protocol: 'http',
  content_hash: 'hosthash123456',
  updated_at: '2026-05-02T00:00:00Z',
  sync_status: 'host only',
}

function makeFederation(overrides: Partial<ScriptLibraryFederation> = {}): ScriptLibraryFederation {
  return {
    sourceFilter: 'all',
    remoteRows: [remoteItem],
    actionBlocked: false,
    actionBlockedReason: '',
    onPushScript: vi.fn(),
    onPullRemote: vi.fn(),
    onCompare: vi.fn(),
    hostItemForLocal: () => undefined,
    syncStatusForLocal: () => 'local only',
    remoteItemHref: () => 'https://remote.example.test/#/scripts?scriptId=host-script',
    ...overrides,
  }
}

const baseProps = {
  scripts: [script],
  sortField: 'name' as const,
  sortDirection: 'asc' as const,
  onSort: vi.fn(),
  expandedScriptId: null,
  onToggleScriptRuns: vi.fn(),
  loadingRunsFor: null,
  scriptRuns: {},
  innerSortField: 'name' as const,
  innerSortDirection: 'asc' as const,
  onInnerSort: vi.fn(),
  getSortedInnerRuns: (runs: never[]) => runs,
  onEdit: vi.fn(),
  onDelete: vi.fn(),
}

describe('ScriptsLibraryTable federation', () => {
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
    vi.clearAllMocks()
  })

  function findButton(label: string) {
    return Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === label) as HTMLButtonElement | undefined
  }

  it('adds a Source column and renders local and remote rows with transfer actions', async () => {
    const federation = makeFederation()
    await act(async () => {
      root.render(<ScriptsLibraryTable {...baseProps} federation={federation} />)
    })

    const headers = Array.from(container.querySelectorAll('th')).map(th => th.textContent || '')
    expect(headers.some(header => header.includes('Source'))).toBe(true)

    expect(container.textContent).toContain('Local')
    expect(container.textContent).toContain('Local Checkout')
    expect(container.textContent).toContain('Remote')
    expect(container.textContent).toContain('Host Only Script')

    const pushButton = findButton('Push')
    expect(pushButton).toBeDefined()
    await act(async () => { pushButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(federation.onPushScript).toHaveBeenCalledWith(script)

    const pullButton = findButton('Pull')
    expect(pullButton).toBeDefined()
    await act(async () => { pullButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(federation.onPullRemote).toHaveBeenCalledWith(remoteItem)

    const openLink = Array.from(container.querySelectorAll('a')).find(a => a.textContent?.includes('Open in Remote Library'))
    expect(openLink).toBeDefined()
  })

  it('hides local rows when the source filter is remote-only', async () => {
    await act(async () => {
      root.render(<ScriptsLibraryTable {...baseProps} federation={makeFederation({ sourceFilter: 'host' })} />)
    })
    expect(container.textContent).not.toContain('Local Checkout')
    expect(container.textContent).toContain('Host Only Script')
  })

  it('hides remote rows when the source filter is local-only', async () => {
    await act(async () => {
      root.render(<ScriptsLibraryTable {...baseProps} federation={makeFederation({ sourceFilter: 'local', remoteRows: [] })} />)
    })
    expect(container.textContent).toContain('Local Checkout')
    expect(container.textContent).not.toContain('Host Only Script')
  })

  it('disables transfers and suppresses them in read-only mode', async () => {
    await act(async () => {
      root.render(<ScriptsLibraryTable {...baseProps} canManageScripts={false} federation={makeFederation({ actionBlocked: true })} />)
    })
    // Read-only: no transfer buttons at all
    expect(findButton('Push')).toBeUndefined()
    expect(findButton('Pull')).toBeUndefined()
    expect(container.textContent).toContain('Read-only')
  })
})
