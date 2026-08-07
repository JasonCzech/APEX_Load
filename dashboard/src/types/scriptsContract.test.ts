import { describe, expect, expectTypeOf, it } from 'vitest'

import type { JsonRequestBody } from './generated/openapi'
import type {
  BrowserConfig,
  BrowserGeolocation,
  BrowserHTTPCredentials,
  BrowserProxyConfig,
  EventHubsConfig,
  HTTPConfig,
  JMSConfig,
  JMSTLSConfig,
  RunLogicConfig,
  ScriptActionDefinition,
  ScriptAssertionDefinition,
  ScriptAuthConfig,
  ScriptConfig,
  ScriptControlFlow,
  ScriptDefinition,
  ScriptImportMetadata,
  ScriptMetadata,
  ScriptRuntimePolicy,
} from './scripts'

type CreateScriptBody = JsonRequestBody<'handleCreateScript'>
type GeneratedScript = NonNullable<CreateScriptBody['script']>
type GeneratedAction = NonNullable<GeneratedScript['actions']>[number]
type GeneratedAssertion = NonNullable<GeneratedAction['assertions']>[number]
type GeneratedControl = NonNullable<GeneratedAction['control']>
type GeneratedActionAuth = NonNullable<GeneratedAction['auth']>
type GeneratedConfig = NonNullable<GeneratedScript['config']>
type GeneratedHTTP = NonNullable<GeneratedConfig['http']>
type GeneratedHTTPTLS = NonNullable<GeneratedHTTP['tls']>
type GeneratedHTTPAuth = NonNullable<GeneratedHTTP['auth']>
type HandwrittenHTTPTLS = NonNullable<HTTPConfig['tls']>
type HandwrittenHTTPAuth = NonNullable<HTTPConfig['auth']>
type GeneratedBrowser = NonNullable<GeneratedConfig['browser']>
type GeneratedBrowserGeolocation = NonNullable<GeneratedBrowser['geolocation']>
type GeneratedBrowserCredentials = NonNullable<GeneratedBrowser['http_credentials']>
type GeneratedBrowserProxy = NonNullable<GeneratedBrowser['proxy']>
type GeneratedBrowserViewport = NonNullable<GeneratedBrowser['viewport']>
type GeneratedBrowserReviewPolicy = NonNullable<GeneratedBrowser['review_policy']>
type HandwrittenBrowserViewport = NonNullable<BrowserConfig['viewport']>
type HandwrittenBrowserReviewPolicy = NonNullable<BrowserConfig['review_policy']>
type GeneratedJMS = NonNullable<GeneratedConfig['jms']>
type GeneratedJMSTLS = NonNullable<GeneratedJMS['tls']>
type GeneratedEventHubs = NonNullable<GeneratedConfig['eventhubs']>
type GeneratedRunLogic = NonNullable<GeneratedConfig['run_logic']>
type GeneratedMetadata = NonNullable<GeneratedScript['metadata']>
type GeneratedRuntimePolicy = NonNullable<GeneratedMetadata['runtime_policy']>
type GeneratedImportMetadata = NonNullable<GeneratedMetadata['import']>

describe('ScriptDefinition runtime contract', () => {
  it('keeps handwritten editor keys aligned with the generated create-script contract', () => {
    expectTypeOf<keyof ScriptDefinition>().toEqualTypeOf<keyof GeneratedScript>()
    expectTypeOf<keyof ScriptActionDefinition>().toEqualTypeOf<keyof GeneratedAction>()
    expectTypeOf<keyof ScriptAssertionDefinition>().toEqualTypeOf<keyof GeneratedAssertion>()
    expectTypeOf<keyof ScriptControlFlow>().toEqualTypeOf<keyof GeneratedControl>()
    expectTypeOf<keyof ScriptAuthConfig>().toEqualTypeOf<keyof GeneratedActionAuth>()
    expectTypeOf<keyof HTTPConfig>().toEqualTypeOf<keyof GeneratedHTTP>()
    expectTypeOf<keyof HandwrittenHTTPTLS>().toEqualTypeOf<keyof GeneratedHTTPTLS>()
    expectTypeOf<keyof HandwrittenHTTPAuth>().toEqualTypeOf<keyof GeneratedHTTPAuth>()
  })

  // Value literals only prove the keys that exist today still round-trip; they
  // cannot notice a *new* pkg/dsl field. Key-equality on every block the editor
  // mirrors is what makes a future Go addition fail this suite instead of
  // silently becoming raw-JSON-only in the runtime settings editor.
  it('keeps the protocol config blocks the editor mirrors aligned key-for-key', () => {
    expectTypeOf<keyof ScriptConfig>().toEqualTypeOf<keyof GeneratedConfig>()
    expectTypeOf<keyof RunLogicConfig>().toEqualTypeOf<keyof GeneratedRunLogic>()
    expectTypeOf<keyof BrowserConfig>().toEqualTypeOf<keyof GeneratedBrowser>()
    expectTypeOf<keyof BrowserGeolocation>().toEqualTypeOf<keyof GeneratedBrowserGeolocation>()
    expectTypeOf<keyof BrowserHTTPCredentials>().toEqualTypeOf<keyof GeneratedBrowserCredentials>()
    expectTypeOf<keyof BrowserProxyConfig>().toEqualTypeOf<keyof GeneratedBrowserProxy>()
    expectTypeOf<keyof HandwrittenBrowserViewport>().toEqualTypeOf<keyof GeneratedBrowserViewport>()
    expectTypeOf<keyof HandwrittenBrowserReviewPolicy>().toEqualTypeOf<keyof GeneratedBrowserReviewPolicy>()
    expectTypeOf<keyof JMSConfig>().toEqualTypeOf<keyof GeneratedJMS>()
    expectTypeOf<keyof JMSTLSConfig>().toEqualTypeOf<keyof GeneratedJMSTLS>()
    expectTypeOf<keyof EventHubsConfig>().toEqualTypeOf<keyof GeneratedEventHubs>()
    expectTypeOf<keyof ScriptMetadata>().toEqualTypeOf<keyof GeneratedMetadata>()
    expectTypeOf<keyof ScriptRuntimePolicy>().toEqualTypeOf<keyof GeneratedRuntimePolicy>()
    expectTypeOf<keyof ScriptImportMetadata>().toEqualTypeOf<keyof GeneratedImportMetadata>()
  })

  it('models controller-injected execution and protocol settings without lossy casts', () => {
    const script: ScriptDefinition = {
      name: 'runtime-contract',
      version: '1.0',
      protocol: 'eventhubs',
      generated_by: 'deterministic-local',
      generated_from: 'recording-session-1',
      goal_config: { target_average_tps: 12.5 },
      metadata: { runtime_policy: { allowed_target_hosts: ['orders.example.com'] } },
      config: {
        base_url: '',
        run_logic: { workload_model: 'open', max_in_flight: 64 },
        browser: {
          locale: 'en-US',
          timezone: 'America/New_York',
          experience_canary: true,
          traceparent: true,
          har_capture: 'on_error',
          har_capture_policy: 'scrub',
          har_sla_threshold_ms: 2500,
          har_artifact_dir: '/tmp/browser-artifacts',
          geolocation: { latitude: 40.7, longitude: -74, accuracy: 20 },
          http_credentials: { username: 'operator', origin: 'https://example.com' },
          proxy: { server: 'https://proxy.example.com', bypass: 'localhost' },
          client_certificates: [{ origin: 'https://example.com', cert_path: 'client.pem' }],
        },
        jms: { tls: { ca_cert_secret: 'jms-ca', insecure_skip_verify: false } },
        eventhubs: {
          namespace: 'orders.servicebus.windows.net',
          event_hub: 'orders',
          consumer_group: 'load-tests',
          partition_key: 'customer-1',
          timeout_ms: 1500,
          mock: false,
        },
      },
      data_sources: [{
        name: 'customers',
        type: 'csv',
        staged_asset: 'asset/customers.csv',
        strategy: 'sequential',
      }],
      actions: [{
        name: 'publish',
        type: 'eventhubs_produce',
        target: 'orders',
        body_file: 'payload.json',
        think_time: { type: 'fixed', mean_ms: 250 },
        auth: { type: 'ntlm', username: 'operator', domain: 'CORP' },
        timeout_ms: 1_500,
        max_redirects: 0,
        assertions: [{ type: 'body_size', min: 1 }],
        control: { do_while: true },
      }],
    }

    const roundTrip = JSON.parse(JSON.stringify(script)) as ScriptDefinition

    expect(roundTrip.config.run_logic?.max_in_flight).toBe(64)
    expect(roundTrip.config.browser?.client_certificates?.[0].cert_path).toBe('client.pem')
    expect(roundTrip.config.jms?.tls?.ca_cert_secret).toBe('jms-ca')
    expect(roundTrip.config.eventhubs?.event_hub).toBe('orders')
    expect(roundTrip.metadata?.runtime_policy?.allowed_target_hosts).toEqual(['orders.example.com'])
    expect(roundTrip.data_sources?.[0].staged_asset).toBe('asset/customers.csv')
    expect(roundTrip.actions[0].body_file).toBe('payload.json')
    expect(roundTrip.actions[0].control?.do_while).toBe(true)
  })
})
