// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { StoredScript, TestSummary } from '../types'
import { SCRIPT_RUN_CATALOG_CACHE_TTL_MS, useScriptsLibraryModel } from './useScriptsLibraryModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

interface LibraryHarnessProps {
  scripts: StoredScript[]
  api: { listTests: (params?: unknown) => Promise<{ tests: TestSummary[] }> }
  projectId?: string
}

function LibraryHarness({ scripts, api, projectId = 'project-1' }: LibraryHarnessProps) {
  const model = useScriptsLibraryModel({ api: api as never, scripts, projectId })

  return (
    <div>
      <span data-testid="first-script">{model.sortedScripts[0]?.name || 'none'}</span>
      <span data-testid="expanded-script">{model.expandedScriptId || 'none'}</span>
      <span data-testid="loading-runs">{model.loadingRunsFor || 'none'}</span>
      <span data-testid="run-count">{String(model.scriptRuns['script-1']?.length || 0)}</span>
      <span data-testid="second-run-count">{String(model.scriptRuns['script-2']?.length || 0)}</span>
      <span data-testid="run-error">{model.scriptRunErrors['script-1'] || 'none'}</span>
      <span data-testid="first-run">{model.getSortedInnerRuns(model.scriptRuns['script-1'] || [])[0]?.name || 'none'}</span>
      <button data-testid="sort-created" onClick={() => model.onSort('created_at')}>sort-created</button>
      <button data-testid="expand-runs" onClick={() => void model.onToggleScriptRuns(scripts[0])}>expand-runs</button>
      <button data-testid="expand-second-runs" onClick={() => void model.onToggleScriptRuns(scripts[1])}>expand-second-runs</button>
      <button data-testid="retry-runs" onClick={() => void model.onRetryScriptRuns(scripts[0])}>retry-runs</button>
      <button data-testid="sort-duration" onClick={() => model.onInnerSort('duration')}>sort-duration</button>
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
    vi.useRealTimers()
  })

  it('sorts scripts and reuses the project run catalog when a script is reopened', async () => {
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
    expect(api.listTests).toHaveBeenCalledWith({ limit: 1000, offset: 0, project_id: 'project-1', summary: true })
    expect(readTestId('expanded-script')).toBe('script-1')
    expect(readTestId('run-count')).toBe('2')
    expect(readTestId('first-run')).toBe('Newer Run')

    await click('expand-runs')
    expect(readTestId('expanded-script')).toBe('none')

    await click('expand-runs')
    await flushPromises()
    expect(api.listTests).toHaveBeenCalledTimes(1)
  })

  it('drains bounded project-run pages before filtering runs for an expanded script', async () => {
    const api = {
      listTests: vi.fn(async (params?: unknown) => {
        const offset = typeof params === 'object' && params !== null && 'offset' in params
          ? Number((params as { offset?: number }).offset || 0)
          : 0
        return {
          tests: offset === 0
            ? [buildRun('run-newer', 'Newer Run', 'script-1', '2026-05-12T20:05:00Z')]
            : [buildRun('run-older', 'Older Run', 'script-1', '2026-05-12T20:00:00Z')],
          count: 1,
          total: 2,
          limit: 1000,
          offset,
        }
      }),
    }
    const scripts = [buildScript('script-1', 'Script', '2026-05-12T20:00:00Z')]

    await act(async () => {
      root.render(<LibraryHarness scripts={scripts} api={api} />)
    })
    await click('expand-runs')
    await flushPromises()

    expect(api.listTests).toHaveBeenNthCalledWith(1, { limit: 1000, offset: 0, project_id: 'project-1', summary: true })
    expect(api.listTests).toHaveBeenNthCalledWith(2, { limit: 1000, offset: 1, project_id: 'project-1', summary: true })
    expect(readTestId('run-count')).toBe('2')
    expect(readTestId('first-run')).toBe('Newer Run')
  })

  it('reuses the project run catalog when expanding a different script', async () => {
    const api = {
      listTests: vi.fn().mockResolvedValue({
        tests: [
          buildRun('run-one', 'First Script Run', 'script-1', '2026-05-12T20:00:00Z'),
          buildRun('run-two', 'Newly Completed Run', 'script-2', '2026-05-12T20:01:00Z'),
        ],
      }),
    }
    const scripts = [
      buildScript('script-1', 'First', '2026-05-12T20:00:00Z'),
      buildScript('script-2', 'Second', '2026-05-12T20:00:00Z'),
    ]
    await act(async () => root.render(<LibraryHarness scripts={scripts} api={api} />))

    await click('expand-runs')
    await flushPromises()
    await click('expand-second-runs')
    await flushPromises()

    expect(api.listTests).toHaveBeenCalledTimes(1)
    expect(readTestId('run-count')).toBe('0')
    expect(readTestId('second-run-count')).toBe('1')
  })

  it('refreshes the bounded project run cache after its TTL expires', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-05-12T20:00:00Z'))
    const api = {
      listTests: vi.fn()
        .mockResolvedValueOnce({ tests: [buildRun('run-one', 'First Script Run', 'script-1', '2026-05-12T20:00:00Z')] })
        .mockResolvedValueOnce({ tests: [buildRun('run-two', 'Second Script Run', 'script-2', '2026-05-12T20:01:00Z')] }),
    }
    const scripts = [
      buildScript('script-1', 'First', '2026-05-12T20:00:00Z'),
      buildScript('script-2', 'Second', '2026-05-12T20:00:00Z'),
    ]
    await act(async () => root.render(<LibraryHarness scripts={scripts} api={api} />))

    await click('expand-runs')
    await flushPromises()
    vi.advanceTimersByTime(SCRIPT_RUN_CATALOG_CACHE_TTL_MS + 1)
    await click('expand-second-runs')
    await flushPromises()

    expect(api.listTests).toHaveBeenCalledTimes(2)
    expect(readTestId('second-run-count')).toBe('1')
  })

  it('explicitly refreshes a successful project run cache on retry', async () => {
    const api = {
      listTests: vi.fn()
        .mockResolvedValueOnce({ tests: [buildRun('run-one', 'Original Run', 'script-1', '2026-05-12T20:00:00Z')] })
        .mockResolvedValueOnce({ tests: [buildRun('run-two', 'Refreshed Run', 'script-1', '2026-05-12T20:01:00Z')] }),
    }
    const scripts = [buildScript('script-1', 'Script', '2026-05-12T20:00:00Z')]
    await act(async () => root.render(<LibraryHarness scripts={scripts} api={api} />))

    await click('expand-runs')
    await flushPromises()
    expect(readTestId('first-run')).toBe('Original Run')

    await click('retry-runs')
    await flushPromises()

    expect(api.listTests).toHaveBeenCalledTimes(2)
    expect(readTestId('first-run')).toBe('Refreshed Run')
  })

  it('sorts run durations by parsed elapsed time rather than display text', async () => {
    const api = {
      listTests: vi.fn().mockResolvedValue({
        tests: [
          { ...buildRun('two-minutes', 'Two Minutes', 'script-1', '2026-05-12T20:00:00Z'), duration: '2m' },
          { ...buildRun('ninety-seconds', 'Ninety Seconds', 'script-1', '2026-05-12T20:01:00Z'), duration: '90s' },
        ],
      }),
    }
    const scripts = [buildScript('script-1', 'Script', '2026-05-12T20:00:00Z')]
    await act(async () => root.render(<LibraryHarness scripts={scripts} api={api} />))
    await click('expand-runs')
    await flushPromises()
    await click('sort-duration')

    expect(readTestId('first-run')).toBe('Ninety Seconds')
  })

  it('drops cached and in-flight linked runs when the active project changes', async () => {
    const projectOne = deferred<{ tests: TestSummary[] }>()
    const api = {
      listTests: vi.fn()
        .mockReturnValueOnce(projectOne.promise)
        .mockResolvedValueOnce({ tests: [buildRun('project-two-run', 'Project Two Run', 'script-1', '2026-05-12T20:05:00Z')] }),
    }
    const scripts = [buildScript('script-1', 'Script', '2026-05-12T20:00:00Z')]

    await act(async () => {
      root.render(<LibraryHarness scripts={scripts} api={api} projectId="project-1" />)
    })
    await click('expand-runs')

    await act(async () => {
      root.render(<LibraryHarness scripts={scripts} api={api} projectId="project-2" />)
    })
    await act(async () => {
      projectOne.resolve({ tests: [buildRun('project-one-run', 'Project One Run', 'script-1', '2026-05-12T20:00:00Z')] })
      await projectOne.promise
    })
    await flushPromises()

    expect(readTestId('expanded-script')).toBe('none')
    expect(readTestId('run-count')).toBe('0')

    await click('expand-runs')
    await flushPromises()

    expect(api.listTests).toHaveBeenLastCalledWith({ limit: 1000, offset: 0, project_id: 'project-2', summary: true })
    expect(readTestId('run-count')).toBe('1')
    expect(readTestId('first-run')).toBe('Project Two Run')
  })

  it('preserves a load failure as an error and retries without treating it as an empty catalog', async () => {
    const api = {
      listTests: vi.fn()
        .mockRejectedValueOnce(new Error('controller unavailable'))
        .mockResolvedValueOnce({ tests: [buildRun('retry-run', 'Recovered Run', 'script-1', '2026-05-12T20:05:00Z')] }),
    }
    const scripts = [buildScript('script-1', 'Script', '2026-05-12T20:00:00Z')]

    await act(async () => {
      root.render(<LibraryHarness scripts={scripts} api={api} />)
    })
    await click('expand-runs')
    await flushPromises()

    expect(readTestId('run-error')).toBe('controller unavailable')
    expect(readTestId('run-count')).toBe('0')

    await click('retry-runs')
    await flushPromises()

    expect(api.listTests).toHaveBeenCalledTimes(2)
    expect(readTestId('run-error')).toBe('none')
    expect(readTestId('run-count')).toBe('1')
    expect(readTestId('first-run')).toBe('Recovered Run')
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

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(next => {
    resolve = next
  })
  return { promise, resolve }
}
