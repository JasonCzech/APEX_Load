import {
  readStoredString,
  removeStoredItem,
  writeStoredJSON,
  writeStoredString,
} from '../utils/browserStorage'
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
