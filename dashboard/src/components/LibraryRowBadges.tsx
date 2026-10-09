import type { ReactNode } from 'react'
import type { AuthoringHealth, AuthoringHealthInput } from '../types'
import type { ObjectActivityEvent, ObjectActivitySummary } from '../types/objectActivity'
import { formatDateTime } from '../utils/dateTime'
import ReferenceKeyBadge from './ReferenceKeyBadge'
import BadgeIcon, { type BadgeIconKind } from './ux/BadgeIcon'
import InfoPopover from './ux/InfoPopover'

type Tone = 'healthy' | 'warning' | 'danger' | ''

function actor(event?: ObjectActivityEvent) {
  return event?.actor_display_name || event?.actor_subject || 'Unknown'
}

/**
 * A library object's identity details (reference key, who created and changed
 * it) behind one info badge, so the row keeps only its name and key badges.
 * children adds rows to the details list.
 */
export function ObjectInfoBadge({ name, refKey, createdAt, activity, children }: {
  name: string
  refKey?: string
  createdAt?: string
  activity?: ObjectActivitySummary
  children?: ReactNode
}) {
  const created = activity?.creator?.created_at || createdAt
  const modifier = activity?.last_modifier
  const archive = activity?.last_archive
  return <InfoPopover
    label={`Details for ${name}`}
    title={name}
    triggerClassName="script-health-token script-info-badge"
    trigger={<BadgeIcon kind="info" />}
  >
    <dl className="ux-info-list">
      <div><dt>Reference</dt><dd>{refKey ? <ReferenceKeyBadge value={refKey} /> : 'Not assigned'}</dd></div>
      <div><dt>Created</dt><dd>{created ? formatDateTime(created) : 'Unknown time'}{activity ? ` · ${actor(activity.creator)}` : ''}</dd></div>
      {activity && <div><dt>Last modified</dt><dd>{modifier ? `${formatDateTime(modifier.created_at)} · ${actor(modifier)}` : 'Not changed since it was created'}</dd></div>}
      {archive && <div><dt>{archive.action === 'restore' ? 'Restored' : 'Archived'}</dt><dd>{formatDateTime(archive.created_at)} · {actor(archive)}</dd></div>}
      {children}
    </dl>
  </InfoPopover>
}

/** The script's identity details behind one badge, so the row keeps only the name, protocol and version. */
export function ScriptInfoBadge({ duplicateName, ...details }: {
  name: string
  refKey?: string
  createdAt?: string
  activity?: ObjectActivitySummary
  duplicateName: boolean
}) {
  return <ObjectInfoBadge {...details}>
    {duplicateName && <div><dt>Name</dt><dd>Another script in this folder has the same name. Rename one to tell them apart.</dd></div>}
  </ObjectInfoBadge>
}

export type SecretState = 'ready' | 'loading' | 'error' | 'unaddressable'

/** Overall status of a library row: authoring health, unless the secret check has not settled. */
function healthStatus(health: AuthoringHealth | undefined, secretState?: SecretState): { label: string; tone: Tone; icon: BadgeIconKind } {
  if (secretState === 'unaddressable') return { label: 'Script unavailable', tone: 'warning', icon: 'unknown' }
  if (secretState === 'loading') return { label: 'Checking secrets', tone: 'warning', icon: 'pending' }
  if (secretState === 'error') return { label: 'Secret status unavailable', tone: 'warning', icon: 'unknown' }
  switch (health?.status) {
    case 'blocked': return { label: 'Blocked', tone: 'danger', icon: 'ban' }
    case 'needs_review': return { label: 'Needs review', tone: 'warning', icon: 'alert' }
    case 'healthy': return { label: 'Healthy', tone: 'healthy', icon: 'check' }
    case 'ready': return { label: 'Static checks passed', tone: 'healthy', icon: 'check' }
    case undefined: return { label: 'Not checked yet', tone: '', icon: 'unknown' }
    default: return { label: String(health?.status).replace(/_/g, ' '), tone: '', icon: 'unknown' }
  }
}

const INPUT_STATUS: Record<string, { label: string; tone: Tone }> = {
  blocker: { label: 'Blocker', tone: 'danger' },
  warning: { label: 'Warning', tone: 'warning' },
  needs_review: { label: 'Review', tone: 'warning' },
  pass: { label: 'Passed', tone: 'healthy' },
  info: { label: 'Info', tone: '' },
}

/**
 * The colour-coded health badge (icon + score). Its popup lists every health
 * input and, when the row checks them (secretState set), the secret state with
 * the fixes the row offers.
 */
export function ScriptHealthBadge({ name, health, inputs, secretState, missingSecrets = [], onConfigureSecrets, onRetrySecretCheck }: {
  name: string
  health?: AuthoringHealth
  /** Health inputs, most severe first. */
  inputs: AuthoringHealthInput[]
  secretState?: SecretState
  missingSecrets?: string[]
  onConfigureSecrets?: () => void
  onRetrySecretCheck?: () => void
}) {
  const status = healthStatus(health, secretState)
  const score = health ? String(health.score) : ''
  return <InfoPopover
    label={`Authoring health for ${name}: ${status.label}${score ? `, score ${score}` : ''}. Show details`}
    title="Authoring health"
    triggerClassName={`script-health-token script-health-badge script-health-status ${status.tone}`.trim()}
    trigger={<><BadgeIcon kind={status.icon} />{score && <span>{score}</span>}</>}
  >
    <p className="script-health-popover-status">
      <span className={`script-health-token ${status.tone}`.trim()}>{status.label}</span>
      {score && <span>Score {score} of 100</span>}
    </p>
    {inputs.length > 0 && <ul className="script-health-popover-inputs">
      {inputs.map((input, index) => {
        const state = INPUT_STATUS[input.status] || { label: input.status.replace(/_/g, ' '), tone: '' as Tone }
        return <li key={`${input.key}-${index}`}>
          <span className={`script-health-token ${state.tone}`.trim()}>{state.label}</span>
          <span><strong>{input.key.replace(/_/g, ' ')}</strong>{input.count !== undefined ? ` · ${input.count}` : ''} — {input.summary}</span>
        </li>
      })}
    </ul>}
    {secretState && <p className="script-health-popover-secrets">
      {secretState === 'unaddressable' ? 'This legacy script has no script ID, so its secrets cannot be checked. Repair it before use.'
        : secretState === 'loading' ? 'Checking the project secrets this script needs…'
          : secretState === 'error' ? 'The project secrets this script needs could not be checked.'
            : missingSecrets.length ? `Missing project secrets: ${missingSecrets.join(', ')}.`
              : 'All project secrets this script needs are configured.'}
    </p>}
    {(missingSecrets.length > 0 && onConfigureSecrets) || (secretState === 'error' && onRetrySecretCheck) ? <div className="script-health-popover-actions">
      {missingSecrets.length > 0 && onConfigureSecrets && <button type="button" className="btn btn-sm btn-secondary" onClick={onConfigureSecrets}>Configure secrets</button>}
      {secretState === 'error' && onRetrySecretCheck && <button type="button" className="btn btn-sm btn-ghost" onClick={onRetrySecretCheck}>Retry secret check</button>}
    </div> : null}
  </InfoPopover>
}

/** A count badge (icon + number); with onOpen it is a button that opens the matching issue list. */
export function HealthCountBadge({ tone, icon, count, label, onOpen }: { tone: Tone; icon: BadgeIconKind; count: number; label: string; onOpen?: () => void }) {
  if (!onOpen) {
    return <span className={`script-health-token script-health-badge static ${tone}`.trim()} title={label}>
      <BadgeIcon kind={icon} /><span aria-hidden="true">{count}</span><span className="sr-only">{label}</span>
    </span>
  }
  return <button type="button" className={`script-health-token script-health-badge issue-pill-button ${tone}`.trim()} aria-label={label} title={label} onClick={onOpen}>
    <BadgeIcon kind={icon} /><span>{count}</span>
  </button>
}

/**
 * A script's readiness for a scenario as one colour-coded badge, shown only
 * when it needs attention; its popup says what to resolve.
 */
export function ScriptReadinessBadge({ name, readiness, needsReview }: {
  name: string
  readiness?: { blocked: boolean; summary: string }
  needsReview: boolean
}) {
  if (!readiness || (!readiness.blocked && !needsReview)) return null
  const status = readiness.blocked
    ? { label: 'Blocked', tone: 'danger', icon: 'ban' as const }
    : { label: 'Review recommended', tone: 'warning', icon: 'alert' as const }
  return <InfoPopover
    label={`Readiness for ${name}: ${status.label}. Show details`}
    title="Scenario readiness"
    triggerClassName={`script-health-token script-health-badge script-health-status ${status.tone}`}
    trigger={<><BadgeIcon kind={status.icon} /><span>{status.label}</span></>}
  >
    <p className="script-health-popover-secrets">{readiness.summary}</p>
  </InfoPopover>
}
