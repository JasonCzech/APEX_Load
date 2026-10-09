import ObjectHistory from './ObjectHistory'
import ObjectArchiveControls from './ObjectArchiveControls'
import { assignmentSummary } from './libraryAssignmentModel'
import LibraryPagination from './LibraryPagination'
import { useLibraryPagination } from './useLibraryPagination'
import RowActions from './ux/RowActions'
import { Fragment, lazy, Suspense, useState } from 'react'

import { Folder, GitHubScriptStatus, StoredScript, TestSummary } from '../types'
import type { ScriptValidationResponse } from '../types/scripts'
import { LibraryCatalogItem, LibrarySourceFilter } from '../types/library'
import { type ScriptSecretStatus } from '../types/secrets'
import { formatLibraryDateTime, shortLibraryHash } from './libraryFederationUtils'
import { SortIndicator } from './SortIndicator'
import { ScriptLibrarySortField, ScriptRunSortField } from './useScriptsLibraryModel'
import ReferenceKeyBadge from './ReferenceKeyBadge'
import { HealthCountBadge, ScriptHealthBadge, ScriptInfoBadge } from './LibraryRowBadges'
import { authoringHealthCounts, prioritizeHealthInputs } from './authoringHealthInputs'
import { isAddressableScriptId } from './scriptIdentity'
import type { IssueDetail } from './IssueDetailsDialog'
import { scriptContainsSecretReferences } from './scriptSecretReferenceModel'
import SelectAllCheckbox from './SelectAllCheckbox'
import StatusBadge from './StatusBadge'
import { formatDateTime as formatDisplayDateTime } from '../utils/dateTime'

const IssueDetailsDialog = lazy(() => import('./IssueDetailsDialog'))

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
  /** Keeps the rows-per-page choice with the library's filters. */
  paginationScope?: string
  onArchiveChanged?: () => void | Promise<void>
  scripts: StoredScript[]
  folders?: Folder[]
  sortField: ScriptLibrarySortField
  sortDirection: 'asc' | 'desc'
  onSort: (field: ScriptLibrarySortField) => void
  expandedScriptId: string | null
  onToggleScriptRuns: (script: StoredScript) => void
  loadingRunsFor: string | null
  scriptRuns: Record<string, TestSummary[]>
  scriptRunErrors?: Record<string, string>
  onRetryScriptRuns?: (script: StoredScript) => void
  innerSortField: ScriptRunSortField
  innerSortDirection: 'asc' | 'desc'
  onInnerSort: (field: ScriptRunSortField) => void
  getSortedInnerRuns: (runs: TestSummary[]) => TestSummary[]
  onSelectScenario?: (id: string) => void
  onEdit: (id: string) => void
  onChangeAssignment?: (script: StoredScript) => void
  onMove?: (script: StoredScript) => void
  movingScriptIds?: Set<string>
  onDelete: (id: string) => void
  selectedScriptIds?: Set<string>
  onToggleScriptSelection?: (id: string) => void
  onToggleAllVisibleScripts?: (checked: boolean) => void
  deletingScriptIds?: Set<string>
  canManageScripts?: boolean
  emptyMessage?: string
  onCreate?: () => void
  federation?: ScriptLibraryFederation
  github?: ScriptLibraryGitHub
  secretStatusByScript?: Record<string, ScriptSecretStatus>
  secretStatusStateByScript?: Record<string, 'loading' | 'ready' | 'error'>
  onConfigureSecrets?: (script: StoredScript) => void
  onRetrySecretStatus?: (script: StoredScript) => void
  onValidateScriptDetails?: (id: string) => Promise<ScriptValidationResponse>
}

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : 'Never'
}

function formatDateTime(value?: string) {
  return formatDisplayDateTime(value)
}

function releaseList(script: StoredScript) {
  return (script.associated_releases || []).map((release) => release.name).join(', ')
}

function scenarioRunReleaseList(script: StoredScript) {
  return (script.scenario_run_releases || []).map((release) => release.name).join(', ')
}

function formatScenarioCount(script: StoredScript) {
  const count = script.scenario_count || 0
  return `${count} scenario${count === 1 ? '' : 's'}`
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
  const prioritizedInputs = prioritizeHealthInputs(script.authoring_health?.inputs || [])
  const notableInputs = prioritizedInputs.filter(input => input.status !== 'pass' && input.status !== 'info')
  const detailInputs = notableInputs.length > 0 ? notableInputs : prioritizedInputs.slice(0, 3)
  return { ...authoringHealthCounts(script), detailInputs, summaryInput: detailInputs[0] }
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

export default function ScriptsLibraryTable({
  paginationScope,
  onArchiveChanged,
  scripts,
  folders = [],
  sortField,
  sortDirection,
  onSort,
  expandedScriptId,
  onToggleScriptRuns,
  loadingRunsFor,
  scriptRuns,
  scriptRunErrors = {},
  onRetryScriptRuns,
  innerSortField,
  innerSortDirection,
  onInnerSort,
  getSortedInnerRuns,
  onSelectScenario,
  onEdit,
  onDelete,
  onMove,
  onChangeAssignment,
  movingScriptIds = new Set(),
  selectedScriptIds = new Set(),
  onToggleScriptSelection,
  onToggleAllVisibleScripts,
  deletingScriptIds = new Set(),
  canManageScripts = true,
  emptyMessage = 'No scripts yet.',
  onCreate,
  federation,
  github,
  secretStatusByScript = {},
  secretStatusStateByScript = {},
  onConfigureSecrets,
  onRetrySecretStatus,
  onValidateScriptDetails,
}: Props) {
  const [showDetailColumns, setShowDetailColumns] = useState(false)
  const [issueDialog, setIssueDialog] = useState<{ title: string; issues: IssueDetail[] } | null>(null)
  const openBlockerIssues = async (script: StoredScript) => {
    const blockerInputs = script.authoring_health?.inputs.filter(input => input.status === 'blocker') || []
    const validationInput = blockerInputs.find(input => input.key === 'validation_issues')
    const summaryIssues: IssueDetail[] = blockerInputs
      .filter(input => input.key !== 'validation_issues')
      .map((input, index) => ({
        id: `${input.key}-${index}`,
        severity: 'blocker',
        title: input.key.replace(/_/g, ' '),
        description: input.summary,
        count: input.count,
        source: 'Authoring health',
      }))
    if (!validationInput || !onValidateScriptDetails || !isAddressableScriptId(script.id)) {
      setIssueDialog({
        title: `${script.name} blockers`,
        issues: [
          ...(validationInput ? [{
            id: 'validation-issues',
            severity: 'blocker' as const,
            title: 'validation issues',
            description: validationInput.summary,
            count: validationInput.count,
            source: 'Authoring health',
          }] : []),
          ...summaryIssues,
        ],
      })
      return
    }

    setIssueDialog({
      title: `${script.name} blockers`,
      issues: [{
        id: 'validation-loading',
        severity: 'info',
        title: 'Loading validation details',
        description: 'Checking the stored script for exact validation failures…',
        source: 'Script validation',
      }],
    })
    try {
      const result = await onValidateScriptDetails(script.id)
      const exactIssues: IssueDetail[] = (result.issues || []).map((issue, index) => ({
        id: `validation-${index}`,
        severity: 'blocker',
        title: `Validation issue ${index + 1}`,
        description: issue,
        source: 'Script validation',
      }))
      setIssueDialog({
        title: `${script.name} blockers`,
        issues: [
          ...(exactIssues.length > 0 ? exactIssues : [{
            id: 'validation-empty',
            severity: 'info' as const,
            title: 'Validation passed',
            description: 'No current validation issues were returned. Refresh the library to update its health summary.',
            source: 'Script validation',
          }]),
          ...summaryIssues,
        ],
      })
    } catch (error) {
      setIssueDialog({
        title: `${script.name} blockers`,
        issues: [{
          id: 'validation-error',
          severity: 'warning',
          title: 'Validation details unavailable',
          description: error instanceof Error ? error.message : 'The validation request failed.',
          source: 'Script validation',
        }, ...summaryIssues],
      })
    }
  }
  const folderNameById = new Map(folders.map(folder => [folder.id, folder.name]))
  const showLocal = !federation || federation.sourceFilter !== 'host'
  const allLocalScripts = showLocal ? scripts : []
  const allRemoteRows = federation && federation.sourceFilter !== 'local' ? federation.remoteRows : []
  // Every row is local unless a remote workspace contributes rows, so the Source column only appears then.
  const showSource = allRemoteRows.length > 0
  const pagination = useLibraryPagination(allLocalScripts.length + allRemoteRows.length, JSON.stringify([allLocalScripts.map(script => script.id), allRemoteRows.map(item => item.id)]), paginationScope ?? null)
  const localScripts = allLocalScripts.slice(pagination.start, pagination.end)
  // Script names are not unique, but two scripts with one name in one folder
  // are told apart only by reference key; flag them like VuGen/LRE would.
  const duplicateNameKey = (script: StoredScript) => `${script.folder_id || ''}\u001f${(script.name || '').trim().toLowerCase()}`
  const nameCounts = new Map<string, number>()
  for (const script of allLocalScripts) nameCounts.set(duplicateNameKey(script), (nameCounts.get(duplicateNameKey(script)) || 0) + 1)
  const remoteRows = allRemoteRows.slice(Math.max(0, pagination.start - allLocalScripts.length), Math.max(0, pagination.end - allLocalScripts.length))
  const selectableScriptCount = localScripts.filter(script => isAddressableScriptId(script.id)).length
  const selectedVisibleScriptCount = localScripts.filter(script => isAddressableScriptId(script.id) && selectedScriptIds.has(script.id!)).length
  const showSelection = canManageScripts && Boolean(onToggleScriptSelection) && Boolean(onToggleAllVisibleScripts)
  const columnCount = (showSelection ? 1 : 0) + (showSource ? 1 : 0) + 9
  const isEmpty = localScripts.length === 0 && remoteRows.length === 0
  if (isEmpty && onCreate && canManageScripts) return <div className="empty-card">
    <p>{emptyMessage}</p><p className="empty-hint">Create or import a script to begin building your test library.</p>
    <button type="button" className="btn btn-primary" onClick={onCreate}>Create your first script</button>
  </div>
  return (
    <div className="data-table">
      <LibraryPagination label="Script library" pagination={pagination} />
      <table data-table-pagination="off" className={`scripts-library-table ${showDetailColumns ? 'show-detail-columns' : ''}`}>
        <caption className="script-table-column-control"><button className="btn btn-secondary btn-sm" type="button" aria-expanded={showDetailColumns} onClick={() => setShowDetailColumns(value => !value)}>{showDetailColumns ? 'Hide usage & dates' : 'Show usage & dates'}</button></caption>
        <thead>
          <tr>
            {showSelection && (
              <th className="library-selection-column">
                <SelectAllCheckbox
                  selectedCount={selectedVisibleScriptCount}
                  visibleCount={selectableScriptCount}
                  ariaLabel="Select all scripts on this page"
                  onChange={checked => localScripts.forEach(script => { if (isAddressableScriptId(script.id) && selectedScriptIds.has(script.id!) !== checked) onToggleScriptSelection?.(script.id!) })}
                />
              </th>
            )}
            {showSource && <th>Source</th>}
            <SortHeader field="name" activeField={sortField} direction={sortDirection} label="Script" onActivate={onSort} />
            <SortHeader field="folder" activeField={sortField} direction={sortDirection} label="Folder" onActivate={onSort} />
            <SortHeader field="authoring_health" activeField={sortField} direction={sortDirection} label="Authoring health" onActivate={onSort} />
            <SortHeader field="scenario_count" activeField={sortField} direction={sortDirection} label="Usage" onActivate={onSort} />
            <th>Assignment</th>
            <SortHeader field="associated_releases" activeField={sortField} direction={sortDirection} label="Run releases" onActivate={onSort} />
            <SortHeader field="created_at" activeField={sortField} direction={sortDirection} label="Created" onActivate={onSort} />
            <SortHeader field="last_run_at" activeField={sortField} direction={sortDirection} label="Last run" onActivate={onSort} />
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
              && requiresSecrets
            const hostItem = scriptAddressable ? federation?.hostItemForLocal(scriptId) : undefined
            const syncStatus = scriptAddressable ? federation?.syncStatusForLocal(scriptId) || 'local only' : 'unavailable'
            const conflict = syncStatus === 'conflict'
            const duplicateName = (nameCounts.get(duplicateNameKey(script)) || 0) > 1
            return (
              <Fragment key={rowKey}>
                <tr className={selectedScriptIds.has(scriptId) ? 'selected' : undefined}>
                  {showSelection && (
                    <td className="library-selection-column">
                      {scriptAddressable ? (
                        <input
                          type="checkbox"
                          aria-label={`Select ${script.name}`}
                          checked={selectedScriptIds.has(scriptId)}
                          disabled={deletingScriptIds.has(scriptId)}
                          onClick={event => event.stopPropagation()}
                          onChange={() => onToggleScriptSelection?.(scriptId)}
                        />
                      ) : <span className="cell-muted">—</span>}
                    </td>
                  )}
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
                      {/* The name expands this script's runs; Edit opens it. Say so visually. */}
                      {scriptAddressable && <span className="script-row-chevron" aria-hidden="true">{expandedScriptId === scriptId ? '▾' : '▸'}</span>}
                      {script.name}
                    </button>
                    <div className="script-table-meta">
                      <span className="protocol-badge">{script.protocol}</span>
                      <span className="script-health-token">v{script.version || '—'}</span>
                      {/* Reference key and who created or changed it live behind one info badge. */}
                      <ScriptInfoBadge name={script.name} refKey={script.ref_key} createdAt={script.created_at} activity={script.activity} duplicateName={duplicateName} />
                      {script.archived_at && <span className="script-health-token">Archived</span>}
                      {duplicateName && <span className="script-health-token warning" title="Another script in this folder has the same name.">Duplicate name</span>}
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
                  <td>{script.folder_id ? folderNameById.get(script.folder_id) || script.folder_id : <span className="cell-muted">Unfiled</span>}</td>
                  <td aria-label="Authoring health">
                    {/* Colour-coded badges only; each opens the detail behind it. */}
                    <div className="script-health-badges">
                      <ScriptHealthBadge
                        name={script.name}
                        health={script.authoring_health}
                        inputs={prioritizeHealthInputs(script.authoring_health?.inputs || [])}
                        secretState={!scriptAddressable ? 'unaddressable' : !secretStatusUnknown ? 'ready' : secretStatusState === 'loading' ? 'loading' : 'error'}
                        missingSecrets={missingSecrets}
                        onConfigureSecrets={configureSecretsAvailable ? () => onConfigureSecrets?.(script) : undefined}
                        onRetrySecretCheck={onRetrySecretStatus && scriptAddressable ? () => onRetrySecretStatus(script) : undefined}
                      />
                      {!secretStatusUnknown && healthSummary.blockers > 0 && (
                        <HealthCountBadge
                          tone="danger"
                          icon="cross"
                          count={healthSummary.blockers}
                          label={`View ${healthSummary.blockers} blocker ${healthSummary.blockers === 1 ? 'detail' : 'details'} for ${script.name}`}
                          onOpen={() => { void openBlockerIssues(script) }}
                        />
                      )}
                      {!secretStatusUnknown && healthSummary.warnings > 0 && (
                        <HealthCountBadge
                          tone="warning"
                          icon="alert"
                          count={healthSummary.warnings}
                          label={`View ${healthSummary.warnings} warning ${healthSummary.warnings === 1 ? 'detail' : 'details'} for ${script.name}`}
                          onOpen={() => setIssueDialog({
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
                        />
                      )}
                      {missingSecrets.length > 0 && (
                        <HealthCountBadge
                          tone="warning"
                          icon="key"
                          count={missingSecrets.length}
                          label={`View ${missingSecrets.length} missing secret ${missingSecrets.length === 1 ? 'detail' : 'details'} for ${script.name}`}
                          onOpen={() => {
                            if (onConfigureSecrets) {
                              onConfigureSecrets(script)
                              return
                            }
                            setIssueDialog({
                              title: `${script.name} missing secrets`,
                              issues: missingSecrets.map((name, index) => ({
                                id: `${name}-${index}`,
                                severity: 'warning',
                                title: name,
                                description: 'This required project secret has not been configured.',
                                source: 'Project secrets',
                              })),
                            })
                          }}
                        />
                      )}
                    </div>
                  </td>
                  <td aria-label="Scenario usage">
                    <div className="script-table-usage">
                      <strong>{formatScenarioCount(script)}</strong>
                      {/* Run releases are listed when there are any; "0 scenarios" already says there are none. */}
                      {(script.scenario_run_releases?.length || 0) > 0 && <span>{scenarioRunReleaseList(script)}</span>}
                    </div>
                  </td>
                  <td aria-label="Assignment">{assignmentSummary(script.library_assignment)}</td>
                  <td aria-label="Run releases">
                    {(script.associated_releases?.length || 0) > 0
                      ? <div className="script-table-usage"><span>{releaseList(script)}</span></div>
                      : <span className="cell-muted">—</span>}
                  </td>
                  <td className="cell-muted">{formatDate(script.created_at)}</td>
                  <td className="cell-muted">{formatDate(script.last_run_at)}</td>
                  <td className="library-row-actions-cell" style={{ textAlign: 'right' }}>
                    {!scriptAddressable ? (
                      <span className="cell-muted" title="This legacy row has no script ID. Repair it before using script actions.">
                        Unavailable
                      </span>
                    ) : canManageScripts ? (
                      <>
                        <button className="btn btn-sm btn-ghost" disabled={movingScriptIds.has(scriptId)} aria-label={`${script.archived_at ? 'View' : 'Edit'} ${script.name}`} onClick={(event) => { event.stopPropagation(); onEdit(scriptId) }}>{script.archived_at ? "View" : "Edit"}</button>
                        <RowActions className="library-more-actions" label={script.name}>
                        {!script.archived_at && onChangeAssignment && <button className="btn btn-sm btn-ghost" disabled={deletingScriptIds.has(scriptId) || movingScriptIds.has(scriptId)} onClick={() => onChangeAssignment(script)}>Change application/releases…</button>}
                        {!script.archived_at && onMove && <button className="btn btn-sm btn-ghost" disabled={deletingScriptIds.has(scriptId) || movingScriptIds.has(scriptId)} onClick={() => onMove(script)}>Move to folder…</button>}
                        {!script.archived_at && configureSecretsAvailable && (
                          <button className="btn btn-sm btn-secondary" onClick={(event) => { event.stopPropagation(); onConfigureSecrets?.(script) }}>Configure secrets</button>
                        )}
                        {secretStatusUnknown && secretStatusState === 'error' && onRetrySecretStatus && (
                          <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); onRetrySecretStatus(script) }}>Retry secret check</button>
                        )}
                        <ObjectArchiveControls inMenu resourceType="script" resourceId={scriptId} resourceName={script.name} archivedAt={script.archived_at} updatedAt={script.script?.updated_at} canManage={!!canManageScripts} onChanged={onArchiveChanged || (() => undefined)} />
                        <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger-text)' }} disabled={deletingScriptIds.has(scriptId) || movingScriptIds.has(scriptId)} onClick={(event) => { event.stopPropagation(); onDelete(scriptId) }}>{deletingScriptIds.has(scriptId) ? 'Deleting…' : 'Delete'}</button>
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
                              aria-describedby={conflict || (federation.actionBlocked && federation.actionBlockedReason) ? `push-blocked-${scriptId}` : undefined}
                            >Push</button>
                            {(conflict || (federation.actionBlocked && federation.actionBlockedReason)) && <p id={`push-blocked-${scriptId}`} className="ux-row-actions-note">{conflict ? 'Compare this conflict before transferring.' : federation.actionBlockedReason}</p>}
                            {hostItem?.local_item_id && (
                              <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); federation.onCompare(scriptId, hostItem) }}>Compare</button>
                            )}
                          </>
                        )}
                        </RowActions>
                      </>
                    ) : (
                      <span className="cell-muted">Read-only</span>
                    )}
                  </td>
                </tr>
                {scriptAddressable && expandedScriptId === scriptId && (
                  <tr data-table-detail="true" className="no-hover" id={`script-runs-${scriptId}`}>
                    <td colSpan={columnCount} style={{ background: 'var(--bg-primary)' }}>
                      <ObjectHistory key={scriptId} projectId={script.project_id} resourceType="script" resourceId={scriptId} activity={script.activity} />
                      <div style={{ padding: '10px 4px' }}>
                        <div className="cell-muted" style={{ marginBottom: 8 }}>
                          Linked Scenario Runs {loadingRunsFor === scriptId ? '(loading…)' : `(${(scriptRuns[scriptId] || []).length})`}
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
                        {scriptRunErrors[scriptId] ? (
                          <div className="script-health-detail" role="alert">
                            <span className="results-compare-label">Linked Runs Unavailable</span>
                            <div className="cell-muted">{scriptRunErrors[scriptId]}</div>
                            {onRetryScriptRuns && (
                              <button className="btn btn-secondary btn-sm" type="button" onClick={() => onRetryScriptRuns(script)}>
                                Retry
                              </button>
                            )}
                          </div>
                        ) : loadingRunsFor === scriptId ? (
                          <div className="cell-muted" role="status">Loading linked scenario runs…</div>
                        ) : (scriptRuns[scriptId] || []).length === 0 ? (
                          <div className="cell-muted">No linked scenario runs for this script yet.</div>
                        ) : (
                          <div className="data-table">
                            <table data-table-pagination="on" className="scripts-library-runs-table">
                              <thead>
                                <tr>
                                  <SortHeader field="name" activeField={innerSortField} direction={innerSortDirection} label="Run Name" onActivate={onInnerSort} />
                                  <SortHeader field="status" activeField={innerSortField} direction={innerSortDirection} label="Status" onActivate={onInnerSort} />
                                  <SortHeader field="scenario_id" activeField={innerSortField} direction={innerSortDirection} label="Scenario ID" onActivate={onInnerSort} />
                                  <SortHeader field="release_id" activeField={innerSortField} direction={innerSortDirection} label="Release ID" onActivate={onInnerSort} />
                                  <SortHeader field="start_time" activeField={innerSortField} direction={innerSortDirection} label="Started" onActivate={onInnerSort} />
                                  <SortHeader field="duration" activeField={innerSortField} direction={innerSortDirection} label="Configured duration" onActivate={onInnerSort} />
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
                                    <td><StatusBadge status={run.status} stoppedByOperator={Boolean(run.stopped_by_subject)} /></td>
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
                {showSelection && <td className="library-selection-column"><span className="cell-muted">—</span></td>}
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
        <Suspense fallback={null}>
          <IssueDetailsDialog
            title={issueDialog.title}
            issues={issueDialog.issues}
            onClose={() => setIssueDialog(null)}
          />
        </Suspense>
      )}
    </div>
  )
}
