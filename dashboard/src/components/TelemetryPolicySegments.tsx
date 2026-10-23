import { useEffect, useId, useRef, useState } from 'react'
import { ApiError } from '../hooks/apiClient'
import { telemetryPolicySegmentsApi } from '../hooks/api/telemetryPolicySegmentsApi'
import type { TelemetryPolicySegmentReceipt, TelemetryPolicySegmentRequest } from '../types/telemetryPolicy'
import { formatDateTime } from '../utils/dateTime'
import TelemetryPolicySelect from './TelemetryPolicySelect'
import { sendingChanges, unansweredChanges } from './telemetryBudgetChangeState'
import { TELEMETRY_POLICY_PROJECT_DEFAULT } from './telemetryAcceptanceChoice'
import { telemetryPolicySegmentLabel, telemetryPolicySegmentsView, type TelemetryPolicySegmentsView } from './telemetryPolicyModel'

// TEL-B: a running test's telemetry budget policy changes by appending a
// segment. Each segment classifies the live intervals wholly after its
// effective time; the interval a change falls inside is classified by none.

// A change's request ID; a browser context without randomUUID (not served
// over HTTPS) still gets a unique one.
function newSegmentRequestID(): string {
  const source = globalThis.crypto
  if (typeof source?.randomUUID === 'function') return source.randomUUID()
  return `segment-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`
}

/** The verified segments list, shown only when the run's policy changed. */
export function TelemetryPolicySegmentList({ segments }: { segments?: unknown }) {
  // Keyed by content: a poll that returns the same list keeps the view it
  // verified rather than verifying (and blanking) it again. The list itself
  // is verified, never a JSON copy (which would turn a -0 limit into 0).
  const key = Array.isArray(segments) && segments.length >= 2 ? JSON.stringify(segments) : ''
  // The committed list, set before the verification below runs in the same
  // commit (an abandoned render never reaches it).
  const latest = useRef(segments)
  useEffect(() => { latest.current = segments })
  const [verified, setVerified] = useState<{ key: string; view: TelemetryPolicySegmentsView }>()
  useEffect(() => {
    if (key === '') return
    let active = true
    void telemetryPolicySegmentsView(latest.current).then(view => { if (active) setVerified({ key, view }) })
    return () => { active = false }
  }, [key])
  if (key === '') return null
  const view = verified && verified.key === key ? verified.view : { reason: 'Verifying the retained policy digests…' }
  return <section className="goal-status-card telemetry-policy-segments-card" aria-label="Telemetry policy segments" data-testid="telemetry-policy-segments">
    <h3>Telemetry policy segments</h3>
    <p>This run's telemetry budget policy changed while it ran. Each segment classifies the intervals after it took effect; an interval a change fell inside, or classified while the run's segments could not be read, belongs to no segment: its budgets are not judged, though what no policy decides (losses, gaps, transaction, broker and throttling samples) still is.</p>
    {view.segments ? <table className="data-table">
      <thead><tr><th>Segment</th><th>From</th><th>Policy</th><th>Cadence</th><th>Policy hash</th></tr></thead>
      <tbody>
        {view.segments.map(segment => <tr key={segment.sequence}>
          <td>{segment.sequence}</td>
          <td>{formatDateTime(segment.effective_at, { seconds: true })}</td>
          <td>{telemetryPolicySegmentLabel(segment)}{segment.requested_by ? ` · by ${segment.requested_by}` : ''}</td>
          <td>{segment.policy ? `${segment.policy.expected_controller_publication_interval_ms.toLocaleString()} ms` : 'n/a'}</td>
          <td style={{ overflowWrap: 'anywhere' }}>{segment.policy ? <code>{segment.policy.hash}</code> : 'Receipt invalid'}</td>
        </tr>)}
      </tbody>
    </table> : <p>{view.reason}</p>}
  </section>
}

interface ChangeProps {
  testId: string
  projectId?: string
  status: string
  /** The run's segments as last read (the admission receipt first); absent before any change. */
  segments?: TelemetryPolicySegmentReceipt[]
  /** The cadence scope of the receipt in force: a local run's cadence can change. */
  cadenceScope?: string
  onChanged: (segments: unknown) => void
}

/** Changes a running or paused test's telemetry budget policy from now on. */
export function TelemetryBudgetChange({ testId, projectId, status, segments, cadenceScope, onChanged }: ChangeProps) {
  const [open, setOpen] = useState(false)
  const [policyId, setPolicyId] = useState('')
  const [cadence, setCadence] = useState('')
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [refreshKey, setRefreshKey] = useState(0)
  // One submission is captured when the change is reviewed: its request ID
  // and expected sequence. A change sent without an answer is retried under
  // its submission, so the retry returns the change it made, and a change
  // made meanwhile by someone else is refused (409) rather than silently
  // built upon. Any other review starts a submission from the latest segment.
  const [submission, setSubmission] = useState<TelemetryPolicySegmentRequest>()
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])
  // Focus follows the control: to its heading when it opens, back to the
  // open button when it closes.
  const heading = useRef<HTMLHeadingElement>(null)
  const opener = useRef<HTMLButtonElement>(null)
  const wasOpen = useRef(false)
  useEffect(() => {
    if (open) heading.current?.focus()
    else if (wasOpen.current) opener.current?.focus()
    wasOpen.current = open
  }, [open])
  const cadenceErrorId = useId()
  if (status !== 'RUNNING' && status !== 'PAUSED') return null

  const local = cadenceScope === 'local-controller-ticker'
  const cadenceSeconds = cadence.trim() === '' ? undefined : Number(cadence)
  const cadenceInvalid = cadenceSeconds !== undefined && (!Number.isInteger(cadenceSeconds) || cadenceSeconds < 1 || cadenceSeconds > 3600)
  const latest = segments && segments.length > 0 ? segments[segments.length - 1].sequence : 1
  const choice = policyId.trim() || TELEMETRY_POLICY_PROJECT_DEFAULT

  const edit = (apply: () => void) => {
    apply()
    setConfirming(false)
  }

  const review = () => {
    if (cadenceInvalid) return
    setError('')
    const interval = local && cadenceSeconds !== undefined ? cadenceSeconds : undefined
    // The same change as one sent without a known answer is retried under
    // its submission (identity and expected sequence), even after Back,
    // Cancel, a remount or another change reviewed meanwhile, so it returns
    // the change it made instead of appending another. It is a retry only
    // while the latest segment could be the one it made (or none was made):
    // past that, another change is in force, and the same choice is a new
    // change from it. Anything else is reviewed afresh against the latest
    // segment.
    const unanswered = unansweredChanges.get(testId)
    if (unanswered && latest > unanswered.expected_sequence + 1) unansweredChanges.delete(testId)
    if (unanswered && latest <= unanswered.expected_sequence + 1 && unanswered.telemetry_budget_policy_id === choice && unanswered.controller_publication_interval_seconds === interval) {
      setSubmission(unanswered)
    } else {
      setSubmission({ request_id: newSegmentRequestID(), telemetry_budget_policy_id: choice, expected_sequence: latest,
        ...(interval !== undefined ? { controller_publication_interval_seconds: interval } : {}) })
    }
    setConfirming(true)
  }

  // Reads the run's segments again; the latest sequence read, if any.
  const reload = async (): Promise<number | undefined> => {
    try {
      const listed = await telemetryPolicySegmentsApi.list(testId)
      if (mounted.current) onChanged(listed.segments)
      const segments = Array.isArray(listed.segments) ? listed.segments : []
      const last: unknown = segments[segments.length - 1]
      return typeof last === 'object' && last !== null && typeof (last as { sequence?: unknown }).sequence === 'number' ? (last as { sequence: number }).sequence : undefined
    } catch {
      // The run's segments stay as last read until the next poll.
      return undefined
    }
  }

  // Ends the run's unanswered change if it is still this one.
  const answered = (sent: TelemetryPolicySegmentRequest) => {
    if (unansweredChanges.get(testId) === sent) unansweredChanges.delete(testId)
  }

  const submit = async () => {
    if (!submission) return
    if (sendingChanges.has(testId)) {
      setError('A change to this run is still being sent.')
      return
    }
    if (latest > submission.expected_sequence + 1) {
      // Another change is in force since this one was reviewed: a retry
      // would answer with the change it made, now superseded, and a change
      // never sent would be refused.
      answered(submission)
      setSubmission(undefined)
      setConfirming(false)
      setError('Another change was made meanwhile. Review this change again.')
      return
    }
    sendingChanges.add(testId)
    setBusy(true)
    setError('')
    unansweredChanges.set(testId, submission)
    try {
      const { segment: made, replayed } = await telemetryPolicySegmentsApi.append(testId, submission)
      answered(submission)
      if (!mounted.current) return
      setSubmission(undefined)
      setConfirming(false)
      setOpen(false)
      setPolicyId('')
      setCadence('')
      const current = await reload()
      // A retry answers with the change it made, which a later one may
      // have replaced since.
      if (mounted.current && typeof made?.sequence === 'number') {
        if (current !== undefined && current > made.sequence) {
          setNotice(`The change was stored as segment ${made.sequence}; segment ${current} has replaced it since.`)
        } else if (current === undefined && replayed) {
          setNotice(`The change was stored earlier as segment ${made.sequence}; the run's latest segments could not be read to confirm it is still in force.`)
        } else {
          setNotice(`The change was stored as segment ${made.sequence}.`)
        }
      }
    } catch (cause) {
      // A refusal answers the change; a 503 or a lost response may not have
      // (the change may be stored), so it stays to be retried.
      const refused = cause instanceof ApiError && [400, 403, 404, 409].includes(cause.status)
      if (refused) answered(submission)
      if (!mounted.current) return
      setError(cause instanceof Error ? cause.message : 'The change could not be made.')
      if (refused) {
        // The run's segments or policies may have changed: read them again.
        setSubmission(undefined)
        setConfirming(false)
        setRefreshKey(key => key + 1)
        await reload()
      }
    } finally {
      sendingChanges.delete(testId)
      if (mounted.current) setBusy(false)
    }
  }

  // A live region present before (and whatever) it announces, so every
  // stored change is read out.
  const announcement = <p role="status" data-testid="telemetry-budget-change-notice" style={notice ? undefined : { margin: 0 }}>{notice}</p>
  if (!open) {
    return <>
      {announcement}
      <button type="button" className="secondary-button" ref={opener} onClick={() => { setNotice(''); setOpen(true) }} data-testid="telemetry-budget-change-open">Change telemetry budgets</button>
    </>
  }
  return <>{announcement}<section className="goal-status-card telemetry-budget-change" aria-label="Change telemetry budgets" data-testid="telemetry-budget-change" aria-busy={busy || undefined}>
    <h3 ref={heading} tabIndex={-1}>Change telemetry budgets</h3>
    <TelemetryPolicySelect kind="budget" subject="change" projectId={projectId} value={policyId} onChange={value => { if (value !== policyId) edit(() => setPolicyId(value)) }} refreshKey={refreshKey} />
    {local && <label className="scenario-field">
      <span>Publication cadence (seconds, optional)</span>
      <input className="settings-input" inputMode="numeric" value={cadence} placeholder="Keep the current cadence"
        onChange={event => { const value = event.target.value; if (value !== cadence) edit(() => setCadence(value)) }} aria-invalid={cadenceInvalid || undefined} aria-describedby={cadenceInvalid ? cadenceErrorId : undefined} />
    </label>}
    {cadenceInvalid && <p className="form-error" id={cadenceErrorId}>The cadence is a whole number of seconds from 1 to 3600.</p>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {confirming && submission
      ? <div className="button-row">
          <p role="status">{`From now on this run's live telemetry is classified by ${submission.telemetry_budget_policy_id === TELEMETRY_POLICY_PROJECT_DEFAULT ? 'the current threshold settings' : `policy ${submission.telemetry_budget_policy_id}`}${submission.controller_publication_interval_seconds !== undefined ? `, published every ${submission.controller_publication_interval_seconds} s` : ''}. This cannot be undone; a later change appends another segment.`}</p>
          <button type="button" className="primary-button" disabled={busy} onClick={() => void submit()} data-testid="telemetry-budget-change-confirm">{busy ? 'Changing…' : 'Confirm change'}</button>
          <button type="button" className="secondary-button" disabled={busy} onClick={() => setConfirming(false)}>Back</button>
        </div>
      : <div className="button-row">
          <button type="button" className="primary-button" disabled={busy || cadenceInvalid} onClick={review} data-testid="telemetry-budget-change-review">Review change</button>
          <button type="button" className="secondary-button" disabled={busy} onClick={() => { setOpen(false); setConfirming(false); setError('') }}>Cancel</button>
        </div>}
  </section></>
}
