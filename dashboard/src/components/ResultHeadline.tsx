import './ResultHeadline.css'
import { useEffect, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import type { RunArchiveReport } from '../types'
import { defaultReportPopulation, formatReportPercent, populationLabels, reportAcceptance, reportPopulationOverview } from './runReport/reportPresentation'
import { formatMs } from './runReport/runReportModel'
import { rcaSummaryLine, rcaTone, rcaValidityLabel } from './rca/rcaModel'
import './rca/RootCause.css'

// Limit archive work when several visible rows request their assessment together.
let pending: Promise<void> = Promise.resolve()
export default function ResultHeadline({ testId }: { testId: string }) {
  const api = useApi()
  const ref = useRef<HTMLDivElement>(null)
  const [report, setReport] = useState<RunArchiveReport | null>(null)
  const [failed, setFailed] = useState(false)
  const [visible, setVisible] = useState(false)
  useEffect(() => {
    if (!ref.current) return
    if (typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver(entries => { if (entries.some(entry => entry.isIntersecting)) { setVisible(true); observer.disconnect() } })
    observer.observe(ref.current)
    return () => observer.disconnect()
  }, [])
  useEffect(() => {
    if (!visible) return
    let cancelled = false
    setReport(null)
    setFailed(false)
    const load = async () => {
      if (cancelled) return
      try {
        const result = await api.getTestArchiveReport(testId, { includeReleaseTrend: false, timeoutMs: 30000 })
        if (!cancelled) setReport(result)
      } catch { if (!cancelled) setFailed(true) }
    }
    pending = pending.then(load, load)
    return () => { cancelled = true }
  }, [api, testId, visible])
  const population = defaultReportPopulation(report)
  const metrics = reportPopulationOverview(report, population)
  // Validity and the root cause sit beside acceptance; a project that does
  // not present root cause shows neither, and unknown validity says nothing.
  const rootCause = report?.root_cause_summary?.state === 'not_enabled' ? null : rcaSummaryLine(report?.root_cause_summary)
  const validity = report?.validity?.verdict && report.validity.verdict !== 'unknown' ? report.validity.verdict : undefined
  return <div ref={ref} className="result-headline">
    <strong>{report ? reportAcceptance(report).status : failed ? 'Assessment unavailable' : 'Assessment pending'}</strong>
    {validity && <span className={`rca-badge rca-badge-${rcaTone(validity)}`}>{rcaValidityLabel(validity)}</span>}
    {report && <span>{populationLabels[population]} · {metrics?.transactions ?? 0} samples · P95 {formatMs(metrics?.p95_ms)} · error rate {formatReportPercent(metrics?.error_pct)}</span>}
    {rootCause && <span className="result-headline-root-cause" data-tone={rootCause.tone}>Root cause: {rootCause.text}</span>}
  </div>
}
