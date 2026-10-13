// What a step can reference, and what it references that nothing defines. One
// scanner serves the reference picker, the unresolved-reference warnings, the
// "This step produces" panel and (later) the data panel. It follows the
// engine's template roots (pkg/engine/vuser_resolve.go): var, param, data,
// env, secret, file, run, fn and fake.
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'

type Action = ScriptActionDefinition
type Json = Record<string, unknown>
export type ReferencePhase = 'init' | 'action' | 'teardown'

export type ReferenceGroup = 'parameters' | 'data' | 'steps' | 'environment' | 'secrets' | 'functions'

export const REFERENCE_GROUP_LABELS: Record<ReferenceGroup, string> = {
  parameters: 'Parameters',
  data: 'Data',
  steps: 'Earlier steps',
  environment: 'Environment and run',
  secrets: 'Secrets',
  functions: 'Functions',
}

/** Where a step sits: its phase, top-level index and nested path ("then[0]", "branches[1].steps[0]"). */
export interface StepPosition {
  phase: ReferencePhase
  actionIndex: number
  stepPath?: string[]
}

export interface ReferenceItem {
  group: ReferenceGroup
  /** The expression inside the braces: "param.user", "var.row.record_id". */
  expression: string
  /** What it is, in a few words. */
  detail: string
  /** Set only on some paths to here (inside an if, a switch case or a loop). */
  mayBeUnset?: boolean
  /** The step that sets it (earlier steps only). */
  source?: StepPosition & { name: string }
}

export interface ReferenceScope {
  items: ReferenceItem[]
  byExpression: Map<string, ReferenceItem>
  /** Variables set only by later steps, for "is set only by a later step". */
  laterVariables: Map<string, StepPosition & { name: string }>
  parameterNames: Set<string>
  dataSources: Map<string, string[]>
}

const TEMPLATE = /\{\{\s*([^{}]+?)\s*\}\}/g

export interface TemplateToken {
  /** The whole token, braces included. */
  text: string
  expression: string
  root: string
  from: number
  to: number
}

/** The `{{...}}` references in a text, in order. */
export function templateTokens(text: string): TemplateToken[] {
  const tokens: TemplateToken[] = []
  for (const match of text.matchAll(TEMPLATE)) {
    const expression = match[1].trim()
    const dot = expression.indexOf('.')
    tokens.push({ text: match[0], expression, root: dot < 0 ? expression : expression.slice(0, dot), from: match.index!, to: match.index! + match[0].length })
  }
  return tokens
}

export const BUILTIN_FUNCTIONS: Array<{ expression: string; detail: string }> = [
  { expression: 'fn.uuid', detail: 'A random UUID, new each use' },
  { expression: 'fn.timestamp.iso8601', detail: 'The current time, ISO 8601' },
  { expression: 'fn.timestamp.unix', detail: 'The current time, Unix seconds' },
  { expression: 'fn.timestamp.unix_ms', detail: 'The current time, Unix milliseconds' },
  { expression: 'fn.random(1,100)', detail: 'A random whole number between two bounds' },
  { expression: 'fn.random_string(8)', detail: 'A random string of the given length' },
  { expression: 'fake.email', detail: 'A made-up email address' },
  { expression: 'fake.uuid', detail: 'A made-up UUID' },
]

/** A step's position in execution order: a segment per level (the branch list and the index in it). */
interface Segment { branch: string; index: number; conditional: boolean; lane: boolean }

function isRecord(value: unknown): value is Json {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function phaseActions(script: ScriptDefinition, phase: ReferencePhase): Action[] {
  return (phase === 'init' ? script.init : phase === 'teardown' ? script.teardown : script.actions) || []
}

/** A control step's child lists, with the step-path segment each child gets. */
function childLists(action: Action): Array<{ branch: string; steps: Action[]; segment: (index: number) => string; conditional: boolean; lane: boolean }> {
  const control = action.control as Json | undefined
  if (!control) return []
  const type = action.type
  const lists: Array<{ branch: string; steps: Action[]; segment: (index: number) => string; conditional: boolean; lane: boolean }> = []
  const steps = (value: unknown) => Array.isArray(value) ? value as Action[] : []
  if (type === 'control_if') {
    lists.push({ branch: 'then', steps: steps(control.steps), segment: index => `then[${index}]`, conditional: true, lane: false })
    lists.push({ branch: 'else', steps: steps(control.else), segment: index => `else[${index}]`, conditional: true, lane: false })
  } else if (type === 'control_loop' || type === 'ws_on_message') {
    lists.push({ branch: 'loop', steps: steps(control.steps), segment: index => `loop[${index}]`, conditional: true, lane: false })
  } else if (type === 'control_switch') {
    steps(control.cases).forEach((item, caseIndex) => lists.push({ branch: `case[${caseIndex}]`, steps: steps((item as unknown as Json).steps), segment: index => `case[${caseIndex}][${index}]`, conditional: true, lane: false }))
    lists.push({ branch: 'default', steps: steps(control.default), segment: index => `default[${index}]`, conditional: true, lane: false })
  } else if (type === 'control_parallel') {
    steps(control.branches).forEach((item, branchIndex) => lists.push({ branch: `branches[${branchIndex}]`, steps: steps((item as unknown as Json).steps), segment: index => `branches[${branchIndex}].steps[${index}]`, conditional: false, lane: true }))
  }
  return lists
}

interface Placed { action: Action; position: StepPosition; segments: Segment[] }

/** Every step of a script in execution order, with its position. */
function placeSteps(script: ScriptDefinition): Placed[] {
  const placed: Placed[] = []
  const visit = (action: Action, position: StepPosition, segments: Segment[]) => {
    placed.push({ action, position, segments })
    for (const list of childLists(action)) {
      list.steps.forEach((child, index) => visit(child, { ...position, stepPath: [...(position.stepPath || []), list.segment(index)] }, [...segments, { branch: `${segments.length}:${list.branch}`, index, conditional: list.conditional, lane: list.lane }]))
    }
  }
  const phases: ReferencePhase[] = ['init', 'action', 'teardown']
  phases.forEach((phase, phaseIndex) => phaseActions(script, phase).forEach((action, index) => visit(action, { phase, actionIndex: index }, [{ branch: `phase`, index: phaseIndex * 1_000_000 + index, conditional: false, lane: false }])))
  return placed
}

/**
 * Whether a producer runs before a step, and whether always: 'before' or
 * 'maybe' (only on some paths), or null when it does not come first (later,
 * the step itself or inside it, or another branch of the same if, switch or
 * parallel block).
 */
function precedes(producer: Segment[], step: Segment[]): 'before' | 'maybe' | null {
  let depth = 0
  while (depth < producer.length && depth < step.length && producer[depth].branch === step[depth].branch && producer[depth].index === step[depth].index) depth += 1
  if (depth === producer.length) return null // the step itself, or a block that contains it
  if (depth === step.length) return null // inside the step
  if (producer[depth].branch !== step[depth].branch) return null // another branch or lane of one block
  if (producer[depth].index > step[depth].index) return null
  // Inside an earlier sibling: conditional nesting below this level makes it optional.
  return producer.slice(depth + 1).some(segment => segment.conditional) ? 'maybe' : 'before'
}

export interface StepOutput { expression: string; detail: string; kind?: 'record_id' | 'record_version' | 'lease' }

// Per step object: an edit replaces only the step it changes, so typing in
// one step rescans only that step.
const outputCache = new WeakMap<Action, StepOutput[]>()
const textCache = new WeakMap<Action, string>()

/** The variables a step sets, by expression, with what each is. */
export function stepOutputs(action: Action): StepOutput[] {
  const cached = outputCache.get(action)
  if (cached) return cached
  const outputs = computeStepOutputs(action)
  outputCache.set(action, outputs)
  return outputs
}

function computeStepOutputs(action: Action): StepOutput[] {
  const outputs: StepOutput[] = []
  const params = isRecord(action.params) ? action.params : {}
  // A sequence's requests save values too (an imported concurrent group's web_reg_save_param).
  const requests = action.type === 'http_sequence' && Array.isArray(params.requests) ? params.requests.filter(isRecord) : []
  for (const owner of [action as unknown as Json, ...requests]) {
    for (const extractor of Array.isArray(owner.extractors) ? owner.extractors.filter(isRecord) : []) {
      const name = typeof extractor.name === 'string' ? extractor.name.trim() : ''
      if (name && !outputs.some(output => output.expression === `var.${name}`)) outputs.push({ expression: `var.${name}`, detail: `Saved by ${owner === (action as unknown as Json) ? 'its' : 'a request’s'} ${typeof extractor.type === 'string' ? extractor.type : 'extractor'} extractor` })
    }
  }
  const saved = (key: string) => typeof params[key] === 'string' && (params[key] as string).trim() ? (params[key] as string).trim() : ''
  if (action.type === 'http_oauth2_token') outputs.push({ expression: `var.${saved('save_as') || 'access_token'}`, detail: 'The access token' })
  if (['source_json', 'source_parameter', 'source_xml'].includes(action.type) && saved('save_param')) outputs.push({ expression: `var.${saved('save_param')}`, detail: 'Saved by the source call' })
  const prefix = saved('result_prefix')
  if (prefix && action.type === 'live_table') {
    for (const [field, detail] of [['status', 'The operation’s status'], ['revision', 'The table revision'], ['record_id', 'The row’s record ID'], ['record_version', 'The row’s version']] as const) {
      outputs.push({ expression: `var.${prefix}.${field}`, detail, ...(field === 'record_id' || field === 'record_version' ? { kind: field } : {}) })
    }
    const columns = new Set<string>()
    if (Array.isArray(params.columns)) for (const column of params.columns) if (typeof column === 'string') columns.add(column)
    for (const key of ['values', 'expected']) if (isRecord(params[key])) for (const column of Object.keys(params[key] as Json)) columns.add(column)
    for (const column of columns) outputs.push({ expression: `var.${prefix}.values.${column}`, detail: `The row’s ${column}` })
  } else if (prefix && action.type === 'native_javascript') {
    outputs.push({ expression: `var.${prefix}`, detail: 'Fields the script returns appear under this name' })
  }
  return outputs
}

function envKeys(script: ScriptDefinition): string[] {
  const config = (script.config || {}) as unknown as Json
  const keys = new Set<string>()
  for (const source of [config.environment, config.variables, config.env]) {
    if (isRecord(source)) Object.keys(source).forEach(key => keys.add(key))
  }
  return [...keys].sort()
}

const secretCache = new WeakMap<Action, string[]>()

/** The secret names a step references, cached per step object. */
function stepSecretNames(action: Action): string[] {
  const cached = secretCache.get(action)
  if (cached) return cached
  const names = templateTokens(stepTemplateText(action)).filter(token => token.root === 'secret').map(token => token.expression.slice('secret.'.length))
  secretCache.set(action, names)
  return names
}

/** Every `{{...}}` in a step's executable values (not imported evidence in metadata). */
export function stepTemplateText(action: Action): string {
  const cached = textCache.get(action)
  if (cached !== undefined) return cached
  // A block's own fields only: its steps are scanned as steps.
  const text = JSON.stringify({ target: action.target, selector: action.selector, value: action.value, headers: action.headers, header_list: action.header_list, body: action.body, params: action.params, auth: action.auth, assertions: action.assertions, extractors: action.extractors, condition: (action.control as Json | undefined)?.condition })
  textCache.set(action, text)
  return text
}

/**
 * What the step at `position` can reference: parameters, data columns, what
 * earlier steps set (an optional value set on some paths only says so),
 * environment and run values, secret names and built-in functions.
 */
export function buildReferenceScope(script: ScriptDefinition, position: StepPosition, options: { secretNames?: string[]; dataAliases?: Record<string, string[]> } = {}): ReferenceScope {
  const items: ReferenceItem[] = []
  const parameterNames = new Set((script.parameters || []).map(parameter => parameter.name).filter((name): name is string => Boolean(name)))
  for (const parameter of script.parameters || []) {
    if (!parameter.name) continue
    const sample = parameter.values?.[0]
    items.push({ group: 'parameters', expression: `param.${parameter.name}`, detail: `${parameter.type || 'list'} parameter${sample !== undefined && sample !== '' ? ` · e.g. ${String(sample).slice(0, 40)}` : ''}` })
  }
  const dataSources = new Map<string, string[]>()
  for (const source of script.data_sources || []) {
    if (!source.name) continue
    dataSources.set(source.name, (source.columns || []).filter((column): column is string => typeof column === 'string'))
    for (const column of source.columns || []) items.push({ group: 'data', expression: `data.${source.name}.${column}`, detail: `Column of data source ${source.name}` })
  }
  for (const [alias, columns] of Object.entries(options.dataAliases || {})) {
    if (dataSources.has(alias)) continue
    for (const column of columns) items.push({ group: 'data', expression: `data.${alias}.${column}`, detail: `Column of ${alias}, bound by a scenario at run time` })
  }
  const placed = placeSteps(script)
  const current = placed.find(item => item.position.phase === position.phase && item.position.actionIndex === position.actionIndex && (item.position.stepPath || []).join('/') === (position.stepPath || []).join('/'))
  const laterVariables = new Map<string, StepPosition & { name: string }>()
  const seen = new Map<string, ReferenceItem>()
  for (const producer of placed) {
    const outputs = stepOutputs(producer.action)
    if (!outputs.length) continue
    const order = current ? precedes(producer.segments, current.segments) : 'before'
    const source = { ...producer.position, name: producer.action.name || producer.action.type }
    for (const output of outputs) {
      if (order === null) {
        if (current && !laterVariables.has(output.expression) && producer !== current) laterVariables.set(output.expression, source)
        continue
      }
      const existing = seen.get(output.expression)
      // A later definition on every path supersedes an optional earlier one.
      if (existing && (existing.mayBeUnset !== true || order === 'maybe')) continue
      const item: ReferenceItem = { group: 'steps', expression: output.expression, detail: `${output.detail} · ${source.name}`, ...(order === 'maybe' ? { mayBeUnset: true } : {}), source }
      if (existing) items.splice(items.indexOf(existing), 1, item)
      else items.push(item)
      seen.set(output.expression, item)
    }
  }
  for (const key of seen.keys()) laterVariables.delete(key)
  for (const key of envKeys(script)) items.push({ group: 'environment', expression: `env.${key}`, detail: 'Set by the run’s environment' })
  items.push({ group: 'environment', expression: 'run.id', detail: 'The run’s ID, set at run time' })
  const secrets = new Set(options.secretNames || [])
  for (const placedStep of placed) for (const name of stepSecretNames(placedStep.action)) secrets.add(name)
  for (const name of [...secrets].sort()) items.push({ group: 'secrets', expression: `secret.${name}`, detail: 'Name only; the value is never shown' })
  for (const fn of BUILTIN_FUNCTIONS) items.push({ group: 'functions', ...fn })
  return { items, byExpression: new Map(items.map(item => [item.expression, item])), laterVariables, parameterNames, dataSources }
}

export interface UnresolvedReference {
  token: TemplateToken
  message: string
  /** Defined names close to the misspelled one, best first. */
  suggestions: string[]
  /** The variable or parameter name, for "add an extractor" / "make it a parameter". */
  name: string
}

function distance(left: string, right: string): number {
  const previous = Array.from({ length: right.length + 1 }, (_, index) => index)
  for (let i = 1; i <= left.length; i += 1) {
    let diagonal = previous[0]
    previous[0] = i
    for (let j = 1; j <= right.length; j += 1) {
      const above = previous[j]
      previous[j] = Math.min(previous[j] + 1, previous[j - 1] + 1, diagonal + (left[i - 1].toLowerCase() === right[j - 1].toLowerCase() ? 0 : 1))
      diagonal = above
    }
  }
  return previous[right.length]
}

function similar(expression: string, candidates: string[]): string[] {
  const limit = Math.max(2, Math.floor(expression.length / 4))
  return candidates
    .map(candidate => ({ candidate, score: distance(expression, candidate) }))
    .filter(entry => entry.score <= limit)
    .sort((left, right) => left.score - right.score || left.candidate.localeCompare(right.candidate))
    .slice(0, 3)
    .map(entry => entry.candidate)
}

/**
 * The references in `text` that nothing defines before the step: an
 * unknown parameter, a variable no earlier step sets, a column a data
 * source lacks, or an unknown root. Environment, run, secret, file and
 * function values are resolved at run time and are not flagged.
 */
export function unresolvedReferences(text: string, scope: ReferenceScope): UnresolvedReference[] {
  const unresolved: UnresolvedReference[] = []
  const candidates = [...scope.byExpression.keys()]
  for (const token of templateTokens(text)) {
    const rest = token.expression.slice(token.root.length + 1)
    let message = ''
    if (token.root === 'param') {
      if (!scope.parameterNames.has(rest)) message = `{{${token.expression}}} is not a parameter of this script`
    } else if (token.root === 'var') {
      // A var name falls back to a parameter of that name at run time.
      if (!scope.byExpression.has(token.expression) && !scope.parameterNames.has(rest)) {
        const later = scope.laterVariables.get(token.expression)
        message = later ? `{{${token.expression}}} is set only by a later step (${later.name})` : `{{${token.expression}}} is not set by an earlier step`
      }
    } else if (token.root === 'data') {
      const [source, ...column] = rest.split('.')
      const columns = scope.dataSources.get(source)
      if (columns && column.length && columns.length && !columns.includes(column.join('.'))) message = `{{${token.expression}}}: ${source} has no column ${column.join('.')}`
    } else if (!['env', 'secret', 'file', 'run', 'fn', 'fake'].includes(token.root)) {
      message = `{{${token.expression}}} does not start with var, param, data, env, secret, file, run, fn or fake`
    }
    if (!message) continue
    unresolved.push({ token, message, suggestions: similar(token.expression, candidates.filter(candidate => candidate.split('.')[0] === token.root || token.root.length < 3)), name: rest || token.expression })
  }
  return unresolved
}
