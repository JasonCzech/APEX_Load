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
import { syntaxTree } from '@codemirror/language'
import { linter, type Diagnostic } from '@codemirror/lint'
import type { EditorState, Extension } from '@codemirror/state'
import type { SyntaxNode } from '@lezer/common'

import {
  dslActionTypesForProtocol,
  dslSpecForActionType,
  getCachedDslActionCatalog,
  type DslCatalogField,
} from './dslActionCatalog'

type JsonPathSegment = string | number

const ACTION_LIST_KEYS = new Set(['actions', 'init', 'teardown', 'steps', 'else'])

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
  const root = syntaxTree(state).topNode.firstChild
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
  if (!node) return null
  return { from: node.from, to: node.to }
}

interface ActionContext {
  objectNode: SyntaxNode
  actionType: string
  inParams: boolean
}

/**
 * If `node` sits inside an action object (an element of actions/init/
 * teardown or a nested control steps/else array), return that object and
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
 * Build the assist extensions: completion, JSON syntax lint, and debounced
 * server-side draft validation through `validateDraft`.
 */
export function buildDslAssistExtensions(
  validateDraft: (script: unknown) => Promise<DslValidationOutcome>,
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
    linter(jsonParseLinter()),
    validationLinter,
  ]
}
