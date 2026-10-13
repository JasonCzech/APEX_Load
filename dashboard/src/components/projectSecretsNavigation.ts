import { readStoredJSON, removeStoredItem, writeStoredJSON } from '../utils/browserStorage'
import { sharedAssetsHref } from './sharedAssetsNavigation'

export const PROJECT_SECRETS_SETTINGS_PROJECT_KEY = 'apexload.settings.projectId'

/**
 * How long a Configure secrets request waits for the Secrets section. One
 * whose navigation never arrived (a cancelled unsaved-changes prompt, a tab
 * that never opened) must not steer a later, unrelated visit.
 */
export const PROJECT_SECRETS_REQUEST_TTL_MS = 60_000

/** Remembers which project a Configure secrets link is for, so another is never configured in its place. */
export function prepareProjectSecretsNavigation(projectId?: string, now = Date.now()) {
  if (projectId?.trim()) {
    writeStoredJSON(PROJECT_SECRETS_SETTINGS_PROJECT_KEY, { projectId: projectId.trim(), at: now })
  } else {
    removeStoredItem(PROJECT_SECRETS_SETTINGS_PROJECT_KEY)
  }
}

/** The project a recent Configure secrets link named, or '' when none is waiting. */
export function requestedProjectSecretsProjectId(now = Date.now()): string {
  const request = readStoredJSON<unknown>(PROJECT_SECRETS_SETTINGS_PROJECT_KEY, null)
  if (!request || typeof request !== 'object') return ''
  const { projectId, at } = request as { projectId?: unknown; at?: unknown }
  if (typeof projectId !== 'string' || typeof at !== 'number' || now - at > PROJECT_SECRETS_REQUEST_TTL_MS) return ''
  return projectId.trim()
}

export function consumeProjectSecretsSettingsProjectId(now = Date.now()): string {
  const projectId = requestedProjectSecretsProjectId(now)
  removeStoredItem(PROJECT_SECRETS_SETTINGS_PROJECT_KEY)
  return projectId
}

/** Where secrets live: Shared assets › Secrets, which every role that may see secrets can open. */
export function projectSecretsHref(): string {
  return sharedAssetsHref('secrets')
}

/** Opens Shared assets › Secrets for a project, in place of whatever page asked. */
export function openProjectSecretsPage(projectId?: string) {
  prepareProjectSecretsNavigation(projectId)
  window.location.hash = projectSecretsHref()
}
