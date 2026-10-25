import './ReplayPreflightCard.css'
import { useMemo } from 'react'
import type { ReplayPreflight, ScriptDefinition } from '../types/scripts'
import { replayPreflightStatusLabel } from './replayPreflightUtils'
import { dependencyCodeFolding, parseReviewGateIssue, readinessIssueText, replayReadinessIssues, reviewGateBreadcrumb, reviewGateCodeProblems, savedReviewTitle } from './replayReadinessIssues'

// The preflight issues the review_gates check stands for: execution blockers
// and the review wording of a review-required gate, not static validation.
function reviewGateMessages(preflight: ReplayPreflight): string[] {
  return [...new Set((preflight.issues || []).map(message => message.trim()).filter(message => {
    const gate = parseReviewGateIssue(message)
    return Boolean(gate && (gate.codes.length > 0 || /\breview/i.test(gate.rest)))
  }))]
}

export default function ReplayPreflightCard({
  preflight,
  compact = false,
  onConfigureSecrets,
  secretStatusState,
  onRetrySecretStatus,
  configureSecretsLabel = 'Configure secrets',
  onSelectIssue,
  issueBreadcrumb = reviewGateBreadcrumb,
  script,
}: {
  preflight: ReplayPreflight
  compact?: boolean
  onConfigureSecrets?: () => void
  secretStatusState?: 'checking' | 'unavailable'
  onRetrySecretStatus?: () => void
  configureSecretsLabel?: string
  /**
   * Opens a review gate's step, given the gate and the row's own problem
   * (its code, empty for a gate that names none); without it the gates are
   * listed as text.
   */
  onSelectIssue?: (message: string, problem: { code: string; message: string }) => void
  /** Where a gate sits, for its list item; defaults to the gate's own path and name. */
  issueBreadcrumb?: (message: string) => string | undefined
  /** The document the preflight describes, so a blocking value is titled by why it blocks. */
  script?: ScriptDefinition
}) {
  const expectedActionCount = preflight.expected_action_count ?? preflight.action_count
  const gateMessages = reviewGateMessages(preflight)
  // A dependency code naming a value that blocks replay is that value's
  // problem, listed once, at the first gate naming it, and titled as Author
  // Problems and the start gate title it (replayReadinessIssues); so are the
  // codes for a value the draft now supplies, which a save re-evaluates.
  const folding = dependencyCodeFolding(preflight)
  // The titles can analyze the whole script (an older controller's preflight
  // does not say why a value blocks), and the editor renders this card on
  // every render, so they are computed once per preflight and script.
  const valueTitles = useMemo(() => new Map((preflight.blocking_variables || []).length === 0 ? [] : replayReadinessIssues({ ...preflight, issues: [] }, [], new Map(), script)
    .flatMap(issue => issue.id.startsWith('readiness:blocking:') && issue.variable ? [[issue.variable, issue.title] as const] : [])), [preflight, script])
  const listedValues = new Set<string>()
  // A gate reads by what to do about it and where, as Author Problems lists
  // it, rather than as the raw sentence the check carries: one row per
  // problem, so a gate with two codes is two rows here as it is two blockers
  // in Author Problems and the start gate. Each row opens its own problem.
  const gateList = gateMessages.length > 0 && (
    <ul className="replay-preflight-gates" aria-label="Review gates that block replay">
      {gateMessages.flatMap(message => {
        const where = issueBreadcrumb(message)
        const problems = reviewGateCodeProblems(message)
        return (problems.length > 0 ? problems : [{ code: '', message }]).flatMap(problem => {
          const value = folding(problem.code)
          const folded = value && value.kind !== 'code' ? `${value.kind}:${value.name}` : undefined
          if (folded && listedValues.has(folded)) return []
          if (folded) listedValues.add(folded)
          const title = value?.kind === 'value' ? valueTitles.get(value.name) || `Unresolved value: ${value.name}`
            : value?.kind === 'saved-review' ? savedReviewTitle(value.name)
              : readinessIssueText(problem.message).title
          const content = <><strong>{title}</strong>{where && <small>{where}</small>}</>
          return [<li key={folded || `${message}#${problem.code}`}>{onSelectIssue
            ? <button type="button" className="btn btn-ghost btn-sm" onClick={() => onSelectIssue(message, problem)}>{content}</button>
            : content}</li>]
        })
      })}
    </ul>
  )
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
          <span>{preflight.action_count} action{preflight.action_count === 1 ? '' : 's'} across all phases</span>
          {expectedActionCount !== preflight.action_count && (
            <span>{expectedActionCount} expected</span>
          )}
          <span>{preflight.data_source_count} data source{preflight.data_source_count === 1 ? '' : 's'}</span>
          {preflight.browser_required && <span>Browser runtime</span>}
        </div>
      </div>
      <div className="replay-preflight-checks">
        {(preflight.checks || []).filter(check => !compact || check.status !== 'pass').sort((a, b) => ((a.status === 'fail' ? 0 : a.status === 'warn' ? 1 : 2) - (b.status === 'fail' ? 0 : b.status === 'warn' ? 1 : 2))).map(check => (
          <div key={check.id} className={`replay-preflight-check ${check.status}`}>
            <span>{check.label}</span><strong>{check.id === 'runtime' && check.status === 'warn' ? 'Not verified' : check.status === 'pass' ? 'Passed' : check.status === 'warn' ? (check.id === 'secrets' && preflight.missing_secrets?.length ? 'Needs work' : 'Review') : 'Needs work'}</strong>
            {check.id === 'review_gates' && check.status !== 'pass' && gateList ? gateList : check.detail && <small>{check.detail}</small>}
          </div>
        ))}
      </div>
      {compact && (preflight.checks || []).some(check => check.status === 'pass') && <details><summary>Passed static checks ({(preflight.checks || []).filter(check => check.status === 'pass').length})</summary>
        <div className="replay-preflight-checks">{(preflight.checks || []).filter(check => check.status === 'pass').map(check => <div key={check.id} className="replay-preflight-check pass"><span>{check.label}</span><strong>Passed</strong><small>{check.detail}</small></div>)}</div>
      </details>}
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
