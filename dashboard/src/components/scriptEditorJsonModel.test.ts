import { describe, expect, it } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import type { ParameterCandidate } from './scriptEditorState'
import {
  SCRIPT_CODE_LINE_WRAP_MAX_CHARS,
  authorFocusFromKey,
  buildJsonParameterDraft,
  buildScriptOutlineSummary,
  codePathForAuthorFocus,
  codePathForParameterCandidateOccurrence,
  deriveScriptJsonDraftState,
  findCandidateLiteralRangeInJson,
  findParameterReferencesInJson,
  formatScriptJson,
  formatScriptJsonDraft,
  formatParameterCandidateOccurrence,
  offsetForLine,
  parameterReferenceLines,
  parseScriptJsonDraft,
  resolveAuthorJsonScope,
  selectionStateForJsonRequest,
  shouldWrapScriptJsonLines,
  synchronizeStructuredScriptJson,
  updateScriptJsonText,
} from './scriptEditorJsonModel'

function script(overrides: Partial<ScriptDefinition> = {}): ScriptDefinition {
  return {
    name: 'json-model',
    version: '1.0.0',
    protocol: 'http',
    config: {
      base_url: 'https://example.test',
      think_time: { type: 'fixed', mean_ms: 0, ignore: true },
      pacing: { type: 'none' },
      run_logic: { auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
    },
    actions: [],
    ...overrides,
  }
}

describe('Script JSON draft model', () => {
  it('parses wrapped and bare drafts and formats an applicable canonical document', () => {
    const wrapped = formatScriptJson(script({ name: 'wrapped' }))
    expect(parseScriptJsonDraft(wrapped)).toMatchObject({ ok: true, script: { name: 'wrapped' } })
    expect(parseScriptJsonDraft(JSON.stringify(script({ name: 'bare' })))).toMatchObject({
      ok: true,
      script: { name: 'bare' },
    })

    const formatted = formatScriptJsonDraft(
      JSON.stringify({ script: script({ name: 'applied' }) }),
      script(),
    )
    expect(formatted).toMatchObject({ ok: true, dirty: true, script: { name: 'applied' } })
    if (formatted.ok) expect(formatted.text).toBe(formatScriptJson(formatted.script))

    expect(parseScriptJsonDraft('  ')).toEqual({ ok: false, message: 'Script JSON is empty.' })
    expect(parseScriptJsonDraft('{bad').ok).toBe(false)
  })

  it('keeps structured and JSON state synchronized without overwriting an active raw edit', () => {
    const structured = script({ name: 'structured' })
    const stale = formatScriptJson(script({ name: 'stale' }))

    expect(synchronizeStructuredScriptJson(structured, stale, false)).toEqual({
      changed: true,
      text: formatScriptJson(structured),
    })
    expect(synchronizeStructuredScriptJson(structured, stale, true)).toEqual({
      changed: false,
      text: stale,
    })
    expect(updateScriptJsonText(formatScriptJson(structured), structured).dirty).toBe(false)
    expect(updateScriptJsonText(stale, structured).dirty).toBe(true)
  })

  it('derives synced, dirty, and invalid status while retaining structured fallback state', () => {
    const structured = script({ name: 'structured' })
    expect(deriveScriptJsonDraftState(formatScriptJson(structured), false, structured)).toMatchObject({
      status: 'synced',
      statusMessage: 'Synced',
      parseMessage: '',
      currentScript: { name: 'structured' },
    })
    expect(deriveScriptJsonDraftState(formatScriptJson(script({ name: 'draft' })), true, structured)).toMatchObject({
      status: 'dirty',
      statusMessage: 'Unsaved JSON edits',
      currentScript: { name: 'draft' },
    })
    expect(deriveScriptJsonDraftState('{bad', true, structured)).toMatchObject({
      status: 'invalid',
      statusMessage: 'Invalid JSON',
      currentScript: { name: 'structured' },
    })
    // Clean JSON is a projection of structured state. It is not reparsed on every
    // structured edit, even during the brief render before its text synchronizes.
    expect(deriveScriptJsonDraftState('{stale-invalid', false, structured)).toMatchObject({
      status: 'synced',
      parseMessage: '',
      currentScript: { name: 'structured' },
      parseResult: { ok: true, script: { name: 'structured' } },
    })
  })
})

describe('Script JSON parameter selection', () => {
  const parameters = [{ name: 'session_id', type: 'list', values: ['seed-session'] }]

  it('turns literal and existing-token selections into parameterization drafts', () => {
    const literalSelection = selectionStateForJsonRequest({
      start: 20,
      end: 31,
      selectedText: 'Customer 42',
      x: 100,
      y: 200,
    }, parameters)
    expect(literalSelection).toMatchObject({ existingName: null, selectedText: 'Customer 42' })
    expect(buildJsonParameterDraft(literalSelection, parameters)).toMatchObject({
      source: 'json',
      actionIndex: -1,
      requestedName: 'customer_42',
      seedValue: 'Customer 42',
      start: 20,
      end: 31,
    })

    const tokenSelection = selectionStateForJsonRequest({
      start: 4,
      end: 24,
      selectedText: '{{ param.session_id }}',
      x: 0,
      y: 0,
    }, parameters)
    expect(tokenSelection?.existingName).toBe('session_id')
    expect(buildJsonParameterDraft(tokenSelection, parameters)).toMatchObject({
      existingName: 'session_id',
      requestedName: 'session_id',
      seedValue: 'seed-session',
    })
    expect(selectionStateForJsonRequest({ start: 0, end: 0, selectedText: ' ', x: 0, y: 0 }, parameters)).toBeNull()
  })
})

describe('Script JSON path and line navigation', () => {
  it('resolves nested author focus keys into exact JSON paths and scoped actions', () => {
    const sequence = script({
      actions: [{
        name: 'Checkout',
        type: 'http_sequence',
        target: '',
        params: {
          requests: [
            { name: 'First', type: 'GET', method: 'GET', target: '/one' },
            { name: 'Second', type: 'POST', method: 'POST', target: '/two' },
          ],
        },
      }],
    })
    const focus = authorFocusFromKey('action:0#requests[1]')
    expect(focus).toMatchObject({ phase: 'action', actionIndex: 0, stepPath: ['requests[1]'] })
    expect(codePathForAuthorFocus(focus)).toBe('script.actions[0].params.requests[1]')
    expect(resolveAuthorJsonScope(sequence, focus)).toMatchObject({
      label: 'Second',
      path: 'script.actions[0].params.requests[1]',
      subAction: true,
      action: { target: '/two' },
    })
  })

	it('resolves control_switch case and default focus paths', () => {
		const switchScript = script({ actions: [{
			name: 'Route', type: 'control_switch', target: '',
			control: {
				cases: [{ condition: { expr: 'var.kind == "a"' }, steps: [{ name: 'Case A', type: 'GET', target: '/a' }] }],
				default: [{ name: 'Fallback', type: 'GET', target: '/fallback' }],
			},
		}] })
		const caseFocus = authorFocusFromKey('action:0#case[0][0]')
		expect(codePathForAuthorFocus(caseFocus)).toBe('script.actions[0].control.cases[0].steps[0]')
		expect(resolveAuthorJsonScope(switchScript, caseFocus)).toMatchObject({ action: { name: 'Case A' } })
		const defaultFocus = authorFocusFromKey('action:0#default[0]')
		expect(codePathForAuthorFocus(defaultFocus)).toBe('script.actions[0].control.default[0]')
		expect(resolveAuthorJsonScope(switchScript, defaultFocus)).toMatchObject({ action: { name: 'Fallback' } })
	})

  it('maps parameter references and candidate literals to stable lines and offsets', () => {
    const text = [
      '{',
      '  "target": "{{param.user}}",',
      '  "body": "literal-value",',
      '  "again": "{{ param.user }}"',
      '}',
    ].join('\n')
    const references = findParameterReferencesInJson(text, 'user')
    expect(parameterReferenceLines(text, references)).toEqual([2, 4])

    const candidate: ParameterCandidate = {
      id: 'body',
      keyHint: 'body',
      value: 'literal-value',
      varName: 'body_value',
      selected: true,
      occurrences: [{ actionIdx: 0, field: 'body' }],
    }
    const ranges = findCandidateLiteralRangeInJson(text, { startLine: 3, endLine: 3 }, candidate, 0)
    expect(ranges).toHaveLength(1)
    expect(text.slice(ranges[0].from, ranges[0].to)).toBe('literal-value')
    expect(offsetForLine(text, 3)).toBe(text.indexOf('  "body"'))
  })

  it('targets an exact ordered-header occurrence in JSON authoring', () => {
    const occurrence = {
      actionIdx: 2,
      field: 'header' as const,
      headerKey: 'X-Trace',
      headerIndex: 1,
    }
    expect(codePathForParameterCandidateOccurrence(occurrence))
      .toBe('script.actions[2].header_list[1].value')
    expect(formatParameterCandidateOccurrence(occurrence))
      .toBe('Action 3 / header X-Trace (occurrence 2)')
  })
})

describe('large Script JSON documents', () => {
  it('disables wrapping at the virtualization threshold and resolves deep lines without parsing', () => {
    expect(shouldWrapScriptJsonLines(SCRIPT_CODE_LINE_WRAP_MAX_CHARS)).toBe(true)
    expect(shouldWrapScriptJsonLines(SCRIPT_CODE_LINE_WRAP_MAX_CHARS + 1)).toBe(false)

    const largeDocument = 'x\n'.repeat(130_000)
    expect(largeDocument.length).toBeGreaterThan(SCRIPT_CODE_LINE_WRAP_MAX_CHARS)
    expect(shouldWrapScriptJsonLines(largeDocument)).toBe(false)
    expect(offsetForLine(largeDocument, 100_001)).toBe(200_000)
  })

  it('builds outline metrics without serializing the full document view', () => {
    const actions = Array.from({ length: 1_000 }, (_, index) => ({
      name: `Action ${index + 1}`,
      type: 'GET',
      method: 'GET',
      target: `/items/{{param.item_${index}}}`,
    }))
    const summary = buildScriptOutlineSummary(script({ actions }))
    expect(summary.actionCount).toBe(1_000)
    expect(summary.parameterRefCount).toBe(1_000)
  })
})
