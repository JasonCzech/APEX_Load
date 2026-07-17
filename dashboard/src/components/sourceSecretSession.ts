import { isValidSecretBindingName } from './secretBindingUtils'

export interface SourceSecretSessionFailureMetadata {
  consumed: boolean
  manualConfigurationRequired: boolean
  partial: boolean
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
  const remainingNames = Array.isArray(payload?.remaining_names)
    ? Array.from(new Set(payload.remaining_names
      .filter((name): name is string => typeof name === 'string')
      .map(name => name.trim())
      .filter(isValidSecretBindingName)))
    : []
  return {
    consumed: payload?.source_secret_session_consumed === true || payload?.import_session_consumed === true,
    manualConfigurationRequired: payload?.manual_configuration_required === true,
    partial: payload?.partial === true,
    remainingNames,
  }
}

export function formatSourceSecretSessionFailure(error: unknown): string {
  const metadata = getSourceSecretSessionFailureMetadata(error)
  const names = metadata.remainingNames.length > 0
    ? ` Remaining credentials: ${metadata.remainingNames.join(', ')}.`
    : ''
  if (metadata.consumed || metadata.manualConfigurationRequired || metadata.partial) {
    return `Some detected credential values could not be imported. The protected source session cannot be retried; configure the remaining credentials manually.${names}`
  }
  return 'Detected credential migration could not be confirmed. To prevent reuse of a possibly consumed source session, configure the credentials manually or restart the import.'
}

export function sourceSecretSessionIsExpired(expiresAt: string | undefined, now = Date.now()): boolean {
  if (!expiresAt) return false
  const expiry = Date.parse(expiresAt)
  return Number.isFinite(expiry) && expiry <= now
}
