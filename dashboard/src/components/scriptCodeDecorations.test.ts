// @vitest-environment jsdom

import { describe, expect, it } from 'vitest'
import { EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'

import {
  resolvedValueForToken,
  scriptCodeResolvedValueField,
  setScriptCodeResolvedValues,
} from './scriptCodeDecorations'

describe('resolvedValueForToken', () => {
  const values = { id: '4471', username: 'alice', BASE: 'https://x' }

  it('resolves var/param/env references by bare name', () => {
    expect(resolvedValueForToken('var.id', values)).toBe('4471')
    expect(resolvedValueForToken('param.username', values)).toBe('alice')
    expect(resolvedValueForToken('env.BASE', values)).toBe('https://x')
  })

  it('returns undefined for secrets, functions, and unknown names', () => {
    expect(resolvedValueForToken('secret.id', values)).toBeUndefined()
    expect(resolvedValueForToken('fn.uuid()', values)).toBeUndefined()
    expect(resolvedValueForToken('var.missing', values)).toBeUndefined()
  })
})

describe('scriptCodeResolvedValueField', () => {
  function decorationCount(doc: string, payloadRangeEnd: number, values: Record<string, string>) {
    const view = new EditorView({
      state: EditorState.create({ doc, extensions: [scriptCodeResolvedValueField] }),
    })
    view.dispatch({ effects: setScriptCodeResolvedValues.of({ values, range: { startLine: 1, endLine: payloadRangeEnd } }) })
    let count = 0
    const set = view.state.field(scriptCodeResolvedValueField)
    const cursor = set.iter()
    while (cursor.value) { count += 1; cursor.next() }
    view.destroy()
    return count
  }

  it('adds one widget per resolvable token inside the range', () => {
    const doc = '{\n  "target": "/orders/{{var.id}}?u={{param.username}}",\n  "x": "{{secret.tok}}"\n}'
    // line 2 has two resolvable tokens; the secret on line 3 is excluded.
    expect(decorationCount(doc, 2, { id: '4471', username: 'alice' })).toBe(2)
  })

  it('produces no decorations without values or range', () => {
    const doc = '{ "t": "{{var.id}}" }'
    const view = new EditorView({ state: EditorState.create({ doc, extensions: [scriptCodeResolvedValueField] }) })
    view.dispatch({ effects: setScriptCodeResolvedValues.of(null) })
    expect(view.state.field(scriptCodeResolvedValueField).size).toBe(0)
    view.destroy()
  })
})
