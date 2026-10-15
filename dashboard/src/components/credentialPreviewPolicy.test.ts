import { describe, expect, it } from 'vitest'
import { isRecordedStateHeader, isSensitiveCredentialPreviewName } from './credentialPreviewPolicy'

// Mirrors pkg/credential TestSensitiveNamePreservesNormalizedVocabulary.
describe('isSensitiveCredentialPreviewName', () => {
  it.each([
    ['password', true],
    ['api_token', true],
    ['credentials[client_secret]', true],
    ['tokenURL', false],
    ['author', false],
    ['session_id', true],
    ['user_session', true],
    ['access_token_expires_in', true],
    ['type_token', true],
    ['session.flags.token', true],
    ['sas_url', true],
    ['session_uri', true],
    // A final property word describes the credential rather than holding it.
    ['session_duration', false],
    [':session_feature_flags', false],
    ['token_type', false],
    ['auth.mode', false],
    ['flags', false],
  ])('%s → %s', (name, sensitive) => {
    expect(isSensitiveCredentialPreviewName(name)).toBe(sensitive)
  })
})

// Mirrors pkg/credential TestRecordedStateHeaderIsTheStateImportsKeep.
describe('isRecordedStateHeader', () => {
  it.each([
    ['Cookie', true],
    [' set-cookie ', true],
    ['idpctx-session-id', true],
    ['X-Session-Token', true],
    ['X-Session-Key', true],
    ['X-CSRF-Token', true],
    ['X-XSRF-TOKEN', true],
    ['traceparent', true],
    ['X-Amzn-Trace-Id', true],
    ['X-B3-SpanId', true],
    ['X-Correlation-ID', true],
    ['Idempotency-Key', true],
    ['Authorization', false],
    ['X-Auth-Token', false],
    ['X-Span-Auth-Token', false],
    ['X-Session-Api-Key', false],
    ['X-Trace-Authorization', false],
    ['X-API-Key', false],
    ['Content-Type', false],
    ['', false],
  ])('%s → %s', (name, recorded) => {
    expect(isRecordedStateHeader(name)).toBe(recorded)
  })
})
