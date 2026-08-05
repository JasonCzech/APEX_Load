import { jsonLanguage } from '@codemirror/lang-json'
import { type Extension, StateEffect, StateField } from '@codemirror/state'
import { Decoration, type DecorationSet, EditorView, ViewPlugin } from '@codemirror/view'

export type ApexDslSemanticKind =
  | 'sectionKey'
  | 'sectionBlock'
  | 'actionBlock'
  | 'actionName'
  | 'actionMethod'
  | 'actionTarget'
  | 'parameterBlock'
  | 'parameterName'
  | 'parameterReference'
  | 'dataSourceBlock'
  | 'dataSourceName'
  | 'extractorBlock'
  | 'assertionBlock'
  | 'supportingKey'
  | 'configKey'
  | 'transactionMetadata'

export interface ApexDslSemanticRange {
  from: number
  to: number
  kind: ApexDslSemanticKind
  path: string
}

interface JsonNodeRecord {
  name: string
  from: number
  to: number
  children: JsonNodeRecord[]
}

interface ParsedJsonTree {
  root: JsonNodeRecord
  hasError: boolean
}

interface JsonPropertyRecord {
  key: string
  path: string
  node: JsonNodeRecord
  keyNode: JsonNodeRecord
  valueNode: JsonNodeRecord
}

const VALUE_NODE_NAMES = new Set(['Object', 'Array', 'String', 'Number', 'True', 'False', 'Null'])
const SECTION_KEYS = new Set(['config', 'parameters', 'data_sources', 'init', 'actions', 'teardown'])
const ACTION_SECTION_KEYS = new Set(['init', 'actions', 'teardown'])
const ACTION_PRIMARY_KEYS = new Set(['name', 'type', 'method', 'target'])
const SUPPORTING_ACTION_KEYS = new Set([
  'headers',
  'body',
  'body_encoding',
  'params',
  'metadata',
  'wait_for',
  'capture',
  'soap',
  'think_time_after',
])
const TRANSACTION_METADATA_KEYS = new Set([
  'source_transaction',
  'source_subtransaction',
  'source_transaction',
  'source_subtransaction',
  'transaction',
  'transaction_name',
])
const BLOCK_KINDS = new Set<ApexDslSemanticKind>([
  'sectionBlock',
  'actionBlock',
  'parameterBlock',
  'dataSourceBlock',
  'extractorBlock',
  'assertionBlock',
])

function parseJsonTree(text: string): ParsedJsonTree {
  const tree = jsonLanguage.parser.parse(text)
  const cursor = tree.cursor()
  let hasError = false

  const readNode = (): JsonNodeRecord => {
    if (cursor.type.isError) {
      hasError = true
    }
    const node: JsonNodeRecord = {
      name: cursor.name,
      from: cursor.from,
      to: cursor.to,
      children: [],
    }

    if (cursor.firstChild()) {
      do {
        node.children.push(readNode())
      } while (cursor.nextSibling())
      cursor.parent()
    }

    return node
  }

  return { root: readNode(), hasError }
}

function valueChild(node: JsonNodeRecord): JsonNodeRecord | null {
  return node.children.find(child => VALUE_NODE_NAMES.has(child.name)) || null
}

function decodeJsonPropertyName(text: string, node: JsonNodeRecord): string {
  try {
    return JSON.parse(text.slice(node.from, node.to)) as string
  } catch {
    return ''
  }
}

function propertiesForObject(text: string, objectNode: JsonNodeRecord, parentPath: string): JsonPropertyRecord[] {
  if (objectNode.name !== 'Object') return []

  return objectNode.children.flatMap(child => {
    if (child.name !== 'Property') return []
    const keyNode = child.children.find(grandchild => grandchild.name === 'PropertyName')
    const valueNode = valueChild(child)
    if (!keyNode || !valueNode) return []
    const key = decodeJsonPropertyName(text, keyNode)
    if (!key) return []
    return [{
      key,
      path: parentPath ? `${parentPath}.${key}` : key,
      node: child,
      keyNode,
      valueNode,
    }]
  })
}

function objectElementsForArray(arrayNode: JsonNodeRecord): JsonNodeRecord[] {
  if (arrayNode.name !== 'Array') return []
  return arrayNode.children.filter(child => child.name === 'Object')
}

function firstObjectNode(root: JsonNodeRecord): JsonNodeRecord | null {
  if (root.name === 'Object') return root
  return root.children.find(child => child.name === 'Object') || null
}

function scriptObjectNode(text: string, root: JsonNodeRecord): JsonNodeRecord | null {
  const rootObject = firstObjectNode(root)
  if (!rootObject) return null

  const scriptProperty = propertiesForObject(text, rootObject, '')
    .find(property => property.key === 'script' && property.valueNode.name === 'Object')
  return scriptProperty?.valueNode || rootObject
}

function addRange(ranges: ApexDslSemanticRange[], kind: ApexDslSemanticKind, node: JsonNodeRecord, path: string) {
  if (node.to <= node.from) return
  ranges.push({ from: node.from, to: node.to, kind, path })
}

function visitNodes(node: JsonNodeRecord, visit: (node: JsonNodeRecord) => void) {
  visit(node)
  node.children.forEach(child => visitNodes(child, visit))
}

function collectParameterReferences(text: string, scriptNode: JsonNodeRecord, ranges: ApexDslSemanticRange[]) {
  visitNodes(scriptNode, node => {
    if (node.name !== 'String') return
    const rawInner = text.slice(node.from + 1, node.to - 1)
    const pattern = /\{\{\s*param\.[A-Za-z_$][\w.$-]*\s*\}\}/g
    let match = pattern.exec(rawInner)
    while (match) {
      ranges.push({
        from: node.from + 1 + match.index,
        to: node.from + 1 + match.index + match[0].length,
        kind: 'parameterReference',
        path: 'script:parameter-reference',
      })
      match = pattern.exec(rawInner)
    }
  })
}

function collectConfigProperties(text: string, objectNode: JsonNodeRecord, path: string, ranges: ApexDslSemanticRange[], depth = 0) {
  if (depth > 2) return
  propertiesForObject(text, objectNode, path).forEach(property => {
    addRange(ranges, 'configKey', property.keyNode, property.path)
    if (property.valueNode.name === 'Object') {
      collectConfigProperties(text, property.valueNode, property.path, ranges, depth + 1)
    }
  })
}

function collectMetadataTransactions(text: string, objectNode: JsonNodeRecord, path: string, ranges: ApexDslSemanticRange[]) {
  propertiesForObject(text, objectNode, path).forEach(property => {
    if (TRANSACTION_METADATA_KEYS.has(property.key)) {
      addRange(ranges, 'transactionMetadata', property.keyNode, property.path)
      addRange(ranges, 'transactionMetadata', property.valueNode, property.path)
    }
    if (property.valueNode.name === 'Object') {
      collectMetadataTransactions(text, property.valueNode, property.path, ranges)
    }
  })
}

function collectAction(text: string, objectNode: JsonNodeRecord, path: string, ranges: ApexDslSemanticRange[]) {
  addRange(ranges, 'actionBlock', objectNode, path)

  propertiesForObject(text, objectNode, path).forEach(property => {
    if (property.key === 'name') {
      addRange(ranges, 'actionName', property.keyNode, property.path)
      addRange(ranges, 'actionName', property.valueNode, property.path)
      return
    }
    if (property.key === 'type' || property.key === 'method') {
      addRange(ranges, 'actionMethod', property.keyNode, property.path)
      addRange(ranges, 'actionMethod', property.valueNode, property.path)
      return
    }
    if (property.key === 'target') {
      addRange(ranges, 'actionTarget', property.keyNode, property.path)
      addRange(ranges, 'actionTarget', property.valueNode, property.path)
      return
    }
    if (property.key === 'extractors' && property.valueNode.name === 'Array') {
      addRange(ranges, 'supportingKey', property.keyNode, property.path)
      objectElementsForArray(property.valueNode).forEach((extractor, index) => {
        const extractorPath = `${property.path}[${index}]`
        addRange(ranges, 'extractorBlock', extractor, extractorPath)
        propertiesForObject(text, extractor, extractorPath).forEach(extractorProperty => {
          if (extractorProperty.key === 'name' || extractorProperty.key === 'type' || extractorProperty.key === 'source') {
            addRange(ranges, 'supportingKey', extractorProperty.keyNode, extractorProperty.path)
            addRange(ranges, 'supportingKey', extractorProperty.valueNode, extractorProperty.path)
          }
        })
      })
      return
    }
    if (property.key === 'assertions' && property.valueNode.name === 'Array') {
      addRange(ranges, 'supportingKey', property.keyNode, property.path)
      objectElementsForArray(property.valueNode).forEach((assertion, index) => {
        const assertionPath = `${property.path}[${index}]`
        addRange(ranges, 'assertionBlock', assertion, assertionPath)
        propertiesForObject(text, assertion, assertionPath).forEach(assertionProperty => {
          if (assertionProperty.key === 'type' || assertionProperty.key === 'expected' || assertionProperty.key === 'value') {
            addRange(ranges, 'supportingKey', assertionProperty.keyNode, assertionProperty.path)
            addRange(ranges, 'supportingKey', assertionProperty.valueNode, assertionProperty.path)
          }
        })
      })
      return
    }
    if (property.key === 'metadata' && property.valueNode.name === 'Object') {
      addRange(ranges, 'supportingKey', property.keyNode, property.path)
      collectMetadataTransactions(text, property.valueNode, property.path, ranges)
      return
    }
    if (ACTION_PRIMARY_KEYS.has(property.key) || SUPPORTING_ACTION_KEYS.has(property.key)) {
      addRange(ranges, 'supportingKey', property.keyNode, property.path)
    }
  })
}

function collectParameter(text: string, objectNode: JsonNodeRecord, path: string, ranges: ApexDslSemanticRange[]) {
  addRange(ranges, 'parameterBlock', objectNode, path)

  propertiesForObject(text, objectNode, path).forEach(property => {
    if (property.key === 'name') {
      addRange(ranges, 'parameterName', property.keyNode, property.path)
      addRange(ranges, 'parameterName', property.valueNode, property.path)
      return
    }
    if (property.key === 'type' || property.key === 'values' || property.key === 'file' || property.key === 'column') {
      addRange(ranges, 'supportingKey', property.keyNode, property.path)
    }
  })
}

function collectDataSource(text: string, objectNode: JsonNodeRecord, path: string, ranges: ApexDslSemanticRange[]) {
  addRange(ranges, 'dataSourceBlock', objectNode, path)

  propertiesForObject(text, objectNode, path).forEach(property => {
    if (property.key === 'name') {
      addRange(ranges, 'dataSourceName', property.keyNode, property.path)
      addRange(ranges, 'dataSourceName', property.valueNode, property.path)
      return
    }
    if (property.key === 'type' || property.key === 'path' || property.key === 'strategy' || property.key === 'columns') {
      addRange(ranges, 'supportingKey', property.keyNode, property.path)
    }
  })
}

function uniqueSortedRanges(ranges: ApexDslSemanticRange[]): ApexDslSemanticRange[] {
  const seen = new Set<string>()
  return ranges
    .filter(range => range.to > range.from)
    .sort((left, right) => left.from - right.from || left.to - right.to || left.kind.localeCompare(right.kind))
    .filter(range => {
      const key = `${range.from}:${range.to}:${range.kind}:${range.path}`
      if (seen.has(key)) return false
      seen.add(key)
      return true
    })
}

export function collectApexDslSemanticRanges(text: string): ApexDslSemanticRange[] {
  if (!text.trim()) return []

  let parsed: ParsedJsonTree
  try {
    parsed = parseJsonTree(text)
  } catch {
    return []
  }
  if (parsed.hasError) return []

  const scriptNode = scriptObjectNode(text, parsed.root)
  if (!scriptNode) return []

  const ranges: ApexDslSemanticRange[] = []
  collectParameterReferences(text, scriptNode, ranges)

  propertiesForObject(text, scriptNode, 'script').forEach(property => {
    if (SECTION_KEYS.has(property.key)) {
      addRange(ranges, 'sectionKey', property.keyNode, property.path)
      if (property.valueNode.name === 'Array' || property.valueNode.name === 'Object') {
        addRange(ranges, 'sectionBlock', property.valueNode, property.path)
      }
    }

    if (ACTION_SECTION_KEYS.has(property.key) && property.valueNode.name === 'Array') {
      objectElementsForArray(property.valueNode).forEach((action, index) => {
        collectAction(text, action, `${property.path}[${index}]`, ranges)
      })
      return
    }
    if (property.key === 'parameters' && property.valueNode.name === 'Array') {
      objectElementsForArray(property.valueNode).forEach((parameter, index) => {
        collectParameter(text, parameter, `${property.path}[${index}]`, ranges)
      })
      return
    }
    if (property.key === 'data_sources' && property.valueNode.name === 'Array') {
      objectElementsForArray(property.valueNode).forEach((dataSource, index) => {
        collectDataSource(text, dataSource, `${property.path}[${index}]`, ranges)
      })
      return
    }
    if (property.key === 'config' && property.valueNode.name === 'Object') {
      collectConfigProperties(text, property.valueNode, property.path, ranges)
    }
  })

  return uniqueSortedRanges(ranges)
}

function classNameForKind(kind: ApexDslSemanticKind) {
  return `apex-dsl-${kind.replace(/[A-Z]/g, match => `-${match.toLowerCase()}`)}`
}

/**
 * Whole-document semantic highlighting parses the entire JSON through the Lezer
 * parser and materializes a decoration per matched range (and one line decoration
 * per line of every block). For very large imported scripts (e.g. a multi-thousand
 * action LoadRunner archive whose pretty-printed JSON runs to tens of thousands of
 * lines) that synchronous parse + decoration build blocks the main thread for
 * seconds on editor mount. Above this size we skip semantic highlighting entirely;
 * CodeMirror's built-in JSON syntax highlighting still applies.
 */
export const APEX_DSL_SEMANTIC_MAX_DOC_CHARS = 200_000
export const APEX_DSL_SEMANTIC_REBUILD_DELAY_MS = 120

export function buildApexDslSemanticDecorations(state: EditorView['state']): DecorationSet {
  if (state.doc.length > APEX_DSL_SEMANTIC_MAX_DOC_CHARS) return Decoration.none
  const ranges = collectApexDslSemanticRanges(state.doc.toString())
  if (ranges.length === 0) return Decoration.none

  const decorations = ranges.flatMap(range => {
    if (BLOCK_KINDS.has(range.kind)) {
      const lineClass = `apex-dsl-line ${classNameForKind(range.kind)}`
      const lineDecoration = Decoration.line({ class: lineClass })
      const startLine = state.doc.lineAt(range.from)
      const endLine = state.doc.lineAt(Math.max(range.from, range.to - 1))
      const entries: Array<{ from: number; to: number; decoration: Decoration }> = []
      for (let lineNo = startLine.number; lineNo <= endLine.number; lineNo += 1) {
        const line = state.doc.line(lineNo)
        entries.push({ from: line.from, to: line.from, decoration: lineDecoration })
      }
      return entries
    }

    return [{
      from: range.from,
      to: range.to,
      decoration: Decoration.mark({ class: `apex-dsl-token ${classNameForKind(range.kind)}` }),
    }]
  })

  return Decoration.set(
    decorations.map(entry => entry.decoration.range(entry.from, entry.to)),
    true,
  )
}

const setApexDslSemanticDecorations = StateEffect.define<DecorationSet>()

const apexDslSemanticHighlightPlugin = ViewPlugin.fromClass(class {
  private rebuildTimer: ReturnType<typeof globalThis.setTimeout> | null = null

  constructor(private readonly view: EditorView) {}

  update(update: { docChanged: boolean }) {
    if (!update.docChanged) return
    if (this.rebuildTimer !== null) globalThis.clearTimeout(this.rebuildTimer)
    this.rebuildTimer = globalThis.setTimeout(() => {
      this.rebuildTimer = null
      this.view.dispatch({
        effects: setApexDslSemanticDecorations.of(buildApexDslSemanticDecorations(this.view.state)),
      })
    }, APEX_DSL_SEMANTIC_REBUILD_DELAY_MS)
  }

  destroy() {
    if (this.rebuildTimer !== null) globalThis.clearTimeout(this.rebuildTimer)
  }
})

const apexDslSemanticHighlightField = StateField.define<DecorationSet>({
  create: state => buildApexDslSemanticDecorations(state),
  update(decorations, tr) {
    for (const effect of tr.effects) {
      if (effect.is(setApexDslSemanticDecorations)) {
        return effect.value
      }
    }
    return decorations.map(tr.changes)
  },
  provide: field => [
    EditorView.decorations.from(field),
    apexDslSemanticHighlightPlugin,
  ],
})

export function apexDslSemanticHighlighting(): Extension {
  return apexDslSemanticHighlightField
}
