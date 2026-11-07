import type { StoredScript } from '../types'
import type { ScriptDefinition } from '../types/scripts'
import { inactiveProxyCredentialNotes, proxyCredentialSecretRefs, withoutProxyCredentials, type ProxyCredentialKind, type ProxyCredentialSecretRef } from './proxyCredentialSecrets'

const LOGICAL_SECRET_NAME = /^[A-Za-z0-9_.-]{1,249}$/
const SECRET_TEMPLATE = /\{\{\s*secret\.\s*([A-Za-z0-9_.-]{1,249})\s*\}\}/g
// Fields whose plain value is a logical secret name, as the controller's
// dedicatedLogicalSecretFields (pkg/engine/secret_references.go) reads them.
const DEDICATED_SECRET_FIELDS = new Set([
  'ca_cert_secret',
  'client_secret_ref',
  'connection_string_secret',
  'dsn_secret',
  'sasl_password_secret',
])
const NON_EXECUTABLE_ROOT_FIELDS = new Set([
  'created_at',
  'folder_id',
  'generated_by',
  'generated_from',
  'id',
  'metadata',
  'name',
  'project_id',
  'protocol',
  'ref_key',
  'updated_at',
  'version',
])
const scriptSecretReferenceCache = new WeakMap<StoredScript, string[]>()

function canonicalSecretName(name: string): string {
  return name.trim().toUpperCase().replace(/[.-]/g, '_')
}

function reviewOnlyAction(value: Record<string, unknown>): boolean {
  if (!('name' in value) || !('target' in value)) return false
  const actionType = typeof value.type === 'string' ? value.type.trim().toLowerCase() : ''
  return actionType === 'source_review' || actionType === 'review_required'
}

function decodedBase64Text(value: string): string {
  try {
    return globalThis.atob(value.replace(/\s+/g, ''))
  } catch {
    return ''
  }
}

function collectSecretNames(
  value: unknown,
  names: Set<string>,
  root = false,
  dedicatedField = false,
  path = '',
): void {
  if (Array.isArray(value)) {
    value.forEach(child => collectSecretNames(child, names, false, dedicatedField, path))
    return
  }
  if (value !== null && typeof value === 'object') {
    const record = value as Record<string, unknown>
    if (reviewOnlyAction(record)) return
    if (typeof record.body === 'string' && String(record.body_encoding || '').trim().toLowerCase() === 'base64') {
      collectSecretNames(decodedBase64Text(record.body), names)
    }
    Object.entries(record).forEach(([key, child]) => {
      const normalizedKey = key.trim().toLowerCase()
      if (root && NON_EXECUTABLE_ROOT_FIELDS.has(normalizedKey)) return
      // The backend intentionally treats JVM arguments as opaque command-line
      // text, so templates there must not block script readiness either.
      if (path === '/config/java_vuser' && normalizedKey === 'additional_vm_parameters') return
      collectSecretNames(
        child,
        names,
        false,
        DEDICATED_SECRET_FIELDS.has(normalizedKey),
        `${path}/${normalizedKey}`,
      )
    })
    return
  }
  if (typeof value !== 'string') return

  for (const match of value.matchAll(SECRET_TEMPLATE)) {
    names.add(canonicalSecretName(match[1]))
  }
  const candidate = value.trim()
  if (dedicatedField && !candidate.includes('{{') && LOGICAL_SECRET_NAME.test(candidate)) {
    names.add(canonicalSecretName(candidate))
  }
}

export interface ScriptSecretReferenceSummary {
  name: string
  occurrence_count: number
  locations: string[]
}

function collectSecretReferenceSummaries(
  value: unknown,
  summaries: Map<string, { count: number; locations: Set<string> }>,
  root = false,
  dedicatedField = false,
  path = '',
): void {
  if (Array.isArray(value)) {
    value.forEach((child, index) => collectSecretReferenceSummaries(child, summaries, false, dedicatedField, `${path}/${index}`))
    return
  }
  if (value !== null && typeof value === 'object') {
    const record = value as Record<string, unknown>
    if (reviewOnlyAction(record)) return
    const encodedBody = typeof record.body === 'string' && String(record.body_encoding || '').trim().toLowerCase() === 'base64'
    if (encodedBody) {
      collectSecretReferenceSummaries(decodedBase64Text(record.body as string), summaries, false, false, `${path}/body`)
    }
    Object.entries(record).forEach(([key, child]) => {
      const normalizedKey = key.trim().toLowerCase()
      if (root && NON_EXECUTABLE_ROOT_FIELDS.has(normalizedKey)) return
      if (encodedBody && normalizedKey === 'body') return
      if (path === '/config/java_vuser' && normalizedKey === 'additional_vm_parameters') return
      collectSecretReferenceSummaries(
        child,
        summaries,
        false,
        DEDICATED_SECRET_FIELDS.has(normalizedKey),
        `${path}/${key.replace(/~/g, '~0').replace(/\//g, '~1')}`,
      )
    })
    return
  }
  if (typeof value !== 'string') return

  const recordOccurrence = (rawName: string) => {
    const name = canonicalSecretName(rawName)
    const summary = summaries.get(name) || { count: 0, locations: new Set<string>() }
    summary.count += 1
    summary.locations.add(path)
    summaries.set(name, summary)
  }
  for (const match of value.matchAll(SECRET_TEMPLATE)) recordOccurrence(match[1])
  const candidate = value.trim()
  if (dedicatedField && !candidate.includes('{{') && LOGICAL_SECRET_NAME.test(candidate)) recordOccurrence(candidate)
}

export function scriptSecretReferenceSummaries(script?: ScriptDefinition | null): ScriptSecretReferenceSummary[] {
  if (!script) return []
  const summaries = new Map<string, { count: number; locations: Set<string> }>()
  collectSecretReferenceSummaries(script, summaries, true)
  return Array.from(summaries, ([name, summary]) => ({
    name,
    occurrence_count: summary.count,
    locations: Array.from(summary.locations).sort(),
  })).sort((left, right) => left.name.localeCompare(right.name))
}

/**
 * The project secrets a script needs to run: every secret it references,
 * except one referenced only by a proxy credential that is not in use
 * (proxyCredentialSecrets), which stays a reference and never blocks.
 */
export function requiredScriptDefinitionSecrets(script: ScriptDefinition | null | undefined): string[] {
  if (!script) return []
  const names = new Set<string>()
  collectSecretNames(withoutProxyCredentials(script, 'inactive'), names, true)
  return Array.from(names).sort()
}

/**
 * The secrets referenced only by proxy credentials that are not in use: a
 * name also referenced anywhere else stays required.
 */
export function inactiveProxyOnlySecretNames(script: ScriptDefinition | null | undefined): Set<string> {
  if (!script) return new Set()
  const inactive = proxyCredentialSecretRefs(script.config).filter(ref => !ref.active)
  if (inactive.length === 0) return new Set()
  const required = new Set(requiredScriptDefinitionSecrets(script))
  return new Set(inactive.map(ref => ref.name).filter(name => !required.has(name)))
}

/**
 * The part of the secrets check detail that names the secrets referenced only
 * by proxy credentials that are not in use, matching the server's
 * ReplaySecretCheckDetail: it follows "No required project secrets." when
 * nothing is required, and is appended after the required/missing wording
 * otherwise ("" when there are none).
 */
export function inactiveProxySecretCheckDetail(script: ScriptDefinition | null | undefined): string {
  const onlyInactive = inactiveProxyOnlySecretNames(script)
  if (!script || onlyInactive.size === 0) return ''
  const names = (kind: ProxyCredentialKind) => Array.from(new Set(proxyCredentialSecretRefs(script.config)
    .filter(ref => !ref.active && ref.kind === kind && onlyInactive.has(ref.name))
    .map(ref => ref.name))).sort()
  const parts: string[] = []
  const httpNames = names('http')
  const browserNames = names('browser')
  if (httpNames.length > 0) parts.push(`Not needed for the current Proxy mode: ${httpNames.join(', ')}`)
  if (browserNames.length > 0) parts.push(`Not needed without a browser proxy server: ${browserNames.join(', ')}`)
  return parts.join('; ')
}

/**
 * The non-blocking notes for the secrets only not-in-use proxy credentials
 * reference (inactiveProxyCredentialNotes): a secret also used anywhere else
 * in the script is still needed, so it gets no "not needed" note.
 */
export function scriptInactiveProxyCredentialNotes(script: ScriptDefinition | null | undefined, kind?: ProxyCredentialKind): string[] {
  if (!script) return []
  return inactiveProxyCredentialNotes(script.config, inactiveProxyOnlySecretNames(script), kind)
}

/**
 * names without the secrets referenced only by proxy credentials that are not
 * in use, for a required list an older controller computed without the rule.
 */
export function withoutInactiveProxySecretNames(names: readonly string[], script: ScriptDefinition | null | undefined): string[] {
  const inactive = inactiveProxyOnlySecretNames(script)
  if (inactive.size === 0) return [...names]
  return names.filter(name => !inactive.has(canonicalSecretName(name)))
}

/**
 * The in-use proxy credential references of the secrets nothing else in the
 * script references, by name: an unbound one is the proxy's problem, which
 * changing the proxy settings also clears.
 */
export function activeProxyOnlySecretRefs(script: ScriptDefinition | null | undefined): Map<string, ProxyCredentialSecretRef[]> {
  const byName = new Map<string, ProxyCredentialSecretRef[]>()
  if (!script) return byName
  const active = proxyCredentialSecretRefs(script.config).filter(ref => ref.active)
  if (active.length === 0) return byName
  const elsewhere = new Set<string>()
  collectSecretNames(withoutProxyCredentials(script, 'all'), elsewhere, true)
  for (const ref of active) {
    if (elsewhere.has(ref.name)) continue
    byName.set(ref.name, [...(byName.get(ref.name) || []), ref])
  }
  return byName
}

export function requiredScriptSecrets(script: StoredScript): string[] {
  const cached = scriptSecretReferenceCache.get(script)
  if (cached) return [...cached]
  if (!script.script) return []
  const required = requiredScriptDefinitionSecrets(script.script)
  scriptSecretReferenceCache.set(script, required)
  return [...required]
}

// Every reference counts here, a not-in-use proxy credential's too: the
// script's secrets stay bindable while nothing requires them.
export function scriptContainsSecretReferences(script: StoredScript): boolean {
  if (requiredScriptSecrets(script).length > 0) return true
  return proxyCredentialSecretRefs(script.script?.config).length > 0
}
