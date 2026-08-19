import { type SecretBindingRequirement } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'

export interface ProtectedImportCandidate {
  import_session_id?: string
  expires_at?: string
}

export interface CredentialPreservingImportCandidate {
  import_draft_expires_at?: string
}

export function credentialPreservingImportDraftRequiresSave(
  candidate: CredentialPreservingImportCandidate | null | undefined,
  script: ScriptDefinition | null | undefined,
  requirements: readonly SecretBindingRequirement[] = [],
): boolean {
  if (candidate?.import_draft_expires_at?.trim()) return true
  if (requirements.some(requirement => requirement.destination_value_preserved === true)) return true
  const token = script?.metadata?.import?.preserved?.server_import_attestation_v1
  return typeof token === 'string' && token.trim().length > 0
}

export function activeProtectedImportSessionId(
  candidate: ProtectedImportCandidate | null | undefined,
  now = Date.now(),
): string | undefined {
  const id = candidate?.import_session_id?.trim()
  if (!id) return undefined
  const rawExpiry = candidate?.expires_at?.trim()
  if (!rawExpiry) return id
  const expiry = Date.parse(rawExpiry)
  if (!Number.isFinite(expiry)) return id
  return expiry > now ? id : undefined
}

export function invalidateProtectedImportCandidate<T extends ProtectedImportCandidate>(candidate: T): T {
  if (!candidate.import_session_id && !candidate.expires_at) return candidate
  const next = { ...candidate }
  delete next.import_session_id
  delete next.expires_at
  return next
}

/**
 * Protected commits replay parameterization against the immutable server-side
 * candidate. The browser preview, however, is edited cumulatively. Retain every
 * previously applied suggestion so those two representations stay equivalent
 * across multiple review passes.
 */
export function mergeProtectedParameterizationSelectionIds(
  existing: readonly string[],
  added: readonly string[],
): string[] {
  return Array.from(new Set(
    [...existing, ...added]
      .map(id => id.trim())
      .filter(Boolean),
  )).sort()
}

export function protectedImportExpiryLabel(raw: string | undefined, now = Date.now()): string {
  if (!raw?.trim()) return ''
  const expiry = Date.parse(raw)
  if (!Number.isFinite(expiry)) return ''
  if (expiry <= now) return 'The protected server candidate has expired.'
  return `Protected server candidate available until ${new Date(expiry).toLocaleString()}.`
}

export interface ImportDraftExpiryStatus {
  expired: boolean
  label: string
}

export function earliestImportDraftExpiry(values: readonly (string | null)[] | undefined): string | undefined {
	return values
		?.filter((value): value is string => !!value && Number.isFinite(Date.parse(value)))
		.sort((left, right) => Date.parse(left) - Date.parse(right))[0]
}

export function importDraftExpiryStatus(
  raw: string | undefined,
  now = Date.now(),
): ImportDraftExpiryStatus | null {
  if (!raw?.trim()) return null
  const expiry = Date.parse(raw)
  if (!Number.isFinite(expiry)) return null
  if (expiry <= now) {
    return {
      expired: true,
      label: 'This credential-preserving import draft has expired. Re-run the import before saving or editing it.',
    }
  }
  return {
    expired: false,
    label: `Credential-preserving import draft available until ${new Date(expiry).toLocaleString()}.`,
  }
}
