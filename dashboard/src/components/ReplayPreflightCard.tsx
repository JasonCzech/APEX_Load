import type { ReplayPreflight } from '../types'

export function replayPreflightStatusLabel(preflight?: ReplayPreflight | null) {
  if (!preflight) return 'Not checked'
  if (!preflight.valid) return 'Needs attention'
  if ((preflight.checks || []).some(check => check.status === 'warn')) return 'Ready with notes'
  return 'Ready'
}

export default function ReplayPreflightCard({ preflight }: { preflight: ReplayPreflight }) {
  return (
    <div className={`replay-preflight-card ${preflight.valid ? 'ready' : 'attention'}`}>
      <div className="replay-preflight-header">
        <div>
          <span className="replay-detail-label">Preflight</span>
          <strong>{replayPreflightStatusLabel(preflight)}</strong>
        </div>
        <div className="replay-preflight-meta">
          <span>{preflight.action_count} action{preflight.action_count === 1 ? '' : 's'}</span>
          <span>{preflight.data_source_count} data source{preflight.data_source_count === 1 ? '' : 's'}</span>
          {preflight.browser_required && <span>Browser runtime</span>}
        </div>
      </div>
      <div className="replay-preflight-checks">
        {(preflight.checks || []).map(check => (
          <div key={check.id} className={`replay-preflight-check ${check.status}`}>
            <span>{check.label}</span>
            <strong>{check.status === 'pass' ? 'Ready' : check.status === 'warn' ? 'Note' : 'Needs work'}</strong>
            {check.detail && <small>{check.detail}</small>}
          </div>
        ))}
      </div>
      {((preflight.missing_variables?.length || 0) > 0 || (preflight.missing_secrets?.length || 0) > 0) && (
        <div className="replay-preflight-alerts">
          {(preflight.missing_variables || []).map(name => (
            <span key={`missing-var-${name}`}>Missing variable: {name}</span>
          ))}
          {(preflight.missing_secrets || []).map(name => (
            <span key={`missing-secret-${name}`}>Missing secret: {name}</span>
          ))}
        </div>
      )}
    </div>
  )
}
