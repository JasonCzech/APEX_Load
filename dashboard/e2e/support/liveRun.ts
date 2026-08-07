import { test as base, type APIRequestContext, type APIResponse } from '@playwright/test'

// Seeds a genuine controller run so browser specs can assert against the real
// dashboard rendering real backend metrics. Without this, a spec that wants
// live metrics has to hand-write a DOM fixture, which passes even when the
// application is completely broken (or not served at all).
//
// The run drives the controller's own health endpoint: it is always reachable
// from the controller under test, needs no extra fixture server, and is cheap
// enough to leave running for the few seconds a browser assertion needs.

export interface LiveRun {
  scriptId: string
  testId: string
}

export interface StartLiveRunOptions {
  /** Distinguishes the seeded script/run in the controller. */
  label?: string
  /** VUsers in the single load group. Keep this small; it is smoke load. */
  vusers?: number
  /** Wall-clock budget for the run reaching RUNNING with recorded traffic. */
  readyTimeoutMs?: number
  /**
   * Controller duration string for the seeded run. Must comfortably outlive the
   * caller's whole `test.setTimeout` budget (see RUN_DURATION) — but no longer,
   * because it is also the worst-case lifetime of a run whose teardown is lost.
   */
  duration?: string
}

const HEALTH_PATH = '/api/v1/system/health'

// Browser specs wait tens of seconds for the metric stream to settle before
// they assert on the hero tiles. If the run reached its natural end inside that
// budget the controller would correctly report active_vusers=0 and a healthy
// dashboard would fail the assertions, so the seeded run has to outlive the
// waiting the specs are allowed to do.
//
// It must not outlive it by much, though: the duration is also the blast radius
// of a teardown that never happens. The `liveRun` fixture below tears runs down
// from fixture scope precisely so that a timed-out spec still cleans up, but a
// crashed worker has no teardown at all, and specs run sequentially against one
// shared controller (playwright.config.ts: workers 1, retries 1 in CI), so a
// leaked run contaminates whatever runs next. 5m covers the longest spec budget
// in this suite (command-center-hero-metrics: 240s) with a minute to spare, and
// self-heals about a minute after that spec's hard cap.
const RUN_DURATION = '5m'

async function requireOk(response: APIResponse, what: string): Promise<unknown> {
  if (!response.ok()) {
    const body = await response.text()
    throw new Error(`${what} failed: HTTP ${response.status()} ${response.statusText()} ${body.slice(0, 500)}`)
  }
  return response.json()
}

function identifier(payload: unknown, what: string): string {
  const id = (payload as { id?: unknown } | null)?.id
  if (typeof id !== 'string' || id === '') {
    throw new Error(`${what} returned no id: ${JSON.stringify(payload).slice(0, 500)}`)
  }
  return id
}

/**
 * Creates a script and a run, starts the run, and resolves once the controller
 * reports it RUNNING with at least one completed transaction — the point at
 * which the dashboard has live metrics to render.
 */
export async function startLiveRun(
  request: APIRequestContext,
  baseURL: string | undefined,
  options: StartLiveRunOptions = {},
): Promise<LiveRun> {
  if (!baseURL) {
    throw new Error('startLiveRun needs the controller base URL (playwright `use.baseURL`)')
  }
  const label = options.label || 'e2e-live-run'
  const vusers = options.vusers ?? 2
  const readyTimeoutMs = options.readyTimeoutMs ?? 90_000

  const scriptPayload = await requireOk(
    await request.post('/api/v1/scripts', {
      data: {
        script: {
          name: `${label}-script`,
          version: '1.0.0',
          protocol: 'http',
          config: {
            base_url: baseURL,
            pacing: { type: 'fixed_interval', interval_ms: 500 },
          },
          actions: [{ name: 'Health', type: 'http', method: 'GET', target: HEALTH_PATH }],
        },
      },
    }),
    'create e2e script',
  )
  const scriptId = identifier(scriptPayload, 'create e2e script')

  const testPayload = await requireOk(
    await request.post('/api/v1/tests', {
      data: {
        name: `${label}-run`,
        duration: options.duration ?? RUN_DURATION,
        groups: [{ name: 'smoke', script_id: scriptId, vusers, ramp_up: '1s' }],
      },
    }),
    'create e2e run',
  )
  const testId = identifier(testPayload, 'create e2e run')
  const run: LiveRun = { scriptId, testId }

  try {
    await requireOk(await request.post(`/api/v1/tests/${testId}/start`), 'start e2e run')
    await waitForLiveTraffic(request, testId, readyTimeoutMs)
  } catch (error) {
    await abortLiveRun(request, run)
    throw error
  }
  return run
}

async function waitForLiveTraffic(request: APIRequestContext, testId: string, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs
  let lastStatus = 'unknown'
  let lastTransactions = 0
  while (Date.now() < deadline) {
    const response = await request.get(`/api/v1/tests/${testId}`)
    if (response.ok()) {
      const test = (await response.json()) as {
        status?: string
        live_metrics?: { total_transactions?: number }
      }
      lastStatus = test.status || 'unknown'
      lastTransactions = test.live_metrics?.total_transactions ?? 0
      if (lastStatus === 'RUNNING' && lastTransactions > 0) return
      if (lastStatus === 'FAILED' || lastStatus === 'ABORTED' || lastStatus === 'COMPLETE') {
        throw new Error(`e2e run ${testId} reached ${lastStatus} before producing live metrics`)
      }
    }
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  throw new Error(
    `e2e run ${testId} did not report live traffic within ${timeoutMs}ms (status=${lastStatus}, transactions=${lastTransactions})`,
  )
}

/**
 * Best-effort teardown. Never throws: a cleanup problem must not mask (or
 * manufacture) a spec failure, and the controller under test is disposable.
 *
 * It does not stay silent, though. A swallowed abort leaves the seeded run
 * driving load against the controller that the next spec asserts on, so every
 * failed call is reported on stderr with the step that failed — the run is
 * still leaked, but the leak is no longer invisible.
 */
export async function abortLiveRun(request: APIRequestContext, run: LiveRun): Promise<void> {
  const attempt = async (what: string, send: () => Promise<APIResponse>) => {
    try {
      const response = await send()
      if (!response.ok() && response.status() !== 404) {
        console.warn(`[liveRun] ${what} for ${run.testId} returned HTTP ${response.status()} ${response.statusText()}`)
      }
    } catch (error) {
      // Never rethrow: teardown must not manufacture a spec failure. Do report
      // it — this is exactly the path that leaks a running load test.
      console.warn(`[liveRun] ${what} for ${run.testId} failed: ${String(error)}`)
    }
  }
  await attempt('abort', () => request.post(`/api/v1/tests/${run.testId}/abort`))
  // An aborted run stays durably STOPPING until teardown and archive
  // finalization publish the terminal state, and the delete is rejected until
  // then. Give that a short window so repeated local runs do not accumulate
  // undeletable records.
  await waitForTerminalStatus(request, run.testId, 15_000)
  await attempt('delete test', () => request.delete(`/api/v1/tests/${run.testId}`))
  await attempt('delete script', () => request.delete(`/api/v1/scripts/${run.scriptId}`))
}

const TERMINAL_STATUSES = new Set(['COMPLETE', 'ABORTED', 'FAILED', 'CANCELLED'])

async function waitForTerminalStatus(request: APIRequestContext, testId: string, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      const response = await request.get(`/api/v1/tests/${testId}`)
      if (!response.ok()) return
      const status = ((await response.json()) as { status?: string }).status || ''
      if (TERMINAL_STATUSES.has(status)) return
    } catch {
      return
    }
    await new Promise(resolve => setTimeout(resolve, 500))
  }
}

export interface LiveRunFixtures {
  /**
   * Seeds a live controller run and registers it for automatic teardown.
   * Prefer this over calling `startLiveRun`/`abortLiveRun` by hand.
   */
  seedLiveRun: (options?: StartLiveRunOptions) => Promise<LiveRun>
}

/**
 * `test` with a `seedLiveRun` fixture whose teardown survives a test timeout.
 *
 * A `try/finally` in the test body does not: when Playwright times a test out
 * it stops awaiting the body and starts disposing fixtures, so the `finally`
 * runs against an already-closed `request` context and every teardown call
 * throws `Target page, context or browser has been closed`. With teardown
 * swallowed, the seeded run kept executing against the controller the next spec
 * asserts on. Verified with the repo's own Playwright: a body `finally` after a
 * timeout reached the server zero times, while this fixture's teardown reached
 * it every time (and is allowed ~20s+ of its own budget after the timeout).
 *
 * The fixture also uses its own APIRequestContext rather than the built-in
 * `request` fixture, because that one is disposed as part of the same timeout
 * teardown and is not guaranteed to still be usable here.
 */
export const test = base.extend<LiveRunFixtures>({
  // The fixture callback's second argument is Playwright's `use`; it is named
  // `provide` here only so eslint's react-hooks/rules-of-hooks does not read the
  // call as a misplaced React hook.
  seedLiveRun: async ({ playwright, baseURL }, provide) => {
    const seeded: LiveRun[] = []
    const context = await playwright.request.newContext({ baseURL })
    await provide(async (options: StartLiveRunOptions = {}) => {
      const run = await startLiveRun(context, baseURL, options)
      seeded.push(run)
      return run
    })
    try {
      for (const run of seeded.reverse()) {
        await abortLiveRun(context, run)
      }
    } finally {
      await context.dispose()
    }
  },
})

export { expect } from '@playwright/test'
