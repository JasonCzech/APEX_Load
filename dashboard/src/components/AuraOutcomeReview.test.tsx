// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AuraOutcomeBatch, AuraOutcomeReport } from '../types/scriptImports'
import AuraOutcomeReview from './AuraOutcomeReview'

let container: HTMLDivElement
let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove() })
const batch = (sequence: number, changes: Partial<AuraOutcomeBatch> = {}): AuraOutcomeBatch => ({
  action_sequence: sequence, action_path: `actions[${sequence - 1}]`, har_entry_id: sequence + 5, request_valid: true,
  analysis: { success: true, complete: true, has_response: true, request_actions: 1, response_actions: 1, matched_actions: 1, outcomes: [{ ordinal: 1, state: 'SUCCESS' }] }, ...changes,
})
const report = (changes: Partial<AuraOutcomeReport> = {}): AuraOutcomeReport => ({
  complete: true, total_batches: 1, inspected_batches: 1, complete_batches: 1, batch_limit: 512, analysis_byte_limit: 64 * 1024 * 1024,
  omitted_batches: 0, incomplete_batches: 0, missing_responses: 0, invalid_requests: 0,
  request_actions: 1, response_actions: 1, matched_actions: 1, state_counts: { SUCCESS: 1 }, batches: [batch(1)], ...changes,
})
const render = (value: AuraOutcomeReport) => act(() => { root.render(<AuraOutcomeReview report={value} />) })
const findButton = (name: string) => [...container.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === name)!

describe('AuraOutcomeReview', () => {
  it('distinguishes complete captured failures from successful replay and orders safe action ordinals', () => {
    const failed = batch(1, { analysis: { success: false, complete: true, has_response: true, request_actions: 2, response_actions: 2, matched_actions: 2, outcomes: [{ ordinal: 1, state: 'SUCCESS' }, { ordinal: 2, state: 'ERROR' }], issues: [{ code: 'non_success_state', ordinal: 2 }] } })
    render(report({ request_actions: 2, response_actions: 2, matched_actions: 2, state_counts: { SUCCESS: 1, ERROR: 1 }, batches: [failed] }))
    expect(container.textContent).toContain('Capture inspection complete. 1 captured action did not succeed.')
    expect(container.textContent).toContain('Captured failure')
    expect(container.textContent).toContain('HTTP 200 does not establish Aura action success')
    expect(container.textContent).toContain('Action 2: ERROR')
    expect(container.textContent).toContain('2 matched / 2 requested / 2 returned actions')
    expect(container.textContent).not.toContain('Captured success')
  })

  it('does not interpret request-only success as a response or silently hide omitted evidence', () => {
    render(report({ complete: false, total_batches: 3, complete_batches: 0, omitted_batches: 2, missing_responses: 1, incomplete_batches: 1, response_actions: 0, matched_actions: 0, state_counts: {}, batches: [batch(1, { analysis: { success: true, complete: true, has_response: false, request_actions: 1, response_actions: 0, matched_actions: 0 } })] }))
    expect(container.textContent).toContain('Capture inspection incomplete.')
    expect(container.textContent).toContain('No response captured')
    expect(container.textContent).toContain('2 batches omitted by inspection limits')
    expect(container.textContent).toContain('512 batches · 64 MiB')
    expect(container.textContent).not.toContain('Captured success')
  })

  it('separates invalid imported requests from complete captured envelopes and keeps omitted evidence incomplete', () => {
    render(report({ complete: true, omitted_batches: 1, total_batches: 2, incomplete_batches: 1, invalid_requests: 1, batches: [batch(1, { request_valid: false, request_issues: [{ code: 'invalid_form' }] })] }))
    expect(container.textContent).toContain('Capture inspection incomplete.')
    expect(container.textContent).toContain('Imported request findings')
    expect(container.textContent).toContain('Request form could not be inspected')
    expect(container.textContent).not.toContain('Captured envelope findings')
    expect(container.textContent).toContain('Imported request structure needs review')
    expect(container.textContent).not.toContain('Captured success')
  })

  it('distinguishes omitted retained details from capture completeness and keeps full counts visible', () => {
    render(report({ details_complete: false, outcomes_omitted: 255, issues_omitted: 12, request_issues_omitted: 3, outcome_detail_limit: 2048, issue_detail_limit: 2048, request_actions: 256, response_actions: 256, matched_actions: 256, state_counts: { SUCCESS: 256 }, batches: [batch(1, { request_issues_omitted: 3, analysis: { success: true, complete: true, has_response: true, request_actions: 256, response_actions: 256, matched_actions: 256, outcomes: [{ ordinal: 1, state: 'SUCCESS' }], outcomes_omitted: 255, issues_omitted: 12 } })] }))
    expect(container.textContent).toContain('Capture inspection complete.')
    expect(container.textContent).toContain('Detailed rows limited')
    expect(container.textContent).toContain('255 action outcomes · 12 captured findings · 3 imported request findings omitted')
    expect(container.textContent).toContain('Aggregate counts and inspection results include these inspected rows')
    expect(container.textContent).toContain('256 matched / 256 requested / 256 returned actions')
    expect(container.textContent).toContain('255 action outcomes · 12 captured findings omitted from this batch')
    expect(container.textContent).toContain('3 imported request findings omitted from this batch')
    expect(container.textContent).toContain('2048 action outcomes · 2048 findings across all batches')
  })

  it('renders unknown codes and states with constant labels without source values or prototype keys', () => {
    const unsafe = 'private-token-descriptor-error-payload'
    const captured = batch(1, { analysis: { success: false, complete: false, has_response: true, request_actions: 1, response_actions: 1, matched_actions: 1, outcomes: [{ ordinal: 1, state: unsafe as 'UNKNOWN' }], issues: [{ code: unsafe }, { code: 'constructor' }, { code: '__proto__' }] } })
    render(report({ complete: false, incomplete_batches: 1, state_counts: { UNKNOWN: 1 }, issue_counts: { [unsafe]: 1, constructor: 1 }, batches: [captured] }))
    expect(container.textContent).toContain('UNKNOWN')
    expect(container.textContent).toContain('Unrecognized inspection diagnostic')
    expect(container.innerHTML).not.toContain(unsafe)
    expect(container.innerHTML).not.toContain('constructor')
    expect(container.innerHTML).not.toContain('__proto__')
    expect(container.textContent).toContain('Incomplete evidence')
  })

  it('filters review batches and paginates all batches without losing source ordinals', () => {
    const batches = Array.from({ length: 22 }, (_, index) => batch(index + 1))
    batches[21].analysis.success = false
    batches[21].analysis.outcomes = [{ ordinal: 1, state: 'ERROR' }]
    render(report({ total_batches: 22, inspected_batches: 22, complete_batches: 22, batches }))
    expect(container.querySelectorAll('.aura-outcome-batch')).toHaveLength(1)
    expect(container.textContent).toContain('HAR entry 27')
    act(() => { (container.querySelector('input[type=checkbox]') as HTMLInputElement).click() })
    expect(container.querySelectorAll('.aura-outcome-batch')).toHaveLength(20)
    act(() => { findButton('Next batches').click() })
    expect(container.querySelectorAll('.aura-outcome-batch')).toHaveLength(2)
    expect(container.textContent).toContain('21–22 of 22 batches')
    expect(findButton('Next batches').disabled).toBe(true)
    act(() => { findButton('Previous batches').click() })
    expect(container.textContent).toContain('1–20 of 22 batches')
  })

  it('links only one exact existing action mapping and never guesses from HAR positions', () => {
    const onViewSource = vi.fn()
    const mapping = { id: 'existing-mapping', source_file: 'sample.har', start_line: 12, target_kind: 'action', target_path: 'actions[3]', status: 'converted' }
    act(() => { root.render(<AuraOutcomeReview report={report({ batches: [batch(4)] })} sourceReview={{ mappings: [mapping] }} onViewSource={onViewSource} />) })
    act(() => { findButton('View request 4 source mapping').click() })
    expect(onViewSource).toHaveBeenCalledWith(mapping)
    act(() => { root.render(<AuraOutcomeReview report={report({ batches: [batch(4)] })} sourceReview={{ mappings: [mapping, { ...mapping, id: 'ambiguous' }] }} onViewSource={onViewSource} />) })
    expect(container.querySelectorAll('button')).toHaveLength(2)
    act(() => { root.render(<AuraOutcomeReview report={report({ batches: [batch(4, { action_path: undefined })] })} sourceReview={{ mappings: [mapping] }} onViewSource={onViewSource} />) })
    expect(container.querySelectorAll('button')).toHaveLength(2)
  })

  it('offers the remaining action details and rejects invalid numeric ordinals', () => {
    render(report({ batches: [batch(Number.NaN, { har_entry_id: -1, analysis: { success: false, complete: false, has_response: true, request_actions: 51, response_actions: 51, matched_actions: 0, outcomes: Array.from({ length: 51 }, (_, index) => ({ ordinal: index + 1, state: 'UNKNOWN' })) } })] }))
    expect(container.textContent).toContain('Request unavailable')
    expect(container.textContent).toContain('HAR entry unavailable')
    expect(container.querySelectorAll('.aura-outcome-actions li')).toHaveLength(50)
    expect(container.textContent).toContain('Remaining action outcomes (1)')
    const remaining = [...container.querySelectorAll('details')].find(detail => detail.querySelector('summary')?.textContent === 'Remaining action outcomes (1)')!
    expect(remaining.textContent).toContain('Action 51: UNKNOWN')
  })
})
