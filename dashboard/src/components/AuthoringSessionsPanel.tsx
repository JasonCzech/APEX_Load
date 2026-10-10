import { type Dispatch, type SetStateAction, useState } from 'react'
import { type AuthoringCorrelationPreviewRequest, type AuthoringCorrelationPreviewResponse, type AuthoringSession } from '../types'
import { formatDateTime } from '../utils/dateTime'

interface Props {
  sessions: AuthoringSession[]
  onResume: (session: AuthoringSession) => void
  onApprove: (session: AuthoringSession) => void
  onDiscard: (session: AuthoringSession) => void
  correlationPreviews?: Record<string, AuthoringCorrelationPreviewResponse>
  correlationErrors?: Record<string, string>
  onPreviewCorrelation: (session: AuthoringSession, payload: AuthoringCorrelationPreviewRequest) => void
  onApplyCorrelation: (session: AuthoringSession, payload: AuthoringCorrelationPreviewRequest) => void
}

type CorrelationFormState = {
  revision: string
  name: string
  source: string
  expression: string
  sampleValue: string
  replaceSampleValue: boolean
  foundActionIndex: number | null
  usedActionIndexes: number[]
}

export default function AuthoringSessionsPanel({
  sessions,
  onResume,
  onApprove,
  onDiscard,
  correlationPreviews = {},
  correlationErrors = {},
  onPreviewCorrelation,
  onApplyCorrelation,
}: Props) {
  const [forms, setForms] = useState<Record<string, CorrelationFormState>>({})
  const openSessions = sessions.filter(session => session.status !== 'approved')
  if (openSessions.length === 0) {
    return null
  }

  return (
    <section className="authoring-sessions-panel">
      <div className="authoring-sessions-header">
        <div>
          <span className="script-create-kicker">Review queue</span>
          <h3>Authoring Sessions</h3>
        </div>
        <span className="authoring-session-count">{openSessions.length} open</span>
      </div>
      <div className="authoring-session-list">
        {openSessions.map(session => {
          // Deriving the same-revision action fingerprint serializes the draft's
          // action graph. Compute it once per card: doing it from every field and
          // every consumer row makes a large authoring draft quadratic to render.
          const form = formFor(session, forms)
          const usedActionIndexes = new Set(form.usedActionIndexes)
          return (
          <article key={session.id} className="authoring-session-card">
            <div className="authoring-session-main">
              <span className={`authoring-session-status ${session.status}`}>{formatStatus(session.status)}</span>
              <h4>{session.name}</h4>
              <p>{formatSource(session.source)} · Updated {formatDate(session.updated_at)}</p>
              <div className="authoring-session-health-row">
                {formatReviewValue(session.review_state?.approval_health_status) && (
                  <span className={`script-health-token ${healthTone(formatReviewValue(session.review_state?.approval_health_status))}`}>
                    Last approval: {formatReviewValue(session.review_state?.approval_health_status)}
                  </span>
                )}
                {Array.isArray(session.review_state?.pending_repairs) && (
                  <span className="script-health-token warning">{session.review_state.pending_repairs.length} pending repair{session.review_state.pending_repairs.length === 1 ? '' : 's'}</span>
                )}
                {session.draft?.metadata?.import?.source && (
                  <span className="script-health-token">{formatSource(session.draft.metadata.import.source)}</span>
                )}
                {session.approval_ready === false && (
                  <span className="script-health-token danger">approval blocked</span>
                )}
                {session.approval_ready === true && (
                  <span className="script-health-token healthy">approval ready</span>
                )}
              </div>
              {session.next_actions?.length ? (
                <ul className="authoring-session-next-actions">
                  {session.next_actions.slice(0, 3).map(action => <li key={action}>{action}</li>)}
                </ul>
              ) : null}
              {session.blocking_health_inputs?.length ? (
                <p className="authoring-session-blockers">Blocking inputs: {session.blocking_health_inputs.join(', ')}</p>
              ) : null}
            </div>
            <div className="authoring-session-actions">
              <button className="btn btn-secondary" type="button" onClick={() => onResume(session)} disabled={!session.draft}>Resume</button>
              <button className="btn btn-primary" type="button" onClick={() => onApprove(session)} disabled={!session.draft || session.status === 'blocked' || session.approval_ready === false}>Approve</button>
              <button className="btn btn-secondary" type="button" onClick={() => onDiscard(session)}>Discard</button>
            </div>
            {session.draft && (
              <div className="authoring-correlation-repair">
                <div className="authoring-correlation-heading">
                  <strong>Guided Correlation Repair</strong>
                  <span>{session.draft.actions?.length || 0} actions available</span>
                </div>
                <div className="authoring-correlation-grid">
                  <label>
                    Variable
                    <input
                      value={form.name}
                      onChange={event => setFormValue(session, setForms, 'name', event.target.value)}
                      placeholder="csrf_token"
                    />
                  </label>
                  <label>
                    Source
                    <select
                      value={form.source}
                      onChange={event => setFormValue(session, setForms, 'source', event.target.value)}
                    >
                      <option value="body">Body</option>
                      <option value="header">Header</option>
                      <option value="cookie">Cookie</option>
                    </select>
                  </label>
                  <label>
                    Expression
                    <input
                      value={form.expression}
                      onChange={event => setFormValue(session, setForms, 'expression', event.target.value)}
                      placeholder="$.token"
                    />
                  </label>
                  <label>
                    Sample value
                    <input
                      value={form.sampleValue}
                      onChange={event => setFormValue(session, setForms, 'sampleValue', event.target.value)}
                      placeholder="abc123"
                    />
                  </label>
                  <label>
                    Found in action
                    <select
                      aria-label={`Correlation producer for ${session.name}`}
                      value={form.foundActionIndex ?? ''}
                      onChange={event => {
                        const foundActionIndex = event.target.value ? Number(event.target.value) : null
                        setForms(currentForms => {
                          const current = formFor(session, currentForms)
                          return {
                            ...currentForms,
                            [session.id]: {
                              ...current,
                              foundActionIndex,
                              usedActionIndexes: foundActionIndex === null
                                ? []
                                : (session.draft?.actions || []).flatMap((_, index) => index + 1 > foundActionIndex ? [index + 1] : []),
                            },
                          }
                        })
                      }}
                    >
                      <option value="">Select producing action</option>
                      {(session.draft?.actions || []).map((action, index) => (
                        <option key={`${session.id}-producer-${index + 1}`} value={index + 1}>
                          {index + 1}. {action.name || `Action ${index + 1}`}
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
                <fieldset className="authoring-correlation-consumers">
                  <legend>Used by actions</legend>
                  {(session.draft.actions || []).map((action, index) => {
                    const actionIndex = index + 1
                    if (form.foundActionIndex === null || actionIndex <= form.foundActionIndex) return null
                    return (
                      <label key={`${session.id}-consumer-${actionIndex}`}>
                        <input
                          type="checkbox"
                          checked={usedActionIndexes.has(actionIndex)}
                          onChange={event => {
                            const next = event.target.checked
                              ? [...form.usedActionIndexes, actionIndex].sort((left, right) => left - right)
                              : form.usedActionIndexes.filter(value => value !== actionIndex)
                            setFormValue(session, setForms, 'usedActionIndexes', next)
                          }}
                        />
                        {actionIndex}. {action.name || `Action ${actionIndex}`}
                      </label>
                    )
                  })}
                </fieldset>
                <label className="authoring-correlation-toggle">
                  <input
                    type="checkbox"
                    checked={form.replaceSampleValue}
                    onChange={event => setFormValue(session, setForms, 'replaceSampleValue', event.target.checked)}
                  />
                  Replace matching sample values with the new variable
                </label>
                <div className="authoring-session-actions">
                  <button className="btn btn-secondary" type="button" disabled={form.foundActionIndex === null} onClick={() => onPreviewCorrelation(session, buildCorrelationPayload(session, forms))}>Preview Repair</button>
                  <button className="btn btn-primary" type="button" disabled={form.foundActionIndex === null} onClick={() => onApplyCorrelation(session, buildCorrelationPayload(session, forms))}>Apply Repair</button>
                </div>
                {correlationErrors[session.id] && <p className="authoring-correlation-error">{correlationErrors[session.id]}</p>}
                {correlationPreviews[session.id] && (
                  <div className="authoring-correlation-preview">
                    <div>
                      <strong>{correlationPreviews[session.id].applied ? 'Applied' : 'Preview'}</strong>
                      <span>{correlationPreviews[session.id].changes.length} changes · health {correlationPreviews[session.id].authoring_health.status} · {correlationPreviews[session.id].authoring_health.score}</span>
                    </div>
                    <ul>
                      {correlationPreviews[session.id].changes.slice(0, 4).map(change => (
                        <li key={`${change.kind}:${change.path}:${change.after || change.before}`}>
                          <span>{change.kind}</span>
                          <code>{change.path}</code>
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </div>
            )}
          </article>
          )
        })}
      </div>
    </section>
  )
}

function formatStatus(status: string): string {
  const value = status.trim()
  if (!value) return 'Unknown'
  return value.replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
}

function formatSource(source: string): string {
  const value = source.trim()
  if (!value) return 'Unknown source'
  return value.replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
}

function formatDate(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return 'unknown'
  }
  return formatDateTime(date, { seconds: true })
}

function formatReviewValue(value: unknown): string {
  if (value === undefined || value === null) {
    return ''
  }
  return String(value)
}

function healthTone(value: string): string {
  if (value === 'blocked') return 'danger'
  if (value === 'needs_review') return 'warning'
  if (value === 'ready' || value === 'healthy') return 'healthy'
  return ''
}

function formFor(session: AuthoringSession, forms: Record<string, CorrelationFormState>): CorrelationFormState {
  const form = forms[session.id]
  const revision = correlationFormRevision(session)
  return form?.revision === revision ? form : defaultCorrelationForm(revision)
}

function correlationFormRevision(session: AuthoringSession): string {
  // The numeric action choices are meaningful only for the exact draft the
  // operator reviewed. Include both the durable revision and action graph so a
  // same-revision prop correction cannot leave an out-of-range selection live.
  return `${session.updated_at}\u0000${JSON.stringify(session.draft?.actions || [])}`
}

function defaultCorrelationForm(revision: string): CorrelationFormState {
  return {
    revision,
    name: 'correlated_value',
    source: 'body',
    expression: '$.token',
    sampleValue: '',
    replaceSampleValue: true,
    foundActionIndex: null,
    usedActionIndexes: [],
  }
}

function setFormValue<K extends keyof CorrelationFormState>(
  session: AuthoringSession,
  setForms: Dispatch<SetStateAction<Record<string, CorrelationFormState>>>,
  key: K,
  value: CorrelationFormState[K],
) {
  setForms(forms => ({
    ...forms,
    [session.id]: {
      ...formFor(session, forms),
      [key]: value,
    },
  }))
}

function buildCorrelationPayload(session: AuthoringSession, forms: Record<string, CorrelationFormState>): AuthoringCorrelationPreviewRequest {
  const form = formFor(session, forms)
  return {
    name: form.name,
    expected_updated_at: session.updated_at,
    source: form.source,
    expression: form.expression,
    sample_value: form.sampleValue,
    ...(form.foundActionIndex === null ? {} : { found_action_index: form.foundActionIndex }),
    ...(form.foundActionIndex === null ? {} : { used_action_indexes: form.usedActionIndexes }),
    replace_sample_value: form.replaceSampleValue,
  }
}
