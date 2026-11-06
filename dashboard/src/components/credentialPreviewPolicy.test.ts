import { describe, expect, it } from 'vitest'
import { isCredentialLocatorName, isRecordedStateHeader, isSensitiveCredentialPreviewKey, isSensitiveCredentialPreviewName } from './credentialPreviewPolicy'

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

// Mirrors the pkg/credential.SensitiveName exemptions and pkg/scriptdisclosure
// isTokenEndpointKey / isNonSecretIdentifierKey: where a credential is used,
// or which one, is not the credential.
// Mirrors pkg/credential.NonCredentialName (SensitiveName does not apply it)
// and pkg/scriptdisclosure isTokenEndpointKey / isNonSecretIdentifierKey.
describe('credential locator names', () => {
  it.each([
    // [name, Go SensitiveName result]: camelCase is not split by either side.
    ['token_url', true],
    ['tokenUrl', false],
    ['sasl.oauthbearer.token.endpoint.url', true],
    ['oauth_token_endpoint_url', true],
    ['shared_access_key_name', true],
    ['SharedAccessKeyName', false],
  ])('%s is a locator key; by name sensitive %s', (name, byName) => {
    expect(isSensitiveCredentialPreviewKey(name)).toBe(false)
    expect(isCredentialLocatorName(name)).toBe(true)
    expect(isSensitiveCredentialPreviewName(name)).toBe(byName)
  })
  it.each([
    // Neighbours that still hold or carry a credential.
    ['access_token_url', true],
    ['token', true],
    ['shared_access_key', true],
    ['client_secret', true],
    ['sas_url', true],
  ])('%s → sensitive %s', (name, sensitive) => {
    expect(isSensitiveCredentialPreviewName(name)).toBe(sensitive)
    expect(isSensitiveCredentialPreviewKey(name)).toBe(sensitive)
    expect(isCredentialLocatorName(name)).toBe(!sensitive)
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
