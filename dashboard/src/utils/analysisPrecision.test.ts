import { expect, it } from 'vitest'
import type { AnalysisImport, AnalysisMapping, AnalysisPrecisionReview } from '../types/analysis'
import { admitAnalysisPrecisionReview, analysisPrecisionImportReady } from './analysisPrecision'

const digest = 'a'.repeat(64)
const dataset: AnalysisImport = { id: 'dataset', project_id: 'project', name: 'Dataset', revision: 7, state: 'mapped', accepted: 0, rejected: 0, files: [{ id: 'file', relative_path: 'file.parquet', uploaded: 12, size: 12 }] }
const review: AnalysisPrecisionReview = { version: 1, import_id: 'dataset', revision: 7, acknowledgement_sha256: digest, review: { version: 1, conversion: 'float64', mapping_sha256: digest, source_set_sha256: digest, source_count: 1, sha256: digest, risks: [{ file_sha256: digest, column: ['amount'], precision: 38, scale: 2, reason: 'fractional_decimal_may_round_to_float64' }] } }

it('admits only bounded review metadata for the exact import snapshot', () => {
  expect(() => admitAnalysisPrecisionReview(review, dataset)).not.toThrow()
  for (const change of [
    { import_id: 'foreign' }, { revision: 6 }, { version: 2 }, { acknowledgement_sha256: digest.toUpperCase() },
    ...[{ conversion: 'float32' }, { source_count: 2 }, { mapping_sha256: '' }, { source_set_sha256: '' }, { sha256: '' }, { risks: Array(65).fill(review.review.risks[0]) }].map(change => ({ review: { ...review.review, ...change } })),
  ]) expect(() => admitAnalysisPrecisionReview({ ...review, ...change }, dataset)).toThrow()
})
it('refuses malformed or protected risk columns before rendering them', () => {
  for (const change of [{ column: ['credentials', 'password'] }, { column: [] }, { column: [''] }, { column: ['x'.repeat(257)] }, { precision: 39 }, { scale: 39 }, { scale: -1 }, { scale: 1.5 }, { file_sha256: 'x'.repeat(64) }, { reason: 'unknown' }]) {
    const bad = { ...review, review: { ...review.review, risks: [{ ...review.review.risks[0], ...change }] } }
    expect(() => admitAnalysisPrecisionReview(bad, dataset)).toThrow()
  }
})
it('keeps a reviewed risk separate from the user acknowledgement and policy', () => {
  const mapping: AnalysisMapping = { version: 3, precision_policy: 'allow_float64_rounding', timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [] }
  expect(analysisPrecisionImportReady(mapping, undefined, true)).toBe(false)
  expect(analysisPrecisionImportReady(mapping, review, false)).toBe(false)
  expect(analysisPrecisionImportReady(mapping, review, true)).toBe(true)
  expect(analysisPrecisionImportReady({ ...mapping, precision_policy: 'reject_potential_loss' }, review, true)).toBe(false)
  expect(analysisPrecisionImportReady({ ...mapping, version: 2 }, review, true)).toBe(false)
  expect(analysisPrecisionImportReady({ ...mapping, version: 2 }, { ...review, review: { ...review.review, risks: [] } }, false)).toBe(true)
})
