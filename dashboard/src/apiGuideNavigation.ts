export type ApiGuideContentPageID = 'general' | 'mcp' | 'quick-start' | 'authentication' | 'endpoint-map'
export type ApiGuideRouteID = 'swagger' | 'repo-guide'
export type ApiGuideToggleID = ApiGuideContentPageID | ApiGuideRouteID

export const API_GUIDE_DEFAULT_PAGE: ApiGuideContentPageID = 'general'

export const API_GUIDE_CONTENT_PAGE_IDS: ApiGuideContentPageID[] = [
  'general',
  'mcp',
  'quick-start',
  'authentication',
  'endpoint-map',
]

export const API_GUIDE_TOGGLE_OPTIONS: Array<{ value: ApiGuideToggleID; label: string }> = [
  { value: 'general', label: 'General' },
  { value: 'mcp', label: 'MCP Server' },
  { value: 'quick-start', label: 'Quick Start' },
  { value: 'authentication', label: 'Authentication' },
  { value: 'endpoint-map', label: 'Endpoint Map' },
  { value: 'swagger', label: 'Swagger' },
  { value: 'repo-guide', label: 'Repo Guide' },
]

export function isApiGuideContentPageID(value: string | null | undefined): value is ApiGuideContentPageID {
  return API_GUIDE_CONTENT_PAGE_IDS.includes(value as ApiGuideContentPageID)
}

export function apiGuideHashForToggle(value: ApiGuideToggleID): string {
  if (value === 'swagger') return '#/swagger'
  if (value === 'repo-guide') return '#/repository-guide'
  if (value === API_GUIDE_DEFAULT_PAGE) return '#/docs'
  return `#/docs?guide=${encodeURIComponent(value)}`
}
