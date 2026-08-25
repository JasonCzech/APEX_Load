import { useCallback, useMemo } from 'react'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'

export interface DashboardDefinitionPanel {
  id: string
  title: string
  card_id?: string
  kind: string
  x: number
  y: number
  w: number
  h: number
	query?: DashboardAnalyticsQuery
}

export interface DashboardAnalyticsQuery {
	metric: string
	aggregation: string
	dimensions?: string[]
	filters?: Record<string, string>
	bucket_seconds?: number
	max_series?: number
	overlay?: boolean
}

export interface DashboardDefinitionTab {
  id: string
  title: string
  panels: DashboardDefinitionPanel[]
}

export interface DashboardDefinition {
  id: string
  project_id: string
  name: string
  description?: string
  owner?: string
  visibility: 'project'
  revision: number
  slideshow_interval_seconds?: number
  tabs: DashboardDefinitionTab[]
  created_at: string
  updated_at: string
}

export function useDashboardDefinitionsApi() {
  const createDashboardDefinition = useCallback((body: {
    project_id: string
    name: string
    description?: string
    visibility: 'project'
    slideshow_interval_seconds?: number
    tabs: DashboardDefinitionTab[]
  }, options?: ApiCallOptions) => request<DashboardDefinition>('/monitoring/dashboard-definitions', withCallOptions({
    method: 'POST', body: JSON.stringify(body),
  }, options)), [])

  const listDashboardDefinitions = useCallback((projectId: string, options?: ApiCallOptions) =>
    request<{ dashboards: DashboardDefinition[]; count: number }>(`/monitoring/dashboard-definitions?project_id=${encodeURIComponent(projectId)}`, withCallOptions(undefined, options)), [])

  return useMemo(() => ({ createDashboardDefinition, listDashboardDefinitions }), [createDashboardDefinition, listDashboardDefinitions])
}
