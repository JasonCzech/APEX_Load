import './ReplayPreflightCard.css'
import type { ReplayPreflight } from '../types/scripts'
import { replayPreflightStatusLabel } from './replayPreflightUtils'

export default function ReplayPreflightCard({
  preflight,
  onConfigureSecrets,
  secretStatusState,
  onRetrySecretStatus,
  configureSecretsLabel = 'Configure secrets',
}: {
  preflight: ReplayPreflight
  onConfigureSecrets?: () => void
  secretStatusState?: 'checking' | 'unavailable'
  onRetrySecretStatus?: () => void
  configureSecretsLabel?: string
}) {
  const expectedActionCount = preflight.expected_action_count ?? preflight.action_count
  return (
    <div className={`replay-preflight-card ${preflight.valid && !secretStatusState ? 'ready' : 'attention'}`}>
      <div className="replay-preflight-header">
        <div>
          <span className="replay-detail-label">Preflight (static checks)</span>
          <strong>
            {secretStatusState === 'checking'
              ? 'Checking credentials'
              : secretStatusState === 'unavailable'
                ? 'Credential status unavailable'
                : replayPreflightStatusLabel(preflight)}
          </strong>
        </div>
        <div className="replay-preflight-meta">
          <span>{preflight.action_count} action{preflight.action_count === 1 ? '' : 's'}</span>
          {expectedActionCount !== preflight.action_count && (
            <span>{expectedActionCount} expected</span>
          )}
          <span>{preflight.data_source_count} data source{preflight.data_source_count === 1 ? '' : 's'}</span>
          {preflight.browser_required && <span>Browser runtime</span>}
        </div>
      </div>
      <div className="replay-preflight-checks">
        {(preflight.checks || []).map(check => (
          <div key={check.id} className={`replay-preflight-check ${check.status}`}>
            <span>{check.label}</span>
            <strong>{check.id === 'runtime' && check.status === 'warn' ? 'Not verified' : check.status === 'pass' ? 'Passed' : check.status === 'warn' ? 'Note' : 'Needs work'}</strong>
            {check.detail && <small>{check.detail}</small>}
          </div>
        ))}
      </div>
      {secretStatusState && (
        <div className="replay-preflight-alerts" role="status">
          <span>
            {secretStatusState === 'checking'
              ? 'Project secret configuration is still being checked. Replay remains blocked until the check completes.'
              : 'Project secret configuration could not be verified. Replay remains blocked until the check succeeds.'}
          </span>
          {onConfigureSecrets && (
            <button className="btn btn-secondary btn-sm" type="button" onClick={onConfigureSecrets}>
              {configureSecretsLabel}
            </button>
          )}
          {secretStatusState === 'unavailable' && onRetrySecretStatus && (
            <button className="btn btn-secondary btn-sm" type="button" onClick={onRetrySecretStatus}>
              Retry credential check
            </button>
          )}
        </div>
      )}
      {((preflight.missing_variables?.length || 0) > 0 || (preflight.missing_secrets?.length || 0) > 0) && (
        <div className="replay-preflight-alerts">
          {(preflight.missing_variables || []).map(name => (
            <span key={`missing-var-${name}`}>Missing variable: {name}</span>
          ))}
          {(preflight.missing_secrets || []).map(name => (
            <span key={`missing-secret-${name}`}>Missing secret: {name}</span>
          ))}
          {(preflight.missing_secrets?.length || 0) > 0 && onConfigureSecrets && (
            <button className="btn btn-secondary btn-sm" type="button" onClick={onConfigureSecrets}>
              {configureSecretsLabel}
            </button>
          )}
        </div>
      )}
    </div>
  )
}
