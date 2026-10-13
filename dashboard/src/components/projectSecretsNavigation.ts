import {
  readStoredString,
  removeStoredItem,
  writeStoredJSON,
  writeStoredString,
} from '../utils/browserStorage'
import { settingsHref } from './settingsNavigation'
import { requestSettingsFocus } from './settingsSectionLayout'

export const PROJECT_SECRETS_SETTINGS_PROJECT_KEY = 'apexload.settings.projectId'

export function prepareProjectSecretsSettingsNavigation(projectId?: string) {
  requestSettingsFocus('projects')
  writeStoredJSON('apexload.settings.projectSubsection', 'secrets')
  if (projectId?.trim()) {
    writeStoredString(PROJECT_SECRETS_SETTINGS_PROJECT_KEY, projectId.trim())
  } else {
    removeStoredItem(PROJECT_SECRETS_SETTINGS_PROJECT_KEY)
  }
}

export function consumeProjectSecretsSettingsProjectId(): string {
  const projectId = readStoredString(PROJECT_SECRETS_SETTINGS_PROJECT_KEY).trim()
  removeStoredItem(PROJECT_SECRETS_SETTINGS_PROJECT_KEY)
  return projectId
}

/**
 * Where Project secrets lives: its own destination, which every role that may
 * see secrets can open (administrators get Settings, others the vault page).
 * Plain #/settings would leave a non-administrator on personal preferences.
 */
export function projectSecretsHref(): string {
  return settingsHref('secrets')
}

/** Opens Project secrets for a project, in place of whatever page asked. */
export function openProjectSecretsSettings(projectId?: string) {
  prepareProjectSecretsSettingsNavigation(projectId)
  window.location.hash = projectSecretsHref()
}
