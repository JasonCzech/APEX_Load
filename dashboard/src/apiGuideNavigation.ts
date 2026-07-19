export type ApiGuideContentPageID = 'general' | 'user-guide' | 'admin-guide' | 'mcp' | 'quick-start' | 'authentication' | 'endpoint-map'
export type ApiGuideRouteID = 'swagger' | 'repo-guide'
export type ApiGuideToggleID = ApiGuideContentPageID | ApiGuideRouteID

export type ApiGuideToggleOption = { value: ApiGuideToggleID; label: string }

export const API_GUIDE_DEFAULT_PAGE: ApiGuideContentPageID = 'general'

export const API_GUIDE_CONTENT_PAGE_IDS: ApiGuideContentPageID[] = [
  'general',
  'user-guide',
  'admin-guide',
  'mcp',
  'quick-start',
  'authentication',
  'endpoint-map',
]

// Base toggle order. The admin-only "Admin Guide" option is not listed here; it is
// spliced in for administrators by apiGuideToggleOptions() so the chip stays hidden
// from non-admins.
export const API_GUIDE_TOGGLE_OPTIONS: ApiGuideToggleOption[] = [
  { value: 'general', label: 'General' },
  { value: 'user-guide', label: 'User Guide' },
  { value: 'mcp', label: 'MCP Server' },
  { value: 'quick-start', label: 'Quick Start' },
  { value: 'authentication', label: 'Authentication' },
  { value: 'endpoint-map', label: 'Endpoint Map' },
  { value: 'swagger', label: 'Swagger' },
  { value: 'repo-guide', label: 'Repo Guide' },
]

const ADMIN_GUIDE_TOGGLE_OPTION: ApiGuideToggleOption = { value: 'admin-guide', label: 'Admin Guide' }

// Returns the toggle options for the API guide, inserting the admin-only "Admin Guide"
// option immediately after "User Guide" when the current principal is an administrator.
export function apiGuideToggleOptions({ includeAdminGuide }: { includeAdminGuide: boolean }): ApiGuideToggleOption[] {
  if (!includeAdminGuide) return API_GUIDE_TOGGLE_OPTIONS
  const userGuideIndex = API_GUIDE_TOGGLE_OPTIONS.findIndex(option => option.value === 'user-guide')
  const insertAt = userGuideIndex === -1 ? API_GUIDE_TOGGLE_OPTIONS.length : userGuideIndex + 1
  return [
    ...API_GUIDE_TOGGLE_OPTIONS.slice(0, insertAt),
    ADMIN_GUIDE_TOGGLE_OPTION,
    ...API_GUIDE_TOGGLE_OPTIONS.slice(insertAt),
  ]
}

export function isApiGuideContentPageID(value: string | null | undefined): value is ApiGuideContentPageID {
  return API_GUIDE_CONTENT_PAGE_IDS.includes(value as ApiGuideContentPageID)
}

export function apiGuideHashForToggle(value: ApiGuideToggleID): string {
  if (value === 'swagger') return '#/swagger'
  if (value === 'repo-guide') return '#/repository-guide'
  if (value === API_GUIDE_DEFAULT_PAGE) return '#/docs'
  return `#/docs?guide=${encodeURIComponent(value)}`
}
