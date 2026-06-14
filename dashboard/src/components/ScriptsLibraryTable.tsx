import { Fragment } from 'react'

import { GitHubScriptStatus, StoredScript, TestSummary } from '../types'
import { LibraryCatalogItem, LibrarySourceFilter } from '../types/library'
import { formatLibraryDateTime, shortLibraryHash } from './libraryFederationUtils'
import { ScriptLibrarySortField, ScriptRunSortField } from './useScriptsLibraryModel'
import ReferenceKeyBadge from './ReferenceKeyBadge'

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
  federation?: ScriptLibraryFederation
  github?: ScriptLibraryGitHub
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

function authoringHealthSummary(script: StoredScript) {
  const inputs = script.authoring_health?.inputs || []
  const blockers = inputs.filter(input => input.status === 'blocker').length
  const warnings = inputs.filter(input => input.status === 'warning').length
  const reviews = inputs.filter(input => input.status === 'needs_review').length
  return { blockers, warnings: warnings + reviews, topInputs: inputs.slice(0, 3) }
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

function SortIndicator({ active, direction }: { active: boolean, direction: 'asc' | 'desc' }) {
  return (
    <span className={`sort-icon ${active ? 'active' : ''}`}>
      {active ? (direction === 'asc' ? '↑' : '↓') : '↕'}
    </span>
  )
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
  federation,
  github,
}: Props) {
  const showSource = Boolean(federation)
  const columnCount = (showSource ? 1 : 0) + 7
  const showLocal = !federation || federation.sourceFilter !== 'host'
  const localScripts = showLocal ? scripts : []
  const remoteRows = federation && federation.sourceFilter !== 'local' ? federation.remoteRows : []
  const isEmpty = localScripts.length === 0 && remoteRows.length === 0

  return (
    <div className="data-table">
      <table>
        <thead>
          <tr>
            {showSource && <th>Source</th>}
            <th onClick={() => onSort('name')} className="sortable">Script <SortIndicator active={sortField === 'name'} direction={sortDirection} /></th>
            <th onClick={() => onSort('authoring_health')} className="sortable">Health <SortIndicator active={sortField === 'authoring_health'} direction={sortDirection} /></th>
            <th onClick={() => onSort('scenario_count')} className="sortable">Usage <SortIndicator active={sortField === 'scenario_count'} direction={sortDirection} /></th>
            <th onClick={() => onSort('associated_releases')} className="sortable">Associated Releases <SortIndicator active={sortField === 'associated_releases'} direction={sortDirection} /></th>
            <th onClick={() => onSort('created_at')} className="sortable">Create Date <SortIndicator active={sortField === 'created_at'} direction={sortDirection} /></th>
            <th onClick={() => onSort('last_run_at')} className="sortable">Last Run <SortIndicator active={sortField === 'last_run_at'} direction={sortDirection} /></th>
            <th style={{ textAlign: 'right' }}>Actions</th>
          </tr>
        </thead>
        <tbody>
          {localScripts.map((script) => {
            const healthSummary = authoringHealthSummary(script)
            const hostItem = federation?.hostItemForLocal(script.id)
            const syncStatus = federation?.syncStatusForLocal(script.id) || 'local only'
            const conflict = syncStatus === 'conflict'
            return (
              <Fragment key={script.id}>
                <tr key={script.id} className="clickable-row" onClick={() => onToggleScriptRuns(script)}>
                  {showSource && (
                    <td>
                      <span className="script-health-token">Local</span>
                    </td>
                  )}
                  <td className="cell-name">
                    <div>{script.name}</div>
                    <div className="script-table-meta">
                      <span className="protocol-badge">{script.protocol}</span>
                      <span className="script-health-token">v{script.version || '—'}</span>
                      <ReferenceKeyBadge value={script.ref_key} />
                      {githubStatusLabel(github?.statusByScript?.[script.id]) && (
                        <span
                          className={`script-health-token ${githubStatusClass(github?.statusByScript?.[script.id])}`}
                          title={github?.statusByScript?.[script.id]?.html_url}
                        >
                          {githubStatusLabel(github?.statusByScript?.[script.id])}
                        </span>
                      )}
                    </div>
                  </td>
                  <td>
                    <span className={`protocol-badge ${authoringHealthClass(script)}`}>{authoringHealthLabel(script)}</span>
                    {script.authoring_health && (
                      <div className="script-health-inline">
                        {healthSummary.blockers > 0 && <span className="script-health-token danger">{healthSummary.blockers} blockers</span>}
                        {healthSummary.warnings > 0 && <span className="script-health-token warning">{healthSummary.warnings} warnings</span>}
                        {healthSummary.blockers === 0 && healthSummary.warnings === 0 && <span className="script-health-token healthy">ready</span>}
                      </div>
                    )}
                  </td>
                  <td>
                    <div className="script-table-usage">
                      <strong>{formatScenarioCount(script)}</strong>
                      <span>{scenarioRunReleaseList(script)}</span>
                    </div>
                  </td>
                  <td>
                    <div className="script-table-usage">
                      <span>{releaseList(script)}</span>
                    </div>
                  </td>
                  <td className="cell-muted">{formatDate(script.created_at)}</td>
                  <td className="cell-muted">{formatDate(script.last_run_at)}</td>
                  <td style={{ textAlign: 'right' }}>
                    {canManageScripts ? (
                      <>
                        <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); onEdit(script.id) }}>Edit</button>
                        <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={(event) => { event.stopPropagation(); onDelete(script.id) }}>Delete</button>
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
                              <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); federation.onCompare(script.id, hostItem) }}>Compare</button>
                            )}
                          </>
                        )}
                      </>
                    ) : (
                      <span className="cell-muted">Read-only</span>
                    )}
                  </td>
                </tr>
                {expandedScriptId === script.id && (
                  <tr className="no-hover">
                    <td colSpan={columnCount} style={{ background: 'var(--bg-primary)' }}>
                      <div style={{ padding: '10px 4px' }}>
                        <div className="cell-muted" style={{ marginBottom: 8 }}>
                          Linked Scenario Runs {loadingRunsFor === script.id ? '(loading...)' : `(${(scriptRuns[script.id] || []).length})`}
                        </div>
                        {script.authoring_health?.inputs?.length ? (
                          <div className="script-health-detail">
                            <span className="results-compare-label">Authoring Health Inputs</span>
                            <div className="archive-report-badges">
                              {healthSummary.topInputs.map(input => (
                                <span key={`${input.key}-${input.status}`} className={`archive-chip ${input.status === 'blocker' ? 'danger' : ''}`}>
                                  {input.key.replace(/_/g, ' ')} · {input.status}{input.count !== undefined ? ` · ${input.count}` : ''}
                                </span>
                              ))}
                            </div>
                            <div className="cell-muted">{healthSummary.topInputs[0]?.summary}</div>
                          </div>
                        ) : null}
                        {(scriptRuns[script.id] || []).length === 0 ? (
                          <div className="cell-muted">No linked scenario runs for this script yet.</div>
                        ) : (
                          <div className="data-table">
                            <table>
                              <thead>
                                <tr>
                                  <th onClick={() => onInnerSort('name')} className="sortable">
                                    Run Name <SortIndicator active={innerSortField === 'name'} direction={innerSortDirection} />
                                  </th>
                                  <th onClick={() => onInnerSort('status')} className="sortable">
                                    Status <SortIndicator active={innerSortField === 'status'} direction={innerSortDirection} />
                                  </th>
                                  <th onClick={() => onInnerSort('scenario_id')} className="sortable">
                                    Scenario ID <SortIndicator active={innerSortField === 'scenario_id'} direction={innerSortDirection} />
                                  </th>
                                  <th onClick={() => onInnerSort('release_id')} className="sortable">
                                    Release ID <SortIndicator active={innerSortField === 'release_id'} direction={innerSortDirection} />
                                  </th>
                                  <th onClick={() => onInnerSort('start_time')} className="sortable">
                                    Started <SortIndicator active={innerSortField === 'start_time'} direction={innerSortDirection} />
                                  </th>
                                  <th onClick={() => onInnerSort('duration')} className="sortable">
                                    Duration <SortIndicator active={innerSortField === 'duration'} direction={innerSortDirection} />
                                  </th>
                                </tr>
                              </thead>
                              <tbody>
                                {getSortedInnerRuns(scriptRuns[script.id] || []).map((run) => (
                                  <tr key={run.id} className="clickable-row" onClick={(event) => { event.stopPropagation(); onSelectScenario?.(run.id) }}>
                                    <td className="cell-name">
                                      <div>{run.name}</div>
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
          {isEmpty && <tr><td colSpan={columnCount} className="cell-muted">{emptyMessage}</td></tr>}
        </tbody>
      </table>
    </div>
  )
}
