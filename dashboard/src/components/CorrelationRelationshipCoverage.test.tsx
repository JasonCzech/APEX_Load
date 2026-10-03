// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { CorrelationRelationshipCoverage as Coverage } from '../types/scripts'
import CorrelationRelationshipCoverage from './CorrelationRelationshipCoverage'

const coverage: Coverage = { complete: false, producer_count: 1, consumer_count: 2, missing_producers: 1, unavailable_extractors: 1, missing_consumers: 1, state_counts: { producer_missing: 1, consumer_missing: 1 }, details_complete: true, details_omitted: 0, detail_limit: 256, relationships: [{ rule_id: 'not-rendered-private-value', producer_step_id: 0, consumer_step_id: 1, state: 'producer_missing', producer_observed: false, extractor_available: false, consumer_observed: false }] }

describe('correlation relationship coverage', () => {
  let container: HTMLDivElement, root: Root
  beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
  afterEach(() => { act(() => root.unmount()); container.remove() })
  const render = (value?: Coverage) => act(() => { root.render(<CorrelationRelationshipCoverage coverage={value} />) })

  it('reports simultaneous missing producer, extractor and consumer evidence without exposing rule values', () => {
    render(coverage)
    expect(container.textContent).toContain('Relationship inspection incomplete')
    expect(container.textContent).toContain('1 missing replay producer · 1 unavailable replay extracted value · 1 missing replay consumer')
    expect(container.textContent).toContain('Producer step ID 0 → consumer step ID 1')
    expect(container.textContent).toContain('Producer absent · Extracted value unavailable · Consumer absent')
    expect(container.textContent).toContain('not successful extraction, correlation, or replay')
    expect(container.innerHTML).not.toContain('not-rendered-private-value')
  })

  it('keeps zero detections and omitted detail coverage unresolved even with no rows', () => {
    render({ ...coverage, producer_count: 0, consumer_count: 300, relationships: [], details_complete: false, details_omitted: 300 })
    expect(container.textContent).toContain('No producer bindings detected. This does not prove login or session bootstrap coverage')
    expect(container.textContent).toContain('300 relationship rows not shown')
    expect(container.textContent).toContain('300 recorded consumer links')
  })

  it('omits invalid states and numeric identities without exposing their source values', () => {
    render({ ...coverage, relationships: [{ ...coverage.relationships[0], state: 'private-state' as never, producer_step_id: Number.NaN, consumer_step_id: -1 }] })
    expect(container.textContent).toContain('Relationship inspection incomplete')
    expect(container.textContent).toContain('1 relationship rows not shown')
    expect(container.querySelector('details')).toBeNull()
    expect(container.innerHTML).not.toContain('private-state')
  })

  it('bounds retained rows and pages details without hiding aggregate counts', () => {
    render({ ...coverage, producer_count: 1000, consumer_count: 1000, relationships: Array.from({ length: 260 }, (_, index) => ({ ...coverage.relationships[0], consumer_step_id: index })), details_complete: false, details_omitted: 740 })
    const details = container.querySelector('details')!
    expect(details.querySelectorAll('li')).toHaveLength(20)
    expect(container.textContent).toContain('744 relationship rows not shown')
    expect(container.textContent).toContain('1000 recorded consumer links')
    const next = [...container.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Next relationships')!
    act(() => { next.click() })
    expect(details.textContent).toContain('21–40 of 256 relationships')
    expect(details.textContent).toContain('consumer step ID 20')
  })
  it('resets a paged report when cleared and replaced, including reusing the old report object', () => {
    const oldReport = { ...coverage, relationships: Array.from({ length: 22 }, (_, index) => ({ ...coverage.relationships[0], consumer_step_id: index })) }
    const next = () => [...container.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Next relationships')!
    render(oldReport)
    act(() => { next().click() })
    expect(container.querySelector('details')!.textContent).toContain('21–22 of 22 relationships')
    render(undefined)
    expect(container.querySelector('section')).toBeNull()
    render(oldReport)
    expect(container.querySelector('details')!.textContent).toContain('1–20 of 22 relationships')
    act(() => { next().click() })
    const newReport = { ...oldReport, relationships: oldReport.relationships.map(row => ({ ...row, producer_step_id: 99 })) }
    render(newReport)
    expect(container.querySelector('details')!.textContent).toContain('1–20 of 22 relationships')
    expect(container.querySelector('details')!.textContent).toContain('Producer step ID 99 → consumer step ID 0')
  })

  it.each([
    { ...coverage, complete: true, relationships: [null] },
    { ...coverage, complete: true, relationships: {} },
    { ...coverage, complete: true, relationships: [{ ...coverage.relationships[0], state: 'private-state' }] },
    { ...coverage, complete: true, state_counts: { producer_missing: 'private-value' } },
    { ...coverage, complete: true, state_counts: { unknown_future_state: 1 } },
    { ...coverage, complete: true, state_counts: { unknown_future_state: 0 } },
    ...[null, [], { toString: 1, valueOf: 1 }, { toString: null }].map(state => ({ ...coverage, complete: true, relationships: [{ ...coverage.relationships[0], state }] })),
    ...[{ producer_step_id: {} }, { consumer_step_id: -1 }, { producer_observed: 'true' }, { extractor_available: null }, { consumer_observed: [] }].map(invalid => ({ ...coverage, complete: true, relationships: [{ ...coverage.relationships[0], ...invalid }] })),
    'private-report',
  ])('handles malformed coverage without crashing or claiming completeness', malformed => {
    render(malformed as never)
    expect(container.textContent).toContain('Relationship inspection incomplete')
    expect(container.textContent).toContain('Relationship details are malformed or unavailable')
    expect(container.textContent).not.toContain('Relationship inspection complete.')
    for (const value of ['private-state', 'private-value', 'private-report', 'unknown_future_state', 'private_state']) expect(container.innerHTML).not.toContain(value)
  })

  it('retains complete inspection for supported numeric state counts, including optional zero counts', () => {
    render({ ...coverage, complete: true, producer_count: 1, consumer_count: 1, missing_producers: 0, unavailable_extractors: 0, missing_consumers: 0, state_counts: { producer_missing: 0, producer_unchanged: 1 }, relationships: [{ ...coverage.relationships[0], state: 'producer_unchanged', producer_observed: true, extractor_available: true, consumer_observed: true }] })
    expect(container.textContent).toContain('Relationship inspection complete.')
    expect(container.textContent).toContain('Producer value unchanged: 1')
    expect(container.textContent).not.toContain('malformed or unavailable')
    expect(container.textContent).not.toContain('Relationship inspection incomplete.')
  })

})
