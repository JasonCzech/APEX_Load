import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { BrowserReplayEvent } from '../types/replay'
import { ReplayExtractions } from './ReplayExtractions'

const base: BrowserReplayEvent = {
  phase: 'action', action_name: 'Request', action_type: 'request', target: '/',
  success: true, started_at: '', completed_at: '', duration_ms: 1,
}

describe('ReplayExtractions', () => {
  it('shows the omission warning when no extraction survived', () => {
    const html = renderToStaticMarkup(<ReplayExtractions event={{ ...base, protocol_evidence: { concurrent_group_extractions_omitted: true } }} />)
    expect(html).toContain('Extractions')
    expect(html).toContain('Some extractions were omitted')
    expect(html).toContain('displayed values may be incomplete')
  })

  it('shows retained values and a valid omitted count', () => {
    const html = renderToStaticMarkup(<ReplayExtractions event={{ ...base, extractions: { customer: 'visible' }, protocol_evidence: { concurrent_group_extractions_omitted: true, concurrent_group_extractions_omitted_count: 2 } }} />)
    expect(html).toContain('2 extractions were omitted')
    expect(html).toContain('customer')
    expect(html).toContain('visible')
  })

  it('does not claim omissions without a true receipt and ignores invalid counts', () => {
    expect(renderToStaticMarkup(<ReplayExtractions event={base} />)).toBe('')
    const html = renderToStaticMarkup(<ReplayExtractions event={{ ...base, protocol_evidence: { concurrent_group_extractions_omitted: true, concurrent_group_extractions_omitted_count: -1 } }} />)
    expect(html).toContain('Some extractions were omitted')
    expect(html).not.toContain('-1')
  })

  it('shows failed-attempt values separately and reports omitted values even when none survive', () => {
    const html = renderToStaticMarkup(<ReplayExtractions event={{ ...base, extractions: { applied: 'one' }, unapplied_extractions: { failed: 'two' }, unapplied_extractions_omitted_count: 1 }} />)
    expect(html).toContain('Unapplied extractions')
    expect(html).toContain('not applied to the session variables')
    expect(html).toContain('failed')
    expect(html).toContain('1 extraction was omitted')
    const omittedOnly = renderToStaticMarkup(<ReplayExtractions event={{ ...base, unapplied_extractions_omitted_count: 2 }} />)
    expect(omittedOnly).toContain('2 extractions were omitted')
  })

  it('renders streamed child diagnostics and omission receipts', () => {
    const html = renderToStaticMarkup(<ReplayExtractions event={{ ...base, streamed_children: [{ action_name: 'Child', success: false, error_message: 'failed', extractions: { accepted: 'one' }, unapplied_extractions: { rejected: 'two' }, unapplied_extractions_omitted_count: 1 }], streamed_children_omitted_count: 2 }} />)
    expect(html).toContain('Child')
    expect(html).toContain('accepted')
    expect(html).toContain('rejected')
    expect(html).toContain('1 unapplied extraction was omitted')
    expect(html).toContain('2 streamed child events were omitted')
  })
})
