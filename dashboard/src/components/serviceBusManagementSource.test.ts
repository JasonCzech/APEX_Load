import { describe, expect, it } from 'vitest'
import { newServiceBusIdentity, newServiceBusScope, serviceBusEntityPath, serviceBusSourceAvailable, serviceBusSourceError, type ServiceBusMonitorSource as Source } from './serviceBusManagementSource'

const validSource = (): Source => ({ id: 'source', project_id: 'project', revision: 1, name: 'Runtime counts', collector_id: 'collector', pack: 'servicebus-runtime', enabled: true, interval_seconds: 30, destination: 'management://namespace', collector_public_key: 'public-key', management_scope: { ...newServiceBusScope(), provider: 'example.servicebus.windows.net', entities: [{ service_bus: { queue: 'Orders/Incoming' } }, { service_bus: { topic: 'Events', subscription: 'Workers' } }] }, management_identity: { ...newServiceBusIdentity(), principal: 'ManageReader', policy_revision: 'v1', credential_ref: 'management-secret', credential_version: 'saved-version' } })

it('offers the reader only when monitoring, management and the exact executable pack are enabled', () => {
  const capability = { monitoring_enabled: true, messaging_management_enabled: true, messaging_management_packs: ['servicebus-runtime', 'unimplemented'] }
  expect(serviceBusSourceAvailable(capability)).toBe(true)
  for (const value of [undefined, {}, { ...capability, monitoring_enabled: false }, { ...capability, messaging_management_enabled: false }, { ...capability, messaging_management_packs: ['kafka-offsets'] }, { ...capability, messaging_management_packs: null }]) expect(serviceBusSourceAvailable(value)).toBe(false)
})

it('creates a separate shared-key identity and an explicit empty queue scope', () => {
  expect(newServiceBusScope()).toEqual({ version: 1, kind: 'servicebus_runtime', provider: '', entities: [{ service_bus: { queue: '' } }] })
  expect(newServiceBusIdentity()).toEqual({ mode: 'shared_key', principal: '', policy_revision: '', credential_ref: '' })
  expect(serviceBusSourceError(validSource())).toBe('')
})

it('preserves authored tuple casing while deriving only a canonical duplicate key', () => {
  const source = validSource()
  expect(serviceBusEntityPath(source.management_scope!.entities[0])).toBe('/orders/incoming')
  expect(serviceBusEntityPath(source.management_scope!.entities[1])).toBe('/events/Subscriptions/workers')
  expect(source.management_scope!.entities[0].service_bus!.queue).toBe('Orders/Incoming')
  expect(source.management_scope!.entities[1].service_bus).toEqual({ topic: 'Events', subscription: 'Workers' })
  const duplicate = validSource(); duplicate.management_scope!.entities.push({ service_bus: { queue: 'orders/INCOMING' } })
  expect(serviceBusSourceError(duplicate)).toContain('case-aliased')
  const subscriptionAlias = validSource(); subscriptionAlias.management_scope!.entities.push({ service_bus: { topic: 'EVENTS', subscription: 'workers' } })
  expect(serviceBusSourceError(subscriptionAlias)).toContain('case-aliased')
})

describe('finite Service Bus path grammar', () => {
  it('accepts exact ASCII queue/topic and subscription bounds', () => {
    const source = validSource()
    source.management_scope!.entities = [{ service_bus: { queue: 'q'.repeat(256) } }, { service_bus: { topic: 't'.repeat(256), subscription: 's'.repeat(50) } }]
    expect(serviceBusSourceError(source)).toBe('')
    expect(serviceBusEntityPath({ service_bus: { queue: 'a/one_two.three-four/Z9' } })).toBe('/a/one_two.three-four/z9')
  })
  it.each(['', 'q'.repeat(257), '/queue', 'queue/', 'a//b', '.', '..', 'a/../b', '_queue', 'queue_', '-queue', 'queue-', 'queue.', 'a\\b', 'a%2Fb', 'a?b', 'a#b', 'a:b', 'a b', 'a\u0000b', 'a\u0085b', 'café', 'Subscriptions', 'a/MESSAGES/b', 'AuthorizationRules', 'Rules', '$DeadLetterQueue'])('rejects ambiguous or unsafe queue path %j', queue => {
    expect(serviceBusEntityPath({ service_bus: { queue } })).toBeUndefined()
  })
  it.each(['', 's'.repeat(51), 'a/b', '.', '..', 'a?', 'a#', 'a%', 'Subscriptions', 'rules', 'Ω'])('rejects unsafe single-segment subscription %j', subscription => {
    expect(serviceBusEntityPath({ service_bus: { topic: 'events', subscription } })).toBeUndefined()
  })
  it('requires exactly one typed queue or topic/subscription tuple', () => {
    for (const entity of [{}, { service_bus: {} }, { service_bus: { topic: 't' } }, { service_bus: { subscription: 's' } }, { service_bus: { queue: 'q', topic: 't', subscription: 's' } }, { service_bus: { queue: 'q' }, kafka: { topic: 't', partition: 0, group: 'g' } }, { service_bus: { queue: 'q' }, solace: { vpn: 'v', queue: 'q' } }]) expect(serviceBusEntityPath(entity)).toBeUndefined()
  })
})

it('requires canonical namespace identity and excludes Kafka isolation', () => {
  for (const provider of ['', 'namespace', '127.0.0.1', 'EXAMPLE.servicebus.windows.net', 'example.servicebus.windows.net.', 'https://example.servicebus.windows.net', 'example.servicebus.windows.net:443', 'example/path', 'example?query', 'example#fragment', 'a..b', '-a.example', 'a-.example', `${'a'.repeat(64)}.example`, 'é.example', 'a\u0001.example', 'a'.repeat(254)]) {
    const source = validSource(); source.management_scope!.provider = provider
    expect(serviceBusSourceError(source)).toContain('hostname')
  }
  const isolated = validSource(); isolated.management_scope!.isolation = 'read_committed'
  expect(serviceBusSourceError(isolated)).toContain('only to Kafka')
})

it('enforces the initial credential profile and never falls back to script credentials', () => {
  for (const mode of ['anonymous', 'sasl_plain', 'workload', 'api_token']) {
    const source = validSource(); source.management_identity!.mode = mode
    expect(serviceBusSourceError(source)).toContain('shared-key')
  }
  for (const value of ['', ' padded', 'padded ', 'bad\u0000value', 'bad\u007fvalue', 'bad\u0085value', '\ud800', 'é', 'p'.repeat(51), '_policy', 'policy-', 'p/other']) {
    const source = validSource(); source.management_identity!.principal = value
    expect(serviceBusSourceError(source)).toContain('policy name')
  }
  for (const value of ['', ' padded', 'padded ', 'bad\u0000value', 'bad\u007fvalue', 'bad\u0085value', '\ud800', 'é'.repeat(129)]) {
    const source = validSource(); source.management_identity!.policy_revision = value
    expect(serviceBusSourceError(source)).toContain('policy name')
  }
  const exact = validSource(); exact.management_identity!.principal = 'p'.repeat(50); exact.management_identity!.policy_revision = 'é'.repeat(128)
  expect(serviceBusSourceError(exact)).toBe('')
  const reservedPolicy = validSource(); reservedPolicy.management_identity!.principal = 'Rules'
  expect(serviceBusSourceError(reservedPolicy)).toBe('')
  for (const field of ['credential_ref', 'collector_public_key'] as const) {
    const source = validSource()
    if (field === 'credential_ref') source.management_identity!.credential_ref = 'é'.repeat(65)
    else source.collector_public_key = 'é'.repeat(65)
    expect(serviceBusSourceError(source)).toContain('collector public key')
  }
  const legacy = validSource(); legacy.credential_ref = 'script-secret'
  expect(serviceBusSourceError(legacy)).toContain('separate management')
})

it('rejects invalid aliases, cadence, inventory size and complete source body size', () => {
  for (const destination of ['', 'https://example.servicebus.windows.net', 'management://broker/path', 'management://broker?query', 'management://broker#fragment', 'management://' + 'a'.repeat(129)]) {
    const source = validSource(); source.destination = destination
    expect(serviceBusSourceError(source)).toContain('management://alias')
  }
  for (const interval of [29, 3601, 30.5, Number.NaN]) {
    const source = validSource(); source.interval_seconds = interval
    expect(serviceBusSourceError(source)).toContain('30–3600')
  }
  const upper = validSource(); upper.interval_seconds = 3600
  expect(serviceBusSourceError(upper)).toBe('')
  for (const count of [0, 257]) {
    const source = validSource(); source.management_scope!.entities = Array.from({ length: count }, (_, index) => ({ service_bus: { queue: `q${index}` } }))
    expect(serviceBusSourceError(source)).toContain('256')
  }
  const large = validSource(); large.management_scope!.entities = Array.from({ length: 80 }, (_, index) => ({ service_bus: { queue: `q${index}/${'q'.repeat(240)}` } }))
  expect(serviceBusSourceError(large)).toContain('16 KiB')
})
