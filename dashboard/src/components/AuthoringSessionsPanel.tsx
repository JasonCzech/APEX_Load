import { type AuthoringSession } from '../types'

interface Props {
  sessions: AuthoringSession[]
  onResume: (session: AuthoringSession) => void
  onApprove: (session: AuthoringSession) => void
  onDiscard: (session: AuthoringSession) => void
}

export default function AuthoringSessionsPanel({ sessions, onResume, onApprove, onDiscard }: Props) {
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
              {formatReviewValue(session.review_state?.approval_health_status) && (
                <small>Last approval health: {formatReviewValue(session.review_state?.approval_health_status)}</small>
              )}
            </div>
            <div className="authoring-session-actions">
              <button className="btn btn-secondary" type="button" onClick={() => onResume(session)} disabled={!session.draft}>Resume</button>
              <button className="btn btn-primary" type="button" onClick={() => onApprove(session)} disabled={!session.draft || session.status === 'blocked'}>Approve</button>
              <button className="btn btn-secondary" type="button" onClick={() => onDiscard(session)}>Discard</button>
            </div>
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
