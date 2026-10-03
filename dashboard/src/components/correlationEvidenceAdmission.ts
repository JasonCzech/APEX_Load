import type { CorrelationRelationshipCoverage } from '../types/scripts'
import type { SalesforceCorrelationCoverage, SalesforceNativeRequestReference } from '../types/scriptImports'

const object = (value: unknown): Record<string, unknown> | undefined => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined
const number = (value: unknown) => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
const nativeCounts = ['discovered_correlations', 'import_matched_producer_correlations', 'import_unmatched_producer_correlations', 'observed_consumer_references', 'import_matched_consumer_references', 'import_unmatched_consumer_references', 'source_linked_producer_correlations', 'source_unlinked_producer_correlations', 'source_linked_consumer_references', 'source_unlinked_consumer_references', 'native_mapped_producer_correlations', 'native_unmapped_producer_correlations', 'native_mapped_consumer_references', 'native_unmapped_consumer_references', 'pre_applied_correlations', 'manual_correlations', 'retained_correlations', 'omitted_correlations', 'retained_consumer_references', 'omitted_consumer_references', 'correlation_detail_limit', 'consumer_reference_detail_limit', 'detail_byte_limit']
const relationshipCounts = ['producer_count', 'consumer_count', 'missing_producers', 'unavailable_extractors', 'missing_consumers', 'details_omitted', 'detail_limit']
const relationshipStates = ['producer_missing', 'extractor_unavailable', 'consumer_missing', 'producer_unchanged', 'stale_literal', 'no_stale_literal', 'manual_encoding', 'manual_surface']
const referenceFields = ['original_action_path', 'source_mapping_id', 'source_file', 'native_action_path']

// A report may count only the fixed supported states. Stop after at most nine
// own keys instead of allocating an unbounded key list from malformed input.
function validRelationshipStateCounts(states?: Record<string, unknown>): boolean {
  if (!states) return false
  let inspected = 0
  for (const state in states) {
    if (!Object.prototype.hasOwnProperty.call(states, state)) continue
    if (++inspected > relationshipStates.length || !relationshipStates.includes(state) || !number(states[state])) return false
  }
  return true
}

function reference(value: unknown): SalesforceNativeRequestReference | undefined {
  const item = object(value)
  if (!item || !number(item.har_entry_id) || referenceFields.some(field => item[field] !== undefined && (typeof item[field] !== 'string' || (item[field] as string).length > 4096))) return undefined
  return item as unknown as SalesforceNativeRequestReference
}

export function admitNativeCorrelationEvidence(value: unknown) {
  const preview = object(value)
  const coverage = object(preview?.correlation_coverage)
  const coverageValid = Boolean(coverage && typeof coverage.complete === 'boolean' && typeof coverage.details_complete === 'boolean' && typeof coverage.source_review_available === 'boolean' && nativeCounts.every(field => number(coverage[field])) && (coverage.catalog_limited === undefined || typeof coverage.catalog_limited === 'boolean'))
  let malformed = value != null && !preview || !!preview && preview.correlations !== undefined && !Array.isArray(preview.correlations) || !!preview && preview.correlation_coverage !== undefined && !coverageValid
  let omittedRows = 0, omittedReferences = 0, referencesRemaining = 2048
  const rawRows = Array.isArray(preview?.correlations) ? preview.correlations : []
  omittedRows = Math.max(0, rawRows.length - 2048)
  const rows: { ordinal: number; preApplied: boolean; producer?: SalesforceNativeRequestReference; consumers: SalesforceNativeRequestReference[]; consumersOmitted: number }[] = []
  for (let index = 0; index < Math.min(rawRows.length, 2048); index++) {
    const row = object(rawRows[index])
    if (!row) { malformed = true; omittedRows++; continue }
    const producer = row.producer_reference === undefined ? undefined : reference(row.producer_reference)
    if (row.producer_reference !== undefined && !producer) malformed = true
    if (row.pre_applied !== undefined && typeof row.pre_applied !== 'boolean') malformed = true
    if (row.consumer_references_omitted !== undefined && !number(row.consumer_references_omitted)) malformed = true
    const rawConsumers = Array.isArray(row.consumer_references) ? row.consumer_references : []
    if (row.consumer_references !== undefined && !Array.isArray(row.consumer_references)) malformed = true
    const consumers: SalesforceNativeRequestReference[] = []
    const inspected = Math.min(rawConsumers.length, referencesRemaining)
    let rejected = 0
    for (let consumerIndex = 0; consumerIndex < inspected; consumerIndex++) {
      const item = reference(rawConsumers[consumerIndex])
      if (item) consumers.push(item)
      else { malformed = true; rejected++ }
    }
    referencesRemaining -= inspected
    const omitted = rawConsumers.length - inspected + rejected
    omittedReferences += omitted
    rows.push({ ordinal: index + 1, preApplied: row.pre_applied === true, producer, consumers, consumersOmitted: (number(row.consumer_references_omitted) ? row.consumer_references_omitted as number : 0) + omitted })
  }
  return { present: value != null, coverage: coverage as unknown as SalesforceCorrelationCoverage | undefined, coverageValid, rows, malformed: Boolean(malformed), omittedRows, omittedReferences, nativeFinal: preview?.native_path_domain === 'final_native' }
}

export function admitRelationshipEvidence(value: unknown) {
  const coverage = object(value)
  const valid = Boolean(coverage && typeof coverage.complete === 'boolean' && typeof coverage.details_complete === 'boolean' && relationshipCounts.every(field => number(coverage[field])) && object(coverage.state_counts))
  const states = object(coverage?.state_counts)
  let malformed = value != null && !valid || !!coverage && !Array.isArray(coverage.relationships) || !!states && !validRelationshipStateCounts(states)
  const rawRows = Array.isArray(coverage?.relationships) ? coverage.relationships : []
  let omittedRows = Math.max(0, rawRows.length - 256)
  const rows: CorrelationRelationshipCoverage['relationships'] = []
  for (let index = 0; index < Math.min(rawRows.length, 256); index++) {
    const row = object(rawRows[index])
    if (!row) { malformed = true; omittedRows++; continue }
    if (!number(row.producer_step_id) || !number(row.consumer_step_id) || typeof row.state !== 'string' || !relationshipStates.includes(row.state) || ['producer_observed', 'extractor_available', 'consumer_observed'].some(field => typeof row[field] !== 'boolean')) { malformed = true; omittedRows++; continue }
    rows.push(row as unknown as CorrelationRelationshipCoverage['relationships'][number])
  }
  return { present: value != null, coverage: coverage as unknown as CorrelationRelationshipCoverage | undefined, rows, malformed: Boolean(malformed), omittedRows }
}
