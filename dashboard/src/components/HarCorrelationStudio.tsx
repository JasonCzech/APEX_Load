import { Fragment, useId, useMemo, useState } from 'react'
import type { HARDiffClassification, HARDiffReport, HARImportStats, SalesforceReview } from '../types/scriptImports'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { formatUpTo } from '../utils/numberFormat'
import { findBlockedUnexportedBodyActions, flattenScriptActions } from './harCorrelationStudioModel'
import './HarCorrelationStudio.css'

const formatInteger = (value: number): string => formatUpTo(value, 0)

export interface HarCorrelationStudioProps {
  script: ScriptDefinition
  stats?: Partial<HARImportStats> & Record<string, unknown>
  harDiff?: HARDiffReport
  salesforceReview?: SalesforceReview | null
  duplicateFileWarning?: string
  busy?: boolean
  onExcludeBlockedActions?: (options: { actionUids?: string[]; excludeUnexportedBodies?: boolean }) => void
}

type StudioFilter = 'all' | 'dynamic' | 'wired' | 'review' | 'release_static'

type LifecycleStageId = 'stage1_init' | 'stage2_aura_context' | 'stage3_business' | 'stage4_teardown' | 'generic'

interface StudioRow {
  key: string
  variableName: string
  stage: LifecycleStageId
  statusKind: 'wired' | 'warn' | 'review' | 'static'
  statusLabel: string
  diffStatus: string
  isDynamic: boolean
  isWired: boolean
  needsReview: boolean
  isReleaseStatic: boolean
  producerLabel: string
  consumerLabel: string
  extractorSummary: string
  guidance: string
}

const DEFAULT_VISIBLE_ROWS = 15

function classifySalesforceStage(
  varName: string,
  producerSection: 'Init' | 'Actions' | 'Teardown' | null,
  isSalesforce: boolean,
): LifecycleStageId {
  if (!isSalesforce) return 'generic'
  const lower = varName.toLowerCase()
  if (producerSection === 'Teardown' || lower.includes('logout')) return 'stage4_teardown'
  if (lower.includes('fwuid') || lower.includes('aura_context') || lower.includes('loaded')) return 'stage2_aura_context'
  if (
    producerSection === 'Init'
    || lower.includes('aura_token')
    || lower.includes('sid')
    || lower.includes('cshc')
    || lower.includes('frontdoor')
    || lower.includes('contentdoor')
    || lower.includes('recsession')
    || lower.includes('salesforce_users')
  ) {
    return 'stage1_init'
  }
  return 'stage3_business'
}

const STAGE_TITLES: Record<LifecycleStageId, string> = {
  stage1_init: 'Stage 1 · Desktop sign-in & session bootstrap (Init)',
  stage2_aura_context: 'Stage 2 · Release-coupled Aura framework context',
  stage3_business: 'Stage 3 · Business record & dynamic action correlations',
  stage4_teardown: 'Stage 4 · Logout & session cleanup (Teardown)',
  generic: 'Captured correlations & comparison findings',
}

const STAGE_ORDER: LifecycleStageId[] = [
  'stage1_init',
  'stage2_aura_context',
  'stage3_business',
  'stage4_teardown',
  'generic',
]

function actionConsumesVariable(action: ScriptActionDefinition, varName: string): boolean {
  const needle = `{{var.${varName}}}`
  const checkText = (text: unknown) => typeof text === 'string' && text.includes(needle)
  if (checkText(action.target) || checkText(action.body)) return true
  if (Array.isArray(action.headers)) {
    if (action.headers.some(h => checkText(h?.value))) return true
  } else if (action.headers && typeof action.headers === 'object') {
    if (Object.values(action.headers).some(checkText)) return true
  }
  if (action.params && typeof action.params === 'object') {
    try {
      if (JSON.stringify(action.params).includes(needle)) return true
    } catch {
      return false
    }
  }
  return false
}

export default function HarCorrelationStudio({
  script,
  stats,
  harDiff,
  salesforceReview,
  duplicateFileWarning,
  busy = false,
  onExcludeBlockedActions,
}: HarCorrelationStudioProps) {
  const headingId = useId()
  const [filter, setFilter] = useState<StudioFilter>('all')
  const [expanded, setExpanded] = useState(false)

  const flattened = useMemo(() => flattenScriptActions(script), [script])
  const blockedActions = useMemo(() => findBlockedUnexportedBodyActions(script), [script])
  const isSalesforce = Boolean(salesforceReview?.detected)

  const phaseCounts = useMemo(() => {
    let init = 0
    let actions = 0
    let teardown = 0
    for (const item of flattened) {
      if (item.section === 'Init') init += 1
      else if (item.section === 'Teardown') teardown += 1
      else actions += 1
    }
    return { init, actions, teardown, total: init + actions + teardown }
  }, [flattened])

  const funnel = useMemo(() => {
    const hasServerTotalRequests = typeof stats?.total_requests === 'number' && stats.total_requests > 0
    const filteredTelemetry = typeof stats?.filtered_telemetry === 'number' ? stats.filtered_telemetry : 0
    if (hasServerTotalRequests) {
      const filteredTotal = typeof stats?.filtered_assets === 'number' ? stats.filtered_assets : filteredTelemetry
      const filteredStatic = Math.max(0, filteredTotal - filteredTelemetry)
      const totalEntries = typeof stats?.total_entries === 'number' && stats.total_entries > 0
        ? stats.total_entries
        : (stats.total_requests ?? 0)
      const totalHttp = typeof stats?.converted_actions === 'number' && stats.converted_actions > 0
        ? stats.converted_actions
        : phaseCounts.total
      return {
        totalEntries,
        filteredTotal,
        filteredStatic,
        filteredTelemetry,
        totalHttp,
      }
    }
    const totalHttp = phaseCounts.total
    const filteredStatic = typeof stats?.filtered_assets === 'number' ? stats.filtered_assets : 0
    const totalEntries = typeof stats?.total_entries === 'number' && stats.total_entries > 0
      ? stats.total_entries
      : totalHttp + filteredStatic + filteredTelemetry
    return {
      totalEntries,
      filteredTotal: filteredStatic + filteredTelemetry,
      filteredStatic,
      filteredTelemetry,
      totalHttp,
    }
  }, [phaseCounts.total, stats])

  const rows = useMemo<StudioRow[]>(() => {
    const byName = new Map<string, StudioRow>()
    const diffByName = new Map<string, HARDiffClassification>()
    for (const item of harDiff?.classifications || []) {
      if (item?.name) diffByName.set(item.name, item)
    }

    // 1. Data pool credentials hoisted for Salesforce desktop sign-in
    for (const ds of script.data_sources || []) {
      if (ds.name === 'salesforce_users') {
        byName.set('data.salesforce_users', {
          key: 'data.salesforce_users',
          variableName: 'salesforce_users (username, password)',
          stage: 'stage1_init',
          statusKind: 'wired',
          statusLabel: 'Data pool wired (unique/VUser)',
          diffStatus: harDiff?.compared ? 'Per-VUser credential pool' : 'Single-HAR pool',
          isDynamic: false,
          isWired: true,
          needsReview: true,
          isReleaseStatic: false,
          producerLabel: 'CSV data source (salesforce_users)',
          consumerLabel: 'Init sign-in POST (un, pw)',
          extractorSummary: 'strategy: unique · secret column: password',
          guidance: 'Populate one row per concurrent VUser (exempt from interactive MFA) before load execution.',
        })
      }
    }

    // 2. All extractors wired into the generated script
    for (const entry of flattened) {
      const extractors = Array.isArray(entry.action.extractors) ? entry.action.extractors : []
      for (const ext of extractors) {
        const varName = typeof ext?.name === 'string' ? ext.name.trim() : ''
        if (!varName || byName.has(varName)) continue
        const consumers = flattened.filter(candidate => candidate.seq > entry.seq && actionConsumesVariable(candidate.action, varName))
        const diff = diffByName.get(varName)
        const isUncapturedAuraToken = varName.includes('aura_token')
          && typeof ext.expression === 'string'
          && ext.expression.includes('ERIC_')
        const isAuraContextRelease = varName.toLowerCase().includes('fwuid') || varName.toLowerCase().includes('aura_context')
        const stage = classifySalesforceStage(varName, entry.section, isSalesforce)
        const isDynamic = diff?.classification === 'dynamic' || (!diff && !isAuraContextRelease)
        const scopeRedirectNote = ext.scope && (ext.scope as { redirects?: string }).redirects === 'include'
          ? ' (incl. 302 redirect hops)'
          : ''

        let statusKind: StudioRow['statusKind'] = 'wired'
        let statusLabel = 'Auto-correlated'
        let guidance = diff?.reason || 'Extracted from server response and substituted into downstream requests.'
        let needsReview = false

        if (isUncapturedAuraToken) {
          statusKind = 'warn'
          statusLabel = 'Expected at live replay'
          needsReview = true
          guidance = 'Confirmed via auraConfig.eikoocnekot in /one/one.app HTML; browser sanitized Set-Cookie (__Host-ERIC_*) from HAR export.'
        } else if (isAuraContextRelease) {
          statusKind = 'static'
          statusLabel = 'Release-coupled'
          guidance = 'Same-day captures share fwuid/app hashes; extractor refreshes automatically after Salesforce seasonal or cache updates.'
        }

        const diffStatus = diff
          ? `${diff.classification} (${Math.round((diff.diff_confidence || 0) * 100)}%)`
          : harDiff?.compared
            ? 'Native rule'
            : 'Single-HAR rule'

        byName.set(varName, {
          key: varName,
          variableName: varName,
          stage,
          statusKind,
          statusLabel,
          diffStatus,
          isDynamic,
          isWired: true,
          needsReview,
          isReleaseStatic: isAuraContextRelease,
          producerLabel: `${entry.section} #${entry.seq} · ${entry.action.name || entry.action.target || entry.path}`,
          consumerLabel: consumers.length > 0
            ? `${consumers.length} step${consumers.length === 1 ? '' : 's'} (first: ${consumers[0].section} #${consumers[0].seq})`
            : '0 downstream steps',
          extractorSummary: `${ext.type || 'extract'} on ${ext.source || 'body'}${scopeRedirectNote}`,
          guidance,
        })
      }
    }

    // 3. Remaining 2-HAR diff classifications not already wired as extractors
    for (const item of harDiff?.classifications || []) {
      const varName = item?.name?.trim()
      if (!varName || byName.has(varName)) continue
      const isStatic = item.classification === 'static'
      const isAuraContext = varName.toLowerCase().includes('fwuid') || varName.toLowerCase().includes('aura.context') || varName.toLowerCase().includes('loaded')
      const stage = isAuraContext && isSalesforce
        ? 'stage2_aura_context'
        : classifySalesforceStage(varName, null, isSalesforce)
      const hasKnownProducer = item.producer_found === true || (typeof item.found_in_step === 'number' && item.found_in_step > 0)
      const statusKind: StudioRow['statusKind'] = isStatic ? 'static' : 'review'
      const statusLabel = isStatic
        ? (isAuraContext ? 'Static same-day (release-coupled)' : 'Static across pair')
        : hasKnownProducer
          ? 'Dynamic · candidate extractor'
          : 'Dynamic · client/parameter candidate'

      byName.set(varName, {
        key: `diff-${varName}`,
        variableName: varName,
        stage,
        statusKind,
        statusLabel,
        diffStatus: `${item.classification} (${Math.round((item.diff_confidence || 0) * 100)}%)`,
        isDynamic: item.classification === 'dynamic' || item.classification === 'candidate',
        isWired: false,
        needsReview: !isStatic,
        isReleaseStatic: isStatic || isAuraContext,
        producerLabel: typeof item.found_in_step === 'number' && item.found_in_step > 0
          ? `Step #${item.found_in_step}${item.matched_descriptor ? ` (${item.matched_descriptor})` : ''}`
          : 'Not observed in server response (client nonce/counter or input)',
        consumerLabel: Array.isArray(item.used_in_steps) && item.used_in_steps.length > 0
          ? `${item.used_in_steps.length} step${item.used_in_steps.length === 1 ? '' : 's'}`
          : 'Observed in compared request',
        extractorSummary: item.extract_type ? `${item.extract_type} (${item.source || 'response'})` : 'Diff observation only (not auto-wired)',
        guidance: item.reason || (hasKnownProducer
          ? 'Differed across the two HARs with a candidate server producer; review before wiring.'
          : 'Differed across the two HARs without a server producer; keep literal or bind to {{fn.*}} / parameter if required.'),
      })
    }

    // 4. If Salesforce logout steps were hoisted to Teardown, include a lifecycle confirmation row
    if (isSalesforce && phaseCounts.teardown > 0 && !Array.from(byName.values()).some(r => r.stage === 'stage4_teardown')) {
      byName.set('lifecycle.teardown.logout', {
        key: 'lifecycle.teardown.logout',
        variableName: 'secur/logout.jsp',
        stage: 'stage4_teardown',
        statusKind: 'wired',
        statusLabel: 'Hoisted to Teardown',
        diffStatus: 'Lifecycle step',
        isDynamic: false,
        isWired: true,
        needsReview: false,
        isReleaseStatic: false,
        producerLabel: `Teardown (${phaseCounts.teardown} step${phaseCounts.teardown === 1 ? '' : 's'})`,
        consumerLabel: 'Runs once after final VUser iteration',
        extractorSummary: 'Session sign-out',
        guidance: 'Moved out of timed iteration loop so VUsers retain their authenticated session across iterations.',
      })
    }

    return Array.from(byName.values())
  }, [flattened, harDiff, isSalesforce, phaseCounts.teardown, script.data_sources])

  const filterCounts = useMemo(() => ({
    all: rows.length,
    dynamic: rows.filter(r => r.isDynamic).length,
    wired: rows.filter(r => r.isWired).length,
    review: rows.filter(r => r.needsReview).length,
    release_static: rows.filter(r => r.isReleaseStatic).length,
  }), [rows])

  const filteredRows = useMemo(() => {
    switch (filter) {
      case 'dynamic':
        return rows.filter(r => r.isDynamic)
      case 'wired':
        return rows.filter(r => r.isWired)
      case 'review':
        return rows.filter(r => r.needsReview)
      case 'release_static':
        return rows.filter(r => r.isReleaseStatic)
      default:
        return rows
    }
  }, [filter, rows])

  const visibleRows = expanded ? filteredRows : filteredRows.slice(0, DEFAULT_VISIBLE_ROWS)

  const groupedVisibleRows = useMemo(() => {
    const groups: Array<{ stage: LifecycleStageId; items: StudioRow[] }> = []
    for (const stage of STAGE_ORDER) {
      const items = visibleRows.filter(r => r.stage === stage)
      if (items.length > 0) groups.push({ stage, items })
    }
    return groups
  }, [visibleRows])

  return (
    <section className="har-correlation-studio" aria-labelledby={headingId}>
      <h4 id={headingId}>Correlation studio &amp; capture funnel</h4>

      <div className="har-capture-funnel" role="group" aria-label="HAR capture conversion funnel">
        <div className="har-funnel-step">
          <span className="har-funnel-label">Captured HAR entries</span>
          <span className="har-funnel-value">{formatInteger(funnel.totalEntries)}</span>
          <span className="har-funnel-detail">Raw browser log entries</span>
        </div>
        <div className="har-funnel-step">
          <span className="har-funnel-label">Filtered static &amp; telemetry</span>
          <span className="har-funnel-value">{formatInteger(funnel.filteredTotal)}</span>
          <span className="har-funnel-detail">
            {formatInteger(funnel.filteredStatic)} static · {formatInteger(funnel.filteredTelemetry)} telemetry
          </span>
        </div>
        <div className="har-funnel-step">
          <span className="har-funnel-label">HTTP requests converted</span>
          <span className="har-funnel-value">{formatInteger(funnel.totalHttp)}</span>
          <span className="har-funnel-detail">Executable protocol requests</span>
        </div>
        <div className="har-funnel-step">
          <span className="har-funnel-label">DSL lifecycle placement</span>
          <span className="har-funnel-value">{formatInteger(phaseCounts.total)} steps</span>
          <span className="har-funnel-detail">
            Init {formatInteger(phaseCounts.init)} · Actions {formatInteger(phaseCounts.actions)} · Teardown {formatInteger(phaseCounts.teardown)}
          </span>
        </div>
      </div>

      {duplicateFileWarning && (
        <div className="sample-import-warning" role="alert">
          <strong>Identical comparison HAR detected:</strong> {duplicateFileWarning}
        </div>
      )}

      {harDiff && !harDiff.compared && harDiff.reason && !duplicateFileWarning && (
        <div className="sample-import-warning" role="alert">
          <strong>Two-HAR comparison skipped:</strong> {harDiff.reason}
        </div>
      )}

      {blockedActions.length > 0 && (
        <div className="har-blocker-banner" role="region" aria-label="Requests missing exported bodies">
          <div className="har-blocker-banner-header">
            <h5 className="har-blocker-banner-title">
              {blockedActions.length} request{blockedActions.length === 1 ? '' : 's'} omitted request body in HAR export (blocks replay)
            </h5>
            {onExcludeBlockedActions && (blockedActions.length > 1 || blockedActions.some(item => !item.uid)) && (
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                disabled={busy}
                onClick={() => onExcludeBlockedActions({ excludeUnexportedBodies: true })}
              >
                Exclude all {blockedActions.length} blocked {blockedActions.length === 1 ? 'request' : 'requests'} from draft
              </button>
            )}
          </div>
          <p className="har-blocker-guidance">
            Browser HAR exports often omit large POST bodies (or client telemetry payloads). Exclude non-essential requests below to unblock replay immediately, or re-export from Chrome DevTools using <strong>Export HAR (with sensitive data)</strong> / record with APEX Recording Studio if the request carries required business state.
          </p>
          <ul className="har-blocker-list">
            {blockedActions.map(item => (
              <li key={item.uid || item.path} className="har-blocker-item">
                <div className="har-blocker-meta">
                  <strong>{item.section} · {item.name}</strong>
                  <span className="har-funnel-detail">
                    Path: <code>{item.path}</code>
                    {item.recordedBytes ? ` · Recorded Content-Length: ${formatInteger(item.recordedBytes)} bytes` : ''}
                  </span>
                </div>
                {onExcludeBlockedActions && (
                  <button
                    type="button"
                    className="btn btn-primary btn-sm"
                    disabled={busy || !item.uid}
                    title={!item.uid ? 'Action lacks an individual import UID; use bulk exclude above.' : undefined}
                    onClick={() => {
                      if (item.uid) onExcludeBlockedActions({ actionUids: [item.uid] })
                    }}
                  >
                    Exclude action from draft
                  </button>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}

      {rows.length > 0 && (
        <>
          <div className="har-studio-filters" role="group" aria-label="Filter correlation studio rows">
            {([
              ['all', `All (${filterCounts.all})`],
              ['wired', `Auto-correlated (${filterCounts.wired})`],
              ['dynamic', `Dynamic across pair (${filterCounts.dynamic})`],
              ['review', `Needs review (${filterCounts.review})`],
              ['release_static', `Release-coupled / static (${filterCounts.release_static})`],
            ] as const).map(([key, label]) => (
              <button
                key={key}
                type="button"
                className="har-studio-chip"
                aria-pressed={filter === key}
                onClick={() => { setFilter(key); setExpanded(false) }}
              >
                {label}
              </button>
            ))}
          </div>

          <div
            className="har-studio-table-region table-responsive"
            tabIndex={0}
            role="region"
            aria-label="Correlation studio classification table"
          >
            <table className="har-studio-table">
              <thead>
                <tr>
                  <th scope="col">Variable / token</th>
                  <th scope="col">Status</th>
                  <th scope="col">2-HAR comparison</th>
                  <th scope="col">Producer</th>
                  <th scope="col">Consumers</th>
                  <th scope="col">Mechanism &amp; guidance</th>
                </tr>
              </thead>
              <tbody>
                {groupedVisibleRows.map(group => (
                  <Fragment key={group.stage}>
                    {isSalesforce && (
                      <tr className="har-studio-stage-row">
                        <th colSpan={6} scope="colgroup">{STAGE_TITLES[group.stage]}</th>
                      </tr>
                    )}
                    {group.items.map(row => (
                      <tr key={row.key}>
                        <th scope="row"><code>{row.variableName}</code></th>
                        <td>
                          <span className={`har-status-pill har-status-pill--${row.statusKind}`}>
                            {row.statusLabel}
                          </span>
                        </td>
                        <td>{row.diffStatus}</td>
                        <td>{row.producerLabel}</td>
                        <td>{row.consumerLabel}</td>
                        <td>
                          <div><strong>{row.extractorSummary}</strong></div>
                          <div className="har-funnel-detail">{row.guidance}</div>
                        </td>
                      </tr>
                    ))}
                  </Fragment>
                ))}
                {groupedVisibleRows.length === 0 && (
                  <tr>
                    <td colSpan={6}>No correlations match the selected filter.</td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          {filteredRows.length > DEFAULT_VISIBLE_ROWS && (
            <div className="har-studio-expand-row">
              <span className="har-funnel-detail">
                Showing {formatInteger(visibleRows.length)} of {formatInteger(filteredRows.length)} correlation items
              </span>
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                onClick={() => setExpanded(current => !current)}
              >
                {expanded ? `Show top ${DEFAULT_VISIBLE_ROWS}` : `Show all ${formatInteger(filteredRows.length)} correlations`}
              </button>
            </div>
          )}
        </>
      )}
    </section>
  )
}
