import { useEffect, useRef, useState } from 'react'

import type { BaselineOverviewDiff } from './runReportModel'

interface Props {
  compareId: string
  suggestedCompareId?: string
  baselineName?: string
  deltas: BaselineOverviewDiff
  loading?: boolean
  error?: string
  onCompareChange: (id: string) => void
  onPinBaseline: () => Promise<boolean>
}

function DeltaCell({ label, value }: { label: string; value?: { label: string; tone: string } }) {
  return (
    <div className={`run-report-compare-delta run-report-delta-${value?.tone || 'neutral'}`}>
      <span>{label}</span>
      <strong>{value?.label || 'n/a'}</strong>
    </div>
  )
}

export default function RunReportCompareBar({
  compareId,
  suggestedCompareId,
  baselineName,
  deltas,
  loading,
  error,
  onCompareChange,
  onPinBaseline,
}: Props) {
  const [draft, setDraft] = useState(compareId)
  const [pinning, setPinning] = useState(false)
  const [pinMessage, setPinMessage] = useState('')
  const compareIdRef = useRef(compareId)
  const pinRequestGenerationRef = useRef(0)
  const pinningRef = useRef(false)
  compareIdRef.current = compareId

  useEffect(() => {
    pinRequestGenerationRef.current += 1
    pinningRef.current = false
    setPinning(false)
    setDraft(compareId)
    setPinMessage('')
  }, [compareId])

  const pin = async () => {
    if (pinningRef.current || !compareId) return
    pinningRef.current = true
    const requestCompareId = compareId
    const requestGeneration = ++pinRequestGenerationRef.current
    setPinning(true)
    try {
      const pinned = await onPinBaseline()
      if (pinned && requestGeneration === pinRequestGenerationRef.current && compareIdRef.current === requestCompareId) {
        setPinMessage('Pinned')
      }
    } finally {
      if (requestGeneration === pinRequestGenerationRef.current) {
        pinningRef.current = false
        setPinning(false)
      }
    }
  }

  return (
    <section className="run-report-panel run-report-compare">
      <div className="run-report-section-heading">
        <div>
          <h2>Baseline Compare</h2>
          <p>{baselineName || (compareId ? `Comparing against ${compareId}` : 'No baseline selected')}</p>
        </div>
      </div>
      <div className="run-report-compare-controls">
        <label>
          <span>Baseline run id</span>
          <input value={draft} onChange={event => setDraft(event.currentTarget.value)} placeholder={suggestedCompareId || 'run id'} />
        </label>
        <button className="btn btn-sm btn-secondary" type="button" onClick={() => onCompareChange(draft.trim())}>
          Apply
        </button>
        {suggestedCompareId && !compareId && (
          <button className="btn btn-sm btn-ghost" type="button" onClick={() => onCompareChange(suggestedCompareId)}>
            Use pinned
          </button>
        )}
        <button className="btn btn-sm btn-primary" type="button" onClick={() => void pin()} disabled={pinning || !compareId}>
          {pinning ? 'Pinning...' : 'Pin baseline'}
        </button>
        {pinMessage && <span role="status">{pinMessage}</span>}
      </div>
      {loading && <p role="status">Loading baseline...</p>}
      {error && <p className="run-report-error">{error}</p>}
      <div className="run-report-compare-deltas">
        <DeltaCell label="Avg latency" value={deltas.avgLatency} />
        <DeltaCell label="P95" value={deltas.p95} />
        <DeltaCell label="Error rate" value={deltas.errorRate} />
        <DeltaCell label="Peak TPS" value={deltas.peakTps} />
        <DeltaCell label="Peak VUsers" value={deltas.peakVusers} />
      </div>
    </section>
  )
}
