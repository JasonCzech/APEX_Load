import { isValidSecretBindingName } from './secretBindingUtils'

export interface SourceSecretSessionFailureMetadata {
  consumed: boolean
  retained: boolean
  manualConfigurationRequired: boolean
  partial: boolean
  committedNames: string[]
  remainingNames: string[]
}

function errorPayload(error: unknown): Record<string, unknown> | null {
  if (!error || typeof error !== 'object' || !('payload' in error)) return null
  const payload = (error as { payload?: unknown }).payload
  return payload && typeof payload === 'object' ? payload as Record<string, unknown> : null
}

/**
 * Extracts only governed, non-sensitive lifecycle metadata from a failed
 * source-secret operation. Values and provider-returned metadata are never
 * retained by the browser.
 */
export function getSourceSecretSessionFailureMetadata(error: unknown): SourceSecretSessionFailureMetadata {
  const payload = errorPayload(error)
  const safeNames = (value: unknown) => Array.isArray(value)
    ? Array.from(new Set(value
      .filter((name): name is string => typeof name === 'string')
      .map(name => name.trim())
      .filter(isValidSecretBindingName)))
    : []
  const partial = payload?.partial === true
  const consumed = payload?.source_secret_session_consumed === true || payload?.import_session_consumed === true
  const retained = payload?.session_retained === true
    || payload?.import_session_retained === true
    || (partial && payload?.import_session_consumed === false)
  return {
    consumed,
    retained: retained && !consumed,
    manualConfigurationRequired: payload?.manual_configuration_required === true,
    partial,
    committedNames: safeNames(payload?.committed_names),
    remainingNames: safeNames(payload?.remaining_names),
  }
}

export function formatSourceSecretSessionFailure(error: unknown): string {
  const metadata = getSourceSecretSessionFailureMetadata(error)
  const committed = metadata.committedNames.length > 0
    ? ` Retained credentials: ${metadata.committedNames.join(', ')}.`
    : ''
  const remaining = metadata.remainingNames.length > 0
    ? ` Remaining credentials: ${metadata.remainingNames.join(', ')}.`
    : ''
  if (metadata.retained) {
    return `Some detected credential values could not be imported, but the protected import session was retained. Correct the credential provider or bindings, then retry this save.${committed}${remaining}`
  }
  if (metadata.consumed || metadata.manualConfigurationRequired || metadata.partial) {
    return `Some detected credential values could not be imported. The protected source session cannot be retried; configure the remaining credentials manually.${committed}${remaining}`
  }
  return 'Detected credential migration could not be confirmed. To prevent reuse of a possibly consumed source session, configure the credentials manually or restart the import.'
}

export function sourceSecretSessionIsExpired(expiresAt: string | undefined, now = Date.now()): boolean {
  if (!expiresAt) return false
  const expiry = Date.parse(expiresAt)
  return Number.isFinite(expiry) && expiry <= now
}
