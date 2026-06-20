// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ScriptsLibraryTable, { type ScriptLibraryFederation } from './ScriptsLibraryTable'
import type { StoredScript, TestSummary } from '../types'
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
  getSortedInnerRuns: (runs: TestSummary[]) => runs,
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

  it('surfaces blocking health inputs ahead of passing validation in expanded details', async () => {
    const blockedScript = {
      ...script,
      id: 'blocked-script',
      name: 'Imported Upgrade',
      authoring_health: {
        score: 57,
        status: 'blocked',
        inputs: [
          { key: 'validation_issues', status: 'pass', count: 0, summary: 'Script validation passed.' },
          { key: 'warnings', status: 'pass', count: 0, summary: 'No validation warnings.' },
          { key: 'runtime_parity', status: 'warning', count: 8, summary: 'Runtime parity has partial, compatibility-only, or blocked settings.' },
          { key: 'data_source_fit', status: 'blocker', count: 1, summary: 'Data-source fit checks found missing columns, missing sources, or insufficient unique rows.' },
          { key: 'provenance', status: 'pass', summary: 'Source provenance is attached to the draft.' },
        ],
      },
    } as StoredScript

    await act(async () => {
      root.render(<ScriptsLibraryTable {...baseProps} scripts={[blockedScript]} expandedScriptId="blocked-script" />)
    })

    const detail = container.querySelector('.script-health-detail')
    expect(detail).not.toBeNull()
    const detailText = detail?.textContent || ''
    expect(detailText).toContain('data source fit · blocker · 1')
    expect(detailText).toContain('runtime parity · warning · 8')
    expect(detailText).toContain('Data-source fit checks found missing columns, missing sources, or insufficient unique rows.')
    expect(detailText).not.toContain('validation issues · pass')
    expect(detailText).not.toContain('Script validation passed.')

    const chipTexts = Array.from(detail?.querySelectorAll('.archive-chip') || []).map(chip => chip.textContent || '')
    expect(chipTexts[0]).toContain('data source fit · blocker · 1')
    expect(chipTexts[1]).toContain('runtime parity · warning · 8')
  })

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
    const sourceCells = Array.from(container.querySelectorAll('tbody > tr > td:first-child')).map(cell => cell.textContent?.trim())
    expect(sourceCells).toEqual(['Local', 'Remote'])

    const pushButton = findButton('Push')
    expect(pushButton).toBeDefined()
    await act(async () => { pushButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(federation.onPushScript).toHaveBeenCalledWith(script)

    const pullButton = findButton('Pull')
    expect(pullButton).toBeDefined()
    await act(async () => { pullButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(federation.onPullRemote).toHaveBeenCalledWith(remoteItem)

    const openLink = Array.from(container.querySelectorAll('a')).find(a => a.textContent?.includes('Open in Remote Workspace'))
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
