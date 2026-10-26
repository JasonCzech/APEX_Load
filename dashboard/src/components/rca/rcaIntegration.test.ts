import { describe, expect, it } from 'vitest'

import type { MetricsHistoryPoint, RunArchiveErrorPattern } from '../../types'
import type { ReportRootCauseSummary } from '../../types/rca'
import { timelineOverlayBands } from '../runReport/timelineOverlays'
import { rcaHypothesisAnnotation, rcaHypothesisFinding, rcaHypothesisNoteDraft } from './rcaActions'
import {
  normalizeRcaAssessment,
  rcaErrorPatternAnchor,
  rcaLargestChanges,
  rcaPatternHypotheses,
  rcaSummaryLine,
  rcaTimelineOverlays,
} from './rcaModel'
import { rcaTestAssessment, rcaTestSummary } from './rcaTestFixture'
import { rcaSummaryWithLatest, rcaSummaryWithResponse } from './useRootCauseSummary'

const assessment = () => normalizeRcaAssessment(rcaTestAssessment())

describe('rcaSummaryLine', () => {
  const line = (changes: Partial<ReportRootCauseSummary>) => rcaSummaryLine(rcaTestSummary(changes))
  it('names the leading hypothesis and invites investigating it', () => {
    expect(line({})).toMatchObject({ text: '#1 Resource pressure (medium confidence)', tone: 'warning', action: 'investigate', actionLabel: 'Investigate root cause', pending: false })
  })
  it('says no cause is supported, with the changes to review', () => {
    expect(line({ outcome: 'no_supported_hypothesis', top_hypothesis: undefined, anomaly_count: 1 })).toMatchObject({ text: 'No cause supported · 1 change detected', action: 'review' })
    expect(line({ outcome: 'no_supported_hypothesis', top_hypothesis: undefined, anomaly_count: 12 })?.text).toBe('No cause supported · 12 changes detected')
  })
  it('says when evidence was too thin', () => {
    expect(line({ outcome: 'insufficient_evidence', top_hypothesis: undefined })).toMatchObject({ text: 'Too little evidence to judge a cause', action: 'open' })
  })
  it('states the analysis when there is no verdict', () => {
    const empty = { analysis_id: undefined, outcome: undefined, top_hypothesis: undefined }
    expect(line({ ...empty, state: 'queued' })).toMatchObject({ text: 'Analysis queued', pending: true })
    expect(line({ ...empty, state: 'running' })).toMatchObject({ text: 'Analysis running', pending: true })
    expect(line({ ...empty, state: 'failed' })).toMatchObject({ text: 'Analysis failed', tone: 'danger' })
    expect(line({ ...empty, state: 'cancelled' })?.text).toBe('Analysis cancelled')
    expect(line({ ...empty, state: 'none' })?.text).toBe('No analysis yet')
    expect(line({ ...empty, state: 'not_enabled', presented: false })).toMatchObject({ action: 'none', text: 'Root-cause analysis is not enabled for this project' })
    expect(rcaSummaryLine(undefined)).toBeNull()
  })
  it('notes a re-analysis, a failed newer analysis and a changed archive beside the verdict', () => {
    const latest = { analysis_id: 'rca-3', revision: 3, created_at: '2026-10-05T12:20:00Z' }
    expect(line({ state: 'running', latest: { ...latest, state: 'running' } })?.text).toBe('#1 Resource pressure (medium confidence) · re-analysis running')
    expect(line({ state: 'failed', latest: { ...latest, state: 'failed' } })?.text).toBe('#1 Resource pressure (medium confidence) · latest analysis failed')
    expect(line({ archive_changed: true })?.text).toBe('#1 Resource pressure (medium confidence) · archive changed since')
  })
})

describe('rcaSummaryWithResponse', () => {
  it('turns a completed newer analysis into the verdict', () => {
    const pending = rcaTestSummary({ state: 'queued', analysis_id: undefined, outcome: undefined, top_hypothesis: undefined, latest: { analysis_id: 'rca-1', revision: 2, state: 'queued', created_at: '2026-10-05T12:10:00Z' } })
    const next = rcaSummaryWithResponse(pending, { analysis_id: 'rca-1', revision: 2, state: 'completed', presented: true, origin: 'automatic', completed_at: '2026-10-05T12:10:05Z', assessment: assessment() })
    expect(next).toMatchObject({ state: 'completed', analysis_id: 'rca-1', outcome: 'supported', origin: 'automatic', hypothesis_count: 2, anomaly_count: 2, latest: undefined })
    expect(next.top_hypothesis).toMatchObject({ id: 'hypothesis-cpu', title: 'Resource pressure', confidence: 'medium' })
  })
  it('keeps the verdict beside a failed newer analysis', () => {
    const summary = rcaTestSummary({ state: 'running', latest: { analysis_id: 'rca-3', revision: 3, state: 'running', created_at: '2026-10-05T12:20:00Z' } })
    const next = rcaSummaryWithResponse(summary, { analysis_id: 'rca-3', revision: 3, state: 'failed', presented: true })
    expect(next).toMatchObject({ state: 'failed', analysis_id: 'rca-1', outcome: 'supported' })
    expect(next.latest?.state).toBe('failed')
  })
})

describe('rcaSummaryWithLatest', () => {
  const summary = rcaTestSummary()
  it('takes a newer analysis the panel started as the newest state', () => {
    const next = rcaSummaryWithLatest(summary, { analysis_id: 'rca-3', revision: 3, state: 'queued', presented: true, origin: 'requested', created_at: '2026-10-05T12:20:00Z' })
    expect(next).toMatchObject({ state: 'queued', analysis_id: 'rca-1', outcome: 'supported', latest: { analysis_id: 'rca-3', revision: 3, state: 'queued', origin: 'requested' } })
    expect(rcaSummaryLine(next)?.text).toBe('#1 Resource pressure (medium confidence) · re-analysis queued')
  })
  it('takes the panel\'s newer completed analysis as the verdict', () => {
    const next = rcaSummaryWithLatest(summary, { analysis_id: 'rca-3', revision: 3, state: 'completed', presented: true, origin: 'requested', assessment: assessment() })
    expect(next).toMatchObject({ state: 'completed', analysis_id: 'rca-3', revision: 3, origin: 'requested', latest: undefined })
  })
  it('changes nothing for the same or an older analysis', () => {
    expect(rcaSummaryWithLatest(summary, { analysis_id: 'rca-1', revision: 2, state: 'completed', presented: true, assessment: assessment() })).toBe(summary)
    const newer = rcaTestSummary({ state: 'running', latest: { analysis_id: 'rca-4', revision: 4, state: 'running', created_at: '2026-10-05T12:30:00Z' } })
    expect(rcaSummaryWithLatest(newer, { analysis_id: 'rca-3', revision: 3, state: 'completed', presented: true, assessment: assessment() })).toBe(newer)
    expect(rcaSummaryWithLatest(summary, { state: 'none', presented: true })).toBe(summary)
    const off = rcaTestSummary({ state: 'not_enabled', presented: false })
    expect(rcaSummaryWithLatest(off, { analysis_id: 'rca-3', revision: 3, state: 'queued', presented: false })).toBe(off)
  })
})

describe('rcaLargestChanges', () => {
  it('ranks by peak z, so changes in different units compare', () => {
    const a = assessment()
    expect(rcaLargestChanges(a).map(x => x.id)).toEqual(['anomaly-sym', 'anomaly-cpu'])
    expect(rcaLargestChanges(a, 1).map(x => x.id)).toEqual(['anomaly-sym'])
    expect(rcaLargestChanges(a, 0)).toEqual([])
  })
})

describe('rcaPatternHypotheses', () => {
  const pattern = (changes: Partial<RunArchiveErrorPattern>) => ({ signature: 's', sample: 'HTTP 503', count: 3, first_seen: '2026-10-05T12:04:00Z', latest_seen: '2026-10-05T12:05:00Z', actions: ['Checkout'], ...changes }) as RunArchiveErrorPattern
  it('finds the hypotheses whose symptom the pattern names inside their span', () => {
    expect(rcaPatternHypotheses(assessment(), pattern({})).map(h => h.id)).toEqual(['hypothesis-cpu', 'hypothesis-gc'])
  })
  it('attributes nothing to a pattern outside the span, about another action, or naming none', () => {
    expect(rcaPatternHypotheses(assessment(), pattern({ first_seen: '2026-10-05T13:00:00Z', latest_seen: '2026-10-05T13:05:00Z' }))).toEqual([])
    expect(rcaPatternHypotheses(assessment(), pattern({ actions: ['Login'] }))).toEqual([])
    expect(rcaPatternHypotheses(assessment(), pattern({ actions: [] }))).toEqual([])
  })
})

describe('rcaErrorPatternAnchor', () => {
  it('is a stable element ID per signature', () => {
    expect(rcaErrorPatternAnchor('checkout-503')).toBe(rcaErrorPatternAnchor('checkout-503'))
    expect(rcaErrorPatternAnchor('checkout-503')).not.toBe(rcaErrorPatternAnchor('login-401'))
    expect(rcaErrorPatternAnchor('checkout-503')).toMatch(/^error-pattern-[0-9a-f]{8}$/)
  })
})

describe('rcaTimelineOverlays', () => {
  it('marks cited anomalies under their hypothesis, uncited ones, invalid spans and load steps', () => {
    const overlays = rcaTimelineOverlays(assessment(), [{ at: Date.parse('2026-10-05T12:02:00Z'), label: 'Load step to 20 VUsers' }])
    const byId = Object.fromEntries(overlays.map(o => [o.id, o]))
    expect(byId['anomaly-anomaly-sym']).toMatchObject({ tone: 'danger', ref: 'hypothesis:hypothesis-cpu' })
    expect(byId['anomaly-anomaly-sym'].label).toContain('Symptom: Checkout · p95 latency')
    expect(byId['anomaly-anomaly-cpu']).toMatchObject({ tone: 'warning', ref: 'hypothesis:hypothesis-cpu' })
    expect(overlays.filter(o => o.kind === 'validity')).toHaveLength(1)
    expect(overlays.filter(o => o.kind === 'phase')[0].label).toBe('Load step to 20 VUsers')
  })
  it('opens an uncited change by its window', () => {
    const a = assessment()
    const overlays = rcaTimelineOverlays({ ...a, hypotheses: [] })
    expect(overlays.filter(o => o.kind === 'anomaly').every(o => o.tone === 'accent' && o.ref?.startsWith('window:'))).toBe(true)
  })
})

describe('timelineOverlayBands', () => {
  // Samples carry their interval's end; 10 s intervals from 12:00:10.
  const data = Array.from({ length: 12 }, (_, i) => ({ time: `t${i}`, timestampMs: Date.parse('2026-10-05T12:00:10Z') + i * 10_000 }) as MetricsHistoryPoint)
  const at = (iso: string) => Date.parse(iso)
  it('places spans and instants on the sample whose interval starts nearest', () => {
    const bands = timelineOverlayBands([
      { id: 'span', kind: 'anomaly', start: at('2026-10-05T12:00:30Z'), end: at('2026-10-05T12:01:00Z'), label: 'span', tone: 'danger' },
      { id: 'step', kind: 'phase', start: at('2026-10-05T12:00:20Z'), label: 'step', tone: 'muted' },
    ], data)
    expect(bands.map(b => [b.overlay.id, b.x1, b.x2])).toEqual([['span', 't3', 't6'], ['step', 't2', undefined]])
  })
  it('uses distinct numeric coordinates when formatted clock labels repeat', () => {
    const repeated = data.map(point => ({ ...point, time: 'same clock label' }))
    const bands = timelineOverlayBands([
      { id: 'span', kind: 'anomaly', start: at('2026-10-05T12:00:30Z'), end: at('2026-10-05T12:01:00Z'), label: 'span', tone: 'danger' },
    ], repeated, point => point.timestampMs!)
    expect(bands.map(b => [b.x1, b.x2])).toEqual([[data[3].timestampMs, data[6].timestampMs]])
  })
  it('leaves out what lies outside the samples and cuts spans at the edges', () => {
    const bands = timelineOverlayBands([
      { id: 'before', kind: 'phase', start: at('2026-10-05T11:00:00Z'), label: 'before', tone: 'muted' },
      { id: 'over', kind: 'anomaly', start: at('2026-10-05T11:59:00Z'), end: at('2026-10-05T13:00:00Z'), label: 'over', tone: 'danger' },
    ], data)
    expect(bands.map(b => [b.overlay.id, b.x1, b.x2])).toEqual([['over', 't0', 't11']])
    expect(timelineOverlayBands(undefined, data)).toEqual([])
  })
})

describe('rcaActions', () => {
  it('turns a hypothesis into an analyst finding that says where it came from', () => {
    const a = assessment()
    const finding = rcaHypothesisFinding(a.hypotheses[0], a, 'rca-1', 2)
    expect(finding).toMatchObject({ id: '', title: 'Resource pressure', severity: 'minor' })
    expect(finding.observed).toBe('Resource pressure (medium confidence) explains Checkout · p95 latency.')
    expect(finding.root_cause).toContain('Generated hypothesis #1 of root-cause analysis rca-1 (revision 2)')
    expect(finding.root_cause).toContain('CPU stays below saturation while latency rises')
    expect(finding.evidence).toContain('began before Checkout · p95 latency')
  })
  it('places a timeline note at the earliest symptom onset', () => {
    const a = assessment()
    expect(rcaHypothesisAnnotation(a.hypotheses[0], a)).toMatchObject({ timestamp: '2026-10-05T12:03:20.000Z', label: 'Root cause #1: Resource pressure' })
    expect(rcaHypothesisAnnotation({ ...a.hypotheses[0], symptoms: [], chain: [] }, a)).toBeNull()
  })
  it('drafts a test note with the link that reopens the hypothesis', () => {
    const a = assessment()
    const draft = rcaHypothesisNoteDraft(a.hypotheses[0], a, 'https://apex.example/#/report/run-1?section=diagnostics&hypothesis=hypothesis-cpu')
    expect(draft).toContain('Root cause #1: Resource pressure')
    expect(draft).toContain('- Symptom: Checkout · p95 latency')
    expect(draft.trim().endsWith('hypothesis=hypothesis-cpu')).toBe(true)
  })
})
