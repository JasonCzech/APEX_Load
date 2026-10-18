import { describe, expect, it } from 'vitest'
import { canonicalImportURL, restoreImportAliases } from './importResponse'

describe('canonical import response', () => {
  it('preserves query options and restores references without copying drafts', () => {
    expect(canonicalImportURL('/import')).toBe('/import?response_format=canonical-v1')
    expect(canonicalImportURL('/import?project_id=a')).toBe('/import?project_id=a&response_format=canonical-v1')
    const script = { name: 'Orders', actions: [{ body: 'large body' }] }
    const wire = { response_format: 'canonical-v1', scripts: [script], primary_script_index: 0, stats: { count: 1 },
      result: { format: 'har' }, result_aliases: { stats: 'stats', unsupported: 'diagnostics' },
      unsupported_from_diagnostics: true, diagnostics: [{ element: 'Timing', severity: 'info', detail: 'inferred', count: 4, locations: ['entry 1', 'entry 2'] }] }
    const got = restoreImportAliases<Record<string, unknown>>(wire)
    expect(got.script).toBe(script)
    expect((got.result as Record<string, unknown>).stats).toBe(wire.stats)
    expect(got.unsupported_elements).toEqual(['Timing (4 occurrences; examples: entry 1, entry 2) [info]: inferred'])
    expect(wire).not.toHaveProperty('script')
    expect(wire.result).not.toHaveProperty('stats')
  })

  it('handles absent primary drafts, unknown formats, and legacy controllers', () => {
    const legacy = { script: { name: 'old' } }
    expect(restoreImportAliases(legacy)).toBe(legacy)
    for (const index of [-1, 1, 0.5, undefined]) {
      expect(restoreImportAliases({ response_format: 'canonical-v1', scripts: [{}], primary_script_index: index })).not.toHaveProperty('script')
    }
    expect(restoreImportAliases(null)).toBeNull()
  })

  it('restores each collection child independently and preserves failed members', () => {
    const first = { name: 'first' }; const second = { name: 'second' }
    const got = restoreImportAliases({ archive_collection: true, archives: [
      { response: { response_format: 'canonical-v1', scripts: [first, second], primary_script_index: 1 } },
      { error: 'invalid archive' },
    ] })
    expect(got.archives[0].response).toHaveProperty('script', second)
    expect(got.archives[1]).toEqual({ error: 'invalid archive' })
  })

  it('restores Salesforce summary previews and converter aliases to the same draft', () => {
    const primary = { name: 'Primary' }; const native = { name: 'Native', actions: [{ body: 'large body' }] }
    const wire = { response_format: 'canonical-v1', scripts: [primary, native], primary_script_index: 0,
      salesforce_preview_script_index: 1, summary: { salesforce_review: { detected: true, native_preview: { native_path_domain: 'final_native' } } },
      result: { format: 'archive' }, result_aliases: { summary: 'summary' } }
    const got = restoreImportAliases<Record<string, unknown>>(wire)
    const summary = got.summary as typeof wire.summary & { salesforce_review: { native_preview: { script: unknown } } }
    expect(summary.salesforce_review.native_preview.script).toBe(native)
    expect((got.result as Record<string, unknown>).summary).toBe(summary)
    expect(wire.summary.salesforce_review.native_preview).not.toHaveProperty('script')
    expect(got.script).toBe(primary)
  })

  it('preserves distinct Salesforce previews and ignores invalid references', () => {
    const draft = { name: 'Primary' }; const preview = { name: 'Distinct' }
    const wire = { response_format: 'canonical-v1', scripts: [draft], salesforce_preview_script_index: 0,
      summary: { salesforce_review: { native_preview: { script: preview } } } }
    expect(restoreImportAliases(wire).summary.salesforce_review.native_preview.script).toBe(preview)
    for (const index of [-1, 1, 0.5, undefined]) {
      const result = restoreImportAliases({ ...wire, salesforce_preview_script_index: index,
        summary: { salesforce_review: { native_preview: { native_path_domain: 'final_native' } } } })
      expect(result.summary.salesforce_review.native_preview).not.toHaveProperty('script')
    }
  })
})
