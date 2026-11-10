import { describe, expect, it } from 'vitest'
import type { BrowserReplayEvent } from '../../types/replay'
import { containsToken, isDistinctiveValue, usedCorrelations, wireVariants } from './correlation'

const ev = (o: Partial<BrowserReplayEvent>): BrowserReplayEvent => ({ phase: 'action', action_name: 'Step', action_type: 'request', target: '', success: true, started_at: '', completed_at: '', duration_ms: 1, ...o })

describe('debug correlation matching', () => {
  it('finds values sent URL-, form- or JSON-encoded', () => {
    const token = 'abc+def/ghi=='
    const login = ev({ action_name: 'Login', extractions: { aura_token: token, csrf: 'x"y\\z-123456' } })
    const query = ev({ snapshot: { request_url: `https://sf.test/aura?aura.token=${encodeURIComponent(token)}`, request_headers: {} } })
    const json = ev({ snapshot: { request_url: 'https://sf.test/x', request_headers: { 'Content-Type': 'application/json' }, request_body: JSON.stringify({ csrf: 'x"y\\z-123456' }) } })
    expect(usedCorrelations(query, [login])).toEqual([{ name: 'aura_token', from: 'Login' }])
    expect(usedCorrelations(json, [login])).toEqual([{ name: 'csrf', from: 'Login' }])
    expect(wireVariants('a b')).toEqual(expect.arrayContaining(['a b', 'a%20b', 'a+b']))
  })

  it('ignores short, common or partial matches', () => {
    expect(isDistinctiveValue('true')).toBe(false)
    expect(isDistinctiveValue('SUCCESS')).toBe(false)
    expect(isDistinctiveValue('2026')).toBe(false)
    expect(isDistinctiveValue('12345')).toBe(false)
    expect(isDistinctiveValue('1234567')).toBe(true)
    expect(isDistinctiveValue('kQzPwXyTrB')).toBe(true)
    expect(isDistinctiveValue('Enabled')).toBe(false)
    expect(isDistinctiveValue('12345678')).toBe(true)
    expect(isDistinctiveValue('application')).toBe(false)
    expect(containsToken('id=1001234', '100123')).toBe(false)
    expect(containsToken('id=100123&x', '100123')).toBe(true)
    const prev = ev({ action_name: 'Status', extractions: { flag: 'true', kind: 'application', order: '100123' } })
    const req = ev({ snapshot: { request_url: 'https://sf.test/orders/1001234', request_headers: { 'Content-Type': 'application/json' }, request_body: '{"pay":true}' } })
    expect(usedCorrelations(req, [prev])).toEqual([])
  })

  it('flags references that were never substituted', () => {
    const prev = ev({ action_name: 'Login', extractions: { auth_token: 'tok-abc123xyz' } })
    const req = ev({ snapshot: { request_url: 'https://sf.test/x', request_headers: { 'X-Session': '{{var.auth_token}}' } } })
    expect(usedCorrelations(req, [prev])).toEqual([{ name: 'auth_token', from: 'Login', unresolved: true }])
  })
})

describe('encoded neighbours', () => {
  it('matches values right after or before percent and JSON escapes', () => {
    expect(containsToken('message=%7B%22recordId%22%3A%22001xx000003DGb2AAG%22%7D', '001xx000003DGb2AAG')).toBe(true)
    expect(containsToken('state%3DkQ9zPwXy12', 'kQ9zPwXy12')).toBe(true)
    expect(containsToken('{"a":"x\\nkQ9zPwXy12\\n"}', 'kQ9zPwXy12')).toBe(true)
    expect(containsToken('AkQ9zPwXy12', 'kQ9zPwXy12')).toBe(false)
  })
})
