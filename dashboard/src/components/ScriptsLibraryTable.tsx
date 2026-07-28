import { Fragment, useState } from 'react'

import { AuthoringHealthInput, GitHubScriptStatus, StoredScript, TestSummary } from '../types'
import { LibraryCatalogItem, LibrarySourceFilter } from '../types/library'
import { type ScriptSecretStatus } from '../types/secrets'
import { formatLibraryDateTime, shortLibraryHash } from './libraryFederationUtils'
import { SortIndicator } from './SortIndicator'
import { ScriptLibrarySortField, ScriptRunSortField } from './useScriptsLibraryModel'
import ReferenceKeyBadge from './ReferenceKeyBadge'
import { isAddressableScriptId } from './scriptIdentity'
import IssueDetailsDialog, { type IssueDetail } from './IssueDetailsDialog'

export interface ScriptLibraryFederation {
  sourceFilter: LibrarySourceFilter
  remoteRows: LibraryCatalogItem[]
  actionBlocked: boolean
  actionBlockedReason?: string
  onPushScript: (script: StoredScript) => void
  onPullRemote: (item: LibraryCatalogItem) => void
  onCompare: (localId: string, remoteItem: LibraryCatalogItem) => void
  hostItemForLocal: (localId: string) => LibraryCatalogItem | undefined
  syncStatusForLocal: (localId: string) => string
  remoteItemHref: (item: LibraryCatalogItem) => string
}

export interface ScriptLibraryGitHub {
  enabled: boolean
  onPush: (script: StoredScript) => void
  statusByScript?: Record<string, GitHubScriptStatus>
}

interface Props {
  scripts: StoredScript[]
  sortField: ScriptLibrarySortField
  sortDirection: 'asc' | 'desc'
  onSort: (field: ScriptLibrarySortField) => void
  expandedScriptId: string | null
  onToggleScriptRuns: (script: StoredScript) => void
  loadingRunsFor: string | null
  scriptRuns: Record<string, TestSummary[]>
  innerSortField: ScriptRunSortField
  innerSortDirection: 'asc' | 'desc'
  onInnerSort: (field: ScriptRunSortField) => void
  getSortedInnerRuns: (runs: TestSummary[]) => TestSummary[]
  onSelectScenario?: (id: string) => void
  onEdit: (id: string) => void
  onDelete: (id: string) => void
  canManageScripts?: boolean
  emptyMessage?: string
  onCreate?: () => void
  federation?: ScriptLibraryFederation
  github?: ScriptLibraryGitHub
  secretStatusByScript?: Record<string, ScriptSecretStatus>
  secretStatusStateByScript?: Record<string, 'loading' | 'ready' | 'error'>
  onConfigureSecrets?: (script: StoredScript) => void
  onRetrySecretStatus?: (script: StoredScript) => void
}

function scriptContainsSecretReferences(script: StoredScript): boolean {
  if (!script.script) return false
  try {
    return JSON.stringify(script.script).includes('{{secret.')
  } catch {
    return false
  }
}

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : 'Never'
}

function formatDateTime(value?: string) {
  return value ? new Date(value).toLocaleString() : '—'
}

function releaseList(script: StoredScript) {
  const releases = script.associated_releases || []
  if (releases.length === 0) return 'No releases yet'
  return releases.map((release) => release.name).join(', ')
}

function scenarioRunReleaseList(script: StoredScript) {
  const releases = script.scenario_run_releases || []
  if (releases.length === 0) return 'No scenario-run releases yet'
  return releases.map((release) => release.name).join(', ')
}

function formatScenarioCount(script: StoredScript) {
  const count = script.scenario_count || 0
  return `${count} scenario${count === 1 ? '' : 's'}`
}

function authoringHealthLabel(script: StoredScript) {
  const health = script.authoring_health
  if (!health) return 'Unknown'
  const status = health.status === 'needs_review' ? 'Needs Review' : health.status === 'ready' ? 'Ready' : health.status.charAt(0).toUpperCase() + health.status.slice(1)
  return `${status} · ${health.score}`
}

function authoringHealthClass(script: StoredScript) {
  const status = script.authoring_health?.status
  if (status === 'blocked') return 'danger'
  if (status === 'needs_review') return 'warning'
  if (status === 'healthy' || status === 'ready') return 'healthy'
  return ''
}

function SortHeader<Field extends string>({
  field,
  activeField,
  direction,
  label,
  onActivate,
}: {
  field: Field
  activeField: Field
  direction: 'asc' | 'desc'
  label: string
  onActivate: (field: Field) => void
}) {
  const active = field === activeField
  return (
    <th className="sortable" aria-sort={active ? (direction === 'asc' ? 'ascending' : 'descending') : 'none'}>
      <button type="button" onClick={() => onActivate(field)}>
        {label}
        <SortIndicator active={active} direction={direction} />
      </button>
    </th>
  )
}

function authoringHealthSummary(script: StoredScript) {
  const inputs = script.authoring_health?.inputs || []
  const blockers = inputs.filter(input => input.status === 'blocker').length
  const warnings = inputs.filter(input => input.status === 'warning').length
  const reviews = inputs.filter(input => input.status === 'needs_review').length
  const prioritizedInputs = prioritizeHealthInputs(inputs)
  const notableInputs = prioritizedInputs.filter(input => input.status !== 'pass' && input.status !== 'info')
  const detailInputs = notableInputs.length > 0 ? notableInputs : prioritizedInputs.slice(0, 3)
  return { blockers, warnings: warnings + reviews, detailInputs, summaryInput: detailInputs[0] }
}

function githubStatusLabel(status?: GitHubScriptStatus) {
  if (!status?.linked) return ''
  switch (status.status) {
    case 'synced':
      return 'GitHub synced'
    case 'synced_local':
      return 'GitHub linked'
    case 'local_drift':
      return 'GitHub local drift'
    case 'remote_drift':
      return 'GitHub remote drift'
    case 'diverged':
      return 'GitHub diverged'
    case 'remote_missing':
      return 'GitHub missing'
    default:
      return 'GitHub linked'
  }
}

function githubStatusClass(status?: GitHubScriptStatus) {
  if (!status?.linked) return ''
  if (status.status === 'synced') return 'healthy'
  if (status.status === 'local_drift' || status.status === 'remote_drift' || status.status === 'synced_local') return 'warning'
  if (status.status === 'diverged' || status.status === 'remote_missing') return 'danger'
  return ''
}

function remoteItemMetadata(item: LibraryCatalogItem) {
  return [
    `Project ${item.project_id || 'unmapped'}`,
    `Hash ${shortLibraryHash(item.content_hash)}`,
    item.dependencies?.length ? `${item.dependencies.length} dependencies` : '',
  ].filter(Boolean)
}

function healthInputPriority(status?: string) {
  switch (status) {
    case 'blocker':
      return 0
    case 'warning':
    case 'needs_review':
      return 1
    case 'pass':
      return 3
    case 'info':
      return 4
    default:
      return 2
  }
}

function prioritizeHealthInputs(inputs: AuthoringHealthInput[]) {
  return inputs
    .map((input, index) => ({ input, index }))
    .sort((left, right) => {
      const priorityDelta = healthInputPriority(left.input.status) - healthInputPriority(right.input.status)
      return priorityDelta !== 0 ? priorityDelta : left.index - right.index
    })
    .map(({ input }) => input)
}

export default function ScriptsLibraryTable({
  scripts,
  sortField,
  sortDirection,
  onSort,
  expandedScriptId,
  onToggleScriptRuns,
  loadingRunsFor,
  scriptRuns,
  innerSortField,
  innerSortDirection,
  onInnerSort,
  getSortedInnerRuns,
  onSelectScenario,
  onEdit,
  onDelete,
  canManageScripts = true,
  emptyMessage = 'No scripts yet.',
  onCreate,
  federation,
  github,
  secretStatusByScript = {},
  secretStatusStateByScript = {},
  onConfigureSecrets,
  onRetrySecretStatus,
}: Props) {
  const [issueDialog, setIssueDialog] = useState<{ title: string; issues: IssueDetail[] } | null>(null)
  const showSource = Boolean(federation)
  const columnCount = (showSource ? 1 : 0) + 7
  const showLocal = !federation || federation.sourceFilter !== 'host'
  const localScripts = showLocal ? scripts : []
  const remoteRows = federation && federation.sourceFilter !== 'local' ? federation.remoteRows : []
  const isEmpty = localScripts.length === 0 && remoteRows.length === 0
  return (
    <div className="data-table">
      <table className="scripts-library-table">
        <thead>
          <tr>
            {showSource && <th>Source</th>}
            <SortHeader field="name" activeField={sortField} direction={sortDirection} label="Script" onActivate={onSort} />
            <SortHeader field="authoring_health" activeField={sortField} direction={sortDirection} label="Health" onActivate={onSort} />
            <SortHeader field="scenario_count" activeField={sortField} direction={sortDirection} label="Usage" onActivate={onSort} />
            <SortHeader field="associated_releases" activeField={sortField} direction={sortDirection} label="Associated Releases" onActivate={onSort} />
            <SortHeader field="created_at" activeField={sortField} direction={sortDirection} label="Create Date" onActivate={onSort} />
            <SortHeader field="last_run_at" activeField={sortField} direction={sortDirection} label="Last Run" onActivate={onSort} />
            <th style={{ textAlign: 'right' }}>Actions</th>
          </tr>
        </thead>
        <tbody>
          {localScripts.map((script, scriptIndex) => {
            const scriptId = script.id || ''
            const scriptAddressable = isAddressableScriptId(script.id)
            const rowKey = scriptAddressable
              ? scriptId
              : `legacy-unaddressable:${script.ref_key || script.name}:${scriptIndex}`
            const healthSummary = authoringHealthSummary(script)
            const secretStatus = scriptAddressable ? secretStatusByScript[scriptId] : undefined
            const secretStatusState = scriptAddressable ? secretStatusStateByScript[scriptId] : 'error'
            const missingSecrets = secretStatus?.missing || []
            const requiresSecrets = (secretStatus?.required.length || 0) > 0 || scriptContainsSecretReferences(script)
            const secretStatusUnknown = !scriptAddressable
              || (requiresSecrets && secretStatusState !== undefined && secretStatusState !== 'ready')
            const configureSecretsAvailable = scriptAddressable
              && onConfigureSecrets
              && (missingSecrets.length > 0 || secretStatusUnknown)
            const hostItem = scriptAddressable ? federation?.hostItemForLocal(scriptId) : undefined
            const syncStatus = scriptAddressable ? federation?.syncStatusForLocal(scriptId) || 'local only' : 'unavailable'
            const conflict = syncStatus === 'conflict'
            return (
              <Fragment key={rowKey}>
                <tr>
                  {showSource && (
                    <td>
                      <span className="script-health-token">Local</span>
                    </td>
                  )}
                  <td className="cell-name">
                    <button
                      type="button"
                      className="script-row-toggle"
                      aria-expanded={scriptAddressable && expandedScriptId === scriptId}
                      aria-controls={scriptAddressable ? `script-runs-${scriptId}` : undefined}
                      disabled={!scriptAddressable}
                      title={scriptAddressable ? undefined : 'This legacy script has no addressable ID and must be repaired before it can be opened.'}
                      onClick={() => {
                        if (scriptAddressable) onToggleScriptRuns(script)
                      }}
                    >
                      {script.name}
                    </button>
                    <div className="script-table-meta">
                      <span className="protocol-badge">{script.protocol}</span>
                      <span className="script-health-token">v{script.version || '—'}</span>
                      <ReferenceKeyBadge value={script.ref_key} />
                      {scriptAddressable && githubStatusLabel(github?.statusByScript?.[scriptId]) && (
                        <span
                          className={`script-health-token ${githubStatusClass(github?.statusByScript?.[scriptId])}`}
                          title={github?.statusByScript?.[scriptId]?.html_url}
                        >
                          {githubStatusLabel(github?.statusByScript?.[scriptId])}
                        </span>
                      )}
                    </div>
                  </td>
                  <td aria-label="Scenario usage">
                    <span className={`protocol-badge ${secretStatusUnknown ? 'warning' : authoringHealthClass(script)}`}>
                      {secretStatusUnknown
                        ? !scriptAddressable
                          ? 'Script unavailable'
                          : secretStatusState === 'loading' ? 'Checking secrets' : 'Secret status unavailable'
                        : authoringHealthLabel(script)}
                    </span>
                    {(script.authoring_health || missingSecrets.length > 0 || secretStatusUnknown) && (
                      <div className="script-health-inline">
                        {!secretStatusUnknown && script.authoring_health && healthSummary.blockers > 0 && (
                          <button
                            type="button"
                            className="script-health-token danger issue-pill-button"
                            aria-label={`View ${healthSummary.blockers} blocker ${healthSummary.blockers === 1 ? 'detail' : 'details'} for ${script.name}`}
                            onClick={() => setIssueDialog({
                              title: `${script.name} blockers`,
                              issues: script.authoring_health!.inputs
                                .filter(input => input.status === 'blocker')
                                .map((input, index) => ({
                                  id: `${input.key}-${index}`,
                                  severity: 'blocker',
                                  title: input.key.replace(/_/g, ' '),
                                  description: input.summary,
                                  count: input.count,
                                  source: 'Authoring health',
                                })),
                            })}
                          >
                            {healthSummary.blockers} blocker{healthSummary.blockers === 1 ? '' : 's'}
                          </button>
                        )}
                        {!secretStatusUnknown && script.authoring_health && healthSummary.warnings > 0 && (
                          <button
                            type="button"
                            className="script-health-token warning issue-pill-button"
                            aria-label={`View ${healthSummary.warnings} warning ${healthSummary.warnings === 1 ? 'detail' : 'details'} for ${script.name}`}
                            onClick={() => setIssueDialog({
                              title: `${script.name} warnings`,
                              issues: script.authoring_health!.inputs
                                .filter(input => input.status === 'warning' || input.status === 'needs_review')
                                .map((input, index) => ({
                                  id: `${input.key}-${index}`,
                                  severity: 'warning',
                                  title: input.key.replace(/_/g, ' '),
                                  description: input.summary,
                                  count: input.count,
                                  source: input.status === 'needs_review' ? 'Needs review' : 'Authoring health',
                                })),
                            })}
                          >
                            {healthSummary.warnings} warning{healthSummary.warnings === 1 ? '' : 's'}
                          </button>
                        )}
                        {!secretStatusUnknown && script.authoring_health && healthSummary.blockers === 0 && healthSummary.warnings === 0 && <span className="script-health-token healthy">ready</span>}
                        {secretStatusUnknown && (
                          <button
                            type="button"
                            className="script-health-token warning issue-pill-button"
                            aria-label={`View secret status details for ${script.name}`}
                            onClick={() => setIssueDialog({
                              title: `${script.name} secret status`,
                              issues: [{
                                id: 'secret-status',
                                severity: 'warning',
                                title: !scriptAddressable
                                  ? 'Missing script ID'
                                  : secretStatusState === 'loading' ? 'Secret status pending' : 'Secret status unavailable',
                                description: !scriptAddressable
                                  ? 'This legacy script has no addressable ID and must be repaired before its credential configuration can be checked.'
                                  : secretStatusState === 'loading'
                                    ? 'Credential configuration is still being checked. This script is not considered ready until the check completes.'
                                    : 'Credential configuration could not be verified. Retry the check or open Project Secrets to configure values.',
                                source: 'Project secrets',
                              }],
                            })}
                          >
                            {!scriptAddressable
                              ? 'missing script id'
                              : secretStatusState === 'loading' ? 'secret status pending' : 'secret status unknown'}
                          </button>
                        )}
                        {missingSecrets.length > 0 && (
                          <button
                            type="button"
                            className="script-health-token warning issue-pill-button"
                            aria-label={`View ${missingSecrets.length} missing secret ${missingSecrets.length === 1 ? 'detail' : 'details'} for ${script.name}`}
                            onClick={() => setIssueDialog({
                              title: `${script.name} missing secrets`,
                              issues: missingSecrets.map((name, index) => ({
                                id: `${name}-${index}`,
                                severity: 'warning',
                                title: name,
                                description: 'This required project secret has not been configured.',
                                source: 'Project secrets',
                              })),
                            })}
                          >
                            {missingSecrets.length} secret{missingSecrets.length === 1 ? '' : 's'} missing
                          </button>
                        )}
                      </div>
                    )}
                  </td>
                  <td aria-label="Scenario usage">
                    <div className="script-table-usage">
                      <strong>{formatScenarioCount(script)}</strong>
                      <span>{scenarioRunReleaseList(script)}</span>
                    </div>
                  </td>
                  <td aria-label="Associated releases">
                    <div className="script-table-usage">
                      <span>{releaseList(script)}</span>
                    </div>
                  </td>
                  <td className="cell-muted">{formatDate(script.created_at)}</td>
                  <td className="cell-muted">{formatDate(script.last_run_at)}</td>
                  <td style={{ textAlign: 'right' }}>
                    {!scriptAddressable ? (
                      <span className="cell-muted" title="This legacy row has no script ID. Repair it before using script actions.">
                        Unavailable
                      </span>
                    ) : canManageScripts ? (
                      <>
                        <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); onEdit(scriptId) }}>Edit</button>
                        {configureSecretsAvailable && (
                          <button className="btn btn-sm btn-secondary" onClick={(event) => { event.stopPropagation(); onConfigureSecrets?.(script) }}>Configure secrets</button>
                        )}
                        {secretStatusUnknown && secretStatusState === 'error' && onRetrySecretStatus && (
                          <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); onRetrySecretStatus(script) }}>Retry secret check</button>
                        )}
                        <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={(event) => { event.stopPropagation(); onDelete(scriptId) }}>Delete</button>
                        {github?.enabled && (
                          <button
                            className="btn btn-sm btn-ghost"
                            onClick={(event) => { event.stopPropagation(); github.onPush(script) }}
                            title="Push this script to GitHub"
                          >GitHub</button>
                        )}
                        {federation && (
                          <>
                            <button
                              className="btn btn-sm btn-secondary"
                              onClick={(event) => { event.stopPropagation(); federation.onPushScript(script) }}
                              disabled={federation.actionBlocked || conflict}
                              title={conflict ? 'Compare this conflict before transferring.' : federation.actionBlockedReason}
                            >Push</button>
                            {hostItem?.local_item_id && (
                              <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); federation.onCompare(scriptId, hostItem) }}>Compare</button>
                            )}
                          </>
                        )}
                      </>
                    ) : (
                      <span className="cell-muted">Read-only</span>
                    )}
                  </td>
                </tr>
                {scriptAddressable && expandedScriptId === scriptId && (
                  <tr className="no-hover" id={`script-runs-${scriptId}`}>
                    <td colSpan={columnCount} style={{ background: 'var(--bg-primary)' }}>
                      <div style={{ padding: '10px 4px' }}>
                        <div className="cell-muted" style={{ marginBottom: 8 }}>
                          Linked Scenario Runs {loadingRunsFor === scriptId ? '(loading...)' : `(${(scriptRuns[scriptId] || []).length})`}
                        </div>
                        {script.authoring_health?.inputs?.length ? (
                          <div className="script-health-detail">
                            <span className="results-compare-label">Authoring Health Inputs</span>
                            <div className="archive-report-badges">
                              {healthSummary.detailInputs.map(input => (
                                <span key={`${input.key}-${input.status}`} className={`archive-chip ${input.status === 'blocker' ? 'danger' : ''}`}>
                                  {input.key.replace(/_/g, ' ')} · {input.status}{input.count !== undefined ? ` · ${input.count}` : ''}
                                </span>
                              ))}
                            </div>
                            <div className="cell-muted">{healthSummary.summaryInput?.summary}</div>
                          </div>
                        ) : null}
                        {missingSecrets.length > 0 && (
                          <div className="script-health-detail">
                            <span className="results-compare-label">Missing Project Secrets</span>
                            <div className="archive-report-badges">
                              {missingSecrets.map(name => <span key={name} className="archive-chip danger">{name}</span>)}
                            </div>
                            {onConfigureSecrets && (
                              <button className="btn btn-secondary btn-sm" type="button" onClick={() => onConfigureSecrets(script)}>
                                Configure secrets
                              </button>
                            )}
                          </div>
                        )}
                        {secretStatusUnknown && (
                          <div className="script-health-detail" role="status">
                            <span className="results-compare-label">
                              {secretStatusState === 'loading' ? 'Checking Project Secrets' : 'Project Secret Status Unavailable'}
                            </span>
                            <div className="cell-muted">
                              {secretStatusState === 'loading'
                                ? 'Credential configuration is still being checked. This script is not considered ready until the check completes.'
                                : 'Credential configuration could not be verified. Retry the check or open Project Secrets to configure values.'}
                            </div>
                            <div className="settings-inline-actions">
                              {onConfigureSecrets && (
                                <button className="btn btn-secondary btn-sm" type="button" onClick={() => onConfigureSecrets(script)}>
                                  Configure secrets
                                </button>
                              )}
                              {secretStatusState === 'error' && onRetrySecretStatus && (
                                <button className="btn btn-secondary btn-sm" type="button" onClick={() => onRetrySecretStatus(script)}>
                                  Retry secret check
                                </button>
                              )}
                            </div>
                          </div>
                        )}
                        {(scriptRuns[scriptId] || []).length === 0 ? (
                          <div className="cell-muted">No linked scenario runs for this script yet.</div>
                        ) : (
                          <div className="data-table">
                            <table className="scripts-library-runs-table">
                              <thead>
                                <tr>
                                  <SortHeader field="name" activeField={innerSortField} direction={innerSortDirection} label="Run Name" onActivate={onInnerSort} />
                                  <SortHeader field="status" activeField={innerSortField} direction={innerSortDirection} label="Status" onActivate={onInnerSort} />
                                  <SortHeader field="scenario_id" activeField={innerSortField} direction={innerSortDirection} label="Scenario ID" onActivate={onInnerSort} />
                                  <SortHeader field="release_id" activeField={innerSortField} direction={innerSortDirection} label="Release ID" onActivate={onInnerSort} />
                                  <SortHeader field="start_time" activeField={innerSortField} direction={innerSortDirection} label="Started" onActivate={onInnerSort} />
                                  <SortHeader field="duration" activeField={innerSortField} direction={innerSortDirection} label="Duration" onActivate={onInnerSort} />
                                </tr>
                              </thead>
                              <tbody>
                                {getSortedInnerRuns(scriptRuns[scriptId] || []).map((run) => (
                                  <tr key={run.id}>
                                    <td className="cell-name">
                                      {onSelectScenario ? (
                                        <button type="button" className="script-run-link" onClick={() => onSelectScenario(run.id)}>
                                          {run.name}
                                        </button>
                                      ) : <div>{run.name}</div>}
                                      <div className="script-table-meta">
                                        <ReferenceKeyBadge value={run.ref_key} />
                                      </div>
                                    </td>
                                    <td>{run.status}</td>
                                    <td className="cell-mono">{run.scenario_id || '—'}</td>
                                    <td className="cell-mono">{run.release_id || '—'}</td>
                                    <td className="cell-muted">{formatDateTime(run.start_time || run.created_at)}</td>
                                    <td className="cell-muted">{run.duration}</td>
                                  </tr>
                                ))}
                              </tbody>
                            </table>
                          </div>
                        )}
                      </div>
                    </td>
                  </tr>
                )}
              </Fragment>
            )
          })}
          {federation && remoteRows.map((item) => {
            const syncStatus = item.sync_status || 'host only'
            const conflict = syncStatus === 'conflict'
            const href = federation.remoteItemHref(item)
            return (
              <tr key={`remote:${item.kind}:${item.id}`} className="no-hover">
                <td>
                  <span className="script-health-token">Remote</span>
                </td>
                <td className="cell-name">
                  <div>{item.name || item.id}</div>
                  <div className="script-table-meta">
                    {item.protocol && <span className="protocol-badge">{item.protocol}</span>}
                    {item.version && <span className="script-health-token">v{item.version}</span>}
                    {item.has_archive && <span className="script-health-token healthy">archive</span>}
                  </div>
                </td>
                <td className="cell-muted">—</td>
                <td>
                  <div className="script-table-usage">
                    {remoteItemMetadata(item).map(piece => <span key={piece}>{piece}</span>)}
                  </div>
                </td>
                <td className="cell-muted">—</td>
                <td className="cell-muted">—</td>
                <td className="cell-muted">{formatLibraryDateTime(item.updated_at)}</td>
                <td style={{ textAlign: 'right' }}>
                  {canManageScripts ? (
                    <>
                      <button
                        className="btn btn-sm btn-secondary"
                        onClick={() => federation.onPullRemote(item)}
                        disabled={federation.actionBlocked || conflict}
                        title={conflict ? 'Compare this conflict before transferring.' : federation.actionBlockedReason}
                      >Pull</button>
                      {item.local_item_id && (
                        <button className="btn btn-sm btn-ghost" onClick={() => federation.onCompare(item.local_item_id!, item)}>Compare</button>
                      )}
                      {href && <a className="btn btn-sm btn-ghost" href={href} target="_blank" rel="noreferrer">Open in Remote Workspace</a>}
                    </>
                  ) : (
                    <span className="cell-muted">Read-only</span>
                  )}
                </td>
              </tr>
            )
          })}
          {isEmpty && (
            <tr>
              <td colSpan={columnCount}>
                <div className="settings-inline-actions script-library-empty-actions">
                  <span className="cell-muted">{emptyMessage}</span>
                  {onCreate && canManageScripts && (
                    <button type="button" className="btn btn-primary btn-sm" onClick={onCreate}>Create your first script</button>
                  )}
                </div>
              </td>
            </tr>
          )}
        </tbody>
      </table>
      {issueDialog && (
        <IssueDetailsDialog
          title={issueDialog.title}
          issues={issueDialog.issues}
          onClose={() => setIssueDialog(null)}
        />
      )}
    </div>
  )
}
