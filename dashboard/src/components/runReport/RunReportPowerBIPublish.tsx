import { useCallback, useEffect, useState } from 'react'

import { ApiError } from '../../hooks/apiClient'
import type { DashboardPowerBIPublish, PowerBIPublishResponse, PowerBIPublishStatusResponse } from '../../types/settings'

interface Props {
  testId: string
  publish: (id: string, options?: { force?: boolean }) => Promise<PowerBIPublishResponse>
  getStatus: (id: string) => Promise<PowerBIPublishStatusResponse>
}

function formatPublishTime(value?: string) {
  if (!value) return ''
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString()
}

function describePublish(publish: DashboardPowerBIPublish) {
  switch (publish.status) {
    case 'complete':
      return `Published ${formatPublishTime(publish.published_at)} (${publish.transaction_rows_pushed} transaction row${publish.transaction_rows_pushed === 1 ? '' : 's'})`
    case 'partial':
      return `Partial publish — ${publish.transaction_rows_pushed}/${publish.transaction_rows_total} transaction rows pushed${publish.error ? `: ${publish.error}` : ''}`
    case 'failed':
      return `Publish failed${publish.error ? `: ${publish.error}` : ''}`
    default:
      return 'Publish in progress'
  }
}

// A 409 from the publish endpoint means the run's rows are already in the
// dataset; the ApiError payload carries the prior publish record, and a
// confirm row then offers the force re-publish path.
export default function RunReportPowerBIPublish({ testId, publish, getStatus }: Props) {
  const [enabled, setEnabled] = useState(false)
  // 'loading' until the first status answer; 'failed' when it could not be read.
  const [statusState, setStatusState] = useState<'loading' | 'loaded' | 'failed'>('loading')
  const [latest, setLatest] = useState<DashboardPowerBIPublish | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirmingForce, setConfirmingForce] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const response = await getStatus(testId)
      setEnabled(response.enabled)
      setLatest(response.publishes[0] || null)
      setStatusState('loaded')
    } catch {
      setStatusState('failed')
    }
  }, [getStatus, testId])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const runPublish = async (force: boolean) => {
    setBusy(true)
    setError('')
    setConfirmingForce(false)
    try {
      const response = await publish(testId, { force })
      setLatest(response.publish)
    } catch (cause) {
      const conflictPayload = cause instanceof ApiError && cause.status === 409
        ? (cause.payload as { publish?: DashboardPowerBIPublish } | undefined)
        : undefined
      // Only a duplicate-publish conflict carries the prior publish record;
      // other 409s (e.g. dataset schema drift) must surface as errors, not as
      // the "publish again" confirm.
      if (!force && conflictPayload?.publish) {
        setLatest(conflictPayload.publish)
        setConfirmingForce(true)
      } else {
        setError(cause instanceof Error ? cause.message : 'Publish failed')
        void refresh()
      }
    } finally {
      setBusy(false)
    }
  }

  // The disclosure that holds this panel must never open onto nothing.
  if (!enabled) {
    return <p className="run-report-muted" role="status">
      {statusState === 'loading'
        ? 'Checking Power BI publishing…'
        : statusState === 'failed'
          ? 'Power BI publishing status could not be checked. Try again later.'
          : 'Power BI publishing is not set up for this deployment. An administrator can configure it under Settings › Connections › Power BI.'}
    </p>
  }

  return (
    <section className="run-report-panel run-report-export" data-testid="powerbi-publish-panel">
      <div className="run-report-section-heading">
        <div>
          <h2>Power BI</h2>
          <p>Push this run&apos;s KPIs and per-transaction stats to the shared Power BI dataset.</p>
        </div>
      </div>
      <div className="run-report-export-actions">
        <button
          className="btn btn-sm btn-secondary"
          type="button"
          onClick={() => void runPublish(false)}
          disabled={busy || confirmingForce}
        >
          {busy ? 'Publishing...' : 'Publish to Power BI'}
        </button>
        {confirmingForce && (
          <>
            <button className="btn btn-sm btn-secondary" type="button" onClick={() => void runPublish(true)} disabled={busy}>
              Publish again (adds duplicate rows)
            </button>
            <button className="btn btn-sm btn-secondary" type="button" onClick={() => setConfirmingForce(false)} disabled={busy}>
              Cancel
            </button>
          </>
        )}
      </div>
      {confirmingForce && (
        <p className="run-report-muted" role="status">
          This run is already in the Power BI dataset. Publishing again appends duplicate rows — Power BI reports should keep only the latest publish per run.
        </p>
      )}
      {latest && <p className="run-report-muted" data-testid="powerbi-last-publish">{describePublish(latest)}</p>}
      {error && <p className="run-report-error" role="alert">{error}</p>}
    </section>
  )
}
