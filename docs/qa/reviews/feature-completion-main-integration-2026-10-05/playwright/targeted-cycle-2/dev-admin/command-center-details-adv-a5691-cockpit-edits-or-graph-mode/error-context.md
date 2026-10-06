# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: command-center-details.spec.ts >> advanced details returns to the same run without losing cockpit edits or graph mode
- Location: e2e/command-center-details.spec.ts:27:1

# Error details

```
TimeoutError: apiRequestContext.post: Timeout 15000ms exceeded.
Call log:
  - → POST http://127.0.0.1:18471/api/v1/tests/9a40b5a9-bf2e-4611-aac6-34b46dc40182/start
    - user-agent: Playwright/1.61.1 (arm64; macOS 27.0) node/22.22 CI/1
    - accept: */*
    - accept-encoding: gzip,deflate,br

```

# Test source

```ts
  16  | 
  17  | export interface StartLiveRunOptions {
  18  |   /** Distinguishes the seeded script/run in the controller. */
  19  |   label?: string
  20  |   /** VUsers in the single load group. Keep this small; it is smoke load. */
  21  |   vusers?: number
  22  |   /** Wall-clock budget for the run reaching RUNNING with recorded traffic. */
  23  |   readyTimeoutMs?: number
  24  |   /**
  25  |    * Controller duration string for the seeded run. Must comfortably outlive the
  26  |    * caller's whole `test.setTimeout` budget (see RUN_DURATION) — but no longer,
  27  |    * because it is also the worst-case lifetime of a run whose teardown is lost.
  28  |    */
  29  |   duration?: string
  30  | }
  31  | 
  32  | const HEALTH_PATH = '/api/v1/system/health'
  33  | 
  34  | // Browser specs wait tens of seconds for the metric stream to settle before
  35  | // they assert on the hero tiles. If the run reached its natural end inside that
  36  | // budget the controller would correctly report active_vusers=0 and a healthy
  37  | // dashboard would fail the assertions, so the seeded run has to outlive the
  38  | // waiting the specs are allowed to do.
  39  | //
  40  | // It must not outlive it by much, though: the duration is also the blast radius
  41  | // of a teardown that never happens. The `liveRun` fixture below tears runs down
  42  | // from fixture scope precisely so that a timed-out spec still cleans up, but a
  43  | // crashed worker has no teardown at all, and specs run sequentially against one
  44  | // shared controller (playwright.config.ts: workers 1, retries 1 in CI), so a
  45  | // leaked run contaminates whatever runs next. 5m covers the longest spec budget
  46  | // in this suite (command-center-hero-metrics: 240s) with a minute to spare, and
  47  | // self-heals about a minute after that spec's hard cap.
  48  | const RUN_DURATION = '5m'
  49  | 
  50  | async function requireOk(response: APIResponse, what: string): Promise<unknown> {
  51  |   if (!response.ok()) {
  52  |     const body = await response.text()
  53  |     throw new Error(`${what} failed: HTTP ${response.status()} ${response.statusText()} ${body.slice(0, 500)}`)
  54  |   }
  55  |   return response.json()
  56  | }
  57  | 
  58  | function identifier(payload: unknown, what: string): string {
  59  |   const id = (payload as { id?: unknown } | null)?.id
  60  |   if (typeof id !== 'string' || id === '') {
  61  |     throw new Error(`${what} returned no id: ${JSON.stringify(payload).slice(0, 500)}`)
  62  |   }
  63  |   return id
  64  | }
  65  | 
  66  | /**
  67  |  * Creates a script and a run, starts the run, and resolves once the controller
  68  |  * reports it RUNNING with at least one completed transaction — the point at
  69  |  * which the dashboard has live metrics to render.
  70  |  */
  71  | export async function startLiveRun(
  72  |   request: APIRequestContext,
  73  |   baseURL: string | undefined,
  74  |   options: StartLiveRunOptions = {},
  75  | ): Promise<LiveRun> {
  76  |   if (!baseURL) {
  77  |     throw new Error('startLiveRun needs the controller base URL (playwright `use.baseURL`)')
  78  |   }
  79  |   const label = options.label || 'e2e-live-run'
  80  |   const vusers = options.vusers ?? 2
  81  |   const readyTimeoutMs = options.readyTimeoutMs ?? 90_000
  82  | 
  83  |   const scriptPayload = await requireOk(
  84  |     await request.post('/api/v1/scripts', {
  85  |       data: {
  86  |         script: {
  87  |           name: `${label}-script`,
  88  |           version: '1.0.0',
  89  |           protocol: 'http',
  90  |           config: {
  91  |             base_url: baseURL,
  92  |             pacing: { type: 'fixed_interval', interval_ms: 500 },
  93  |           },
  94  |           actions: [{ name: 'Health', type: 'http', method: 'GET', target: HEALTH_PATH }],
  95  |         },
  96  |       },
  97  |     }),
  98  |     'create e2e script',
  99  |   )
  100 |   const scriptId = identifier(scriptPayload, 'create e2e script')
  101 | 
  102 |   const testPayload = await requireOk(
  103 |     await request.post('/api/v1/tests', {
  104 |       data: {
  105 |         name: `${label}-run`,
  106 |         duration: options.duration ?? RUN_DURATION,
  107 |         groups: [{ name: 'smoke', script_id: scriptId, vusers, ramp_up: '1s' }],
  108 |       },
  109 |     }),
  110 |     'create e2e run',
  111 |   )
  112 |   const testId = identifier(testPayload, 'create e2e run')
  113 |   const run: LiveRun = { scriptId, testId }
  114 | 
  115 |   try {
> 116 |     await requireOk(await request.post(`/api/v1/tests/${testId}/start`), 'start e2e run')
      |                                   ^ TimeoutError: apiRequestContext.post: Timeout 15000ms exceeded.
  117 |     await waitForLiveTraffic(request, testId, readyTimeoutMs)
  118 |   } catch (error) {
  119 |     await abortLiveRun(request, run)
  120 |     throw error
  121 |   }
  122 |   return run
  123 | }
  124 | 
  125 | async function waitForLiveTraffic(request: APIRequestContext, testId: string, timeoutMs: number): Promise<void> {
  126 |   const deadline = Date.now() + timeoutMs
  127 |   let lastStatus = 'unknown'
  128 |   let lastTransactions = 0
  129 |   while (Date.now() < deadline) {
  130 |     const response = await request.get(`/api/v1/tests/${testId}`)
  131 |     if (response.ok()) {
  132 |       const test = (await response.json()) as {
  133 |         status?: string
  134 |         live_metrics?: { total_transactions?: number }
  135 |       }
  136 |       lastStatus = test.status || 'unknown'
  137 |       lastTransactions = test.live_metrics?.total_transactions ?? 0
  138 |       if (lastStatus === 'RUNNING' && lastTransactions > 0) return
  139 |       if (lastStatus === 'FAILED' || lastStatus === 'ABORTED' || lastStatus === 'COMPLETE') {
  140 |         throw new Error(`e2e run ${testId} reached ${lastStatus} before producing live metrics`)
  141 |       }
  142 |     }
  143 |     await new Promise(resolve => setTimeout(resolve, 500))
  144 |   }
  145 |   throw new Error(
  146 |     `e2e run ${testId} did not report live traffic within ${timeoutMs}ms (status=${lastStatus}, transactions=${lastTransactions})`,
  147 |   )
  148 | }
  149 | 
  150 | /**
  151 |  * Best-effort teardown. Never throws: a cleanup problem must not mask (or
  152 |  * manufacture) a spec failure, and the controller under test is disposable.
  153 |  *
  154 |  * It does not stay silent, though. A swallowed abort leaves the seeded run
  155 |  * driving load against the controller that the next spec asserts on, so every
  156 |  * failed call is reported on stderr with the step that failed — the run is
  157 |  * still leaked, but the leak is no longer invisible.
  158 |  */
  159 | export async function abortLiveRun(request: APIRequestContext, run: LiveRun): Promise<void> {
  160 |   const attempt = async (what: string, send: () => Promise<APIResponse>) => {
  161 |     try {
  162 |       const response = await send()
  163 |       if (!response.ok() && response.status() !== 404) {
  164 |         console.warn(`[liveRun] ${what} for ${run.testId} returned HTTP ${response.status()} ${response.statusText()}`)
  165 |       }
  166 |     } catch (error) {
  167 |       // Never rethrow: teardown must not manufacture a spec failure. Do report
  168 |       // it — this is exactly the path that leaks a running load test.
  169 |       console.warn(`[liveRun] ${what} for ${run.testId} failed: ${String(error)}`)
  170 |     }
  171 |   }
  172 |   await attempt('abort', () => request.post(`/api/v1/tests/${run.testId}/abort`))
  173 |   // An aborted run stays durably STOPPING until teardown and archive
  174 |   // finalization publish the terminal state, and the delete is rejected until
  175 |   // then. Give that a short window so repeated local runs do not accumulate
  176 |   // undeletable records.
  177 |   await waitForTerminalStatus(request, run.testId, 15_000)
  178 |   await attempt('delete test', () => request.delete(`/api/v1/tests/${run.testId}`))
  179 |   await attempt('delete script', () => request.delete(`/api/v1/scripts/${run.scriptId}`))
  180 | }
  181 | 
  182 | const TERMINAL_STATUSES = new Set(['COMPLETE', 'ABORTED', 'FAILED', 'CANCELLED'])
  183 | 
  184 | async function waitForTerminalStatus(request: APIRequestContext, testId: string, timeoutMs: number): Promise<void> {
  185 |   const deadline = Date.now() + timeoutMs
  186 |   while (Date.now() < deadline) {
  187 |     try {
  188 |       const response = await request.get(`/api/v1/tests/${testId}`)
  189 |       if (!response.ok()) return
  190 |       const status = ((await response.json()) as { status?: string }).status || ''
  191 |       if (TERMINAL_STATUSES.has(status)) return
  192 |     } catch {
  193 |       return
  194 |     }
  195 |     await new Promise(resolve => setTimeout(resolve, 500))
  196 |   }
  197 | }
  198 | 
  199 | export interface LiveRunFixtures {
  200 |   /**
  201 |    * Seeds a live controller run and registers it for automatic teardown.
  202 |    * Prefer this over calling `startLiveRun`/`abortLiveRun` by hand.
  203 |    */
  204 |   seedLiveRun: (options?: StartLiveRunOptions) => Promise<LiveRun>
  205 | }
  206 | 
  207 | /**
  208 |  * `test` with a `seedLiveRun` fixture whose teardown survives a test timeout.
  209 |  *
  210 |  * A `try/finally` in the test body does not: when Playwright times a test out
  211 |  * it stops awaiting the body and starts disposing fixtures, so the `finally`
  212 |  * runs against an already-closed `request` context and every teardown call
  213 |  * throws `Target page, context or browser has been closed`. With teardown
  214 |  * swallowed, the seeded run kept executing against the controller the next spec
  215 |  * asserts on. Verified with the repo's own Playwright: a body `finally` after a
  216 |  * timeout reached the server zero times, while this fixture's teardown reached
```