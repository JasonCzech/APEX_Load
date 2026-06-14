// Shared view helpers for SalesforceReview so the dedicated Salesforce import
// view and the enterprise-archive import view render the broadened API-surface
// breakdown identically.
import type { SalesforceReview } from '../types/scriptImports'

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
