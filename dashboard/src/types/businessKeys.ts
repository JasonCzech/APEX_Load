// Business keys (G16): approved mappings from a script extraction to a log
// field of an elastic-query monitor source, and lookups that join supplied
// values to a run. Lookup values are never stored or echoed by the
// controller; results refer to them by their position in the request.

export interface BusinessKeyMapping {
  extraction: string
  source_id: string
  field: string
  label?: string
}

export interface BusinessKeyMappingDefinition {
  id: string
  project_id: string
  kind: 'business_key_mapping'
  name: string
  revision: number
  definition: BusinessKeyMapping
  deleted: boolean
}

export interface BusinessKeyRequest {
  timestamp: string
  population?: string
  group?: string
  name: string
  duration_ms: number
  success: boolean
  status_code?: number
  trace_id?: string
  matched_by: 'digest' | 'value'
}

export interface BusinessKeyLogEntry {
  timestamp: string
  trace_id?: string
  message?: string
}

export interface BusinessKeyLookupResult {
  index: number
  requests: { total: number; matches: BusinessKeyRequest[] }
  logs: { status: 'found' | 'not_found' | 'error' | 'unavailable'; detail?: string; total: number; entries: BusinessKeyLogEntry[] }
}

export interface BusinessKeyLookupResponse {
  mapping: { id: string; revision: number; label?: string; extraction: string; source_id: string; field: string }
  results: BusinessKeyLookupResult[]
  limitations: string[]
}
