import './ResultHeadline.css'
import { useEffect, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import type { RunArchiveReport } from '../types'
import { defaultReportPopulation, formatReportPercent, populationLabels, reportAcceptance, reportPopulationOverview } from './runReport/reportPresentation'
import { formatMs } from './runReport/runReportModel'

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
  return <div ref={ref} className="result-headline">
    <strong>{report ? reportAcceptance(report).status : failed ? 'Assessment unavailable' : 'Assessment pending'}</strong>
    {report && <span>{populationLabels[population]} · {metrics?.transactions ?? 0} samples · p95 {formatMs(metrics?.p95_ms)} · errors {formatReportPercent(metrics?.error_pct)}</span>}
  </div>
}
