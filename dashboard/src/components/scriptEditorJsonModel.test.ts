import { describe, expect, it } from 'vitest'

import type { ScriptDefinition, ScriptActionDefinition } from '../types/scripts'
import type { ParameterCandidate } from './scriptEditorState'
import {
  SCRIPT_CODE_LINE_WRAP_MAX_CHARS,
  authorFocusFromKey,
  buildJsonParameterDraft,
  buildScriptOutlineSummary,
  clearUnexportedHarBodyBlockerOnAction,
  codePathForAuthorFocus,
  codePathForParameterCandidateOccurrence,
  deriveScriptJsonDraftState,
  findCandidateLiteralRangeInJson,
  findParameterReferencesInJson,
  formatScriptJson,
  formatScriptJsonDraft,
  formatParameterCandidateOccurrence,
  jsonPathForActionStepPath,
  offsetForLine,
  parameterReferenceLines,
  parseScriptJsonDraft,
  remapBreakpointsAfterNestedRemoval,
  removeNestedAction,
  resolveAuthorJsonScope,
  selectionStateForJsonRequest,
  shouldWrapScriptJsonLines,
  stepPathForJsonSubPath,
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

  it('maps a JSON path below an action back to the step path the outline gives it', () => {
    const leaf = (name: string): ScriptActionDefinition => ({ name, type: 'GET', target: `/${name}` })
    const loop: ScriptActionDefinition = { name: 'Poll', type: 'control_loop', target: '', control: { steps: [leaf('tick')] } }
    const sequence: ScriptActionDefinition = { name: 'Checkout', type: 'http_sequence', target: '', params: { requests: [leaf('first'), leaf('second')] } }
    const root: ScriptActionDefinition = {
      name: 'Route', type: 'control_if', target: '',
      control: { steps: [sequence, loop], else: [leaf('fallback')] },
    }
    const cases: Array<[ScriptActionDefinition, string, string[]]> = [
      [sequence, '.params.requests[1]', ['requests[1]']],
      [root, '.control.steps[0].params.requests[0]', ['then[0]', 'requests[0]']],
      [root, '.control.steps[1].control.steps[0]', ['then[1]', 'loop[0]']],
      [root, '.control.else[0]', ['else[0]']],
      [{ name: 'Route', type: 'control_switch', target: '', control: { cases: [{ condition: { expr: 'x' }, steps: [leaf('a')] }], default: [leaf('b')] } }, '.control.cases[0].steps[0]', ['case[0][0]']],
      [{ name: 'Route', type: 'control_switch', target: '', control: { default: [leaf('b')] } }, '.control.default[0]', ['default[0]']],
      [{ name: 'Fan out', type: 'control_parallel', target: '', control: { branches: [{ name: 'one', steps: [leaf('a')] }] } }, '.control.branches[0].steps[0]', ['branches[0].steps[0]']],
    ]
    for (const [action, subPath, stepPath] of cases) {
      expect({ subPath, stepPath: stepPathForJsonSubPath(action, subPath) }).toEqual({ subPath, stepPath })
      // The inverse of the path the outline's step path names.
      expect(jsonPathForActionStepPath(stepPath)).toBe(subPath)
      expect(resolveAuthorJsonScope(script({ actions: [action] }), { phase: 'action', actionIndex: 0, stepPath })?.subAction).toBe(true)
    }
    // A path that names no step, or nothing below the action, has no step path.
    expect(stepPathForJsonSubPath(sequence, '.params.requests[5]')).toBeUndefined()
    expect(stepPathForJsonSubPath(sequence, '.header_list[0]')).toBeUndefined()
    expect(stepPathForJsonSubPath(sequence, '')).toBeUndefined()
    expect(stepPathForJsonSubPath(undefined, '.params.requests[0]')).toBeUndefined()
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

describe('guided nested action updates', () => {
  it.each(['loop[0]', 'then[0]', 'else[0]', 'default[0]', 'case[0][0]', 'branches[0].steps[0]', 'requests[0]'])('preserves siblings and vendor fields when editing %s', async path => {
    const { updateNestedAction } = await import('./scriptEditorJsonModel')
    const leaf = { name: 'Check', type: 'browser_wait', target: '#result', metadata: { vendor: 'preserved' }, assertions: [{ type: 'element_text_contains', value: '#result', expected: 'Before' }] }
    const independentLeaf = () => structuredClone(leaf)
    const root: ScriptActionDefinition = { name: 'Container', type: 'control_loop', target: '', control: { steps: [independentLeaf()], else: [independentLeaf()], default: [independentLeaf()], cases: [{ condition: { left: 'x', op: 'eq', right: 'x' }, steps: [independentLeaf()] }], branches: [{ name: 'one', steps: [independentLeaf()] }] }, params: { requests: [independentLeaf()] } }
    const updated = updateNestedAction(root, [path], action => ({ ...action, assertions: [{ ...action.assertions![0], expected: 'After' }] }))
    const scope = resolveAuthorJsonScope(script({ init: [updated] }), { phase: 'init', actionIndex: 0, stepPath: [path] })
    expect(scope?.action.assertions?.[0].expected).toBe('After')
    expect(scope?.action.metadata).toEqual({ vendor: 'preserved' })
    expect(leaf.assertions[0].expected).toBe('Before')
    expect(updateNestedAction(root, ['bad[0]'], () => leaf)).toBe(root)
  })
})

describe('removeNestedAction', () => {
  const step = (name: string) => ({ name, type: 'GET', target: `/${name}` }) as ScriptActionDefinition
  const parallel = (branches: ScriptActionDefinition[][]) => ({
    name: 'Fan out', type: 'control_parallel',
    control: { branches: branches.map((steps, index) => ({ name: `B${index}`, steps })) },
  }) as unknown as ScriptActionDefinition

  it('unwraps a parallel block left with one multi-step branch into that branch\'s steps', () => {
    const root = parallel([[step('blocked')], [step('s2'), step('s3')]])
    const removal = removeNestedAction(root, ['branches[0].steps[0]'])!
    expect(removal.replacement.map(action => action.name)).toEqual(['s2', 's3'])
    expect(removal.relocate(['branches[1].steps[1]'])).toEqual({ index: 1, stepPath: [] })
    expect(removal.relocate(['branches[0].steps[0]'])).toBeNull()
    expect(removal.relocate([])).toBeNull()
  })

  it('removes a nested sequence or if block a removal empties, at any depth', () => {
    const sequence = { name: 'Seq', type: 'http_sequence', params: { requests: [step('only')] } } as unknown as ScriptActionDefinition
    const root = { name: 'If', type: 'control_if', control: { condition: 'x', steps: [sequence, step('after')] } } as unknown as ScriptActionDefinition
    const removal = removeNestedAction(root, ['then[0]', 'requests[0]'])!
    expect(removal.replacement).toHaveLength(1)
    expect(removal.replacement[0].control?.steps?.map(action => action.name)).toEqual(['after'])
    expect(removal.relocate(['then[1]'])).toEqual({ index: 0, stepPath: ['then[0]'] })
    expect(removal.relocate(['then[0]'])).toBeNull()

    const innerIf = { name: 'Inner', type: 'control_if', control: { condition: 'y', steps: [step('lone')] } } as unknown as ScriptActionDefinition
    const outer = { name: 'Loop', type: 'control_loop', control: { count: 2, steps: [innerIf, step('keep')] } } as unknown as ScriptActionDefinition
    const nested = removeNestedAction(outer, ['loop[0]', 'then[0]'])!
    expect(nested.replacement[0].control?.steps?.map(action => action.name)).toEqual(['keep'])
    // Emptying the only step of a top-level sequence removes the whole action.
    expect(removeNestedAction(sequence, ['requests[0]'])!.replacement).toEqual([])
  })

  it('keeps a parallel block of two or more branches and shifts later branches', () => {
    const root = parallel([[step('a')], [step('b')], [step('c'), step('d')]])
    const removal = removeNestedAction(root, ['branches[0].steps[0]'])!
    expect(removal.replacement[0].control?.branches?.map(branch => branch.steps?.map(action => action.name))).toEqual([['b'], ['c', 'd']])
    expect(removal.relocate(['branches[2].steps[1]'])).toEqual({ index: 0, stepPath: ['branches[1].steps[1]'] })
    expect(removeNestedAction(root, ['branches[9].steps[0]'])).toBeNull()
  })

  it('moves breakpoints with the steps they were set on', () => {
    const sequence = { name: 'Seq', type: 'http_sequence', params: { requests: [step('r0'), step('r1'), step('r2'), step('r3'), step('r4')] } } as unknown as ScriptActionDefinition
    const removal = removeNestedAction(sequence, ['requests[1]'])!
    const next = remapBreakpointsAfterNestedRemoval([
      { phase: 'action', action_index: 2, request_index: 4 },
      { phase: 'action', action_index: 2, request_index: 1 },
      { phase: 'action', action_index: 2, request_index: 0, disabled: true },
      { phase: 'action', action_index: 2 },
      { phase: 'init', action_index: 2, request_index: 4 },
    ], 'action', 2, removal)
    expect(next).toEqual([
      { phase: 'action', action_index: 2, request_index: 3 },
      { phase: 'action', action_index: 2, request_index: 0, disabled: true },
      { phase: 'action', action_index: 2 },
      { phase: 'init', action_index: 2, request_index: 4 },
    ])

    const unwrapped = removeNestedAction(parallel([[step('x')], [step('s2'), { name: 'Seq', type: 'http_sequence', params: { requests: [step('q0'), step('q1')] } } as unknown as ScriptActionDefinition]]), ['branches[0].steps[0]'])!
    expect(remapBreakpointsAfterNestedRemoval([
      { phase: 'action', action_index: 0, step_path: ['branches[1].steps[1]', 'requests[1]'] },
      { phase: 'action', action_index: 0 },
      { phase: 'action', action_index: 1 },
    ], 'action', 0, unwrapped)).toEqual([
      { phase: 'action', action_index: 1, request_index: 1 },
      { phase: 'action', action_index: 2 },
    ])
  })
})

describe('clearUnexportedHarBodyBlockerOnAction', () => {
  it('drops the body reason from import_review.reason_codes and its re-capture resolution', () => {
    const action = {
      name: 'Beacon', type: 'POST', target: '/aura',
      metadata: {
        execution_blockers: ['har_request_body_not_exported', 'import_review_required'],
        import_review: { version: 1, source: 'har', reason_codes: ['har_request_body_not_exported', 'http_header_omitted'], source_refs: ['entry-5'], resolution: 'recapture_body' },
      },
    } as unknown as ScriptActionDefinition
    const cleared = clearUnexportedHarBodyBlockerOnAction(action)
    expect(cleared.metadata?.execution_blockers).toEqual(['import_review_required'])
    expect(cleared.metadata?.import_review).toEqual({ version: 1, source: 'har', reason_codes: ['http_header_omitted'], source_refs: ['entry-5'], resolution: 'replace_behavior_and_remove_gate' })
  })

  it('removes the review when the body was its only reason', () => {
    const action = {
      name: 'Beacon', type: 'POST', target: '/aura',
      metadata: { execution_blockers: ['har_request_body_not_exported'], review_required: true, import_review: { version: 1, source: 'har', reason_codes: ['har_request_body_not_exported'], source_refs: ['entry-5'], resolution: 'recapture_body' } },
    } as unknown as ScriptActionDefinition
    expect(clearUnexportedHarBodyBlockerOnAction(action).metadata).toEqual({})
  })
})
