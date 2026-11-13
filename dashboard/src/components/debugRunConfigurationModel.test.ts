import { describe, expect, it } from 'vitest'

import type { ScriptConfig } from '../types/scripts'
import {
  debugHttpRunConfiguration,
  debugPacingLabel,
  debugRetriesLabel,
  debugRunTarget,
  debugSessionBaseURL,
  debugSessionOverridesLabel,
  debugThinkTimeLabel,
} from './debugRunConfigurationModel'
import { setSessionOverride } from './debugSessionOverrides'

const ENGINE: ScriptConfig = {
  base_url: '',
  http: { request_timeout_ms: 60000, connect_timeout_ms: 30000, tls_timeout_ms: 10000, max_conns_per_host: 100, max_redirects: 10, keep_alive: true, cookies_enabled: true, tls: { min_version: '1.2' } },
}

describe('debug run configuration', () => {
  it('shows the engine defaults for unset HTTP fields, not stale literals', () => {
    const http = debugHttpRunConfiguration({ script: { base_url: '' }, engine: ENGINE })
    expect(http.timeouts).toBe('Request 60000 ms · Connect 30000 ms · TLS 10000 ms')
    expect(http.connections).toBe('Keep-alive on · Max redirects 10 · Max conns/host 100')
    expect(http.tls).toBe('Min 1.2')
  })

  it('falls back to the engine contract values while the defaults are unavailable', () => {
    const http = debugHttpRunConfiguration({ script: { base_url: '' } })
    expect(http.timeouts).toBe('Request 60000 ms · Connect 30000 ms · TLS 10000 ms')
    expect(http.connections).toContain('Max conns/host 100')
  })

  it('takes the engine endpoint values over the fallback', () => {
    const http = debugHttpRunConfiguration({ script: { base_url: '' }, engine: { base_url: '', http: { request_timeout_ms: 61000, connect_timeout_ms: 31000 } } })
    expect(http.timeouts).toBe('Request 61000 ms · Connect 31000 ms · TLS 10000 ms')
  })

  it('marks values a session override supplies and keeps script values', () => {
    const session = setSessionOverride(setSessionOverride(undefined, 'http.request_timeout_ms', 5000), 'http.keep_alive', false)
    const http = debugHttpRunConfiguration({ session, script: { base_url: '', http: { connect_timeout_ms: 2500 } }, engine: ENGINE })
    expect(http.timeouts).toBe('Request 5000 ms (session override) · Connect 2500 ms · TLS 10000 ms')
    expect(http.connections).toContain('Keep-alive off (session override)')
  })

  it('shows the engine default, not 0 ms, for a zero timeout or max conns/host', () => {
    let session = setSessionOverride(undefined, 'http.request_timeout_ms', 0)
    session = setSessionOverride(session, 'http.max_conns_per_host', 0)
    const http = debugHttpRunConfiguration({ session, script: { base_url: '', http: { connect_timeout_ms: 0 } }, engine: ENGINE })
    expect(http.timeouts).toBe('Request 60000 ms (0 = engine default) (session override) · Connect 30000 ms (0 = engine default) · TLS 10000 ms')
    expect(http.connections).toContain('Max conns/host 100 (0 = engine default) (session override)')
  })

  it('never shows proxy credentials', () => {
    const http = debugHttpRunConfiguration({ script: { base_url: '', http: { proxy_mode: 'custom', proxy: { url: 'http://user:pass@proxy.test:8080' } } }, engine: ENGINE })
    expect(http.proxy).toBe('custom (http://***@proxy.test:8080)')
  })

  it('labels pacing as skipped in a single-VUser debug replay', () => {
    expect(debugPacingLabel({ base_url: '' })).toBe('Skipped in single-VUser debug replay')
    expect(debugPacingLabel({ base_url: '', pacing: { type: 'fixed_delay', delay_ms: 2000 } })).toMatch(/^Skipped in single-VUser debug replay \(script: .+\)$/)
  })

  it('shows the session base URL, retries and the override list', () => {
    const session = setSessionOverride(setSessionOverride(undefined, 'base_url', 'https://staging.test'), 'error_handling.max_retries', 2)
    expect(debugSessionBaseURL({ session, script: { base_url: 'https://prod.test' } })).toBe('https://staging.test (session override)')
    expect(debugSessionBaseURL({ script: { base_url: 'https://prod.test' } })).toBeUndefined()
    expect(debugRetriesLabel({ session, script: { base_url: '', error_handling: { retry_delay_ms: 100 } } })).toBe('2 (100 ms, fixed) (session override)')
    expect(debugRetriesLabel({ script: { base_url: '' } })).toBe('0')
    expect(debugSessionOverridesLabel(session)).toBe('2 settings: base_url, error_handling.max_retries')
    expect(debugSessionOverridesLabel(undefined)).toBe('None')
  })

  it('lays session think-time overrides over the script think time', () => {
    const session = setSessionOverride(setSessionOverride(undefined, 'think_time.type', 'fixed'), 'think_time.mean_ms', 250)
    const label = debugThinkTimeLabel({ session, script: { base_url: '', think_time: { type: 'none' } } }, false)
    expect(label).toContain('(session override)')
    expect(label).toContain('0.25 seconds')
    expect(debugThinkTimeLabel({ session, script: { base_url: '' } }, true)).toMatch(/^Ignored for debug \(/)
  })

  it('records the session base URL override as the run target, without credentials', () => {
    const script: ScriptConfig = { base_url: 'https://api.example.com' }
    expect(debugRunTarget(undefined, script)).toBe('https://api.example.com')
    expect(debugRunTarget(undefined, { base_url: '' })).toBe('Action-specific targets')
    expect(debugRunTarget(setSessionOverride(undefined, 'base_url', 'https://staging.example.test'), script)).toBe('https://staging.example.test')
    const withCredentials = debugRunTarget(setSessionOverride(undefined, 'base_url', 'https://user:hunter2@staging.example.test'), { base_url: '' })
    expect(withCredentials).toContain('staging.example.test')
    expect(withCredentials).not.toContain('hunter2')
    // An override of some other field keeps the script's base URL.
    expect(debugRunTarget(setSessionOverride(undefined, 'log.level', 'debug'), script)).toBe('https://api.example.com')
    // The script's own base URL is recorded without credentials too.
    const scriptCredentials = debugRunTarget(undefined, { base_url: 'https://user:secret@api.example.test' })
    expect(scriptCredentials).toContain('api.example.test')
    expect(scriptCredentials).not.toContain('secret')
    expect(scriptCredentials).not.toContain('user:')
  })
})
