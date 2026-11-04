import { Fragment, useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react'
import type {
  AgentforceReview,
  AgentforceTransformChange,
  HARDiffClassification,
  HARDiffReport,
  HARDiffRequestInput,
  HARImportStats,
  HARImportSummary,
  ImportReviewSection,
  MulesoftReview,
  SalesforceDomainObservation,
  SalesforceReview,
} from '../types/scriptImports'
import type { ReplayPreflight, ScriptDefinition, SourceReview, SourceReviewMapping } from '../types/scripts'
import { countLabel, formatUpTo } from '../utils/numberFormat'
import AuraOutcomeReview from './AuraOutcomeReview'
import AuraParameterLocationDetails from './AuraParameterLocationDetails'
import { admitNativeCorrelationEvidence, type AdmittedCorrelationMetadata, type AdmittedNativeCorrelation } from './correlationEvidenceAdmission'
import {
  findBlockedUnexportedBodyActions,
  flattenScriptActions,
  recordingProfileDroppedRequests,
  requestFieldDraftStates,
  scriptTransactionNames,
  stepVariableReferences,
  type FlattenedActionEntry,
  type RequestFieldDraftState,
} from './harCorrelationStudioModel'
import { replayPreflightStatusLabel } from './replayPreflightUtils'
import SalesforceCorrelationReview from './SalesforceCorrelationReview'
import { exactSourceMapping, salesforceApiSurfaceBreakdown, salesforceEndpointTotal } from './salesforceReviewSummary'
import { handleTabKey } from './tabKeyboard'
import TabList from './ux/TabList'
import './HarCorrelationStudio.css'

const formatInteger = (value: number): string => formatUpTo(value, 0)

// A display cap that says what it hides: "Showing K of N" and "Show all N".
export function CappedFindingList<T>({ items, limit, noun, itemKey, renderItem, leading }: {
  items: readonly T[]
  limit: number
  noun: string
  itemKey: (item: T, index: number) => string
  renderItem: (item: T) => ReactNode
  /** Rows always listed ahead of the capped items (such as blockers). */
  leading?: ReactNode
}) {
  const [showAll, setShowAll] = useState(false)
  const visible = showAll ? items : items.slice(0, limit)
  return (
    <>
      <ul className="sample-import-findings">
        {leading}
        {visible.map((item, index) => <li key={itemKey(item, index)}>{renderItem(item)}</li>)}
      </ul>
      {items.length > limit && (
        <div className="sample-import-actions compact">
          <span className="sample-import-muted">Showing {visible.length} of {items.length} {noun}</span>
          <button type="button" className="btn btn-secondary btn-sm" aria-expanded={showAll} onClick={() => setShowAll(current => !current)}>
            {showAll ? `Show first ${limit} ${noun}` : `Show all ${items.length} ${noun}`}
          </button>
        </div>
      )}
    </>
  )
}

// A Salesforce host's imported requests, and every raw HAR entry on it when
// that differs (filtered static assets and telemetry count only there).
// After an exclusion the counts still describe the draft as it was converted.
function salesforceDomainCounts(domain: SalesforceDomainObservation, excludedSinceConversion = false): string {
  const requests = `${domain.count} request${domain.count === 1 ? '' : 's'} ${excludedSinceConversion ? 'at import' : 'in the draft'}`
  const harCount = Number.isSafeInteger(domain.har_count) && (domain.har_count || 0) > 0 ? domain.har_count! : undefined
  if (harCount === undefined || harCount === domain.count) return requests
  return `${requests} · ${harCount} HAR entr${harCount === 1 ? 'y' : 'ies'}`
}

export interface HarCorrelationStudioProps {
  script: ScriptDefinition
  stats?: Partial<HARImportStats> & Record<string, unknown>
  /** The conversion's warnings, which name the recording profile's drops. */
  warnings?: readonly string[] | null
  harDiff?: HARDiffReport
  salesforceReview?: SalesforceReview | null
  duplicateFileWarning?: string
  /** True while a conversion or an exclusion is in flight. */
  busy?: boolean
  /**
   * Requests were excluded since conversion: the pair diff's step numbers
   * name actions by their position at conversion, which no longer match.
   */
  stepPositionsStale?: boolean
  /** Returns false when the host refused the exclusion without starting it. */
  onExcludeBlockedActions?: (options: { actionUids?: string[]; excludeUnexportedBodies?: boolean }) => boolean | void
  /** Where Recording Studio opens, to re-record what the HAR export left out. */
  recordingStudioHref?: string
  /** Consolidated HAR review props so the Review tab has a single 3-zone layout with zero accordions. */
  preflight?: ReplayPreflight | null
  harSummary?: HARImportSummary | null
  sourceReview?: SourceReview | null
  excludedCount?: number
  findingListScope?: string
  mulesoftReview?: MulesoftReview | null
  agentforceReview?: AgentforceReview | null
  agentforceTransformChanges?: readonly AgentforceTransformChange[]
  // The import API serializes an empty list as null.
  diagnostics?: ReadonlyArray<{ code?: string; source_ref?: string; element: string; detail: string; severity: 'blocker' | 'warning' | 'info'; location?: string }> | null
  unsupportedElements?: readonly string[] | null
  reviewSections?: readonly ImportReviewSection[] | null
  onViewSource?: (mapping: SourceReviewMapping, title?: string, description?: string) => void
}

type StudioFilter = 'all' | 'dynamic' | 'wired' | 'review' | 'release_static'
type WorkbenchTabId = 'correlations' | 'aura' | 'architecture' | 'provenance' | 'log'

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
  producerMapping?: SourceReviewMapping
  consumerLabel: string
  extractorSummary: string
  guidance: string
}

interface ConversionLogGroup {
  key: string
  severity: 'blocker' | 'warning' | 'info'
  category: string
  detail: string
  count: number
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
// The analyzer adds this reason, and pins the extractor to the named cookie,
// only when the /one/one.app bootstrap HTML declares auraConfig.eikoocnekot
// (pkg/convert/salesforce/aura_token_cookie.go). Without it the cookie name is
// inferred and the extractor takes any (__Host-)ERIC_* cookie.
const EIKOOCNEKOT_CONFIRMED_REASON = /^confirmed auraConfig\.eikoocnekot \(([^)]*)\)/

function uncapturedCookieGuidance(metadata: AdmittedCorrelationMetadata | undefined): string {
  const confirmed = metadata?.reasons.map(reason => EIKOOCNEKOT_CONFIRMED_REASON.exec(reason)).find(Boolean)
  if (confirmed) {
    return `Confirmed via auraConfig.eikoocnekot${confirmed[1] ? ` (${confirmed[1]})` : ''} in /one/one.app HTML; browser sanitized Set-Cookie (__Host-ERIC_*) from HAR export.`
  }
  return 'Not confirmed: the capture has no /one/one.app HTML declaring auraConfig.eikoocnekot, so the cookie name is inferred and the extractor takes the first (__Host-)ERIC_* cookie. Check at live replay that it captures the Aura token.'
}

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

// What a request value that changed between the two recordings most likely
// is (pkg/convert/har DiffInput*), and how replay should obtain it.
const UNHANDLED_INPUT_KINDS: Record<string, { label: string; guidance: string; serverIssued?: boolean }> = {
  client_generated_id: { label: 'Client-generated ID', guidance: 'Use {{fn.uuid()}} when the server accepts any fresh ID, or parameterize it.' },
  client_timestamp: { label: 'Client timestamp', guidance: 'Parameterize it with a timestamp parameter so each request sends the current time.' },
  client_counter: { label: 'Client counter', guidance: 'Usually replays harmlessly; parameterize it as a counter if the server rejects repeated values.' },
  server_value: { label: 'Server value, not correlated', guidance: 'An earlier response issues this value; add a correlation so replay extracts it.', serverIssued: true },
  unknown_origin: { label: 'Unknown origin', guidance: 'Parameterize it, or correlate it if a response issues it.' },
  origin_not_checked: { label: 'Origin not checked', guidance: 'Too short or past the search budget to trace; check whether it must change per session.' },
}
const UNKNOWN_INPUT_KIND = UNHANDLED_INPUT_KINDS.unknown_origin

// The diff ran once, at import; the draft's current requests say whether a
// field it reported is still sent as recorded. A field no request sends any
// more counts as resolved only after requests were excluded: at import the
// server reports only fields the draft still sends, so an absence without an
// exclusion means this parser missed it, and the row stays under review.
function unhandledInputRow(input: HARDiffRequestInput, isSalesforce: boolean, draftState: RequestFieldDraftState | undefined, requestsExcluded: boolean): StudioRow {
  const kind = UNHANDLED_INPUT_KINDS[input.kind] || UNKNOWN_INPUT_KIND
  const requests = Number.isSafeInteger(input.requests) && input.requests > 0 ? input.requests : 0
  const distinct = Number.isSafeInteger(input.distinct_values) && input.distinct_values > 0 ? input.distinct_values : 0
  const entries = (input.baseline_entries || []).filter(entry => Number.isSafeInteger(entry) && entry > 0)
  const shownEntries = entries.slice(0, 3)
  const stage: LifecycleStageId = kind.serverIssued
    ? (isSalesforce ? 'sf_aura' : 'generic_app')
    : (isSalesforce ? 'sf_client' : 'generic_client')
  const consumerLabel = `${formatInteger(requests)} request${requests === 1 ? '' : 's'}${shownEntries.length
    ? ` (HAR entr${shownEntries.length === 1 ? 'y' : 'ies'} ${shownEntries.join(', ')}${entries.length > shownEntries.length ? ', …' : ''})`
    : ''}`
  const diffStatus = `Changed in ${formatInteger(requests)} request${requests === 1 ? '' : 's'} · ${formatInteger(distinct)} value${distinct === 1 ? '' : 's'}`
  if (draftState === 'templated') {
    return {
      key: `input:${input.field}`,
      variableName: input.field,
      stage,
      statusKind: 'wired',
      statusLabel: `Parameterized in the draft · ${kind.label}`,
      diffStatus,
      isDynamic: true,
      isWired: false,
      needsReview: false,
      isReleaseStatic: false,
      producerLabel: kind.serverIssued ? 'Earlier server response' : 'Client-side (not issued by a server response)',
      consumerLabel,
      extractorSummary: 'Sent from a template',
      guidance: 'Changed between the recordings; every request in the draft now sends a template here instead of the recorded value.',
    }
  }
  if (draftState === 'not_found' && requestsExcluded) {
    return {
      key: `input:${input.field}`,
      variableName: input.field,
      stage,
      statusKind: 'static',
      statusLabel: `No longer sent · ${kind.label}`,
      diffStatus,
      isDynamic: true,
      isWired: false,
      needsReview: false,
      isReleaseStatic: false,
      producerLabel: kind.serverIssued ? 'Earlier server response' : 'Client-side (not issued by a server response)',
      consumerLabel: `${consumerLabel} at import`,
      extractorSummary: 'Not sent by the draft',
      guidance: 'Changed between the recordings, but every request that sent it was excluded from the draft; nothing is left to parameterize.',
    }
  }
  return {
    key: `input:${input.field}`,
    variableName: input.field,
    stage,
    statusKind: 'review',
    statusLabel: `Sent as recorded · ${kind.label}`,
    diffStatus,
    isDynamic: true,
    isWired: false,
    needsReview: true,
    isReleaseStatic: false,
    producerLabel: kind.serverIssued ? 'Earlier server response (no extractor)' : 'Client-side (not issued by a server response)',
    consumerLabel,
    extractorSummary: 'Not parameterized',
    guidance: input.reason ? `${input.reason} ${kind.guidance}` : kind.guidance,
  }
}

const EMPTY_SCRIPT_CONFIG: ScriptDefinition['config'] = { base_url: '' }

export default function HarCorrelationStudio({
  script,
  stats,
  warnings,
  harDiff,
  salesforceReview,
  duplicateFileWarning,
  busy = false,
  stepPositionsStale = false,
  onExcludeBlockedActions,
  recordingStudioHref,
  preflight,
  harSummary,
  sourceReview,
  excludedCount = 0,
  findingListScope = 'har',
  mulesoftReview,
  agentforceReview,
  agentforceTransformChanges = [],
  diagnostics = [],
  unsupportedElements = [],
  reviewSections = [],
  onViewSource,
}: HarCorrelationStudioProps) {
  const headingId = useId()
  const blockerIdPrefix = useId()
  const workbenchIdPrefix = useId()
  const [filter, setFilter] = useState<StudioFilter>('all')
  const [expanded, setExpanded] = useState(false)
  const [selectedWorkbenchTab, setSelectedWorkbenchTab] = useState<WorkbenchTabId | null>(null)

  // Keyed on the request lists, not the script object: a rename commits a new
  // script object with the same steps and must not re-walk them.
  const { init: scriptInit, actions: scriptActions, teardown: scriptTeardown, data_sources: dataSources } = script
  const steps = useMemo<ScriptDefinition>(() => ({
    name: '',
    version: '',
    protocol: 'http',
    config: EMPTY_SCRIPT_CONFIG,
    init: scriptInit,
    actions: scriptActions,
    teardown: scriptTeardown,
  }), [scriptActions, scriptInit, scriptTeardown])
  const flattened = useMemo(() => flattenScriptActions(steps), [steps])
  const blockedActions = useMemo(() => findBlockedUnexportedBodyActions(steps, flattened), [flattened, steps])
  const transactionCount = useMemo(() => scriptTransactionNames(steps).size, [steps])
  // Every request that sends each {{var.*}}, in replay order.
  const consumersByVariable = useMemo(() => {
    const byName = new Map<string, FlattenedActionEntry[]>()
    for (const entry of flattened) {
      for (const name of stepVariableReferences(entry.action)) {
        const consumers = byName.get(name)
        if (consumers) consumers.push(entry)
        else byName.set(name, [entry])
      }
    }
    return byName
  }, [flattened])
  const isSalesforce = Boolean(salesforceReview?.detected)
  const admittedCorrelationEvidence = useMemo(
    () => admitNativeCorrelationEvidence(salesforceReview?.native_preview),
    [salesforceReview?.native_preview],
  )
  // The analyzer's rule and evidence for each correlation, by variable name.
  const correlationMetadata = useMemo(() => {
    const byName = new Map<string, AdmittedNativeCorrelation>()
    for (const row of admittedCorrelationEvidence.rows) {
      if (row.name && !byName.has(row.name)) byName.set(row.name, row)
    }
    return byName
  }, [admittedCorrelationEvidence.rows])
  const uncapturedCookieTokens = useMemo(
    () => Array.from(correlationMetadata.values()).filter(row => row.ruleId === UNCAPTURED_COOKIE_RULE),
    [correlationMetadata],
  )

  const phaseCounts = useMemo(() => {
    let init = 0
    let actions = 0
    let teardown = 0
    let extractors = 0
    for (const item of flattened) {
      if (item.section === 'Init') init += 1
      else if (item.section === 'Teardown') teardown += 1
      else actions += 1
      if (Array.isArray(item.action.extractors)) extractors += item.action.extractors.filter(ext => typeof ext?.name === 'string' && ext.name.trim()).length
    }
    return { init, actions, teardown, total: init + actions + teardown, extractors, transactions: transactionCount }
  }, [flattened, transactionCount])

  // The server's total_requests counts every HAR entry it read, and its
  // filtered_assets already includes filtered_telemetry and the recording
  // profile's drops.
  const funnel = useMemo(() => {
    const count = (value: unknown) => typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : 0
    const filteredTelemetry = count(stats?.filtered_telemetry)
    const filteredTotal = Math.max(count(stats?.filtered_assets), filteredTelemetry)
    const filteredByProfile = Math.min(recordingProfileDroppedRequests(warnings), filteredTotal - filteredTelemetry)
    const filteredStatic = filteredTotal - filteredTelemetry - filteredByProfile
    const converted = count(stats?.converted_actions) || phaseCounts.total
    const totalEntries = count(stats?.total_requests) || converted + filteredTotal + count(stats?.skipped_requests)
    return { totalEntries, filteredTotal, filteredStatic, filteredByProfile, filteredTelemetry }
  }, [phaseCounts.total, stats, warnings])

  const unhandledInputStates = useMemo(() => requestFieldDraftStates(steps, (harDiff?.unhandled_inputs || [])
    .map(input => typeof input?.field === 'string' ? input.field.trim() : '')
    .filter(Boolean)), [harDiff?.unhandled_inputs, steps])

  const rows = useMemo<StudioRow[]>(() => {
    const byName = new Map<string, StudioRow>()
    const diffByName = new Map<string, HARDiffClassification>()
    for (const item of harDiff?.classifications || []) {
      if (item?.name) diffByName.set(item.name, item)
    }

    // 1. Data pool credentials hoisted for Salesforce desktop sign-in
    for (const ds of dataSources || []) {
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
        const consumers = (consumersByVariable.get(varName) || []).filter(candidate => candidate.seq > entry.seq)
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
          guidance = uncapturedCookieGuidance(metadata)
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
        const producerMapping = exactSourceMapping(metadata?.producer, sourceReview || undefined, stepPositionsStale)

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
          producerMapping,
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
      const producerMapping = exactSourceMapping(metadata?.producer, sourceReview || undefined, stepPositionsStale)

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
          ? stepPositionsStale
            ? `A recorded server response${item.matched_descriptor ? ` (${item.matched_descriptor})` : ''}; its step number predates the exclusion`
            : `Step #${item.found_in_step}${item.matched_descriptor ? ` (${item.matched_descriptor})` : ''}`
          : 'Not observed in server response (client nonce/counter or input)',
        producerMapping,
        consumerLabel: Array.isArray(item.used_in_steps) && item.used_in_steps.length > 0
          ? stepPositionsStale
            ? `${item.used_in_steps.length} recorded step${item.used_in_steps.length === 1 ? '' : 's'} at import`
            : `${item.used_in_steps.length} step${item.used_in_steps.length === 1 ? '' : 's'}`
          : 'Observed in compared request',
        extractorSummary: item.extract_type ? `${item.extract_type} (${item.source || 'response'})` : 'Diff observation only (not auto-wired)',
        guidance: item.reason || (hasKnownProducer
          ? 'Differed across the two HARs with a candidate server producer; review before wiring.'
          : 'Differed across the two HARs without a server producer; keep literal or bind to {{fn.*}} / parameter if required.'),
      })
    }

    // 3b. Request fields that changed between the recordings and that the
    // script still sends as recorded (client counters, nonces, per-request IDs).
    for (const input of harDiff?.unhandled_inputs || []) {
      const field = typeof input?.field === 'string' ? input.field.trim() : ''
      if (!field || byName.has(`input:${field}`)) continue
      byName.set(`input:${field}`, unhandledInputRow({ ...input, field }, isSalesforce, unhandledInputStates.get(field), stepPositionsStale))
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
  }, [consumersByVariable, correlationMetadata, dataSources, flattened, harDiff, isSalesforce, phaseCounts.teardown, sourceReview, stepPositionsStale, unhandledInputStates])

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

  // Compact Zone 1 governance & match metadata strip (replaces standalone
  // ReplayPreflightCard, HAR Artifact Review card, and Salesforce fact grid).
  const metaFacts = useMemo(() => {
    const items: Array<{ label: string; value: string | number }> = []
    if (preflight) {
      const checkLabels = preflight.checks?.map(c => c.label).filter(Boolean).join(', ')
      items.push({
        label: 'Preflight',
        value: `${replayPreflightStatusLabel(preflight)}${checkLabels ? ` (${checkLabels})` : ''}`,
      })
    }
    if (harSummary) {
      items.push({ label: 'HAR Artifact Review', value: harSummary.artifact_mode })
      items.push({ label: 'sensitivity', value: harSummary.artifact_sensitivity || harSummary.governance?.sensitivity || 'unknown' })
      items.push({ label: 'retention days', value: harSummary.retention_days ?? harSummary.governance?.retention_days ?? 0 })
      if (harDiff) {
        items.push({ label: 'dynamic candidates', value: harDiff.dynamic_candidates })
        items.push({ label: 'static candidates', value: harDiff.static_candidates })
      }
      if (stats?.filtered_assets !== undefined) {
        items.push({ label: 'filtered assets', value: funnel.filteredTotal })
      }
    }
    if (salesforceReview) {
      items.push({ label: 'Salesforce Native Review', value: salesforceReview.detected ? 'Detected' : 'Needs review' })
      items.push({ label: 'HAR coverage', value: `${Math.round(salesforceReview.har_match_coverage?.coverage_pct || 0)}%` })
      items.push({ label: 'matched requests', value: `${salesforceReview.har_match_coverage?.matched_requests || 0} / ${salesforceReview.har_match_coverage?.imported_requests || 0}` })
      items.push({ label: 'domains', value: salesforceReview.domains?.length || 0 })
      items.push({ label: 'Aura calls', value: salesforceEndpointTotal(salesforceReview, 'aura_endpoints') })
      items.push({ label: 'Lightning calls', value: salesforceEndpointTotal(salesforceReview, 'lightning_endpoints') })
    }
    return items
  }, [funnel.filteredTotal, harDiff, harSummary, preflight, salesforceReview, stats?.filtered_assets])

  const apiSurface = useMemo(
    () => (salesforceReview ? salesforceApiSurfaceBreakdown(salesforceReview) : []),
    [salesforceReview],
  )

  const groupedConversionLog = useMemo<ConversionLogGroup[]>(() => {
    const byKey = new Map<string, ConversionLogGroup>()
    const add = (severity: 'blocker' | 'warning' | 'info', category: string, detail: string) => {
      const normalizedDetail = detail.trim()
      if (!normalizedDetail) return
      const key = `${severity}|${category}|${normalizedDetail.replace(/\b(step|request|entry)\s+#?\d+/gi, '$1 #N')}`
      const existing = byKey.get(key)
      if (existing) {
        existing.count += 1
      } else {
        byKey.set(key, { key, severity, category, detail: normalizedDetail, count: 1 })
      }
    }
    for (const diag of diagnostics ?? []) {
      add(diag.severity || 'info', diag.code || diag.element || 'converter', `${diag.detail}${diag.location ? ` (${diag.location})` : ''}`)
    }
    for (const warning of warnings || []) {
      add('warning', 'har_warning', warning)
    }
    for (const unsupported of unsupportedElements ?? []) {
      add('warning', 'unsupported_element', unsupported)
    }
    for (const section of reviewSections ?? []) {
      const sev: 'blocker' | 'warning' | 'info' = section.severity === 'blocker' ? 'blocker' : section.severity === 'warning' ? 'warning' : 'info'
      for (const item of section.items || []) {
        add(sev, section.title || section.key, item)
      }
    }
    return Array.from(byKey.values())
  }, [diagnostics, reviewSections, unsupportedElements, warnings])

  const auraOutcomes = salesforceReview?.aura_outcomes
  const hasAuraTab = Boolean(
    auraOutcomes && (
      (auraOutcomes.total_batches || 0) > 0
      || (auraOutcomes.batches?.length || 0) > 0
      || auraOutcomes.complete === false
    ),
  )
  const auraFailedCount = useMemo(() => {
    if (!auraOutcomes?.state_counts) return 0
    return Object.entries(auraOutcomes.state_counts)
      .reduce((sum, [state, count]) => (state === 'SUCCESS' ? sum : sum + (Number.isSafeInteger(count) && count > 0 ? count : 0)), 0)
  }, [auraOutcomes?.state_counts])

  const hasProvenanceTab = Boolean(
    admittedCorrelationEvidence.present && (
      admittedCorrelationEvidence.coverage
      || admittedCorrelationEvidence.rows.length > 0
      || admittedCorrelationEvidence.malformed
    ),
  )

  const architectureItemCount = (salesforceReview?.domains?.length || 0)
    + (salesforceReview?.transactions?.length || 0)
    + apiSurface.length
    + (salesforceReview?.parameterizations?.length || 0)
    + (salesforceReview?.blockers?.length || 0)
    + (salesforceReview?.next_actions?.length || 0)
    + (mulesoftReview ? 1 : 0)
    + (agentforceReview ? 1 : 0)
  const hasArchitectureTab = architectureItemCount > 0

  const workbenchTabs = useMemo(() => {
    const tabs: Array<{ id: WorkbenchTabId; label: string; badge: number }> = []
    if (rows.length > 0 || (!hasAuraTab && !hasProvenanceTab && !hasArchitectureTab && groupedConversionLog.length === 0)) {
      tabs.push({ id: 'correlations', label: 'Correlations', badge: rows.length })
    }
    if (hasAuraTab) {
      const auraBadge = auraOutcomes?.total_batches || auraOutcomes?.batches?.length || 0
      tabs.push({ id: 'aura', label: 'Aura outcomes', badge: auraBadge })
    }
    if (hasProvenanceTab) {
      tabs.push({ id: 'provenance', label: 'Captured provenance', badge: admittedCorrelationEvidence.rows.length })
    }
    if (hasArchitectureTab) {
      tabs.push({ id: 'architecture', label: 'Domains & parameters', badge: architectureItemCount })
    }
    if (groupedConversionLog.length > 0) {
      tabs.push({ id: 'log', label: 'Conversion log', badge: groupedConversionLog.length })
    }
    return tabs
  }, [admittedCorrelationEvidence.rows.length, architectureItemCount, auraOutcomes?.batches?.length, auraOutcomes?.total_batches, groupedConversionLog.length, hasArchitectureTab, hasAuraTab, hasProvenanceTab, rows.length])

  const defaultWorkbenchTab = useMemo<WorkbenchTabId>(() => {
    if ((admittedCorrelationEvidence.coverage?.native_unmapped_consumer_references || 0) > 0 && hasProvenanceTab) {
      return 'provenance'
    }
    if (rows.length > 0) return 'correlations'
    if (hasAuraTab) return 'aura'
    if (hasProvenanceTab) return 'provenance'
    if (hasArchitectureTab) return 'architecture'
    if (groupedConversionLog.length > 0) return 'log'
    return 'correlations'
  }, [admittedCorrelationEvidence.coverage?.native_unmapped_consumer_references, groupedConversionLog.length, hasArchitectureTab, hasAuraTab, hasProvenanceTab, rows.length])

  const activeWorkbenchTab: WorkbenchTabId = selectedWorkbenchTab && workbenchTabs.some(tab => tab.id === selectedWorkbenchTab)
    ? selectedWorkbenchTab
    : defaultWorkbenchTab

  const showWorkbenchTabs = workbenchTabs.length > 1

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
      // The exclusion failed; the row remains, and so does focus on it (its
      // button was disabled while the exclusion ran).
      excludeRequestRef.current = null
      const active = document.activeElement
      if (!active || active === document.body) blockerButtonRefs.current.get(request.index)?.focus()
    }
  }, [blockedActions, busy])
  const requestExclusion = (index: number, options: { actionUids?: string[]; excludeUnexportedBodies?: boolean }) => {
    const request = { index, before: blockedActions.length }
    excludeRequestRef.current = request
    setExclusionStatus('')
    // A refused request changes neither the blocked list nor busy, so nothing
    // would ever settle it: drop it now, or a later unrelated drop in blocked
    // requests (a re-inspection) would be announced as this exclusion.
    if (onExcludeBlockedActions?.(options) === false && excludeRequestRef.current === request) excludeRequestRef.current = null
  }
  const recordingStudioLink = recordingStudioHref
    ? <a className="btn btn-secondary btn-sm" href={recordingStudioHref}>Record in Recording Studio</a>
    : null

  const correlationsContent = rows.length > 0 ? (
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
                    <td>
                      <div>{row.producerLabel}</div>
                      {row.producerMapping && onViewSource && (
                        <button
                          type="button"
                          className="btn btn-secondary btn-sm har-source-inline-btn"
                          onClick={() => onViewSource(row.producerMapping!, 'Captured correlation request', 'Inspect the exact original source mapping for this captured request.')}
                        >
                          View producer source
                        </button>
                      )}
                    </td>
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

      {harDiff?.unhandled_inputs_truncated && (
        <p className="har-funnel-detail" role="note">
          Only the first {formatInteger(harDiff.unhandled_inputs?.length || 0)} changed request fields are listed; more changed between the recordings.
        </p>
      )}

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
  ) : null

  return (
    <section className="har-correlation-studio" aria-labelledby={headingId}>
      <h4 id={headingId} ref={studioHeadingRef} tabIndex={-1}>Correlation studio &amp; capture funnel</h4>
      <p className="sr-only" role="status">{exclusionStatus}</p>

      {/* Zone 1: Unified Capture & Readiness Header */}
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
            {formatInteger(funnel.filteredStatic)} static · {funnel.filteredByProfile > 0 ? `${formatInteger(funnel.filteredByProfile)} by recording profile · ` : ''}{formatInteger(funnel.filteredTelemetry)} telemetry
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

      {metaFacts.length > 0 && (
        <dl className="import-evidence-facts har-capture-meta-strip" aria-label="HAR governance and match summary">
          {metaFacts.map(fact => (
            <div key={fact.label}>
              <dt>{fact.label}</dt>
              <dd>{fact.value}</dd>
            </div>
          ))}
        </dl>
      )}

      {excludedCount > 0 && salesforceReview && (
        <p className="sample-import-muted">
          The request counts below describe the draft as converted; {countLabel(excludedCount, 'request')} {excludedCount === 1 ? 'was' : 'were'} excluded from it since.
        </p>
      )}

      {/* Zone 2: Consolidated Actionable Readiness Queue */}
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

      {auraFailedCount > 0 && hasAuraTab && activeWorkbenchTab !== 'aura' && (
        <div className="har-capture-callout" role="note" aria-label="Captured Aura action failures">
          <strong>{formatInteger(auraFailedCount)} captured Aura action{auraFailedCount === 1 ? '' : 's'} did not succeed inside an HTTP 200 batch.</strong>
          <p>
            Salesforce Aura batches return HTTP 200 even when individual sub-actions fail. Inspect the Aura outcomes tab to verify whether the recording captured a clean baseline flow.
          </p>
          <button
            type="button"
            className="btn btn-secondary btn-sm"
            onClick={() => setSelectedWorkbenchTab('aura')}
          >
            Inspect Aura outcomes ({formatInteger(auraFailedCount)})
          </button>
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

      {/* Zone 3: Segmented Evidence Workbench (Zero Accordions) */}
      {showWorkbenchTabs && (
        <TabList className="har-workbench-tabs" role="tablist" aria-label="HAR review evidence views" onKeyDown={handleTabKey}>
          {workbenchTabs.map(tab => {
            const selected = activeWorkbenchTab === tab.id
            return (
              <button
                key={tab.id}
                id={`${workbenchIdPrefix}-tab-${tab.id}`}
                type="button"
                role="tab"
                aria-selected={selected}
                aria-controls={`${workbenchIdPrefix}-panel-${tab.id}`}
                tabIndex={selected ? 0 : -1}
                className="har-workbench-tab"
                onClick={() => setSelectedWorkbenchTab(tab.id)}
              >
                {tab.label} ({formatInteger(tab.badge)})
              </button>
            )
          })}
        </TabList>
      )}

      {workbenchTabs.some(tab => tab.id === 'correlations') && (
        <div
          id={showWorkbenchTabs ? `${workbenchIdPrefix}-panel-correlations` : undefined}
          role={showWorkbenchTabs ? 'tabpanel' : undefined}
          aria-labelledby={showWorkbenchTabs ? `${workbenchIdPrefix}-tab-correlations` : undefined}
          hidden={showWorkbenchTabs && activeWorkbenchTab !== 'correlations'}
          className="har-workbench-panel"
        >
          {correlationsContent}
        </div>
      )}

      {hasAuraTab && (
        <div
          id={showWorkbenchTabs ? `${workbenchIdPrefix}-panel-aura` : undefined}
          role={showWorkbenchTabs ? 'tabpanel' : undefined}
          aria-labelledby={showWorkbenchTabs ? `${workbenchIdPrefix}-tab-aura` : undefined}
          hidden={showWorkbenchTabs && activeWorkbenchTab !== 'aura'}
          className="har-workbench-panel"
        >
          <AuraOutcomeReview
            collapsible={false}
            report={salesforceReview?.aura_outcomes}
            script={script}
            sourceReview={sourceReview || undefined}
            positionsShifted={stepPositionsStale}
            onViewSource={mapping => onViewSource?.(mapping, 'Captured Aura request', 'Inspect the existing source mapping for this captured request.')}
          />
        </div>
      )}

      {hasProvenanceTab && (
        <div
          id={showWorkbenchTabs ? `${workbenchIdPrefix}-panel-provenance` : undefined}
          role={showWorkbenchTabs ? 'tabpanel' : undefined}
          aria-labelledby={showWorkbenchTabs ? `${workbenchIdPrefix}-tab-provenance` : undefined}
          hidden={showWorkbenchTabs && activeWorkbenchTab !== 'provenance'}
          className="har-workbench-panel"
        >
          <SalesforceCorrelationReview
            collapsible={false}
            preview={salesforceReview?.native_preview}
            sourceReview={sourceReview || undefined}
            positionsShifted={stepPositionsStale}
            onViewSource={mapping => onViewSource?.(mapping, 'Captured correlation request', 'Inspect the exact original source mapping for this captured request.')}
          />
        </div>
      )}

      {hasArchitectureTab && (
        <div
          id={showWorkbenchTabs ? `${workbenchIdPrefix}-panel-architecture` : undefined}
          role={showWorkbenchTabs ? 'tabpanel' : undefined}
          aria-labelledby={showWorkbenchTabs ? `${workbenchIdPrefix}-tab-architecture` : undefined}
          hidden={showWorkbenchTabs && activeWorkbenchTab !== 'architecture'}
          className="har-workbench-panel"
        >
          <div className="har-arch-grid">
            {(salesforceReview?.domains?.length || 0) > 0 && (
              <div className="har-arch-card">
                <h5>Salesforce domains ({formatInteger(salesforceReview!.domains!.length)})</h5>
                <CappedFindingList
                  key={`${findingListScope}-sf-domains`}
                  items={salesforceReview!.domains!}
                  limit={5}
                  noun="domains"
                  itemKey={domain => `${domain.host}-${domain.kind}`}
                  renderItem={domain => <><strong>{domain.host}</strong> {domain.kind} ({salesforceDomainCounts(domain, excludedCount > 0)})</>}
                />
              </div>
            )}

            {(salesforceReview?.transactions?.length || 0) > 0 && (
              <div className="har-arch-card">
                <h5>Synthesized transactions ({formatInteger(salesforceReview!.transactions!.length)})</h5>
                <CappedFindingList
                  key={`${findingListScope}-sf-transactions`}
                  items={salesforceReview!.transactions!}
                  limit={5}
                  noun="transactions"
                  itemKey={tx => `${tx.name}-${tx.source}`}
                  renderItem={tx => <><strong>{tx.name}</strong> {tx.request_count} request{tx.request_count === 1 ? '' : 's'}{tx.aura_actions ? ` / ${tx.aura_actions} Aura action${tx.aura_actions === 1 ? '' : 's'}` : ''}</>}
                />
              </div>
            )}

            {apiSurface.length > 0 && (
              <div className="har-arch-card">
                <h5>API surface</h5>
                <ul className="sample-import-findings">
                  {apiSurface.map(surface => (
                    <li key={surface.key}>
                      <strong>{surface.label}</strong> {surface.calls} call{surface.calls === 1 ? '' : 's'} across {surface.endpoints} endpoint{surface.endpoints === 1 ? '' : 's'}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {(salesforceReview?.parameterizations?.length || 0) > 0 && (
              <div className="har-arch-card">
                <h5>Aura parameterization candidates</h5>
                <CappedFindingList
                  key={`${findingListScope}-sf-parameterizations`}
                  items={salesforceReview!.parameterizations!}
                  limit={6}
                  noun="parameterization candidates"
                  itemKey={param => param.name}
                  renderItem={param => <>
                    <strong>{param.param_key}</strong> ({param.kind}) → <code>{param.template}</code>
                    {param.decoded_field_path && <AuraParameterLocationDetails location={param} />}
                  </>}
                />
              </div>
            )}

            {(salesforceReview?.blockers?.length || salesforceReview?.next_actions?.length) ? (
              <div className="har-arch-card">
                <h5>Readiness &amp; next actions</h5>
                <CappedFindingList
                  key={`${findingListScope}-sf-next-actions`}
                  items={salesforceReview?.next_actions || []}
                  limit={4}
                  noun="next actions"
                  leading={salesforceReview?.blockers?.map(item => <li key={`blocker-${item}`}><strong>Blocker</strong> {item}</li>)}
                  itemKey={item => `next-${item}`}
                  renderItem={item => <><strong>Next action</strong> {item}</>}
                />
              </div>
            ) : null}

            {mulesoftReview && (
              <div className="har-arch-card">
                <h5>MuleSoft / Anypoint Review</h5>
                {(mulesoftReview.domains?.length || 0) > 0 && (
                  <CappedFindingList
                    key={`${findingListScope}-mulesoft-domains`}
                    items={mulesoftReview.domains!}
                    limit={5}
                    noun="domains"
                    itemKey={domain => `${domain.host}-${domain.kind}`}
                    renderItem={domain => <><strong>{domain.host}</strong> {domain.kind} ({domain.count} request{domain.count === 1 ? '' : 's'})</>}
                  />
                )}
                {(mulesoftReview.policy_findings?.length || 0) > 0 && (
                  <>
                    <p className="sample-import-subtitle">Policies</p>
                    <ul className="sample-import-findings">
                      {mulesoftReview.policy_findings!.map((policy, index) => <li key={`policy-${index}`}><strong>{policy.policy}</strong> {policy.detail}</li>)}
                    </ul>
                  </>
                )}
                {(mulesoftReview.auth_findings?.length || 0) > 0 && (
                  <>
                    <p className="sample-import-subtitle">Auth</p>
                    <ul className="sample-import-findings">
                      {mulesoftReview.auth_findings!.map((auth, index) => <li key={`auth-${index}`}><strong>{auth.type}</strong> {auth.detail}</li>)}
                    </ul>
                  </>
                )}
                {(mulesoftReview.blockers?.length || mulesoftReview.next_actions?.length) ? (
                  <CappedFindingList
                    key={`${findingListScope}-mulesoft-next-actions`}
                    items={mulesoftReview.next_actions || []}
                    limit={4}
                    noun="next actions"
                    leading={mulesoftReview.blockers?.map(item => <li key={`blocker-${item}`}><strong>Blocker</strong> {item}</li>)}
                    itemKey={item => `next-${item}`}
                    renderItem={item => <><strong>Next action</strong> {item}</>}
                  />
                ) : null}
              </div>
            )}

            {agentforceReview && (
              <div className="har-arch-card">
                <h5>Agentforce Agent API Review {agentforceReview.streaming ? <span className="tag-chip">streaming (SSE)</span> : null}</h5>
                {agentforceTransformChanges.length > 0 && (
                  <>
                    <p className="sample-import-subtitle">Applied replay-readiness changes ({agentforceTransformChanges.length})</p>
                    <ul className="sample-import-findings">
                      {agentforceTransformChanges.map((change, index) => (
                        <li key={`af-change-${index}`}><strong>{change.step}</strong>{change.action_name ? ` — ${change.action_name}` : ''}: {change.detail}</li>
                      ))}
                    </ul>
                  </>
                )}
                {(agentforceReview.lifecycle?.length || 0) > 0 && (
                  <>
                    <p className="sample-import-subtitle">Conversation lifecycle</p>
                    <ul className="sample-import-findings">
                      {agentforceReview.lifecycle!.map((step, index) => (
                        <li key={`step-${index}`}><strong>{step.step}</strong> {step.method} {step.path}</li>
                      ))}
                    </ul>
                  </>
                )}
                {(agentforceReview.session_vars?.length || 0) > 0 && (
                  <>
                    <p className="sample-import-subtitle">Correlation</p>
                    <ul className="sample-import-findings">
                      {agentforceReview.session_vars!.map((sessionVar, index) => (
                        <li key={`var-${index}`}><strong>{sessionVar.name}</strong> {sessionVar.type} <code>{sessionVar.expression}</code></li>
                      ))}
                    </ul>
                  </>
                )}
                {(agentforceReview.assertion_hints?.length || 0) > 0 && (
                  <ul className="sample-import-findings">
                    {agentforceReview.assertion_hints!.map((hint, index) => <li key={`hint-${index}`}><strong>Assertion</strong> {hint.detail}</li>)}
                  </ul>
                )}
                {(agentforceReview.blockers?.length || agentforceReview.next_actions?.length) ? (
                  <CappedFindingList
                    key={`${findingListScope}-agentforce-next-actions`}
                    items={agentforceReview.next_actions || []}
                    limit={4}
                    noun="next actions"
                    leading={agentforceReview.blockers?.map(item => <li key={`blocker-${item}`}><strong>Blocker</strong> {item}</li>)}
                    itemKey={item => `next-${item}`}
                    renderItem={item => <><strong>Next action</strong> {item}</>}
                  />
                ) : null}
              </div>
            )}
          </div>
        </div>
      )}

      {groupedConversionLog.length > 0 && (
        <div
          id={showWorkbenchTabs ? `${workbenchIdPrefix}-panel-log` : undefined}
          role={showWorkbenchTabs ? 'tabpanel' : undefined}
          aria-labelledby={showWorkbenchTabs ? `${workbenchIdPrefix}-tab-log` : undefined}
          hidden={showWorkbenchTabs && activeWorkbenchTab !== 'log'}
          className="har-workbench-panel"
        >
          <div className="har-studio-table-region table-responsive" tabIndex={0} role="region" aria-label="Deduplicated conversion log">
            <table className="har-studio-table">
              <thead>
                <tr>
                  <th scope="col">Severity</th>
                  <th scope="col">Category / rule</th>
                  <th scope="col">Occurrences</th>
                  <th scope="col">Detail</th>
                </tr>
              </thead>
              <tbody>
                {groupedConversionLog.map(entry => (
                  <tr key={entry.key}>
                    <td>
                      <span className={`har-status-pill har-status-pill--${entry.severity === 'blocker' ? 'review' : entry.severity === 'warning' ? 'warn' : 'static'}`}>
                        {entry.severity}
                      </span>
                    </td>
                    <td><code>{entry.category}</code></td>
                    <td>{formatInteger(entry.count)}</td>
                    <td>{entry.detail}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </section>
  )
}
