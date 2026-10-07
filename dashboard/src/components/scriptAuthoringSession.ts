// Binding rules for the script authoring session. A validation result
// describes exactly one resolved document (its fingerprint), the baseline
// revision that document was edited from, and the context the result depended
// on (project, folder and replay variables). It is current only while all
// three still match; any edit, save, scope change or context change makes it
// stale. A response is admitted only for the latest request, only while the
// document it was issued for is still the current one, and only when the
// controller echoes the fingerprint (or, for a stored script, the revision) it
// actually validated.

export interface ValidationBinding {
  fingerprint: string
  contentRevision: string
  context: string
}

export function sameValidationBinding(a: ValidationBinding | null | undefined, b: ValidationBinding | null | undefined): boolean {
  return Boolean(a && b) && a!.fingerprint === b!.fingerprint && a!.contentRevision === b!.contentRevision && a!.context === b!.context
}

// validationContextKey is everything besides the document and its baseline
// that a validation result depends on: the project and folder it was issued
// in and the replay variables it was seeded with.
export function validationContextKey(projectId: string | undefined, folderId: string | null | undefined, variables: Record<string, string> | null | undefined): string {
  return JSON.stringify([projectId ?? null, folderId ?? null, variables ?? null])
}

// draftFingerprint is a compact deterministic digest of a resolved document's
// canonical JSON text (two 32-bit lanes plus the length). It identifies a
// document for binding, not for security; the controller echoes it verbatim.
export function draftFingerprint(documentKey: string): string {
  let h1 = 0xdeadbeef
  let h2 = 0x41c6ce57
  for (let i = 0; i < documentKey.length; i++) {
    const code = documentKey.charCodeAt(i)
    h1 = Math.imul(h1 ^ code, 2654435761)
    h2 = Math.imul(h2 ^ code, 1597334677)
  }
  h1 = Math.imul(h1 ^ (h1 >>> 16), 2246822507) ^ Math.imul(h2 ^ (h2 >>> 13), 3266489909)
  h2 = Math.imul(h2 ^ (h2 >>> 16), 2246822507) ^ Math.imul(h1 ^ (h1 >>> 13), 3266489909)
  const hex = (value: number) => (value >>> 0).toString(16).padStart(8, '0')
  return `sdf1-${hex(h2)}${hex(h1)}-${documentKey.length.toString(16)}`
}

export interface ValidationRequest {
  requestId: number
  sent: ValidationBinding
  // issuedAgainst is the binding rendered when the request was issued.
  // Resolving the draft may flush pending cells, so until the flushed document
  // renders, the editor still shows this binding; nothing has changed since.
  issuedAgainst: ValidationBinding | null
  // stored is true when the controller validates the stored script rather
  // than the submitted draft.
  stored: boolean
}

export interface ValidationEcho {
  validated_fingerprint?: string
  validated_content_revision?: string
}

export type ValidationAdmission = 'current' | 'superseded' | 'unbound'

// validationRequestStillCurrent: the latest request, and the editor still
// holds the document it was issued for (flushed or not yet re-rendered).
export function validationRequestStillCurrent(latestRequestId: number, request: ValidationRequest, current: ValidationBinding | null): boolean {
  return request.requestId === latestRequestId
    && (sameValidationBinding(request.sent, current) || sameValidationBinding(request.issuedAgainst, current))
}

export function admitValidationResponse(latestRequestId: number, request: ValidationRequest, echo: ValidationEcho | null | undefined, current: ValidationBinding | null): ValidationAdmission {
  if (!validationRequestStillCurrent(latestRequestId, request, current)) return 'superseded'
  const bound = request.stored
    ? Boolean(request.sent.contentRevision) && echo?.validated_content_revision === request.sent.contentRevision
    : echo?.validated_fingerprint === request.sent.fingerprint
  return bound ? 'current' : 'unbound'
}
