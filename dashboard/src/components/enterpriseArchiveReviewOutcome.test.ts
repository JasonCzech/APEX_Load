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

  it('does not block on variables replay sends as placeholder text', () => {
    const item = state()
    item.result!.preflight = { valid: true, action_count: 1, expected_action_count: 1, data_source_count: 0, parameter_count: 0, missing_variables: ['store_ref'], checks: [{ id: 'variables', label: 'Variable readiness', status: 'warn' }] }
    expect(outcome(item)).toBe('Ready to save')
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
