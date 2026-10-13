import type { ScriptActionDefinition as Action, ScriptDefinition, ScriptExtractorDefinition as Extractor } from '../types/scripts'
import { evaluateExtractor, boundaryExpression } from './extractorMatch'
import { placeSteps, stepsAfter, type StepPosition } from './referenceScopeModel'
import { addCorrelationExtractorIfMissing, applyCorrelationRepairToAction, type CorrelationRepairLocation } from './scriptCorrelationRepair'
import { resolveNestedAction, updateNestedAction } from './scriptEditorJsonModel'
import { uniqueVariableName } from './scriptDataUsageModel'

// What a piece of a replayed response can become: a check that the response
// contains it, a value saved between two boundaries, and that value used in
// later requests in place of the literal (LoadRunner's correlation).

const MAX_BOUNDARY = 64

/**
 * Boundaries that save exactly `body[start, end)`: the left one is the
 * shortest text before the value whose first occurrence ends where the value
 * starts; the right one the shortest after it that the value does not
 * contain. Null when no boundaries up to 64 characters do that. A value at
 * the end of a body the replay kept only part of has none: the response goes
 * on past it.
 */
export function suggestBoundaries(body: string, start: number, end: number, bodyIsWhole = true): { left: string; right: string } | null {
  if (start <= 0 || end <= start || end > body.length) return null
  let left = ''
  for (let size = 1; size <= Math.min(MAX_BOUNDARY, start); size++) {
    const candidate = body.slice(start - size, start)
    if (body.indexOf(candidate) + size === start) { left = candidate; break }
  }
  if (!left) return null
  let right = ''
  for (let size = 1; size <= Math.min(MAX_BOUNDARY, body.length - end); size++) {
    const candidate = body.slice(end, end + size)
    if (body.indexOf(candidate, start) === end) { right = candidate; break }
  }
  // At the very end of the body, no right boundary takes the rest.
  if (!right && (end !== body.length || !bodyIsWhole)) return null
  const expression = boundaryExpression(left, right)
  return evaluateExtractor('boundary', expression, body).matches[0] === body.slice(start, end) ? { left, right } : null
}

/**
 * A name for the selected value from what labels it in the response: a JSON
 * key, a form field's name, a query or cookie name, the path segment before
 * it, or the JSON key of the text it is in; else "value". Not one the script
 * uses yet.
 */
export function suggestValueName(body: string, start: number, taken: ReadonlySet<string>): string {
  const before = body.slice(Math.max(0, start - 200), start)
  const patterns = [
    /"([A-Za-z_][\w-]*)"\s*:\s*"?$/, // "token": "…
    /name=["']([A-Za-z_][\w-]*)["'][^<>]*value=["']$/i, // name="csrf" value="…
    /([A-Za-z_][\w-]*)=["']?$/, // sessionId=… or id="…
    /\/([A-Za-z_][\w-]*)\/$/, // /cart/…
    /"([A-Za-z_][\w-]*)"\s*:\s*"[^"]*$/, // "path": "/x/…
  ]
  const label = patterns.map(pattern => pattern.exec(before)?.[1]).find(Boolean)
  const base = (label || 'value').replace(/[^\w]/g, '_').replace(/^(\d)/, '_$1')
  return uniqueVariableName(base, taken)
}

/**
 * Which step sits where, by type and name (sequence requests included): while
 * it is unchanged, a replay's steps are still where they were, whatever checks,
 * saved values or values in requests changed since.
 */
export function stepLayoutKey(script: ScriptDefinition): string {
  return JSON.stringify(placeSteps(script).flatMap(({ action, position }) => [
    [position.phase, position.actionIndex, (position.stepPath || []).join('/'), action.type, action.name],
    ...(action.type === 'http_sequence' ? sequenceRequests(action, position).map(request => [request.position.phase, request.position.actionIndex, request.position.stepPath!.join('/'), request.action.type ?? request.action.method, request.action.name]) : []),
  ]))
}

/** One later use of a value: where in which step, with the text around it. */
export interface ValueUse {
  position: StepPosition
  stepName: string
  field: string
  /** The field in words: URL, body, or a header's name. */
  fieldLabel: string
  start: number
  end: number
  /** Up to 30 characters around the value, with … where the field goes on. */
  before: string
  after: string
}

const isRequest = (action: Action) => !action.control && action.type !== 'http_sequence' && (typeof action.target === 'string' || typeof action.body === 'string' || Boolean(action.header_list?.length || action.headers))
const isRecord = (value: unknown): value is Record<string, unknown> => Boolean(value) && typeof value === 'object' && !Array.isArray(value)
const REQUEST_SEGMENT = /^requests\[(\d+)\]$/

function occurrences(text: string, literal: string): number[] {
  const found: number[] = []
  for (let at = text.indexOf(literal); at >= 0; at = text.indexOf(literal, at + literal.length)) found.push(at)
  return found
}

/** The step at a position, wherever it sits. */
function stepAt(script: ScriptDefinition, position: StepPosition): Action | null {
  const list = position.phase === 'action' ? script.actions : script[position.phase]
  return resolveNestedAction(list?.[position.actionIndex], position.stepPath)
}

/** A sequence's requests from `from` on, each at its own position. */
function sequenceRequests(sequence: Action, position: StepPosition, from = 0): Array<{ action: Action; position: StepPosition }> {
  const requests: unknown[] = Array.isArray(sequence.params?.requests) ? sequence.params.requests : []
  return requests.flatMap((request, index) => index >= from && isRecord(request) ? [{ action: request as unknown as Action, position: { ...position, stepPath: [...(position.stepPath || []), `requests[${index}]`] } }] : [])
}

/**
 * Where the requests that run after `source` send `literal`: their URLs,
 * bodies and header values, with header offsets counted across the header's
 * values joined by newlines (as a correlation repair reads them). A request of
 * a sequence counts: the later ones of the source's own sequence when it runs
 * them in order (concurrent ones are prepared together), and those of every
 * later sequence.
 */
export function laterUses(script: ScriptDefinition, source: StepPosition, literal: string): ValueUse[] {
  if (!literal) return []
  const path = source.stepPath || []
  const child = REQUEST_SEGMENT.exec(path[path.length - 1] || '')
  const container: StepPosition = child ? { ...source, stepPath: path.length > 1 ? path.slice(0, -1) : undefined } : source
  const candidates: Array<{ action: Action; position: StepPosition }> = []
  if (child) {
    const sequence = stepAt(script, container)
    if (sequence?.type === 'http_sequence' && sequence.params?.execution_mode !== 'concurrent') candidates.push(...sequenceRequests(sequence, container, Number(child[1]) + 1))
  }
  for (const step of stepsAfter(script, container)) candidates.push(...(step.action.type === 'http_sequence' ? sequenceRequests(step.action, step.position) : [step]))
  const uses: ValueUse[] = []
  for (const { action, position } of candidates) {
    if (!isRequest(action)) continue
    const stepName = action.name || action.method || action.type || 'Request'
    const add = (field: string, fieldLabel: string, text: string) => {
      for (const at of occurrences(text, literal)) {
        const from = Math.max(0, at - 30), to = Math.min(text.length, at + literal.length + 30)
        uses.push({ position, stepName, field, fieldLabel, start: at, end: at + literal.length, before: `${from > 0 ? '…' : ''}${text.slice(from, at)}`, after: `${text.slice(at + literal.length, to)}${to < text.length ? '…' : ''}` })
      }
    }
    if (typeof action.target === 'string') add('request_url', 'URL', action.target)
    if (typeof action.body === 'string') add('request_body', 'Body', action.body)
    const byName = new Map<string, { label: string; values: string[] }>()
    const entries = action.header_list?.length ? action.header_list.map(header => [header.name, header.value] as const) : Object.entries(action.headers || {})
    for (const [name, value] of entries) {
      const key = name.trim().toLowerCase()
      const group = byName.get(key) || { label: name.trim(), values: [] }
      group.values.push(value)
      byName.set(key, group)
    }
    for (const [key, group] of byName) add(`request_headers.${key}`, `Header ${group.label}`, group.values.join('\n'))
  }
  return uses
}

/** The repair locations of the chosen uses, by step. */
export function repairLocations(uses: ValueUse[]): Array<{ position: StepPosition; locations: CorrelationRepairLocation[] }> {
  const byStep = new Map<string, { position: StepPosition; locations: CorrelationRepairLocation[] }>()
  for (const use of uses) {
    const key = `${use.position.phase}:${use.position.actionIndex}#${(use.position.stepPath || []).join('/')}`
    const entry = byStep.get(key) || { position: use.position, locations: [] }
    entry.locations.push({ field: use.field, start: use.start, end: use.end })
    byStep.set(key, entry)
  }
  return [...byStep.values()]
}

/** The script with one step changed, wherever it sits. */
export function updateStep(script: ScriptDefinition, position: StepPosition, update: (action: Action) => Action): ScriptDefinition {
  const key = position.phase === 'action' ? 'actions' : position.phase
  const list = [...(script[key] || [])]
  if (!list[position.actionIndex]) return script
  list[position.actionIndex] = updateNestedAction(list[position.actionIndex], position.stepPath, update)
  return { ...script, [key]: list }
}

/**
 * The value saved on the source step and used in place of the literal in the
 * chosen later uses (one transaction). A step whose uses cannot all be
 * replaced exactly (an Aura body under structural review, a moved header) is
 * left as it was and counted as skipped.
 */
export function applyValueCorrelation(script: ScriptDefinition, source: StepPosition, extractor: Extractor, uses: ValueUse[]): { script: ScriptDefinition; replaced: number; skipped: number } {
  let next = updateStep(script, source, action => addCorrelationExtractorIfMissing(action, extractor))
  let replaced = 0
  let skipped = 0
  for (const { position, locations } of repairLocations(uses)) {
    let changed = false
    next = updateStep(next, position, action => {
      const repaired = applyCorrelationRepairToAction(action, { failingActionIndex: position.actionIndex, requestLocations: locations, replacement: `{{var.${extractor.name}}}` })
      changed = repaired !== action
      return repaired
    })
    if (changed) replaced += locations.length
    else skipped += locations.length
  }
  return { script: next, replaced, skipped }
}
