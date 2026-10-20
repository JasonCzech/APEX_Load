import { expect, it } from 'vitest'
import { javaHTTPActionsFromArchive, javaHTTPBase64WorkShare, javaHTTPMeanBytes, javaHTTPPairedRatio, javaHTTPSummaryLines } from './javaHttpDiagnostics'
import { javaHTTPFixture } from './javaHttpTestFixtures'

it('keeps observed zero separate from absence, overflow and empty ratios', () => {
  const s = javaHTTPFixture()
  expect(javaHTTPMeanBytes(s.response_retained)).toBe(0)
  expect(javaHTTPMeanBytes({ ...s.response_retained, count: 0 })).toBeUndefined()
  expect(javaHTTPMeanBytes({ ...s.request_decoded, overflow: true })).toBeUndefined()
  expect(javaHTTPPairedRatio(s.decoded_per_encoded)).toBe(0.5)
  expect(javaHTTPPairedRatio(s.base64_per_retained)).toBeUndefined()
  expect(javaHTTPPairedRatio({ ...s.decoded_per_encoded, denominator_bytes: NaN })).toBeUndefined()
  expect(javaHTTPBase64WorkShare(s)).toBe(0)
  expect(javaHTTPBase64WorkShare({ ...s, base64_work: { samples: 0, base64_ms: 0, enclosing_ms: 0 } })).toBeUndefined()
})
it('uses paired sums rather than ratios of different marginal populations', () => {
  const s = javaHTTPFixture(); s.request_encoded.sum_bytes = 100; s.request_encoded.count = 2
  expect(javaHTTPPairedRatio(s.decoded_per_encoded)).toBe(0.5)
  s.base64_work = { samples: 2, base64_ms: 3, enclosing_ms: 10 }; s.samples = 3
  expect(javaHTTPBase64WorkShare(s)).toBe(0.3)
})
it('labels counts, coverage and unavailable object footprint without legacy field inference', () => {
  const s = javaHTTPFixture(), lines = javaHTTPSummaryLines(s, 2).join('\n')
  expect(lines).toContain('Observed diagnostics: 1 of 2 action executions')
  expect(lines).toContain('Coverage incomplete')
  expect(lines).toContain('Retained response: 0 bytes mean')
  expect(lines).toContain('Base64 / retained response: Unavailable')
  expect(lines).toContain('Base64 work / enclosing time: 0 %')
  expect(javaHTTPSummaryLines({ ...s, version: 2 }, 1)).toEqual(['Diagnostics unavailable: unsupported or inconsistent evidence.'])
  expect(javaHTTPSummaryLines(s, 0)).toEqual(['Diagnostics unavailable: unsupported or inconsistent evidence.'])
})

it('retains archive action populations and keeps old archives absent', () => {
  expect(javaHTTPActionsFromArchive({ legacy: { transactions: 10 } })).toBeUndefined()
  const summary = javaHTTPFixture()
  expect(javaHTTPActionsFromArchive({ request: { transactions: 3, actual_action_samples: 2, java_http: summary }, legacy: { transactions: 10 } })?.actions).toEqual({ request: { population: 2, diagnostics: summary } })
})

it('does not infer an absent actual-action population from mixed transaction totals', () => {
  const summary = javaHTTPFixture()
  expect(javaHTTPActionsFromArchive({ request: { transactions: 9, java_http: summary } })).toEqual({ version: 1, incomplete: true, unavailable_reason: 'malformed' })
})
