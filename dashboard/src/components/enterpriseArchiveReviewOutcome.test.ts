import { describe, expect, it } from 'vitest'
import { archiveReviewOutcome } from './enterpriseArchiveReviewOutcome'

function state(protocol = 'http'): Parameters<typeof archiveReviewOutcome>[0] {
  return {
    result: {
      scripts: [{ name: 'draft', version: '1.0', protocol, actions: [], config: { base_url: '' } }],
      warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [], correlations: [], summary: { unsupported_function_count: 0 },
      stats: { total_functions: 0, converted_actions: 0, extractors: 0, think_times: 0, transactions: 0, skipped: 0 },
    },
    savedResultScripts: {}, reviewOptionsIdentity: 'current', reviewGeneration: 2,
    replayReviewGeneration: 2, replayScriptIndex: 0, replayProjectId: 'project',
    replayValidationLoading: false, replayValidationSession: null,
  }
}
const outcome = (item: ReturnType<typeof state>) => archiveReviewOutcome(item, 'current', 'project')

describe('archive queue review outcome', () => {
  it('does not call stale reviews or invalid static checks ready to save', () => {
    const item = state()
    expect(outcome(item)).toBe('Ready to save')
    item.reviewOptionsIdentity = 'old'
    expect(outcome(item)).toBe('Needs validation')
    item.reviewOptionsIdentity = 'current'
    item.result!.preflight = { valid: false, action_count: 0, expected_action_count: 0, data_source_count: 0, parameter_count: 0 }
    expect(outcome(item)).toBe('Blocker')
    item.result = null
    expect(outcome(item)).toBe('Review required')
  })

  it('requires repair for unresolved runtime references even if other checks passed', () => {
    const item = state()
    item.result!.preflight = { valid:true, action_count:1, expected_action_count:1, data_source_count:0, parameter_count:0, missing_secrets:['MASKED_PAYLOAD'] }
    expect(outcome(item)).toBe('Blocker')
  })

  it('counts stamped missing secrets only until the inspection answers, as the review does', () => {
    const item = state()
    // Stamped without the project's secret resolver: every project secret reads as missing.
    item.result!.preflight = { valid: true, action_count: 1, expected_action_count: 1, data_source_count: 0, parameter_count: 0, missing_secrets: ['LOGIN_TOKEN'], checks: [{ id: 'secrets', label: 'Secret bindings', status: 'warn' }] }
    expect(outcome(item)).toBe('Blocker')
    const inspected = (readiness: { replay_ready: boolean; unresolved_bindings?: number } | undefined) => archiveReviewOutcome(item, 'current', 'project', () => readiness)
    // The inspection resolves them against the project and omits a zero count.
    expect(inspected({ replay_ready: true })).toBe('Ready to save')
    expect(inspected({ replay_ready: true, unresolved_bindings: 1 })).toBe('Blocker')
    // A script no inspection has answered for keeps the stamped count.
    expect(inspected(undefined)).toBe('Blocker')
    // Credentials the import itself needs bound are a credential review.
    item.result!.secret_binding_requirements = [{ id: 'req-1', path: 'actions[0].headers.Authorization', label: 'Authorization', context: 'header', suggested_name: 'LOGIN_TOKEN', script_index: 0 }]
    expect(inspected({ replay_ready: true })).toBe('Needs credential review')
  })

  it('does not block on variables replay sends as placeholder text', () => {
    const item = state()
    item.result!.preflight = { valid: true, action_count: 1, expected_action_count: 1, data_source_count: 0, parameter_count: 0, missing_variables: ['store_ref'], checks: [{ id: 'variables', label: 'Variable readiness', status: 'warn' }] }
    expect(outcome(item)).toBe('Ready to save')
  })

  it('treats optional parameterization suggestions as ready to save', () => {
    const item = state()
    item.result!.parameterization = {
      suggestions: [{
        id: 'pz_customer', kind: 'data_param', var_name: 'customer_id', replacement: '{{var.customer_id}}',
        confidence: 0.9, rule_id: 'test.customer', rationale: 'Varies per user', auto_apply: true, high_risk: false, pre_applied: false,
      }],
      summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
    }
    expect(outcome(item)).toBe('Ready to save')
  })

  it('calls a promotion hold a blocker only when it names blockers', () => {
    const item = state()
    // Review codes alone (a placeholder value, a review decision) hold promotion, not replay.
    item.result!.fidelity_report = { promotion_status: 'blocked', blockers: [], warnings: ['conditional_producer:C_StoreId'] }
    expect(outcome(item)).toBe('Needs review')
    item.result!.fidelity_report = { promotion_status: 'blocked' }
    expect(outcome(item)).toBe('Needs review')
    item.result!.fidelity_report = { promotion_status: 'blocked', blockers: ['legacy_tls_replacement at init[0]'] }
    expect(outcome(item)).toBe('Blocker')
    item.result!.fidelity_report = { promotion_status: 'promotable', blockers: [] }
    expect(outcome(item)).toBe('Ready to save')
    item.result!.summary = { ...item.result!.summary, migration_readiness: { score_pct: 60, blocked: true, summary: 'Blocked by policy' } }
    expect(outcome(item)).toBe('Blocker')
  })

  it('calls a promotion hold a blocker for a step gate, never for an archive sentence alone', () => {
    const item = state()
    // The converter's sentence holds promotion and load-test launch; replay runs.
    item.result!.fidelity_report = { promotion_status: 'blocked', blockers: ['One or more response contributors or one-shot headers occur after the final request and cannot be bound faithfully.'] }
    expect(outcome(item)).toBe('Needs review')
    // A guided repair writes the code of a gate it left in place.
    item.result!.fidelity_report = { promotion_status: 'blocked', blockers: ['One or more C lifecycle source files were missing, ambiguous, or incompletely parsed.', 'legacy_tls_replacement'] }
    expect(outcome(item)).toBe('Blocker')
    item.result!.fidelity_report = { promotion_status: 'blocked', blockers: ['missing_producer:sid at actions[3].params.requests[1]'] }
    expect(outcome(item)).toBe('Blocker')
  })

  it('still calls an invalid preflight a blocker when promotion is held without blockers', () => {
    const item = state()
    item.result!.fidelity_report = { promotion_status: 'blocked', blockers: [] }
    item.result!.preflight = { valid: false, action_count: 1, expected_action_count: 1, data_source_count: 0, parameter_count: 0 }
    expect(outcome(item)).toBe('Blocker')
  })

  it.each([
    ['queued', 'Replay in progress'], ['running', 'Replay in progress'], ['paused', 'Replay in progress'],
    ['failed', 'Needs replay retry'], ['stopped', 'Needs replay retry'], ['completed', 'Review replay results'],
  ])('shows %s replay state even when the result uses scripts rather than script', (status, label) => {
    const item = state('browser')
    item.replayValidationSession = { status }
    expect(outcome(item)).toBe(label)
  })

  it.each(['replayReviewGeneration', 'replayScriptIndex', 'replayProjectId'] as const)('rejects replay evidence with stale %s', key => {
    const item = state('browser')
    item.replayValidationSession = { status: 'completed' }
    if (key === 'replayProjectId') item[key] = 'other'
    else item[key] = 99
    expect(outcome(item)).toBe('Needs replay')
  })

  it('preserves protocol guidance for reference artifacts without executable scripts', () => {
    const item = state()
    item.result!.scripts = []
    item.result!.detected_protocol = { canonical_source_protocol: 'sap_gui', import_mode: 'review_only', confidence: 'high' }
    expect(outcome(item)).toBe('Review only')
  })

  it('distinguishes queued approval sessions from library saves and ignores unrelated indices', () => {
    const item = state()
    item.result!.scripts.push({ ...item.result!.scripts[0], name: 'sibling' })
    item.queuedResultScripts = { 0: { sessionId: 'review-1' }, 99: {} }
    expect(outcome(item)).toBe('Queued 1 of 2 for approval')
    item.savedResultScripts[1] = {}
    expect(outcome(item)).toBe('Queued 1 of 2 for approval; saved 1')
    item.savedResultScripts[0] = {}
    expect(outcome(item)).toBe('Saved')
  })

  it('reports partial saves without counting unrelated indices', () => {
    const item = state()
    item.result!.scripts.push({ ...item.result!.scripts[0], name: 'second' })
    item.savedResultScripts = { 0: {}, 99: {} }
    expect(outcome(item)).toBe('Saved 1 of 2')
    item.savedResultScripts[1] = {}
    expect(outcome(item)).toBe('Saved')
  })
})
