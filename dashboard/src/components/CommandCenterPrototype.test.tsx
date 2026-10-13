// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CommandCenterPrototype, {
  buildSelectedGroupChartHistoryFromTransactions,
  historicalGroupSummaryQuery,
  selectedGroupLoadImpactData,
  stabilizeErrorRowOrder,
  type ErrorPattern,
} from './CommandCenterPrototype'
import { buildRunLoadPlan, loadPlanPhaseAt, observedVUsersSince, plannedVUsersAt, runLoadStatus } from './commandCenterLoadModel'
import { buildTransactionRunRows, type CommandGroup } from './commandCenterScenarioRows'
import { liveEvidenceCacheKey } from './commandCenterEvidenceModel'
import { transactionChartSeriesKey } from './transactionChartSeries'
import { EXECUTION_ARCHIVE_FINALIZATION_TIMEOUT_MS } from './useExecutionTestPolling'
import type { MetricsHistoryPoint, TestSummary, TransactionEvent, TxMetrics } from '../types'
import { ApiContext } from '../hooks/apiContext'
import type { ApiServices } from '../hooks/ApiProvider'
import { rcaTestResponse } from './rca/rcaTestFixture'

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

  it('keeps error rows stationary while live counts refresh', () => {
    const pattern = (id: string, count: number): ErrorPattern => ({
      id,
      signature: id,
      title: id,
      group: 'Checkout',
      transaction: id,
      count,
      rate: count,
      firstSeen: '10:00:00',
      latestSeen: '10:00:01',
      severity: 'warning',
      sample: `${id} failed`,
      actions: [id],
      affectedGroups: ['Checkout'],
    })

    const refreshedByCount = [pattern('error-b', 12), pattern('error-a', 11), pattern('error-c', 1)]
    const stable = stabilizeErrorRowOrder(refreshedByCount, 'count-desc', {
      viewKey: 'count-desc',
      ids: ['error-a', 'error-b'],
    })
    expect(stable.map(row => row.id)).toEqual(['error-a', 'error-b', 'error-c'])
    expect(stable[0].count).toBe(11)

    const explicitlyResorted = stabilizeErrorRowOrder([refreshedByCount[2], refreshedByCount[1], refreshedByCount[0]], 'count-asc', {
      viewKey: 'count-desc',
      ids: stable.map(row => row.id),
    })
    expect(explicitlyResorted.map(row => row.id)).toEqual(['error-c', 'error-a', 'error-b'])
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

  // The demo fixtures are only reachable through `prototypeMode`, which nothing
  // in App.tsx sets. A static import would put all of them back into the chunk
  // every production Command Center session downloads, so the separation has to
  // be asserted at the module-graph level -- rendering cannot observe it.
  it('keeps the prototype demo fixtures out of the production Command Center chunk', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync, readdirSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const owner = `${cwd}/src/components/CommandCenterPrototype.tsx`
    const fixtureModule = `${cwd}/src/components/commandCenterPrototypeFixtures.ts`
    const source = readFileSync(owner, 'utf8') as string

    // The tree walk below exempts the owner -- it is the one shipped module
    // allowed to name the fixture chunk -- so this block is the owner's only
    // line of defence, and it must not be line-anchored. A regex that requires
    // the specifier to share a physical line with the `import` keyword misses a
    // multi-line static value import, an `export ... from` re-export and
    // `require`, each of which statically merges the 19 KB fixture module back
    // into the eagerly-loaded Command Center chunk. So: match the two sanctioned
    // references with newline-tolerant patterns, then erase them and require
    // that nothing else in the file names the module by path at all.
    // A value import must be dynamic; the only static one may be `import type`,
    // which the compiler erases and therefore never reaches the bundle.
    const sanctionedTypeImport = /import\s+type\s(?:(?!\bfrom\b)[\s\S])*?from\s*['"][^'"]*\/commandCenterPrototypeFixtures['"]/g
    const sanctionedDynamicImport = /\bimport\(\s*['"][^'"]*\/commandCenterPrototypeFixtures['"]\s*\)/g
    expect([...source.matchAll(sanctionedTypeImport)]).toHaveLength(1)
    expect([...source.matchAll(sanctionedDynamicImport)]).toHaveLength(1)
    // The needle keeps the path separator so that reading a member off the
    // resolved dynamic module (`module.commandCenterPrototypeFixtures`) stays
    // legal while any specifier -- './', '../components/', anything -- does not.
    const unsanctionedReferences = source
      .replace(sanctionedTypeImport, '')
      .replace(sanctionedDynamicImport, '')
    expect(unsanctionedReferences).not.toContain('/commandCenterPrototypeFixtures')

    // Pinning CommandCenterPrototype.tsx alone is not enough: a static import
    // from ANY other eagerly-loaded module (for example commandCenterScenarioRows.ts,
    // which this component imports statically) pulls the fixtures back into the
    // production graph while the assertions above still pass. Walk the tree and
    // require that no other shipped module references the fixture module at all.
    // Only the owner and the fixture module itself are exempt here, and the owner
    // is fully covered by the erase-and-assert check above -- not unguarded.
    const modules: string[] = []
    const walk = (directory: string) => {
      for (const entry of readdirSync(directory, { withFileTypes: true }) as Array<{ name: string; isDirectory(): boolean }>) {
        const full = `${directory}/${entry.name}`
        if (entry.isDirectory()) {
          walk(full)
          continue
        }
        if (!/\.tsx?$/.test(entry.name) || /\.test\.tsx?$/.test(entry.name)) continue
        modules.push(full)
      }
    }
    walk(`${cwd}/src`)
    // Guards the walk itself: a scanner that silently stops matching would make
    // the offender list trivially empty.
    expect(modules.length).toBeGreaterThan(300)
    const offenders = modules
      .filter(file => file !== owner && file !== fixtureModule)
      .filter(file => (readFileSync(file, 'utf8') as string).includes('commandCenterPrototypeFixtures'))
      .map(file => file.slice(cwd.length + 1))
    expect(offenders).toEqual([])

    // App.tsx must not opt the production route into prototypeMode either.
    const appSource = readFileSync(`${cwd}/src/App.tsx`, 'utf8') as string
    expect(appSource).not.toContain('prototypeMode')
  })

  it('keeps revised queued-copy modules lazy and out of Advanced Details', async () => {
    // @ts-expect-error Dashboard app tsconfig intentionally excludes Node test types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const source = readFileSync(`${cwd}/src/components/CommandCenterPrototype.tsx`, 'utf8') as string
    expect(source).toMatch(/lazy\(\(\) => import\('\.\/TestExecutionScenarioEditor'\)\)/)
    expect(source.replace(/import\('\.\/TestExecutionScenarioEditor'\)/g, '')).not.toContain('/TestExecutionScenarioEditor')
    expect(readFileSync(`${cwd}/src/components/TestExecution.tsx`, 'utf8')).not.toContain('TestExecutionScenarioEditor')
  })

  it('shows the queued-copy action only for a permitted canonical unstarted run', async () => {
    const queued: TestSummary = { ...buildLiveVuserTest(), project_id: 'project-1', status: 'QUEUED', started_at: undefined }
    vi.spyOn(globalThis, 'fetch').mockImplementation(input => {
      const url = requestUrl(input)
      if (url.endsWith(`/tests/${queued.id}`)) return Promise.resolve(jsonResponse(queued))
      if (url.includes('/tests?')) return Promise.resolve(jsonResponse({ tests: [queued], count: 1 }))
      return Promise.resolve(jsonResponse({}))
    })
    const render = async (canManageTests = false, run = queued) => { await act(async () => root.render(<CommandCenterPrototype projectId="project-1" testId={run.id} initialTest={run} canManageTests={canManageTests} onSelectRun={vi.fn()} />)); await flush() }
    await render()
    expect(container.textContent).not.toContain('Create revised queued copy')
    await render(true)
    expect(buttonNamed('Create revised queued copy').disabled).toBe(false)
    await render(false)
    expect(container.textContent).not.toContain('Create revised queued copy')
    const started = { ...queued, status: 'RUNNING' as const, started_at: '2026-10-05T00:00:01Z' }
    await render(true, started)
    expect(container.textContent).not.toContain('Create revised queued copy')
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
    const plan = buildRunLoadPlan({ ...buildLiveVuserTest(), duration: '1m', groups: [testGroup] }, [group])

    expect(plannedVUsersAt(plan, 0)).toBe(100)
    expect(plannedVUsersAt(plan, 59)).toBeLessThan(100)
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
    const plan = buildRunLoadPlan({ ...buildLiveVuserTest(), duration: '2m', groups: [testGroup] }, [commandGroup('Checkout', 100)])

    expect(plannedVUsersAt(plan, 90)).toBe(60)
    expect(loadPlanPhaseAt(plan, 90, true)).toBe('Staged load')
  })

  it('plans a ramped run as the engine steps it, at the live target', () => {
    // 3 groups of 5 over a 2 m ramp: one VUser per group every 24 s, the first at once.
    const groups = ['Browse', 'Cart', 'Checkout'].map(name => ({ name, script_id: name, vusers: 5, ramp_up: '2m', role: 'load' as const }))
    const plan = buildRunLoadPlan({ ...buildLiveVuserTest(), duration: '5m', groups } as TestSummary, groups.map(group => commandGroup(group.name, 5)))
    expect([0, 24, 48, 72, 96].map(second => plannedVUsersAt(plan, second))).toEqual([3, 6, 9, 12, 15])
    expect(plannedVUsersAt(plan, 23)).toBe(3)
    expect(loadPlanPhaseAt(plan, 30, false)).toBe('Ramp-up')
    expect(loadPlanPhaseAt(plan, 200, false)).toBe('Steady state')
    expect(loadPlanPhaseAt(plan, 300, false)).toBe('Load complete')
    // An operator's live target replaces the configured count.
    const raised = buildRunLoadPlan({ ...buildLiveVuserTest(), duration: '5m', groups } as TestSummary, groups.map(group => commandGroup(group.name, group.name === 'Browse' ? 10 : 5)))
    expect(plannedVUsersAt(raised, 200)).toBe(20)
  })

  it('states where a run stands against its plan', () => {
    const groups = [{ name: 'Browse', script_id: 's', vusers: 4, ramp_up: '40s', ramp_down: '20s', role: 'load' as const }]
    const plan = buildRunLoadPlan({ ...buildLiveVuserTest(), duration: '2m', groups } as TestSummary, [commandGroup('Browse', 4)])
    // The plan runs 2 m and ramps down for 20 s more: remaining counts the ramp-down.
    expect(runLoadStatus(plan, 60, { state: 'running', openEnded: false, staged: false })).toMatchObject({ plannedNow: 4, phase: 'Steady state', elapsedLabel: '1m of 2m20s elapsed', progressPct: 60 / 140 * 100 })
    // One of the four stops every 5 s after the deadline.
    expect(runLoadStatus(plan, 124, { state: 'running', openEnded: false, staged: false })).toMatchObject({ plannedNow: 4, phase: 'Ramp-down' })
    expect(runLoadStatus(plan, 125, { state: 'running', openEnded: false, staged: false }).plannedNow).toBe(3)
    expect(runLoadStatus(plan, 0, { state: 'not_started', openEnded: false, staged: false }).phase).toBe('Not started')
    expect(runLoadStatus(plan, 30, { state: 'paused', openEnded: false, staged: false }).phase).toBe('Paused')
    // A run stopped early is past its plan, whatever the plan had left.
    expect(runLoadStatus(plan, 57, { state: 'ended', openEnded: false, staged: false })).toMatchObject({ phase: 'Run ended', progressPct: 100, elapsedLabel: '57s of 2m20s elapsed' })
  })

  it('plans a run to its iterations up to now, holding its level with no ramp-down', () => {
    const groups = [{ name: 'Browse', script_id: 's', vusers: 4, ramp_up: '40s', ramp_down: '20s', role: 'load' as const }]
    const plan = buildRunLoadPlan({ ...buildLiveVuserTest(), termination_mode: 'iterations', duration: '', groups } as TestSummary, [commandGroup('Browse', 4)], 240)
    expect(plan.totalSeconds).toBe(240)
    expect(plan.points[plan.points.length - 1]).toMatchObject({ second: 240, total: 4 })
    expect(runLoadStatus(plan, 240, { state: 'running', openEnded: true, staged: false })).toMatchObject({ plannedNow: 4, progressPct: 0, phase: 'Running iterations', elapsedLabel: '4m elapsed' })
    // The start delay comes out of the horizon, so the plan ends where the run is.
    const delayed = buildRunLoadPlan({ ...buildLiveVuserTest(), termination_mode: 'iterations', duration: '', start_delay: '1m', groups } as TestSummary, [commandGroup('Browse', 4)], 240)
    expect(delayed.totalSeconds).toBe(240)
    expect(plannedVUsersAt(delayed, 59)).toBe(0)
    expect(plannedVUsersAt(delayed, 60)).toBe(1)
    expect(runLoadStatus(plan, 10, { state: 'running', openEnded: true, staged: false }).phase).toBe('Ramp-up')
  })

  it('places observed VUsers on seconds from the run start', () => {
    const start = Date.parse('2026-06-08T13:05:00Z')
    const history = [-1000, 0, 2500].map(offset => ({ time: '', timestampMs: start + offset, vusers: 3 }) as MetricsHistoryPoint).concat([{ time: '', vusers: 9 } as MetricsHistoryPoint])
    expect(observedVUsersSince(history, start)).toEqual([{ second: 0, vusers: 3 }, { second: 2.5, vusers: 3 }])
    expect(observedVUsersSince(history, Number.NaN)).toEqual([])
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

  it('omits global transaction series from client-scoped group data', () => {
    const checkoutTpsKey = transactionChartSeriesKey('Checkout / submit', 'tps')
    const browseAvgKey = transactionChartSeriesKey('Browse:catalog', 'avgMs')
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
      Checkout_vusers: 10,
      Checkout_tps: 4,
      Checkout_avgMs: 120,
      Checkout_transactions: 4,
      Checkout_errors: 0,
      [checkoutTpsKey]: 4,
      [browseAvgKey]: 900,
    }]

    const [scoped] = selectedGroupLoadImpactData(data, [commandGroup('Checkout', 10)])

    expect(scoped.tps).toBe(4)
    expect(checkoutTpsKey in scoped).toBe(false)
    expect(browseAvgKey in scoped).toBe(false)
    expect(data[0][checkoutTpsKey]).toBe(4)
    expect(data[0][browseAvgKey]).toBe(900)
  })

  it('preserves a reported selected-group zero and only falls back when VUsers are absent', () => {
    const base: MetricsHistoryPoint = {
      time: '12:00',
      timestampMs: Date.parse('2026-06-08T12:00:00Z'),
      vusers: 8,
      tps: 12,
      avgMs: 100,
      errorPct: 0,
      p50: 90,
      p90: 120,
      p95: 140,
      p99: 180,
      Checkout_tps: 0,
      Checkout_avgMs: 0,
      Checkout_transactions: 0,
      Checkout_errors: 0,
    }
    const group = commandGroup('Checkout', 10)

    const [reportedZero] = selectedGroupLoadImpactData([{ ...base, Checkout_vusers: 0 }], [group])
    const [missingVusers] = selectedGroupLoadImpactData([base], [group])

    expect(reportedZero.vusers).toBe(0)
    expect(missingVusers.vusers).toBe(8)
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

  it.each([false, true])('keeps staged targets and dismisses confirmation when switching (remount: %s)', async (remount) => {
    const runViewMemory = new Map<string, Map<string, unknown>>()
    const runA = buildLiveVuserTest()
    const runB = { ...buildLiveVuserTest(), id: 'run-b', name: 'Second run' }
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({}))
    await act(async () => { root.render(<CommandCenterPrototype key={remount ? runA.id : undefined} runViewMemory={runViewMemory} testId={runA.id} initialTest={runA} />) })
    await flush()
    await changeNumberInput(inputByLabel('Live Checkout target VUsers'), '65')
    await click(buttonNamed('Pause'))
    expect(container.querySelector('.confirm-run-action-modal')).not.toBeNull()
    await act(async () => { root.render(<CommandCenterPrototype key={remount ? runB.id : undefined} runViewMemory={runViewMemory} testId={runB.id} initialTest={runB} />) })
    await flush()
    expect(container.querySelector('.confirm-run-action-modal')).toBeNull()
    expect(inputByLabel('Live Checkout target VUsers').value).toBe('50')
    await act(async () => { root.render(<CommandCenterPrototype key={remount ? runA.id : undefined} runViewMemory={runViewMemory} testId={runA.id} initialTest={runA} />) })
    await flush()
    expect(inputByLabel('Live Checkout target VUsers').value).toBe('65')
  })

  it('leaves paused time out of the run clock, as the engine makes it up', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    const run = buildLiveVuserTest()
    // Five minutes after the start, one of them spent paused.
    vi.setSystemTime(Date.parse(run.started_at!) + 5 * 60_000)
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => Promise.resolve(jsonResponse({})))
    await act(async () => { root.render(<CommandCenterPrototype testId={run.id} initialTest={{ ...run, cumulative_pause_ns: 60e9 }} history={[]} />) })
    const clock = Array.from(container.querySelectorAll('.command-metric-pill')).find(pill => pill.querySelector('span')?.textContent === 'Elapsed · remaining')
    expect(clock?.querySelector('strong')?.textContent).toBe('4m 00s · 6m 00s')
    // The load model says where the run is in its plan on the same clock.
    expect(container.querySelector('[aria-label="Load model profile"]')?.textContent).toContain('4m of 10m elapsed')
  })

  it('says in the Pause dialog that the run makes up its paused time', async () => {
    const run = buildLiveVuserTest()
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => Promise.resolve(jsonResponse({})))
    await act(async () => { root.render(<CommandCenterPrototype testId={run.id} initialTest={run} history={[]} />) })
    await click(buttonNamed('Pause'))
    const dialog = container.querySelector('.confirm-run-action-modal')!
    const detail = dialog.querySelector('.confirm-run-action-detail')!
    expect(detail.textContent).toBe('Paused time does not count against the 10m duration: the run makes it up after you resume, so it ends that much later.')
    expect(dialog.getAttribute('aria-describedby')?.split(' ')).toContain(detail.id)
  })

  it('shrinks the run shelf to one row of status and controls once the page scrolls past it', async () => {
    const run = buildLiveVuserTest()
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => Promise.resolve(jsonResponse({})))
    const scroller = document.createElement('main')
    scroller.className = 'main-content'
    container.appendChild(scroller)
    const pageRoot = createRoot(scroller)
    await act(async () => { pageRoot.render(<CommandCenterPrototype testId={run.id} initialTest={run} history={[]} />) })
    const shelf = scroller.querySelector<HTMLElement>('.command-control-shelf')!
    const previous = shelf.previousElementSibling as HTMLElement
    // The shelf checks where it is once a frame while the page scrolls.
    const scrollPage = () => act(async () => {
      scroller.dispatchEvent(new Event('scroll'))
      await new Promise(resolve => requestAnimationFrame(resolve))
    })
    // jsdom has no layout (or gap): the shelf sits right under the element before it, then holds while that scrolls away.
    vi.spyOn(previous, 'getBoundingClientRect').mockReturnValue({ top: 0, bottom: 100 } as DOMRect)
    vi.spyOn(shelf, 'getBoundingClientRect').mockReturnValue({ top: 100 } as DOMRect)
    await scrollPage()
    expect(shelf.classList.contains('compact')).toBe(false)
    expect(shelf.textContent).toContain('Run notes')

    vi.spyOn(previous, 'getBoundingClientRect').mockReturnValue({ top: -400, bottom: -300 } as DOMRect)
    vi.spyOn(shelf, 'getBoundingClientRect').mockReturnValue({ top: 60 } as DOMRect)
    await scrollPage()
    expect(shelf.classList.contains('compact')).toBe(true)
    // The essentials, each named: load, throughput in the page's unit, time and errors.
    expect(Array.from(shelf.querySelectorAll('[role="group"][aria-label="Run summary"] strong')).map(label => label.textContent)).toEqual(['Load', expect.stringMatching(/^(Requests\/s|TPS)$/), 'Time', 'Errors'])
    // Every control stays within reach; only the summary pills fold into one line.
    expect(Array.from(shelf.querySelectorAll('button')).map(button => button.textContent)).toEqual(expect.arrayContaining(['Pause', 'Stop', 'Abort', 'Run notes']))
    expect(shelf.querySelectorAll('[aria-label="Run summary"]')).toHaveLength(1)

    vi.spyOn(previous, 'getBoundingClientRect').mockReturnValue({ top: 0, bottom: 100 } as DOMRect)
    vi.spyOn(shelf, 'getBoundingClientRect').mockReturnValue({ top: 100 } as DOMRect)
    await scrollPage()
    expect(shelf.classList.contains('compact')).toBe(false)
    expect(shelf.style.marginBottom).toBe('')
    await act(async () => pageRoot.unmount())
  })

  it('reverts a rejected VUser target and says it was not applied', async () => {
    const run = buildLiveVuserTest()
    vi.spyOn(globalThis, 'fetch').mockImplementation(input => {
      const url = requestUrl(input)
      if (url.includes('/vusers')) return Promise.resolve(new Response(JSON.stringify({ error: 'capacity advisory recommends 13 load generators but only 12 are available' }), { status: 400, headers: { 'Content-Type': 'application/json' } }))
      if (url.endsWith(`/tests/${run.id}`)) return Promise.resolve(jsonResponse(run))
      return Promise.resolve(jsonResponse({}))
    })
    await act(async () => { root.render(<CommandCenterPrototype testId={run.id} initialTest={run} />) })
    await flush()
    await changeNumberInput(inputByLabel('Live Checkout target VUsers'), '99999')
    const apply = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.trim() === 'Apply' && button.classList.contains('btn-primary'))!
    await click(apply)
    await confirmRunAction('Apply change')
    await flush()
    expect(inputByLabel('Live Checkout target VUsers').value).toBe('50')
    expect(container.textContent).toContain('Not applied: Live Checkout keeps its current VUser target')
  })

  it('keeps the selected run status consistent with acknowledged controls when the catalog is stale', async () => {
    const run = buildLiveVuserTest()
    const other = { ...run, id: 'other-run', name: 'Other run' }
    let current = run
    let resolvePause: ((response: Response) => void) | undefined
    vi.spyOn(globalThis, 'fetch').mockImplementation(input => {
      const url = requestUrl(input)
      if (url.includes('/tests?')) return Promise.resolve(jsonResponse({ tests: [run, other], total: 2 }))
      if (url.endsWith(`/tests/${run.id}`)) return Promise.resolve(jsonResponse(current))
      if (url.endsWith('/pause')) return new Promise(resolve => { resolvePause = response => { current = { ...run, status: 'PAUSED' }; resolve(response) } })
      if (url.endsWith('/resume')) current = run
      return Promise.resolve(jsonResponse({}))
    })
    await act(async () => { root.render(<CommandCenterPrototype projectId="project-1" testId={run.id} onSelectRun={vi.fn()} />) })
    await flush()
    const selector = container.querySelector<HTMLSelectElement>('select[aria-label="Current run"]')!
    expect(selector.selectedOptions[0].textContent).toContain('RUNNING')
    await click(buttonNamed('Pause'))
    await confirmRunAction('Pause run')
    expect(selector.selectedOptions[0].textContent).toContain('RUNNING')
    await act(async () => { resolvePause?.(jsonResponse({})); await Promise.resolve() })
    await flush()
    expect(buttonNamed('Resume')).toBeDefined()
    expect(selector.selectedOptions[0].textContent).toContain('PAUSED')
    expect(Array.from(selector.options).find(option => option.value === other.id)?.textContent).toContain('RUNNING')
    await click(buttonNamed('Resume'))
    await confirmRunAction('Resume run')
    await flush()
    expect(selector.selectedOptions[0].textContent).toContain('RUNNING')
    expect(buttonNamed('Pause')).toBeDefined()
  })

  it('does not apply a late pause response to the newly selected run', async () => {
    const runA = buildLiveVuserTest()
    const runB = { ...buildLiveVuserTest(), id: 'run-b', name: 'Second run' }
    let resolvePause: ((response: Response) => void) | undefined
    vi.spyOn(globalThis, 'fetch').mockImplementation(input => requestUrl(input).endsWith('/pause')
      ? new Promise(resolve => { resolvePause = resolve }) : Promise.resolve(jsonResponse({})))
    await act(async () => { root.render(<CommandCenterPrototype testId={runA.id} initialTest={runA} />) })
    await flush()
    await click(buttonNamed('Pause'))
    await confirmRunAction('Pause run')
    await act(async () => { root.render(<CommandCenterPrototype testId={runB.id} initialTest={runB} />) })
    await flush()
    await act(async () => { resolvePause?.(jsonResponse({})); await Promise.resolve() })
    await flush()
    expect(container.textContent).toContain('Second run')
    expect(buttonNamed('Pause')).toBeDefined()
    expect(container.textContent).not.toContain('Run paused at the next action boundary')
  })

  it('routes even a single discovered run through the supplied navigation callback', async () => {
    const onSelectRun = vi.fn()
    const run = buildLiveVuserTest()
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ tests: [run], total: 1 }))
    await act(async () => { root.render(<CommandCenterPrototype projectId="project-1" releaseId="release-1" onSelectRun={onSelectRun} />) })
    await flush()
    expect(container.querySelector('[aria-label="Active run overview"]')).not.toBeNull()
    expect(onSelectRun).not.toHaveBeenCalled()
    expect(fetchMock.mock.calls.some(([input]) => requestUrl(input).includes('release_id=release-1'))).toBe(true)
    await click(buttonNamed('Live VUser Checkout'))
    expect(onSelectRun).toHaveBeenCalledWith(run.id)
    expect(container.querySelector('[aria-label="Command center run controls"]')).toBeNull()
  })

  it('distinguishes catalog failure from empty, preserves the last list, and retries', async () => {
    let fails = true
    const onSelectRun = vi.fn()
    const run = buildLiveVuserTest()
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => fails
      ? Promise.resolve(new Response(JSON.stringify({ error: 'Catalog unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } }))
      : Promise.resolve(jsonResponse({ tests: [run], total: 1 })))
    await act(async () => { root.render(<CommandCenterPrototype projectId="project-1" onSelectRun={onSelectRun} />) })
    await flush()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Active runs could not be determined')
    expect(container.textContent).not.toContain('No active runs')
    expect(container.textContent).not.toContain('Open analysis')
    fails = false
    await click(buttonNamed('Retry active runs'))
    await flush()
    expect(container.textContent).toContain(run.name)
    expect(container.querySelector('[role="alert"]')).toBeNull()
    fails = true
    await click(buttonNamed('Refresh runs'))
    await flush()
    expect(container.textContent).toContain(run.name)
    expect(container.textContent).toContain('Showing the last successful list')
    expect(container.textContent).toContain('Last updated')
    fails = false
    await click(buttonNamed('Retry active runs'))
    await flush()
    expect(container.querySelector('[role="alert"]')).toBeNull()
  })

  it('provides permission-aware empty actions and resolves missing release before Results', async () => {
    const onNewTest = vi.fn()
    const onResults = vi.fn()
    const onSelectRelease = vi.fn()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ tests: [], total: 0 }))
    await act(async () => { root.render(<CommandCenterPrototype projectId="project-1" onSelectRun={vi.fn()} onOpenResults={onResults}
      overviewActions={{ onNewTest, newTestDisabledReason: 'Viewer cannot create runs', onSelectRelease, releases: [] }} />) })
    await flush()
    expect(buttonNamed('New run').disabled).toBe(true)
    expect(buttonNamed('Open Results').disabled).toBe(true)
    expect(container.textContent).toContain('No releases are available')
    expect(container.textContent).not.toContain('Open analysis')
    await click(buttonNamed('New run'))
    expect(onNewTest).not.toHaveBeenCalled()
    await click(buttonNamed('Open Results'))
    expect(onResults).not.toHaveBeenCalled()
  })

  it('shows the empty state (not the prototype run) when discovery finds no live runs', async () => {
    const openResults = vi.fn()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ tests: [], count: 0 }))

    await act(async () => {
      root.render(<CommandCenterPrototype projectId="project-1" onOpenResults={openResults} />)
    })
    await flush()

    expect(container.textContent).toContain('No active runs')
    expect(container.textContent).toContain('Start a new run or choose a saved scenario.')
    expect(container.textContent).toContain('Open Results')
    expect(container.textContent).not.toContain('Checkout Peak Soak - Command Center Prototype')
    expect(container.querySelector('[aria-label="Command center run controls"]')).toBeNull()
    expect(container.querySelector('[aria-label="Active run overview"]')).not.toBeNull()

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

    expect(container.querySelector('[aria-label="Active run overview"]')).toBeNull()
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

    await click(buttonNamed('Run notes'))
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
    expect(container.textContent).toContain('Active runs')
    expect(container.textContent).toContain('2 active runs')
    expect(container.textContent).toContain('Checkout Soak')
    expect(container.textContent).toContain('Search Spike')
    expect(container.querySelector('[aria-label="Active run overview"]')).not.toBeNull()

    await click(buttonNamed('Checkout Soak'))
    await flush()

    expect(container.querySelector('[aria-label="Active run overview"]')).toBeNull()
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
    expect(container.textContent).toContain('Choose a workspace')
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

    expect(container.textContent).toContain('No saved archive is available and publication progress is unknown')
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

  it('restores every archived transaction series after a completed run reload', async () => {
    const terminalRun: TestSummary = {
      id: 'completed-transactions',
      name: 'Completed transaction run',
      status: 'COMPLETE',
      total_vusers: 6,
      duration: '25s',
      created_at: '2026-06-08T13:00:00Z',
      ended_at: '2026-06-08T13:00:25Z',
      groups: [{
        name: 'smoke',
        protocol: 'http',
        script_id: 'script-1',
        script_name: 'Multi transaction script',
        vusers: 3,
      }, {
        name: 'browse',
        protocol: 'http',
        script_id: 'script-2',
        script_name: 'Second transaction script',
        vusers: 3,
      }],
      archive: {
        test_id: 'completed-transactions',
        schema_version: 'apexload.archive.v2',
        archive_path: '/archives/completed-transactions',
        manifest_path: '/archives/completed-transactions/manifest.json',
        transaction_count: 24,
        resource_sample_count: 1,
        interval_summary_count: 1,
        created_at: '2026-06-08T13:00:00Z',
        completed_at: '2026-06-08T13:00:25Z',
      },
    }
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async input => {
      const url = new URL(requestUrl(input), 'http://localhost')
      if (url.pathname === '/api/v1/tests/completed-transactions/archive/summaries') {
        const selectedGroups = url.searchParams.getAll('groups')
        return jsonResponse({
          ...(selectedGroups.length > 0 ? { filters: { groups: selectedGroups } } : {}),
          summaries: [{
            timestamp: '2026-06-08T13:00:24Z',
            test_id: terminalRun.id,
            test_name: terminalRun.name,
            active_vusers: 3,
            tps: 24,
            avg_response_ms: 10,
            error_pct: 0,
            total_transactions: 24,
            total_errors: 0,
            p50_ms: 8,
            p90_ms: 12,
            p95_ms: 14,
            p99_ms: 16,
            transactions: {
              Login: { name: 'Login', tps: 12, count: 12, errors: 0, avg_ms: 8 },
              Search: { name: 'Search', tps: 12, count: 12, errors: 0, avg_ms: 12 },
            },
          }],
        })
      }
      return jsonResponse({})
    })

    await act(async () => {
      root.render(<CommandCenterPrototype testId={terminalRun.id} initialTest={terminalRun} history={[]} />)
    })
    await flush()
    await click(buttonNamed('Graph Wall'))
    await flush()

    const responseTimeChart = container.querySelector('[aria-label^="Response Time (ms) chart."]')
    const tpsChart = container.querySelector('[aria-label^="Transactions Per Second chart."]')
    expect(responseTimeChart?.getAttribute('aria-label')).toContain('Transaction · Login')
    expect(responseTimeChart?.getAttribute('aria-label')).toContain('Transaction · Search')
    expect(tpsChart?.getAttribute('aria-label')).toContain('Transaction · Login')
    expect(tpsChart?.getAttribute('aria-label')).toContain('Transaction · Search')

    const groupSelection = container.querySelector('[aria-label="Group selection"]')
    const smokeVisibility = groupSelection?.querySelector<HTMLButtonElement>('[aria-label="smoke graph visibility"]')
    expect(smokeVisibility).not.toBeNull()
    await click(smokeVisibility!)
    await flush()

    expect(responseTimeChart?.getAttribute('aria-label')).not.toContain('Transaction · Login')
    expect(responseTimeChart?.getAttribute('aria-label')).not.toContain('Transaction · Search')
    expect(tpsChart?.getAttribute('aria-label')).not.toContain('Transaction · Login')
    expect(tpsChart?.getAttribute('aria-label')).not.toContain('Transaction · Search')

    await click(smokeVisibility!)
    await flush()

    expect(responseTimeChart?.getAttribute('aria-label')).toContain('Transaction · Login')
    expect(responseTimeChart?.getAttribute('aria-label')).toContain('Transaction · Search')
    expect(tpsChart?.getAttribute('aria-label')).toContain('Transaction · Login')
    expect(tpsChart?.getAttribute('aria-label')).toContain('Transaction · Search')
    expect(fetchMock.mock.calls.some(([input]) => {
      const url = new URL(requestUrl(input), 'http://localhost')
      return url.pathname.endsWith('/archive/summaries') && url.searchParams.get('sample_limit') === '5000'
    })).toBe(true)
  })

  it('defaults to operations and returns from analysis without changing the selected run', async () => {
    await renderPrototype()

    expect(container.querySelector('[aria-label="Run operations"]')).not.toBeNull()
    expect(container.querySelector('[aria-label="Command Center analysis Ops view"]')).toBeNull()

    await click(buttonNamed('Open analysis'))

    expect(container.querySelector('[aria-label="Command Center analysis Ops view"]')).not.toBeNull()
    expect(container.textContent).toContain('Target Transition')
    expect(buttonNamed('OPS VIEW').getAttribute('aria-pressed')).toBe('true')

    await click(buttonNamed('GRAPH WALL'))

    expect(container.querySelector('[aria-label="Runtime graph wall"]')).not.toBeNull()
    expect(buttonNamed('GRAPH WALL').getAttribute('aria-pressed')).toBe('true')

    await click(buttonNamed('Back to operations'))
    await click(buttonNamed('OPS View'))

    expect(container.querySelector('[aria-label="Run operations"]')).not.toBeNull()
    expect(container.querySelector('[aria-label="Command Center analysis Ops view"]')).toBeNull()
  })

  it('switches between the OPS View and Graph Wall variants from the sidebar', async () => {
    await renderPrototype()

    expect(container.textContent).toContain('OPS View')
    expect(container.textContent).toContain('Graph Wall')
    expect(container.querySelector('[aria-label="Run operations"]')).not.toBeNull()
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
    expect(opsPrimaryColumn?.textContent).toContain('Groups')
    expect(opsPrimaryColumn?.textContent).toContain('Error Details')
    expect(opsPrimaryColumn?.textContent).toContain('Infrastructure')
    expect(opsLoadModelRow?.querySelector('.command-group-panel')).toBe(opsCentralGroupPanel)
    expect(opsLoadModelPanel?.textContent).toContain('Load model')
    expect(opsLoadModelPanel?.textContent).toContain('elapsed')
    const progressPct = Number(opsLoadModelPanel?.getAttribute('data-progress-pct'))
    expect(Number.isNaN(progressPct)).toBe(false)
    expect(progressPct).toBeGreaterThanOrEqual(0)
    expect(progressPct).toBeLessThanOrEqual(100)
    expect(opsLoadModelPanel?.getAttribute('data-target-vusers')).toBe('2140')
    expect(Array.from(opsPrimaryColumn!.children).indexOf(opsLoadModelRow!)).toBe(0)
    expect(opsPrimaryColumn?.querySelector('.command-hero-strip')).toBeNull()

    await click(buttonNamed('Graph Wall'))

    expect(container.querySelector('[aria-label="Runtime graph wall"]')).not.toBeNull()
    expect(container.querySelector('[aria-label="Group filters"]')).toBeNull()
    expect(container.textContent).not.toContain('Graph-heavy view')

    const graphSidebar = container.querySelector('.command-grafana-side')
    const graphSwitchPanel = graphSidebar?.querySelector('.command-view-switch-panel')
    const graphSelectionPanel = graphSidebar?.querySelector('[aria-label="Group selection"]')
    const graphErrorPanel = graphSidebar?.querySelector('[aria-label="Error drill-down"]')
    const graphDisplayOptions = graphSidebar?.querySelector('[aria-label="Graph display options"]')
    const graphDisplayOptionsSlot = graphSidebar?.querySelector('.command-graph-options-slot')
    expect(graphDisplayOptions).not.toBeNull()
    expect(graphDisplayOptions?.textContent).toContain('Display Window')
    expect(graphDisplayOptions?.textContent).toContain('Reset Graphs')
    expect(Array.from(graphSidebar?.children ?? []).indexOf(graphErrorPanel!)).toBeLessThan(Array.from(graphSidebar?.children ?? []).indexOf(graphDisplayOptionsSlot!))
    await click(buttonNamed('Configure Graphs'))
    let graphOptionsPanel = container.querySelector('.charts-config-drawer')

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
    expect(graphOptionsPanel?.textContent).toContain('Graph Configuration')
    expect(graphOptionsPanel?.textContent).not.toContain('Display Window')
    expect(graphDisplayOptions?.textContent).toContain('Layout')
    expect(graphDisplayOptions?.textContent).toContain('2x2')
    expect(graphDisplayOptions?.textContent).toContain('3x3')
    expect(graphOptionsPanel?.textContent).not.toContain('Role View')
    expect(graphDisplayOptions?.textContent).toContain('Display Time')
    expect(graphDisplayOptions?.textContent).toContain('Time Units')
    expect(buttonWithin(graphDisplayOptions!, 'Milliseconds').getAttribute('aria-pressed')).toBe('true')
    expect(buttonWithin(graphDisplayOptions!, 'Seconds').getAttribute('aria-pressed')).toBe('false')
    await click(buttonWithin(graphDisplayOptions!, 'Seconds'))
    expect(buttonWithin(graphDisplayOptions!, 'Seconds').getAttribute('aria-pressed')).toBe('true')
    expect(window.localStorage.getItem('apexload.commandCenter.grafana.chartTimeUnit')).toBe('seconds')
    expect(graphOptionsPanel?.textContent).not.toContain('Graph data range')
    expect(graphOptionsPanel?.textContent).not.toContain('Graph density')
    expect(graphOptionsPanel?.textContent).not.toContain('Graph preset')
    expect(graphOptionsPanel?.textContent).not.toContain('Graph x-axis labels')

    await click(buttonWithin(graphOptionsPanel!, 'Close'))
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
    // The Seconds toggle selected earlier in this test rescales every
    // millisecond axis, so the millisecond titles follow the chosen unit.
    // Non-time units are unaffected.
    expect(runtimeGraphs?.textContent).toContain('Route Latency (s)')
    expect(runtimeGraphs?.textContent).not.toContain('Route Latency (ms)')

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

    await click(buttonNamed('Configure Graphs'))
    graphOptionsPanel = container.querySelector('.charts-config-drawer')
    await click(buttonWithin(graphDisplayOptions!, 'Relative Time'))

    expect(buttonWithin(graphDisplayOptions!, 'Relative Time').getAttribute('aria-pressed')).toBe('true')

    expect(buttonWithin(graphDisplayOptions!, '3x3').getAttribute('aria-pressed')).toBe('true')
    expect(runtimeGraphs?.querySelector('.chart-metrics-table')).toBeNull()

    const tableRowsToggle = inputByLabel('Show table rows beneath graphs')
    expect(tableRowsToggle.checked).toBe(false)

    await click(tableRowsToggle)

    expect(tableRowsToggle.checked).toBe(true)
    expect(runtimeGraphs?.querySelector('.chart-metrics-table')).not.toBeNull()

    await click(tableRowsToggle)
    await click(buttonWithin(graphDisplayOptions!, '2x2'))

    expect(buttonWithin(graphDisplayOptions!, '2x2').getAttribute('aria-pressed')).toBe('true')
    expect(container.querySelector('.charts-grid.command-center-graphs.command-center-compact')).toBeNull()
    expect(runtimeGraphs?.querySelector('.chart-metrics-table')).toBeNull()

    const graphExpandButton = runtimeGraphs?.querySelector<HTMLButtonElement>('.btn-expand')
    expect(graphExpandButton).not.toBeNull()

    await click(graphExpandButton!)

    expect(runtimeGraphs?.querySelector('.chart-card.expanded .chart-metrics-table')).not.toBeNull()

    await click(buttonWithin(graphDisplayOptions!, '3x3'))

    expect(container.querySelector('.charts-grid.command-center-graphs.command-center-compact')).not.toBeNull()
  })

  it('updates local run state and staged VUser targets', async () => {
    await renderPrototype()

    await click(buttonNamed('Pause'))
    await confirmRunAction('Pause run')

    expect(container.textContent).toContain('PAUSED')
    expect(container.textContent).toContain('Run paused at the next action boundary.')

    const checkoutTarget = inputByLabel('Checkout API target VUsers')
    await changeNumberInput(checkoutTarget, '1330')

    expect(checkoutTarget.value).toBe('1330')
    expect(container.textContent).toContain('Checkout API target staged at 1,330 VUsers.')
    expect(container.textContent).toContain('2,270 VU')
  })

  it('shows run controls read-only, with the reason, to a role that cannot control runs', async () => {
    const run = buildLiveVuserTest()
    await act(async () => root.render(<CommandCenterPrototype testId={run.id} initialTest={run} runControlsReadOnlyReason="Your role in this project can watch this run but not control it." />))
    const actions = container.querySelector('.command-run-actions') as HTMLElement
    const shelfButton = (name: string) => Array.from(actions.querySelectorAll('button')).find(candidate => candidate.textContent?.trim() === name) as HTMLButtonElement | undefined
    for (const name of ['Pause', 'Stop', 'Abort']) {
      expect(shelfButton(name)?.disabled).toBe(true)
    }
    const note = container.querySelector('.command-control-readonly')
    expect(note?.textContent).toContain('can watch this run but not control it')
    expect(shelfButton('Stop')?.getAttribute('aria-describedby')).toBe(note?.id)
  })

  it('says when the live stream is down, since live values may then be stale', async () => {
    const run = buildLiveVuserTest()
    await act(async () => root.render(<CommandCenterPrototype testId={run.id} initialTest={run} liveStreamState="disconnected" />))
    expect(container.querySelector('.command-stream-note')?.textContent).toContain('Live stream: disconnected')
    await act(async () => root.render(<CommandCenterPrototype testId={run.id} initialTest={run} liveStreamState="connected" />))
    expect(container.querySelector('.command-stream-note')).toBeNull()
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
    await confirmRunAction('Stop run')

    expect(container.textContent).toContain('STOPPING')
    // Stop is no longer offered once the graceful stop is underway (canStop excludes STOPPING),
    // but Abort must remain the escalation path.
    expect(shelfButton('Stop')).toBeUndefined()
    const abortDuringStopping = shelfButton('Abort')
    expect(abortDuringStopping).toBeDefined()
    expect(abortDuringStopping?.disabled).toBe(false)

    await click(abortDuringStopping as HTMLButtonElement)
    await confirmRunAction('Abort run')

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
    await confirmRunAction('Abort run')

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/tests/live-vuser-1/abort'),
      expect.objectContaining({ method: 'POST' }),
    )
    expect(container.textContent).toContain('STOPPING')
    expect(container.textContent).toContain('Abort requested. Waiting for generator teardown and finalization.')
    expect(container.textContent).not.toContain('ABORTED')
    expect(Array.from(runActions.querySelectorAll('button')).some(button => button.textContent?.trim() === 'Abort')).toBe(true)
  })

  it('reflects Stop and Abort on a paused run even while the socket override is stale', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => jsonResponse({}))
    // statusOverride is the last lifecycle broadcast the app saw. A run that has
    // been sitting PAUSED keeps publishing nothing, so the override is stale the
    // moment the operator stops the run from this shelf.
    await act(async () => {
      root.render(<CommandCenterPrototype
        testId="live-vuser-1"
        initialTest={{ ...buildLiveVuserTest(), status: 'PAUSED' }}
        history={[]}
        statusOverride="PAUSED"
      />)
    })

    const runActions = () => container.querySelector('.command-run-actions') as HTMLElement
    const shelfButton = (name: string) =>
      Array.from(runActions().querySelectorAll('button')).find(
        candidate => candidate.textContent?.trim() === name,
      ) as HTMLButtonElement | undefined

    expect(shelfButton('Resume')).toBeDefined()

    await click(shelfButton('Stop') as HTMLButtonElement)
    await confirmRunAction('Stop run')

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/tests/live-vuser-1/stop'),
      expect.objectContaining({ method: 'POST' }),
    )
    expect(container.textContent).toContain('STOPPING')
    expect(container.textContent).toContain('Graceful stop requested.')
    expect(shelfButton('Resume')).toBeUndefined()
    expect(shelfButton('Stop')).toBeUndefined()

    const abort = shelfButton('Abort')
    expect(abort?.disabled).toBe(false)

    await click(abort as HTMLButtonElement)
    await confirmRunAction('Abort run')

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/tests/live-vuser-1/abort'),
      expect.objectContaining({ method: 'POST' }),
    )
    expect(container.textContent).toContain('Abort requested. Waiting for generator teardown and finalization.')
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
    // A running test's load changes only once confirmed, with what it does.
    expect(fetchMock.mock.calls.some(([input]) => requestUrl(input).includes('/vusers'))).toBe(false)
    expect(container.querySelector('.confirm-run-action-detail')?.textContent).toBe('Live Checkout 50 → 60 VUsers')
    await confirmRunAction('Apply change')

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/tests/live-vuser-1/vusers'),
      expect.objectContaining({
        method: 'PATCH',
        body: JSON.stringify({ group: 'Live Checkout', target_vusers: 60, action: 'run' }),
      }),
    )
    expect(container.textContent).toContain('Live Checkout VUser target applied at 60.')
  })

  it('accepts the full load-group VUser limit instead of a prototype-scale ceiling', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => jsonResponse({
      test_id: 'live-vuser-1',
      group: 'Live Checkout',
      action: 'run',
      target_vusers: 1_000_000,
      status: 'scaled',
    }))

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={buildLiveVuserTest()} history={[]} />)
    })

    const targetInput = inputByLabel('Live Checkout target VUsers')
    expect(targetInput.max).toBe('1000000')
    await changeNumberInput(targetInput, '1000000')
    await click(buttonNamed('Apply'))
    await confirmRunAction('Apply change')

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/tests/live-vuser-1/vusers'),
      expect.objectContaining({
        method: 'PATCH',
        body: JSON.stringify({ group: 'Live Checkout', target_vusers: 1_000_000, action: 'run' }),
      }),
    )
    expect(container.textContent).toContain('Live Checkout VUser target applied at 1,000,000.')
  })

  // The staged-target clamp silently rewrites operator input, so it needs a case
  // that actually crosses the ceiling: every other VUser case in this suite sits
  // at or below the limit, which cannot tell a working clamp from a missing one.
  it('clamps an over-ceiling VUser target down to the load-group limit', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async () => jsonResponse({}))

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={buildLiveVuserTest()} history={[]} />)
    })

    const targetInput = inputByLabel('Live Checkout target VUsers')
    await changeNumberInput(targetInput, '1500000')

    expect(targetInput.value).toBe('1000000')
    expect(container.textContent).toContain('Live Checkout target staged at 1,000,000 VUsers.')
  })

  it('clamps observer-canary VUser targets to the observer ceiling', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async () => jsonResponse({}))

    const baseTest = buildLiveVuserTest()
    await act(async () => {
      root.render(<CommandCenterPrototype
        testId="live-vuser-1"
        initialTest={{
          ...baseTest,
          total_vusers: 5,
          groups: [{ ...baseTest.groups[0], protocol: 'browser', role: 'observer', vusers: 5 }],
          live_metrics: {
            ...baseTest.live_metrics!,
            active_vusers: 4,
            groups: {
              'Live Checkout': { ...baseTest.live_metrics!.groups!['Live Checkout'], vusers: 4 },
            },
          },
        }}
        history={[]}
      />)
    })

    const targetInput = inputByLabel('Live Checkout target VUsers')
    expect(targetInput.max).toBe('10')
    await changeNumberInput(targetInput, '50')

    expect(targetInput.value).toBe('10')
    expect(container.textContent).toContain('Live Checkout target staged at 10 VUsers.')
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
    expect(container.querySelector('.confirm-run-action-detail')?.textContent).toBe('Live Checkout 50 → 60 VUsers over 2m')
    await confirmRunAction('Apply change')

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
    // Each group's Apply is named by its group, starting with what it shows.
    expect(applyButton.getAttribute('aria-label')).toMatch(/^Apply .+ target$/)
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
    expect(tableWorkspace?.textContent).toContain('Checkout API')
    expect(tableWorkspace?.textContent).toContain('Target TPS')
    // Transactions have their own table.
    expect(tableWorkspace?.textContent).not.toContain('POST /orders')
    await click(tabNamed('Transactions'))
    expect(tableWorkspace?.textContent).toContain('POST /orders')
    expect(tableWorkspace?.textContent).not.toContain('Checkout API')
    // Each row says whose share of the transaction it is.
    expect([...tableWorkspace!.querySelectorAll('th')].map(header => header.textContent?.trim())).toEqual(expect.arrayContaining([expect.stringMatching(/^Group/)]))

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
    expect(container.textContent).toContain('VUsers')
    expect(container.textContent).toContain('Transactions Per Second')
    expect(container.querySelector('.charts-grid.command-center-graphs.command-center-compact')).not.toBeNull()
    expect(container.querySelector('.chart-card.command-center-chart-card')).not.toBeNull()

    await click(buttonNamed('Configure Graphs'))

    // The card list follows the graph page in view, so the fleet CPU card is
    // reachable from the Infrastructure page rather than Live Metrics.
    expect(container.querySelector('input[aria-label="Show CPU Usage — Fleet (%)"]')).toBeNull()

    await click(tabNamed('Infrastructure'))

    const cpuToggle = inputByLabel('Show CPU Usage — Fleet (%)')
    expect(cpuToggle.checked).toBe(true)

    await click(cpuToggle)

    expect(window.localStorage.getItem('apexload.commandCenter.grafana.hiddenCharts')).toContain('cpu')

    await click(tabNamed('Live Metrics'))

    const expandButton = container.querySelector<HTMLButtonElement>('.btn-expand')
    expect(expandButton).not.toBeNull()

    await click(expandButton!)

    expect(container.textContent).toContain('Runtime graph expanded.')
    expect(container.querySelector('.chart-card.expanded')).not.toBeNull()

    const vuserToggle = inputByLabel('Show VUsers')
    expect(vuserToggle.checked).toBe(true)
    expect(container.querySelector('.charts-config-drawer')?.textContent).toContain('Graph Cards')
    expect(container.querySelector('[aria-label="Graph display options"]')?.textContent).toContain('Display Window')

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

    const rowsWhenPresent = buildTransactionRunRows({
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
    const rowsWhenMissing = buildTransactionRunRows({
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

    expect(rowsWhenPresent.some(row => row.name === 'POST /orders')).toBe(true)
    expect(rowsWhenMissing.some(row => row.name === 'POST /orders')).toBe(true)
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

  it('keeps error drill-down selection when the backend reorders error patterns', async () => {
    const stableRun: TestSummary = {
      ...buildLiveVuserTest(),
      live_metrics: {
        ...buildLiveVuserTest().live_metrics!,
        error_patterns: [
          {
            signature: 'HTTP 500 basket underflow',
            sample: 'retrying checkout basket read',
            count: 4,
            first_seen: '2026-06-08T13:09:00Z',
            latest_seen: '2026-06-08T13:10:00Z',
            actions: ['POST /checkout'],
            affected_groups: ['Live Checkout'],
            error_pct: 0.5,
          },
          {
            signature: 'HTTP 503 checkout retrying',
            sample: 'gateway returned temporary unavailable',
            count: 2,
            first_seen: '2026-06-08T13:09:00Z',
            latest_seen: '2026-06-08T13:10:00Z',
            actions: ['GET /catalog'],
            affected_groups: ['Live Checkout'],
            error_pct: 0.5,
          },
        ],
      },
    }

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={stableRun} history={[]} />)
    })
    await click(tabNamed('Error Details'))
    await flush()

    const errorRows = Array.from(container.querySelectorAll<HTMLTableRowElement>('.command-error-table tbody tr'))
    expect(errorRows.length).toBe(2)
    await act(async () => {
      errorRows[1].click()
    })
    let selected = Array.from(container.querySelectorAll<HTMLTableRowElement>('.command-error-table tbody tr.selected'))
    expect(selected).toHaveLength(1)
    expect(selected[0].textContent).toContain('HTTP 503 checkout retrying')

    const rerankedRun: TestSummary = {
      ...stableRun,
      live_metrics: {
        ...stableRun.live_metrics!,
        error_patterns: [
          { ...stableRun.live_metrics!.error_patterns![1], count: 4 },
          { ...stableRun.live_metrics!.error_patterns![0], count: 3 },
        ],
      },
    }

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-vuser-1" initialTest={rerankedRun} history={[]} />)
    })
    await flush()

    selected = Array.from(container.querySelectorAll<HTMLTableRowElement>('.command-error-table tbody tr.selected'))
    expect(selected).toHaveLength(1)
    expect(selected[0].textContent).toContain('HTTP 503 checkout retrying')
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
    // A failure is titled by its example; the signature only groups it.
    const drillDown = container.querySelector('[aria-label="Error drill-down"]')!
    expect(drillDown.querySelector('.command-error-item span')?.textContent).toBe('inventory reservation returned status 500')
    expect(drillDown.textContent).toContain('Grouped asHTTP 500 checkout spike')
    // One per-signature rate definition, named as such (not a windowed "Error rate").
    expect(container.textContent).toContain('Share of errors')
    expect(container.textContent).not.toContain('In-flight Run Command Center')
  })

  it('moves TPS into the primary run summary and removes the redundant hero row', async () => {
    const liveTest = buildLiveVuserTest()
    liveTest.live_metrics = {
      ...liveTest.live_metrics!,
      // total_* and error_pct describe the last one-second window; the hero
      // must show the run-to-date rate from the run_* counters instead.
      error_pct: 0,
      total_transactions: 18,
      total_errors: 0,
      run_total_transactions: 100,
      run_total_errors: 20,
    }

    await act(async () => {
      root.render(<CommandCenterPrototype testId={liveTest.id} initialTest={liveTest} history={[]} />)
    })

    const summaryMetrics = Array.from(container.querySelectorAll<HTMLElement>('.command-metric-pill'))
    expect(summaryMetrics[0].querySelector('span')?.textContent).toBe('Observed / Target Load')
    expect(summaryMetrics[0].querySelector('strong')?.textContent).toBe('42 / 50 VU')
    const errorRateMetric = summaryMetrics
      .find(metric => metric.querySelector('span')?.textContent === 'Transaction error rate (run to date)')
    expect(errorRateMetric?.querySelector('strong')?.textContent).toBe('16.67%')
    expect(summaryMetrics.some(metric => metric.querySelector('span')?.textContent?.startsWith('Highest'))).toBe(false)
    const tpsMetric = summaryMetrics.find(metric => metric.querySelector('span')?.textContent?.startsWith('TPS'))
    expect(tpsMetric?.querySelector('strong')?.textContent).toBe('75.4')
    expect(container.querySelector('.command-hero-strip')).toBeNull()
  })

  // A script without transactions: each iteration's second request fails,
  // so every iteration's implicit transaction fails while half the requests
  // pass. Live figures count requests, as the report and the target do.
  it('counts requests for a group whose script has no transactions', async () => {
    const liveTest = buildLiveVuserTest()
    liveTest.groups = [liveTest.groups[0], { ...liveTest.groups[0], name: 'Inventory', script_name: 'Inventory Script', vusers: 4 }]
    const checkout = liveTest.live_metrics!.groups!['Live Checkout']
    liveTest.live_metrics = {
      ...liveTest.live_metrics!,
      tps: 20, total_transactions: 20, total_errors: 3, run_total_transactions: 400, run_total_errors: 387,
      requests: 50, request_errors: 15, request_rate: 50, run_total_requests: 2_800, run_request_errors: 863,
      groups: {
        'Live Checkout': { ...checkout, transactions: 20, errors: 0, tps: 20, run_transactions: 400, run_errors: 0, requests: 20, request_rate: 20, run_requests: 1_071, run_request_errors: 0 },
        Inventory: { ...checkout, name: 'Inventory', vusers: 4, transactions: 0, errors: 3, tps: 0, run_transactions: 0, run_errors: 387, requests: 30, request_errors: 15, request_rate: 30, run_requests: 1_729, run_request_errors: 863 },
      },
    }
    await act(async () => root.render(<CommandCenterPrototype testId={liveTest.id} initialTest={liveTest} history={[]} />))

    const pill = (label: string) => Array.from(container.querySelectorAll<HTMLElement>('.command-metric-pill'))
      .find(metric => metric.querySelector('span')?.textContent === label)?.querySelector('strong')?.textContent
    expect(pill('Requests/s (last sample)')).toBe('50.0')
    expect(pill('Request error rate (run to date)')).toBe('30.82%')
    expect(pill('Highest request error rate (run to date)')).toBe('49.91% · Inventory')
    expect(container.textContent).not.toContain('Transaction error rate')

    const groupLoad = container.querySelector('[aria-label="Group load and health"]')!
    const headers = Array.from(groupLoad.querySelectorAll('[role="columnheader"]')).map(header => header.textContent)
    expect(headers).toEqual(['Group', 'Current VUsers', 'Target', 'Requests/s (last sample)', 'ART', 'Error rate (requests, run to date)'])
    const inventory = Array.from(groupLoad.querySelectorAll('[role="row"]')).find(row => row.textContent?.includes('Inventory'))!
    const cells = inventory.querySelectorAll('[role="cell"]')
    expect(cells[3].textContent).toBe('30.0')
    expect(cells[5].textContent).toBe('49.91%')

    const groupsTable = container.querySelector('[role="tabpanel"][aria-label="Groups"]')!
    expect(Array.from(groupsTable.querySelectorAll('th:not([hidden])')).map(header => header.textContent?.replace(/[▲▼·]$/, ''))).toEqual([
      'Group', 'Current VUsers', 'Target VUsers', 'Requests/s (last sample)', 'Request error rate (run to date)', 'P95',
    ])
    expect(groupsTable.textContent).toContain('attempts, failures and error rates are run to date')
    // Only the name column is fixed; the rest can be chosen one by one.
    const pickers = Array.from(groupsTable.querySelectorAll<HTMLInputElement>('.command-column-picker input'))
    expect(pickers.slice(0, 2).map(input => [input.parentElement?.textContent, input.disabled])).toEqual([['Group', true], ['Protocol', false]])
    await click(pickers[1])
    expect(groupsTable.querySelector<HTMLTableCellElement>('th[aria-label="Protocol sort"]')?.hidden).toBe(false)
  })

  it('shows the highest cumulative group error rate only for multi-group runs', async () => {
    const liveTest = buildLiveVuserTest()
    liveTest.groups = [
      liveTest.groups[0],
      { ...liveTest.groups[0], name: 'Search', script_name: 'Search Script', vusers: 25 },
    ]
    liveTest.live_metrics = {
      ...liveTest.live_metrics!,
      // The last one-second window happens to hold no errors; the run-to-date
      // counters carry the rate the run summary must show.
      total_transactions: 20,
      total_errors: 0,
      error_pct: 0,
      run_total_transactions: 180,
      run_total_errors: 20,
      groups: {
        'Live Checkout': {
          ...liveTest.live_metrics!.groups!['Live Checkout'],
          transactions: 12,
          errors: 0,
          run_transactions: 90,
          run_errors: 10,
        },
        Search: {
          ...liveTest.live_metrics!.groups!['Live Checkout'],
          name: 'Search',
          vusers: 25,
          transactions: 8,
          errors: 0,
          run_transactions: 40,
          run_errors: 10,
        },
      },
    }

    await act(async () => {
      root.render(<CommandCenterPrototype testId={liveTest.id} initialTest={liveTest} history={[]} />)
    })

    const summaryMetrics = Array.from(container.querySelectorAll<HTMLElement>('.command-metric-pill'))
    expect(summaryMetrics[0].querySelector('span')?.textContent).toBe('Observed / Target Load')
    expect(summaryMetrics[0].querySelector('strong')?.textContent).toBe('67 / 75 VU')
    const overall = summaryMetrics.find(metric => metric.querySelector('span')?.textContent === 'Transaction error rate (run to date)')
    const highest = summaryMetrics.find(metric => metric.querySelector('span')?.textContent === 'Highest transaction error rate (run to date)')
    expect(overall?.querySelector('strong')?.textContent).toBe('10.00%')
    expect(highest?.querySelector('strong')?.textContent).toBe('20.00% · Search')
  })

  it.each([
    { label: 'zero errors', transactions: 100, errors: 0, expected: 'No errors', danger: false },
    { label: 'no samples', transactions: 0, errors: 0, expected: 'No samples', danger: false },
    { label: 'positive ties', transactions: 99, errors: 1, expected: '1.00% · Live Checkout, Search (tied)', danger: true },
    { label: 'small nonzero errors', transactions: 100000, errors: 1, expected: '<0.01% · Live Checkout, Search (tied)', danger: true },
  ])('reports $label without misleading group attribution', async ({ transactions, errors, expected, danger }) => {
    const liveTest = buildLiveVuserTest()
    liveTest.groups = [liveTest.groups[0], { ...liveTest.groups[0], name: 'Search' }]
    const groupMetrics = liveTest.live_metrics!.groups!['Live Checkout']
    liveTest.live_metrics = {
      ...liveTest.live_metrics!, total_transactions: transactions * 2, total_errors: errors * 2,
      groups: Object.fromEntries(['Live Checkout', 'Search'].map(name => [name, { ...groupMetrics, name, transactions, errors }])),
    }
    await act(async () => root.render(<CommandCenterPrototype testId={liveTest.id} initialTest={liveTest} />))
    // Runs recorded before run counters existed fall back to the window and say so.
    const highest = Array.from(container.querySelectorAll('.command-metric-pill')).find(metric => metric.querySelector('span')?.textContent === 'Highest transaction error rate (last sample)')!
    expect(highest.querySelector('strong')?.textContent).toBe(expected)
    expect(highest.classList.contains('danger')).toBe(danger)
    if (!errors) expect(highest.textContent).not.toMatch(/Live Checkout|Search/)
  })

  it('retains table filters, full metrics and staged targets across view changes', async () => {
    await renderPrototype()
    const preset = container.querySelector<HTMLInputElement>('.command-column-preset input')!
    expect(container.querySelector('.command-scenario-table.essential-metrics')).not.toBeNull()
    const p99 = Array.from(container.querySelectorAll<HTMLInputElement>('.command-column-picker input')).find(input => input.parentElement?.textContent === 'P99')!
    await click(p99)
    expect(container.querySelector<HTMLTableCellElement>('th[aria-label="P99 sort"]')?.hidden).toBe(false)
    // Six default columns, the fixture's browser group's journeys, and P99.
    expect(container.querySelectorAll('thead th:not([hidden])').length).toBe(8)
    await click(preset)
    await changeTextInput(inputByLabel('Filter command center table rows'), 'Checkout')
    await click(buttonNamed('Graph Wall'))
    await click(buttonNamed('OPS View'))
    expect(inputByLabel('Filter command center table rows').value).toBe('Checkout')
    expect(container.querySelector<HTMLInputElement>('.command-column-preset input')?.checked).toBe(true)
    await click(tabNamed('Error Details'))
    expect(inputByLabel('Filter command center table rows').value).toBe('')
    await changeTextInput(inputByLabel('Filter command center table rows'), 'not-an-error')
    expect(container.textContent).toContain('No matching errors')
    expect(container.querySelector('.command-evidence-panel')).toBeNull()
    await click(tabNamed('Groups'))
    expect(inputByLabel('Filter command center table rows').value).toBe('Checkout')
  })

  it('reports absent capacity telemetry and a genuinely empty error table', async () => {
    const run = buildLiveVuserTest()
    run.live_metrics = { ...run.live_metrics!, total_errors: 0, error_patterns: [], capacity_status: undefined }
    await act(async () => root.render(<CommandCenterPrototype testId={run.id} initialTest={run} />))
    expect(container.querySelector('[aria-label="Capacity snapshot"]')?.textContent).toContain('Telemetry unavailable')
    expect(container.querySelector('[aria-label="Capacity snapshot"]')?.textContent).not.toContain('10 / 12')
    await click(tabNamed('Error Details'))
    expect(container.textContent).toContain('No errors recorded.')
    expect(container.querySelector('.command-evidence-panel')).toBeNull()
    const pending = { ...run, live_metrics: { ...run.live_metrics!, total_errors: 3 } }
    await act(async () => root.render(<CommandCenterPrototype testId={pending.id} initialTest={pending} />))
    expect(container.textContent).toContain('Errors recorded; signatures are not available yet.')
    await click(buttonNamed('Graph Wall'))
    expect(buttonNamed('Adjust run')).toBeDefined()
    expect(container.querySelector('[aria-label="Run summary"]')?.textContent).toContain('TPS')
  })

  it('labels advanced navigation and restores focus after a retained details visit', async () => {
    const liveTest = buildLiveVuserTest()
    const onOpenExecution = vi.fn()
    const render = (active: boolean) => root.render(<CommandCenterPrototype testId={liveTest.id} initialTest={liveTest} active={active} onOpenExecution={onOpenExecution} />)
    await act(async () => render(true))
    const trigger = buttonNamed('Advanced run details')
    await click(trigger)
    expect(onOpenExecution).toHaveBeenCalledWith(liveTest.id)
    await act(async () => render(false))
    expect(container.textContent).not.toContain('Open analysis')
    await act(async () => render(true))
    expect(document.activeElement).toBe(trigger)
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
    // The row is titled by its example, with the signature that groups it beneath.
    const errorTable = container.querySelector('table[aria-label="Error patterns"]')!
    expect(errorTable.querySelector('thead th')?.textContent).toContain('Error')
    expect(errorTable.querySelector('tbody td strong')?.textContent).toBe('shared failure')
    expect(errorTable.querySelector('tbody td .command-table-subtext')?.textContent).toBe('Grouped as HTTP 500 shared signature')
    expect(container.querySelector('[aria-label="Live error evidence"] .command-evidence-header strong')?.textContent).toBe('shared failure')

    await act(async () => {
      root.render(<CommandCenterPrototype testId="run-b" initialTest={withEvidencePattern('run-b', 'Run B')} history={[]} />)
      await Promise.resolve()
    })

    expect(container.textContent).not.toContain('RUN A EVIDENCE')
    expect(container.textContent).not.toContain('Loading live evidence')
    expect(container.querySelector('[aria-label="Live error evidence"]')?.getAttribute('aria-busy')).toBe('true')

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
    expect(controlShelf?.textContent).toContain('Start run')
    expect(controlShelf?.textContent).not.toContain('Pause')
    expect(controlShelf?.textContent).not.toContain('Stop')

    await click(buttonNamed('Start run'))
    await confirmRunAction('Start run')
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

  it('states an ended run\'s root cause and opens it from the historical banner', async () => {
    const openRootCause = vi.fn()
    const getRootCauseAssessment = vi.fn().mockResolvedValue({ ...rcaTestResponse(), origin: 'automatic' })
    const ended = { id: 'ended-run', name: 'Ended', status: 'COMPLETE', total_vusers: 5, duration: '5m', created_at: '2026-06-08T13:00:00Z',
      archive: { test_id: 'ended-run', schema_version: '1', archive_path: '/a', manifest_path: '/a/manifest.json', transaction_count: 10, resource_sample_count: 0, interval_summary_count: 0, created_at: '2026-06-08T13:00:00Z', completed_at: '2026-06-08T13:05:00Z' },
      groups: [] } as unknown as TestSummary
    await act(async () => {
      root.render(
        <ApiContext.Provider value={{ getRootCauseAssessment } as unknown as ApiServices}>
          <CommandCenterPrototype testId="ended-run" initialTest={ended} history={[]} onOpenArchive={vi.fn()} onOpenRootCause={openRootCause} />
        </ApiContext.Provider>,
      )
    })
    await act(async () => { await Promise.resolve() })
    expect(getRootCauseAssessment).toHaveBeenCalledWith('ended-run', expect.objectContaining({ signal: expect.anything() }))
    expect(container.querySelector('.command-history-root-cause')?.textContent).toBe('Root cause: #1 Resource pressure (medium confidence)')
    await click(buttonNamed('Open root cause'))
    expect(openRootCause).toHaveBeenCalledWith('ended-run')
  })

  it('says an archived run has no analysis rather than promising an automatic one', async () => {
    // The banner shows once the archive is ready, when an automatic analysis
    // would already be queued; a run archived before the project turned
    // automatic analysis on never gets one.
    const getRootCauseAssessment = vi.fn().mockResolvedValue({ state: 'none', presented: true, analysis_enabled: true, auto_analyze: true })
    const ended = { id: 'ended-run', name: 'Ended', status: 'COMPLETE', total_vusers: 5, duration: '5m', created_at: '2026-06-08T13:00:00Z',
      archive: { test_id: 'ended-run', schema_version: '1', archive_path: '/a', manifest_path: '/a/manifest.json', transaction_count: 10, resource_sample_count: 0, interval_summary_count: 0, created_at: '2026-06-08T13:00:00Z', completed_at: '2026-06-08T13:05:00Z' },
      groups: [] } as unknown as TestSummary
    await act(async () => {
      root.render(
        <ApiContext.Provider value={{ getRootCauseAssessment } as unknown as ApiServices}>
          <CommandCenterPrototype testId="ended-run" initialTest={ended} history={[]} onOpenArchive={vi.fn()} onOpenRootCause={vi.fn()} />
        </ApiContext.Provider>,
      )
    })
    await act(async () => { await Promise.resolve() })
    expect(container.querySelector('.command-history-root-cause')?.textContent).toBe('Root cause: No analysis yet')
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
    // A goal run's throughput stays TPS, the unit of its targets, and its gap
    // is measured on the same window as the rate beside it.
    const goalRow = container.querySelector('.command-group-row.goal-target')!
    expect(goalRow.querySelector('[data-label="TPS (last sample)"]')?.textContent).toBe('75.4target 90.0 TPS / 16.2% under')
    expect(container.textContent).toContain('suggested 56 VU')
    // The VUser guardrail bounds the whole run, so it is stated once, not on each group.
    expect(container.textContent).toContain('The goal controller keeps the whole run between 10 and 200 VUsers.')
    expect(goalRow.textContent).not.toContain('guardrail')
    expect(container.querySelector('[aria-label="Group load transition controls"]')).toBeNull()
    // VUsers and throughput are separate pills; the throughput pill carries the target and the gap.
    const pills = Array.from(container.querySelectorAll<HTMLElement>('.command-metric-pill'))
    const activeLoad = pills.find(metric => metric.querySelector('span')?.textContent === 'Active VUsers')
    expect(activeLoad?.querySelector('strong')?.textContent).toBe('42 VU')
    const throughputPill = pills.find(metric => metric.querySelector('span')?.textContent?.endsWith('/ target'))
    expect(throughputPill?.querySelector('strong')?.textContent).toBe('75.4 of 90.0 · 16.2% under')
    const projected = Array.from(container.querySelectorAll('.command-capacity-snapshot .command-metric-pill')).find(metric => metric.querySelector('span')?.textContent === 'Projected')
    expect(projected?.querySelector('strong')?.textContent).toBe('50 VU')

    const targetInput = inputByLabel('Live Checkout target TPS')
    const targetSlider = inputByLabel('Live Checkout target TPS slider')
    expect(targetInput.value).toBe('90')
    expect(targetSlider.max).toBe('180')

    await changeNumberInput(targetInput, '120')
    expect(container.textContent).toContain('Live Checkout target staged at 120.0 TPS.')
    expect(targetSlider.max).toBe('240')
    // Until it is applied the gap is still measured against, and named with, 90.
    expect(goalRow.querySelector('.command-tps-stack small')?.textContent).toBe('target 90.0 TPS / 16.2% under')

    await click(buttonNamed('Apply'))
    expect(container.querySelector('.confirm-run-action-detail')?.textContent).toBe('Live Checkout 90 → 120 TPS')
    await confirmRunAction('Apply change')

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

  // A traffic profile moves its targets during the run. The page loaded with
  // stale stored targets (1.0) must show the target in force now (4.0, from
  // the live sample), where the run is in its profile, and an overshoot as
  // such — the review's replay showed "Target 1.0 TPS" while the engine held 4.
  it('shows a traffic-profile group at its current target and profile point, over or under', async () => {
    const startedAt = new Date(Date.now() - 45_000).toISOString()
    const liveTest: TestSummary = {
      id: 'live-profile-1',
      name: 'Profile replay checkout',
      status: 'RUNNING',
      duration: '2m',
      total_vusers: 2,
      created_at: startedAt,
      started_at: startedAt,
      scenario_mode: 'goal_oriented',
      goal_config: { target_average_tps: 2, min_vusers: 1, max_vusers: 20 },
      goal_targets: { Store_Browse: { group: 'Store_Browse', target_tps: 1, updated_at: startedAt } },
      traffic_profile: {
        version: 1,
        source: { kind: 'file' },
        value_semantics: 'tps',
        runtime_target: 'tps',
        interpolation: 'step',
        source_duration: '10m0s',
        target_duration: '2m0s',
        mappings: [{
          series_key: 'checkout',
          series_name: 'checkout',
          group_name: 'Store_Browse',
          peak_target: 5,
          points: [
            { offset_ms: 0, target: 1 }, { offset_ms: 12000, target: 1.5 }, { offset_ms: 24000, target: 2.5 },
            { offset_ms: 36000, target: 4 }, { offset_ms: 48000, target: 5 },
          ],
        }],
      } as TestSummary['traffic_profile'],
      groups: [{ name: 'Store_Browse', protocol: 'http', script_id: 'script-browse', script_name: 'Store_Browse', vusers: 2 }],
      live_metrics: {
        timestamp: new Date().toISOString(),
        active_vusers: 2,
        tps: 4.6,
        target_tps: 4,
        avg_response_ms: 40,
        error_pct: 0,
        total_transactions: 120,
        total_errors: 0,
        p50_ms: 30,
        p90_ms: 60,
        p95_ms: 70,
        p99_ms: 90,
        groups: {
          Store_Browse: { name: 'Store_Browse', vusers: 2, transactions: 120, errors: 0, tps: 4.6, target_tps: 4, avg_response_ms: 40, p50_ms: 30, p90_ms: 60, p99_ms: 90 },
        },
      },
    }

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-profile-1" initialTest={liveTest} history={[]} />)
    })

    const goalRow = container.querySelector('.command-group-row.goal-target')!
    expect(inputByLabel('Store_Browse target TPS').value).toBe('4')
    expect(goalRow.querySelector('.command-tps-stack small')?.textContent).toBe('target 4.0 TPS / 15.0% over')
    expect(goalRow.querySelector('.command-group-name small')?.textContent).toMatch(/^http \/ profile point 4 of 5, next 5\.0 TPS in /)
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
    expect(action).toBeTruthy()
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
    // prototypeMode pulls its demo fixtures through a dynamic import so they
    // stay out of the production Command Center chunk. Awaiting the same module
    // settles the component's own import and flushes the resulting render.
    await act(async () => {
      await import('./commandCenterPrototypeFixtures')
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

  // Start/Pause/Stop/Abort route through a confirmation dialog before firing.
  async function confirmRunAction(label: string) {
    const dialog = container.querySelector('.confirm-run-action-modal')
    expect(dialog).not.toBeNull()
    const button = Array.from(dialog?.querySelectorAll('button') ?? [])
      .find(candidate => candidate.textContent?.trim() === label) as HTMLButtonElement | undefined
    expect(button).toBeDefined()
    await click(button as HTMLButtonElement)
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

  it.each(['zero', 'missing'])('keeps %s pre-sample response metrics unavailable on a running workload', async mode => {
    const liveTest = buildLiveVuserTest()
    liveTest.live_metrics = mode === 'missing' ? undefined : { ...liveTest.live_metrics!, total_transactions: 0, total_errors: 0, tps: 0, error_pct: 0 }
    vi.spyOn(window, 'fetch').mockImplementation(() => Promise.resolve(jsonResponse({})))
    await act(async () => { root.render(<CommandCenterPrototype testId={liveTest.id} initialTest={liveTest} history={[]} />) })
    expect(container.querySelector('[aria-label="No request samples"]')).not.toBeNull()
    expect(container.querySelector('[aria-label="Run summary"]')?.textContent).toContain('No samples')
    expect(container.querySelector('[aria-label="Run summary"]')?.textContent).not.toContain('0.0%')
  })

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
      rate: 0,
      art: 0,
      errorPct: 0,
      p99: 0,
      attempts: 0,
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
