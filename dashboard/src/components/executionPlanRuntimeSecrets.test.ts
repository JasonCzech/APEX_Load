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

// Mirrors pkg/api TestExecutionPlanRejectsTypedProtocolCredentials and the
// URL/capability rules beside it (runtime_protocol_sections*_test.go).
describe('planEntryTypedCredentialPaths: native protocol sections', () => {
  const secret = 'Hunter2pw-protocol'
  const typed = {
    kafka: {
      sasl_mechanism: 'plain', sasl_username: 'kafka-user', sasl_password: secret,
      oauth: { mode: 'client_credentials', client_id: 'cid', client_secret: secret },
      schema_registry_url: `https://sr-user:${secret}@sr.example.test`,
      brokers: ['k1.example.test:9092', `u:${secret}@k2.example.test:9092`],
      properties: {
        'sasl.jaas.config': `org.apache.kafka.common.security.plain.PlainLoginModule required username="u" password="${secret}";`,
        'ssl.key.password': secret,
        'request.timeout.ms': '45000',
      },
    },
    jms: { username: 'jms-user', password: secret, broker_url: `tcp://b:${secret}@broker.example.test:55555`, properties: { conn: `Endpoint=sb://ns/;SharedAccessKeyName=send;SharedAccessKey=${secret}` } },
    eventhubs: { connection_string: `Endpoint=sb://ns/;SharedAccessKey=${secret}`, checkpoint_container_url: `https://acct.blob.core.windows.net/c?sv=1&sig=${secret}` },
    grpc: { metadata: { authorization: `Bearer ${secret}`, 'x-tenant': 'east' } },
    database: { driver: 'postgres', dsn: `postgres://app:${secret}@db.example.test/app` },
    network: { username: 'mail-user', password: secret },
    appium: {
      server_url: `https://cloud-user:${secret}@hub.example.test/wd/hub`,
      capabilities: { platformName: 'iOS', 'cloud:options': { accessKey: secret, build: 'nightly' } },
    },
  } as PartialRuntimeSettingsOverride

  it('flags every typed protocol credential and leaves identities and plain values alone', () => {
    const paths = planEntryTypedCredentialPaths(typed)
    for (const want of [
      'kafka.sasl_password', 'kafka.oauth.client_secret', 'kafka.schema_registry_url', 'kafka.brokers.1',
      'kafka.properties.sasl.jaas.config', 'kafka.properties.ssl.key.password', 'jms.password', 'jms.broker_url',
      'jms.properties.conn', 'eventhubs.connection_string', 'eventhubs.checkpoint_container_url',
      'grpc.metadata.authorization', 'database.dsn', 'network.password', 'appium.server_url',
      'appium.capabilities.cloud:options.accessKey',
    ]) {
      expect(paths).toContain(want)
    }
    for (const unexpected of ['kafka.sasl_username', 'kafka.brokers.0', 'kafka.properties.request.timeout.ms', 'grpc.metadata.x-tenant', 'network.username', 'appium.capabilities.platformName']) {
      expect(paths).not.toContain(unexpected)
    }
    expect(planEntryRuntimeSecretError(typed)).not.toContain(secret)
  })

  it('accepts project references, including reference userinfo and secret names', () => {
    expect(planEntryTypedCredentialPaths({
      kafka: { sasl_password: '{{secret.KAFKA}}', sasl_password_secret: 'KAFKA', properties: { 'sasl.jaas.config': 'password="{{secret.KAFKA}}";' }, schema_registry_url: 'https://{{secret.SR_USER}}:{{secret.SR_PW}}@sr.example.test' },
      database: { dsn: '{{secret.DSN}}', dsn_secret: '{{secret.DSN}}' },
      grpc: { metadata: { authorization: 'Bearer {{secret.TOKEN}}' } },
      appium: { server_url: 'https://{{secret.BS_USER}}:{{secret.BS_KEY}}@hub.browserstack.com/wd/hub' },
      eventhubs: { checkpoint_container_url: 'https://acct.blob.core.windows.net/c?sv=1&sig={{secret.SAS}}' },
    } as PartialRuntimeSettingsOverride)).toEqual([])
  })

  it('accepts a gRPC Cookie metadata value made only of references, and refuses a typed cookie', () => {
    expect(planEntryTypedCredentialPaths({ grpc: { metadata: { cookie: 'session={{secret.S}}; csrf={{secret.C}}' } } } as PartialRuntimeSettingsOverride)).toEqual([])
    expect(planEntryTypedCredentialPaths({ grpc: { metadata: { Cookie: 'session=abc; csrf={{secret.C}}' } } } as PartialRuntimeSettingsOverride)).toEqual(['grpc.metadata.Cookie'])
    expect(planEntryTypedCredentialPaths({ grpc: { metadata: { cookie: 'session={{secret.APEX_RUNTIME_OVERRIDE_G1_A}}' } } } as PartialRuntimeSettingsOverride)).toEqual(['grpc.metadata.cookie'])
  })

  it('refuses reserved references and literals in secret-name fields', () => {
    const paths = planEntryTypedCredentialPaths({
      database: { dsn: '{{secret.APEX_RUNTIME_OVERRIDE_G1_A}}', dsn_secret: 'postgres://u:p@db/app' },
      kafka: { properties: { x: 'a {{secret.APEX_RUNTIME_OVERRIDE_G1_A}}' } },
    } as PartialRuntimeSettingsOverride)
    expect(paths).toEqual(expect.arrayContaining(['database.dsn', 'database.dsn_secret', 'kafka.properties.x']))
  })

  it('treats device-cloud capability keys as credentials and quotes flat dotted keys', () => {
    const key = 'bs-access-key-9481'
    for (const capability of ['browserstack.key', 'testobject_api_key', 'lt:options.token', 'bstack:secret']) {
      const paths = planEntryTypedCredentialPaths({ appium: { capabilities: { 'browserstack.user': 'alice', [capability]: key } } } as PartialRuntimeSettingsOverride)
      expect(paths.some(path => path.includes(capability))).toBe(true)
      expect(paths.some(path => path.includes('browserstack.user'))).toBe(false)
    }
    expect(planEntryTypedCredentialPaths({ appium: { capabilities: { 'lt:options.accessKey': 'FLAT' } } } as PartialRuntimeSettingsOverride))
      .toEqual(['appium.capabilities["lt:options.accessKey"]'])
    expect(planEntryTypedCredentialPaths({ appium: { capabilities: { 'appium:autoAcceptAlerts': 'true', keyboard: 'hw' } } } as PartialRuntimeSettingsOverride)).toEqual([])
  })

  it('flags a token URL query secret and network address userinfo, not their plain forms', () => {
    expect(planEntryTypedCredentialPaths({
      kafka: { oauth: { token_url: 'https://idp.example/token?client_secret=x1', client_id: 'c' } },
      network: { address: 'smtp://bob:pw@mail:25' },
    } as PartialRuntimeSettingsOverride)).toEqual(['kafka.oauth.token_url', 'network.address'])
    expect(planEntryTypedCredentialPaths({
      kafka: { oauth: { token_url: 'https://idp.example/token', client_id: 'c' } },
      network: { address: 'mail.example.test:25' },
    } as PartialRuntimeSettingsOverride)).toEqual([])
  })
})
