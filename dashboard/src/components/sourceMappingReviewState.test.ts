import { describe, expect, it } from 'vitest'
import { createMappingReviewState, decisionMatches, preserveMappingBindings, readMappingReviewState, rebindMapping, resolveMappingBindings, withMappingReviewState } from './sourceMappingReviewState'
import type { ScriptDefinition, SourceReview } from '../types/scripts'

const review: SourceReview = { mappings: [
  { id: 'first', source_file: 'Action.c', start_line: 1, target_path: 'actions[0].target', status: 'converted' },
  { id: 'source-only', source_file: 'Action.c', start_line: 2, status: 'unsupported' },
] }
const script: ScriptDefinition = { name: 'Example', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [
  { type: 'request', name: 'First', target: '/first' }, { type: 'request', name: 'Second', target: '/second' },
], metadata: { import: { preserved: { source_review: review } } } }

describe('durable source mapping identity', () => {
  it('preserves the baseline before saving a reordered draft and follows the same unique action after reopening', async () => {
    const reordered = { ...script, actions: [...script.actions].reverse() }
    const saved = await preserveMappingBindings(reordered, script)
    const state = readMappingReviewState(JSON.parse(JSON.stringify(saved)))!
    expect(state.snapshot?.actions[0].target).toBe('/first')
    expect(state.snapshot?.metadata).toBeUndefined()
    expect((await resolveMappingBindings(saved, review.mappings!, state)).first).toMatchObject({ status: 'moved', path: 'actions[1].target' })
  })

  it.each([
    ['changed', [{ ...script.actions[0], target: '/edited' }, script.actions[1]]],
    ['changed', [script.actions[1]]],
    ['ambiguous', [script.actions[0], script.actions[0]]],
  ] as const)('fails closed for %s action identities', async (status, actions) => {
    const state = await createMappingReviewState(script, review)
    const result = (await resolveMappingBindings({ ...script, actions: [...actions] }, review.mappings!, state)).first
    expect(result.status).toBe(status)
    expect(result.path).toBeUndefined()
  })

  it('binds decisions to target and import revisions, and invalidates them on explicit re-link', async () => {
    let state = await createMappingReviewState(script, review)
    const result = (await resolveMappingBindings(script, review.mappings!, state)).first
    const decision = { outcome: 'acknowledged' as const, reviewer: 'reviewer-123', reviewedAt: new Date().toISOString(), note: 'Checked', importRevision: state.importRevision, targetDigest: result.targetDigest! }
    state = { ...state, decisions: { first: decision } }
    expect(decisionMatches(decision, state, result)).toBe(true)
    expect(decisionMatches(decision, { ...state, importRevision: 'new-import' }, result)).toBe(false)
    expect(decisionMatches(decision, state, { ...result, targetDigest: 'edited' })).toBe(false)
    const rebound = await rebindMapping(script, review.mappings![0], state, 'actions[1].target')
    expect(rebound.decisions.first).toBeUndefined()
    expect((await resolveMappingBindings(script, review.mappings!, rebound)).first.path).toBe('actions[1].target')
  })

  it('permits source-only review and deliberate linking without inventing a destination', async () => {
    const state = await createMappingReviewState(script, review)
    const result = (await resolveMappingBindings(script, review.mappings!, state))['source-only']
    expect(result.status).toBe('unmapped')
    expect(result.path).toBeUndefined()
    expect(result.targetDigest).toBeTruthy()
    const rebound = await rebindMapping(script, review.mappings![1], state, 'actions[1]')
    expect((await resolveMappingBindings(script, review.mappings!, rebound))['source-only'].path).toBe('actions[1]')
  })

  it('rejects invalid re-link paths and review metadata targets', async () => {
    const state = await createMappingReviewState(script, review)
    for (const path of ['actions[99]', 'actions[0]garbage', 'metadata.import.preserved.source_mapping_review']) {
      await expect(rebindMapping(script, review.mappings![0], state, path)).rejects.toThrow()
    }
  })

  it('does not silently anchor a changed import from an older baseline', async () => {
    const changed = { ...script, metadata: { import: { preserved: { source_review: { ...review, mappings: [] } } } } }
    expect(await preserveMappingBindings(changed, script)).toBe(changed)
    const malformed = withMappingReviewState(script, { version: 1, importRevision: 'x', capturedAt: 'now', bindings: {}, decisions: { x: null } } as never)
    expect(readMappingReviewState(malformed)).toBeNull()
    await expect(createMappingReviewState(malformed, review)).rejects.toThrow('Saved mapping review data is invalid')
  })

  it('handles hostile mapping identifiers as data', async () => {
    const hostile = { mappings: [{ ...review.mappings![0], id: '__proto__' }] }
    const state = await createMappingReviewState(script, hostile)
    expect(Object.prototype.hasOwnProperty.call(state.bindings, '__proto__')).toBe(true)
    const reopened = readMappingReviewState(JSON.parse(JSON.stringify(withMappingReviewState(script, state))))!
    expect(reopened.decisions.constructor).toBeUndefined()
    expect(reopened.decisions['__proto__']).toBeUndefined()
    expect((await resolveMappingBindings(script, hostile.mappings, state))['__proto__'].path).toBe('actions[0].target')
  })
  it.each(['original_import', 'future_domain', ' '])('keeps %s paths source-only and removes legacy decisions on save without changing source evidence', async domain => {
    const legacy = await createMappingReviewState(script, review)
    const resolution = (await resolveMappingBindings(script, review.mappings!, legacy)).first
    const oldDecision = { outcome: 'acknowledged' as const, reviewer: 'old-reviewer', reviewedAt: 'old-time', importRevision: legacy.importRevision, targetDigest: resolution.targetDigest!, note: 'Old annotation' }
    const markedReview = { ...review, target_domain: domain }
    const native: ScriptDefinition = { ...withMappingReviewState(script, { ...legacy, decisions: { first: oldDecision } }), actions: [{ type: 'control_loop', name: 'Transformed native action', target: '' }], metadata: { import: { preserved: { source_review: markedReview, source_mapping_review: { ...legacy, decisions: { first: oldDecision } }, keep: 'preserved' } } } }
    const before = JSON.stringify(native)
    expect(readMappingReviewState(native)).toBeNull()
    const inert = await createMappingReviewState(native, markedReview)
    expect(inert).toMatchObject({ sourceOnly: true, bindings: {}, decisions: {} })
    expect(inert.snapshot).toBeUndefined()
    expect((await resolveMappingBindings(native, markedReview.mappings!, legacy)).first).toEqual({ status: 'unmapped' })
    expect((await resolveMappingBindings(script, markedReview.mappings!, inert)).first).toEqual({ status: 'unmapped' })
    expect(decisionMatches(oldDecision, inert, resolution)).toBe(false)
    await expect(rebindMapping(native, markedReview.mappings![0], legacy, 'actions[0]')).rejects.toThrow('Original source paths')
    await expect(rebindMapping(script, markedReview.mappings![0], inert, 'actions[0]')).rejects.toThrow('Original source paths')
    const saved = await preserveMappingBindings(native, script)
    expect(saved.metadata?.import?.preserved?.source_mapping_review).toBeUndefined()
    expect(saved.metadata?.import?.preserved?.source_review).toEqual(markedReview)
    expect(saved.metadata?.import?.preserved?.keep).toBe('preserved')
    expect(saved.actions).toEqual(native.actions)
    expect(withMappingReviewState(native, legacy).metadata?.import?.preserved?.source_mapping_review).toBeUndefined()
    expect(JSON.stringify(native)).toBe(before)
    expect(readMappingReviewState(JSON.parse(JSON.stringify(saved)))).toBeNull()
  })

  it('retains current-script bindings for absent or empty legacy domains', async () => {
    for (const domain of [undefined, '']) {
      const currentReview = { ...review, target_domain: domain }
      const current = { ...script, metadata: { import: { preserved: { source_review: currentReview } } } }
      const state = await createMappingReviewState(current, currentReview)
      expect((await resolveMappingBindings(current, currentReview.mappings!, state)).first.status).toBe('matched')
      expect(readMappingReviewState(withMappingReviewState(current, state))).not.toBeNull()
    }
  })

  it('clears stale annotations from a legacy native-selected draft without a domain marker', async () => {
    const state = await createMappingReviewState(script, review)
    const native: ScriptDefinition = { ...script, metadata: { import: { source: 'salesforce', preserved: { source_review: review, salesforce_review: { native_preview: { correlations: [] } }, source_mapping_review: { ...state, decisions: { first: { outcome: 'acknowledged', reviewer: 'old', reviewedAt: 'old', importRevision: state.importRevision, targetDigest: 'old', note: 'Old annotation' } } } } } } }
    const original = JSON.stringify(native)
    expect(readMappingReviewState(native)).toBeNull()
    expect(await createMappingReviewState(native, review)).toMatchObject({ sourceOnly: true, bindings: {}, decisions: {} })
    expect((await resolveMappingBindings(native, review.mappings!, state)).first).toEqual({ status: 'unmapped' })
    await expect(rebindMapping(native, review.mappings![0], state, 'actions[0]')).rejects.toThrow('Original source paths')
    const saved = await preserveMappingBindings(native, script)
    expect(saved.metadata?.import?.preserved?.source_mapping_review).toBeUndefined()
    expect(saved.metadata?.import?.preserved?.source_review).toEqual(review)
    expect(saved.metadata?.import?.preserved?.salesforce_review).toEqual(native.metadata?.import?.preserved?.salesforce_review)
    expect(JSON.stringify(native)).toBe(original)
    expect(readMappingReviewState(JSON.parse(JSON.stringify(saved)))).toBeNull()
  })

})
