import type { StoredScript } from '../types'

const LOGICAL_SECRET_NAME = /^[A-Za-z0-9_.-]{1,249}$/
const SECRET_TEMPLATE = /\{\{\s*secret\.\s*([A-Za-z0-9_.-]{1,249})\s*\}\}/g
const DEDICATED_SECRET_FIELDS = new Set([
  'ca_cert_secret',
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

export function requiredScriptSecrets(script: StoredScript): string[] {
  const cached = scriptSecretReferenceCache.get(script)
  if (cached) return [...cached]
  if (!script.script) return []
  const names = new Set<string>()
  collectSecretNames(script.script, names, true)
  const required = Array.from(names).sort()
  scriptSecretReferenceCache.set(script, required)
  return [...required]
}

export function scriptContainsSecretReferences(script: StoredScript): boolean {
  return requiredScriptSecrets(script).length > 0
}
