import { describe, expect, it } from 'vitest'
import type { Parameterization, ParameterizationSuggestion, SecretBindingRequirement } from '../types/scriptImports'
import type { ScriptDefinition } from '../types/scripts'
import { ApiError } from '../hooks/apiClient'
import { carryPreAppliedCorrelations, exclusionRefusedBeforeCommit, sourceSecretSessionAfterExclusion, filesHaveIdenticalBytes, harShapingApplied, PRE_APPLIED_AFTER_EXCLUSION_RATIONALE } from './ProtocolAssetImportModel'

const requirement = (id: string, extra: Partial<SecretBindingRequirement> = {}): SecretBindingRequirement => ({
  id, path: `/actions/0/${id}`, label: id, context: 'body', suggested_name: id.toUpperCase(), ...extra,
})

describe('sourceSecretSessionAfterExclusion', () => {
  it('adopts the re-keyed session and trusts only the server-marked requirements', () => {
    const next = [requirement('moved', { source_value_available: true }), requirement('manual')]
    const adopted = sourceSecretSessionAfterExclusion('session-1', { source_secret_session_id: 'session-2', source_secret_session_expires_at: '2026-11-02T10:00:00Z' }, next)
    expect(adopted.sessionId).toBe('session-2')
    expect(adopted.expiresAt).toBe('2026-11-02T10:00:00Z')
    expect(adopted.requirements.map(item => [item.id, item.source_value_available === true])).toEqual([['moved', true], ['manual', false]])
  })

  it('claims no recorded value once the consumed session has no replacement', () => {
    for (const response of [{ source_secret_session_id: '' }, {}]) {
      const after = sourceSecretSessionAfterExclusion('session-1', response, [requirement('pw', { source_value_available: true })])
      expect(after.sessionId).toBeUndefined()
      expect(after.requirements[0].source_value_available).toBeUndefined()
    }
  })

  it('claims no recorded value without a session to send', () => {
    const after = sourceSecretSessionAfterExclusion(undefined, { source_secret_session_id: 'forged' }, [requirement('pw', { source_value_available: true })])
    expect(after.sessionId).toBeUndefined()
    expect(after.requirements[0].source_value_available).toBeUndefined()
  })
})

describe('harShapingApplied', () => {
  const script = (actions: ScriptDefinition['actions']): ScriptDefinition => ({ name: 's', version: '1', protocol: 'http', config: { base_url: '' }, actions })

  it('reads step transactions and concurrent groups from the draft', () => {
    const shaped = script([
      { name: 'a', type: 'request', target: '/', metadata: { source_transaction: 'Home' } },
      { name: 'group', type: 'http_sequence', target: '/aura', metadata: { recorded_concurrent_group: 1 }, params: { requests: [] } },
    ])
    expect(harShapingApplied(shaped, [])).toEqual({ stepTransactions: true, recordedConcurrency: true })
    expect(harShapingApplied(script([{ name: 'a', type: 'request', target: '/' }]), [])).toEqual({ stepTransactions: false, recordedConcurrency: false })
  })

  it('does not count a recording that marks its own transactions', () => {
    const recorded = script([{ name: 'a', type: 'request', target: '/', metadata: { source_transaction: 'Recorded' } }])
    expect(harShapingApplied(recorded, ['Step transactions were not added: the recording already marks its transactions, which the script keeps.']).stepTransactions).toBe(false)
  })
})

describe('filesHaveIdenticalBytes', () => {
  it('compares contents, not just sizes, and stops when cancelled', async () => {
    const a = new File(['{"log":1}'], 'a.har')
    expect(await filesHaveIdenticalBytes(a, new File(['{"log":1}'], 'b.har'), () => false)).toBe(true)
    expect(await filesHaveIdenticalBytes(a, new File(['{"log":2}'], 'b.har'), () => false)).toBe(false)
    expect(await filesHaveIdenticalBytes(a, new File(['{"log":10}'], 'b.har'), () => false)).toBe(false)
    expect(await filesHaveIdenticalBytes(a, new File(['{"log":1}'], 'b.har'), () => true)).toBeNull()
  })
})

describe('carryPreAppliedCorrelations', () => {
  const row = (id: string, varName: string, extra: Partial<ParameterizationSuggestion> = {}): ParameterizationSuggestion => ({
    id, kind: 'correlation', var_name: varName, replacement: `{{var.${varName}}}`, confidence: 1, rule_id: 'correlation.pre_applied',
    rationale: 'Already applied during conversion: extracted from step 2 via body and substituted in step(s) 5.', auto_apply: false, high_risk: false, pre_applied: true, ...extra,
  })
  const summary = (extra: Partial<Parameterization['summary']> = {}): Parameterization['summary'] => ({ total: 0, by_kind: {}, auto_apply_count: 0, review_count: 0, high_risk_count: 0, pre_applied_count: 0, ...extra })
  const script = {
    name: 'Draft', version: '1.0.0', protocol: 'http', config: { base_url: '' },
    actions: [
      { name: 'login', type: 'request', target: '/login', extractors: [{ name: 'corr_sid', source: 'body', type: 'regex', expression: 'sid=(\\w+)' }] },
      { name: 'group', type: 'http_sequence', target: '/aura', params: { requests: [{ name: 'aura', type: 'request', target: '/aura', extractors: [{ name: 'corr_token', source: 'header', type: 'regex', expression: 't' }] }] } },
    ],
  } as unknown as ScriptDefinition

  it('keeps the import-time applied rows whose extractor the excluded draft still has', () => {
    const previous: Parameterization = {
      suggestions: [
        row('c1', 'corr_sid', { locations: [{ section: 'actions', action_index: 7, action_name: 'aura', field: 'body' }] as ParameterizationSuggestion['locations'] }),
        row('c2', 'corr_token'), row('c3', 'corr_gone'), { ...row('d1', 'ONE'), kind: 'data_param', pre_applied: false },
      ],
      summary: summary({ total: 4, by_kind: { correlation: 3, data_param: 1 }, pre_applied_count: 3, review_count: 1 }),
    }
    const next: Parameterization = {
      suggestions: [{ ...row('d1', 'ONE'), kind: 'data_param', pre_applied: false }],
      summary: summary({ total: 1, by_kind: { data_param: 1 }, review_count: 1 }),
    }
    const carried = carryPreAppliedCorrelations(previous, next, script)!
    expect(carried.suggestions.map(item => [item.id, item.pre_applied === true])).toEqual([['c1', true], ['c2', true], ['d1', false]])
    expect(carried.summary).toEqual(summary({ total: 3, by_kind: { data_param: 1, correlation: 2 }, review_count: 1, pre_applied_count: 2 }))
    // The step numbers named actions by position before the exclusion.
    expect(carried.suggestions[0].rationale).toBe(PRE_APPLIED_AFTER_EXCLUSION_RATIONALE)
    // So did the locations: the carried row lists none rather than wrong steps.
    expect(carried.suggestions[0].locations).toBeUndefined()
    expect(previous.suggestions[0].locations).toHaveLength(1)
  })

  it('shows the carried rows when the exclusion returned no parameterization, and adds nothing twice', () => {
    const previous: Parameterization = { suggestions: [row('c1', 'corr_sid')], summary: summary({ total: 1, by_kind: { correlation: 1 }, pre_applied_count: 1 }) }
    expect(carryPreAppliedCorrelations(previous, null, script)?.summary.pre_applied_count).toBe(1)
    const already: Parameterization = { suggestions: [row('c1', 'corr_sid')], summary: summary({ total: 1, by_kind: { correlation: 1 }, pre_applied_count: 1 }) }
    expect(carryPreAppliedCorrelations(previous, already, script)).toBe(already)
    expect(carryPreAppliedCorrelations(undefined, null, script)).toBeUndefined()
  })
})

describe('exclusionRefusedBeforeCommit', () => {
  it('trusts a 4xx and the handler-marked 503s, and no other server error', () => {
    expect(exclusionRefusedBeforeCommit(new ApiError('refused', 422, { error: 'refused' }, 'req-1'))).toBe(true)
    expect(exclusionRefusedBeforeCommit(new ApiError('retry', 503, { error: 'retry', code: 'import_draft_attestation_unavailable' }, 'req-2'))).toBe(true)
    // The domain feature policy check refuses before the claim is used.
    expect(exclusionRefusedBeforeCommit(new ApiError('domain feature policy is unavailable', 503, { error: 'domain feature policy is unavailable', code: 'domain_feature_policy_unavailable' }, 'req-4'))).toBe(true)
    // A code-less 503 could be anything after the commit point.
    expect(exclusionRefusedBeforeCommit(new ApiError('unavailable', 503, { error: 'unavailable' }, 'req-5'))).toBe(false)
    // The access logger stamps X-Request-ID before the handler runs and turns
    // a panic after the commit point into a 500, so the header proves nothing.
    expect(exclusionRefusedBeforeCommit(new ApiError('internal error', 500, { error: 'internal error' }, 'req-3'))).toBe(false)
    expect(exclusionRefusedBeforeCommit(new ApiError('bad gateway', 502, undefined, 'proxy-id'))).toBe(false)
    expect(exclusionRefusedBeforeCommit(new TypeError('Failed to fetch'))).toBe(false)
  })
})
