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
})
