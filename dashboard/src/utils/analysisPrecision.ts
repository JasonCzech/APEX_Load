import type { AnalysisImport, AnalysisMapping, AnalysisPrecisionReview } from '../types/analysis'
import { analysisIdentityTextError } from './analysisIdentity'
import { analysisProtectedIdentityColumn } from './analysisCredentialColumn'

const digest = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value)

export function admitAnalysisPrecisionReview(response: AnalysisPrecisionReview, selected: AnalysisImport): void {
  const review = response?.review
  if (response?.version !== 1 || response.import_id !== selected.id || response.revision !== selected.revision || !digest(response.acknowledgement_sha256) || review?.version !== 1 || review.conversion !== 'float64' || !digest(review.sha256) || !digest(review.mapping_sha256) || !digest(review.source_set_sha256) || review.source_count !== selected.files.length || review.source_count < 1 || review.source_count > 1024 || !Array.isArray(review.risks) || review.risks.length > 64) throw new Error('The controller returned an invalid or stale precision review. Review the current import again.')
  for (const risk of review.risks) {
    if (!risk || !digest(risk.file_sha256) || !Array.isArray(risk.column) || risk.column.length < 1 || risk.column.length > 16 || risk.column.some(part => analysisIdentityTextError(part, 256, true)) || analysisProtectedIdentityColumn(risk.column) || !Number.isInteger(risk.precision) || risk.precision < 1 || risk.precision > 38 || !Number.isInteger(risk.scale) || risk.scale < 0 || risk.scale > risk.precision || !['fractional_decimal_may_round_to_float64', 'decimal_digits_may_exceed_float64_integer_precision'].includes(risk.reason)) throw new Error('The controller returned unsupported precision metadata. The import remains unchanged.')
  }
}

export function analysisPrecisionImportReady(mapping: AnalysisMapping, response: AnalysisPrecisionReview | undefined, acknowledged: boolean): boolean {
  if (!response) return false
  return response.review.risks.length === 0 || mapping.version === 3 && mapping.precision_policy === 'allow_float64_rounding' && acknowledged
}
