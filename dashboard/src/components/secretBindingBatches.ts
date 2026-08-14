import { type ProjectSecretWrite } from '../types/secrets'

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
    await createSecrets(projectId, batch)
    onCommitted(batch.map(secret => secret.name))
    if (!requestIsCurrent()) return false
  }
  return true
}
