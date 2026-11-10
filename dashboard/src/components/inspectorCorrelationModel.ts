import type { BrowserReplayEvent } from '../types/replay'
import type { ScriptActionDefinition as Action, ScriptDefinition, ScriptExtractorDefinition } from '../types/scripts'
import { replayEventStepKey, replayEventStepPath } from './actionChecksModel'
import { stepsAfter, stepTemplateText, templateTokens, type StepPosition } from './referenceScopeModel'
import { failingExtractorName, unresolvedVariables, type ReplayVariableDiff } from './requestResponseInspectorModel'
import { resolveAuthorJsonScopeByKey, resolveNestedAction } from './scriptEditorJsonModel'

// The Correlation table: what a replayed action extracts, what it could not
// extract or did not apply, the {{var.…}} values it reads, and what it
// changed. Values are the engine's as published: a value it redacted or
// masked stays exactly as it came.

export type CorrelationTone = 'ok' | 'bad' | 'warn' | 'neutral'

export interface CorrelationRow {
  key: string
  direction: 'Extracts' | 'Reads' | 'Sets'
  variable: string
  /** Where the value comes from or is read; null when that is not known. */
  where: string | null
  /** The value before this action: absent when there is no before to show, null when it was unset. */
  before?: string | null
  /** The value after (or the value read); null when the event carries none. */
  value: string | null
  /** Shown in place of a missing value (null value). */
  missingValue?: string
  result: string
  tone: CorrelationTone
  /** Later steps of the script that reference the variable; absent when the script is not at hand. */
  laterSteps?: number
  /** The value Copy copies; absent when there is none to copy or it is masked or redacted. */
  copyValue?: string
}

export interface CorrelationNote {
  key: string
  text: string
  /** A retention or classification notice (announced as status). */
  status?: boolean
}

export interface CorrelationStreamedChild {
  key: string
  label: string
  passed: boolean
  error?: string
}

export interface CorrelationTableModel {
  rows: CorrelationRow[]
  notes: CorrelationNote[]
  streamedChildren: CorrelationStreamedChild[]
  /** Streamed child events the retained evidence left out. */
  streamedChildrenOmitted: number
  /**
   * {{var.…}} names the authored request reads whose resolution the event does
   * not record: one quiet line below the table, not a row of Unknowns each.
   */
  unknownReads: string[]
}

const positiveCount = (value: unknown): number => typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : 0
const plural = (count: number, one: string, many: string) => count === 1 ? one : many

/** The engine bounds its list of unresolved names (pkg/engine maxReplayUnresolvedVariables). */
const MAX_UNRESOLVED_LISTED = 10

/** A value the engine masked or redacted (or a dashboard mask): never offered for copying. */
export function isMaskedCorrelationValue(value: string): boolean {
  return /\[(?:redacted|masked)\]/i.test(value) || value.includes('\u2022')
}

const copyable = (value: string | null | undefined): { copyValue?: string } =>
  typeof value === 'string' && value !== '' && !isMaskedCorrelationValue(value) ? { copyValue: value } : {}

/** The variable name of a `{{var.NAME}}` expression, or null for any other reference. */
function variableName(expression: string): string | null {
  return expression.startsWith('var.') && expression.length > 4 ? expression.slice(4) : null
}

/** The {{var.…}} names in a text. */
function variableNamesIn(text: string): string[] {
  if (!text.includes('{{')) return []
  return templateTokens(text).flatMap(token => {
    const name = variableName(token.expression)
    return name ? [name] : []
  })
}

/**
 * Where a request reads each {{var.NAME}}: URL, Body or Header plus its name, with
 * "×n" when one field reads it more than once. In the order first read.
 */
export function requestVariableReads(action: Action | null | undefined): Map<string, string[]> {
  const counts = new Map<string, Map<string, number>>()
  if (!action) return new Map()
  const add = (field: string, text: unknown) => {
    if (typeof text !== 'string') return
    for (const name of variableNamesIn(text)) {
      const fields = counts.get(name) || new Map<string, number>()
      fields.set(field, (fields.get(field) || 0) + 1)
      counts.set(name, fields)
    }
  }
  add('URL', action.target)
  const headers = action.header_list?.length
    ? action.header_list.map(header => [header?.name, header?.value] as const)
    : Object.entries(action.headers || {})
  for (const [name, value] of headers) add(`Header ${String(name ?? '').trim() || '(unnamed)'}`, value)
  add('Body', action.body)
  const reads = new Map<string, string[]>()
  for (const [name, fields] of counts) reads.set(name, [...fields].map(([field, count]) => count > 1 ? `${field} ×${count}` : field))
  return reads
}

const REQUEST_SEGMENT = /^requests\[(\d+)\]$/
const isRecord = (value: unknown): value is Record<string, unknown> => Boolean(value) && typeof value === 'object' && !Array.isArray(value)

function phaseList(script: ScriptDefinition, phase: StepPosition['phase']): Action[] | undefined {
  return phase === 'action' ? script.actions : script[phase]
}

/** The step a replay event ran, as a script position. */
export function replayEventStepPosition(event: BrowserReplayEvent): StepPosition | null {
  if (event.phase !== 'init' && event.phase !== 'action' && event.phase !== 'teardown') return null
  const stepPath = replayEventStepPath(event)
  return { phase: event.phase, actionIndex: event.action_index ?? 0, ...(stepPath.length ? { stepPath } : {}) }
}

/**
 * How many steps that run after `source` reference each of `names` as
 * {{var.NAME}} (in any executable field). A request of a sequence counts on
 * its own: the later requests of the source's own sequence when it runs them
 * in order, then every later step (a later sequence counts once).
 */
export function laterStepReads(script: ScriptDefinition, source: StepPosition, names: Iterable<string>): Map<string, number> {
  const wanted = new Set(names)
  const counts = new Map<string, number>()
  if (wanted.size === 0) return counts
  const texts: string[] = []
  const path = source.stepPath || []
  const child = REQUEST_SEGMENT.exec(path[path.length - 1] || '')
  const container: StepPosition = child ? { ...source, stepPath: path.length > 1 ? path.slice(0, -1) : undefined } : source
  if (child) {
    const sequence = resolveNestedAction(phaseList(script, container.phase)?.[container.actionIndex], container.stepPath)
    const requests: unknown[] = Array.isArray(sequence?.params?.requests) ? sequence.params.requests : []
    if (sequence?.type === 'http_sequence' && sequence.params?.execution_mode !== 'concurrent') {
      requests.slice(Number(child[1]) + 1).forEach(request => { if (isRecord(request)) texts.push(stepTemplateText(request as unknown as Action)) })
    }
  }
  // A position the script does not have (it changed since the replay) has no steps after it.
  for (const step of stepsAfter(script, container)) texts.push(stepTemplateText(step.action))
  for (const text of texts) {
    for (const name of new Set(variableNamesIn(text))) if (wanted.has(name)) counts.set(name, (counts.get(name) || 0) + 1)
  }
  return counts
}

function sourceLabel(source: string | undefined): string {
  const trimmed = (source || '').trim()
  return trimmed === '' ? 'body' : trimmed
}

/** The definition that saved this variable: by name, or as one of a save-all extractor's name_N / name_count. */
function definitionFor(extractors: readonly ScriptExtractorDefinition[] | null | undefined, variable: string): ScriptExtractorDefinition | null {
  if (!Array.isArray(extractors)) return null
  const exact = extractors.find(extractor => extractor?.name === variable)
  if (exact) return exact
  return extractors.find(extractor => extractor?.name && extractor.mode === 'all'
    && variable.startsWith(`${extractor.name}_`) && /^(\d+|count)$/.test(variable.slice(extractor.name.length + 1))) ?? null
}

function definitionWhere(extractor: ScriptExtractorDefinition | null): string | null {
  if (!extractor) return null
  const type = (extractor.type || '').trim()
  return type ? `${sourceLabel(extractor.source)} · ${type}` : sourceLabel(extractor.source)
}

/** "No match" for an extractor that found nothing, else "Failed" (it failed or its output was rejected). */
function failingExtractorResult(event: BrowserReplayEvent, name: string): string {
  const quoted = JSON.stringify(name).slice(1, -1)
  for (const text of [event.error_message, event.diagnosis?.cause]) {
    if (text?.includes(`extractor "${quoted}" found no match`) || text?.includes(`extractor "${name}" found no match`)) return 'No match'
  }
  return 'Failed'
}

export interface CorrelationTableInput {
  event: BrowserReplayEvent
  variableDiff: ReplayVariableDiff
  /** The authored step, only while the replay matches the current script. */
  action?: Action | null
  /** Later steps referencing each variable (laterStepReads), only while the script is at hand. */
  laterReads?: ReadonlyMap<string, number> | null
  /** Runtime evidence was withheld this run: the engine's unresolved list may be empty for that alone. */
  runtimeEvidenceUnavailable?: boolean
}

/** The rows and notes of the Correlation table for one replay event. */
export function buildCorrelationTable({ event, variableDiff, action, laterReads, runtimeEvidenceUnavailable = false }: CorrelationTableInput): CorrelationTableModel {
  const rows: CorrelationRow[] = []
  const notes: CorrelationNote[] = []
  const extractors = action?.extractors
  const later = (variable: string) => laterReads ? { laterSteps: laterReads.get(variable) || 0 } : {}
  const extracted = new Set<string>()
  // A live table step's result is shown by its own panel; only its variable changes and reads are listed.
  const extractions = event.action_type !== 'live_table'
  const children = extractions ? event.streamed_children || [] : []

  if (extractions && event.credential_classification_error) {
    notes.push({ key: 'classification', text: `Credential classification: ${event.credential_classification_error}`, status: true })
  }
  if (extractions && event.protocol_evidence?.concurrent_group_extractions_omitted === true) {
    const count = positiveCount(event.protocol_evidence.concurrent_group_extractions_omitted_count)
    notes.push({
      key: 'concurrent-omitted',
      text: `${count ? `${count} ${plural(count, 'extraction was', 'extractions were')} omitted` : 'Some extractions were omitted'} from the retained debugger evidence. The displayed values may be incomplete.`,
      status: true,
    })
  }

  const failing = extractions ? failingExtractorName(event) : null
  if (failing && !Object.prototype.hasOwnProperty.call(event.extractions || {}, failing)) {
    extracted.add(failing)
    rows.push({
      key: `failing:${failing}`, direction: 'Extracts', variable: failing,
      where: definitionWhere(definitionFor(extractors, failing)),
      value: null, missingValue: 'No value', result: failingExtractorResult(event, failing), tone: 'bad', ...later(failing),
    })
  }

  const extractRow = (keyPrefix: string, variable: string, value: string, where: string | null) => {
    extracted.add(variable)
    const diff = Object.prototype.hasOwnProperty.call(variableDiff, variable) ? variableDiff[variable] : undefined
    rows.push({
      key: `${keyPrefix}:${variable}`, direction: 'Extracts', variable, where,
      ...(diff ? { before: diff.previous ?? null } : {}),
      value, result: 'Extracted', tone: 'ok', ...later(variable), ...copyable(value),
    })
  }
  const unappliedRow = (keyPrefix: string, variable: string, value: string, where: string | null) => {
    rows.push({ key: `${keyPrefix}:${variable}`, direction: 'Extracts', variable, where, value, result: 'Not applied', tone: 'warn', ...copyable(value) })
  }
  // An extraction a later one replaced: its value is listed, with no before → after.
  const overriddenRow = (keyPrefix: string, variable: string, value: string, where: string | null) => {
    rows.push({ key: `${keyPrefix}:${variable}`, direction: 'Extracts', variable, where, value, result: 'Overridden', tone: 'neutral', ...copyable(value) })
  }

  if (extractions) {
    for (const [variable, value] of Object.entries(event.extractions || {})) extractRow('extract', variable, value, definitionWhere(definitionFor(extractors, variable)))
    for (const [variable, value] of Object.entries(event.unapplied_extractions || {})) unappliedRow('unapplied', variable, value, definitionWhere(definitionFor(extractors, variable)))
    const unappliedOmitted = positiveCount(event.unapplied_extractions_omitted_count)
    if (unappliedOmitted) {
      notes.push({ key: 'unapplied-omitted', text: `${unappliedOmitted} not applied ${plural(unappliedOmitted, 'extraction was', 'extractions were')} omitted from the retained debugger evidence.`, status: true })
    }
  }

  const streamedChildren: CorrelationStreamedChild[] = children.map((child, index) => {
    const label = child.action_name || `Streamed child ${index + 1}`
    const where = `Streamed child ${label}`
    // Each child's extraction is listed. The parent's own extraction of the
    // same name wins, then a later child's (as the replay applies them); one
    // replaced that way is Overridden, not Extracted.
    for (const [variable, value] of Object.entries(child.extractions || {})) {
      const overridden = Object.prototype.hasOwnProperty.call(event.extractions || {}, variable)
        || children.slice(index + 1).some(next => Object.prototype.hasOwnProperty.call(next.extractions || {}, variable))
      if (overridden) overriddenRow(`child${index}`, variable, value, where)
      else extractRow(`child${index}`, variable, value, where)
    }
    for (const [variable, value] of Object.entries(child.unapplied_extractions || {})) unappliedRow(`child${index}-unapplied`, variable, value, where)
    if (child.credential_classification_error) {
      notes.push({ key: `child${index}-classification`, text: `${label}: credential classification: ${child.credential_classification_error}`, status: true })
    }
    const applied = positiveCount(child.extractions_omitted_count)
    if (applied) notes.push({ key: `child${index}-omitted`, text: `${label}: ${applied} applied ${plural(applied, 'extraction was', 'extractions were')} omitted from retained evidence.`, status: true })
    const unapplied = positiveCount(child.unapplied_extractions_omitted_count)
    if (unapplied) notes.push({ key: `child${index}-unapplied-omitted`, text: `${label}: ${unapplied} not applied ${plural(unapplied, 'extraction was', 'extractions were')} omitted from retained evidence.`, status: true })
    return { key: `child${index}`, label, passed: child.success, ...(child.error_message ? { error: child.error_message } : {}) }
  })

  // Reads: values never set (as the engine listed them) first, then the
  // authored request's other references.
  const unset = unresolvedVariables(event)
  const unsetNames = new Set(unset)
  const reads = requestVariableReads(action)
  // The engine lists unresolved names only on a failure it annotated, with
  // runtime evidence published, and at most ten (an empty list is left out,
  // and events from before the list carry none). Only a list it sent, under
  // the limit, with no classification error, says the other reads resolved.
  const resolvedKnown = !event.success
    && Array.isArray(event.diagnosis?.unresolved_variables)
    && !runtimeEvidenceUnavailable
    && !event.credential_classification_error
    && unset.length < MAX_UNRESOLVED_LISTED
  for (const name of unset) {
    const fields = reads.get(name)
    rows.push({
      key: `read:${name}`, direction: 'Reads', variable: name, where: fields ? fields.join(', ') : null,
      value: `Left as {{var.${name}}}`, result: 'Never set this run', tone: 'bad',
    })
  }
  const unknownReads: string[] = []
  for (const [name, fields] of reads) {
    if (unsetNames.has(name)) continue
    if (!resolvedKnown) {
      unknownReads.push(name)
      continue
    }
    rows.push({
      key: `read:${name}`, direction: 'Reads', variable: name, where: fields.join(', '),
      value: null, missingValue: 'Not recorded', result: 'Resolved', tone: 'ok',
    })
  }

  for (const [variable, diff] of Object.entries(variableDiff)) {
    if (extracted.has(variable)) continue
    rows.push({ key: `set:${variable}`, direction: 'Sets', variable, where: null, before: diff.previous ?? null, value: diff.next, result: 'Changed', tone: 'neutral', ...later(variable), ...copyable(diff.next) })
  }

  return { rows, notes, streamedChildren, streamedChildrenOmitted: extractions ? positiveCount(event.streamed_children_omitted_count) : 0, unknownReads }
}

/** Variables whose later uses the table counts: what the action extracted (or failed to) and changed. */
export function correlationVariablesSet(event: BrowserReplayEvent, variableDiff: ReplayVariableDiff): string[] {
  const names = new Set<string>(Object.keys(variableDiff))
  if (event.action_type !== 'live_table') {
    for (const name of Object.keys(event.extractions || {})) names.add(name)
    for (const child of event.streamed_children || []) for (const name of Object.keys(child.extractions || {})) names.add(name)
    const failing = failingExtractorName(event)
    if (failing) names.add(failing)
  }
  return [...names]
}

/** The quiet line for reads whose resolution the event does not record, or null for none. */
export function unknownReadsText(names: readonly string[]): string | null {
  if (names.length === 0) return null
  const list = names.length === 1 ? names[0] : `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`
  return `Also reads ${list}. The run did not record whether ${names.length === 1 ? 'it had a value' : 'they had values'}.`
}

/** Whether the table has anything to show. */
export function correlationTableIsEmpty(model: CorrelationTableModel): boolean {
  return model.rows.length === 0 && model.notes.length === 0 && model.streamedChildren.length === 0 && model.streamedChildrenOmitted === 0
    && model.unknownReads.length === 0
}

/**
 * The Correlation table for an event, as the inspector shows it: the authored
 * step and later uses come from the script only while the replay matches it
 * (pass null otherwise). The tab badge counts the same rows.
 */
export function correlationTableForEvent(
  event: BrowserReplayEvent,
  variableDiff: ReplayVariableDiff,
  script: ScriptDefinition | null,
  runtimeEvidenceUnavailable = false,
): CorrelationTableModel {
  const stepKey = replayEventStepKey(event)
  const action = script && stepKey ? resolveAuthorJsonScopeByKey(script, stepKey)?.action ?? null : null
  const position = replayEventStepPosition(event)
  const laterReads = script && action && position ? laterStepReads(script, position, correlationVariablesSet(event, variableDiff)) : null
  return buildCorrelationTable({ event, variableDiff, action, laterReads, runtimeEvidenceUnavailable })
}
