import { describe, expect, it } from 'vitest'

import type { StoredScript } from '../types'
import type { ScriptDefinition } from '../types/scripts'
import { requiredScriptSecrets, scriptContainsSecretReferences, scriptSecretReferenceSummaries } from './scriptSecretReferenceModel'

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

describe('messaging secret reference fields', () => {
  it('lists a Kafka OAuth client secret named by its plain client_secret_ref', () => {
    const script = {
      name: 'Orders', version: '1', protocol: 'kafka', actions: [],
      config: { kafka: { oauth: { client_id: '{{secret.KAFKA_CLIENT_ID}}', client_secret_ref: 'kafka.client-secret' } } },
    } as unknown as ScriptDefinition
    expect(scriptSecretReferenceSummaries(script).map(summary => [summary.name, summary.locations])).toEqual([
      ['KAFKA_CLIENT_ID', ['/config/kafka/oauth/client_id']],
      ['KAFKA_CLIENT_SECRET', ['/config/kafka/oauth/client_secret_ref']],
    ])
  })

  it('requires a proxy credential secret only while that proxy sends its credentials', () => {
    const proxied = (mode: string, browserServer?: string) => storedScript({
      name: 'Proxy', version: '1.0.0', protocol: 'http',
      config: {
        base_url: '',
        http: { proxy_mode: mode, proxy: { url: 'http://proxy.example.test:8080', username: 'svc', password: '{{secret.ENTERPRISE_PROXY_PASSWORD}}' } },
        browser: { proxy: { ...(browserServer ? { server: browserServer } : {}), password: '{{secret.BROWSER_PROXY_PASSWORD}}' } },
      },
      actions: [{ name: 'Home', type: 'GET', target: '/' }],
    })
    for (const mode of ['none', 'system', 'pac']) {
      const script = proxied(mode)
      expect(requiredScriptSecrets(script)).toEqual([])
      // Still a reference: the bindings dialog lists it and it stays bindable.
      expect(scriptContainsSecretReferences(script)).toBe(true)
      expect(scriptSecretReferenceSummaries(script.script).map(summary => summary.name)).toEqual(['BROWSER_PROXY_PASSWORD', 'ENTERPRISE_PROXY_PASSWORD'])
    }
    expect(requiredScriptSecrets(proxied('custom'))).toEqual(['ENTERPRISE_PROXY_PASSWORD'])
    expect(requiredScriptSecrets(proxied('none', 'http://proxy.example.test:3128'))).toEqual(['BROWSER_PROXY_PASSWORD'])
  })

  it('keeps requiring a secret an inactive proxy credential shares with a step', () => {
    const script = storedScript({
      name: 'Proxy', version: '1.0.0', protocol: 'http',
      config: { base_url: '', http: { proxy_mode: 'system', proxy: { password: '{{secret.SHARED}}' } } },
      actions: [{ name: 'Login', type: 'POST', target: '/login', body: '{"p":"{{secret.SHARED}}"}' }],
    })
    expect(requiredScriptSecrets(script)).toEqual(['SHARED'])
  })
})
