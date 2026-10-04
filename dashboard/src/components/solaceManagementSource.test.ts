import { expect, it } from 'vitest'
import { newSolaceIdentity, newSolaceScope, solaceApprovalProjection, solaceSourceSnapshot, solaceEntityIdentity, solaceSourceAvailable, solaceSourceError, type SolaceMonitorSource as Source } from './solaceManagementSource'
const source = (): Source => ({ id: '', project_id: 'project', name: 'Depth', revision: 0, collector_id: 'collector', pack: 'solace-semp', interval_seconds: 30, enabled: true, destination: 'management://broker', collector_public_key: 'public-key', management_scope: { ...newSolaceScope(), provider: 'broker.example.com', entities: [{ solace: { vpn: 'primary', queue: 'orders' } }] }, management_identity: { ...newSolaceIdentity(), principal: 'reader', policy_revision: 'p1', credential_ref: 'mgmt-secret' } })
it('requires the exact enabled executable pack', () => {
  expect(solaceSourceAvailable({ monitoring_enabled: true, messaging_management_enabled: true, messaging_management_packs: ['solace-semp'] })).toBe(true)
  for (const caps of [undefined, {}, { monitoring_enabled: false, messaging_management_enabled: true, messaging_management_packs: ['solace-semp'] }, { monitoring_enabled: true, messaging_management_enabled: false, messaging_management_packs: ['solace-semp'] }, { monitoring_enabled: true, messaging_management_enabled: true, messaging_management_packs: ['servicebus-runtime'] }]) expect(solaceSourceAvailable(caps)).toBe(false)
})
it('preserves exact case and escaped names as finite tuple identities', () => {
  const s = source(); s.management_scope!.entities = [{ solace: { vpn: 'Sales/West', queue: '..' } }, { solace: { vpn: 'Sales/West', topic_endpoint: 'a/b%+?☃' } }, { solace: { vpn: 'Sales/West', queue: 'Orders' } }, { solace: { vpn: 'Sales/West', queue: 'orders' } }]
  expect(solaceSourceError(s)).toBe('')
  expect(solaceEntityIdentity(s.management_scope!.entities[1])).toBe(JSON.stringify(['Sales/West', 'topic_endpoint', 'a/b%+?☃']))
  s.management_scope!.entities.push({ solace: { vpn: 'Sales/West', queue: 'orders' } })
  expect(solaceSourceError(s)).toContain('duplicate')
})
it.each([29, 3601, 30.5])('rejects provider cadence %s', interval => { const s = source(); s.interval_seconds = interval; expect(solaceSourceError(s)).toContain('30–3600') })
it.each(['https://broker.example.com', 'BROKER.example.com', 'broker.example.com.', '127.0.0.1', 'broker.example.com:443', 'localhost'])('rejects noncanonical hostname %s', provider => { const s = source(); s.management_scope!.provider = provider; expect(solaceSourceError(s)).toContain('hostname') })
it.each(['anonymous', 'shared_key', 'workload_identity'])('rejects unsupported identity %s', mode => { const s = source(); s.management_identity!.mode = mode; expect(solaceSourceError(s)).toContain('Basic') })
it('rejects mixed providers, missing/both names, colon principal and malformed text', () => {
  const entities = [{ solace: { vpn: 'v', queue: '' } }, { solace: { vpn: 'v', queue: 'q', topic_endpoint: 't' } }, { solace: { vpn: 'v', queue: 'q' }, kafka: { topic: 't', group: 'g', partition: 0 } }, { solace: { vpn: '\u0085v', queue: 'q' } }, { solace: { vpn: 'v', queue: '\ud800' } }, { solace: { vpn: 'v', queue: 'a\0b' } }, { solace: { vpn: 'v', queue: '☃'.repeat(86) } }]
  for (const [index, entity] of entities.entries()) { const s = source(); s.management_scope!.entities = [entity]; expect(solaceSourceError(s)).toContain(index === 4 ? 'plain JSON' : 'exactly one') }
  const s = source(); s.management_identity!.principal = 'reader:foreign'; expect(solaceSourceError(s)).toContain('colon')
  s.management_identity!.principal = 'reader'; s.credential_ref = 'data-plane'; expect(solaceSourceError(s)).toContain('separate')
})
it('refuses missing and oversized inventories and whole-source bytes', () => {
  const empty = source(); empty.management_scope!.entities = []; expect(solaceSourceError(empty)).toContain('1 and 256')
  const excessive = source(); excessive.management_scope!.entities = Array.from({ length: 257 }, (_, i) => ({ solace: { vpn: 'v', queue: `q${i}` } })); expect(solaceSourceError(excessive)).toContain('plain JSON')
  const s = source(); s.management_scope!.entities = Array.from({ length: 100 }, (_, i) => ({ solace: { vpn: 'v'.repeat(200), queue: `q${i}` } })); expect(solaceSourceError(s)).toContain('16 KiB')
})
it('admits before complete serialization and never invokes accessors', () => {
  const oversized = { ...source(), name: 'x'.repeat(16 * 1024 + 1) }
  expect(solaceSourceError(oversized)).toContain('16 KiB')
  const population = { ...source(), metadata: Array.from({ length: 257 }, () => 1) }
  expect(solaceSourceError(population)).toContain('16 KiB')
  const getter = source(); let invoked = false
  Object.defineProperty(getter, 'management_scope', { enumerable: true, get() { invoked = true; throw new Error('must not run') } })
  expect(solaceSourceError(getter)).toContain('plain JSON'); expect(invoked).toBe(false)
  const cycle = { ...source(), metadata: {} as Record<string, unknown> }; cycle.metadata.self = cycle.metadata
  expect(solaceSourceError(cycle)).toContain('plain JSON')
})

it('projects only admitted data and never reads a hidden optional-field getter', () => {
  const s = source(); let invoked = false
  Object.defineProperty(s.management_identity!, 'credential_version', { enumerable: false, get() { invoked = true; return 'x'.repeat(100_000) } })
  const projection = solaceApprovalProjection(s)
  expect(projection).toBeDefined(); expect(invoked).toBe(false)
  expect(JSON.stringify(projection)).not.toContain('credential_version')
  expect(JSON.stringify(projection)).toContain('reader')
})

it.each([NaN, Infinity, -Infinity])('rejects non-JSON numeric cadence before semantic validation: %s', interval => {
  const s = source(); s.interval_seconds = interval; expect(solaceSourceError(s)).toContain('finite plain JSON')
})
it('counts exact UTF-8 serialized source bytes at the hard admission boundary', () => {
  const base = { ...source(), metadata: '' }
  const prefix = new TextEncoder().encode(JSON.stringify(base)).length
  const boundary = { ...base, metadata: 'a'.repeat(16 * 1024 - prefix) }
  expect(new TextEncoder().encode(JSON.stringify(boundary)).length).toBe(16 * 1024)
  expect(solaceSourceSnapshot(boundary).source).toBeDefined()
  expect(solaceSourceSnapshot({ ...boundary, metadata: boundary.metadata + 'a' }).source).toBeUndefined()
  const wide = { ...base, metadata: '☃'.repeat(Math.floor((16 * 1024 - prefix) / 3)) }
  expect(solaceSourceSnapshot(wide).source).toBeDefined()
  expect(solaceSourceSnapshot({ ...wide, metadata: wide.metadata + '☃' }).source).toBeUndefined()
})

it.each(['\t', '\n', '\v', '\f', '\r', '\u0085', ' ', '\u00a0', '\u1680', '\u2000', '\u200a', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000'])('rejects Unicode boundary whitespace without changing the exact tuple: %j', space => {
  for (const vpn of [`${space}v`, `v${space}`]) { const s = source(); s.management_scope!.entities = [{ solace: { vpn, queue: 'q' } }]; expect(solaceSourceError(s)).toContain('exactly one') }
})
