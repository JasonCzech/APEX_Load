// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { StoredScript, TestSummary } from '../types'
import { useScriptsLibraryModel } from './useScriptsLibraryModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function LibraryHarness({ scripts, api }: { scripts: StoredScript[], api: { listTests: (params?: unknown) => Promise<{ tests: TestSummary[] }> } }) {
  const model = useScriptsLibraryModel({ api: api as never, scripts, projectId: 'project-1' })

  return (
    <div>
      <span data-testid="first-script">{model.sortedScripts[0]?.name || 'none'}</span>
      <span data-testid="expanded-script">{model.expandedScriptId || 'none'}</span>
      <span data-testid="loading-runs">{model.loadingRunsFor || 'none'}</span>
      <span data-testid="run-count">{String(model.scriptRuns['script-1']?.length || 0)}</span>
      <span data-testid="first-run">{model.getSortedInnerRuns(model.scriptRuns['script-1'] || [])[0]?.name || 'none'}</span>
      <button data-testid="sort-created" onClick={() => model.onSort('created_at')}>sort-created</button>
      <button data-testid="expand-runs" onClick={() => void model.onToggleScriptRuns(scripts[0])}>expand-runs</button>
    </div>
  )
}

describe('useScriptsLibraryModel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('sorts scripts and loads cached linked runs for the selected script', async () => {
    const api = {
      listTests: vi.fn().mockResolvedValue({
        tests: [
          buildRun('run-older', 'Older Run', 'script-1', '2026-05-12T20:00:00Z'),
          buildRun('run-newer', 'Newer Run', 'script-1', '2026-05-12T20:05:00Z'),
          buildRun('run-other', 'Other Script Run', 'script-2', '2026-05-12T20:06:00Z'),
          { ...buildRun('run-no-scenario', 'Missing Scenario', 'script-1', '2026-05-12T20:07:00Z'), scenario_id: '' },
        ],
      }),
    }

    const scripts = [
      buildScript('script-1', 'Zulu Script', '2026-05-12T20:00:00Z'),
      buildScript('script-2', 'Alpha Script', '2026-05-11T20:00:00Z'),
    ]

    await act(async () => {
      root.render(<LibraryHarness scripts={scripts} api={api} />)
    })

    expect(readTestId('first-script')).toBe('Alpha Script')

    await click('sort-created')

    expect(readTestId('first-script')).toBe('Alpha Script')

    await click('expand-runs')
    await flushPromises()

    expect(api.listTests).toHaveBeenCalledTimes(1)
    expect(api.listTests).toHaveBeenCalledWith({ project_id: 'project-1' })
    expect(readTestId('expanded-script')).toBe('script-1')
    expect(readTestId('run-count')).toBe('2')
    expect(readTestId('first-run')).toBe('Newer Run')

    await click('expand-runs')
    expect(readTestId('expanded-script')).toBe('none')

    await click('expand-runs')
    await flushPromises()
    expect(api.listTests).toHaveBeenCalledTimes(1)
  })

  async function click(testId: string) {
    await act(async () => {
      container.querySelector(`[data-testid="${testId}"]`)?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  function readTestId(testId: string) {
    return container.querySelector(`[data-testid="${testId}"]`)?.textContent
  }
})

function buildScript(id: string, name: string, createdAt: string): StoredScript {
  return {
    id,
    name,
    protocol: 'http',
    version: '1.0.0',
    created_at: createdAt,
    scenario_count: 0,
    associated_releases: [],
    scenario_run_releases: [],
    last_run_at: '',
  }
}

function buildRun(id: string, name: string, scriptId: string, startTime: string): TestSummary {
  return {
    id,
    name,
    status: 'COMPLETE',
    scenario_id: 'scenario-1',
    release_id: 'release-1',
    start_time: startTime,
    created_at: startTime,
    duration: '5m',
    total_vusers: 1,
    groups: [
      {
        name: 'Group 1',
        script_id: scriptId,
        script_name: 'Script',
        protocol: 'http',
        vusers: 1,
      },
    ],
  }
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}