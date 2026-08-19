import { describe, expect, it } from 'vitest'

import {
  activeProtectedImportSessionId,
  credentialPreservingImportDraftRequiresSave,
	  earliestImportDraftExpiry,
  importDraftExpiryStatus,
  invalidateProtectedImportCandidate,
  mergeProtectedParameterizationSelectionIds,
  protectedImportExpiryLabel,
} from './protectedImportSession'

describe('protected import sessions', () => {
  it('uses only a non-expired candidate', () => {
    const now = Date.parse('2026-07-26T12:00:00Z')
    expect(activeProtectedImportSessionId({
      import_session_id: ' session-1 ',
      expires_at: '2026-07-26T12:30:00Z',
    }, now)).toBe('session-1')
    expect(activeProtectedImportSessionId({
      import_session_id: 'session-1',
      expires_at: '2026-07-26T11:59:59Z',
    }, now)).toBeUndefined()
  })

  it('drops candidate fencing after a local draft mutation', () => {
    const original = {
      import_session_id: 'session-1',
      expires_at: '2026-07-26T12:30:00Z',
      script: { name: 'edited' },
    }
    const next = invalidateProtectedImportCandidate(original)
    expect(next).not.toBe(original)
    expect(next.import_session_id).toBeUndefined()
    expect(next.expires_at).toBeUndefined()
    expect(next.script).toEqual({ name: 'edited' })
  })

  it('requires persistence before freeform editing when the server preserved an imported value', () => {
    expect(credentialPreservingImportDraftRequiresSave(
      { import_draft_expires_at: '2099-07-26T12:30:00Z' },
      undefined,
    )).toBe(true)
    expect(credentialPreservingImportDraftRequiresSave(undefined, undefined, [{
      id: 'credential',
      path: '/actions/0/body',
      label: 'Credential',
      context: 'Form credential',
      suggested_name: 'LOGIN_PASSWORD',
      destination_value_preserved: true,
    }])).toBe(true)
    expect(credentialPreservingImportDraftRequiresSave(undefined, {
      name: 'Imported',
      version: '1.0.0',
      protocol: 'http',
      metadata: {
        import: {
          source: 'sample',
          preserved: { server_import_attestation_v1: 'protected:sample' },
        },
      },
      config: { base_url: '' },
      actions: [],
    })).toBe(true)
    expect(credentialPreservingImportDraftRequiresSave(undefined, undefined, [])).toBe(false)
  })

  it('keeps cumulative protected parameterization selections deterministic', () => {
    expect(mergeProtectedParameterizationSelectionIds(
      ['suggestion-b', ' suggestion-a '],
      ['suggestion-c', 'suggestion-b', ''],
    )).toEqual(['suggestion-a', 'suggestion-b', 'suggestion-c'])
  })

  it('provides reader-facing active and expired labels', () => {
    const now = Date.parse('2026-07-26T12:00:00Z')
    expect(protectedImportExpiryLabel('2026-07-26T12:30:00Z', now)).toContain('Protected server candidate available until')
    expect(protectedImportExpiryLabel('2026-07-26T11:59:59Z', now)).toContain('has expired')
  })

  it('classifies credential-preserving draft expiry for action gating', () => {
    const now = Date.parse('2026-07-26T12:00:00Z')
    expect(importDraftExpiryStatus('2026-07-26T12:30:00Z', now)).toEqual(expect.objectContaining({
      expired: false,
      label: expect.stringContaining('Credential-preserving import draft available until'),
    }))
    expect(importDraftExpiryStatus('2026-07-26T11:59:59Z', now)).toEqual({
      expired: true,
      label: 'This credential-preserving import draft has expired. Re-run the import before saving or editing it.',
    })
    expect(importDraftExpiryStatus('not-a-date', now)).toBeNull()
  })

  it('uses the earliest renewed expiry for a protected script batch', () => {
    expect(earliestImportDraftExpiry([
      '2026-07-26T12:45:00Z',
      null,
      'not-a-date',
      '2026-07-26T12:30:00Z',
    ])).toBe('2026-07-26T12:30:00Z')
    expect(earliestImportDraftExpiry([null, 'invalid'])).toBeUndefined()
  })
})
