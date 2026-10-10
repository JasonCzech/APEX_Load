import { describe, expect, it } from 'vitest'
import type { RunArchiveNetworkPhaseSummary } from '../../types'
import { countText, ratioParts, ratioText, reasonLabel, usedHTTP3, versionMixHeadline, versionMixRows, versionMixView, versionPhaseRows } from './httpVersionMixModel'

describe('httpVersionMixModel', () => {
  const mix = {
    actions: 6, by_version: { '3': 1, '2': 2, '1.1': 1, unknown: 1, cache: 1 }, by_selection: { upgraded: 2, not_advertised: 1, exact_policy: 1 },
    http3_policies: 4, upgrade_eligible: 2, fell_back: 1, quic_attempted: 3, quic_failed: 2, attempts: 5,
    fallback_ratio: { numerator: 1, denominator: 2, value: 0.5 }, quic_failure_ratio: { numerator: 2, denominator: 3, value: 2 / 3 },
  }

  it('orders buckets and computes shares of every HTTP action', () => {
    expect(versionMixRows(mix).map(row => [row.label, row.count, row.sharePct.toFixed(1)])).toEqual([
      ['HTTP/1.1', 1, '16.7'], ['HTTP/2', 2, '33.3'], ['HTTP/3', 1, '16.7'], ['No response (version unknown)', 1, '16.7'], ['Served from cache', 1, '16.7'],
    ])
    expect(versionMixHeadline(mix)).toBe('HTTP/1.1 17% · HTTP/2 33% · HTTP/3 17% · no response 17% · cache 17%')
    expect(versionMixHeadline({ actions: 1000, by_version: { '2': 999, '3': 1 } })).toBe('HTTP/2 100% · HTTP/3 <1%')
  })

  it('publishes ratios with their denominators and says when there is none', () => {
    expect(ratioText(mix.fallback_ratio, 'upgrade-eligible auto+h3 actions')).toBe('50.00% (1 of 2 upgrade-eligible auto+h3 actions)')
    expect(ratioText({ numerator: 0, denominator: 0, value: 0 }, 'QUIC attempts')).toBe('n/a (no QUIC attempts)')
    expect(ratioText(undefined, 'QUIC attempts')).toBe('n/a (no QUIC attempts)')
    expect(ratioText({ numerator: 3, denominator: 209733, value: 3 / 209733 }, 'QUIC attempts')).toBe(`<0.01% (3 of ${(209733).toLocaleString()} QUIC attempts)`)
    expect(ratioParts({ numerator: 3, denominator: 209733, value: 3 / 209733 }, 'QUIC attempts')).toEqual({ value: '<0.01%', detail: `3 of ${(209733).toLocaleString()} QUIC attempts` })
  })

  it('labels routing tokens and never shows a raw unknown token', () => {
    expect(countText(mix.by_selection)).toBe('Upgraded to HTTP/3 (2), Exact HTTP/3 policy (1), TCP: HTTP/3 not yet advertised (1)')
    expect(reasonLabel('some_future_reason')).toBe('Some future reason')
    expect(reasonLabel('constructor')).toBe('Constructor')
  })

  it('detects HTTP/3 from policies, responses or QUIC attempts', () => {
    expect(usedHTTP3(mix)).toBe(true)
    expect(usedHTTP3({ actions: 3, by_version: { '2': 3 } })).toBe(false)
    expect(usedHTTP3({ actions: 1, by_version: { '3': 1 } })).toBe(true)
    expect(usedHTTP3(undefined)).toBe(false)
  })

  it('builds the view only for archives with a mix', () => {
    const summary = { version_mix: mix, by_http_version: { '3': { count: 1, avg_connect_ms: 12 }, '2': { count: 2 }, cache: { count: 0 } } } as unknown as RunArchiveNetworkPhaseSummary
    const view = versionMixView(summary)!
    expect(view.usedHTTP3).toBe(true)
    expect(view.quicFailure).toBe('66.67% (2 of 3 QUIC attempts)')
    expect(versionPhaseRows(summary).map(row => row.label)).toEqual(['HTTP/2', 'HTTP/3'])
    expect(versionMixView({ samples: 3 })).toBeNull()
    expect(versionMixView(undefined)).toBeNull()
    expect(versionPhaseRows(undefined)).toEqual([])
  })
})
