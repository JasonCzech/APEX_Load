// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { SourceReview } from '../types/scripts'
import type { SalesforceCorrelationCoverage, SalesforceNativePreview } from '../types/scriptImports'
import SalesforceCorrelationReview from './SalesforceCorrelationReview'

const mapping = { id: 'original', source_file: 'capture.har', start_line: 3, target_path: 'actions[3]', status: 'converted' }
const reference = { har_entry_id: 12, original_action_path: mapping.target_path, source_mapping_id: mapping.id, source_file: mapping.source_file, native_action_path: 'actions[0].params.actions[1]' }
const coverage: SalesforceCorrelationCoverage = { complete: true, details_complete: true, discovered_correlations: 1, observed_consumer_references: 1, import_matched_producer_correlations: 1, import_unmatched_producer_correlations: 0, import_matched_consumer_references: 1, import_unmatched_consumer_references: 0, source_review_available: true, source_linked_producer_correlations: 1, source_unlinked_producer_correlations: 0, source_linked_consumer_references: 1, source_unlinked_consumer_references: 0, native_mapped_producer_correlations: 1, native_unmapped_producer_correlations: 0, native_mapped_consumer_references: 1, native_unmapped_consumer_references: 0, pre_applied_correlations: 1, manual_correlations: 0, retained_correlations: 1, omitted_correlations: 0, retained_consumer_references: 1, omitted_consumer_references: 0, correlation_detail_limit: 2048, consumer_reference_detail_limit: 2048, detail_byte_limit: 262144 }
const preview: SalesforceNativePreview = { native_path_domain: 'final_native', correlation_coverage: coverage, correlations: [{ name: 'private-name', source: 'private-source', expression: 'private-value', found_in_seq: 4, used_in_seqs: [9], pre_applied: true, producer_reference: reference, consumer_references: [reference] }] }

describe('captured correlation source review', () => {
  let container: HTMLDivElement, root: Root
  beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
  afterEach(() => { act(() => root.unmount()); container.remove() })
  const render = (value = preview, review: SourceReview = { target_domain: 'original_import', mappings: [mapping] }, onViewSource = vi.fn()) => { act(() => { root.render(<SalesforceCorrelationReview preview={value} sourceReview={review} onViewSource={onViewSource} />) }); return onViewSource }
  const button = (label: string) => [...container.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent === label)!

  it('shows three independent reference coverage levels and navigates exact original mappings without exposing captured values', () => {
    const navigate = render()
    expect(container.textContent).toContain('Original import matches')
    expect(container.textContent).toContain('Exact source links')
    expect(container.textContent).toContain('Native paths at import')
    expect(container.textContent).toContain('Original import path: actions[3]')
    expect(container.textContent).toContain('Native path at import: actions[0].params.actions[1]')
    expect(container.textContent).toContain('Draft replacement does not validate login, session freshness, or replay')
    for (const value of ['private-name', 'private-source', 'private-value']) expect(container.innerHTML).not.toContain(value)
    act(() => { button('View producer source mapping').click(); button('View consumer 1 source mapping').click() })
    expect(navigate.mock.calls).toEqual([[mapping], [mapping]])
  })

  it.each(['native_preview', 'unknown', ''])('does not show unfinalized native paths for %s domain', domain => {
    render({ ...preview, native_path_domain: domain })
    expect(container.textContent).not.toContain('Native path at import:')
    expect(container.textContent).not.toContain('actions[0].params.actions[1]')
    expect(container.textContent).toContain('Native path coverage is unavailable after final transformations')
  })

  it('rejects duplicate mapping IDs, mismatched paths/files and ordinal guesses', () => {
    const navigate = vi.fn()
    for (const mappings of [[mapping, { ...mapping, target_path: 'actions[9]', source_file: 'other.har' }], [{ ...mapping, source_file: 'other.har' }], [{ ...mapping, target_path: 'actions[4]' }], [{ ...mapping, id: 'ordinal-12' }]]) {
      render(preview, { mappings }, navigate)
      expect(container.querySelectorAll('button')).toHaveLength(0)
      expect(container.textContent).toContain('Exact source link unavailable')
    }
    expect(navigate).not.toHaveBeenCalled()
  })

  it('keeps incomplete counts and omitted details visible with no rows', () => {
    render({ correlation_coverage: { ...coverage, complete: false, details_complete: false, catalog_limited: true, source_review_available: false, discovered_correlations: 5, import_unmatched_producer_correlations: 4, source_unlinked_consumer_references: 7, retained_correlations: 0, omitted_correlations: 5, retained_consumer_references: 0, omitted_consumer_references: 8 }, correlations: [] })
    expect(container.textContent).toContain('Reference inspection incomplete')
    expect(container.textContent).toContain('4 unmatched')
    expect(container.textContent).toContain('7 unavailable')
    expect(container.textContent).toContain('5 correlation rows not shown')
    expect(container.textContent).toContain('8 consumer references not shown')
    expect(container.textContent).toContain('Source mappings are unavailable')
    expect(container.textContent).toContain('Missing links remain unresolved')
  })

  it('does not turn empty detection into session bootstrap proof or legacy sequences into source links', () => {
    render({ correlation_coverage: { ...coverage, discovered_correlations: 0, observed_consumer_references: 0 }, correlations: [] })
    expect(container.textContent).toContain('No correlations detected. This does not prove login or session bootstrap coverage')
    render({ correlations: [{ name: 'capture-name', source: 'body', expression: 'capture-secret', found_in_seq: 4, used_in_seqs: [9] }] })
    expect(container.textContent).toContain('Reference coverage was not retained')
    expect(container.querySelectorAll('button')).toHaveLength(0)
    expect(container.textContent).not.toContain('capture-secret')
  })

  it('pages correlation and consumer details while retaining full aggregate counts', () => {
    const rows = Array.from({ length: 22 }, () => ({ ...preview.correlations![0], consumer_references: Array.from({ length: 22 }, () => reference) }))
    render({ ...preview, correlations: rows, correlation_coverage: { ...coverage, discovered_correlations: 22, observed_consumer_references: 484 } })
    expect(container.querySelectorAll('article')).toHaveLength(20)
    act(() => { button('Next correlations').click() })
    expect(container.querySelectorAll('article')).toHaveLength(2)
    expect(container.textContent).toContain('21–22 of 22 correlations')
    const row = container.querySelector('article')!
    expect(row.querySelectorAll('.salesforce-correlation-reference')).toHaveLength(21)
    act(() => { [...row.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent === 'Next consumer references')!.click() })
    expect(row.querySelectorAll('.salesforce-correlation-reference')).toHaveLength(3)
    expect(row.textContent).toContain('Consumer 22')
    expect(container.textContent).toContain('484 consumer request references')
  })
  it('starts replacement correlation and consumer reports on their first page', () => {
    const makePreview = (entry: number) => ({ ...preview, correlations: Array.from({ length: 22 }, () => ({ ...preview.correlations![0], producer_reference: { ...reference, har_entry_id: entry }, consumer_references: Array.from({ length: 22 }, () => ({ ...reference, har_entry_id: entry })) })) })
    const oldPreview = makePreview(12)
    render(oldPreview)
    act(() => { button('Next correlations').click() })
    expect(container.textContent).toContain('21–22 of 22 correlations')
    render(makePreview(33))
    expect(container.textContent).toContain('1–20 of 22 correlations')
    expect(container.querySelector('article')!.textContent).toContain('HAR entry: 33')
    const consumerNext = () => [...container.querySelector('article')!.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent === 'Next consumer references')!
    act(() => { consumerNext().click() })
    expect(container.querySelector('article')!.textContent).toContain('Consumer references 21–22 of 22')
    render(makePreview(44))
    expect(container.querySelector('article')!.textContent).toContain('Consumer references 1–20 of 22')
    expect(container.querySelector('article')!.textContent).toContain('Consumer 1HAR entry: 44')
    act(() => { button('Next correlations').click() })
    render({ correlations: [] })
    render(oldPreview)
    expect(container.textContent).toContain('1–20 of 22 correlations')
  })

  it.each([
    { ...preview, correlations: [null] },
    { ...preview, correlations: {} },
    { ...preview, correlations: [{ consumer_references: {} }] },
    { ...preview, correlations: [{ consumer_references: [null], producer_reference: { har_entry_id: 'private-id' } }] },
    { ...preview, correlation_coverage: { ...coverage, observed_consumer_references: 'private-count' } },
    'private-preview',
  ])('handles malformed persisted correlation details without crashing or claiming completeness', malformed => {
    render(malformed as never)
    expect(container.textContent).toContain('Reference inspection incomplete')
    expect(container.textContent).toContain('Correlation details are malformed or unavailable')
    expect(container.textContent).not.toContain('Reference inspection complete.')
    for (const value of ['private-id', 'private-count', 'private-preview']) expect(container.innerHTML).not.toContain(value)
  })

  it('bounds consumer references across the whole saved report and exposes omitted details', () => {
    const refs = Array.from({ length: 2048 }, () => reference)
    render({ ...preview, correlations: [{ ...preview.correlations![0], consumer_references: refs }, { ...preview.correlations![0], consumer_references: refs }] })
    const rows = container.querySelectorAll('article')
    expect(rows).toHaveLength(2)
    expect(rows[1].textContent).toContain('No consumer request references retained')
    expect(rows[1].textContent).toContain('2048 consumer references not shown')
    expect(container.textContent).toContain('Detailed evidence incomplete')
  })

})
