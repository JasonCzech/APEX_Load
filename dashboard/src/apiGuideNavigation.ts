export type HelpAreaID = 'getting-started' | 'user-guide' | 'admin-guide' | 'general' | 'troubleshooting'
// Existing public URLs remain valid aliases for the consolidated automation area.
export type ApiGuideContentPageID = HelpAreaID | 'mcp' | 'authentication' | 'quick-start'
export type ApiGuideToggleID = ApiGuideContentPageID | 'swagger' | 'repo-guide'
export type ApiGuideToggleOption = { value: HelpAreaID; label: string }
export const API_GUIDE_DEFAULT_PAGE: ApiGuideContentPageID = 'getting-started'
export const API_GUIDE_CONTENT_PAGE_IDS: ApiGuideContentPageID[] = ['getting-started', 'user-guide', 'admin-guide', 'general', 'troubleshooting', 'mcp', 'authentication', 'quick-start']
export const API_GUIDE_TOGGLE_OPTIONS: ApiGuideToggleOption[] = [
  { value: 'getting-started', label: 'Getting Started' },
  { value: 'user-guide', label: 'User Guide' },
  { value: 'general', label: 'API & Automation' },
  { value: 'troubleshooting', label: 'Troubleshooting' },
]
export function apiGuideToggleOptions({ includeAdminGuide }: { includeAdminGuide: boolean }): ApiGuideToggleOption[] {
  return includeAdminGuide
    ? [...API_GUIDE_TOGGLE_OPTIONS.slice(0, 2), { value: 'admin-guide', label: 'Administration' }, ...API_GUIDE_TOGGLE_OPTIONS.slice(2)]
    : API_GUIDE_TOGGLE_OPTIONS
}
export function helpAreaForPage(page: ApiGuideToggleID): HelpAreaID {
  return ['swagger', 'repo-guide', 'mcp', 'authentication', 'quick-start'].includes(page) ? 'general' : page as HelpAreaID
}
export function isApiGuideContentPageID(value: string | null | undefined): value is ApiGuideContentPageID {
  return API_GUIDE_CONTENT_PAGE_IDS.includes(value as ApiGuideContentPageID)
}
export function apiGuideHashForToggle(value: ApiGuideToggleID): string {
  if (value === 'swagger') return '#/swagger'
  if (value === 'repo-guide') return '#/repository-guide'
  return value === API_GUIDE_DEFAULT_PAGE ? '#/docs' : `#/docs?guide=${value}`
}
