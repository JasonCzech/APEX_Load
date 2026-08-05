// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CommandCenterPrototype, {
  buildSelectedGroupChartHistoryFromTransactions,
  historicalGroupSummaryQuery,
  selectedGroupLoadImpactData,
} from './CommandCenterPrototype'
import { buildLoadModelProfile, plannedGroupLoadAt } from './commandCenterLoadModel'
import { buildScenarioRunRows, type CommandGroup } from './commandCenterScenarioRows'
import { liveEvidenceCacheKey } from './commandCenterEvidenceModel'
import { EXECUTION_ARCHIVE_FINALIZATION_TIMEOUT_MS } from './useExecutionTestPolling'
import type { MetricsHistoryPoint, TestSummary, TransactionEvent, TxMetrics } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('CommandCenterPrototype', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.localStorage.clear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    vi.restoreAllMocks()
    vi.useRealTimers()
    container.remove()
  })

  it('requests one bounded sampled series for historical group charts', () => {
    expect(historicalGroupSummaryQuery(['Checkout', 'Search'])).toEqual({
      sample_limit: 5000,
      groups: ['Checkout', 'Search'],
    })
  })

  it('qualifies live evidence cache entries by run and signature', () => {
    expect(liveEvidenceCacheKey('run-a', 'HTTP 500')).not.toBe(liveEvidenceCacheKey('run-b', 'HTTP 500'))
  })

  it('starts an authored first-stage ramp-down at the group peak', () => {
    const group = commandGroup('Checkout', 100)
    const testGroup = {
      name: 'Checkout',
      script_id: 'script-1',
      vusers: 100,
      role: 'load' as const,
      stages: [{ type: 'ramp_down', duration: '1m', target_vusers: 20 }],
    } as TestSummary['groups'][number]

    expect(buildLoadModelProfile([testGroup], [group], 60).map(point => point.planned)).toEqual([100, 20])
  })

  it('treats authored hold stage casing consistently', () => {
    const testGroup = {
      name: 'Checkout',
      script_id: 'script-1',
      vusers: 100,
      role: 'load' as const,
      stages: [
        { type: 'ramp_up', duration: '1m', target_vusers: 100 },
        { type: 'HOLD', duration: '1m', target_vusers: 60 },
      ],
    } as TestSummary['groups'][number]

    expect(plannedGroupLoadAt(testGroup, 100, 90, 120)).toBe(60)
  })

  it('keeps unfiltered graph data when selected group fields are absent', () => {
    const data: MetricsHistoryPoint[] = [{
      time: '12:00',
      timestampMs: Date.parse('2026-06-08T12:00:00Z'),
      vusers: 20,
      tps: 10,
      avgMs: 150,
      errorPct: 1,
      p50: 100,
      p90: 200,
      p95: 250,
      p99: 300,
    }]
    const selectedGroups = [commandGroup('Wikipedia', 10)]

    expect(selectedGroupLoadImpactData(data, selectedGroups)).toEqual(data)
  })

  it('does not dilute aggregate percentiles when a selected group omits one', () => {
    const data: MetricsHistoryPoint[] = [{
      time: '12:00',
      timestampMs: Date.parse('2026-06-08T12:00:00Z'),
      vusers: 20,
      tps: 20,
      avgMs: 150,
      errorPct: 0,
      p50: 0,
      p90: 0,
      p95: 0,
      p99: 0,
      Alpha_transactions: 100,
      Alpha_errors: 0,
      Alpha_tps: 10,
      Alpha_p50: 100,
      Alpha_p90: 400,
      Alpha_p95: 500,
      Alpha_p99: 600,
      Beta_transactions: 100,
      Beta_errors: 0,
      Beta_tps: 10,
      Beta_p50: 120,
      Beta_p90: 420,
      // Beta omits p95 (server dropped it via omitempty); p99 is present.
      Beta_p99: 620,
    }]
    const selectedGroups = [commandGroup('Alpha', 10), commandGroup('Beta', 10)]

    const [aggregated] = selectedGroupLoadImpactData(data, selectedGroups)

    // p95 is weighted only by Alpha (the group that reported it) -> 500, not
    // diluted toward 0 by Beta's equal attempts (which would yield 250).
    expect(aggregated.p95).toBe(500)
    // p50/p99 are reported by both groups and weight across both equally.
    expect(aggregated.p50).toBe(110)
    expect(aggregated.p99).toBe(610)
  })

  it('rebuilds selected historical group chart history from archive transactions', () => {
    const referenceData: MetricsHistoryPoint[] = [{
      time: '12:00:01',
      timestampMs: Date.parse('2026-06-08T12:00:01Z'),
      vusers: 2,
      tps: 4,
      avgMs: 100,
      errorPct: 0,
      p50: 100,
      p90: 100,
      p95: 100,
      p99: 100,
    }, {
      time: '12:00:02',
      timestampMs: Date.parse('2026-06-08T12:00:02Z'),
      vusers: 4,
      tps: 6,
      avgMs: 200,
      errorPct: 0,
      p50: 200,
      p90: 200,
      p95: 200,
      p99: 200,
    }]
    const allGroups = [commandGroup('Wikipedia', 10), commandGroup('BBC', 10)]
    const transactions: TransactionEvent[] = [
      archiveTransaction('2026-06-08T12:00:00.300Z', 'Wikipedia', 100, true),
      archiveTransaction('2026-06-08T12:00:00.400Z', 'Wikipedia', 300, false),
      archiveTransaction('2026-06-08T12:00:01.300Z', 'Wikipedia', 200, true),
      archiveTransaction('2026-06-08T12:00:01.400Z', 'BBC', 1000, true),
    ]

    const scoped = buildSelectedGroupChartHistoryFromTransactions(referenceData, transactions, [allGroups[0]], allGroups)

    expect(scoped).toHaveLength(2)
    expect(scoped[0]).toEqual(expect.objectContaining({
      time: '12:00:01',
      vusers: 1,
      tps: 2,
      avgMs: 200,
      errorPct: 50,
      p50: 100,
      p90: 300,
      p95: 300,
      p99: 300,
      totalTransactions: 2,
      totalErrors: 1,
      Wikipedia_vusers: 1,
      Wikipedia_tps: 2,
      Wikipedia_avgMs: 200,
      Wikipedia_transactions: 2,
      Wikipedia_errors: 1,
      Wikipedia_errorPct: 50,
      Wikipedia_p95: 300,
    }))
    expect(scoped[1]).toEqual(expect.objectContaining({
      time: '12:00:02',
      vusers: 2,
      tps: 1,
      avgMs: 200,
      errorPct: 0,
      p50: 200,
      p90: 200,
      p95: 200,
      p99: 200,
      totalTransactions: 3,
      totalErrors: 1,
      Wikipedia_vusers: 2,
      Wikipedia_tps: 1,
      Wikipedia_avgMs: 200,
      Wikipedia_transactions: 1,
      Wikipedia_errors: 0,
    }))
  })

  it('shows the empty state (not the prototype run) when discovery finds no live runs', async () => {
    const openResults = vi.fn()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ tests: [], count: 0 }))

    await act(async () => {
      root.render(<CommandCenterPrototype projectId="project-1" onOpenResults={openResults} />)
    })
    await flush()

    expect(container.textContent).toContain('No active run selected')
    expect(container.textContent).toContain('Historical results can be opened from the Results folder.')
    expect(container.textContent).toContain('Open Results')
    expect(container.textContent).not.toContain('Checkout Peak Soak - Command Center Prototype')
    expect(container.querySelector('[aria-label="Command center run controls"]')).toBeNull()
    expect(container.querySelector('[aria-label="No active command center run"]')).not.toBeNull()

    await click(buttonNamed('Open Results'))

    expect(openResults).toHaveBeenCalledOnce()
  })

  it('auto-selects the only live run when opened without a testId', async () => {
    const liveRun = buildLiveVuserTest()
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = requestUrl(input)
      if (url.includes('/tests/live-vuser-1/')) return Promise.resolve(jsonResponse({}))
      if (url.includes('/tests/live-vuser-1')) return Promise.resolve(jsonResponse(liveRun))
      if (url.includes('/tests')) return Promise.resolve(jsonResponse({ tests: [liveRun], count: 1 }))
      return Promise.resolve(jsonResponse({}))
    })

    await act(async () => {
      root.render(<CommandCenterPrototype projectId="project-1" />)
    })
    await flush()

    expect(container.querySelector('[aria-label="No active command center run"]')).toBeNull()
    expect(container.textContent).toContain('Live VUser Checkout')
    expect(container.querySelector('[aria-label="Command center run controls"]')).not.toBeNull()
    const discoveryURL = fetchMock.mock.calls
      .map(([input]) => requestUrl(input))
      .find(url => url.includes('/tests?'))
    expect(discoveryURL).toContain('project_id=project-1')
    expect(discoveryURL).toContain('summary=true')
    expect(discoveryURL).toContain('active_only=true')
    expect(discoveryURL).toContain('limit=200')
    expect(discoveryURL).toContain('offset=0')
  })

  it('shows alerts for a run discovered after opening the unscoped command center', async () => {
    const liveRun = buildLiveVuserTest()
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = requestUrl(input)
      if (url.includes('/tests/live-vuser-1/')) return Promise.resolve(jsonResponse({}))
      if (url.includes('/tests/live-vuser-1')) return Promise.resolve(jsonResponse(liveRun))
      if (url.includes('/tests')) return Promise.resolve(jsonResponse({ tests: [liveRun], count: 1 }))
      return Promise.resolve(jsonResponse({}))
    })
    const baseAlert = {
      metric: 'p99_ms', value: 900, threshold: 500,
      firstSeenAt: '2026-08-04T12:00:00Z', lastSeenAt: '2026-08-04T12:00:00Z',
      acknowledged: false, active: true,
    }

    await act(async () => {
      root.render(<CommandCenterPrototype
        projectId="project-1"
        thresholdAlerts={[
          { ...baseAlert, key: 'live:p99', testId: 'live-vuser-1' },
          { ...baseAlert, key: 'other:p99', testId: 'other-run', value: 1200 },
        ]}
      />)
    })
    await flush()

    expect(container.textContent).toContain('P99 latency 900 ms exceeded threshold 500 ms')
    expect(container.textContent).not.toContain('P99 latency 1200 ms')
  })

  it('opens the Test Notes dialog from the run controls', async () => {
    const liveRun = buildLiveVuserTest()
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = requestUrl(input)
      if (url.includes('/tests/live-vuser-1/notes')) {
        return Promise.resolve(jsonResponse({ test_id: 'live-vuser-1', notes: [] }))
      }
      if (url.includes('/tests/live-vuser-1/')) return Promise.resolve(jsonResponse({}))
      if (url.includes('/tests/live-vuser-1')) return Promise.resolve(jsonResponse(liveRun))
      return Promise.resolve(jsonResponse({}))
    })

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" />)
    })
    await flush()

    await click(buttonNamed('Test Notes'))
    await flush()

    expect(container.querySelector('.test-notes-dialog')).not.toBeNull()
    expect(container.textContent).toContain('No notes yet')
  })

  it('shows a picker when several live runs exist and opens the chosen one', async () => {
    const runA: TestSummary = { ...buildLiveVuserTest(), id: 'run-a', name: 'Checkout Soak' }
    const runB: TestSummary = { ...buildLiveVuserTest(), id: 'run-b', name: 'Search Spike' }
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = requestUrl(input)
      if (url.includes('/tests/run-a/')) return Promise.resolve(jsonResponse({}))
      if (url.includes('/tests/run-a')) return Promise.resolve(jsonResponse(runA))
      if (url.includes('/tests')) return Promise.resolve(jsonResponse({ tests: [runA, runB], count: 2 }))
      return Promise.resolve(jsonResponse({}))
    })

    await act(async () => {
      root.render(<CommandCenterPrototype projectId="project-1" />)
    })
    await flush()

    // Multiple live runs => no guessing; a picker is shown.
    expect(container.textContent).toContain('Select an active run')
    expect(container.textContent).toContain('2 active runs are in progress')
    expect(container.textContent).toContain('Checkout Soak')
    expect(container.textContent).toContain('Search Spike')
    expect(container.querySelector('[aria-label="No active command center run"]')).not.toBeNull()

    await click(buttonNamed('Checkout Soak'))
    await flush()

    expect(container.querySelector('[aria-label="No active command center run"]')).toBeNull()
    expect(container.textContent).toContain('Checkout Soak')
  })

  it('discovers queued and reserved runs so operators can start or abort them', async () => {
    const queued: TestSummary = { ...buildLiveVuserTest(), id: 'queued-run', name: 'Queued Checkout', status: 'QUEUED' }
    const reserved: TestSummary = { ...buildLiveVuserTest(), id: 'reserved-run', name: 'Reserved Checkout', status: 'RESERVED' }
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = requestUrl(input)
      if (url.includes('/tests')) return Promise.resolve(jsonResponse({ tests: [queued, reserved], count: 2 }))
      return Promise.resolve(jsonResponse({}))
    })

    await act(async () => {
      root.render(<CommandCenterPrototype projectId="project-1" />)
    })
    await flush()

    expect(container.textContent).toContain('Queued Checkout')
    expect(container.textContent).toContain('Reserved Checkout')
  })

  it('does not auto-discover runs until a project scope is available', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ tests: [], count: 0 }))

    await act(async () => {
      root.render(<CommandCenterPrototype />)
    })
    await flush()

    expect(fetchMock).not.toHaveBeenCalled()
    expect(container.textContent).toContain('No active run selected')
  })

  it('ignores discovery results from the previous project scope', async () => {
    const runA: TestSummary = { ...buildLiveVuserTest(), id: 'run-a', name: 'Project A Run' }
    const runB: TestSummary = { ...buildLiveVuserTest(), id: 'run-b', name: 'Project B Run' }
    let resolveProjectA: ((response: Response) => void) | undefined
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = requestUrl(input)
      if (url.includes('/tests/run-b')) return Promise.resolve(jsonResponse(runB))
      if (url.includes('project_id=project-a')) {
        return new Promise(resolve => { resolveProjectA = resolve })
      }
      if (url.includes('project_id=project-b')) {
        return Promise.resolve(jsonResponse({ tests: [runB], count: 1 }))
      }
      return Promise.resolve(jsonResponse({}))
    })

    await act(async () => {
      root.render(<CommandCenterPrototype projectId="project-a" />)
      await Promise.resolve()
    })
    await act(async () => {
      root.render(<CommandCenterPrototype projectId="project-b" />)
    })
    await flush()
    expect(container.textContent).toContain('Project B Run')

    await act(async () => {
      resolveProjectA?.(jsonResponse({ tests: [runA], count: 1 }))
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.textContent).toContain('Project B Run')
    expect(container.textContent).not.toContain('Project A Run')
  })

  it('returns to discovery when an auto-selected run becomes terminal', async () => {
    const runA: TestSummary = { ...buildLiveVuserTest(), id: 'run-a', name: 'Finishing Run' }
    const runB: TestSummary = { ...buildLiveVuserTest(), id: 'run-b', name: 'Replacement Run' }
    let discoveryCalls = 0
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = requestUrl(input)
      if (url.includes('/tests/run-a')) return Promise.resolve(jsonResponse({ ...runA, status: 'COMPLETE' }))
      if (url.includes('/tests/run-b')) return Promise.resolve(jsonResponse(runB))
      if (url.includes('/tests')) {
        discoveryCalls += 1
        return Promise.resolve(jsonResponse({ tests: [discoveryCalls === 1 ? runA : runB], count: 1 }))
      }
      return Promise.resolve(jsonResponse({}))
    })

    await act(async () => {
      root.render(<CommandCenterPrototype projectId="project-1" />)
    })
    await flush(12)

    expect(discoveryCalls).toBeGreaterThanOrEqual(2)
    expect(container.textContent).toContain('Replacement Run')
    expect(container.textContent).not.toContain('Finishing Run')
  })

  it('continues polling an explicitly routed terminal run until its archive appears', async () => {
    vi.useFakeTimers()
    const onOpenArchive = vi.fn()
    const terminalRun: TestSummary = {
      id: 'late-archive',
      name: 'Late Archive Run',
      status: 'FAILED',
      total_vusers: 5,
      duration: '1m',
      created_at: '2026-06-08T13:00:00Z',
      ended_at: '2026-06-08T13:01:00Z',
      groups: [{
        name: 'Checkout',
        protocol: 'http',
        script_id: 'script-1',
        script_name: 'Checkout',
        vusers: 5,
      }],
    }
    let testPollCalls = 0
    let archiveReady = false
    vi.spyOn(globalThis, 'fetch').mockImplementation(async input => {
      const url = new URL(requestUrl(input), 'http://localhost')
      if (url.pathname === '/api/v1/tests/late-archive') {
        testPollCalls += 1
        return jsonResponse(!archiveReady ? terminalRun : {
          ...terminalRun,
          archive: {
            test_id: 'late-archive',
            schema_version: '1.0',
            archive_path: '/archives/late-archive',
            manifest_path: '/archives/late-archive/manifest.json',
            transaction_count: 0,
            resource_sample_count: 0,
            interval_summary_count: 0,
            created_at: '2026-06-08T13:01:00Z',
            completed_at: '2026-06-08T13:01:02Z',
          },
        })
      }
      return jsonResponse({})
    })

    await act(async () => {
      root.render(<CommandCenterPrototype testId="late-archive" onOpenArchive={onOpenArchive} />)
      await Promise.resolve()
      await Promise.resolve()
    })
    await flush()

    const initialTestPollCalls = testPollCalls
    expect(initialTestPollCalls).toBeGreaterThanOrEqual(1)
    expect(container.textContent).not.toContain('Open Results Archive')

    archiveReady = true
    await act(async () => {
      vi.advanceTimersByTime(2000)
      await Promise.resolve()
      await Promise.resolve()
    })
    await flush()

    expect(testPollCalls).toBeGreaterThan(initialTestPollCalls)
    expect(container.textContent).toContain('Open Results Archive')

    const completedTestPollCalls = testPollCalls

    await act(async () => {
      vi.advanceTimersByTime(4000)
      await Promise.resolve()
    })
    expect(testPollCalls).toBe(completedTestPollCalls)
  })

  it('offers an in-place archive check after terminal polling times out', async () => {
    vi.useFakeTimers()
    const terminalRun: TestSummary = {
      id: 'late-archive-retry',
      name: 'Late Archive Retry Run',
      status: 'FAILED',
      total_vusers: 5,
      duration: '1m',
      created_at: '2026-06-08T13:00:00Z',
      ended_at: '2026-06-08T13:01:00Z',
      groups: [],
    }
    let archiveReady = false
    let testPollCalls = 0
    vi.spyOn(globalThis, 'fetch').mockImplementation(async input => {
      const url = new URL(requestUrl(input), 'http://localhost')
      if (url.pathname === '/api/v1/tests/late-archive-retry') {
        testPollCalls += 1
        return jsonResponse(archiveReady ? {
          ...terminalRun,
          archive: {
            test_id: terminalRun.id,
            schema_version: '1.0',
            archive_path: '/archives/late-archive-retry',
            manifest_path: '/archives/late-archive-retry/manifest.json',
            transaction_count: 0,
            resource_sample_count: 0,
            interval_summary_count: 0,
            created_at: '2026-06-08T13:01:00Z',
            completed_at: '2026-06-08T13:03:01Z',
          },
        } : terminalRun)
      }
      return jsonResponse({})
    })

    await act(async () => {
      root.render(<CommandCenterPrototype testId={terminalRun.id} onOpenArchive={vi.fn()} />)
    })
    await flush()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(EXECUTION_ARCHIVE_FINALIZATION_TIMEOUT_MS)
    })

    expect(container.textContent).toContain('Archive publication is taking longer than expected')
    const callsAtTimeout = testPollCalls
    archiveReady = true
    const checkAgain = buttonNamed('Check again')
    await act(async () => {
      checkAgain.click()
      checkAgain.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    await flush()

    expect(testPollCalls).toBe(callsAtTimeout + 1)
    expect(container.textContent).toContain('Open Results Archive')
    expect(container.textContent).not.toContain('Check again')
  })

  it('switches between the OPS View and Graph Wall variants from the sidebar', async () => {
    await renderPrototype()

    expect(container.textContent).toContain('OPS View')
    expect(container.textContent).toContain('Graph Wall')
    expect(container.querySelector('[aria-label="Ops cockpit mockup"]')).not.toBeNull()
    expect(container.textContent).not.toContain('Triage Console')
    expect(container.querySelector('.command-center-intro')).toBeNull()
    expect(container.querySelector('[aria-label="Command center run controls"]')?.classList.contains('command-top-banner')).toBe(false)

    const opsSidebar = container.querySelector('[aria-label="Command center sidebar"]')
    const opsPrimaryColumn = container.querySelector('.command-layout-ops > .command-primary-column')
    const opsSwitchPanel = opsSidebar?.querySelector('.command-view-switch-panel')
    const opsCentralGroupPanel = opsPrimaryColumn?.querySelector('.command-group-panel')
    const opsLoadModelRow = opsPrimaryColumn?.querySelector('.command-load-model-row')
    const opsLoadModelPanel = opsLoadModelRow?.querySelector('[aria-label="Load model profile"]')

    expect(opsSwitchPanel).not.toBeNull()
    expect(opsSidebar?.querySelector('.command-group-panel')).toBeNull()
    expect(opsPrimaryColumn).not.toBeNull()
    expect(opsCentralGroupPanel).not.toBeNull()
    expect(opsCentralGroupPanel?.classList.contains('paired')).toBe(true)
    expect(opsLoadModelRow).not.toBeNull()
    expect(opsLoadModelPanel).not.toBeNull()
    expect(opsPrimaryColumn?.querySelector('[aria-label="Command center run tables"]')).not.toBeNull()
    expect(opsPrimaryColumn?.querySelector('.command-runtime-graphs')).toBeNull()
    expect(opsPrimaryColumn?.textContent).toContain('Scenario Run')
    expect(opsPrimaryColumn?.textContent).toContain('Error Details')
    expect(opsPrimaryColumn?.textContent).toContain('Infrastructure')
    expect(opsLoadModelRow?.querySelector('.command-group-panel')).toBe(opsCentralGroupPanel)
    expect(opsLoadModelPanel?.textContent).toContain('Load Model')
    expect(opsLoadModelPanel?.textContent).toContain('elapsed')
    expect(opsLoadModelPanel?.getAttribute('data-progress-pct')).toBe('38')
    expect(opsLoadModelPanel?.getAttribute('data-target-vusers')).toBe('2140')
    expect(Array.from(opsPrimaryColumn!.children).indexOf(opsLoadModelRow!)).toBeGreaterThan(0)

    await click(buttonNamed('Graph Wall'))

    expect(container.querySelector('[aria-label="Grafana wall mockup"]')).not.toBeNull()
    expect(container.querySelector('[aria-label="Group filters"]')).toBeNull()
    expect(container.textContent).not.toContain('Graph-heavy view')

    const graphSidebar = container.querySelector('.command-grafana-side')
    const graphSwitchPanel = graphSidebar?.querySelector('.command-view-switch-panel')
    const graphSelectionPanel = graphSidebar?.querySelector('[aria-label="Group selection"]')
    const graphErrorPanel = graphSidebar?.querySelector('[aria-label="Error drill-down"]')
    const graphOptionsSlot = graphSidebar?.querySelector('.command-graph-options-slot')
    const graphOptionsPanel = graphOptionsSlot?.querySelector('[aria-label="Graph display options"]')

    expect(graphSwitchPanel).not.toBeNull()
    expect(graphSidebar?.querySelector('.command-group-panel')).toBeNull()
    expect(graphSelectionPanel).not.toBeNull()
    expect(graphSelectionPanel?.textContent).toContain('Group Selection')
    expect(graphSelectionPanel?.textContent).toContain('3 / 3 groups visible')
    const selectedGroupButtons = graphSelectionPanel?.querySelectorAll('.command-group-select-toggle[data-state="selected"]')
    expect(selectedGroupButtons).toHaveLength(3)
    expect(Array.from(selectedGroupButtons ?? []).every(button => button.textContent?.includes('Selected'))).toBe(true)
    expect(selectedGroupButtons?.[0].querySelector('.command-group-select-status-dot')).not.toBeNull()
    expect(Array.from(graphSidebar!.children).indexOf(graphSwitchPanel!)).toBeLessThan(Array.from(graphSidebar!.children).indexOf(graphSelectionPanel!))
    expect(graphOptionsPanel).not.toBeNull()
    expect(graphOptionsPanel?.textContent).toContain('Graph Options')
    expect(graphOptionsPanel?.textContent).toContain('Display Window')
    expect(graphOptionsPanel?.textContent).toContain('Layout')
    expect(graphOptionsPanel?.textContent).toContain('2x2')
    expect(graphOptionsPanel?.textContent).toContain('3x3')
    expect(graphOptionsPanel?.textContent).not.toContain('Role View')
    expect(graphOptionsPanel?.textContent).toContain('Display Time')
    expect(graphOptionsPanel?.textContent).not.toContain('Graph data range')
    expect(graphOptionsPanel?.textContent).not.toContain('Graph density')
    expect(graphOptionsPanel?.textContent).not.toContain('Graph preset')
    expect(graphOptionsPanel?.textContent).not.toContain('Graph x-axis labels')
    expect(Array.from(graphSidebar!.children).indexOf(graphOptionsSlot!)).toBeGreaterThan(Array.from(graphSidebar!.children).indexOf(graphErrorPanel!))

    const runtimeGraphs = container.querySelector('.command-runtime-graphs')
    expect(runtimeGraphs?.classList.contains('command-runtime-graphs-grafana')).toBe(true)
    expect(runtimeGraphs?.querySelector('.charts-toolbar-title-row')?.textContent).toContain('Runtime Graphs')
    expect(runtimeGraphs?.querySelector('.charts-toolbar-title-row')?.textContent).toContain('Configure Graphs')
    expect(runtimeGraphs?.querySelector('[role="tablist"][aria-label="Graph groups"]')).not.toBeNull()
    expect(tabNamed('Live Metrics')?.getAttribute('aria-selected')).toBe('true')
    expect(runtimeGraphs?.textContent).toContain('Transactions Per Second')
    expect(runtimeGraphs?.textContent).not.toContain('CPU Usage — Fleet (%)')

    await click(tabNamed('Under Load'))

    expect(tabNamed('Under Load')?.getAttribute('aria-selected')).toBe('true')
    const underLoadDisplayPanel = graphSidebar?.querySelector('[aria-label="Under Load display mode"]')
    expect(underLoadDisplayPanel).not.toBeNull()
    expect(underLoadDisplayPanel?.textContent).toContain('Graphs')
    expect(underLoadDisplayPanel?.textContent).toContain('Heatmap')
    expect(Array.from(graphSidebar!.children).indexOf(underLoadDisplayPanel!)).toBeGreaterThan(Array.from(graphSidebar!.children).indexOf(graphSwitchPanel!))
    expect(Array.from(graphSidebar!.children).indexOf(underLoadDisplayPanel!)).toBeLessThan(Array.from(graphSidebar!.children).indexOf(graphSelectionPanel!))
    expect(buttonWithin(underLoadDisplayPanel!, 'Graphs').getAttribute('aria-pressed')).toBe('true')
    expect(runtimeGraphs?.textContent).toContain('ART Under Load')
    expect(runtimeGraphs?.textContent).toContain('TPS Under Load')
    expect(runtimeGraphs?.textContent).not.toContain('Load Impact Heatmap')

    await click(buttonWithin(underLoadDisplayPanel!, 'Heatmap'))

    expect(buttonWithin(underLoadDisplayPanel!, 'Heatmap').getAttribute('aria-pressed')).toBe('true')
    expect(runtimeGraphs?.textContent).toContain('Load Impact Heatmap')
    expect(runtimeGraphs?.textContent).toContain('2D')
    expect(runtimeGraphs?.textContent).toContain('3D')
    expect(runtimeGraphs?.textContent).not.toContain('ART Under Load')
    const dedicatedHeatmapCard = runtimeGraphs?.querySelector('.load-impact-card.load-impact-map-mode')
    expect(dedicatedHeatmapCard).not.toBeNull()
    const heatmapCell = Array.from(dedicatedHeatmapCard!.querySelectorAll<HTMLButtonElement>('.load-impact-cell')).find(cell => !cell.disabled)
    expect(heatmapCell).toBeDefined()

    await click(heatmapCell!)

    expect(runtimeGraphs?.querySelector('.load-impact-detail')).not.toBeNull()

    await click(tabNamed('Infrastructure'))

    expect(tabNamed('Infrastructure')?.getAttribute('aria-selected')).toBe('true')
    expect(runtimeGraphs?.textContent).toContain('CPU Usage — Fleet (%)')
    expect(runtimeGraphs?.textContent).toContain('Memory — Fleet total (MB)')
    expect(runtimeGraphs?.textContent).toContain('Route Latency (ms)')

    expect(runtimeGraphs?.getAttribute('data-visible-groups')).toContain('browse')
    const initialLoadImpactRange = runtimeGraphs?.getAttribute('data-load-impact-vuser-range')
    expect(initialLoadImpactRange).toBeTruthy()

    const browseVisibility = buttonByAriaLabel('Browse Web graph visibility')
    expect(browseVisibility.getAttribute('aria-pressed')).toBe('true')

    await click(browseVisibility)

    expect(browseVisibility.getAttribute('aria-pressed')).toBe('false')
    expect(browseVisibility.getAttribute('data-state')).toBe('hidden')
    expect(browseVisibility.textContent).toContain('Hidden')
    expect(graphSelectionPanel?.textContent).toContain('2 / 3 groups visible')
    expect(runtimeGraphs?.getAttribute('data-visible-groups')).not.toContain('browse')
    expect(runtimeGraphs?.getAttribute('data-visible-groups')).toContain('checkout')
    expect(runtimeGraphs?.getAttribute('data-load-impact-vuser-range')).not.toBe(initialLoadImpactRange)

    await click(buttonWithin(graphOptionsPanel!, 'Relative Time'))

    expect(buttonWithin(graphOptionsPanel!, 'Relative Time').getAttribute('aria-pressed')).toBe('true')

    expect(buttonWithin(graphOptionsPanel!, '3x3').getAttribute('aria-pressed')).toBe('true')
    expect(runtimeGraphs?.querySelector('.chart-metrics-table')).toBeNull()

    const tableRowsToggle = inputByLabel('Show table rows beneath graphs')
    expect(tableRowsToggle.checked).toBe(false)

    await click(tableRowsToggle)

    expect(tableRowsToggle.checked).toBe(true)
    expect(runtimeGraphs?.querySelector('.chart-metrics-table')).not.toBeNull()

    await click(tableRowsToggle)
    await click(buttonWithin(graphOptionsPanel!, '2x2'))

    expect(buttonWithin(graphOptionsPanel!, '2x2').getAttribute('aria-pressed')).toBe('true')
    expect(container.querySelector('.charts-grid.command-center-graphs.command-center-compact')).toBeNull()
    expect(runtimeGraphs?.querySelector('.chart-metrics-table')).toBeNull()

    const graphExpandButton = runtimeGraphs?.querySelector<HTMLButtonElement>('.btn-expand')
    expect(graphExpandButton).not.toBeNull()

    await click(graphExpandButton!)

    expect(runtimeGraphs?.querySelector('.chart-card.expanded .chart-metrics-table')).not.toBeNull()

    await click(buttonWithin(graphOptionsPanel!, '3x3'))

    expect(container.querySelector('.charts-grid.command-center-graphs.command-center-compact')).not.toBeNull()
  })

  it('updates local run state and staged VUser targets', async () => {
    await renderPrototype()

    await click(buttonNamed('Pause'))

    expect(container.textContent).toContain('PAUSED')
    expect(container.textContent).toContain('Run paused at the next action boundary.')

    const checkoutTarget = inputByLabel('Checkout API target VUsers')
    await changeNumberInput(checkoutTarget, '1330')

    expect(checkoutTarget.value).toBe('1330')
    expect(container.textContent).toContain('Checkout API target staged at 1,330 VUsers.')
    expect(container.textContent).toContain('2,270 VU')
  })

  it('keeps Abort available and enabled while a graceful stop is in progress', async () => {
    await renderPrototype()

    const runActions = () => container.querySelector('.command-run-actions') as HTMLElement
    const shelfButton = (name: string) =>
      Array.from(runActions().querySelectorAll('button')).find(
        candidate => candidate.textContent?.trim() === name,
      ) as HTMLButtonElement | undefined

    // Sanity: while RUNNING, both Stop and Abort are present and enabled.
    expect(shelfButton('Stop')).toBeDefined()
    expect(shelfButton('Abort')).toBeDefined()
    expect(shelfButton('Abort')?.disabled).toBe(false)

    await click(buttonNamed('Stop'))

    expect(container.textContent).toContain('STOPPING')
    // Stop is no longer offered once the graceful stop is underway (canStop excludes STOPPING),
    // but Abort must remain the escalation path.
    expect(shelfButton('Stop')).toBeUndefined()
    const abortDuringStopping = shelfButton('Abort')
    expect(abortDuringStopping).toBeDefined()
    expect(abortDuringStopping?.disabled).toBe(false)

    await click(abortDuringStopping as HTMLButtonElement)

    expect(container.textContent).toContain('ABORTED')
    // Once fully terminal, Abort is no longer offered.
    expect(shelfButton('Abort')).toBeUndefined()
  })

  it('keeps a live run STOPPING after an abort acknowledgement until durable finalization', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => jsonResponse({}))
    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={buildLiveVuserTest()} history={[]} />)
    })

    const runActions = container.querySelector('.command-run-actions') as HTMLElement
    const abort = Array.from(runActions.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Abort') as HTMLButtonElement
    await click(abort)

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/tests/live-vuser-1/abort'),
      expect.objectContaining({ method: 'POST' }),
    )
    expect(container.textContent).toContain('STOPPING')
    expect(container.textContent).toContain('Abort requested. Waiting for generator teardown and finalization.')
    expect(container.textContent).not.toContain('ABORTED')
    expect(Array.from(runActions.querySelectorAll('button')).some(button => button.textContent?.trim() === 'Abort')).toBe(true)
  })

  it('stages slider targets and updates the current target indicator without calling the API', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    await renderPrototype()

    const checkoutSlider = inputByLabel('Checkout API target VUser slider')
    await changeNumberInput(checkoutSlider, '1330')

    expect(fetchMock).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Checkout API target staged at 1,330 VUsers.')
    const indicator = container.querySelector('[aria-label="Current 860 VUsers, target 1,330 VUsers"]')
    expect(indicator).not.toBeNull()
    expect(indicator?.getAttribute('data-current-load')).toBe('860')
    expect(indicator?.getAttribute('data-target-load')).toBe('1330')
  })

  it('uses a shared VUser slider scale that grows from staged targets', async () => {
    await renderPrototype()

    const checkoutSlider = inputByLabel('Checkout API target VUser slider')
    const browseSlider = inputByLabel('Browse Web target VUser slider')
    const searchSlider = inputByLabel('Search Solace target VUser slider')

    expect(checkoutSlider.max).toBe('1800')
    expect(browseSlider.max).toBe('1800')
    expect(searchSlider.max).toBe('1800')
    expect(container.querySelector('[aria-label="Current 320 VUsers, target 420 VUsers"]')?.getAttribute('data-scale-max')).toBe('1800')

    await changeNumberInput(inputByLabel('Checkout API target VUsers'), '1330')

    expect(checkoutSlider.max).toBe('1995')
    expect(browseSlider.max).toBe('1995')
    expect(searchSlider.max).toBe('1995')
    expect(container.querySelector('[aria-label="Current 320 VUsers, target 420 VUsers"]')?.getAttribute('data-scale-max')).toBe('1995')
  })

  it('does not force lower-load VUser sliders onto the old 1800 max', async () => {
    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={buildLiveVuserTest()} history={[]} />)
    })

    const liveSlider = inputByLabel('Live Checkout target VUser slider')
    const indicator = container.querySelector('[aria-label="Current 42 VUsers, target 50 VUsers"]')

    expect(liveSlider.max).toBe('75')
    expect(indicator?.getAttribute('data-scale-max')).toBe('75')
  })

  it('applies live VUser targets immediately without a ramp duration', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify({
      test_id: 'live-vuser-1',
      group: 'Live Checkout',
      action: 'run',
      target_vusers: 60,
      status: 'scaled',
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={buildLiveVuserTest()} history={[]} />)
    })

    await changeNumberInput(inputByLabel('Live Checkout target VUsers'), '60')
    await click(buttonNamed('Apply'))

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/tests/live-vuser-1/vusers'),
      expect.objectContaining({
        method: 'PATCH',
        body: JSON.stringify({ group: 'Live Checkout', target_vusers: 60, action: 'run' }),
      }),
    )
    expect(container.textContent).toContain('Live Checkout VUser target applied at 60.')
  })

  it('applies live VUser targets gradually with the card ramp duration', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify({
      test_id: 'live-vuser-1',
      group: 'Live Checkout',
      action: 'run',
      target_vusers: 60,
      ramp_duration: '2m',
      status: 'ramping',
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={buildLiveVuserTest()} history={[]} />)
    })

    await click(buttonNamed('Gradual'))
    await changeNumberInput(inputByLabel('Live Checkout target VUsers'), '60')
    await click(buttonNamed('Apply'))

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/tests/live-vuser-1/vusers'),
      expect.objectContaining({
        method: 'PATCH',
        body: JSON.stringify({ group: 'Live Checkout', target_vusers: 60, action: 'run', ramp_duration: '2m' }),
      }),
    )
    expect(container.textContent).toContain('Ramping Live Checkout to 60 VUsers over 2m.')
  })

  it('disables gradual VUser apply when the ramp duration is invalid', async () => {
    // A live run fires a Prometheus graphs fetch on mount, so scope the assertion to
    // the vusers control endpoint rather than asserting fetch was never called.
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => jsonResponse({}))

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={buildLiveVuserTest()} history={[]} />)
    })

    await click(buttonNamed('Gradual'))
    await changeTextInput(inputByLabel('Group load ramp duration'), 'soon')

    const applyButton = buttonNamed('Apply')
    expect(applyButton.disabled).toBe(true)
    await click(applyButton)

    expect(fetchMock.mock.calls.some(([input]) => requestUrl(input).includes('/vusers'))).toBe(false)
    expect(container.textContent).toContain('Enter a valid ramp duration before applying gradual load changes.')
  })

  it('shows active VUser ramp targets as committed load indicators', async () => {
    await act(async () => {
      root.render(<CommandCenterPrototype
        testId="live-vuser-1"
        initialTest={{
          ...buildLiveVuserTest(),
          groups: [{ ...buildLiveVuserTest().groups[0], vusers: 50 }],
          vuser_ramps: {
            'Live Checkout': {
              group: 'Live Checkout',
              start_vusers: 42,
              target_vusers: 80,
              started_at: '2026-06-08T13:10:00Z',
              ramp_duration: '2m',
              ramp_duration_seconds: 120,
            },
          },
        }}
        history={[]}
      />)
    })

    const indicator = container.querySelector('[aria-label="Current 42 VUsers, target 80 VUsers"]')
    expect(indicator).not.toBeNull()
    expect(indicator?.getAttribute('data-committed-load')).toBe('80')
    expect(inputByLabel('Live Checkout target VUser slider').max).toBe('120')
    expect(indicator?.getAttribute('data-scale-max')).toBe('120')
    expect(container.textContent).toContain('Ramp 80 VUsers')
  })

  it('renders OPS tables while Graph Wall keeps configurable runtime graphs', async () => {
    await renderPrototype()

    const tableWorkspace = container.querySelector('[aria-label="Command center run tables"]')
    expect(tableWorkspace).not.toBeNull()
    expect(container.querySelector('.command-layout-ops .command-runtime-graphs')).toBeNull()
    expect(tableWorkspace?.textContent).toContain('Scenario Run')
    expect(tableWorkspace?.textContent).toContain('POST /orders')
    expect(tableWorkspace?.textContent).toContain('Target TPS')

    await click(tabNamed('Error Details'))

    expect(tableWorkspace?.textContent).toContain('HTTP 401 token refresh race')
    expect(tableWorkspace?.textContent).toContain('Request body')
    expect(tableWorkspace?.textContent).toContain('invalid_token')

    await click(buttonNamed('Cart lookup p99 spike'))

    expect(container.textContent).toContain('Drill-down selected: Cart lookup p99 spike')
    expect(tableWorkspace?.textContent).toContain('gateway timeout waiting for cart-cache-primary')

    await click(tabNamed('Infrastructure'))

    expect(tableWorkspace?.textContent).toContain('gen-east-1')
    expect(tableWorkspace?.textContent).toContain('checkout.api.internal:443')

    const filterInput = inputByLabel('Filter command center table rows')
    await changeTextInput(filterInput, 'central')

    expect(tableWorkspace?.textContent).toContain('gen-central-2')
    expect(tableWorkspace?.textContent).not.toContain('gen-east-1')

    await click(buttonNamed('Graph Wall'))

    expect(container.textContent).toContain('Runtime Graphs')
    expect(container.textContent).toContain('Virtual Users')
    expect(container.textContent).toContain('Transactions Per Second')
    expect(container.querySelector('.charts-grid.command-center-graphs.command-center-compact')).not.toBeNull()
    expect(container.querySelector('.chart-card.command-center-chart-card')).not.toBeNull()

    await click(buttonNamed('Configure Graphs'))

    const cpuToggle = inputByLabel('Show CPU Usage — Fleet (%)')
    expect(cpuToggle.checked).toBe(true)

    await click(cpuToggle)

    expect(window.localStorage.getItem('apexload.commandCenter.grafana.hiddenCharts')).toContain('cpu')

    const expandButton = container.querySelector<HTMLButtonElement>('.btn-expand')
    expect(expandButton).not.toBeNull()

    await click(expandButton!)

    expect(container.textContent).toContain('Runtime graph expanded.')
    expect(container.querySelector('.chart-card.expanded')).not.toBeNull()

    const vuserToggle = inputByLabel('Show Virtual Users')
    expect(vuserToggle.checked).toBe(true)
    expect(container.querySelector('.charts-config-drawer')?.textContent).toContain('Graph Cards')
    expect(container.querySelector('.charts-config-drawer')?.textContent).not.toContain('Display Window')

    await click(vuserToggle)

    expect(window.localStorage.getItem('apexload.commandCenter.grafana.hiddenCharts')).toContain('vusers')
  })

  it('surfaces Prometheus graphs in the Graph Wall with a non-zero count for a live run', async () => {
    // Route the Prometheus graphs endpoint to a configured pack; everything else
    // (status polls, metrics) resolves to an empty 200 so the live shell renders.
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL) => {
      const url = requestUrl(input)
      if (url.includes('/monitoring/prometheus/graphs')) {
        return jsonResponse({
          enabled: true,
          configured: true,
          generated_at: '2026-06-08T13:10:00Z',
          graphs: [{
            id: 'prometheus_scaling_vusers',
            title: 'Scaling VUsers',
            unit: 'VUsers',
            series: [{
              key: 'prometheus_scaling_vusers_state_active',
              label: 'active',
              labels: { state: 'active' },
              values: [
                { timestamp: '2026-06-08T13:05:00Z', timestamp_ms: Date.parse('2026-06-08T13:05:00Z'), value: 10 },
                { timestamp: '2026-06-08T13:06:00Z', timestamp_ms: Date.parse('2026-06-08T13:06:00Z'), value: 20 },
              ],
            }],
          }],
        })
      }
      return jsonResponse({})
    })

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={buildLiveVuserTest()} history={[]} />)
    })
    await click(buttonNamed('Graph Wall'))
    await flush()

    // The Prometheus graphs endpoint was queried for the live run window.
    expect(fetchMock.mock.calls.some(([input]) => requestUrl(input).includes('/monitoring/prometheus/graphs'))).toBe(true)

    const prometheusTab = Array.from(container.querySelectorAll('button[role="tab"]'))
      .find(tab => tab.textContent?.includes('Prometheus')) as HTMLButtonElement | undefined
    expect(prometheusTab, 'Expected a Prometheus graph-group tab in the Graph Wall').toBeDefined()
    // The count must be non-zero: a naive wiring fix leaves the imported charts hidden
    // by the monitor preset, rendering the tab with a "0" count and nothing inside.
    expect(prometheusTab?.querySelector('small')?.textContent).not.toBe('0')
  })

  it('keeps transaction rows when live metrics temporarily omit transactions', () => {
    const groups = [commandGroup('Live Checkout', 50)]
    const transaction: TxMetrics = {
      name: 'POST /orders',
      count: 23,
      errors: 1,
      avg_ms: 140,
      p50_ms: 120,
      p90_ms: 220,
      p95_ms: 260,
      p99_ms: 410,
      dependency_type: 'http',
    }
    const snapshot = { 'POST /orders': transaction }

    const rowsWhenPresent = buildScenarioRunRows(groups, {
      timestamp: '2026-06-08T13:10:00Z',
      active_vusers: 42,
      tps: 75.4,
      avg_response_ms: 134,
      error_pct: 0.5,
      total_transactions: 1200,
      total_errors: 6,
      p50_ms: 82,
      p90_ms: 210,
      p95_ms: 260,
      p99_ms: 410,
      transactions: snapshot,
    })
    const rowsWhenMissing = buildScenarioRunRows(groups, {
      timestamp: '2026-06-08T13:10:01Z',
      active_vusers: 42,
      tps: 75.4,
      avg_response_ms: 134,
      error_pct: 0.5,
      total_transactions: 1200,
      total_errors: 6,
      p50_ms: 82,
      p90_ms: 210,
      p95_ms: 260,
      p99_ms: 410,
    }, snapshot)

    expect(rowsWhenPresent.some(row => row.id === 'transaction-POST /orders')).toBe(true)
    expect(rowsWhenMissing.some(row => row.id === 'transaction-POST /orders')).toBe(true)
  })

  it('does not query Prometheus in prototype (non-live) mode', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({}))
    await renderPrototype()
    await click(buttonNamed('Graph Wall'))
    await flush()

    expect(fetchMock.mock.calls.some(([input]) => requestUrl(input).includes('/monitoring/prometheus/graphs'))).toBe(false)
    const prometheusTab = Array.from(container.querySelectorAll('button[role="tab"]'))
      .find(tab => tab.textContent?.includes('Prometheus'))
    expect(prometheusTab).toBeUndefined()
  })

  it('activates error and evidence rows with Enter and Space', async () => {
    await renderPrototype()
    await click(tabNamed('Error Details'))

    let errorRows = Array.from(container.querySelectorAll<HTMLTableRowElement>('.command-error-table tbody tr'))
    expect(errorRows.length).toBeGreaterThan(1)
    expect(errorRows[1].tabIndex).toBe(0)
    await act(async () => {
      errorRows[1].focus()
      errorRows[1].dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })
    expect(errorRows[1].classList.contains('selected')).toBe(true)
    expect(errorRows[1].getAttribute('aria-selected')).toBe('true')

    errorRows = Array.from(container.querySelectorAll<HTMLTableRowElement>('.command-error-table tbody tr'))
    await act(async () => {
      errorRows[0].focus()
      errorRows[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })
    const evidenceRows = Array.from(container.querySelectorAll<HTMLTableRowElement>('.command-evidence-table tbody tr'))
    expect(evidenceRows.length).toBeGreaterThan(1)
    await act(async () => {
      evidenceRows[1].focus()
      evidenceRows[1].dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }))
    })
    expect(evidenceRows[1].classList.contains('selected')).toBe(true)
    expect(evidenceRows[1].getAttribute('aria-selected')).toBe('true')
  })

  it('renders live command-center data when a test is selected', async () => {
    const liveTest: TestSummary = {
      id: 'live-run-1',
      name: 'Live Checkout Ramp',
      status: 'COMPLETE',
      total_vusers: 50,
      duration: '10m',
      created_at: '2026-06-08T13:00:00Z',
      started_at: '2026-06-08T13:05:00Z',
      groups: [{
        name: 'Live Checkout',
        protocol: 'http',
        script_id: 'script-live',
        script_name: 'Live Checkout Script',
        vusers: 50,
      }],
      live_metrics: {
        timestamp: '2026-06-08T13:10:00Z',
        active_vusers: 42,
        tps: 75.4,
        target_tps: 90,
        avg_response_ms: 134,
        error_pct: 0.5,
        total_transactions: 1200,
        total_errors: 6,
        p50_ms: 82,
        p90_ms: 210,
        p95_ms: 260,
        p99_ms: 410,
        groups: {
          'Live Checkout': {
            name: 'Live Checkout',
            vusers: 42,
            transactions: 1200,
            errors: 6,
            tps: 75.4,
            avg_response_ms: 134,
            p50_ms: 82,
            p90_ms: 210,
            p99_ms: 410,
          },
        },
        error_patterns: [{
          signature: 'HTTP 500 checkout spike',
          sample: 'inventory reservation returned status 500',
          count: 6,
          first_seen: '2026-06-08T13:09:00Z',
          latest_seen: '2026-06-08T13:10:00Z',
          actions: ['POST /checkout'],
          affected_groups: ['Live Checkout'],
          error_pct: 0.5,
        }],
      },
    }
    const history: MetricsHistoryPoint[] = [{
      time: '09:10:00',
      timestampMs: Date.parse('2026-06-08T13:10:00Z'),
      vusers: 42,
      tps: 75.4,
      targetTps: 90,
      avgMs: 134,
      errorPct: 0.5,
      p50: 82,
      p90: 210,
      p95: 260,
      p99: 410,
      'Live Checkout_tps': 75.4,
    }]

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-run-1" initialTest={liveTest} history={history} />)
    })

    expect(container.textContent).toContain('Live Checkout Ramp')
    expect(container.textContent).toContain('Live Checkout')
    expect(container.textContent).toContain('HTTP 500 checkout spike')
    expect(container.textContent).not.toContain('In-flight Run Command Center')
  })

  it('never reuses same-signature evidence across routed runs', async () => {
    const withEvidencePattern = (id: string, name: string): TestSummary => ({
      ...buildLiveVuserTest(),
      id,
      name,
      live_metrics: {
        ...buildLiveVuserTest().live_metrics!,
        error_patterns: [{
          signature: 'HTTP 500 shared signature',
          sample: 'shared failure',
          count: 1,
          first_seen: '2026-06-08T13:09:00Z',
          latest_seen: '2026-06-08T13:10:00Z',
          actions: ['POST /checkout'],
          affected_groups: ['Live Checkout'],
          evidence_count: 1,
        }],
      },
    })
    let resolveRunB: ((response: Response) => void) | undefined
    vi.spyOn(globalThis, 'fetch').mockImplementation(input => {
      const url = requestUrl(input)
      if (url.includes('/tests/run-a/errors/evidence')) {
        return Promise.resolve(jsonResponse({ evidence: [{
          id: 'evidence-a',
          signature: 'HTTP 500 shared signature',
          action: 'RUN A EVIDENCE',
          group: 'Live Checkout',
          timestamp: '2026-06-08T13:10:00Z',
          duration_ms: 100,
          status: 'failed',
          status_code: 500,
        }] }))
      }
      if (url.includes('/tests/run-b/errors/evidence')) {
        return new Promise<Response>(resolve => { resolveRunB = resolve })
      }
      return Promise.resolve(jsonResponse({}))
    })

    await act(async () => {
      root.render(<CommandCenterPrototype testId="run-a" initialTest={withEvidencePattern('run-a', 'Run A')} history={[]} />)
    })
    await click(tabNamed('Error Details'))
    await flush()
    expect(container.textContent).toContain('RUN A EVIDENCE')

    await act(async () => {
      root.render(<CommandCenterPrototype testId="run-b" initialTest={withEvidencePattern('run-b', 'Run B')} history={[]} />)
      await Promise.resolve()
    })

    expect(container.textContent).not.toContain('RUN A EVIDENCE')
    expect(container.textContent).toContain('Loading live evidence')

    await act(async () => {
      resolveRunB?.(jsonResponse({ evidence: [{
        id: 'evidence-b',
        signature: 'HTTP 500 shared signature',
        action: 'RUN B EVIDENCE',
        group: 'Live Checkout',
        timestamp: '2026-06-08T13:11:00Z',
        duration_ms: 120,
        status: 'failed',
        status_code: 500,
      }] }))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain('RUN B EVIDENCE')
    expect(container.textContent).not.toContain('RUN A EVIDENCE')
  })

  it('refreshes same-signature evidence only when its count or latest evidence changes', async () => {
    const withEvidenceFreshness = (evidenceCount: number, latestEvidenceId: string): TestSummary => ({
      ...buildLiveVuserTest(),
      live_metrics: {
        ...buildLiveVuserTest().live_metrics!,
        error_patterns: [{
          signature: 'HTTP 500 evolving signature',
          sample: 'evolving failure',
          count: evidenceCount,
          first_seen: '2026-06-08T13:09:00Z',
          latest_seen: '2026-06-08T13:10:00Z',
          actions: ['POST /checkout'],
          affected_groups: ['Live Checkout'],
          evidence_count: evidenceCount,
          latest_evidence_id: latestEvidenceId,
        }],
      },
    })
    let evidenceCalls = 0
    vi.spyOn(globalThis, 'fetch').mockImplementation(input => {
      const url = requestUrl(input)
      if (url.includes('/tests/live-vuser-1/errors/evidence')) {
        evidenceCalls += 1
        return Promise.resolve(jsonResponse({ evidence: [{
          id: `evidence-${evidenceCalls}`,
          signature: 'HTTP 500 evolving signature',
          action: `EVIDENCE VERSION ${evidenceCalls}`,
          group: 'Live Checkout',
          timestamp: '2026-06-08T13:10:00Z',
          duration_ms: 100,
          status: 'failed',
          status_code: 500,
        }] }))
      }
      return Promise.resolve(jsonResponse({}))
    })

    const firstSnapshot = withEvidenceFreshness(1, 'evidence-1')
    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={firstSnapshot} history={[]} />)
    })
    await click(tabNamed('Error Details'))
    await flush()
    expect(evidenceCalls).toBe(1)
    expect(container.textContent).toContain('EVIDENCE VERSION 1')

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={firstSnapshot} history={[]} />)
    })
    await flush()
    expect(evidenceCalls).toBe(1)

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={withEvidenceFreshness(2, 'evidence-1')} history={[]} />)
    })
    await flush()
    expect(evidenceCalls).toBe(2)
    expect(container.textContent).toContain('EVIDENCE VERSION 2')

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={withEvidenceFreshness(2, 'evidence-2')} history={[]} />)
    })
    await flush()
    expect(evidenceCalls).toBe(3)
    expect(container.textContent).toContain('EVIDENCE VERSION 3')
  })

  it('starts a reserved run from the command center', async () => {
    const reservedTest: TestSummary = {
      id: 'reserved-run-1',
      ref_key: 'RUN-RESERVED',
      name: 'Reserved Checkout Ramp',
      status: 'RESERVED',
      total_vusers: 50,
      duration: '10m',
      created_at: '2026-06-08T13:00:00Z',
      scheduled_start_at: '2026-06-08T13:15:00Z',
      groups: [{
        name: 'Live Checkout',
        protocol: 'http',
        script_id: 'script-live',
        script_name: 'Live Checkout Script',
        vusers: 50,
      }],
    }
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify({
      status: 'started',
      test_id: 'reserved-run-1',
      test: {
        ...reservedTest,
        status: 'INITIALIZING',
      },
    }), { headers: { 'content-type': 'application/json' } }))

    await act(async () => {
      root.render(<CommandCenterPrototype testId="reserved-run-1" initialTest={reservedTest} history={[]} />)
    })

    const controlShelf = container.querySelector('[aria-label="Command center run controls"]')
    expect(controlShelf?.textContent).toContain('RESERVED')
    expect(controlShelf?.textContent).toContain('Start Test')
    expect(controlShelf?.textContent).not.toContain('Pause')
    expect(controlShelf?.textContent).not.toContain('Stop')

    await click(buttonNamed('Start Test'))
    await act(async () => {
      await Promise.resolve()
    })

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/tests/reserved-run-1/start'),
      expect.objectContaining({ method: 'POST' }),
    )
    expect(controlShelf?.textContent).toContain('Start requested. Run is initializing.')
    fetchMock.mockRestore()
  })

  it('renders terminal runs as historical read-only snapshots with archive navigation', async () => {
    const openExecution = vi.fn()
    const openArchive = vi.fn()
    const completedTest: TestSummary = {
      id: 'completed-run-1',
      ref_key: 'RUN-COMPLETE',
      name: 'Completed Checkout',
      status: 'COMPLETE',
      total_vusers: 50,
      duration: '10m',
      created_at: '2026-06-08T13:00:00Z',
      started_at: '2026-06-08T13:05:00Z',
      archive: {
        test_id: 'completed-run-1',
        schema_version: '1',
        archive_path: '/archives/completed-run-1',
        manifest_path: '/archives/completed-run-1/manifest.json',
        transaction_count: 1200,
        resource_sample_count: 0,
        interval_summary_count: 0,
        created_at: '2026-06-08T13:00:00Z',
        completed_at: '2026-06-08T13:10:00Z',
      },
      groups: [{
        name: 'Live Checkout',
        protocol: 'http',
        script_id: 'script-live',
        script_name: 'Live Checkout Script',
        vusers: 50,
      }],
      live_metrics: {
        timestamp: '2026-06-08T13:10:00Z',
        active_vusers: 0,
        tps: 0,
        avg_response_ms: 134,
        error_pct: 0.5,
        total_transactions: 1200,
        total_errors: 6,
        p50_ms: 82,
        p90_ms: 210,
        p95_ms: 260,
        p99_ms: 410,
        groups: {
          'Live Checkout': {
            name: 'Live Checkout',
            vusers: 0,
            transactions: 1200,
            errors: 6,
            tps: 0,
            avg_response_ms: 134,
            p50_ms: 82,
            p90_ms: 210,
            p99_ms: 410,
          },
        },
      },
    }

    await act(async () => {
      root.render(
        <CommandCenterPrototype
          testId="completed-run-1"
          initialTest={completedTest}
          history={[]}
          statusOverride="RUNNING"
          onOpenExecution={openExecution}
          onOpenArchive={openArchive}
        />,
      )
    })

    expect(container.textContent).toContain('Historical run snapshot')
    expect(container.textContent).toContain('Live pause, stop, abort, and scaling controls are unavailable.')
    expect(container.textContent).toContain('Open Results Archive')
    expect(container.textContent).not.toContain('Pause')
    expect(container.textContent).not.toContain('Stop')
    expect(container.textContent).not.toContain('Abort')
    expect(container.textContent).not.toContain('Apply')
    expect(container.querySelector('[aria-label="Live Checkout target VUsers"]')).toBeNull()
    expect(container.textContent).toContain('50 VUsers')
    expect(container.querySelector('[aria-label="Command center run tables"]')).not.toBeNull()

    const archiveButton = buttonNamed('Open Results Archive')
    expect(archiveButton.className).toContain('btn-primary')

    await click(archiveButton)

    expect(openArchive).toHaveBeenCalledWith('completed-run-1')
    expect(openExecution).not.toHaveBeenCalled()
  })

  it('points historical runs without archive snapshots to the Results folder', async () => {
    const openResults = vi.fn()
    const failedTest: TestSummary = {
      id: 'failed-run-1',
      ref_key: 'RUN-FAILED',
      name: 'Failed Checkout',
      status: 'FAILED',
      total_vusers: 25,
      duration: '8m',
      created_at: '2026-06-08T14:00:00Z',
      ended_at: '2026-06-08T14:08:00Z',
      groups: [{
        name: 'Checkout',
        protocol: 'http',
        script_id: 'script-checkout',
        script_name: 'Checkout Script',
        vusers: 25,
      }],
    }

    await act(async () => {
      root.render(
        <CommandCenterPrototype
          testId="failed-run-1"
          initialTest={failedTest}
          history={[]}
          onOpenResults={openResults}
        />,
      )
    })

    expect(container.textContent).toContain('Historical run snapshot')
    expect(container.textContent).toContain('Historical results can be opened from the Results folder')
    expect(container.textContent).toContain('Open Results')
    expect(container.textContent).not.toContain('Open Results Archive')

    await click(buttonNamed('Open Results'))

    expect(openResults).toHaveBeenCalledOnce()
  })

  it('uses per-group TPS target edits for goal-oriented live runs', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify({
      test_id: 'live-goal-1',
      group: 'Live Checkout',
      target_tps: 120,
      target_average_tps: 120,
      goal_targets: {
        'Live Checkout': {
          group: 'Live Checkout',
          target_tps: 120,
          updated_at: '2026-06-08T13:11:00Z',
        },
      },
      status: 'adjusted',
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    const liveTest: TestSummary = {
      id: 'live-goal-1',
      ref_key: 'RUN-20260608-GOAL',
      name: 'Live Goal Checkout',
      status: 'RUNNING',
      duration: '30m',
      total_vusers: 50,
      created_at: '2026-06-08T13:00:00Z',
      started_at: '2026-06-08T13:05:00Z',
      scenario_mode: 'goal_oriented',
      goal_config: {
        target_average_tps: 90,
        min_vusers: 10,
        max_vusers: 200,
      },
      goal_targets: {
        'Live Checkout': {
          group: 'Live Checkout',
          target_tps: 90,
          updated_at: '2026-06-08T13:05:00Z',
        },
      },
      groups: [{
        name: 'Live Checkout',
        protocol: 'http',
        script_id: 'script-live',
        script_name: 'Live Checkout Script',
        vusers: 50,
      }],
      live_metrics: {
        timestamp: '2026-06-08T13:10:00Z',
        active_vusers: 42,
        tps: 75.4,
        target_tps: 90,
        throughput_gap_pct: 16.2,
        throughput_target_missed: true,
        avg_response_ms: 134,
        error_pct: 0.5,
        total_transactions: 1200,
        total_errors: 6,
        p50_ms: 82,
        p90_ms: 210,
        p95_ms: 260,
        p99_ms: 410,
        goal_status: {
          mode: 'goal_oriented',
          current_tps: 75.4,
          current_total_vusers: 42,
          target_average_tps: 90,
          suggested_total_vusers: 56,
          min_vusers: 10,
          max_vusers: 200,
        },
        groups: {
          'Live Checkout': {
            name: 'Live Checkout',
            vusers: 42,
            transactions: 1200,
            errors: 6,
            tps: 75.4,
            target_tps: 90,
            throughput_gap_pct: 16.2,
            throughput_target_missed: true,
            avg_response_ms: 134,
            p50_ms: 82,
            p90_ms: 210,
            p99_ms: 410,
          },
        },
      },
    }

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-goal-1" initialTest={liveTest} history={[]} />)
    })

    expect(container.textContent).toContain('Target TPS')
    expect(container.textContent).toContain('suggested 56 VU')
    expect(container.textContent).toContain('guardrail 10-200 VU')
    expect(container.querySelector('[aria-label="Group load transition controls"]')).toBeNull()

    const targetInput = inputByLabel('Live Checkout target TPS')
    const targetSlider = inputByLabel('Live Checkout target TPS slider')
    expect(targetInput.value).toBe('90')
    expect(targetSlider.max).toBe('180')

    await changeNumberInput(targetInput, '120')
    expect(container.textContent).toContain('Live Checkout target staged at 120.0 TPS.')
    expect(targetSlider.max).toBe('240')

    await click(buttonNamed('Apply'))

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/tests/live-goal-1/goal-targets'),
      expect.objectContaining({
        method: 'PATCH',
        body: JSON.stringify({ group: 'Live Checkout', target_tps: 120 }),
      }),
    )
    expect(container.textContent).toContain('Live Checkout TPS target applied at 120.0. Aggregate goal is 120.0 TPS.')
    expect(container.textContent).toContain('120.0')
  })

  it('opens a context menu on a live group row and launches the Runtime Settings dialog', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = requestUrl(input)
      if (url.includes('/groups/Live%20Checkout/runtime-settings')) {
        return jsonResponse({
          test_id: 'live-vuser-1',
          ref_key: 'RUN-20260608-VUSER',
          group: 'Live Checkout',
          group_index: 0,
          script_name: 'Live Checkout Script',
          protocol: 'http',
          status: 'RUNNING',
          editable: false,
          editable_reason: 'run is RUNNING; settings were locked when the run left the pre-start queue',
          editable_fields: ['pacing', 'think_time'],
          effective_settings: { pacing: { type: 'fixed_delay', delay_ms: 750 } },
        })
      }
      if (url.includes('/dsl/config-defaults')) return jsonResponse({ config: {} })
      return jsonResponse({})
    })
    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={buildLiveVuserTest()} history={[]} />)
    })

    const groupRow = container.querySelector('.command-group-row:not(.header)') as HTMLElement
    expect(groupRow).not.toBeNull()
    expect(groupRow.tabIndex).toBe(0)
    await act(async () => {
      groupRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 120, clientY: 140 }))
    })

    const menu = document.querySelector('.command-group-context-menu') as HTMLElement
    expect(menu).not.toBeNull()
    expect(menu.tabIndex).toBe(-1)
    const menuItems = Array.from(menu.querySelectorAll('button')).map(button => button.textContent?.trim())
    expect(menuItems).toEqual(['Runtime Settings', 'Parameters'])

    await click(buttonWithin(menu, 'Runtime Settings'))
    await flush()

    expect(document.querySelector('.command-group-context-menu')).toBeNull()
    const dialog = document.querySelector('.group-run-settings-dialog') as HTMLElement
    expect(dialog).not.toBeNull()
    expect(dialog.textContent).toContain('Runtime Settings — Live Checkout')
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/tests/live-vuser-1/groups/Live%20Checkout/runtime-settings'),
      expect.anything(),
    )
    // A running run is read-only: the reason banner shows and there is no save action.
    expect(dialog.textContent).toContain('Read-only')
    expect(dialog.textContent).toContain('fixed_delay')
    expect(Array.from(dialog.querySelectorAll('button')).some(button => button.textContent?.includes('Save to run'))).toBe(false)
  })

  it('exposes group actions as a real button and keeps its keyboard menu inside a narrow viewport', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({}))
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(120)
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(80)
    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={buildLiveVuserTest()} history={[]} />)
    })

    const action = container.querySelector('button.command-group-name[aria-haspopup="menu"]') as HTMLButtonElement | null
    expect(action).not.toBeNull()
    expect(action?.getAttribute('aria-label')).toBe('Live Checkout group actions')
    expect(action?.parentElement?.getAttribute('role')).toBe('cell')
    if (!action) return

    await click(action)
    await flush()

    const menu = document.querySelector('.command-group-context-menu') as HTMLElement | null
    expect(menu).not.toBeNull()
    expect(menu?.style.left).toBe('8px')
    expect(menu?.style.top).toBe('8px')
    expect(document.activeElement?.textContent).toContain('Runtime Settings')
  })

  it('opens the group actions menu from a focused row with Shift+F10', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({}))
    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={buildLiveVuserTest()} history={[]} />)
    })

    const groupRow = container.querySelector('.command-group-row:not(.header)') as HTMLElement
    groupRow.focus()
    await act(async () => {
      groupRow.dispatchEvent(new KeyboardEvent('keydown', {
        bubbles: true,
        cancelable: true,
        key: 'F10',
        shiftKey: true,
      }))
    })
    await flush()

    expect(document.querySelector('.command-group-context-menu')).not.toBeNull()
    expect(document.activeElement?.textContent).toContain('Runtime Settings')
  })

  it('opens the Parameters dialog from the group context menu', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = requestUrl(input)
      if (url.includes('/groups/Live%20Checkout/parameters')) {
        return jsonResponse({
          test_id: 'live-vuser-1',
          group: 'Live Checkout',
          group_index: 0,
          script_name: 'Live Checkout Script',
          protocol: 'http',
          status: 'RUNNING',
          editable: false,
          editable_reason: 'run is RUNNING; settings were locked when the run left the pre-start queue',
          parameters: [
            { name: 'Region', type: 'list', values: ['east', 'west'], update: 'once', selection: 'random' },
          ],
          data_sources: [{ name: 'accounts', type: 'csv', columns: ['user', 'region'], row_count: 2 }],
        })
      }
      return jsonResponse({})
    })
    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={buildLiveVuserTest()} history={[]} />)
    })

    const groupRow = container.querySelector('.command-group-row:not(.header)') as HTMLElement
    await act(async () => {
      groupRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 90, clientY: 100 }))
    })
    const menu = document.querySelector('.command-group-context-menu') as HTMLElement
    await click(buttonWithin(menu, 'Parameters'))
    await flush()

    const dialog = document.querySelector('.group-run-settings-dialog') as HTMLElement
    expect(dialog).not.toBeNull()
    expect(dialog.textContent).toContain('Parameters — Live Checkout')
    expect(dialog.textContent).toContain('Region')
    expect(dialog.textContent).toContain('east, west')
    expect(dialog.textContent).toContain('Data sources: accounts (2 rows): user, region')
  })

  it('does not offer the group context menu for the local prototype', async () => {
    await renderPrototype()

    const groupRow = container.querySelector('.command-group-row:not(.header)') as HTMLElement
    expect(groupRow).not.toBeNull()
    await act(async () => {
      groupRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 90, clientY: 100 }))
    })

    expect(document.querySelector('.command-group-context-menu')).toBeNull()
  })

  async function renderPrototype() {
    await act(async () => {
      root.render(<CommandCenterPrototype prototypeMode />)
    })
  }

  function buttonNamed(name: string) {
    const button = Array.from(container.querySelectorAll('button')).find(candidate => candidate.textContent?.includes(name))
    expect(button, `Expected button named ${name}`).toBeDefined()
    return button as HTMLButtonElement
  }

  function buttonByAriaLabel(label: string) {
    const button = container.querySelector(`button[aria-label="${label}"]`) as HTMLButtonElement | null
    expect(button, `Expected button labeled ${label}`).not.toBeNull()
    return button as HTMLButtonElement
  }

  function tabNamed(name: string) {
    const tab = Array.from(container.querySelectorAll('button[role="tab"]')).find(candidate => candidate.textContent?.includes(name))
    expect(tab, `Expected tab named ${name}`).toBeDefined()
    return tab as HTMLButtonElement
  }

  function buttonWithin(rootElement: Element, name: string) {
    const button = Array.from(rootElement.querySelectorAll('button')).find(candidate => candidate.textContent?.includes(name))
    expect(button, `Expected button named ${name}`).toBeDefined()
    return button as HTMLButtonElement
  }

  function inputByLabel(label: string) {
    const input = container.querySelector(`input[aria-label="${label}"]`) as HTMLInputElement | null
    expect(input, `Expected input labeled ${label}`).not.toBeNull()
    return input as HTMLInputElement
  }

  async function click(element: HTMLElement) {
    await act(async () => {
      element.click()
    })
  }

  function jsonResponse(body: unknown) {
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }

  function requestUrl(input: RequestInfo | URL) {
    if (typeof input === 'string') return input
    if (input instanceof URL) return input.toString()
    return input.url
  }

  // Flush the chained async effects (discover -> select -> load) plus their
  // re-renders. A few act() turns covers the microtask hops between them.
  async function flush(times = 5) {
    for (let i = 0; i < times; i++) {
      await act(async () => {
        await Promise.resolve()
      })
    }
  }

  async function changeNumberInput(input: HTMLInputElement, value: string) {
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
      input.dispatchEvent(new Event('change', { bubbles: true }))
    })
  }

  async function changeTextInput(input: HTMLInputElement, value: string) {
    await changeNumberInput(input, value)
  }

  function buildLiveVuserTest(): TestSummary {
    return {
      id: 'live-vuser-1',
      ref_key: 'RUN-20260608-VUSER',
      name: 'Live VUser Checkout',
      status: 'RUNNING',
      total_vusers: 50,
      duration: '10m',
      created_at: '2026-06-08T13:00:00Z',
      started_at: '2026-06-08T13:05:00Z',
      groups: [{
        name: 'Live Checkout',
        protocol: 'http',
        script_id: 'script-live',
        script_name: 'Live Checkout Script',
        vusers: 50,
      }],
      live_metrics: {
        timestamp: '2026-06-08T13:10:00Z',
        active_vusers: 42,
        tps: 75.4,
        target_tps: 90,
        avg_response_ms: 134,
        error_pct: 0.5,
        total_transactions: 1200,
        total_errors: 6,
        p50_ms: 82,
        p90_ms: 210,
        p95_ms: 260,
        p99_ms: 410,
        groups: {
          'Live Checkout': {
            name: 'Live Checkout',
            vusers: 42,
            transactions: 1200,
            errors: 6,
            tps: 75.4,
            avg_response_ms: 134,
            p50_ms: 82,
            p90_ms: 210,
            p99_ms: 410,
          },
        },
      },
    }
  }

  function commandGroup(name: string, targetVusers: number): CommandGroup {
    return {
      key: name.toLowerCase(),
      name,
      protocol: 'http',
      current: targetVusers,
      target: targetVusers,
      targetVusers,
      targetMode: 'vusers',
      tps: 0,
      art: 0,
      errorPct: 0,
      p99: 0,
      transactions: 0,
      errors: 0,
      color: 'var(--chart-1)',
    }
  }

  function archiveTransaction(timestamp: string, group: string, durationMs: number, success: boolean): TransactionEvent {
    return {
      timestamp,
      test_id: 'completed-run',
      test_name: 'Completed Run',
      group,
      action_name: 'GET /',
      duration_ms: durationMs,
      success,
    }
  }
})
