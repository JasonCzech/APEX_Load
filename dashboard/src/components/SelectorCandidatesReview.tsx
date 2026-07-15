import { type ScriptDefinition } from '../types/scripts'
import './SelectorCandidatesReview.css'

export interface SelectorCandidate {
  kind: string
  value: string
  confidence: number
  reason?: string
}

interface ActionCandidates {
  actionIndex: number
  actionName: string
  actionType: string
  selector: string
  candidates: SelectorCandidate[]
}

function candidatesFromMetadata(value: unknown): SelectorCandidate[] {
  if (!Array.isArray(value)) return []
  return value
    .filter((item): item is Record<string, unknown> => typeof item === 'object' && item !== null)
    .map(item => ({
      kind: typeof item.kind === 'string' ? item.kind : '',
      value: typeof item.value === 'string' ? item.value : '',
      confidence: typeof item.confidence === 'number' ? item.confidence : 0,
      reason: typeof item.reason === 'string' ? item.reason : undefined,
    }))
    .filter(candidate => candidate.value !== '')
}

function collectActionCandidates(script: ScriptDefinition | null): ActionCandidates[] {
  if (!script?.actions) return []
  const out: ActionCandidates[] = []
  for (const [actionIndex, action] of script.actions.entries()) {
    const metadata = (action as { metadata?: Record<string, unknown> }).metadata
    const candidates = candidatesFromMetadata(metadata?.source_selector_candidates)
    if (candidates.length === 0) continue
    out.push({
      actionIndex,
      actionName: action.name || action.type || 'action',
      actionType: action.type || '',
      selector: (action as { selector?: string }).selector || '',
      candidates,
    })
  }
  return out
}

function confidenceBand(confidence: number): string {
  if (confidence >= 85) return 'high'
  if (confidence >= 70) return 'medium'
  return 'low'
}

/**
 * Renders the ranked selector candidates captured per action by any recording
 * surface (embedded recorder, Chrome DevTools Recorder import, TruClient
 * import) so reviewers can see how each step will locate its element and what
 * replay self-heal will fall back to.
 */
export default function SelectorCandidatesReview({
  script,
  onPromoteCandidate,
}: {
  script: ScriptDefinition | null
  onPromoteCandidate?: (actionIndex: number, candidate: SelectorCandidate) => void
}) {
  const actions = collectActionCandidates(script)
  if (actions.length === 0) return null

  return (
    <div className="selector-candidates-review">
      <h3>Selector Candidates</h3>
      <p className="selector-candidates-subtitle">
        Each step keeps its ranked locator alternates. Replay tries the primary selector first and
        self-heals through the alternates when the page drifts.
      </p>
      {actions.map((action, index) => (
        <div className="selector-candidates-action" key={`${action.actionName}-${index}`}>
          <div className="selector-candidates-action-header">
            <span className="selector-candidates-action-name">{action.actionName}</span>
            <code className="selector-candidates-primary">{action.selector}</code>
          </div>
          <ul>
            {action.candidates.map((candidate, candidateIndex) => (
              <li key={`${candidate.kind}-${candidateIndex}`}>
                <span className={`selector-confidence selector-confidence-${confidenceBand(candidate.confidence)}`}>
                  {candidate.confidence}
                </span>
                <span className="selector-kind">{candidate.kind}</span>
                <code>{candidate.value}</code>
                {onPromoteCandidate && candidate.value !== action.selector && (
                  <button
                    type="button"
                    className="selector-promote"
                    onClick={() => onPromoteCandidate(action.actionIndex, candidate)}
                  >
                    Use
                  </button>
                )}
                {candidate.reason && <small>{candidate.reason}</small>}
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  )
}
