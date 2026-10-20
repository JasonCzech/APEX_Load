import type { AnalysisImport, AnalysisMapping } from '../types/analysis'

export const mappingAssociationVersion = 1

// Plain legacy summaries carry no statistic/window declaration. Preserve that
// older display-only contract; identity and measurement declarations need PATCH.
export function mappingRequiresNegotiatedAssociation(mapping?: AnalysisMapping): boolean {
  return !mapping || (mapping.version !== 1 || mapping.measures.some(measure =>
    measure.kind === 'histogram' || measure.statistic !== undefined || measure.window_seconds !== undefined ||
    measure.sample_count_column !== undefined || measure.sample_sum_column !== undefined || measure.histogram !== undefined))
}

export function mappingAssociationCapabilityError(mapping: AnalysisMapping | undefined, advertised?: number): string | undefined {
  if (!mapping) return 'Mapping details are unavailable. Reload the dataset before changing the associated run.'
  if (!mappingRequiresNegotiatedAssociation(mapping)) return
  if (mapping?.version !== 1 && mapping?.version !== 2) return 'This mapping version cannot be reassociated by this client.'
  if (advertised !== mappingAssociationVersion) return 'This controller cannot safely reassociate this mapping. Upgrade the controller before changing the associated run.'
}

function canonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonical)
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).filter(([, item]) => item !== undefined).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0).map(([key, item]) => [key, canonical(item)]))
  return value
}

export function admitMappingAssociationResponse(before: AnalysisImport, runID: string, response: { contract_version?: number; import?: AnalysisImport }): AnalysisImport {
  const result = response?.import
  if (response?.contract_version !== mappingAssociationVersion || !result || result.id !== before.id || result.project_id !== before.project_id || result.revision !== before.revision + 1 || (result.run_id ?? '') !== runID || result.state !== 'ready' || JSON.stringify(canonical(result.mapping)) !== JSON.stringify(canonical(before.mapping))) {
    throw new Error('The controller did not confirm the mapping-preserving association. Reload the dataset before continuing.')
  }
  return result
}
