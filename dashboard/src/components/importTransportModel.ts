import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'

/**
 * Transport panel model: one row per request origin of an HTTP-family import
 * draft, from the same request surfaces and origin rules the server applies
 * (pkg/convert/requestimport/transport_edit.go).
 */

export type ObservedVersion = 'h1' | 'h2' | 'h3' | 'draft' | 'unknown'

export type TransportRequest = {
  path: string
  name: string
  /** Leaf policy; '' when the request inherits the script setting. */
  policy: string
  observed: ObservedVersion
  /** Gates an explicit version choice resolves. */
  versionGates: string[]
  /** A gated source's approximate policy, such as auto+h3 for curl --http3. */
  proposal: string
  edited: boolean
}

export type TransportRow = {
  /** scheme://host[:port]; '' when the origin is set at runtime. */
  origin: string
  requests: TransportRequest[]
  counts: Record<ObservedVersion, number>
  advertised: boolean
  /** The shared leaf policy, or null when the requests differ. */
  policy: string | null
  fidelity: string
}

export type TransportEdit = { path: string; origin: string; from: string; to: string }

export const TRANSPORT_POLICY_OPTIONS: ReadonlyArray<{ value: string; label: string }> = [
  { value: '', label: 'Inherit the script setting' },
  { value: 'auto', label: 'Auto-negotiate (HTTP/2 or HTTP/1.1)' },
  { value: '1.1', label: 'Require HTTP/1.1' },
  { value: '2', label: 'Require HTTP/2 (no fallback)' },
  { value: 'auto+h3', label: 'Upgrade to HTTP/3 when advertised' },
  { value: '3', label: 'Require HTTP/3 (no fallback)' },
]

const VERSION_GATES = new Set(['curl_http3_race_not_modeled', 'http3_draft_token'])
const HTTP_METHODS = new Set(['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS', 'TRACE', 'CONNECT'])
const record = (value: unknown): Record<string, unknown> | null => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
const strings = (value: unknown): string[] => Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : []

export function policyLabel(policy: string): string {
  return TRANSPORT_POLICY_OPTIONS.find(option => option.value === policy)?.label ?? policy
}

/**
 * scheme://host[:port] of a target resolved against the base URL, or ''.
 * Mirrors requestimport.RequestOrigin and the runtime's ResolveURL: a target
 * is absolute only with a literal http:// or https:// prefix, and anything
 * else is appended to the base URL.
 */
export function requestOrigin(baseURL: string, target: string): string {
  const lowered = target.trim().toLowerCase()
  if (lowered.startsWith('{{')) return ''
  const scheme = /^([^/?#]*):\/\//.exec(lowered)?.[1]
  if (scheme !== undefined && scheme !== 'http' && scheme !== 'https') return ''
  const normalized = target.trim().replace(/^(https?:\/\/[^/?#\s]+)\s+([/?#].*)$/, '$1$2')
  let resolved = normalized
  if (!normalized.startsWith('http://') && !normalized.startsWith('https://')) {
    const base = baseURL.trim()
    if (!base) return ''
    resolved = `${base.replace(/\/+$/, '')}/${normalized.replace(/^\{\{\s*(?:var|param)\.[^{}]+\s*\}\}\s*/, '').replace(/^\/+/, '')}`
  }
  const match = /^([A-Za-z][A-Za-z0-9+.-]*):\/\/([^/?#]*)/.exec(resolved)
  if (!match) return ''
  const resolvedScheme = match[1].toLowerCase()
  if (resolvedScheme !== 'http' && resolvedScheme !== 'https') return ''
  let host = match[2].replace(/^.*@/, '').toLowerCase()
  if (!host || host.includes('{{')) return ''
  if ((resolvedScheme === 'https' && host.endsWith(':443')) || (resolvedScheme === 'http' && host.endsWith(':80'))) host = host.slice(0, host.lastIndexOf(':'))
  return `${resolvedScheme}://${host}`
}

function isHTTPRequest(action: ScriptActionDefinition, nested: boolean): boolean {
  if (nested) return true
  const type = String(action.type || '').trim().toLowerCase()
  if (type === 'http_sequence' || type.startsWith('control_')) return false
  if (!String(action.target || '').trim()) return false
  if (String(action.method || '').trim()) return true
  return ['request', 'http_request', 'soap_request', 'java_http_request'].includes(type) || HTTP_METHODS.has(type.toUpperCase())
}

function observedVersion(metadata: Record<string, unknown>): ObservedVersion {
  const capture = record(metadata.http_version_capture)
  if (capture?.request_state === 'draft') return 'draft'
  switch (capture?.request_version) {
    case '1.1': return 'h1'
    case '2': return 'h2'
    case '3': return 'h3'
  }
  if (capture) return 'unknown'
  // Text sources declare their version without a capture record.
  switch (String(metadata.http_version || '').trim().toUpperCase()) {
    case 'HTTP/1.1': case '1.1': return 'h1'
    case 'HTTP/2': case 'HTTP/2.0': return 'h2'
  }
  return 'unknown'
}

function transportRequest(action: ScriptActionDefinition, path: string): TransportRequest {
  const metadata = record(action.metadata) ?? {}
  const gates = [...strings(metadata.execution_blockers), ...strings(metadata.review_reasons)].filter(code => VERSION_GATES.has(code))
  const proposal = record(metadata.http_version_rewrite_proposal)
  return {
    path,
    name: String(action.name || '').trim() || path,
    policy: typeof action.http_version === 'string' ? action.http_version : '',
    observed: observedVersion(metadata),
    versionGates: [...new Set(gates)].sort(),
    proposal: typeof proposal?.policy === 'string' ? proposal.policy : '',
    edited: record(metadata.http_version_transport_edit) !== null,
  }
}

type Surface = { action: ScriptActionDefinition; path: string; nested: boolean }

/** HTTP request surfaces with their DSL paths, in the server walk's order (pkg/dslwalk). */
export function transportSurfaces(script: ScriptDefinition): Surface[] {
  const surfaces: Surface[] = []
  const walk = (actions: ScriptActionDefinition[] | undefined, prefix: string, depth: number) => {
    if (!Array.isArray(actions) || depth > 64) return
    actions.forEach((action, index) => {
      if (!action || typeof action !== 'object') return
      const path = `${prefix}[${index}]`
      surfaces.push({ action, path, nested: false })
      const params = record(action.params)
      if (Array.isArray(params?.requests)) {
        params.requests.forEach((child, childIndex) => {
          if (record(child)) surfaces.push({ action: child as ScriptActionDefinition, path: `${path}.params.requests[${childIndex}]`, nested: true })
        })
      }
      if (record(params?.request)) surfaces.push({ action: params!.request as ScriptActionDefinition, path: `${path}.params.request`, nested: true })
      const control = action.control
      if (!control) return
      walk(control.steps, `${path}.control.steps`, depth + 1)
      walk(control.else, `${path}.control.else`, depth + 1)
      control.cases?.forEach((branch, caseIndex) => walk(branch.steps, `${path}.control.cases[${caseIndex}].steps`, depth + 1))
      walk(control.default, `${path}.control.default`, depth + 1)
      control.branches?.forEach((branch, branchIndex) => walk(branch.steps, `${path}.control.branches[${branchIndex}].steps`, depth + 1))
    })
  }
  for (const phase of ['init', 'actions', 'teardown'] as const) walk(script[phase], phase, 0)
  return surfaces.filter(surface => isHTTPRequest(surface.action, surface.nested))
}

function rowFidelity(row: Omit<TransportRow, 'fidelity'>): string {
  const gated = row.requests.filter(request => request.versionGates.length > 0).length
  if (gated > 0) return gated === 1 ? '1 request needs an explicit HTTP version' : `${gated} requests need an explicit HTTP version`
  if (row.requests.some(request => request.edited)) return 'Chosen in import review'
  if (row.counts.h3 > 0) {
    if (row.policy === 'auto+h3') return 'Captured HTTP/3; upgrades when the origin advertises it'
    if (row.policy === '3') return 'Captured HTTP/3; requires HTTP/3'
    if (row.policy === '') return 'Captured HTTP/3 not kept; inherits the script setting'
  }
  if (row.counts.h1 + row.counts.h2 > 0 && row.policy !== null && row.policy !== '') return 'Captured version kept exactly'
  if (row.counts.h1 + row.counts.h2 + row.counts.h3 + row.counts.draft === 0) return 'No captured version'
  return row.policy === null ? 'Mixed policies' : 'Inherits the script setting'
}

/** One row per origin, sorted by origin; runtime-set origins last. */
export function transportRows(script: ScriptDefinition, advertisedOrigins: ReadonlySet<string> = new Set()): TransportRow[] {
  const baseURL = script.config?.base_url || ''
  const byOrigin = new Map<string, TransportRequest[]>()
  for (const surface of transportSurfaces(script)) {
    const origin = requestOrigin(baseURL, String(surface.action.target || ''))
    byOrigin.set(origin, [...(byOrigin.get(origin) ?? []), transportRequest(surface.action, surface.path)])
  }
  return [...byOrigin.entries()]
    .sort(([a], [b]) => (a === '' ? 1 : b === '' ? -1 : a.localeCompare(b)))
    .map(([origin, requests]) => {
      const counts: Record<ObservedVersion, number> = { h1: 0, h2: 0, h3: 0, draft: 0, unknown: 0 }
      for (const request of requests) counts[request.observed]++
      const policies = new Set(requests.map(request => request.policy))
      const row = { origin, requests, counts, advertised: advertisedOrigins.has(origin), policy: policies.size === 1 ? [...policies][0] : null }
      return { ...row, fidelity: rowFidelity(row) }
    })
}

/** The edits that give every request of a row the policy `to`. */
export function transportEdits(row: TransportRow, to: string): TransportEdit[] {
  if (!row.origin) return []
  return row.requests.filter(request => request.policy !== to).map(request => ({ path: request.path, origin: row.origin, from: request.policy, to }))
}

/** Origins the import reported as advertising HTTP/3 (origin_advertises_h3 findings). */
export function advertisedOriginsFrom(diagnostics: ReadonlyArray<{ code?: string; location?: string }> | undefined): Set<string> {
  return new Set((diagnostics ?? []).filter(diagnostic => diagnostic.code === 'origin_advertises_h3' && diagnostic.location).map(diagnostic => diagnostic.location!.toLowerCase()))
}

export function observedSummary(counts: Record<ObservedVersion, number>): string {
  const parts = (['h1', 'h2', 'h3', 'draft'] as const).filter(version => counts[version] > 0).map(version => `${version} ${counts[version]}`)
  return parts.length ? parts.join(' · ') : 'Not captured'
}
