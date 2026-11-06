import type { ImportRepairSuggestion, JMeterImportResult } from '../types/scriptImports'
import { importReviewResolution, type ImportIssueAction, type ImportWorkspaceIssue } from './importIssueWorkspaceModel'
import { actionAwaitsRuntimeSupport, importActionEntries } from './importReviewIssues'

// The repairs the batch repair endpoint accepts, as the form offers them.
export const JMETER_BATCH_REPAIR_CHOICES = [
  ['confirm_reference_context', 'Confirm the reference timezone and no property overrides'],
  ['normalize_oauth_options', 'Normalize Kafka OAuth option names'],
  ['bind_kafka_credentials', 'Bind Kafka credential references'],
  ['bind_eventhubs_credentials', 'Bind Event Hubs credential reference'],
  ['utc_timestamp', 'Replace a payload timestamp with UTC'],
  ['eventhubs_property', 'Add an Event Hubs application property'],
  ['parameterize_payload', 'Parameterize a payload field'],
] as const

const BATCH_REPAIR_TYPES = new Set<string>(JMETER_BATCH_REPAIR_CHOICES.map(([id]) => id))

// Button labels for the batch repairs a review gate names.
const BATCH_REPAIR_LABELS: Record<string, string> = {
  bind_kafka_credentials: 'Bind Kafka credentials',
  bind_eventhubs_credentials: 'Bind Event Hubs connection string',
  normalize_oauth_options: 'Normalize Kafka OAuth options',
  confirm_reference_context: 'Confirm timezone and overrides',
}

// The batch repair that clears each repairable JMeter gate, for a response
// that carries no repair_suggestions (an older server). The suggestions'
// batch_operation is authoritative when present.
const BATCH_REPAIR_BY_CODE: Record<string, string> = {
  jmeter_kafka_oauth_credentials_required: 'bind_kafka_credentials',
  jmeter_eventhubs_credentials_required: 'bind_eventhubs_credentials',
  jmeter_kafka_nonstandard_oauth_options: 'normalize_oauth_options',
  jmeter_reference_context_confirmation_required: 'confirm_reference_context',
}

export const JMETER_CREDENTIAL_GATE_CODES = ['jmeter_kafka_oauth_credentials_required', 'jmeter_eventhubs_credentials_required'] as const

export const JMETER_NOT_CLEARABLE_LABEL = 'Cannot be cleared yet'

type JMeterScripts = JMeterImportResult['scripts']

export interface JMeterIssueActionContext {
  /** The review is a revisable batch, so the batch repairs are available. */
  batch: boolean
  /** The reviewed scripts are already saved; the batch repairs are locked. */
  saved: boolean
  /** The server's review-gate suggestions, which say what clears each code. */
  suggestions?: readonly ImportRepairSuggestion[] | null
  scripts?: JMeterScripts
  openTimestampPolicy: () => void
  openBatchRepair: (type: string, group: number) => void
}

// The generated script a diagnostic location names (scripts[2].actions[0]);
// a single-script location names the first.
export function jmeterScriptIndex(location?: string): number {
  const match = /\bscripts\[(\d+)\]/.exec(location || '')
  return match ? Number(match[1]) : 0
}

// The script a suggestion's source group names; a plan-wide suggestion (no
// group) opens on the first.
export function jmeterSourceGroupIndex(scripts: JMeterScripts | undefined, sourceGroupID?: string): number {
  if (!sourceGroupID) return 0
  const index = (scripts || []).findIndex(script => script.config?.jmeter?.source_group_id === sourceGroupID)
  return index < 0 ? 0 : index
}

/** The suggestion that resolves one code on one script, else the plan-wide one. */
function suggestionForCode(context: JMeterIssueActionContext, code: string, scriptIndex: number): ImportRepairSuggestion | undefined {
  const matching = (context.suggestions || []).filter(suggestion => suggestion.code === code)
  const group = context.scripts?.[scriptIndex]?.config?.jmeter?.source_group_id
  return matching.find(suggestion => group && suggestion.source_group_id === group) || matching.find(suggestion => !suggestion.source_group_id) || matching[0]
}

function reinspectsTimestampPolicy(suggestion: ImportRepairSuggestion): boolean {
  return Boolean(suggestion.reinspect_options?.some(option => option.field === 'time_mode'))
}

// The credential bindings a saved script still accepts, from the blocked
// step's repair in the Visual Editor (POST /scripts/{id}/repairs).
const SAVED_SCRIPT_REPAIRS = new Set(['bind_kafka_credentials', 'bind_eventhubs_credentials'])

// What clears a batch-repairable gate here, in the words of the controls that
// apply it. The API's guidance names the operation and its request fields
// (bind_kafka_credentials, client_id_secret), which the form never shows.
function batchRepairResolution(type: string, context: Pick<JMeterIssueActionContext, 'batch' | 'saved'>): string | undefined {
  if (context.saved) {
    return SAVED_SCRIPT_REPAIRS.has(type)
      ? 'These scripts are already saved, so the reviewed repairs for all scripts are locked. Open the saved script in the Visual Editor and bind the credentials from the blocked step\'s repair, or import the plan again and apply the repair before saving.'
      : 'These scripts are already saved, so the reviewed repairs for all scripts are locked. Import the plan again and apply the repair before saving to clear this gate.'
  }
  if (!context.batch) {
    return SAVED_SCRIPT_REPAIRS.has(type)
      ? 'This review is not a revisable batch (import artifact storage is unavailable), so the reviewed repairs for all scripts cannot be applied here. Save the scripts, then open the saved script in the Visual Editor and bind the credentials from the blocked step\'s repair. The Credentials tab shows the references but does not clear this gate.'
      : 'This review is not a revisable batch (import artifact storage is unavailable), so the repair that clears this gate cannot be applied here. Replay stays blocked; import the plan again once artifact storage is available.'
  }
  const code = Object.keys(BATCH_REPAIR_BY_CODE).find(candidate => BATCH_REPAIR_BY_CODE[candidate] === type)
  return code ? importReviewResolution(code) : undefined
}

function batchRepairAction(type: string, group: number, context: JMeterIssueActionContext): ImportIssueAction {
  const resolution = batchRepairResolution(type, context)
  if (context.saved || !context.batch) return { resolution }
  return { label: BATCH_REPAIR_LABELS[type] || 'Open this repair', run: () => context.openBatchRepair(type, group), ...(resolution ? { resolution } : {}) }
}

/**
 * A suggestion a batch repair clears, with its action in the words of the
 * form that applies it, or of why that form is not available here.
 */
export function jmeterSuggestionInFormWords(suggestion: ImportRepairSuggestion, context: Pick<JMeterIssueActionContext, 'batch' | 'saved'>): ImportRepairSuggestion {
  const target = jmeterSuggestionTarget(suggestion)
  const resolution = target?.kind === 'batch_repair' ? batchRepairResolution(target.type, context) : undefined
  return resolution ? { ...suggestion, action: resolution } : suggestion
}

// The generated step a diagnostic location names (scripts[1].actions[0]; a
// bare actions[0] on a single-script import).
function locatedStep(scripts: JMeterScripts | undefined, location?: string) {
  const match = /^scripts\[(\d+)\]\.(.+)$/.exec(location || '')
  const script = match ? scripts?.[Number(match[1])] : scripts?.length === 1 ? scripts[0] : undefined
  const path = match ? match[2] : location
  return script && path ? importActionEntries(script).find(entry => entry.path === path)?.action : undefined
}

// The gates no edit, repair or re-import clears in this release
// (jmeterProductGatedReviewCodes in pkg/convert/jmeter/requestimport.go).
const JMETER_PRODUCT_GATED_CODES = new Set(['jmeter_messaging_compatibility_unverified', 'jmeter_kafka_batch_allocation_parity_unverified'])

const AWAITING_RUNTIME_AFTER_FIX = 'The step stays blocked by its messaging replay gate after this fix: nothing clears that gate in this release.'

// A blocker of a step whose import review resolution is
// awaiting_runtime_support, and that no repair clears: the step's own gate
// or one of its product-gated reason codes is unclearable. Any other reason
// code is a source defect a corrected source clears, so it keeps its
// resolution and says the step's gate remains.
function awaitingRuntimeSupportAction(issue: ImportWorkspaceIssue, context: JMeterIssueActionContext): ImportIssueAction | undefined {
  if (issue.severity !== 'blocker' || !issue.code) return undefined
  const step = locatedStep(context.scripts, issue.location)
  if (!actionAwaitsRuntimeSupport(step)) return undefined
  if (issue.code === 'import_review_required' || JMETER_PRODUCT_GATED_CODES.has(issue.code)) return { unclearable: true }
  const review = step!.metadata!.import_review as { reason_codes?: unknown }
  if (!Array.isArray(review.reason_codes) || !review.reason_codes.includes(issue.code)) return undefined
  return { resolution: `${issue.resolution || 'Correct the source or configuration, then import it again.'} ${AWAITING_RUNTIME_AFTER_FIX}` }
}

export function jmeterIssueAction(issue: ImportWorkspaceIssue, context: JMeterIssueActionContext): ImportIssueAction | undefined {
  if (!issue.code) return undefined
  const group = jmeterScriptIndex(issue.location || issue.source)
  const suggestion = suggestionForCode(context, issue.code, group)
  // No repair applies here and the issue's step awaits runtime support.
  const fallback = () => awaitingRuntimeSupportAction(issue, context)
  if (suggestion) {
    if (suggestion.clearable === false) return { unclearable: true }
    if (reinspectsTimestampPolicy(suggestion)) return { label: 'Choose a timestamp policy', run: context.openTimestampPolicy }
    if (suggestion.batch_operation && BATCH_REPAIR_TYPES.has(suggestion.batch_operation)) {
      // A diagnostic's own location is more specific than a plan-wide suggestion.
      return batchRepairAction(suggestion.batch_operation, group, context)
    }
    return fallback()
  }
  if (issue.code === 'jmeter_timestamp_timezone_required') {
    return { label: 'Choose a timestamp policy', run: context.openTimestampPolicy }
  }
  const type = BATCH_REPAIR_BY_CODE[issue.code]
  return type ? batchRepairAction(type, group, context) : fallback()
}

export type JMeterSuggestionTarget =
  | { kind: 'batch_repair'; type: string; group: number }
  | { kind: 'timestamp_policy' }
  | { kind: 'not_clearable' }
  | null

/**
 * What a repair suggestion opens in the JMeter importer, from the fields the
 * API sets for it: a batch repair (batch_operation, on the source_group_id
 * script), the timestamp policy (a time_mode reinspect option), or nothing
 * clears it yet (clearable false). action is resolution text, not an
 * operation, so it never selects a target.
 */
export function jmeterSuggestionTarget(suggestion: ImportRepairSuggestion, scripts?: JMeterScripts): JMeterSuggestionTarget {
  if (suggestion.clearable === false) return { kind: 'not_clearable' }
  const operation = (suggestion.batch_operation || '').trim()
  if (BATCH_REPAIR_TYPES.has(operation)) return { kind: 'batch_repair', type: operation, group: jmeterSourceGroupIndex(scripts, suggestion.source_group_id) }
  if (reinspectsTimestampPolicy(suggestion)) return { kind: 'timestamp_policy' }
  return null
}

export function jmeterBatchRepairLabel(type: string): string {
  return BATCH_REPAIR_LABELS[type] || 'Open this repair'
}

type JMeterDiagnostic = NonNullable<JMeterImportResult['diagnostics']>[number]

/**
 * Diagnostics with detail (what the finding means) and resolution (what
 * clears it) apart. The server's diagnostics[].resolution wins; without it
 * the matching review-gate suggestion's action is the resolution. An older
 * API wrote a gate's detail as "<Detail> <Resolution>", and a transitional
 * one may send both, so a detail ending in the resolution loses that suffix:
 * "Why this matters" and "Available resolution" never repeat each other, and
 * both stay the server's words.
 */
export function jmeterDiagnosticsWithGuidance(diagnostics: JMeterImportResult['diagnostics'], suggestions?: readonly ImportRepairSuggestion[] | null): JMeterImportResult['diagnostics'] {
  if (!diagnostics) return diagnostics
  return diagnostics.map((item): JMeterDiagnostic => {
    const resolution = (item.resolution || (item.code && suggestions?.find(candidate => candidate.code === item.code && candidate.action)?.action) || '').trim()
    if (!resolution) return item
    const detail = item.detail.trim()
    const split = detail.endsWith(` ${resolution}`) ? detail.slice(0, -resolution.length).trim() : item.detail
    return split === item.detail && resolution === item.resolution ? item : { ...item, detail: split, resolution }
  })
}
