import { Fragment, useEffect, useId, useMemo, useRef, useState } from 'react'
import type { HARDiffClassification, HARDiffReport, HARImportStats, SalesforceReview } from '../types/scriptImports'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { formatUpTo } from '../utils/numberFormat'
import { admitNativeCorrelationEvidence, type AdmittedCorrelationMetadata } from './correlationEvidenceAdmission'
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
  /** Where Recording Studio opens, to re-record what the HAR export left out. */
  recordingStudioHref?: string
}

type StudioFilter = 'all' | 'dynamic' | 'wired' | 'review' | 'release_static'

// Salesforce HARs group by the four lifecycle stages a Lightning session goes
// through; any other HAR groups by where its values come from.
type LifecycleStageId =
  | 'sf_identity'
  | 'sf_bootstrap'
  | 'sf_aura'
  | 'sf_client'
  | 'generic_auth'
  | 'generic_app'
  | 'generic_client'

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

// Correlation rules (pkg/convert/correlate/rules.go) by the lifecycle stage
// whose responses issue their values.
const IDENTITY_RULES = new Set([
  'salesforce.frontdoor_sid',
  'salesforce.cshc',
  'salesforce.confirmation_token',
  'salesforce.oauth_identity_url',
  'salesforce.oauth_authorize_redirect',
])
const BOOTSTRAP_RULES = new Set([
  'salesforce.aura_context_fwuid',
  'salesforce.aura_loaded',
  'salesforce.aura_app_descriptor',
  'salesforce.aura_page_uri',
  'salesforce.aura_token',
  'salesforce.aura_token_cookie',
  'salesforce.aura_token_uncaptured_cookie',
])
const RELEASE_COUPLED_RULES = new Set(['salesforce.aura_context_fwuid', 'salesforce.aura_loaded'])
const UNCAPTURED_COOKIE_RULE = 'salesforce.aura_token_uncaptured_cookie'
const RELEASE_COUPLED_REASON = 'same_day_release_coupled'

function isReleaseCoupled(metadata: AdmittedCorrelationMetadata | undefined): boolean {
  if (!metadata) return false
  return RELEASE_COUPLED_RULES.has(metadata.ruleId || '') || metadata.reasons.some(reason => reason.startsWith(RELEASE_COUPLED_REASON))
}

// The stage comes from the server's rule and from where the producing request
// sits in the script, never from the variable's name.
function lifecycleStage(
  isSalesforce: boolean,
  ruleId: string | undefined,
  producerSection: 'Init' | 'Actions' | 'Teardown' | null,
  hasServerProducer: boolean,
  cookieSource = false,
): LifecycleStageId {
  if (isSalesforce) {
    if (ruleId && IDENTITY_RULES.has(ruleId)) return 'sf_identity'
    if (ruleId && BOOTSTRAP_RULES.has(ruleId)) return 'sf_bootstrap'
    if (producerSection === 'Init' || producerSection === 'Teardown') return 'sf_identity'
    return hasServerProducer ? 'sf_aura' : 'sf_client'
  }
  if (producerSection === 'Init' || producerSection === 'Teardown' || cookieSource) return 'generic_auth'
  return hasServerProducer ? 'generic_app' : 'generic_client'
}

const STAGE_TITLES: Record<LifecycleStageId, string> = {
  sf_identity: 'Lifecycle stage 1: Identity & Frontdoor SSO',
  sf_bootstrap: 'Lifecycle stage 2: Lightning bootstrap (/one/one.app)',
  sf_aura: 'Lifecycle stage 3: Aura & Apex actions (Controller$method)',
  sf_client: 'Lifecycle stage 4: Client counters & per-request IDs',
  generic_auth: 'Authentication & session',
  generic_app: 'Application endpoints',
  generic_client: 'Client-generated inputs',
}

const STAGE_ORDER: LifecycleStageId[] = [
  'sf_identity',
  'sf_bootstrap',
  'sf_aura',
  'sf_client',
  'generic_auth',
  'generic_app',
  'generic_client',
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
  recordingStudioHref,
}: HarCorrelationStudioProps) {
  const headingId = useId()
  const blockerIdPrefix = useId()
  const [filter, setFilter] = useState<StudioFilter>('all')
  const [expanded, setExpanded] = useState(false)

  const flattened = useMemo(() => flattenScriptActions(script), [script])
  const blockedActions = useMemo(() => findBlockedUnexportedBodyActions(script), [script])
  const isSalesforce = Boolean(salesforceReview?.detected)
  // The analyzer's rule and evidence for each correlation, by variable name.
  const correlationMetadata = useMemo(() => {
    const byName = new Map<string, AdmittedCorrelationMetadata>()
    for (const row of admitNativeCorrelationEvidence(salesforceReview?.native_preview).rows) {
      if (row.name && !byName.has(row.name)) byName.set(row.name, row)
    }
    return byName
  }, [salesforceReview?.native_preview])
  const uncapturedCookieTokens = useMemo(
    () => Array.from(correlationMetadata.values()).filter(row => row.ruleId === UNCAPTURED_COOKIE_RULE),
    [correlationMetadata],
  )

  const phaseCounts = useMemo(() => {
    let init = 0
    let actions = 0
    let teardown = 0
    let extractors = 0
    const transactions = new Set<string>()
    for (const item of flattened) {
      if (item.section === 'Init') init += 1
      else if (item.section === 'Teardown') teardown += 1
      else actions += 1
      if (Array.isArray(item.action.extractors)) extractors += item.action.extractors.filter(ext => typeof ext?.name === 'string' && ext.name.trim()).length
      const transaction = item.action.metadata?.source_transaction
      if (typeof transaction === 'string' && transaction.trim()) transactions.add(transaction.trim())
    }
    return { init, actions, teardown, total: init + actions + teardown, extractors, transactions: transactions.size }
  }, [flattened])

  // The server's total_requests counts every HAR entry it read, and its
  // filtered_assets already includes filtered_telemetry.
  const funnel = useMemo(() => {
    const count = (value: unknown) => typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : 0
    const filteredTelemetry = count(stats?.filtered_telemetry)
    const filteredTotal = Math.max(count(stats?.filtered_assets), filteredTelemetry)
    const filteredStatic = filteredTotal - filteredTelemetry
    const converted = count(stats?.converted_actions) || phaseCounts.total
    const totalEntries = count(stats?.total_requests) || converted + filteredTotal + count(stats?.skipped_requests)
    return { totalEntries, filteredTotal, filteredStatic, filteredTelemetry }
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
          stage: 'sf_identity',
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
        const metadata = correlationMetadata.get(varName)
        const isUncapturedAuraToken = metadata?.ruleId === UNCAPTURED_COOKIE_RULE
        const isAuraContextRelease = isReleaseCoupled(metadata)
        const stage = lifecycleStage(isSalesforce, metadata?.ruleId, entry.section, true, ext.source === 'cookie')
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
        if (metadata?.confidence !== undefined && !diff) guidance = `${guidance} Rule confidence ${Math.round(metadata.confidence * 100)}%.`

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
      const metadata = correlationMetadata.get(varName)
      const isAuraContext = isReleaseCoupled(metadata)
      const hasKnownProducer = item.producer_found === true || (typeof item.found_in_step === 'number' && item.found_in_step > 0)
      const stage = lifecycleStage(isSalesforce, metadata?.ruleId, null, hasKnownProducer)
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
    if (isSalesforce && phaseCounts.teardown > 0) {
      byName.set('lifecycle.teardown.logout', {
        key: 'lifecycle.teardown.logout',
        variableName: 'secur/logout.jsp',
        stage: 'sf_identity',
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
  }, [correlationMetadata, flattened, harDiff, isSalesforce, phaseCounts.teardown, script.data_sources])

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

  // Excluding a request removes its row and the focused button with it. Keep
  // keyboard users in the list and say what changed.
  const excludeRequestRef = useRef<{ index: number; before: number } | null>(null)
  const blockerButtonRefs = useRef(new Map<number, HTMLButtonElement>())
  const studioHeadingRef = useRef<HTMLHeadingElement>(null)
  const [exclusionStatus, setExclusionStatus] = useState('')
  useEffect(() => {
    const request = excludeRequestRef.current
    if (!request) return
    if (blockedActions.length < request.before) {
      excludeRequestRef.current = null
      const removed = request.before - blockedActions.length
      setExclusionStatus(`Excluded ${removed} blocked request${removed === 1 ? '' : 's'} from the draft. ${blockedActions.length} remain${blockedActions.length === 1 ? 's' : ''}.`)
      const next = blockerButtonRefs.current.get(Math.min(request.index, blockedActions.length - 1))
      if (next && !next.disabled) next.focus()
      else studioHeadingRef.current?.focus()
    } else if (!busy) {
      // The exclusion failed or was refused; the row and its focus remain.
      excludeRequestRef.current = null
    }
  }, [blockedActions, busy])
  const requestExclusion = (index: number, options: { actionUids?: string[]; excludeUnexportedBodies?: boolean }) => {
    excludeRequestRef.current = { index, before: blockedActions.length }
    setExclusionStatus('')
    onExcludeBlockedActions?.(options)
  }
  const recordingStudioLink = recordingStudioHref
    ? <a className="btn btn-secondary btn-sm" href={recordingStudioHref}>Record in Recording Studio</a>
    : null

  return (
    <section className="har-correlation-studio" aria-labelledby={headingId}>
      <h4 id={headingId} ref={studioHeadingRef} tabIndex={-1}>Correlation studio &amp; capture funnel</h4>
      <p className="sr-only" role="status">{exclusionStatus}</p>

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
          <span className="har-funnel-label">Generated actions</span>
          <span className="har-funnel-value">{formatInteger(phaseCounts.total)}</span>
          <span className="har-funnel-detail">
            Init {formatInteger(phaseCounts.init)} · Main {formatInteger(phaseCounts.actions)} · Teardown {formatInteger(phaseCounts.teardown)} · {formatInteger(phaseCounts.transactions)} transaction{phaseCounts.transactions === 1 ? '' : 's'}
          </span>
        </div>
        <div className="har-funnel-step">
          <span className="har-funnel-label">Wired extractors</span>
          <span className="har-funnel-value">{formatInteger(phaseCounts.extractors)}</span>
          <span className="har-funnel-detail">Values extracted at replay</span>
        </div>
        <div className="har-funnel-step">
          <span className="har-funnel-label">Engineer decisions</span>
          <span className="har-funnel-value">{formatInteger(filterCounts.review + blockedActions.length)}</span>
          <span className="har-funnel-detail">
            {formatInteger(filterCounts.review)} to review · {formatInteger(blockedActions.length)} blocked request{blockedActions.length === 1 ? '' : 's'}
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

      {uncapturedCookieTokens.length > 0 && (
        <div className="har-capture-callout" role="note" aria-label="Cookies missing from the HAR export">
          <strong>The browser removed session cookies from this HAR export.</strong>
          <p>
            Chrome and Edge (v130+) leave Set-Cookie headers out of exported HARs, so the __Host-ERIC_* cookie that issues aura.token is not in the capture. The draft extracts it at live replay instead. Record the flow in Recording Studio to capture cookies and full request bodies.
          </p>
          {recordingStudioLink}
        </div>
      )}

      {blockedActions.length > 0 && (
        <div className="har-blocker-banner" role="region" aria-label="Requests missing exported bodies">
          <div className="har-blocker-banner-header">
            <h5 className="har-blocker-banner-title">
              {blockedActions.length} request{blockedActions.length === 1 ? '' : 's'} omitted request body in HAR export (blocks replay)
            </h5>
            <div className="har-blocker-banner-actions">
              {onExcludeBlockedActions && (blockedActions.length > 1 || blockedActions.some(item => !item.uid)) && (
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  disabled={busy}
                  onClick={() => requestExclusion(0, { excludeUnexportedBodies: true })}
                >
                  Exclude all {blockedActions.length} blocked {blockedActions.length === 1 ? 'request' : 'requests'} from draft
                </button>
              )}
              {recordingStudioLink}
            </div>
          </div>
          <p className="har-blocker-guidance">
            Browser HAR exports often omit large POST bodies (or client telemetry payloads). Exclude non-essential requests below to unblock replay immediately, or re-export from Chrome DevTools using <strong>Export HAR (with sensitive data)</strong> / record with APEX Recording Studio if the request carries required business state.
          </p>
          <ul className="har-blocker-list">
            {blockedActions.map((item, index) => (
              <li key={item.uid || item.path} className="har-blocker-item">
                <div className="har-blocker-meta" id={`${blockerIdPrefix}-${index}`}>
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
                    ref={element => {
                      if (element) blockerButtonRefs.current.set(index, element)
                      else blockerButtonRefs.current.delete(index)
                    }}
                    aria-label={`Exclude action from draft: ${item.name}`}
                    aria-describedby={`${blockerIdPrefix}-${index}`}
                    disabled={busy || !item.uid}
                    title={!item.uid ? 'Action lacks an individual import UID; use bulk exclude above.' : undefined}
                    onClick={() => {
                      if (item.uid) requestExclusion(index, { actionUids: [item.uid] })
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
                    <tr className="har-studio-stage-row">
                      <th colSpan={6} scope="colgroup">{STAGE_TITLES[group.stage]}</th>
                    </tr>
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
