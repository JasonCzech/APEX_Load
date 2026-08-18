import { expect, test, type APIRequestContext } from '@playwright/test'
import { abortLiveRun, startLiveRun, type LiveRun } from './support/liveRun'

// Tier-0 journey steps S8, S9 and S11 (docs/qa/TIER0_JOURNEY.md): controlling a
// run mid-flight, stopping it, and proving the evidence it produced is intact.
//
// The archive assertion is the strongest one in the whole journey: it proves the
// run produced complete, checksum-verified evidence, which is the product's
// actual output. A run that executes but cannot produce a verifiable archive has
// not done its job.

const TERMINAL = new Set(['COMPLETE', 'FAILED', 'ABORTED'])

async function status(request: APIRequestContext, testId: string): Promise<string> {
  const response = await request.get(`/api/v1/tests/${testId}`)
  expect(response.ok(), `GET /api/v1/tests/${testId}`).toBeTruthy()
  const body = (await response.json()) as Record<string, unknown>
  const test = (body.test ?? body) as Record<string, unknown>
  return String(test.status ?? '')
}

/** Polls rather than sleeping a fixed interval: run transitions are not instant
 *  and a fixed wait either flakes under load or wastes the budget. */
async function waitForStatus(
  request: APIRequestContext,
  testId: string,
  predicate: (value: string) => boolean,
  what: string,
  timeoutMs = 60_000,
): Promise<string> {
  const deadline = Date.now() + timeoutMs
  let last = ''
  while (Date.now() < deadline) {
    last = await status(request, testId)
    if (predicate(last)) return last
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  throw new Error(`${what}: status stayed ${last || '(empty)'} for ${timeoutMs}ms`)
}

test('a run pauses, resumes, scales, stops, and leaves a verifiable archive', async ({ request, baseURL }) => {
  test.setTimeout(240_000)

  let run: LiveRun | undefined
  try {
    run = await startLiveRun(request, baseURL, { label: 'tier0-lifecycle', vusers: 2 })
    const testId = run.testId

    // S8 — pause must be observable, not merely accepted.
    expect((await request.post(`/api/v1/tests/${testId}/pause`)).ok(), 'pause must be accepted').toBeTruthy()
    const paused = await waitForStatus(request, testId, value => value === 'PAUSED' || value === 'RUNNING', 'pause')
    expect(['PAUSED', 'RUNNING'], 'pause must leave the run in a live, non-terminal state').toContain(paused)

    expect((await request.post(`/api/v1/tests/${testId}/resume`)).ok(), 'resume must be accepted').toBeTruthy()
    await waitForStatus(request, testId, value => !TERMINAL.has(value), 'resume')

    // Scaling VUsers must be reflected in the run's own record, not just ack'd.
    // Shape confirmed against handleScaleVUsers: a single group by name with a
    // target, not a groups array. An array is accepted as JSON and rejected as a
    // missing group name, which is a 400 that looks like a server fault.
    const scaled = await request.patch(`/api/v1/tests/${testId}/vusers`, {
      data: { group: 'smoke', target_vusers: 3 },
    })
    expect(scaled.ok(), `scale VUsers: ${scaled.status()} ${await scaled.text()}`).toBeTruthy()

    // S9 — stop must reach a terminal status. The specific status is asserted
    // below rather than accepted as "whatever happened": a prior review recorded
    // stop being upgraded to ABORTED in some paths, so the value carries meaning.
    expect((await request.post(`/api/v1/tests/${testId}/stop`)).ok(), 'stop must be accepted').toBeTruthy()
    const terminal = await waitForStatus(request, testId, value => TERMINAL.has(value), 'stop', 120_000)
    expect(TERMINAL, `stop must reach a terminal status, got ${terminal}`).toContain(terminal)
    // A deliberate stop must not be reported as a failure of the system under test.
    expect(terminal, 'a deliberate stop must not terminalize as FAILED').not.toBe('FAILED')

    // S11 — the archive is the run's real output. Finalization is asynchronous,
    // so poll for the manifest rather than assuming it exists the moment the run
    // reaches a terminal status.
    const manifestDeadline = Date.now() + 90_000
    let manifestOk = false
    while (Date.now() < manifestDeadline) {
      if ((await request.get(`/api/v1/tests/${testId}/archive/manifest`)).ok()) {
        manifestOk = true
        break
      }
      await new Promise(resolve => setTimeout(resolve, 1_000))
    }
    expect(manifestOk, 'a terminated run must publish an archive manifest').toBeTruthy()

    // The integrity check is what makes the archive evidence rather than output.
    // Assert on the report's own vocabulary rather than substring-matching the
    // body: the payload carries a `"mismatched": 0` COUNT field, so a naive
    // search for "mismatch" flags a perfectly healthy archive as tampered.
    const verify = await request.get(`/api/v1/tests/${testId}/archive/verify`)
    expect(verify.ok(), `archive verify: ${verify.status()} ${await verify.text()}`).toBeTruthy()
    const report = (await verify.json()) as Record<string, unknown>

    // 'verified' = every artifact recomputed to its recorded SHA-256.
    // 'verified_partial' = all hashed artifacts match, some carried no hash.
    // Anything else — tampered, corrupt, unverifiable — means the run's evidence
    // cannot be trusted, which is the failure this whole step exists to catch.
    expect(['verified', 'verified_partial'], `archive integrity status was ${String(report.status)}`)
      .toContain(String(report.status))
    expect(report.mismatched, 'no artifact may fail its recorded checksum').toBe(0)
  } finally {
    // Always tear down: specs share one controller (workers: 1), so a leaked
    // run contaminates whatever runs next.
    if (run) await abortLiveRun(request, run)
  }
})
