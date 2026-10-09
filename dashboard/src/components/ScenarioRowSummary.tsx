import type { ReactNode } from 'react'
import type { StoredScenario } from '../types'
import { formatBrowserRuntimeSummary } from './browserRuntimeSummary'
import { ObjectInfoBadge } from './LibraryRowBadges'
import BadgeIcon from './ux/BadgeIcon'

function scenarioScripts(scenario: StoredScenario) {
  const scripts = scenario.scripts || []
  if (scripts.length === 0) return 'No scripts assigned'
  return scripts.map(script => script.script_name || script.name || script.script_id).join(', ')
}

/** The sum of each group's peak VUsers: the most the scenario can run at once. */
function peakVUsers(scenario: StoredScenario) {
  return (scenario.scripts || []).reduce((total, group) => total + Math.max(group.vusers || 0, ...((group.stages || []).map(stage => stage.target_vusers || 0))), 0)
}

function hostTargets(scenario: StoredScenario) {
  if (scenario.assignment?.mode !== 'specific_hosts') return null
  const targets = scenario.assignment.selected_targets || []
  return targets.length ? targets.join(', ') : 'No hosts selected'
}

function dataPoolList(scenario: StoredScenario) {
  return (scenario.data_pools || []).map(pool => `${pool.alias} (${pool.pool_name || pool.pool_id})`).join(', ')
}

function resultCount(scenario: StoredScenario) {
  const count = scenario.result_count || 0
  return `${count} result${count === 1 ? '' : 's'}`
}

/**
 * A scenario row's identity line: an info badge (reference key, history, host
 * placement, browser settings, data pools) plus short tokens for what changes
 * how it runs. children adds rows to the info badge's details.
 */
export function ScenarioNameMeta({ scenario, children }: { scenario: StoredScenario; children?: ReactNode }) {
  const hosts = hostTargets(scenario)
  const browser = formatBrowserRuntimeSummary(scenario.runtime_settings?.browser)
  const pools = dataPoolList(scenario)
  return <div className="script-table-meta">
    <ObjectInfoBadge name={scenario.name} refKey={scenario.ref_key} createdAt={scenario.created_at} activity={scenario.activity}>
      {hosts && <div><dt>Hosts</dt><dd>{hosts}</dd></div>}
      {browser && <div><dt>Browser</dt><dd>{browser}</dd></div>}
      <div><dt>Data pools</dt><dd>{pools || 'None'}</dd></div>
      {children}
    </ObjectInfoBadge>
    {scenario.archived_at && <span className="script-health-token">Archived</span>}
    {hosts && <span className="script-health-token" title={`Specific hosts: ${hosts}`}>Specific Hosts</span>}
    {browser && <span className="script-health-token" title={`Browser: ${browser}`}>Browser</span>}
  </div>
}

/** The workload at a glance: scripts and duration, peak VUsers and model, plus badges for data pools and scripts that need review. */
export function ScenarioWorkloadSummary({ scenario, scriptsNeedReview }: { scenario: StoredScenario; scriptsNeedReview: boolean }) {
  const poolCount = scenario.data_pools?.length || 0
  return <div className="scenario-table-workload">
    <strong>{scenarioScripts(scenario)} · {scenario.duration}</strong>
    <span title="Sum of each group's peak VUsers">{peakVUsers(scenario)} VUsers · {scenario.workload_shape_id || 'Custom model'}</span>
    {(poolCount > 0 || scriptsNeedReview) && <span className="script-health-badges">
      {poolCount > 0 && <span className="script-health-token" title={`Data pools: ${dataPoolList(scenario)}`}>{poolCount} data {poolCount === 1 ? 'pool' : 'pools'}</span>}
      {scriptsNeedReview && <span className="script-health-token script-health-badge static danger" title="A script in this scenario has authoring blockers. Resolve them in script review before creating a run."><BadgeIcon kind="ban" />Script review required</span>}
    </span>}
  </div>
}

/** Results and the releases they ran under; the count alone says when there are none. */
export function ScenarioUsageSummary({ scenario }: { scenario: StoredScenario }) {
  const releases = Array.from(new Set((scenario.associated_releases || []).map(release => release.name))).join(', ')
  return <div className="script-table-usage">
    <strong>{resultCount(scenario)}</strong>
    {releases && <span>{releases}</span>}
  </div>
}
