export interface ProtectedImportCandidate {
  import_session_id?: string
  expires_at?: string
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
  if (expiry <= now) return 'The protected server candidate has expired; saving will use the validated compatibility path.'
  return `Protected server candidate available until ${new Date(expiry).toLocaleString()}.`
}
