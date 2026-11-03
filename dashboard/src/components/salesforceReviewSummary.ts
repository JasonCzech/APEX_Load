// Shared view helpers for SalesforceReview so the dedicated Salesforce import
// view and the enterprise-archive import view render the broadened API-surface
// breakdown identically.
import type { SalesforceNativeRequestReference, SalesforceReview } from '../types/scriptImports'
import type { SourceReview, SourceReviewMapping } from '../types/scripts'

const ACTION_PATH = /^(?:init|actions|teardown)\[\d+\](?:\.[A-Za-z_]\w*(?:\[\d+\])?)*$/
const PATH_INDEX = /\[(\d+)\]/g
const validActionPath = (value: string | undefined) => typeof value === 'string' && value.length <= 4096 && ACTION_PATH.test(value) ? value : ''

// Excluding requests after conversion moves each later action's mapping to a
// lower index in the same phase, under the same mapping id, and renumbers the
// requests left inside an http_sequence the same way. A mapping the exclusion
// could have produced from the recorded path still identifies the captured
// request: the same phase and field names, with no index higher than before.
function shiftedTargetPath(original: string, moved: string | undefined): boolean {
  const target = validActionPath(moved)
  if (!target || original.replace(PATH_INDEX, '[]') !== target.replace(PATH_INDEX, '[]')) return false
  const from = Array.from(original.matchAll(PATH_INDEX), match => Number(match[1]))
  const to = Array.from(target.matchAll(PATH_INDEX), match => Number(match[1]))
  return from.length === to.length && to.every((index, position) => index <= from[position])
}

export function exactSourceMapping(reference: SalesforceNativeRequestReference | undefined, review: SourceReview | undefined, positionsShifted: boolean): SourceReviewMapping | undefined {
  if (!reference) return undefined
  const originalPath = validActionPath(reference.original_action_path)
  if (!reference.source_mapping_id || !reference.source_file || !originalPath) return undefined
  const matches = Array.isArray(review?.mappings) ? review.mappings.filter(mapping => mapping && typeof mapping === 'object' && mapping.id === reference.source_mapping_id) : []
  const mapping = matches.length === 1 ? matches[0] : undefined
  if (mapping?.source_file !== reference.source_file) return undefined
  if (mapping.target_path === originalPath) return mapping
  return positionsShifted && shiftedTargetPath(originalPath, mapping.target_path) ? mapping : undefined
}


export type SalesforceEndpointKey =
  | 'aura_endpoints' | 'lightning_endpoints' | 'login_flow'
  | 'ui_api_endpoints' | 'rest_endpoints' | 'apex_rest_endpoints'
  | 'composite_endpoints' | 'graphql_endpoints' | 'bulk_endpoints' | 'agentforce_endpoints'

export function salesforceEndpointTotal(review: SalesforceReview, key: SalesforceEndpointKey): number {
  return (review[key] || []).reduce((sum, item) => sum + (item.count || 0), 0)
}

// API surfaces beyond the headline Aura/Lightning counts, rendered when present.
export const SALESFORCE_API_SURFACES: { key: SalesforceEndpointKey; label: string }[] = [
  { key: 'ui_api_endpoints', label: 'UI API' },
  { key: 'rest_endpoints', label: 'REST' },
  { key: 'apex_rest_endpoints', label: 'Apex REST' },
  { key: 'composite_endpoints', label: 'Composite' },
  { key: 'graphql_endpoints', label: 'GraphQL' },
  { key: 'bulk_endpoints', label: 'Bulk' },
  { key: 'agentforce_endpoints', label: 'Agentforce' },
]

export interface SalesforceApiSurface {
  key: SalesforceEndpointKey
  label: string
  calls: number
  endpoints: number
}

export function salesforceApiSurfaceBreakdown(review: SalesforceReview): SalesforceApiSurface[] {
  return SALESFORCE_API_SURFACES
    .map(surface => ({
      ...surface,
      calls: salesforceEndpointTotal(review, surface.key),
      endpoints: review[surface.key]?.length || 0,
    }))
    .filter(surface => surface.calls > 0)
}

// Compact one-line summary (e.g. "Apex REST 3, Composite 2") for dense layouts.
export function salesforceApiSurfaceSummary(review: SalesforceReview): string {
  const parts = salesforceApiSurfaceBreakdown(review).map(surface => `${surface.label} ${surface.calls}`)
  return parts.length ? parts.join(', ') : '—'
}
