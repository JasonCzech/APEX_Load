import { afterEach, describe, expect, it } from 'vitest'

import { CompletionContext } from '@codemirror/autocomplete'
import { json } from '@codemirror/lang-json'
import { ensureSyntaxTree } from '@codemirror/language'
import { EditorState } from '@codemirror/state'

import { setCachedDslActionCatalog, type DslActionCatalog } from './dslActionCatalog'
import {
  describeTemplateReference,
  diagnosticsFromIssues,
  documentProtocol,
  dslCompletionSource,
  locateJsonPath,
  readScriptReferenceModel,
  templateTokenAt,
} from './dslEditorAssist'

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

	it('recognizes actions nested in a control_switch default branch', () => {
		setCachedDslActionCatalog(testCatalog)
		const source = `{
			"name":"switch", "protocol":"browser", "actions":[{
				"name":"route", "type":"control_switch", "control":{
					"cases":[{"condition":{"expr":"true"},"steps":[{"name":"case","type":"browser_cl"}]}],
					"default":[{"name":"fallback","type":"browser_cl"}]
				}
			}]
		}`
		const result = completionAt(source, '"name":"fallback","type":"browser_cl')
		expect(result?.options.map(option => option.label)).toContain('browser_click')
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

const scriptWithRefs = `{
  "name": "demo",
  "protocol": "http",
  "config": { "environment": { "BASE_URL": "https://x" } },
  "parameters": [ { "name": "username", "type": "list" } ],
  "data_sources": [ { "name": "users", "type": "csv", "columns": ["email", "id"] } ],
  "actions": [
    { "name": "login", "type": "GET", "target": "/u/{{param.username}}?e={{data.users.email}}" }
  ]
}`

describe('readScriptReferenceModel', () => {
  it('reads parameters, data sources, and env keys (incl. through the envelope)', () => {
    const model = readScriptReferenceModel(stateFor(scriptWithRefs))
    expect(model.parameters.map(parameter => parameter.name)).toContain('username')
    expect(model.dataSources.map(source => source.name)).toContain('users')
    expect(model.envKeys.has('BASE_URL')).toBe(true)

    const enveloped = `{ "script": ${scriptWithRefs} }`
    expect(readScriptReferenceModel(stateFor(enveloped)).parameters.map(parameter => parameter.name)).toContain('username')
  })

  it('skips the full-document parse for very large imports', () => {
    // Pad a valid script past the size cap so the optional hover assist does not
    // run a multi-MB JSON.parse on every hover. Leading whitespace stays valid
    // JSON, so an empty result proves the cap short-circuited (not a parse error).
    const big = `${' '.repeat(200_001)}${scriptWithRefs}`
    const model = readScriptReferenceModel(stateFor(big))
    expect(model.parameters).toEqual([])
    expect(model.dataSources).toEqual([])
    expect(model.envKeys.size).toBe(0)
  })
})

describe('describeTemplateReference', () => {
  const model = readScriptReferenceModel(stateFor(scriptWithRefs))

  it('marks a defined parameter as known and an undefined one as unknown', () => {
    expect(describeTemplateReference('param.username', model).status).toBe('known')
    const missing = describeTemplateReference('param.nope', model)
    expect(missing.status).toBe('unknown')
    expect(missing.detail).toMatch(/not defined/i)
  })

  it('resolves data bindings and flags a missing column', () => {
    expect(describeTemplateReference('data.users.email', model).status).toBe('known')
    expect(describeTemplateReference('data.users.missing', model).status).toBe('unknown')
    expect(describeTemplateReference('data.absent.email', model).status).toBe('unknown')
  })

  it('treats runtime/env/secret/fn references as informational, not errors', () => {
    expect(describeTemplateReference('var.csrf', model).status).toBe('info')
    expect(describeTemplateReference('secret.token', model).status).toBe('info')
    expect(describeTemplateReference('env.BASE_URL', model).status).toBe('info')
    expect(describeTemplateReference('fn.uuid()', model).status).toBe('info')
  })

  it('flags an unknown namespace', () => {
    expect(describeTemplateReference('bogus.thing', model).status).toBe('unknown')
  })

  it('attaches a concrete runtime value when one is known, but never for secrets', () => {
    const runtime = { username: 'alice', csrf: 'tok-1', PASSWORD: 'p@ss' }
    const param = describeTemplateReference('param.username', model, runtime)
    expect(param.runtimeValue).toBe('alice')

    const variable = describeTemplateReference('var.csrf', model, runtime)
    expect(variable.runtimeValue).toBe('tok-1')
    expect(variable.status).toBe('known') // promoted from info once resolved

    expect(describeTemplateReference('secret.PASSWORD', model, runtime).runtimeValue).toBeUndefined()
    expect(describeTemplateReference('var.unset', model, runtime).runtimeValue).toBeUndefined()
  })
})

describe('templateTokenAt', () => {
  it('finds the {{...}} token spanning a position and ignores positions outside one', () => {
    const state = stateFor(scriptWithRefs)
    const doc = state.doc.toString()
    const inside = doc.indexOf('param.username') + 2
    const token = templateTokenAt(state, inside)
    expect(token?.expr.trim()).toBe('param.username')

    const outside = doc.indexOf('"protocol"') + 1
    expect(templateTokenAt(state, outside)).toBeNull()
  })
})
