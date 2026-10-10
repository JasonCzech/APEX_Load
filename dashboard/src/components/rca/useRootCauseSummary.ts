import { useEffect, useRef, useState } from 'react'

import { useApi } from '../../hooks/useApi'
import type { RcaAnalysisResponse, ReportRootCauseSummary } from '../../types/rca'
import { rcaSignatureLabel } from './rcaModel'

// A queued or running analysis is polled from every 3 s, backing off to
// every 15 s.
const POLL_MS = 3000
const POLL_MAX_MS = 15000

const pendingState = (state?: string) => state === 'queued' || state === 'running'

// rcaSummaryWithResponse is a report's root-cause summary updated with a
// newer response for the analysis it named as its newest: a completed one
// becomes the verdict, a failed or cancelled one the newest state beside the
// verdict it already had.
export function rcaSummaryWithResponse(summary: ReportRootCauseSummary, response: RcaAnalysisResponse): ReportRootCauseSummary {
  if (response.state === 'completed' && response.assessment) {
    const a = response.assessment
    const top = a.hypotheses[0]
    return {
      ...summary,
      state: 'completed',
      analysis_id: response.analysis_id,
      revision: response.revision,
      origin: response.origin,
      outcome: a.outcome,
      validity: a.validity.verdict,
      top_hypothesis: top ? { id: top.id, rank: top.rank, title: rcaSignatureLabel(top.signature), confidence: top.confidence } : undefined,
      hypothesis_count: a.hypotheses.length,
      anomaly_count: a.anomalies.length,
      completed_at: response.completed_at,
      archive_changed: response.archive_changed,
      latest: undefined,
    }
  }
  const latest = summary.latest && summary.latest.analysis_id === response.analysis_id ? { ...summary.latest, state: response.state } : summary.latest
  return { ...summary, state: response.state, latest }
}

// rcaSummaryWithLatest is a report's root-cause summary updated with the
// Root cause panel's analysis when that one is newer, as after an editor
// re-analyzes in Diagnostics: a queued or running one becomes the newest
// state (and is then polled), a completed one the verdict, a failed or
// cancelled one the newest state. An older analysis the panel was pinned to
// by a link changes nothing.
export function rcaSummaryWithLatest(summary: ReportRootCauseSummary | undefined, response: RcaAnalysisResponse | null | undefined): ReportRootCauseSummary | undefined {
  if (!summary || summary.state === 'not_enabled' || !response?.analysis_id || !response.revision) return summary
  if (response.analysis_id === summary.analysis_id) return summary
  const newest = summary.latest?.revision ?? summary.revision ?? 0
  if (response.revision < newest) return summary
  if (response.state === 'completed' && response.assessment) return rcaSummaryWithResponse(summary, response)
  if (response.state === 'none' || response.state === 'completed') return summary
  return { ...summary, state: response.state, latest: { analysis_id: response.analysis_id, revision: response.revision, state: response.state, origin: response.origin, created_at: response.created_at || '' } }
}

// useRootCauseSummary keeps a report's root-cause summary current while the
// run's newest analysis is queued or running: it polls that analysis until
// it ends, then returns the summary updated with it and calls onSettled, so
// the page can reload what depends on the newest analysis.
export function useRootCauseSummary(testId: string, summary: ReportRootCauseSummary | undefined, onSettled?: () => void): ReportRootCauseSummary | undefined {
  const api = useApi()
  const [current, setCurrent] = useState(summary)
  const settled = useRef(onSettled)
  settled.current = onSettled
  const pendingID = summary && pendingState(summary.state) ? summary.latest?.analysis_id : undefined

  useEffect(() => {
    setCurrent(summary)
  }, [summary])

  useEffect(() => {
    if (!summary || !pendingID) return
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    let delay = POLL_MS
    const poll = () => {
      timer = setTimeout(() => void load(), delay)
      delay = Math.min(POLL_MAX_MS, Math.round(delay * 1.5))
    }
    const load = async () => {
      try {
        const next = await api.getRootCauseAnalysis(testId, pendingID, { signal: controller.signal })
        if (controller.signal.aborted) return
        if (pendingState(next.state)) {
          setCurrent(prev => (prev ? rcaSummaryWithResponse(prev, next) : prev))
          poll()
          return
        }
        setCurrent(prev => (prev ? rcaSummaryWithResponse(prev, next) : prev))
        settled.current?.()
      } catch {
        // A failed poll keeps polling, so a brief outage does not leave the
        // analysis shown as pending for good.
        if (!controller.signal.aborted) poll()
      }
    }
    poll()
    return () => {
      controller.abort()
      if (timer) clearTimeout(timer)
    }
  }, [api, pendingID, summary, testId])

  return current
}
