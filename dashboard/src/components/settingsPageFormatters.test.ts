import { describe, expect, it } from 'vitest'
import {
  formatBytes,
  formatReadinessDetails,
  formatDurationSeconds,
  formatAuthMode,
  formatReadinessStatus,
  formatStatusCounts,
  normalizeProjectRedactionRules,
  normalizeDashboardAuthMode,
  parseAuthScopes,
  parseOptionalWholeNumber,
  sortProjects,
  validateAuthURL,
} from './settingsPageFormatters'
import type { Project } from '../types'

describe('settingsPageFormatters', () => {
  it('formats and preserves Local Test Login mode', () => {
    expect(normalizeDashboardAuthMode('local')).toBe('local')
    expect(formatAuthMode('local')).toBe('Local Test Login')
  })

  it('formatBytes scales to the largest unit', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(2048)).toBe('2.00 KB')
    expect(formatBytes(5 * 1024 * 1024)).toBe('5.00 MB')
    expect(formatBytes(3 * 1024 * 1024 * 1024)).toBe('3.00 GB')
    expect(formatBytes(undefined)).toBe('0 B')
  })

  it('formatDurationSeconds renders seconds, minutes, and hours', () => {
    expect(formatDurationSeconds(45)).toBe('45s')
    expect(formatDurationSeconds(120)).toBe('2m')
    expect(formatDurationSeconds(3 * 3600 + 25 * 60)).toBe('3h 25m')
  })

  it('formatReadinessStatus maps known states and passes through unknowns', () => {
    expect(formatReadinessStatus('ready')).toBe('Ready')
    expect(formatReadinessStatus('not_ready')).toBe('Not Ready')
    expect(formatReadinessStatus('mystery')).toBe('mystery')
    expect(formatReadinessStatus(undefined)).toBe('Unknown')
  })

  it('formatStatusCounts sorts and joins, with a none fallback', () => {
    expect(formatStatusCounts({ running: 2, queued: 1 })).toBe('queued=1, running=2')
    expect(formatStatusCounts({})).toBe('none')
    expect(formatStatusCounts(undefined)).toBe('none')
  })

  it('parseOptionalWholeNumber rejects non-integers and negatives', () => {
    expect(parseOptionalWholeNumber('')).toBeUndefined()
    expect(parseOptionalWholeNumber('  ')).toBeUndefined()
    expect(parseOptionalWholeNumber('7')).toBe(7)
    expect(parseOptionalWholeNumber('-3')).toBeUndefined()
    expect(parseOptionalWholeNumber('1.5')).toBeUndefined()
  })

  it('parseAuthScopes splits on whitespace and commas', () => {
    expect(parseAuthScopes('openid, profile  email')).toEqual(['openid', 'profile', 'email'])
    expect(parseAuthScopes('')).toEqual([])
  })

  it('validateAuthURL accepts http(s) and optionally absolute paths', () => {
    expect(validateAuthURL('Issuer', 'https://issuer.example', false)).toBeNull()
    expect(validateAuthURL('Issuer', '', false)).toBeNull()
    expect(validateAuthURL('Issuer', 'ftp://issuer.example', false)).not.toBeNull()
    expect(validateAuthURL('Redirect', '/callback', true)).toBeNull()
    expect(validateAuthURL('Redirect', '/callback', false)).not.toBeNull()
  })

  it('normalizeProjectRedactionRules drops blank rows and trims', () => {
    const out = normalizeProjectRedactionRules([
      { name: ' token ', pattern: ' secret ', replacement: ' *** ' },
      { name: '', pattern: '', replacement: '' },
    ])
    expect(out).toEqual([{ pattern: 'secret', name: 'token', replacement: '***' }])
  })

  it('sortProjects orders by name without mutating the input', () => {
    const input = [{ name: 'Beta' }, { name: 'alpha' }] as Project[]
    const sorted = sortProjects(input)
    expect(sorted.map(p => p.name)).toEqual(['alpha', 'Beta'])
    expect(input.map(p => p.name)).toEqual(['Beta', 'alpha'])
  })
})

it('formats nested readiness objects and arrays without object coercion', () => {
  expect(formatReadinessDetails({ integration: { enabled: false, checks: [{ status: 'not_configured' }] } })).toBe('integration: checks: status: not_configured | enabled: false')
})
