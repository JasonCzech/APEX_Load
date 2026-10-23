// @vitest-environment jsdom
// @ts-expect-error Vitest runs in Node; the dashboard app intentionally omits Node globals.
import { createHash } from 'node:crypto'
import { act, StrictMode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { TelemetryBudgetPolicy } from '../types/telemetryPolicy'
import type { TestSummary } from '../types'
import { TELEMETRY_ANY_BOUNDARY, telemetryPolicySegmentLabel, telemetryPolicySegmentsView, telemetryPolicyView } from './telemetryPolicyModel'
import TelemetryPolicy from './TelemetryPolicy'
import { sendingChanges, unansweredChanges } from './telemetryBudgetChangeState'

const apiMock = vi.hoisted(() => ({ list: vi.fn(), append: vi.fn() }))
const stored = (segment: unknown, replayed = false) => ({ segment, replayed })
vi.mock('../hooks/api/telemetryPolicySegmentsApi', () => ({ telemetryPolicySegmentsApi: apiMock }))
vi.mock('./TelemetryPolicySelect', () => ({
  default: ({ value, onChange }: { value: string; onChange: (value: string) => void }) => (
    <select data-testid="budget-select" value={value} onChange={event => onChange(event.target.value)}>
      <option value="">Threshold settings</option><option value="tight-budgets">Tight</option>
    </select>
  ),
}))

import TestExecutionStatusPanels from './TestExecutionStatusPanels'
import { TelemetryPolicySegmentList } from './TelemetryPolicySegments'
import { ApiError } from '../hooks/apiClient'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// The controller's encoding of a receipt (Go struct order, hash and capture
// time cleared), built independently of the model.
function sealed(policy: Omit<TelemetryBudgetPolicy, 'hash'>): TelemetryBudgetPolicy {
  // Go encodes a negative zero limit as -0.
  const budgets = policy.budgets.map(budget => `{"stage":"${budget.stage}","enabled":${budget.enabled},"limit_ms":${Object.is(budget.limit_ms, -0) ? '-0' : budget.limit_ms}}`).join(',')
  const definition = policy.definition_sha256 ? `"definition_sha256":"${policy.definition_sha256}",` : ''
  const input = `{"schema_version":"${policy.schema_version}","policy_id":"${policy.policy_id}","revision":${policy.revision},${definition}"hash":"","captured_at":"0001-01-01T00:00:00Z","capture_boundary":"${policy.capture_boundary}","classification_algorithm":"${policy.classification_algorithm}","expected_controller_publication_interval_ms":${policy.expected_controller_publication_interval_ms},"cadence_scope":"${policy.cadence_scope}","sustained_breach_window_ms":${policy.sustained_breach_window_ms},"coverage_rule":"${policy.coverage_rule}","budgets":[${budgets}]}`
  return { ...policy, hash: createHash('sha256').update(input).digest('hex') }
}

const base: Omit<TelemetryBudgetPolicy, 'hash'> = {
  schema_version: 'apexload.telemetry_policy.v1', policy_id: 'system-telemetry-budgets', revision: 1, captured_at: '2026-10-23T12:00:00Z',
  capture_boundary: 'execution-admission-before-staging', classification_algorithm: 'legacy-mixed-diagnostic.v1', expected_controller_publication_interval_ms: 1000,
  cadence_scope: 'local-controller-ticker', sustained_breach_window_ms: 15000, coverage_rule: 'retained-classifications-only;gaps-unknown',
  budgets: [{ stage: 'sample_age', enabled: true, limit_ms: 2000 }, { stage: 'generator_processing', enabled: true, limit_ms: 500 }, { stage: 'transport', enabled: true, limit_ms: 500 }, { stage: 'controller_fanout', enabled: true, limit_ms: 500 }, { stage: 'browser_render', enabled: true, limit_ms: 500 }],
}
const admission = () => ({ sequence: 1, effective_at: '2026-10-23T12:00:00Z', source: 'admission' as const, policy_id: 'system-telemetry-budgets', policy: sealed(base) })
const tightened = () => ({ sequence: 2, effective_at: '2026-10-23T12:05:00Z', source: 'run_selection' as const, policy_id: 'tight-budgets', requested_by: 'Editor',
  policy: sealed({ ...base, schema_version: 'apexload.telemetry_policy.v2', policy_id: 'tight-budgets', revision: 3, definition_sha256: 'ab'.repeat(32), capture_boundary: 'in-run-policy-segment', captured_at: '2026-10-23T12:05:00Z',
    budgets: base.budgets.map(budget => budget.stage === 'generator_processing' ? { ...budget, limit_ms: 250 } : budget) }) })
// A threshold-settings segment carries no policy_id, as the controller sends it.
const relaxed = () => ({ sequence: 3, effective_at: '2026-10-23T12:10:00Z', source: 'system_settings' as const,
  policy: sealed({ ...base, capture_boundary: 'in-run-policy-segment', captured_at: '2026-10-23T12:10:00Z', expected_controller_publication_interval_ms: 5000 }) })

const digestStub = { subtle: { digest: async (_algorithm: string, data: Uint8Array) => Uint8Array.from(createHash('sha256').update(data).digest()).buffer }, randomUUID: vi.fn(() => 'request-1') }

describe('telemetry policy segments (TEL-B)', () => {
  beforeEach(() => vi.stubGlobal('crypto', digestStub))
  afterEach(() => vi.unstubAllGlobals())

  it('accepts a receipt captured for an in-run segment where segments are allowed, and no other boundary', async () => {
    expect((await telemetryPolicyView({ availability: 'available', policy: relaxed().policy }, TELEMETRY_ANY_BOUNDARY)).policy?.capture_boundary).toBe('in-run-policy-segment')
    // Admission evidence (the default) stays strict.
    expect((await telemetryPolicyView({ availability: 'available', policy: relaxed().policy })).policy).toBeUndefined()
    expect((await telemetryPolicyView({ availability: 'available', policy: admission().policy })).policy).toBeDefined()
    const other = sealed({ ...base, capture_boundary: 'run-end' as never })
    expect((await telemetryPolicyView({ availability: 'available', policy: other }, TELEMETRY_ANY_BOUNDARY)).policy).toBeUndefined()
  })

  it("describes a segment receipt as a change made while the run ran, and refuses it as admission evidence", async () => {
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    await act(async () => root.render(<TelemetryPolicy evidence={{ availability: 'available', policy: relaxed().policy }} acceptSegments segmentSequence={3} />))
    await act(async () => { await Promise.resolve() })
    expect(container.textContent).toContain('Segment 3, changed while the run ran')
    expect(container.textContent).toContain('Threshold settings when the policy changed')
    expect(container.textContent).not.toContain('captured before runtime staging')
    await act(async () => root.render(<TelemetryPolicy evidence={{ availability: 'available', policy: tightened().policy }} acceptSegments />))
    await act(async () => { await Promise.resolve() })
    expect(container.textContent).toContain("Captured when the run's policy changed while it ran")
    expect(container.textContent).toContain('Project policy tight-budgets · revision 3 (chosen while the run ran)')
    await act(async () => root.render(<TelemetryPolicy evidence={{ availability: 'available', policy: tightened().policy }} />))
    await act(async () => { await Promise.resolve() })
    expect(container.textContent).toContain('invalid or unsupported')
    await act(async () => root.unmount())
    container.remove()
  })

  it('says why no receipt is shown while the run\'s segments cannot be read', async () => {
    expect((await telemetryPolicyView({ availability: 'unavailable', reason: 'policy-segments-unavailable' }, TELEMETRY_ANY_BOUNDARY)).reason).toContain('cannot be read now')
    // Live evidence only: an admission view refuses it.
    expect((await telemetryPolicyView({ availability: 'unavailable', reason: 'policy-segments-unavailable' })).reason).toBe('The retained policy receipt is invalid or unsupported.')
  })

  it('verifies segments in order, the admission receipt first, and refuses anything else whole', async () => {
    const view = await telemetryPolicySegmentsView([admission(), tightened(), relaxed()])
    expect(view.segments?.map(segment => segment.sequence)).toEqual([1, 2, 3])
    expect(view.segments?.map(telemetryPolicySegmentLabel)).toEqual(['Admission receipt', 'Policy tight-budgets revision 3', 'Threshold settings'])
    for (const broken of [
      [tightened(), admission()],
      [admission(), { ...tightened(), sequence: 3 }],
      [admission(), { ...tightened(), source: 'admission' }],
      [{ ...admission(), policy: tightened().policy }],
      [admission(), { ...tightened(), policy: admission().policy }],
      [admission(), { ...tightened(), policy: { ...tightened().policy, budgets: base.budgets } }],
      [admission(), { ...tightened(), secret: 'x' }],
      [{ ...admission(), policy: null }, tightened()],
      [admission(), { ...tightened(), policy: undefined }],
      // The source and policy ID agree with the receipt.
      [admission(), { ...tightened(), source: 'system_settings' }],
      [admission(), { ...relaxed(), sequence: 2, source: 'run_selection' }],
      [admission(), { ...tightened(), policy_id: 'other-policy' }],
    ]) {
      expect((await telemetryPolicySegmentsView(broken)).segments).toBeUndefined()
    }
  })
})

describe('telemetry policy segments in the run page (TEL-B)', () => {
  let container: HTMLDivElement
  let root: Root
  const running = (segments?: unknown[]) => ({ id: 'run-1', name: 'Run', status: 'RUNNING', project_id: 'project-a', groups: [], total_vusers: 1, duration: '1m', created_at: '2026-10-23T12:00:00Z', telemetry_policy_segments: segments } as unknown as TestSummary)
  const render = async (test: TestSummary, canChange = true, strict = false) => {
    const panels = <TestExecutionStatusPanels isGoalOriented={false} goalDescription="" test={test} metrics={{ telemetry_policy: { availability: 'available', policy: admission().policy } }}
      isMultiGroup={false} groupMetrics={[]} groupColors={[]} canChangeTelemetryPolicy={canChange} />
    await act(async () => root.render(strict ? <StrictMode>{panels}</StrictMode> : panels))
    await act(async () => { await Promise.resolve() })
  }
  const click = async (testId: string) => {
    await act(async () => { container.querySelector<HTMLButtonElement>(`[data-testid="${testId}"]`)!.click() })
    await act(async () => { await Promise.resolve() })
  }
  beforeEach(() => {
    vi.stubGlobal('crypto', digestStub)
    let requests = 0
    digestStub.randomUUID.mockImplementation(() => `request-${++requests}`)
    apiMock.list.mockReset()
    apiMock.append.mockReset()
    container = document.createElement('div')
    document.body.append(container)
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.unstubAllGlobals()
    // Each test starts with no change kept or being sent for its run.
    unansweredChanges.clear()
    sendingChanges.clear()
  })

  it("lists a run's segments once its policy changed, and nothing before", async () => {
    await render(running())
    expect(container.querySelector('[data-testid="telemetry-policy-segments"]')).toBeNull()
    await render(running([admission(), tightened()]))
    const list = container.querySelector('[data-testid="telemetry-policy-segments"]')!
    expect(list.textContent).toContain('Admission receipt')
    expect(list.textContent).toContain('Policy tight-budgets revision 3 · by Editor')
    expect(list.textContent).toContain(tightened().policy.hash)
    expect(list.textContent).toContain("an interval a change fell inside, or classified while the run's segments could not be read, belongs to no segment")
  })

  it('offers the change only to an editor of a running run', async () => {
    await render(running(), false)
    expect(container.querySelector('[data-testid="telemetry-budget-change-open"]')).toBeNull()
    await render({ ...running(), status: 'COMPLETE' } as TestSummary)
    expect(container.querySelector('[data-testid="telemetry-budget-change-open"]')).toBeNull()
    await render(running())
    expect(container.querySelector('[data-testid="telemetry-budget-change-open"]')).not.toBeNull()
  })

  it('changes the budgets after a confirm step, with the latest sequence and one request ID across a retry', async () => {
    apiMock.append.mockRejectedValueOnce(new Error('network down'))
    apiMock.append.mockResolvedValueOnce(stored(tightened()))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened()] })
    await render(running())
    await click('telemetry-budget-change-open')
    const select = container.querySelector<HTMLSelectElement>('[data-testid="budget-select"]')!
    await act(async () => { select.value = 'tight-budgets'; select.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(apiMock.append).not.toHaveBeenCalled()
    await click('telemetry-budget-change-review')
    expect(container.textContent).toContain('classified by policy tight-budgets')
    await click('telemetry-budget-change-confirm')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('network down')
    // The lost response's change committed: polling now shows segment 2. The
    // retry still names the original submission, so the controller returns
    // that change rather than appending another.
    await render(running([admission(), tightened()]))
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append).toHaveBeenCalledTimes(2)
    expect(apiMock.append.mock.calls[0]).toEqual(['run-1', { request_id: 'request-1', telemetry_budget_policy_id: 'tight-budgets', expected_sequence: 1 }])
    expect(apiMock.append.mock.calls[1]).toEqual(['run-1', { request_id: 'request-1', telemetry_budget_policy_id: 'tight-budgets', expected_sequence: 1 }])
    expect(digestStub.randomUUID).toHaveBeenCalledTimes(1)
    // The run's segments are read again and listed, and the change is announced.
    expect(container.querySelector('[data-testid="telemetry-policy-segments"]')?.textContent).toContain('Policy tight-budgets revision 3')
    expect(container.querySelector('[data-testid="telemetry-budget-change-notice"]')?.textContent).toBe('The change was stored as segment 2.')
  })

  it('sends the threshold settings and a local cadence, never twice at once', async () => {
    let resolve: (value: unknown) => void = () => {}
    apiMock.append.mockReturnValueOnce(new Promise(done => { resolve = done }))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened(), relaxed()] })
    await render(running([admission(), tightened()]))
    await click('telemetry-budget-change-open')
    // Opening moves focus to the control's heading.
    expect(document.activeElement?.textContent).toBe('Change telemetry budgets')
    expect(document.activeElement?.tagName).toBe('H3')
    const cadence = container.querySelector<HTMLInputElement>('[data-testid="telemetry-budget-change"] input')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(cadence, '0')
      cadence.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.textContent).toContain('whole number of seconds from 1 to 3600')
    // The error is linked to its input.
    const describedBy = cadence.getAttribute('aria-describedby')
    expect(describedBy && container.querySelector(`[id="${describedBy}"]`)?.textContent).toContain('whole number of seconds')
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(cadence, '5')
      cadence.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await click('telemetry-budget-change-review')
    // The confirm text names the cadence change.
    expect(container.textContent).toContain('the current threshold settings, published every 5 s.')
    const confirm = container.querySelector<HTMLButtonElement>('[data-testid="telemetry-budget-change-confirm"]')!
    await act(async () => { confirm.click(); confirm.click() })
    expect(apiMock.append).toHaveBeenCalledTimes(1)
    expect(apiMock.append.mock.calls[0][1]).toEqual({ request_id: 'request-1', telemetry_budget_policy_id: 'project_default', expected_sequence: 2, controller_publication_interval_seconds: 5 })
    await act(async () => { resolve(stored(relaxed())); await Promise.resolve() })
  })

  it("keeps the reviewed sequence when another change is polled during review", async () => {
    apiMock.append.mockRejectedValueOnce(new ApiError('the run\'s telemetry policy segments changed', 409))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened()] })
    await render(running())
    await click('telemetry-budget-change-open')
    await click('telemetry-budget-change-review')
    // Someone else's change arrives by polling while this one is reviewed.
    await render(running([admission(), tightened()]))
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append.mock.calls[0][1].expected_sequence).toBe(1)
    // After the 409, a new review builds on the reloaded segments.
    await click('telemetry-budget-change-review')
    apiMock.append.mockResolvedValueOnce(stored(relaxed()))
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append.mock.calls[1][1].expected_sequence).toBe(2)
    expect(digestStub.randomUUID).toHaveBeenCalledTimes(2)
  })

  it('keeps the submission through Back and Cancel after a lost answer, so a retry never appends another', async () => {
    apiMock.append.mockRejectedValueOnce(new Error('network down'))
    apiMock.append.mockResolvedValueOnce(stored(tightened()))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened()] })
    await render(running())
    await click('telemetry-budget-change-open')
    const select = container.querySelector<HTMLSelectElement>('[data-testid="budget-select"]')!
    await act(async () => { select.value = 'tight-budgets'; select.dispatchEvent(new Event('change', { bubbles: true })) })
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    // The change committed but its answer was lost; polling shows it. Back,
    // then Cancel and reopen, then review the same change again.
    await render(running([admission(), tightened()]))
    await act(async () => { Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Back')!.click() })
    await act(async () => { Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Cancel')!.click() })
    await click('telemetry-budget-change-open')
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append).toHaveBeenCalledTimes(2)
    expect(apiMock.append.mock.calls[1][1]).toEqual(apiMock.append.mock.calls[0][1])
    expect(digestStub.randomUUID).toHaveBeenCalledTimes(1)
  })

  it('reviews afresh, against the latest segment, a change never sent', async () => {
    apiMock.append.mockResolvedValueOnce(stored(relaxed()))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened(), relaxed()] })
    await render(running())
    await click('telemetry-budget-change-open')
    await click('telemetry-budget-change-review')
    await act(async () => { Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Back')!.click() })
    await act(async () => { Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Cancel')!.click() })
    // A colleague's change arrives; the unsent review is not reused.
    await render(running([admission(), tightened()]))
    await click('telemetry-budget-change-open')
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append.mock.calls[0][1].expected_sequence).toBe(2)
  })

  it('keeps a sent submission when an edit is undone after a lost answer', async () => {
    apiMock.append.mockRejectedValueOnce(new Error('network down'))
    apiMock.append.mockResolvedValueOnce(stored(tightened()))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened()] })
    await render(running())
    await click('telemetry-budget-change-open')
    const select = () => container.querySelector<HTMLSelectElement>('[data-testid="budget-select"]')!
    const choose = async (value: string) => act(async () => { select().value = value; select().dispatchEvent(new Event('change', { bubbles: true })) })
    await choose('tight-budgets')
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    await render(running([admission(), tightened()]))
    await choose('')
    await choose('tight-budgets')
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append.mock.calls[1][1]).toEqual(apiMock.append.mock.calls[0][1])
  })

  it('retries a sent change under its submission after another change is reviewed meanwhile', async () => {
    apiMock.append.mockRejectedValueOnce(new Error('network down'))
    apiMock.append.mockResolvedValueOnce(stored(tightened()))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened()] })
    await render(running())
    await click('telemetry-budget-change-open')
    const select = () => container.querySelector<HTMLSelectElement>('[data-testid="budget-select"]')!
    const choose = async (value: string) => act(async () => { select().value = value; select().dispatchEvent(new Event('change', { bubbles: true })) })
    await choose('tight-budgets')
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    // The change committed but its answer was lost; polling shows it. The
    // threshold settings are reviewed and abandoned, then the change again.
    await render(running([admission(), tightened()]))
    await choose('')
    await click('telemetry-budget-change-review')
    await act(async () => { Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Back')!.click() })
    await choose('tight-budgets')
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append).toHaveBeenCalledTimes(2)
    expect(apiMock.append.mock.calls[1][1]).toEqual(apiMock.append.mock.calls[0][1])
    expect(digestStub.randomUUID).toHaveBeenCalledTimes(2)
  })

  it('retries a sent change under its submission after the control remounts', async () => {
    apiMock.append.mockRejectedValueOnce(new Error('network down'))
    apiMock.append.mockResolvedValueOnce(stored(tightened()))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened()] })
    await render(running())
    await click('telemetry-budget-change-open')
    const select = () => container.querySelector<HTMLSelectElement>('[data-testid="budget-select"]')!
    await act(async () => { select().value = 'tight-budgets'; select().dispatchEvent(new Event('change', { bubbles: true })) })
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    // The run's live metrics drop out and return: a new control.
    await act(async () => root.unmount())
    root = createRoot(container)
    await render(running([admission(), tightened()]))
    await click('telemetry-budget-change-open')
    await act(async () => { select().value = 'tight-budgets'; select().dispatchEvent(new Event('change', { bubbles: true })) })
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append.mock.calls[1][1]).toEqual(apiMock.append.mock.calls[0][1])
    expect(digestStub.randomUUID).toHaveBeenCalledTimes(1)
  })

  it('reviews a kept change afresh once a later change than it could have made is in force', async () => {
    const third = { ...relaxed(), sequence: 3 }
    apiMock.append.mockRejectedValueOnce(new Error('network down'))
    apiMock.append.mockResolvedValueOnce(stored({ ...tightened(), sequence: 4 }))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened(), third] })
    await render(running())
    await click('telemetry-budget-change-open')
    const select = () => container.querySelector<HTMLSelectElement>('[data-testid="budget-select"]')!
    await act(async () => { select().value = 'tight-budgets'; select().dispatchEvent(new Event('change', { bubbles: true })) })
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    // The change was stored as segment 2, then another made segment 3: the
    // same choice is now a new change from segment 3, not a retry.
    await render(running([admission(), tightened(), third]))
    await act(async () => { Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Back')!.click() })
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append.mock.calls[1][1]).toEqual({ request_id: 'request-2', telemetry_budget_policy_id: 'tight-budgets', expected_sequence: 3 })
  })

  it('never resends a kept change from the confirm step once a later change is in force', async () => {
    const third = { ...relaxed(), sequence: 3 }
    apiMock.append.mockRejectedValueOnce(new Error('network down'))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened(), third] })
    await render(running())
    await click('telemetry-budget-change-open')
    const select = () => container.querySelector<HTMLSelectElement>('[data-testid="budget-select"]')!
    await act(async () => { select().value = 'tight-budgets'; select().dispatchEvent(new Event('change', { bubbles: true })) })
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    await render(running([admission(), tightened(), third]))
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('Another change was made meanwhile.')
    apiMock.append.mockResolvedValueOnce(stored({ ...tightened(), sequence: 4 }))
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append.mock.calls[1][1]).toEqual({ request_id: 'request-2', telemetry_budget_policy_id: 'tight-budgets', expected_sequence: 3 })
  })

  it('says when a retried change has been replaced since it was stored', async () => {
    const third = { ...relaxed(), sequence: 3 }
    apiMock.append.mockRejectedValueOnce(new Error('network down'))
    apiMock.append.mockResolvedValueOnce(stored(tightened(), true))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened(), third] })
    await render(running())
    await click('telemetry-budget-change-open')
    const select = () => container.querySelector<HTMLSelectElement>('[data-testid="budget-select"]')!
    await act(async () => { select().value = 'tight-budgets'; select().dispatchEvent(new Event('change', { bubbles: true })) })
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    // Polling still shows segment 2 when the retry is confirmed.
    await render(running([admission(), tightened()]))
    await click('telemetry-budget-change-confirm')
    await act(async () => { await Promise.resolve() })
    expect(apiMock.append.mock.calls[1][1]).toEqual(apiMock.append.mock.calls[0][1])
    expect(container.querySelector('[data-testid="telemetry-budget-change-notice"]')?.textContent).toBe('The change was stored as segment 2; segment 3 has replaced it since.')
    // Opening the control again clears it, so it is gone once closed again.
    // The live region is one node, present and shown, open or closed.
    const region = container.querySelector('[data-testid="telemetry-budget-change-notice"]')
    await click('telemetry-budget-change-open')
    expect(container.querySelector('[data-testid="telemetry-budget-change-notice"]')).toBe(region)
    expect((region as HTMLElement).hidden).toBe(false)
    await act(async () => { Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Cancel')!.click() })
    expect(container.querySelector('[data-testid="telemetry-budget-change-notice"]')).toBe(region)
    expect(region?.textContent).toBe('')
    expect((region as HTMLElement).hidden).toBe(false)
    // Closing returns focus to the open button.
    expect(document.activeElement?.getAttribute('data-testid')).toBe('telemetry-budget-change-open')
  })

  it('never confirms a reviewed change once two later changes are in force', async () => {
    const third = { ...relaxed(), sequence: 3 }
    await render(running())
    await click('telemetry-budget-change-open')
    await click('telemetry-budget-change-review')
    // Never sent, reviewed against segment 1; segments 2 and 3 arrive.
    await render(running([admission(), tightened(), third]))
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Another change was made meanwhile.')
  })

  it('says a retried change could not be confirmed in force when the segments cannot be read', async () => {
    apiMock.append.mockRejectedValueOnce(new Error('network down'))
    apiMock.append.mockResolvedValueOnce(stored(tightened(), true))
    apiMock.list.mockRejectedValue(new Error('network down'))
    await render(running())
    await click('telemetry-budget-change-open')
    const select = () => container.querySelector<HTMLSelectElement>('[data-testid="budget-select"]')!
    await act(async () => { select().value = 'tight-budgets'; select().dispatchEvent(new Event('change', { bubbles: true })) })
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    await click('telemetry-budget-change-confirm')
    await act(async () => { await Promise.resolve() })
    expect(container.querySelector('[data-testid="telemetry-budget-change-notice"]')?.textContent).toBe("The change was stored earlier as segment 2; the run's latest segments could not be read to confirm it is still in force.")
  })

  it('never sends a second change from a remounted control while one is being sent', async () => {
    let answer: (value: unknown) => void = () => {}
    apiMock.append.mockReturnValueOnce(new Promise(resolve => { answer = resolve }))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened()] })
    await render(running())
    await click('telemetry-budget-change-open')
    const select = () => container.querySelector<HTMLSelectElement>('[data-testid="budget-select"]')!
    await act(async () => { select().value = 'tight-budgets'; select().dispatchEvent(new Event('change', { bubbles: true })) })
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    await act(async () => root.unmount())
    root = createRoot(container)
    await render(running([admission(), tightened()]))
    await click('telemetry-budget-change-open')
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('A change to this run is still being sent.')
    await act(async () => { answer(stored(tightened())) })
  })

  it('reviews a refused change afresh', async () => {
    apiMock.append.mockRejectedValueOnce(new ApiError('the policy cannot be used', 400))
    apiMock.append.mockResolvedValueOnce(stored(tightened()))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened()] })
    await render(running())
    await click('telemetry-budget-change-open')
    const select = () => container.querySelector<HTMLSelectElement>('[data-testid="budget-select"]')!
    await act(async () => { select().value = 'tight-budgets'; select().dispatchEvent(new Event('change', { bubbles: true })) })
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    expect(container.textContent).toContain('the policy cannot be used')
    // A refusal leaves the confirm step and reads the run's segments again.
    expect(container.querySelector('[data-testid="telemetry-budget-change-confirm"]')).toBeNull()
    expect(apiMock.list).toHaveBeenCalledTimes(1)
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append.mock.calls[1][1].request_id).toBe('request-2')
  })

  it('names the latest segment whose receipt is in force when a policy is chosen again', async () => {
    const again = { ...tightened(), sequence: 4, effective_at: '2026-10-23T12:15:00Z' }
    const test = running([admission(), tightened(), relaxed(), again])
    await act(async () => root.render(<TestExecutionStatusPanels isGoalOriented={false} goalDescription="" test={test} metrics={{ telemetry_policy: { availability: 'available', policy: again.policy } }}
      isMultiGroup={false} groupMetrics={[]} groupColors={[]} canChangeTelemetryPolicy={false} />))
    await act(async () => { await Promise.resolve(); await Promise.resolve() })
    expect(container.textContent).toContain('Segment 4')
    expect(container.textContent).not.toContain('Segment 2,')
    // A receipt the list has not caught up with is named by no segment.
    await act(async () => root.render(<TestExecutionStatusPanels isGoalOriented={false} goalDescription="" test={running([admission(), tightened(), relaxed()])} metrics={{ telemetry_policy: { availability: 'available', policy: again.policy } }}
      isMultiGroup={false} groupMetrics={[]} groupColors={[]} canChangeTelemetryPolicy={false} />))
    await act(async () => { await Promise.resolve(); await Promise.resolve() })
    expect(container.textContent).not.toContain('Segment 2,')
    expect(container.textContent).not.toContain('Segment 4')
  })

  it('makes a request ID without randomUUID and still completes', async () => {
    vi.stubGlobal('crypto', { subtle: digestStub.subtle })
    apiMock.append.mockResolvedValueOnce(stored(relaxed()))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened(), relaxed()] })
    await render(running())
    await click('telemetry-budget-change-open')
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append.mock.calls[0][1].request_id).toMatch(/^segment-/)
    expect(container.querySelector('[data-testid="telemetry-budget-change"]')).toBeNull()
  })

  it('completes a change under StrictMode remounting', async () => {
    apiMock.append.mockResolvedValueOnce(stored(relaxed()))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened(), relaxed()] })
    await render(running(), true, true)
    await click('telemetry-budget-change-open')
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    expect(apiMock.append).toHaveBeenCalledTimes(1)
    expect(container.querySelector('[data-testid="telemetry-budget-change"]')).toBeNull()
    for (let flush = 0; flush < 5; flush++) await act(async () => { await Promise.resolve() })
    expect(container.querySelector('[data-testid="telemetry-policy-segments"]')?.textContent).toContain('Threshold settings')
  })

  it('reloads the segments and shows the refusal when the change conflicts', async () => {
    apiMock.append.mockRejectedValueOnce(new ApiError('the run\'s telemetry policy segments changed', 409))
    apiMock.list.mockResolvedValue({ segments: [admission(), tightened()] })
    await render(running())
    await click('telemetry-budget-change-open')
    await click('telemetry-budget-change-review')
    await click('telemetry-budget-change-confirm')
    expect(apiMock.list).toHaveBeenCalledWith('run-1')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('segments changed')
    expect(container.querySelector('[data-testid="telemetry-policy-segments"]')?.textContent).toContain('Policy tight-budgets revision 3')
  })
})

describe('archived telemetry policy segments (TEL-B)', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => { vi.stubGlobal('crypto', digestStub); container = document.createElement('div'); document.body.append(container); root = createRoot(container) })
  afterEach(async () => { await act(async () => root.unmount()); container.remove(); vi.unstubAllGlobals() })

  it('verifies the list as sent, a -0 limit included, and keeps it verified across an unchanged poll', async () => {
    const zero = { ...tightened(), policy: sealed({ ...tightened().policy, budgets: base.budgets.map(budget => budget.stage === 'browser_render' ? { ...budget, enabled: false, limit_ms: -0 } : budget) }) }
    const segments = [admission(), zero]
    await act(async () => root.render(<TelemetryPolicySegmentList segments={segments} />))
    await act(async () => { await Promise.resolve(); await Promise.resolve() })
    expect(container.textContent).toContain('Policy tight-budgets revision 3')
    // The same content polled again: no re-verification state shown.
    await act(async () => root.render(<TelemetryPolicySegmentList segments={structuredClone(segments)} />))
    expect(container.textContent).not.toContain('Verifying')
    expect(container.textContent).toContain('Policy tight-budgets revision 3')
  })

  it('lists the archived segments', async () => {
    await act(async () => root.render(<TelemetryPolicySegmentList segments={[admission(), tightened(), relaxed()]} />))
    await act(async () => { await Promise.resolve() })
    expect(container.textContent).toContain('Threshold settings')
    expect(container.textContent).toContain('5,000 ms')
  })

  it('lists a segment recorded without a valid receipt with the others', async () => {
    const recorded = [admission(), { ...tightened(), policy: null }, relaxed()]
    const view = await telemetryPolicySegmentsView(recorded)
    expect(view.segments?.map(telemetryPolicySegmentLabel)).toEqual(['Admission receipt', 'Policy tight-budgets (receipt invalid)', 'Threshold settings'])
    await act(async () => root.render(<TelemetryPolicySegmentList segments={recorded} />))
    await act(async () => { await Promise.resolve() })
    expect(container.textContent).toContain('Receipt invalid')
    expect(container.textContent).toContain('5,000 ms')
  })
})
