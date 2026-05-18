import { useState } from 'react'
import { type AuthoringCorrelationPreviewRequest, type AuthoringCorrelationPreviewResponse, type AuthoringSession } from '../types'

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
  name: string
  source: string
  expression: string
  sampleValue: string
  replaceSampleValue: boolean
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
        {openSessions.map(session => (
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
                      value={formFor(session, forms).name}
                      onChange={event => setFormValue(session.id, forms, setForms, 'name', event.target.value)}
                      placeholder="csrf_token"
                    />
                  </label>
                  <label>
                    Source
                    <select
                      value={formFor(session, forms).source}
                      onChange={event => setFormValue(session.id, forms, setForms, 'source', event.target.value)}
                    >
                      <option value="body">Body</option>
                      <option value="header">Header</option>
                      <option value="cookie">Cookie</option>
                    </select>
                  </label>
                  <label>
                    Expression
                    <input
                      value={formFor(session, forms).expression}
                      onChange={event => setFormValue(session.id, forms, setForms, 'expression', event.target.value)}
                      placeholder="$.token"
                    />
                  </label>
                  <label>
                    Sample value
                    <input
                      value={formFor(session, forms).sampleValue}
                      onChange={event => setFormValue(session.id, forms, setForms, 'sampleValue', event.target.value)}
                      placeholder="abc123"
                    />
                  </label>
                </div>
                <label className="authoring-correlation-toggle">
                  <input
                    type="checkbox"
                    checked={formFor(session, forms).replaceSampleValue}
                    onChange={event => setFormValue(session.id, forms, setForms, 'replaceSampleValue', event.target.checked)}
                  />
                  Replace matching sample values with the new variable
                </label>
                <div className="authoring-session-actions">
                  <button className="btn btn-secondary" type="button" onClick={() => onPreviewCorrelation(session, buildCorrelationPayload(session, forms))}>Preview Repair</button>
                  <button className="btn btn-primary" type="button" onClick={() => onApplyCorrelation(session, buildCorrelationPayload(session, forms))}>Apply Repair</button>
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
        ))}
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
  return date.toLocaleString()
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
  return forms[session.id] || {
    name: 'correlated_value',
    source: 'body',
    expression: '$.token',
    sampleValue: '',
    replaceSampleValue: true,
  }
}

function setFormValue<K extends keyof CorrelationFormState>(
  sessionId: string,
  forms: Record<string, CorrelationFormState>,
  setForms: (forms: Record<string, CorrelationFormState>) => void,
  key: K,
  value: CorrelationFormState[K],
) {
  setForms({
    ...forms,
    [sessionId]: {
      ...forms[sessionId] || {
        name: 'correlated_value',
        source: 'body',
        expression: '$.token',
        sampleValue: '',
        replaceSampleValue: true,
      },
      [key]: value,
    },
  })
}

function buildCorrelationPayload(session: AuthoringSession, forms: Record<string, CorrelationFormState>): AuthoringCorrelationPreviewRequest {
  const form = formFor(session, forms)
  return {
    name: form.name,
    source: form.source,
    expression: form.expression,
    sample_value: form.sampleValue,
    found_action_index: 1,
    used_action_indexes: (session.draft?.actions || []).slice(1).map((_, index) => index + 2),
    replace_sample_value: form.replaceSampleValue,
  }
}
