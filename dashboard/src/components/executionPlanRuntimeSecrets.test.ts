import { describe, expect, it } from 'vitest'
import type { PartialRuntimeSettingsOverride } from '../types/runtimeSettings'
import { planEntryRuntimeSecretError, planEntryTypedCredentialPaths } from './executionPlanRuntimeSecrets'

const ref = '{{secret.PROJECT_CRED}}'

describe('planEntryTypedCredentialPaths', () => {
  it('accepts a row with no secret-bearing fields, and empty or absent rows', () => {
    expect(planEntryTypedCredentialPaths(undefined)).toEqual([])
    expect(planEntryTypedCredentialPaths({})).toEqual([])
    expect(planEntryTypedCredentialPaths({ http: { request_timeout_ms: 5000, default_headers: { Accept: 'application/json' } }, base_url: 'https://api.example.test/v1?page=2' })).toEqual([])
  })

  it('flags every typed credential field the run-side mask covers', () => {
    const row = {
      soap: { ws_security: { username_token: { username: 'svc', password: 'hunter2' } } },
      http: {
        auth: { type: 'basic', username: 'ops', password: 'hunter2', token: 'tok' },
        proxy: { url: 'http://ops:hunter2@proxy.test:8080', password: 'hunter2', pac_url: 'https://pac.test/p.pac?token=abc' },
        default_headers: { Authorization: 'Bearer abc', 'X-Trace': 'Bearer leaked', Accept: 'text/html' },
        default_header_list: [{ name: 'X-API-Key', value: 'k-123' }, { name: 'Cookie', value: 'a=1; b={{secret.B}}' }],
      },
      base_url: 'https://svc.test/?api_key=abc',
      browser_config: {
        http_credentials: { username: 'ops', password: 'hunter2' },
        proxy: { server: 'ops:hunter2@proxy.test:3128', password: 'hunter2' },
        client_certificates: [{ pfx_path: '/c.pfx', passphrase: 'pp' }],
        session: { identities: [{ username: 'u', password: 'pw' }], jwt: { login_url: 'https://l.test', client_id: 'c', private_key: '-----BEGIN' } },
      },
      java_vuser: { additional_vm_parameters: ['-Xmx512m', '-Ddb.password=hunter2', '-Dlabel=Bearer abc'] },
      env: { API_TOKEN: 'abc', REGION: 'eu', DATABASE_URL: 'postgres://u:p@db.test/x' },
    } as PartialRuntimeSettingsOverride
    expect(planEntryTypedCredentialPaths(row)).toEqual([
      'soap.ws_security.username_token.password',
      'http.auth.password',
      'http.auth.token',
      'http.proxy.password',
      'http.proxy.url',
      'http.proxy.pac_url',
      'http.default_headers.Authorization',
      'http.default_headers.X-Trace',
      'http.default_header_list.X-API-Key',
      'http.default_header_list.Cookie',
      'base_url',
      'browser_config.http_credentials.password',
      'browser_config.proxy.password',
      'browser_config.proxy.server',
      'browser_config.client_certificates.0.passphrase',
      'browser_config.session.identities.0.password',
      'browser_config.session.jwt.private_key',
      'java_vuser.additional_vm_parameters.1',
      'java_vuser.additional_vm_parameters.2',
      'env.API_TOKEN',
      'env.DATABASE_URL',
    ])
  })

  it('accepts exact project credential references in every secret-bearing field', () => {
    const row = {
      soap: { ws_security: { username_token: { username: 'svc', password: ref } } },
      http: {
        auth: { username: 'ops', password: ref, token: ` ${ref} ` },
        proxy: { url: `http://${ref}:${ref}@proxy.test:8080`, password: ref, username: 'ops' },
        default_headers: { Authorization: `Bearer ${ref}`, 'X-API-Key': ref },
        default_header_list: [{ name: 'Cookie', value: `a=${ref}; b=${ref}` }],
      },
      base_url: `https://svc.test/?api_key=${ref}`,
      browser_config: { http_credentials: { password: ref }, proxy: { server: 'http://proxy.test:3128', password: ref } },
      java_vuser: { additional_vm_parameters: [`-Ddb.password=${ref}`] },
      env: { API_TOKEN: ref },
    } as PartialRuntimeSettingsOverride
    expect(planEntryTypedCredentialPaths(row)).toEqual([])
  })

  it('flags a typed browser storage state at the section level and per identity', () => {
    const state = '{"cookies":[{"name":"sid","value":"abc"}]}'
    expect(planEntryTypedCredentialPaths({
      browser_config: { storage_state: state, session: { strategy: 'storage_state_pool', identities: [{ username: 'u', storage_state: state }] } },
    } as PartialRuntimeSettingsOverride)).toEqual([
      'browser_config.storage_state',
      'browser_config.session.identities.0.storage_state',
    ])
    expect(planEntryTypedCredentialPaths({
      browser_config: { storage_state: ref, session: { identities: [{ username: 'u', storage_state: ref }] } },
    } as PartialRuntimeSettingsOverride)).toEqual([])
  })

  it('refuses controller-reserved APEX_RUNTIME_OVERRIDE_ references and embedded references', () => {
    expect(planEntryTypedCredentialPaths({ http: { auth: { password: '{{secret.APEX_RUNTIME_OVERRIDE_HTTP_AUTH}}' } } })).toEqual(['http.auth.password'])
    expect(planEntryTypedCredentialPaths({ http: { proxy: { password: `x${ref}` } } })).toEqual(['http.proxy.password'])
  })
})

describe('planEntryRuntimeSecretError', () => {
  it('names the offending paths and asks for a credential reference', () => {
    expect(planEntryRuntimeSecretError({ http: { auth: { password: ref } } })).toBe('')
    const message = planEntryRuntimeSecretError({ http: { proxy: { password: 'hunter2' } } })
    expect(message).toContain('{{secret.NAME}}')
    expect(message).toContain('http.proxy.password')
    expect(message).not.toContain('hunter2')
  })
})
