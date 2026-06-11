import { afterEach, describe, expect, it } from 'vitest'

import { CompletionContext } from '@codemirror/autocomplete'
import { json } from '@codemirror/lang-json'
import { ensureSyntaxTree } from '@codemirror/language'
import { EditorState } from '@codemirror/state'

import { setCachedDslActionCatalog, type DslActionCatalog } from './dslActionCatalog'
import { diagnosticsFromIssues, documentProtocol, dslCompletionSource, locateJsonPath } from './dslEditorAssist'

const testCatalog: DslActionCatalog = {
  protocols: ['browser', 'http', 'websocket'],
  common_fields: [
    { name: 'name', required: true, kind: 'string', doc: 'Step name.' },
    { name: 'type', required: true, kind: 'string' },
    { name: 'assertions', kind: 'array' },
  ],
  actions: [
    {
      type: 'browser_click',
      protocols: ['browser'],
      summary: 'Clicks an element.',
      fields: [
        { name: 'selector', kind: 'string', doc: 'Element locator.' },
        { name: 'sync', kind: 'object' },
      ],
    },
    {
      type: 'browser_swipe',
      protocols: ['browser'],
      summary: 'Swipes from an element.',
      fields: [
        { name: 'selector', kind: 'string' },
        { name: 'params.direction', kind: 'enum', enum: ['left', 'right', 'up', 'down'] },
        { name: 'params.distance', kind: 'int' },
      ],
    },
    {
      type: 'ws_connect',
      protocols: ['websocket'],
      summary: 'Opens the connection.',
      fields: [{ name: 'target', required: true, kind: 'string' }],
    },
    {
      type: 'control_if',
      protocols: ['*'],
      summary: 'Conditional block.',
      fields: [{ name: 'control', required: true, kind: 'object' }],
    },
  ],
}

const browserScript = `{
  "name": "demo",
  "protocol": "browser",
  "config": { "base_url": "" },
  "actions": [
    {
      "name": "open",
      "type": "browser_navigate",
      "target": "https://example.com"
    },
    {
      "name": "tap",
      "type": "browser_click",
      "selector": "#go"
    },
    {
      "name": "swipe",
      "type": "browser_swipe",
      "selector": "#list",
      "params": { "direction": "left" }
    }
  ]
}`

function stateFor(doc: string): EditorState {
  const state = EditorState.create({ doc, extensions: [json()] })
  ensureSyntaxTree(state, doc.length, 10_000)
  return state
}

afterEach(() => setCachedDslActionCatalog(null))

const envelopedScript = `{\n  "script": ${browserScript.replace(/\n/g, '\n  ')}\n}`

describe('documentProtocol', () => {
  it('reads the root protocol property', () => {
    expect(documentProtocol(stateFor(browserScript))).toBe('browser')
    expect(documentProtocol(stateFor('{"name":"x"}'))).toBe('')
  })

  it('unwraps the {"script": ...} envelope', () => {
    expect(documentProtocol(stateFor(envelopedScript))).toBe('browser')
  })
})

describe('locateJsonPath', () => {
  it('resolves action indices and nested config paths', () => {
    const state = stateFor(browserScript)
    const action1 = locateJsonPath(state, 'actions[1]')
    expect(action1).not.toBeNull()
    expect(state.sliceDoc(action1!.from, action1!.to)).toContain('"browser_click"')

    const baseUrl = locateJsonPath(state, 'config.base_url')
    expect(baseUrl).not.toBeNull()

    expect(locateJsonPath(state, 'actions[9]')).toBeNull()
    expect(locateJsonPath(state, 'config.missing.deep')).toBeNull()
  })

  it('resolves paths through the {"script": ...} envelope', () => {
    const state = stateFor(envelopedScript)
    const action1 = locateJsonPath(state, 'actions[1]')
    expect(action1).not.toBeNull()
    expect(state.sliceDoc(action1!.from, action1!.to)).toContain('"browser_click"')
  })
})

describe('diagnosticsFromIssues', () => {
  it('maps issue path prefixes to ranges and leaves unmatched issues at the doc start', () => {
    const state = stateFor(browserScript)
    const [located, unmatched] = diagnosticsFromIssues(
      state,
      ['actions[1] "tap": missing target or selector', 'script name is required'],
      'error',
    )
    expect(located.from).toBeGreaterThan(0)
    expect(state.sliceDoc(located.from, located.to)).toContain('"tap"')
    expect(located.severity).toBe('error')
    expect(unmatched.from).toBe(0)
    expect(unmatched.to).toBe(0)
  })
})

function completionAt(doc: string, cursor: string): ReturnType<typeof dslCompletionSource> {
  const pos = doc.indexOf(cursor)
  if (pos < 0) throw new Error(`cursor marker ${cursor} not found`)
  const state = stateFor(doc)
  return dslCompletionSource(new CompletionContext(state, pos + cursor.length, true))
}

describe('dslCompletionSource', () => {
  it('completes action types for the script protocol inside "type" values', () => {
    setCachedDslActionCatalog(testCatalog)
    const result = completionAt(browserScript, '"type": "browser_cl')
    expect(result).not.toBeNull()
    const labels = result!.options.map(option => option.label)
    expect(labels).toContain('browser_click')
    expect(labels).toContain('control_if')
    expect(labels).not.toContain('ws_connect')
  })

  it('completes per-type fields and common fields in key position', () => {
    setCachedDslActionCatalog(testCatalog)
    const result = completionAt(browserScript, '"selector": "#go"'.slice(0, 4))
    expect(result).not.toBeNull()
    const labels = result!.options.map(option => option.label)
    expect(labels).toContain('selector')
    expect(labels).toContain('sync')
    expect(labels).toContain('assertions')
  })

  it('completes enum values for params fields', () => {
    setCachedDslActionCatalog(testCatalog)
    const result = completionAt(browserScript, '"direction": "left')
    expect(result).not.toBeNull()
    const labels = result!.options.map(option => option.label)
    expect(labels).toEqual(expect.arrayContaining(['left', 'right', 'up', 'down']))
  })

  it('returns null without a catalog', () => {
    expect(completionAt(browserScript, '"type": "browser_cl')).toBeNull()
  })
})
