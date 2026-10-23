// @ts-expect-error Vitest runs in Node; the dashboard app intentionally omits Node globals.
import { webcrypto } from 'node:crypto'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { telemetryPolicyChosen, telemetryPolicyView } from './telemetryPolicyModel'

const receipt = () => ({ availability: 'available' as const, policy: { schema_version: 'apexload.telemetry_policy.v1' as const, policy_id: 'system-telemetry-budgets' as const, revision: 1 as const, hash: 'e6fe795b839c0b8b2769d48105525f307343b41f20e1ca2e10dc19580fdfea25', captured_at: '2026-10-05T12:00:00Z', capture_boundary: 'execution-admission-before-staging' as const, classification_algorithm: 'legacy-mixed-diagnostic.v1' as const, expected_controller_publication_interval_ms: 3000, cadence_scope: 'local-controller-ticker' as const, sustained_breach_window_ms: 15000, coverage_rule: 'retained-classifications-only;gaps-unknown' as const, budgets: [{ stage: 'sample_age' as const, enabled: true, limit_ms: 11 }, { stage: 'generator_processing' as const, enabled: false, limit_ms: 0 }, { stage: 'transport' as const, enabled: true, limit_ms: 750 }, { stage: 'controller_fanout' as const, enabled: true, limit_ms: 250 }, { stage: 'browser_render' as const, enabled: true, limit_ms: 1e-12 }] } })

describe('telemetryPolicyView', () => {
  beforeEach(() => vi.stubGlobal('crypto', webcrypto))
  afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })
  it('preserves explicit disabled budgets and owns its receipt projection', async () => {
    const original = receipt()
    const view = await telemetryPolicyView(original)
    expect(view.policy?.budgets[0].limit_ms).toBe(11)
    expect(view.policy?.budgets[1]).toEqual({ stage: 'generator_processing', enabled: false, limit_ms: 0 })
    original.policy.budgets[0].limit_ms = 999
    expect(view.policy?.budgets[0].limit_ms).toBe(11)
  })
  it.each([undefined, { availability: 'unavailable', reason: 'immutable-policy-not-retained' }])('does not manufacture legacy policy identity: %s', async value => {
    const view = await telemetryPolicyView(value)
    expect(view.policy).toBeUndefined()
    expect(view.reason).toContain('No immutable policy receipt')
  })
  it.each([
    ['unknown availability', (v: Record<string, unknown>) => { v.availability = 'healthy' }],
    ['missing policy', (v: Record<string, unknown>) => { delete v.policy }],
    ['extra secret field', (v: Record<string, unknown>) => { v.secret = 'should-not-render' }],
    ['missing required enabled flag', (v: Record<string, unknown>) => { delete ((v.policy as Record<string, unknown>).budgets as Record<string, unknown>[])[0].enabled }],
    ['missing required cadence', (v: Record<string, unknown>) => { delete (v.policy as Record<string, unknown>).expected_controller_publication_interval_ms }],
    ['fractional cadence', (v: Record<string, unknown>) => { (v.policy as Record<string, unknown>).expected_controller_publication_interval_ms = 1500 }],
    ['subsecond cadence', (v: Record<string, unknown>) => { (v.policy as Record<string, unknown>).expected_controller_publication_interval_ms = 1 }],
    ['false disabled positive', (v: Record<string, unknown>) => { ((v.policy as Record<string, unknown>).budgets as Record<string, unknown>[])[0].enabled = false }],
    ['negative budget', (v: Record<string, unknown>) => { ((v.policy as Record<string, unknown>).budgets as Record<string, unknown>[])[0].limit_ms = -1 }],
    ['NaN budget', (v: Record<string, unknown>) => { ((v.policy as Record<string, unknown>).budgets as Record<string, unknown>[])[0].limit_ms = NaN }],
    ['duplicated stage', (v: Record<string, unknown>) => { ((v.policy as Record<string, unknown>).budgets as Record<string, unknown>[])[1].stage = 'sample_age' }],
    ['future algorithm', (v: Record<string, unknown>) => { (v.policy as Record<string, unknown>).classification_algorithm = 'future' }],
    ['incorrect distributed cadence', (v: Record<string, unknown>) => { (v.policy as Record<string, unknown>).cadence_scope = 'distributed-controller-coalescing-gate' }],
    ['UTC offset receipt', (v: Record<string, unknown>) => { (v.policy as Record<string, unknown>).captured_at = '2026-10-05T12:00:00+00:00' }],
    ['normalized invalid calendar day', (v: Record<string, unknown>) => { (v.policy as Record<string, unknown>).captured_at = '2026-02-30T12:00:00Z' }],
    ['null budget', (v: Record<string, unknown>) => { ((v.policy as Record<string, unknown>).budgets as unknown[])[0] = null }],
  ])('refuses %s with no budget or unsafe detail projection', async (_, mutate) => {
    const value = receipt() as unknown as Record<string, unknown>
    mutate(value)
    expect(await telemetryPolicyView(value)).toEqual({ reason: 'The retained policy receipt is invalid or unsupported.' })
  })
  it('refuses an explicitly null receipt instead of relabeling it as legacy absence', async () => {
    expect((await telemetryPolicyView(null)).reason).toContain('invalid')
  })
  it('does not invent a legacy reason from a malformed unavailable object', async () => {
    expect((await telemetryPolicyView({ availability: 'unavailable' })).reason).toContain('invalid')
  })
  it('distinguishes an invalid server receipt from legacy absence', async () => {
    expect((await telemetryPolicyView({ availability: 'unavailable', reason: 'immutable-policy-invalid' })).reason).toContain('invalid')
  })
  it('refuses a valid-shaped changed budget with the original semantic hash', async () => {
    const value = receipt()
    value.policy.budgets[0].limit_ms = 12
    expect((await telemetryPolicyView(value)).policy).toBeUndefined()
    expect((await telemetryPolicyView(value)).reason).toContain('invalid')
  })
  it('owns its candidate before an asynchronous digest completes', async () => {
    const value = receipt()
    const pending = telemetryPolicyView(value)
    value.policy.budgets[0].limit_ms = 999
    expect((await pending).policy?.budgets[0].limit_ms).toBe(11)
  })
  it('does not advertise availability without a functioning digest verifier', async () => {
    vi.stubGlobal('crypto', undefined)
    expect((await telemetryPolicyView(receipt())).reason).toContain('verification is unavailable')
    vi.stubGlobal('crypto', { subtle: { digest: async () => { throw new Error('must-not-display') } } })
    expect(await telemetryPolicyView(receipt())).toEqual({ reason: 'Policy digest verification is unavailable in this browser context.' })
  })
  // Golden values generated by Go1.26.5 encoding/json using the actual
  // api.TelemetryBudgetPolicy and validated by ValidateTelemetryBudgetPolicy.
  // See the retained telemetry-digest-pinned-go-vectors receipt/input.
  it.each([
    [0, 'ef50f5aacb9f510ad18e1b730aa74b6b24f3fbd3244a93720111b3968d3ef8bc'],
    [-0, '7f680e69e4c5fd20aaa6d20518efeeb924bbb15c2a08003d67fac65e5badbe9e'],
    [0.1, '77c70efeb38c26552cbf39c8fb274bc34e6856f3acf41ad6fd5bf116b0f2f226'],
    [1e-12, '562a19ebea0a3446432c88906d5ef15a26b6d1c30379be58a30c7e6258e4a138'],
    [1e-7, 'a7f63ddf346665975324cf6503f64646396116fb8caf887a8c3ff63729d8abdc'],
    [1e-6, '1e661e27c8be30952796566fcbcebbee1ab1a29cf6e7c83f099b747e4c2c7cb2'],
    [1e20, 'bc0e56ebf6102885a29c8f2327b93c80de583974cb2d8b66372d275f8243a2dc'],
    [1e21, '5315874b4cd05ba09b09652188610cc843eaa4e9d230fc29bfa752e3e2e5026c'],
    [5e-324, '2e4e8228ada9933dab337493c11e970fc08ba47134560abeab3fe80fae74627f'],
    [Number.MAX_VALUE, '0e7ac0e1d156a8bba6d0bc9d1eff6c7733fa7e012397b77bfbe9df9b3abe0032'],
    [1.0000000000000002, 'f723d3cd21984d3c9aa555ee0696fa1ca56707d8109d5e441570a68792fa6ac0'],
    [9007199254740992, '3b1b16526d477e090d6449e69c7985106fd7ed4c0e292eb8eb6b392a4bf32277'],
    [1000000000000000100, 'f1b0cddb4e168ae8f21065b7826143e0361e1460a6965d8fcc791e2dacbbd98b'],
  ])('admits the actual Go digest for float boundary %s', async (limit, hash) => {
    const value = receipt()
    value.policy.budgets[0].limit_ms = limit as number
    value.policy.budgets[0].enabled = (limit as number) > 0
    value.policy.hash = hash as string
    expect((await telemetryPolicyView(value)).policy?.hash).toBe(hash)
  })

  it.each(['0001-01-01T00:00:00Z', '0001-01-01T00:00:00.000000000Z'])('refuses the Go zero capture time %s even with an otherwise valid unchanged digest', async captured => {
    const value = receipt()
    value.policy.captured_at = captured
    expect((await telemetryPolicyView(value)).reason).toContain('invalid')
  })
  it('preserves a nonzero nanosecond capture time that Go admits', async () => {
    const value = receipt()
    value.policy.captured_at = '0001-01-01T00:00:00.000000001Z'
    expect((await telemetryPolicyView(value)).policy?.captured_at).toBe(value.policy.captured_at)
  })

  // A version 2 receipt names a project budget policy revision the run chose
  // and its definition digest; the hash is the controller's.
  const projectReceipt = () => {
    const value = receipt() as unknown as { availability: string; policy: Record<string, unknown> }
    Object.assign(value.policy, { schema_version: 'apexload.telemetry_policy.v2', policy_id: 'tight-budgets', revision: 3, definition_sha256: 'ab'.repeat(32), hash: 'f034a3ac9bc132b32ff6487f8f2598e799676832958d96049f4b2dd7914a265b' })
    return value
  }
  it('verifies a chosen project policy receipt against the controller digest', async () => {
    const view = await telemetryPolicyView(projectReceipt())
    expect(view.policy?.policy_id).toBe('tight-budgets')
    expect(view.policy?.definition_sha256).toBe('ab'.repeat(32))
    expect(view.policy && telemetryPolicyChosen(view.policy)).toBe(true)
    const system = await telemetryPolicyView(receipt())
    expect(system.policy && telemetryPolicyChosen(system.policy)).toBe(false)
  })
  it.each([
    ['v1 with a digest', (p: Record<string, unknown>) => { Object.assign(p, { schema_version: 'apexload.telemetry_policy.v1', policy_id: 'system-telemetry-budgets', revision: 1 }) }],
    ['v2 without a digest', (p: Record<string, unknown>) => { delete p.definition_sha256 }],
    ['v2 null digest', (p: Record<string, unknown>) => { p.definition_sha256 = null }],
    ['v2 upper-case digest', (p: Record<string, unknown>) => { p.definition_sha256 = 'AB'.repeat(32) }],
    ['v2 short digest', (p: Record<string, unknown>) => { p.definition_sha256 = 'ab'.repeat(31) }],
    ['v2 empty id', (p: Record<string, unknown>) => { p.policy_id = '' }],
    ['v2 system id', (p: Record<string, unknown>) => { p.policy_id = 'system-telemetry-budgets' }],
    ['v2 padded id', (p: Record<string, unknown>) => { p.policy_id = ' tight-budgets' }],
    ['v2 id ending in a next-line character, a control character', (p: Record<string, unknown>) => { p.policy_id = 'tight\u0085' }],
    // Each non-control character Go's strings.TrimSpace removes, trailing and
    // leading.
    ...[' ', '\u00a0', '\u1680', ...Array.from({ length: 11 }, (_, i) => String.fromCharCode(0x2000 + i)), '\u2028', '\u2029', '\u202f', '\u205f', '\u3000'].flatMap(space => [
      [`v2 id ending in U+${space.charCodeAt(0).toString(16).padStart(4, '0')}`, (p: Record<string, unknown>) => { p.policy_id = `tight${space}` }],
      [`v2 id starting with U+${space.charCodeAt(0).toString(16).padStart(4, '0')}`, (p: Record<string, unknown>) => { p.policy_id = `${space}tight` }],
    ] as [string, (p: Record<string, unknown>) => void][]),
    ['v2 lone surrogate id', (p: Record<string, unknown>) => { p.policy_id = 'tight\ud800' }],
    ['v2 control character id', (p: Record<string, unknown>) => { p.policy_id = 'tight\u0007' }],
    ['v2 long id', (p: Record<string, unknown>) => { p.policy_id = 'é'.repeat(65) }],
    ['v2 revision zero', (p: Record<string, unknown>) => { p.revision = 0 }],
    ['v2 fractional revision', (p: Record<string, unknown>) => { p.revision = 1.5 }],
    ['unknown version', (p: Record<string, unknown>) => { p.schema_version = 'apexload.telemetry_policy.v3' }],
  ])('refuses a project policy receipt with %s before any digest', async (_, mutate) => {
    const digest = vi.spyOn(webcrypto.subtle, 'digest')
    const value = projectReceipt()
    mutate(value.policy)
    expect(await telemetryPolicyView(value)).toEqual({ reason: 'The retained policy receipt is invalid or unsupported.' })
    expect(digest).not.toHaveBeenCalled()
  })
  // Go-computed hashes: a 128-byte ID, an ID Go's encoder escapes, and a
  // leading U+FEFF, which strings.TrimSpace keeps.
  it.each([
    ['a 128-byte id', 'é'.repeat(64), '0c19643c633748383f38cac2813dce632bf06c4fd527762520f225b6c7426b11'],
    ['an escaped id', 'a<b>&c\u2028d\u2029e', '921051622c7bfe91bb5d65813cf9999cf896877f84468bc2babde10ef030acec'],
    ['a byte order mark id', '\ufefftight', '780f21d939eed69e26d70659f91feb4285141221b5f788ff21787317dd0e51fa'],
  ])('admits a project policy receipt with %s', async (_, id, hash) => {
    const value = projectReceipt()
    Object.assign(value.policy, { policy_id: id, hash })
    expect((await telemetryPolicyView(value)).policy?.policy_id).toBe(id)
  })
  it('refuses a project policy receipt whose digest no longer matches', async () => {
    const value = projectReceipt()
    value.policy.definition_sha256 = 'cd'.repeat(32)
    expect((await telemetryPolicyView(value)).policy).toBeUndefined()
  })
})
