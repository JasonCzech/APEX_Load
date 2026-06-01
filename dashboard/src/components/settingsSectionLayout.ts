export const SETTINGS_SECTIONS = [
  { id: 'general', title: 'General' },
  { id: 'thresholds', title: 'Performance Thresholds' },
  { id: 'capacity', title: 'Capacity Planning' },
  { id: 'applications', title: 'Applications & Releases' },
  { id: 'projects', title: 'Projects & Compliance' },
  { id: 'data-pools', title: 'Shared Data Pools' },
  { id: 'integrations', title: 'Integrations' },
] as const

export type SettingsSectionId = typeof SETTINGS_SECTIONS[number]['id']

const SETTINGS_SECTION_IDS = SETTINGS_SECTIONS.map(section => section.id)

export function isSettingsSectionId(value: unknown): value is SettingsSectionId {
  return typeof value === 'string' && SETTINGS_SECTION_IDS.includes(value as SettingsSectionId)
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

export function mergeSettingsOrder(order: SettingsSectionId[]) {
  const retained = order.filter(id => SETTINGS_SECTION_IDS.includes(id))
  const added = SETTINGS_SECTION_IDS.filter(id => !retained.includes(id))
  return [...retained, ...added]
}

export function moveSettingsSection(ids: SettingsSectionId[], draggedId: SettingsSectionId, targetId: SettingsSectionId) {
  if (draggedId === targetId) return ids
  const next = ids.filter(id => id !== draggedId)
  const targetIndex = next.indexOf(targetId)
  if (targetIndex === -1) return ids
  next.splice(targetIndex, 0, draggedId)
  return next
}
