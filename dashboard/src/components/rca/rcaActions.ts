import { request } from '../../hooks/apiClient'
import type { ReportAnnotations, ReportAnnotationsInput, ReportAnnotationsResponse, ReportFinding } from '../../types'
import type { RcaAssessment, RcaHypothesis } from '../../types/rca'
import { formatRcaEffect, formatRcaTime, rcaHypothesisAnomalies, rcaSeriesLabel, rcaSignatureLabel } from './rcaModel'

// The relation words of a hypothesis chain, as the panel prints them.
const RELATIONS: Record<string, string> = {
  precedes: 'began before',
  unordered: 'began at about the same time as',
  load_coincident: 'began with a load step, as did',
}

// rcaHypothesisSentence is a hypothesis in one sentence: its signature, its
// confidence and what it explains.
export function rcaHypothesisSentence(h: RcaHypothesis, assessment: RcaAssessment): string {
  const symptoms = rcaHypothesisAnomalies(h, assessment.anomalies).filter(item => item.role === 'symptom').map(item => rcaSeriesLabel(item.anomaly.series))
  return `${rcaSignatureLabel(h.signature)} (${h.confidence} confidence)${symptoms.length ? ` explains ${symptoms.join('; ')}` : ''}.`
}

// rcaHypothesisFinding turns a generated hypothesis into an analyst finding
// for the report's Findings section: generated evidence an analyst adopts,
// with where it came from, never edited into the generated section itself.
export function rcaHypothesisFinding(h: RcaHypothesis, assessment: RcaAssessment, analysisId: string, revision?: number): ReportFinding {
  const byId = new Map(assessment.anomalies.map(a => [a.id, a]))
  const label = (id: string) => { const a = byId.get(id); return a ? rcaSeriesLabel(a.series) : id }
  const chain = h.chain.map(step => {
    const cause = byId.get(step.from)
    return `${label(step.from)}${cause ? ` (${formatRcaEffect(cause)})` : ''} ${RELATIONS[step.relation] || step.relation.replace(/_/g, ' ')} ${label(step.to)}`
  })
  const locators = h.supporting.map(ref => ref.locator).filter(Boolean)
  return {
    id: '',
    title: rcaSignatureLabel(h.signature),
    severity: h.confidence === 'high' ? 'major' : h.confidence === 'medium' ? 'minor' : 'info',
    observed: rcaHypothesisSentence(h, assessment),
    evidence: [...chain, ...locators].join('\n') || undefined,
    root_cause: `Generated hypothesis #${h.rank} of root-cause analysis ${analysisId}${revision ? ` (revision ${revision})` : ''}. Would disprove it: ${h.falsification || 'no check defined'}`,
    impact: h.symptoms.map(label).join('; ') || undefined,
  }
}

// sameFinding says two findings record the same hypothesis: a generated
// finding names its hypothesis and analysis in its root cause.
const sameFinding = (a: ReportFinding, b: ReportFinding) => a.title === b.title && a.root_cause === b.root_cause

// appendReportFinding adds a finding to a report audience's annotations,
// keeping every other annotation as it stands; a concurrent save is read
// again and retried, at most three times. A save the server kept before it
// refused (the verdict changed meanwhile) is not added twice.
export async function appendReportFinding(testId: string, lens: string, finding: ReportFinding): Promise<ReportAnnotations> {
  const path = `/tests/${encodeURIComponent(testId)}/report/annotations?profile=${encodeURIComponent(lens)}`
  let latest = (await request<ReportAnnotationsResponse>(path)).annotations
  for (let attempt = 0; ; attempt += 1) {
    if (attempt > 0 && (latest.findings || []).some(existing => sameFinding(existing, finding))) return latest
    const body: ReportAnnotationsInput = {
      expected_updated_at: latest.updated_at,
      title: latest.title,
      prepared_by: latest.prepared_by,
      executive_narrative: latest.executive_narrative,
      section_comments: latest.section_comments || {},
      verdict_override: latest.verdict_override || '',
      verdict_explanation: latest.verdict_explanation || '',
      expected_verdict_updated_at: latest.verdict_updated_at,
      findings: [...(latest.findings || []), finding],
    }
    try {
      return (await request<ReportAnnotationsResponse>(path, { method: 'PUT', body: JSON.stringify(body) })).annotations
    } catch (error: unknown) {
      const status = (error as { status?: number })?.status
      if (status !== 409 || attempt >= 2) throw error
      latest = (await request<ReportAnnotationsResponse>(path)).annotations
    }
  }
}

// rcaHypothesisAnnotation is the run-timeline note for a hypothesis, placed
// at its earliest symptom onset (its earliest anomaly when no symptom is
// recorded).
export function rcaHypothesisAnnotation(h: RcaHypothesis, assessment: RcaAssessment): { timestamp: string; label: string; text: string } | null {
  const involved = rcaHypothesisAnomalies(h, assessment.anomalies)
  const symptoms = involved.filter(item => item.role === 'symptom')
  const onsets = (symptoms.length ? symptoms : involved).map(item => Date.parse(item.anomaly.onset)).filter(Number.isFinite)
  if (!onsets.length) return null
  return {
    timestamp: new Date(Math.min(...onsets)).toISOString(),
    label: `Root cause #${h.rank}: ${rcaSignatureLabel(h.signature)}`.slice(0, 256),
    text: rcaHypothesisSentence(h, assessment).slice(0, 2048),
  }
}

// createRunAnnotation adds a note to a run's timeline at the revision the
// notes stand at now.
export async function createRunAnnotation(testId: string, note: { timestamp: string; label: string; text: string }): Promise<void> {
  const path = `/tests/${encodeURIComponent(testId)}/analysis/annotations`
  const current = await request<{ revision: number }>(path)
  await request(path, { method: 'POST', body: JSON.stringify({ expected_revision: current.revision, ...note }) })
}

// rcaHypothesisNoteDraft is a test note's opening text for a hypothesis,
// with the link that reopens it.
export function rcaHypothesisNoteDraft(h: RcaHypothesis, assessment: RcaAssessment, link: string): string {
  const involved = rcaHypothesisAnomalies(h, assessment.anomalies)
  const lines = involved.map(({ anomaly, role }) => `- ${role === 'symptom' ? 'Symptom' : 'Cause'}: ${rcaSeriesLabel(anomaly.series)} ${formatRcaEffect(anomaly)} from ${formatRcaTime(anomaly.onset)}`)
  return [`Root cause #${h.rank}: ${rcaHypothesisSentence(h, assessment)}`, '', ...lines, '', `Would disprove it: ${h.falsification || 'no check defined'}`, '', link].join('\n')
}
