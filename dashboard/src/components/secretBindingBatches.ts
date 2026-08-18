import { type ProjectSecretWrite } from '../types/secrets'
import { ApiError } from '../hooks/apiClient'

const PROJECT_SECRET_WRITE_BATCH_SIZE = 256

/**
 * Keep project-secret writes inside the controller's per-request item limit.
 * A completed batch is reported immediately so callers do not retry names
 * whose create-only write already succeeded if a later batch fails.
 */
export async function createProjectSecretsInBatches(
  createSecrets: (projectId: string, secrets: ProjectSecretWrite[]) => Promise<unknown>,
  projectId: string,
  secrets: readonly ProjectSecretWrite[],
  onCommitted: (names: readonly string[]) => void,
  requestIsCurrent: () => boolean = () => true,
): Promise<boolean> {
  for (let offset = 0; offset < secrets.length; offset += PROJECT_SECRET_WRITE_BATCH_SIZE) {
    if (!requestIsCurrent()) return false
    const batch = secrets.slice(offset, offset + PROJECT_SECRET_WRITE_BATCH_SIZE)
    try {
      await createSecrets(projectId, batch)
    } catch (cause) {
      // Providers may durably create a prefix before returning a structured
      // partial failure. Checkpoint only names that the server confirms and that
      // belonged to this exact request, then let the caller surface the failure.
      if (cause instanceof ApiError && cause.payload && typeof cause.payload === 'object') {
        const payload = cause.payload as { partial?: unknown; created?: unknown }
        if (payload.partial === true && Array.isArray(payload.created)) {
          const requested = new Set(batch.map(secret => secret.name))
          const committed = Array.from(new Set(payload.created.flatMap(entry => {
            if (!entry || typeof entry !== 'object') return []
            const name = (entry as { name?: unknown }).name
            return typeof name === 'string' && requested.has(name) ? [name] : []
          })))
          if (committed.length > 0) onCommitted(committed)
        }
      }
      throw cause
    }
    onCommitted(batch.map(secret => secret.name))
    if (!requestIsCurrent()) return false
  }
  return true
}
