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

// The query semantics a panel is saved under (query_version).
export const ANALYTICS_QUERY_VERSION = 3

export interface BrowserPanelPolicyPin { kind: 'browser_policy' | 'journey_policy'; id: string; revision: number; sha256: string }
export interface BrowserPanelRunInput { run_id: string; manifest_sha256: string; tuple_ids: string[]; policy_pins?: BrowserPanelPolicyPin[] }
export interface BrowserPanelInputs { version: 1 | 2; query_sha256: string; policy_pins?: BrowserPanelPolicyPin[]; selection_receipt_sha256?: string; current: BrowserPanelRunInput; baseline?: BrowserPanelRunInput }

export interface DashboardAnalyticsQuery {
  browser_inputs?: BrowserPanelInputs
  browser_tuple_mode?: 'separate'
  query_version?: number
  live_bucket_mode?: 'source' | 'merged'
  evidence_mode?: 'live' | 'archive'
  overlay_selection?: { dataset_id?: string; source_id?: string; metric: string; aggregation: string; host?: string; dimensions?: Record<string, string>; max_lag_seconds?: number; dataset_artifact_sha256?: string }

  population?: 'action' | 'transaction' | 'browser_document' | 'message' | 'java_http_action' | 'byte'
  property_name?: string
  property_value?: string
  comparison_run_id?: string
  start_seconds?: number
  end_seconds?: number
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
  // Set when a past saved revision is returned: the revision now.
  current_revision?: number
  // The saved revisions a viewer can reopen, newest first.
  retained_revisions?: { revision: number; name: string; saved_at: string }[]
}

/** One definition, or exactly its saved revision. */
export function fetchDashboardDefinition(id: string, revision?: number, signal?: AbortSignal) {
  const query = revision ? `?revision=${encodeURIComponent(String(revision))}` : ''
  return request<DashboardDefinition>(`/monitoring/dashboard-definitions/${encodeURIComponent(id)}${query}`, { signal })
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
