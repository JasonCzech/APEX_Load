/** Which values extracted earlier in a debug run a request uses. Only names are reported, never values. */
import type { BrowserReplayEvent } from '../../types/replay'
import { appliedReplayExtractions } from '../replayExtractionValues'

export interface UsedCorrelation {
  name: string
  /** Action that extracted the value. */
  from: string
  /** The request still contains the literal reference, so substitution failed. */
  unresolved?: boolean
}

const MIN_VALUE_LENGTH = 6
const COMMON_LITERALS = new Set(['true', 'false', 'null', 'undefined', 'success', 'failure', 'error', 'json'])
const TOKEN_CHAR = /[A-Za-z0-9]/

const escapeRegExp = (v: string) => v.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

/** Values too short or too common to identify one extraction. */
export function isDistinctiveValue(value: string): boolean {
  const v = value.trim()
  if (v.length < MIN_VALUE_LENGTH) return false
  if (COMMON_LITERALS.has(v.toLowerCase())) return false
  // Digit IDs of 6+ characters are kept: whole-token matching already rules out
  // a match inside a longer number.
  // Plain words ("application", "Enabled", "SUCCESS") match ordinary text; mixed-case
  // letter tokens ("kQzPwXyTrB") are real correlation values.
  if (/^[A-Za-z]+$/.test(v) && v.length < 16 && (v === v.toLowerCase() || v === v.toUpperCase() || /^[A-Z][a-z]+$/.test(v))) return false
  return true
}

/** The forms a value takes on the wire: raw, URL-encoded, form-encoded and JSON-escaped. */
export function wireVariants(value: string): string[] {
  const variants = new Set([value])
  try {
    const uri = encodeURIComponent(value)
    variants.add(uri)
    variants.add(uri.replace(/%20/g, '+'))
  } catch {
    // Lone surrogates cannot be URI-encoded; the raw form still applies.
  }
  variants.add(JSON.stringify(value).slice(1, -1))
  return [...variants]
}

/** True when needle occurs in haystack as a whole token (not inside a longer word or number). */
export function containsToken(haystack: string, needle: string): boolean {
  if (!needle) return false
  for (let at = haystack.indexOf(needle); at >= 0; at = haystack.indexOf(needle, at + 1)) {
    // An escape (%22, \n, \u0022) right before or after the value is a
    // delimiter in encoded payloads, not part of a longer token.
    const head = haystack.slice(Math.max(0, at - 6), at)
    const tail = haystack.slice(at + needle.length, at + needle.length + 6)
    const before = /%[0-9A-Fa-f]{2}$|\\[nrtbf"/\\]$|\\u[0-9A-Fa-f]{4}$/.test(head) ? '' : at > 0 ? haystack[at - 1] : ''
    const after = /^%[0-9A-Fa-f]{2}|^\\[nrtbfu"/\\]/.test(tail) ? '' : haystack[at + needle.length] ?? ''
    const startsWord = TOKEN_CHAR.test(needle[0])
    const endsWord = TOKEN_CHAR.test(needle[needle.length - 1])
    if ((!startsWord || !TOKEN_CHAR.test(before)) && (!endsWord || !TOKEN_CHAR.test(after))) return true
  }
  return false
}

export function requestHaystack(event: BrowserReplayEvent): string {
  const s = event.snapshot
  const headers = (s ? s.request_headers : event.headers) || {}
  return [s?.request_url || event.target, ...Object.values(headers), s ? s.request_body : event.body].filter(Boolean).join('\n')
}

export function usedCorrelations(event: BrowserReplayEvent, earlier: readonly BrowserReplayEvent[]): UsedCorrelation[] {
  const haystack = requestHaystack(event)
  if (!haystack) return []
  const found = new Map<string, UsedCorrelation>()
  for (const prev of earlier) {
    for (const [name, value] of Object.entries(appliedReplayExtractions(prev))) {
      if (typeof value !== 'string') continue
      if (new RegExp(`\\{\\{\\s*(?:var\\.)?${escapeRegExp(name)}\\s*\\}\\}`).test(haystack)) {
        found.set(name, { name, from: prev.action_name, unresolved: true })
      } else if (isDistinctiveValue(value) && wireVariants(value).some(v => containsToken(haystack, v))) {
        found.set(name, { name, from: prev.action_name })
      }
    }
  }
  return [...found.values()]
}
