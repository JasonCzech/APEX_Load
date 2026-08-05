import { describe, expect, it } from 'vitest'

import type { StoredScript } from '../types'
import { requiredScriptSecrets, scriptContainsSecretReferences } from './scriptSecretReferenceModel'

function storedScript(script: StoredScript['script']): StoredScript {
  return {
    id: 'script-1',
    name: 'Secrets',
    protocol: 'http',
    version: '1.0.0',
    created_at: '2026-08-04T00:00:00Z',
    script,
  }
}

describe('scriptSecretReferenceModel', () => {
  it('matches backend-valid templates and dedicated logical-secret fields', () => {
    const script = storedScript({
      name: 'Secrets',
      version: '1.0.0',
      protocol: 'database',
      config: {
        base_url: '',
        database: { dsn_secret: 'database-password' },
      },
      actions: [{ name: 'Query', type: 'database_query', target: '', params: { note: '{{ secret.login-token }}' } }],
    })

    expect(requiredScriptSecrets(script)).toEqual(['DATABASE_PASSWORD', 'LOGIN_TOKEN'])
    expect(scriptContainsSecretReferences(script)).toBe(true)
  })

  it('inspects encoded executable bodies but ignores root provenance and review-only actions', () => {
    const encodedBody = globalThis.btoa('{"token":"{{secret.ENCODED_TOKEN}}"}')
    const script = storedScript({
      name: '{{secret.DISPLAY_NAME}}',
      version: '1.0.0',
      protocol: 'http',
      metadata: { import: { preserved: { notes: '{{secret.PROVENANCE}}' } } },
      config: { base_url: '' },
      actions: [
        { name: 'Encoded', type: 'POST', target: '/', body: encodedBody, body_encoding: 'base64' },
        { name: 'Review', type: 'source_review', target: '{{secret.REVIEW_ONLY}}' },
      ],
    })

    expect(requiredScriptSecrets(script)).toEqual(['ENCODED_TOKEN'])
  })

  it('mirrors the backend exclusion for opaque Java VM parameters', () => {
    const script = storedScript({
      name: 'Java VUser',
      version: '1.0.0',
      protocol: 'java_vuser',
      config: {
        base_url: '',
        java_vuser: {
          additional_vm_parameters: ['-Dignored={{secret.OPAQUE_VM_VALUE}}'],
        },
      },
      actions: [{ name: 'Run', type: 'java_vuser', target: '', params: { token: '{{secret.RUNTIME_TOKEN}}' } }],
    })

    expect(requiredScriptSecrets(script)).toEqual(['RUNTIME_TOKEN'])
  })
})
