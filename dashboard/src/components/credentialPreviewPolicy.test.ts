import { describe, expect, it } from 'vitest'
import { isSensitiveCredentialPreviewName } from './credentialPreviewPolicy'

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
