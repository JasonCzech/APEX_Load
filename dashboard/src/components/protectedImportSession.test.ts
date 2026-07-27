import { describe, expect, it } from 'vitest'

import {
  activeProtectedImportSessionId,
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
})
