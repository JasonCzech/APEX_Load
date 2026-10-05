// CodeMirror assist extensions for the script JSON editor, driven by the
// DSL action catalog (GET /api/v1/dsl/actions) and the draft-validate
// endpoint (POST /api/v1/scripts/validate).
//
// - Completion: action types valid for the script's protocol, per-type
//   fields (with docs), and enum values — context-resolved from the JSON
//   syntax tree, so suggestions match the action object under the cursor.
// - Diagnostics: debounced server-side validation by the same Go validator
//   that gates launches; issue paths like "actions[2].control.steps[0]" are
//   mapped back to editor ranges through the syntax tree.

import { autocompletion, type Completion, type CompletionContext, type CompletionResult } from '@codemirror/autocomplete'
import { jsonParseLinter } from '@codemirror/lang-json'
import { ensureSyntaxTree, syntaxTree } from '@codemirror/language'
import { linter, type Diagnostic } from '@codemirror/lint'
import type { EditorState, Extension } from '@codemirror/state'
import { hoverTooltip, type Tooltip } from '@codemirror/view'
import type { SyntaxNode } from '@lezer/common'

import { APEX_DSL_SEMANTIC_MAX_DOC_CHARS } from './apexDslSemanticHighlighting'
import {
  dslActionTypesForProtocol,
  dslSpecForActionType,
  getCachedDslActionCatalog,
  type DslCatalogField,
} from './dslActionCatalog'

type JsonPathSegment = string | number

const ACTION_LIST_KEYS = new Set(['actions', 'init', 'teardown', 'steps', 'else', 'default'])

// Lezer JSON trees include punctuation tokens as siblings; only these node
// names are values for array-index counting and property-value extraction.
const JSON_VALUE_NODE_NAMES = new Set(['Object', 'Array', 'String', 'Number', 'True', 'False', 'Null', '⚠'])

function isJsonValueNode(node: SyntaxNode): boolean {
  return JSON_VALUE_NODE_NAMES.has(node.name)
}

function stringContent(state: EditorState, node: SyntaxNode): string {
  const raw = state.sliceDoc(node.from, node.to)
  return raw.replace(/^"/, '').replace(/"$/, '')
}

/** Path from the document root to `node`, innermost segment last. */
export function jsonPathForNode(state: EditorState, node: SyntaxNode): JsonPathSegment[] {
  const segments: JsonPathSegment[] = []
  let current: SyntaxNode | null = node
  while (current) {
    const parent: SyntaxNode | null = current.parent
    if (!parent) break
    if (parent.name === 'Property' && current.name !== 'PropertyName') {
      const nameNode = parent.firstChild
      if (nameNode?.name === 'PropertyName') {
        segments.unshift(stringContent(state, nameNode))
      }
    } else if (parent.name === 'Array') {
      let index = 0
      for (let sibling = current.prevSibling; sibling; sibling = sibling.prevSibling) {
        if (isJsonValueNode(sibling)) index += 1
      }
      segments.unshift(index)
    }
    current = parent
  }
  return segments
}

function propertyValueNode(property: SyntaxNode): SyntaxNode | null {
  for (let child = property.lastChild; child; child = child.prevSibling) {
    if (isJsonValueNode(child)) return child
  }
  return null
}

function objectPropertyValue(state: EditorState, objectNode: SyntaxNode, key: string): SyntaxNode | null {
  for (let child = objectNode.firstChild; child; child = child.nextSibling) {
    if (child.name !== 'Property') continue
    const nameNode = child.firstChild
    if (nameNode?.name === 'PropertyName' && stringContent(state, nameNode) === key) {
      return propertyValueNode(child)
    }
  }
  return null
}

/**
 * The object node holding the script document. The code pane renders either
 * a bare script or the {"script": {...}} envelope used by the API — unwrap
 * the envelope when present.
 */
function scriptRootNode(state: EditorState): SyntaxNode | null {
  if (state.doc.length > APEX_DSL_SEMANTIC_MAX_DOC_CHARS) return null
  // The state's immutable tree can remain partial after its parse context has
  // advanced. Admit only the completed tree returned for this exact document,
  // within a bounded synchronous assist budget; never resolve recovery ranges
  // from the older state snapshot while background parsing catches up.
  const tree = ensureSyntaxTree(state, state.doc.length, 50)
  const root = tree?.topNode.firstChild
  if (!root || root.name !== 'Object') return null
  const wrapped = objectPropertyValue(state, root, 'script')
  if (wrapped?.name === 'Object') return wrapped
  return root
}

/** The script's protocol, read from the (possibly enveloped) script object. */
export function documentProtocol(state: EditorState): string {
  const root = scriptRootNode(state)
  if (!root) return ''
  const value = objectPropertyValue(state, root, 'protocol')
  if (!value || value.name !== 'String') return ''
  return stringContent(state, value)
}

/**
 * Resolve a validator path ("actions[0].control.steps[1]", "config.jms")
 * to the editor range of the value at that path, or null.
 */
export function locateJsonPath(state: EditorState, path: string): { from: number; to: number } | null {
  const segments: JsonPathSegment[] = []
  const matcher = /([^.[\]]+)|\[(\d+)\]/g
  for (let match = matcher.exec(path); match; match = matcher.exec(path)) {
    if (match[2] !== undefined) {
      segments.push(Number(match[2]))
    } else {
      segments.push(match[1])
    }
  }
  let node = scriptRootNode(state)
  for (const segment of segments) {
    if (!node) return null
    if (typeof segment === 'number') {
      if (node.name !== 'Array') return null
      let child: SyntaxNode | null = node.firstChild
      while (child && !isJsonValueNode(child)) child = child.nextSibling
      for (let index = 0; child && index < segment; index += 1) {
        child = child.nextSibling
        while (child && !isJsonValueNode(child)) child = child.nextSibling
      }
      node = child
    } else {
      if (node.name !== 'Object') return null
      node = objectPropertyValue(state, node, segment)
    }
  }
  if (!node || node.name === '⚠' || node.to <= node.from) return null
  return { from: node.from, to: node.to }
}

interface ActionContext {
  objectNode: SyntaxNode
  actionType: string
  inParams: boolean
}

/**
 * If `node` sits inside an action object (an element of actions/init/
 * teardown or a nested control steps/else/case/default array), return that object and
 * its declared type. Detects whether the cursor is inside the action's
 * params object.
 */
function enclosingActionContext(state: EditorState, node: SyntaxNode): ActionContext | null {
  let inParams = false
  for (let current: SyntaxNode | null = node; current; current = current.parent) {
    if (current.name !== 'Object') continue
    const path = jsonPathForNode(state, current)
    if (path.length >= 2 && typeof path[path.length - 1] === 'number' && ACTION_LIST_KEYS.has(String(path[path.length - 2]))) {
      const typeNode = objectPropertyValue(state, current, 'type')
      return {
        objectNode: current,
        actionType: typeNode && typeNode.name === 'String' ? stringContent(state, typeNode) : '',
        inParams,
      }
    }
    if (path.length >= 1 && path[path.length - 1] === 'params') {
      inParams = true
    }
  }
  return null
}

function fieldCompletion(field: DslCatalogField, stripParamsPrefix: boolean, asKey: boolean): Completion | null {
  let label = field.name
  if (stripParamsPrefix) {
    if (!label.startsWith('params.')) return null
    label = label.slice('params.'.length)
  } else if (label.includes('.')) {
    return null
  }
  return {
    label,
    type: 'property',
    detail: field.required ? `${field.kind}, required` : field.kind,
    info: field.doc,
    apply: asKey ? `"${label}": ` : label,
    boost: field.required ? 2 : 0,
  }
}

function actionFieldCompletions(context: ActionContext, asKey: boolean): Completion[] {
  const catalog = getCachedDslActionCatalog()
  if (!catalog) return []
  const spec = context.actionType ? dslSpecForActionType(context.actionType, catalog) : null
  const options: Completion[] = []
  const seen = new Set<string>()
  const push = (completion: Completion | null) => {
    if (!completion || seen.has(completion.label)) return
    seen.add(completion.label)
    options.push(completion)
  }
  if (context.inParams) {
    spec?.fields?.forEach(field => push(fieldCompletion(field, true, asKey)))
    return options
  }
  spec?.fields?.forEach(field => push(fieldCompletion(field, false, asKey)))
  if (spec?.fields?.some(field => field.name.startsWith('params.'))) {
    push({ label: 'params', type: 'property', detail: 'object', info: 'Type-specific parameters.', apply: asKey ? '"params": ' : 'params' })
  }
  catalog.common_fields.forEach(field => push(fieldCompletion(field, false, asKey)))
  return options
}

function actionTypeCompletions(state: EditorState): Completion[] {
  const catalog = getCachedDslActionCatalog()
  if (!catalog) return []
  const protocol = documentProtocol(state)
  const specs = protocol ? dslActionTypesForProtocol(protocol, catalog) : catalog.actions
  return specs.map(spec => ({
    label: spec.type,
    type: 'keyword',
    detail: spec.protocols.includes('*') ? 'engine' : spec.protocols.join(', '),
    info: spec.summary,
    boost: spec.protocols.includes('*') ? -1 : 1,
  }))
}

function enumCompletions(context: ActionContext, state: EditorState, key: string): Completion[] {
  const catalog = getCachedDslActionCatalog()
  if (!catalog || !context.actionType) return []
  const spec = dslSpecForActionType(context.actionType, catalog)
  const fieldName = context.inParams ? `params.${key}` : key
  const field = spec?.fields?.find(candidate => candidate.name === fieldName)
  if (!field?.enum?.length) return []
  return field.enum.map(value => ({ label: value, type: 'constant', info: field.doc }))
}

/** Completion source for the script JSON editor. Exported for tests. */
export function dslCompletionSource(context: CompletionContext): CompletionResult | null {
  const state = context.state
  const node = syntaxTree(state).resolveInner(context.pos, -1)

  // Value position inside a string: "type": "..." / enum fields / root protocol.
  if (node.name === 'String' && node.parent?.name === 'Property') {
    const nameNode = node.parent.firstChild
    const valueNode = propertyValueNode(node.parent)
    if (nameNode?.name === 'PropertyName' && valueNode?.from === node.from && valueNode.to === node.to) {
      const key = stringContent(state, nameNode)
      let options: Completion[] = []
      const actionContext = enclosingActionContext(state, node)
      if (key === 'type' && actionContext && !actionContext.inParams) {
        options = actionTypeCompletions(state)
      } else if (key === 'protocol' && jsonPathForNode(state, node).filter(segment => segment !== 'script').length === 1) {
        const catalog = getCachedDslActionCatalog()
        options = (catalog?.protocols ?? []).map(value => ({ label: value, type: 'constant' }))
      } else if (actionContext) {
        options = enumCompletions(actionContext, state, key)
      }
      if (!options.length) return null
      return { from: node.from + 1, to: node.to > node.from + 1 && state.sliceDoc(node.to - 1, node.to) === '"' ? node.to - 1 : context.pos, options }
    }
  }

  // Key position inside an action (or its params) object.
  if (node.name === 'PropertyName') {
    const actionContext = enclosingActionContext(state, node)
    if (!actionContext) return null
    const options = actionFieldCompletions(actionContext, false)
    if (!options.length) return null
    return { from: node.from + 1, to: node.to > node.from + 1 && state.sliceDoc(node.to - 1, node.to) === '"' ? node.to - 1 : context.pos, options }
  }

  // Bare key position (right after "{" or ","): offer quoted key inserts.
  if (node.name === 'Object' || node.name === '⚠') {
    const objectNode = node.name === 'Object' ? node : node.parent
    if (!objectNode || objectNode.name !== 'Object') return null
    const match = context.matchBefore(/[\w.]*$/)
    if (!match || (match.from === match.to && !context.explicit)) return null
    const actionContext = enclosingActionContext(state, objectNode)
    if (!actionContext) return null
    const options = actionFieldCompletions(actionContext, true)
    if (!options.length) return null
    return { from: match.from, options }
  }

  return null
}

// --- Template reference hover ({{ns.name}} resolution) ---------------------
//
// On hover over a {{...}} token, resolve the namespace and name against the
// script's own parameters/data_sources/config so authors immediately see
// whether a reference is defined — the most common authoring bug is pointing
// at a parameter or data column that does not exist.

const TEMPLATE_TOKEN = /\{\{\s*([^{}]+?)\s*\}\}/g
const KNOWN_NAMESPACES = new Set(['env', 'data', 'var', 'param', 'secret', 'fn', 'fake'])

export interface ScriptReferenceModel {
  parameters: Array<{ name?: string; type?: string }>
  dataSources: Array<{ name?: string; type?: string; columns?: string[] }>
  envKeys: Set<string>
}

const EMPTY_REFERENCE_MODEL: ScriptReferenceModel = { parameters: [], dataSources: [], envKeys: new Set() }

// Memoize the parsed model per immutable document. CodeMirror's `Text` is replaced
// on every edit, so a WeakMap keyed on it returns the cached parse for repeated
// hovers over an unchanged document and lets edited documents be garbage-collected.
const referenceModelCache = new WeakMap<object, ScriptReferenceModel>()

/** Read the parameter/data-source/env surface from the document JSON. */
export function readScriptReferenceModel(state: EditorState): ScriptReferenceModel {
  const doc = state.doc as unknown as object
  const cached = referenceModelCache.get(doc)
  if (cached) return cached
  // Skip the full-document JSON.parse on very large imports — the hover assist is
  // optional and not worth a multi-MB parse. The semantic-highlight path caps at
  // the same size (APEX_DSL_SEMANTIC_MAX_DOC_CHARS).
  if (state.doc.length > APEX_DSL_SEMANTIC_MAX_DOC_CHARS) return EMPTY_REFERENCE_MODEL
  let parsed: unknown
  try {
    parsed = JSON.parse(state.doc.toString())
  } catch {
    return EMPTY_REFERENCE_MODEL
  }
  if (parsed && typeof parsed === 'object' && 'script' in parsed && typeof (parsed as { script?: unknown }).script === 'object') {
    parsed = (parsed as { script?: unknown }).script
  }
  if (!parsed || typeof parsed !== 'object') return EMPTY_REFERENCE_MODEL
  const record = parsed as Record<string, unknown>
  const config = (record.config && typeof record.config === 'object') ? record.config as Record<string, unknown> : {}
  const envKeys = new Set<string>()
  for (const source of [config.environment, config.variables, config.env]) {
    if (source && typeof source === 'object') Object.keys(source as Record<string, unknown>).forEach(key => envKeys.add(key))
  }
  const model: ScriptReferenceModel = {
    parameters: Array.isArray(record.parameters) ? record.parameters as ScriptReferenceModel['parameters'] : [],
    dataSources: Array.isArray(record.data_sources) ? record.data_sources as ScriptReferenceModel['dataSources'] : [],
    envKeys,
  }
  referenceModelCache.set(doc, model)
  return model
}

export interface TemplateReferenceInfo {
  title: string
  detail: string
  /** known = resolved in-script; unknown = a real authoring gap; info = runtime-resolved. */
  status: 'known' | 'unknown' | 'info'
  /** The concrete value this reference resolved to in the latest debug run, when known. */
  runtimeValue?: string
  runtimeQualification?: string
}

/**
 * Describe a `{{...}}` inner expression against the script reference model.
 * When `runtimeValues` (a name->value map from a completed/paused debug run) is
 * supplied, the concrete resolved value is attached. Exported for tests.
 */
export function describeTemplateReference(
  expr: string,
  model: ScriptReferenceModel,
  runtimeValues?: Record<string, string> | null,
  runtimeQualification?: string,
): TemplateReferenceInfo {
  const trimmed = expr.trim()
  const dot = trimmed.indexOf('.')
  const namespace = (dot === -1 ? trimmed : trimmed.slice(0, dot)).trim()
  const rest = dot === -1 ? '' : trimmed.slice(dot + 1).trim()
  const info = describeBase(namespace, rest, trimmed, model)
  // Secrets are masked; never surface a concrete value for them.
  if (runtimeValues && namespace !== 'secret' && ['var', 'param', 'env', 'data'].includes(namespace)) {
    const key = Object.prototype.hasOwnProperty.call(runtimeValues, trimmed) ? trimmed : namespace === 'var' ? rest : null
    if (key !== null && Object.prototype.hasOwnProperty.call(runtimeValues, key)) {
      info.runtimeValue = runtimeValues[key]
      info.runtimeQualification = runtimeQualification
      if (info.status === 'info') info.status = 'known'
    }
  }
  return info
}

function describeBase(namespace: string, rest: string, trimmed: string, model: ScriptReferenceModel): TemplateReferenceInfo {
  switch (namespace) {
    case 'param': {
      const match = model.parameters.find(parameter => parameter.name === rest)
      if (match) {
        return { title: `param.${rest}`, detail: `Parameter — defined${match.type ? ` (type: ${match.type})` : ''}.`, status: 'known' }
      }
      return { title: `param.${rest}`, detail: 'Parameter is not defined in this script. Add it to the Parameter List.', status: 'unknown' }
    }
    case 'data': {
      const segments = rest.split('.')
      const sourceName = segments[0]
      const column = segments.slice(1).join('.')
      const source = model.dataSources.find(dataSource => dataSource.name === sourceName)
      if (!source) {
        return { title: `data.${rest}`, detail: `Data source "${sourceName}" is not defined in this script.`, status: 'unknown' }
      }
      if (column && Array.isArray(source.columns) && source.columns.length > 0 && !source.columns.includes(column)) {
        return { title: `data.${rest}`, detail: `Column "${column}" is not in data source "${sourceName}" (${source.columns.join(', ')}).`, status: 'unknown' }
      }
      return { title: `data.${rest}`, detail: `Data binding — source "${sourceName}"${source.type ? ` (${source.type})` : ''}${column ? `, column "${column}"` : ''}.`, status: 'known' }
    }
    case 'var':
      return { title: `var.${rest}`, detail: 'Runtime variable — set by an extractor, capture, or session variable during the run.', status: 'info' }
    case 'secret':
      return { title: `secret.${rest}`, detail: 'Secret reference — resolved from the secret store at run time (value masked).', status: 'info' }
    case 'env':
      return {
        title: `env.${rest}`,
        detail: model.envKeys.has(rest)
          ? 'Environment variable — a default is set in this script’s config.'
          : 'Environment variable — resolved at run time from the selected environment.',
        status: 'info',
      }
    case 'fn':
      return { title: `fn.${rest}`, detail: 'Built-in function — evaluated each time it is used.', status: 'info' }
    case 'fake':
      return { title: `fake.${rest}`, detail: 'Synthetic data generator — produces a fake value on each use.', status: 'info' }
    default:
      if (KNOWN_NAMESPACES.has(namespace)) {
        return { title: trimmed, detail: 'Template reference.', status: 'info' }
      }
      return { title: trimmed, detail: `Unknown namespace "${namespace}". Expected env, data, var, param, secret, fn, or fake.`, status: 'unknown' }
  }
}

/** Locate the `{{...}}` token spanning `pos` on its line, if any. Exported for tests. */
export function templateTokenAt(state: EditorState, pos: number): { from: number; to: number; expr: string } | null {
  const line = state.doc.lineAt(pos)
  const relative = pos - line.from
  TEMPLATE_TOKEN.lastIndex = 0
  for (let match = TEMPLATE_TOKEN.exec(line.text); match; match = TEMPLATE_TOKEN.exec(line.text)) {
    const start = match.index
    const end = match.index + match[0].length
    if (relative < start || relative > end) continue
    return { from: line.from + start, to: line.from + end, expr: match[1] }
  }
  return null
}

/** Source of resolved runtime values for hover, read live so debug-run updates show. */
export type RuntimeValueSource = () => Record<string, string> | null | undefined

function truncateValue(value: string, max = 160): string {
  return value.length > max ? `${value.slice(0, max)}…` : value
}

function makeTemplateReferenceHover(getRuntimeValues: RuntimeValueSource, getQualification: () => string): Extension {
  return hoverTooltip((view, pos): Tooltip | null => {
    const token = templateTokenAt(view.state, pos)
    if (!token) return null
    const info = describeTemplateReference(token.expr, readScriptReferenceModel(view.state), getRuntimeValues(), getQualification())
    return {
      pos: token.from,
      end: token.to,
      above: true,
      create() {
        const dom = document.createElement('div')
        dom.className = `apex-template-hover apex-template-hover-${info.status}`
        const title = document.createElement('div')
        title.className = 'apex-template-hover-title'
        title.textContent = `{{${info.title}}}`
        const detail = document.createElement('div')
        detail.className = 'apex-template-hover-detail'
        detail.textContent = info.detail
        dom.append(title, detail)
        if (info.runtimeValue !== undefined) {
          const resolved = document.createElement('div')
          resolved.className = 'apex-template-hover-resolved'
          resolved.textContent = `= ${truncateValue(info.runtimeValue) || '(empty)'}`
          dom.append(resolved)
          const qualifier = document.createElement('div')
          qualifier.textContent = info.runtimeQualification || 'Retained runtime evidence'
          dom.append(qualifier)
        }
        return { dom }
      },
    }
  })
}

export interface DslValidationOutcome {
  issues?: string[] | null
  warnings?: unknown
}

function warningMessages(warnings: unknown): string[] {
  if (!Array.isArray(warnings)) return []
  return warnings
    .map(entry => {
      if (typeof entry === 'string') return entry
      if (entry && typeof entry === 'object' && typeof (entry as { message?: unknown }).message === 'string') {
        return (entry as { message: string }).message
      }
      return ''
    })
    .filter(Boolean)
}

const ISSUE_PATH_PATTERN = /^((?:config|goal_config|actions|init|teardown|parameters|data_sources)(?:\[\d+\])?(?:\.[A-Za-z_]\w*(?:\[\d+\])?)*)/

/** Map validator issue strings to editor diagnostics. Exported for tests. */
export function diagnosticsFromIssues(state: EditorState, issues: string[], severity: 'error' | 'warning'): Diagnostic[] {
  return issues.map(issue => {
    const match = ISSUE_PATH_PATTERN.exec(issue)
    const range = match ? locateJsonPath(state, match[1]) : null
    return {
      from: range?.from ?? 0,
      to: range?.to ?? 0,
      severity,
      source: 'apex-dsl',
      message: issue,
    }
  })
}

/**
 * Build the assist extensions: completion, `{{...}}` reference hover, JSON
 * syntax lint, and debounced server-side draft validation through
 * `validateDraft`. `getRuntimeValues` is read live on hover so resolved values
 * from a debug run appear without rebuilding the editor.
 */
export function buildDslAssistExtensions(
  validateDraft: (script: unknown) => Promise<DslValidationOutcome>,
  getRuntimeValues: RuntimeValueSource = () => null,
  getRuntimeQualification: () => string = () => 'Retained runtime evidence',
): Extension[] {
  const validationLinter = linter(
    async view => {
      let script: unknown
      try {
        script = JSON.parse(view.state.doc.toString())
      } catch {
        return [] // syntax issues are reported by jsonParseLinter
      }
      if (!script || typeof script !== 'object') return []
      // The code pane may render the {"script": {...}} envelope — unwrap it.
      const envelope = script as { script?: unknown }
      if (envelope.script && typeof envelope.script === 'object') {
        script = envelope.script
      }
      try {
        const outcome = await validateDraft(script)
        return [
          ...diagnosticsFromIssues(view.state, outcome.issues ?? [], 'error'),
          ...diagnosticsFromIssues(view.state, warningMessages(outcome.warnings), 'warning'),
        ]
      } catch {
        return [] // offline or auth failure: stay quiet, do not block editing
      }
    },
    { delay: 800 },
  )
  return [
    autocompletion({ override: [dslCompletionSource] }),
    makeTemplateReferenceHover(getRuntimeValues, getRuntimeQualification),
    linter(jsonParseLinter()),
    validationLinter,
  ]
}
