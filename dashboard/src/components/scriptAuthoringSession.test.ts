import { describe, expect, it } from 'vitest'
import { admitValidationResponse, describeValidationContextChanges, draftFingerprint, sameValidationBinding, validationContextChanges, validationContextKey, type ValidationBinding, type ValidationRequest } from './scriptAuthoringSession'
import type { ScriptValidationContext } from '../types/scripts'

const binding = (overrides: Partial<ValidationBinding> = {}): ValidationBinding => ({ fingerprint: draftFingerprint('{"name":"a"}'), contentRevision: `scr-v1-${'a'.repeat(64)}`, context: '["p",null,null]', ...overrides })
const request = (overrides: Partial<ValidationRequest> = {}): ValidationRequest => ({ requestId: 3, sent: binding(), issuedAgainst: binding(), stored: false, ...overrides })

describe('script authoring session binding', () => {
  it('fingerprints documents deterministically and distinguishes them', () => {
    expect(draftFingerprint('{"name":"a"}')).toBe(draftFingerprint('{"name":"a"}'))
    expect(draftFingerprint('{"name":"a"}')).not.toBe(draftFingerprint('{"name":"b"}'))
    expect(draftFingerprint('')).toMatch(/^sdf1-[0-9a-f]{16}-0$/)
    expect(draftFingerprint('x'.repeat(4096))).toMatch(/^sdf1-[0-9a-f]{16}-1000$/)
  })

  it('keys the validation context by project, folder and replay variables', () => {
    const base = validationContextKey('p', null, null)
    expect(validationContextKey('p', undefined, undefined)).toBe(base)
    expect(validationContextKey('q', null, null)).not.toBe(base)
    expect(validationContextKey('p', 'folder', null)).not.toBe(base)
    expect(validationContextKey('p', null, { token: 'a' })).not.toBe(base)
    expect(validationContextKey('p', null, { token: 'a' })).not.toBe(validationContextKey('p', null, { token: 'b' }))
  })

  it('binds a result to the document, its baseline revision and its context', () => {
    expect(sameValidationBinding(binding(), binding())).toBe(true)
    expect(sameValidationBinding(binding(), binding({ fingerprint: draftFingerprint('{"name":"b"}') }))).toBe(false)
    expect(sameValidationBinding(binding(), binding({ contentRevision: `scr-v1-${'b'.repeat(64)}` }))).toBe(false)
    expect(sameValidationBinding(binding(), binding({ context: '["p","folder",null]' }))).toBe(false)
    expect(sameValidationBinding(binding(), null)).toBe(false)
    expect(sameValidationBinding(null, null)).toBe(false)
  })

  it('admits only the latest request for the still-current document with a matching echo', () => {
    const sent = binding()
    expect(admitValidationResponse(3, request(), { validated_fingerprint: sent.fingerprint }, sent)).toBe('current')
    // A later request supersedes an earlier one, whatever it echoes.
    expect(admitValidationResponse(4, request(), { validated_fingerprint: sent.fingerprint }, sent)).toBe('superseded')
    // An edit, save, rebaseline or context change in flight supersedes it too.
    expect(admitValidationResponse(3, request(), { validated_fingerprint: sent.fingerprint }, binding({ fingerprint: draftFingerprint('{"name":"edited"}') }))).toBe('superseded')
    expect(admitValidationResponse(3, request(), { validated_fingerprint: sent.fingerprint }, binding({ contentRevision: `scr-v1-${'c'.repeat(64)}` }))).toBe('superseded')
    expect(admitValidationResponse(3, request(), { validated_fingerprint: sent.fingerprint }, binding({ context: '["q",null,null]' }))).toBe('superseded')
    expect(admitValidationResponse(3, request(), { validated_fingerprint: sent.fingerprint }, null)).toBe('superseded')
    // A controller that does not confirm the draft it validated binds nothing.
    expect(admitValidationResponse(3, request(), {}, sent)).toBe('unbound')
    expect(admitValidationResponse(3, request(), { validated_fingerprint: draftFingerprint('other') }, sent)).toBe('unbound')
    expect(admitValidationResponse(3, request(), null, sent)).toBe('unbound')
  })

  it('accepts the pre-flush document until the flushed one renders, but nothing else', () => {
    const rendered = binding({ fingerprint: draftFingerprint('{"name":"a","cells":"old"}') })
    const flushed = request({ issuedAgainst: rendered })
    const echo = { validated_fingerprint: flushed.sent.fingerprint }
    expect(admitValidationResponse(3, flushed, echo, rendered)).toBe('current')
    expect(admitValidationResponse(3, flushed, echo, flushed.sent)).toBe('current')
    expect(admitValidationResponse(3, flushed, echo, binding({ fingerprint: draftFingerprint('{"name":"typed later"}') }))).toBe('superseded')
    expect(admitValidationResponse(3, request({ issuedAgainst: null }), echo, rendered)).toBe('superseded')
  })

  it('binds a stored validation to the revision the controller read', () => {
    const sent = binding()
    const stored = request({ stored: true })
    expect(admitValidationResponse(3, stored, { validated_content_revision: sent.contentRevision }, sent)).toBe('current')
    expect(admitValidationResponse(3, stored, { validated_content_revision: `scr-v1-${'b'.repeat(64)}` }, sent)).toBe('unbound')
    expect(admitValidationResponse(3, stored, { validated_fingerprint: sent.fingerprint }, sent)).toBe('unbound')
    const unsaved = request({ stored: true, sent: binding({ contentRevision: '' }) })
    expect(admitValidationResponse(3, unsaved, { validated_content_revision: '' }, unsaved.sent)).toBe('unbound')
  })
})

describe('validation context changes (AUTH-B)', () => {
  const context = (secrets: string, policy: string, revision = `vctx1:${secrets}:${policy}`): ScriptValidationContext => ({
    version: 1, revision, secret_reference_limit: 256,
    components: { capabilities: 'c', policy, approvals: 'a', secret_references: secrets },
  })
  it('names exactly the components that changed', () => {
    expect(validationContextChanges(context('s1', 'p1'), context('s1', 'p1'))).toEqual([])
    expect(validationContextChanges(context('s1', 'p1'), context('s2', 'p1'))).toEqual(['secret_references'])
    expect(validationContextChanges(context('s1', 'p1'), context('s2', 'p2'))).toEqual(['policy', 'secret_references'])
    expect(describeValidationContextChanges(['policy', 'secret_references'])).toBe('project or domain policy, project secrets')
  })
  it('treats a changed revision with identical components as a change of every component', () => {
    expect(validationContextChanges(context('s1', 'p1', 'vctx1:old'), context('s1', 'p1', 'vctx2:new'))).toHaveLength(4)
  })
  it('reports nothing without both contexts', () => {
    expect(validationContextChanges(null, context('s1', 'p1'))).toEqual([])
    expect(validationContextChanges(context('s1', 'p1'), null)).toEqual([])
  })
})
