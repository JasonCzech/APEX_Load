export const SETTINGS_SECTIONS = [
  { id: 'general', title: 'General' },
  { id: 'thresholds', title: 'Performance Thresholds' },
  { id: 'capacity', title: 'Capacity Planning' },
  { id: 'domains', title: 'Domains' },
  { id: 'feature-access', title: 'Feature Access' },
  { id: 'projects', title: 'Projects & Compliance' },
  { id: 'applications', title: 'Applications & Releases' },
  { id: 'data-pools', title: 'Data Pools' },
  { id: 'integrations', title: 'Integrations' },
] as const

export type SettingsSectionId = typeof SETTINGS_SECTIONS[number]['id']

const SETTINGS_SECTION_IDS = SETTINGS_SECTIONS.map(section => section.id)

export const GENERAL_SETTINGS_SUBSECTIONS = [
  { id: 'platform', title: 'Platform' },
  { id: 'usage', title: 'Usage' },
  { id: 'entitlements', title: 'Entitlements' },
  { id: 'readiness', title: 'Readiness' },
  { id: 'authentication', title: 'Authentication' },
  { id: 'governance', title: 'Governance' },
] as const

export type GeneralSettingsSubsectionId = typeof GENERAL_SETTINGS_SUBSECTIONS[number]['id']

const GENERAL_SETTINGS_SUBSECTION_IDS = GENERAL_SETTINGS_SUBSECTIONS.map(section => section.id)

export const GOVERNANCE_SETTINGS_SUBSECTIONS = [
  { id: 'schedule-defaults', title: 'Schedule Defaults' },
  { id: 'script-imports', title: 'Script Imports' },
  { id: 'service-account-apis', title: 'Service Account APIs' },
  { id: 'recent-audit-events', title: 'Recent Audit Events' },
] as const

export type GovernanceSettingsSubsectionId = typeof GOVERNANCE_SETTINGS_SUBSECTIONS[number]['id']

const GOVERNANCE_SETTINGS_SUBSECTION_IDS = GOVERNANCE_SETTINGS_SUBSECTIONS.map(section => section.id)

export const CAPACITY_SETTINGS_SUBSECTIONS = [
  { id: 'availability', title: 'Availability' },
  { id: 'generator', title: 'Generator Profile' },
  { id: 'reservations', title: 'Standing allocations' },
  { id: 'retention', title: 'Retention' },
] as const

export type CapacitySettingsSubsectionId = typeof CAPACITY_SETTINGS_SUBSECTIONS[number]['id']

const CAPACITY_SETTINGS_SUBSECTION_IDS = CAPACITY_SETTINGS_SUBSECTIONS.map(section => section.id)

export const PROJECT_SETTINGS_SUBSECTIONS = [
  { id: 'catalog', title: 'Project Catalog' },
  { id: 'limits', title: 'Limits' },
  { id: 'github', title: 'GitHub Policy' },
  { id: 'redaction', title: 'Redaction Rules' },
  { id: 'secrets', title: 'Project Secrets' },
] as const

export type ProjectSettingsSubsectionId = typeof PROJECT_SETTINGS_SUBSECTIONS[number]['id']

const PROJECT_SETTINGS_SUBSECTION_IDS = PROJECT_SETTINGS_SUBSECTIONS.map(section => section.id)

export const INTEGRATION_SETTINGS_SUBSECTIONS = [
  { id: 'library', title: 'Library Federation' },
  { id: 'github', title: 'GitHub' },
  { id: 'elk-capture', title: 'Elastic test capture' },
  { id: 'dynatrace-capture', title: 'Dynatrace test capture' },
  { id: 'prometheus-capture', title: 'Prometheus test monitoring' },
  { id: 'prometheus', title: 'APEX platform graphs' },
  { id: 'opensearch', title: 'OpenSearch' },
  { id: 'grafana', title: 'Grafana' },
  { id: 'dashboards', title: 'Dashboards' },
  { id: 'powerbi', title: 'Power BI' },
  { id: 'confluence', title: 'Confluence' },
  { id: 'notifications', title: 'Notifications' },
] as const

export type IntegrationSettingsSubsectionId = typeof INTEGRATION_SETTINGS_SUBSECTIONS[number]['id']

const INTEGRATION_SETTINGS_SUBSECTION_IDS = INTEGRATION_SETTINGS_SUBSECTIONS.map(section => section.id)

export function isSettingsSectionId(value: unknown): value is SettingsSectionId {
  return typeof value === 'string' && SETTINGS_SECTION_IDS.includes(value as SettingsSectionId)
}

export function isGeneralSettingsSubsectionId(value: unknown): value is GeneralSettingsSubsectionId {
  return typeof value === 'string' && GENERAL_SETTINGS_SUBSECTION_IDS.includes(value as GeneralSettingsSubsectionId)
}

export function isGovernanceSettingsSubsectionId(value: unknown): value is GovernanceSettingsSubsectionId {
  return typeof value === 'string' && GOVERNANCE_SETTINGS_SUBSECTION_IDS.includes(value as GovernanceSettingsSubsectionId)
}

export function isCapacitySettingsSubsectionId(value: unknown): value is CapacitySettingsSubsectionId {
  return typeof value === 'string' && CAPACITY_SETTINGS_SUBSECTION_IDS.includes(value as CapacitySettingsSubsectionId)
}

export function isProjectSettingsSubsectionId(value: unknown): value is ProjectSettingsSubsectionId {
  return typeof value === 'string' && PROJECT_SETTINGS_SUBSECTION_IDS.includes(value as ProjectSettingsSubsectionId)
}

export function isIntegrationSettingsSubsectionId(value: unknown): value is IntegrationSettingsSubsectionId {
  return typeof value === 'string' && INTEGRATION_SETTINGS_SUBSECTION_IDS.includes(value as IntegrationSettingsSubsectionId)
}

export function readSettingsSectionArray(key: string): SettingsSectionId[] {
  if (typeof window === 'undefined') return []
  try {
    const parsed = JSON.parse(window.localStorage.getItem(key) || '[]')
    if (!Array.isArray(parsed)) return []
    return parsed.filter(isSettingsSectionId)
  } catch {
    return []
  }
}

export function readSettingsSubsectionId(key: string): GeneralSettingsSubsectionId
export function readSettingsSubsectionId(
  key: string,
  fallback: GeneralSettingsSubsectionId,
): GeneralSettingsSubsectionId
export function readSettingsSubsectionId<SubsectionId extends string>(
  key: string,
  fallback: SubsectionId,
  isValid: (value: unknown) => value is SubsectionId,
): SubsectionId
export function readSettingsSubsectionId(
  key: string,
  fallback: GeneralSettingsSubsectionId = 'platform',
  isValid: (value: unknown) => boolean = isGeneralSettingsSubsectionId,
): string {
  if (typeof window === 'undefined') return fallback
  try {
    const parsed = JSON.parse(window.localStorage.getItem(key) || 'null')
    return isValid(parsed) ? parsed : fallback
  } catch {
    return fallback
  }
}

export function mergeSettingsOrder(order: SettingsSectionId[]) {
  const retained = order.filter(id => SETTINGS_SECTION_IDS.includes(id))
  const featureAccessWasPersisted = retained.includes('feature-access')
  const result = [...retained]
  SETTINGS_SECTION_IDS.forEach((id) => {
    if (result.includes(id)) return
    if (id === 'domains') {
      const applicationIndex = result.indexOf('applications')
      if (applicationIndex !== -1) {
        result.splice(applicationIndex, 0, id)
        return
      }
    }
    result.push(id)
  })
  const projectIndex = result.indexOf('projects')
  const applicationIndex = result.indexOf('applications')
  if (projectIndex !== -1 && applicationIndex !== -1 && projectIndex !== applicationIndex - 1) {
    result.splice(projectIndex, 1)
    result.splice(result.indexOf('applications'), 0, 'projects')
  }
  if (!featureAccessWasPersisted) {
    const existingIndex = result.indexOf('feature-access')
    if (existingIndex !== -1) result.splice(existingIndex, 1)
    const domainIndex = result.indexOf('domains')
    result.splice(domainIndex === -1 ? result.length : domainIndex + 1, 0, 'feature-access')
  }
  return result
}

export function moveSettingsSection(ids: SettingsSectionId[], draggedId: SettingsSectionId, targetId: SettingsSectionId) {
  if (draggedId === targetId) return ids
  const next = ids.filter(id => id !== draggedId)
  const targetIndex = next.indexOf(targetId)
  if (targetIndex === -1) return ids
  next.splice(targetIndex, 0, draggedId)
  return next
}

// Stages a Settings deep link for the next SettingsPage mount: the page
// consumes (and clears) these keys, activating the section and, for capacity,
// the subsection. Call just before navigating to the settings page.
export function requestSettingsFocus(section: SettingsSectionId, subsection?: string) {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem('apexload.settings.focusSection', section)
    if (subsection) {
      window.localStorage.setItem('apexload.settings.focusSubsection', subsection)
    } else {
      window.localStorage.removeItem('apexload.settings.focusSubsection')
    }
  } catch { /* storage unavailable */ }
}
