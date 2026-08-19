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

  async function waitForIssueDialog() {
    for (let attempt = 0; attempt < 50; attempt += 1) {
      const dialog = document.body.querySelector<HTMLElement>('[role="dialog"]')
      if (dialog) return dialog
      await act(async () => { await new Promise(resolve => window.setTimeout(resolve, 0)) })
    }
    return document.body.querySelector<HTMLElement>('[role="dialog"]')
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

    const blockerButton = findButton('1 blocker')
    expect(blockerButton?.getAttribute('aria-label')).toContain('Imported Upgrade')
    await act(async () => { blockerButton?.click() })
    expect((await waitForIssueDialog())?.textContent).toContain('Data-source fit checks found missing columns')

    await act(async () => {
      ;(document.body.querySelector('[role="dialog"] button') as HTMLButtonElement).click()
    })
    const warningButton = findButton('1 warning')
    await act(async () => { warningButton?.click() })
    expect((await waitForIssueDialog())?.textContent).toContain('Runtime parity has partial')
  })

  it('shows linked-run load failures with Retry instead of an empty-catalog message', async () => {
    const onRetryScriptRuns = vi.fn()
    await act(async () => {
      root.render(
        <ScriptsLibraryTable
          {...baseProps}
          expandedScriptId="local-script"
          scriptRunErrors={{ 'local-script': 'Controller unavailable' }}
          onRetryScriptRuns={onRetryScriptRuns}
        />,
      )
    })

    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Controller unavailable')
    expect(container.textContent).not.toContain('No linked scenario runs for this script yet.')

    await act(async () => { findButton('Retry')?.click() })
    expect(onRetryScriptRuns).toHaveBeenCalledWith(script)
  })

  it('loads exact validation failures from a library blocker', async () => {
    const blockedScript = {
      ...script,
      id: 'blocked-script',
      name: 'Imported Parameters',
      authoring_health: {
        score: 47,
        status: 'blocked',
        inputs: [
          {
            key: 'validation_issues',
            status: 'blocker',
            count: 1,
            summary: 'Script has validation issues that block save or replay.',
          },
        ],
      },
    } as StoredScript
    const onValidateScriptDetails = vi.fn().mockResolvedValue({
      script_id: blockedScript.id,
      valid: false,
      issues: ['data_sources[0] "merged_parameters": file asset path must be absolute'],
    })

    await act(async () => {
      root.render(
        <ScriptsLibraryTable
          {...baseProps}
          scripts={[blockedScript]}
          onValidateScriptDetails={onValidateScriptDetails}
        />,
      )
    })

    await act(async () => { findButton('1 blocker')?.click() })
    expect(onValidateScriptDetails).toHaveBeenCalledWith(blockedScript.id)
    const dialogText = (await waitForIssueDialog())?.textContent || ''
    expect(dialogText).toContain('Validation issue 1')
    expect(dialogText).toContain('file asset path must be absolute')
    expect(dialogText).not.toContain('Script has validation issues that block save or replay.')
  })

  it('surfaces missing project secrets and links to configuration', async () => {
    const onConfigureSecrets = vi.fn()
    await act(async () => {
      root.render(
        <ScriptsLibraryTable
          {...baseProps}
          secretStatusByScript={{
            [script.id]: {
              script_id: script.id,
              project_id: 'project-1',
              required: ['ORDER_API_TOKEN'],
              configured: [],
              missing: ['ORDER_API_TOKEN'],
              secrets: [{ name: 'ORDER_API_TOKEN', configured: false }],
              capabilities: {
                provider: 'database',
                writable: true,
                create_supported: true,
                replace_supported: true,
                source_secret_migration_policy_enabled: false,
              },
            },
          }}
          onConfigureSecrets={onConfigureSecrets}
        />,
      )
    })

    expect(container.textContent).toContain('1 secret missing')
    await act(async () => findButton('1 secret missing')?.click())
    expect(onConfigureSecrets).toHaveBeenCalledWith(script)
    const configure = findButton('Configure secrets')
    await act(async () => configure?.click())
    expect(onConfigureSecrets).toHaveBeenCalledTimes(2)
  })

  it('keeps secret binding configuration available after every value is configured', async () => {
    const onConfigureSecrets = vi.fn()
    const secretBackedScript: StoredScript = {
      ...script,
      script: {
        name: script.name,
        version: script.version,
        protocol: 'http',
        config: { base_url: 'https://api.example.test' },
        actions: [{ name: 'Call API', type: 'request', target: '/', headers: { Authorization: 'Bearer {{secret.ORDER_API_TOKEN}}' } }],
      },
    }
    await act(async () => {
      root.render(
        <ScriptsLibraryTable
          {...baseProps}
          scripts={[secretBackedScript]}
          secretStatusByScript={{
            [script.id]: {
              script_id: script.id,
              project_id: 'project-1',
              required: ['ORDER_API_TOKEN'],
              configured: ['ORDER_API_TOKEN'],
              missing: [],
              secrets: [{ name: 'ORDER_API_TOKEN', configured: true, provider: 'database' }],
              capabilities: {
                provider: 'database',
                writable: true,
                create_supported: true,
                replace_supported: true,
                source_secret_migration_policy_enabled: false,
              },
            },
          }}
          secretStatusStateByScript={{ [script.id]: 'ready' }}
          onConfigureSecrets={onConfigureSecrets}
        />,
      )
    })

    await act(async () => findButton('Configure secrets')?.click())
    expect(onConfigureSecrets).toHaveBeenCalledWith(secretBackedScript)
  })

  it('never presents a secret-backed script as healthy when credential status is unavailable', async () => {
    const secretBackedScript: StoredScript = {
      ...script,
      script: {
        name: script.name,
        version: script.version,
        protocol: 'http',
        config: { base_url: 'https://api.example.test' },
        actions: [{ name: 'Call API', type: 'request', target: '/', headers: { Authorization: 'Bearer {{secret.ORDER_API_TOKEN}}' } }],
      },
      authoring_health: { score: 100, status: 'ready', inputs: [] },
    }
    const onConfigureSecrets = vi.fn()
    const onRetrySecretStatus = vi.fn()
    await act(async () => {
      root.render(
        <ScriptsLibraryTable
          {...baseProps}
          scripts={[secretBackedScript]}
          secretStatusStateByScript={{ [script.id]: 'error' }}
          onConfigureSecrets={onConfigureSecrets}
          onRetrySecretStatus={onRetrySecretStatus}
        />,
      )
    })

    expect(container.textContent).toContain('Secret status unavailable')
    expect(container.textContent).toContain('secret status unknown')
    expect(container.textContent).not.toContain('ready')
    await act(async () => findButton('Configure secrets')?.click())
    await act(async () => findButton('Retry secret check')?.click())
    expect(onConfigureSecrets).toHaveBeenCalledWith(secretBackedScript)
    expect(onRetrySecretStatus).toHaveBeenCalledWith(secretBackedScript)
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

  it('supports keyboard sorting and exposes the active sort direction', async () => {
    const onSort = vi.fn()
    await act(async () => {
      root.render(<ScriptsLibraryTable {...baseProps} onSort={onSort} />)
    })

    const scriptHeader = Array.from(container.querySelectorAll<HTMLTableCellElement>('th'))
      .find(header => header.textContent?.includes('Script'))
    const healthHeader = Array.from(container.querySelectorAll<HTMLTableCellElement>('th'))
      .find(header => header.textContent?.includes('Health'))
    expect(scriptHeader?.getAttribute('aria-sort')).toBe('ascending')
    expect(healthHeader?.getAttribute('aria-sort')).toBe('none')
    const healthSortButton = healthHeader?.querySelector('button')
    expect(healthSortButton).toBeInstanceOf(HTMLButtonElement)

    await act(async () => {
      healthSortButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onSort).toHaveBeenCalledWith('authoring_health')
  })

  it('expands script runs from the keyboard', async () => {
    const onToggleScriptRuns = vi.fn()
    await act(async () => {
      root.render(<ScriptsLibraryTable {...baseProps} onToggleScriptRuns={onToggleScriptRuns} />)
    })

    const toggle = container.querySelector<HTMLButtonElement>('.script-row-toggle')
    expect(toggle?.getAttribute('aria-expanded')).toBe('false')
    expect(toggle?.textContent).toContain('Local Checkout')

    await act(async () => {
      toggle?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onToggleScriptRuns).toHaveBeenCalledWith(script)
  })

  it('offers a create action when the script library is empty', async () => {
    const onCreate = vi.fn()
    await act(async () => {
      root.render(<ScriptsLibraryTable {...baseProps} scripts={[]} onCreate={onCreate} />)
    })

    const createButton = findButton('Create your first script')
    expect(createButton).toBeDefined()
    await act(async () => { createButton?.click() })
    expect(onCreate).toHaveBeenCalledTimes(1)
  })
})
