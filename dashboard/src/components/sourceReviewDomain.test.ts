import { describe, expect, it } from 'vitest'
import type { ScriptDefinition, ScriptImportMetadata, SourceReview } from '../types/scripts'
import { scriptHasSourceOnlyReview, sourceReviewForImport } from './sourceReviewDomain'

const review: SourceReview = { mappings: [{ id: 'original', source_file: 'capture.har', start_line: 1, target_path: 'actions[0]', status: 'converted' }] }
const metadata: ScriptImportMetadata = { source: 'salesforce', preserved: { source_review: review, salesforce_review: { native_preview: { correlations: [] } } } }
const script = (importMetadata: ScriptImportMetadata) => ({ name: 'Native', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], metadata: { import: importMetadata } }) as ScriptDefinition

describe('source review domain compatibility', () => {
  it('conservatively identifies only legacy native-selected Salesforce provenance without mutating captured evidence', () => {
    const before = JSON.stringify(metadata)
    expect(sourceReviewForImport(review, metadata)).toEqual({ ...review, target_domain: 'original_import' })
    expect(scriptHasSourceOnlyReview(script(metadata))).toBe(true)
    expect(JSON.stringify(metadata)).toBe(before)
  })

  it.each(['har', 'enterprise_script_archive', 'enterprise_archive', undefined])('retains current bindings for %s generic imports even with native preview evidence', source => {
    const generic = { ...metadata, source }
    expect(sourceReviewForImport(review, generic)).toBe(review)
    expect(scriptHasSourceOnlyReview(script(generic))).toBe(false)
  })

  it.each([undefined, null, [], 'not-preview'])('does not infer native selection from missing or malformed native preview %s', native_preview => {
    const missing = { ...metadata, preserved: { source_review: review, salesforce_review: { native_preview } } }
    expect(sourceReviewForImport(review, missing)).toBe(review)
    expect(scriptHasSourceOnlyReview(script(missing))).toBe(false)
  })

  it.each(['', 'original_import', 'future_domain'])('keeps an explicit %s domain authoritative over legacy provenance', target_domain => {
    const explicit = { ...review, target_domain }
    const marked = { ...metadata, preserved: { ...metadata.preserved, source_review: explicit } }
    expect(sourceReviewForImport(explicit, marked)).toBe(explicit)
    expect(scriptHasSourceOnlyReview(script(marked))).toBe(target_domain !== '')
  })
})
