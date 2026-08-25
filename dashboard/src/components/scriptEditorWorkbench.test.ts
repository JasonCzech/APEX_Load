// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from 'vitest'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import type { ProtocolEvidence } from '../types'
import {
  ACTION_LIST_TAB_ID,
  BROWSER_DEVICE_PROFILES,
  SCRIPT_JSON_TAB_ID,
  appendActionStep,
  buildScriptPhaseActionRows,
  buildDebugRailRenderRows,
  debugSequenceKey,
  clampDebuggerPaneSize,
  codePathForDebugRow,
  resolveDebugRowForEvent,
  actionTransactionMarker,
  actionTransactionName,
  computeTransactionBands,
  defaultCenterTabsState,
  duplicateActionInList,
  filterScriptPhaseActionRows,
  findParameterReferencesInJson,
  moveActionStep,
  remapIndexAfterMarkerWrap,
  removeActionStep,
  setActionTransactionName,
  tagActionsTransaction,
  uniqueActionName,
  uniqueTransactionName,
  updateActionStepField,
  wrapActionsInTransactionMarkers,
  formatBrowserDeviceProfileLabel,
  formatDebugPhaseLabel,
  formatProtocolEvidenceSummary,
  formatScriptJson,
  isCenterWorkbenchTabId,
  lookupBrowserDeviceProfile,
  parseScriptJsonDraft,
  readCenterTabsState,
  scriptActionsForPhase,
} from './scriptEditorWorkbench'

const action = (name: string): ScriptActionDefinition => ({ name } as unknown as ScriptActionDefinition)

const script = (over: Partial<ScriptDefinition> = {}): ScriptDefinition =>
  ({ name: 'demo', actions: [], ...over } as unknown as ScriptDefinition)

describe('JSON draft round-trip', () => {
  it('formats a script under a top-level script key', () => {
    const text = formatScriptJson(script({ name: 'order-flow' }))
    expect(text).toContain('"script"')
    expect(JSON.parse(text).script.name).toBe('order-flow')
  })

  it('parses a wrapped {script} draft', () => {
    const text = formatScriptJson(script({ name: 'wrapped' }))
    const res = parseScriptJsonDraft(text)
    expect(res.ok).toBe(true)
    if (res.ok) expect(res.script.name).toBe('wrapped')
  })

  it('parses a bare script object', () => {
    const res = parseScriptJsonDraft(JSON.stringify({ name: 'bare', actions: [] }))
    expect(res.ok).toBe(true)
    if (res.ok) expect(res.script.name).toBe('bare')
  })

  it('rejects empty and invalid JSON', () => {
    expect(parseScriptJsonDraft('   ')).toEqual({ ok: false, message: 'Script JSON is empty.' })
    const bad = parseScriptJsonDraft('{ not json')
    expect(bad.ok).toBe(false)
    if (!bad.ok) expect(bad.message).toMatch(/invalid/i)
  })

  // R1 (make-or-break): the parse <-> serialize round-trip must preserve
  // unknown / advanced / params.* keys so editing never silently corrupts an
  // imported script.
  it('preserves unknown, advanced and params.* keys through a round-trip', () => {
    const original = {
      name: 'import',
      protocol: 'http',
      actions: [
        {
          name: 'login',
          type: 'POST',
          target: '/login',
          params: { lr_unmask: 'abc', max_messages: 5 },
          vendor_extension: { keep: true },
        },
      ],
      lr_runtime_settings: { unknown_block: [1, 2, 3] },
    } as unknown as ScriptDefinition
    const roundTripped = parseScriptJsonDraft(formatScriptJson(original))
    expect(roundTripped.ok).toBe(true)
    if (roundTripped.ok) expect(roundTripped.script).toEqual(original)
  })
})

describe('clampDebuggerPaneSize', () => {
  it.each([
    ['rail', 10, 180],
    ['rail', 9999, 560],
    ['vars', 400, 400],
    ['bottom', 130.6, 140],
  ] as const)('clamps %s %d to %d', (kind, value, expected) => {
    expect(clampDebuggerPaneSize(kind, value)).toBe(expected)
  })
})

describe('center tabs state', () => {
  beforeEach(() => window.localStorage.clear())

  it('isCenterWorkbenchTabId validates known ids', () => {
    expect(isCenterWorkbenchTabId('action-list')).toBe(true)
    expect(isCenterWorkbenchTabId('source')).toBe(true)
    expect(isCenterWorkbenchTabId('script-json')).toBe(false)
    expect(isCenterWorkbenchTabId('metadata-json')).toBe(true)
    expect(isCenterWorkbenchTabId('conversion-mapping')).toBe(false)
    expect(isCenterWorkbenchTabId('nope')).toBe(false)
    expect(isCenterWorkbenchTabId(42)).toBe(false)
  })

  it('returns default when storage empty', () => {
    expect(readCenterTabsState('k')).toEqual(defaultCenterTabsState())
  })

  it('always keeps action-list and source first while migrating legacy script-json storage', () => {
    window.localStorage.setItem('k', JSON.stringify({ tabs: ['configuration', 'script-json'], activeTabId: 'configuration' }))
    const state = readCenterTabsState('k')
    expect(state.tabs[0]).toBe(ACTION_LIST_TAB_ID)
    expect(state.tabs[1]).toBe(SCRIPT_JSON_TAB_ID)
    expect(state.tabs.filter(t => t === ACTION_LIST_TAB_ID)).toHaveLength(1)
    expect(state.tabs.filter(t => t === SCRIPT_JSON_TAB_ID)).toHaveLength(1)
    expect(state.tabs).toEqual([ACTION_LIST_TAB_ID, SCRIPT_JSON_TAB_ID, 'runtime-settings'])
    expect(state.activeTabId).toBe('runtime-settings')
  })

  it('falls back to action-list for an unknown active tab', () => {
    window.localStorage.setItem('k', JSON.stringify({ tabs: ['configuration'], activeTabId: 'bogus' }))
    expect(readCenterTabsState('k').activeTabId).toBe(ACTION_LIST_TAB_ID)
  })

  it('drops stale standalone conversion mapping tabs from storage', () => {
    window.localStorage.setItem('k', JSON.stringify({ tabs: ['conversion-mapping', 'imported-metadata'], activeTabId: 'conversion-mapping' }))
    expect(readCenterTabsState('k')).toEqual({
      tabs: [ACTION_LIST_TAB_ID, SCRIPT_JSON_TAB_ID, 'imported-metadata'],
      activeTabId: ACTION_LIST_TAB_ID,
    })
  })

  it('returns default on malformed JSON', () => {
    window.localStorage.setItem('k', '{not json')
    expect(readCenterTabsState('k')).toEqual(defaultCenterTabsState())
  })
})

describe('phase action rows', () => {
  it('orders init -> action -> teardown with running global index', () => {
    const s = script({ init: [action('i0')], actions: [action('a0'), action('a1')], teardown: [action('t0')] })
    const rows = buildScriptPhaseActionRows(s)
    expect(rows.map(r => [r.phase, r.actionIndex, r.globalIndex])).toEqual([
      ['init', 0, 0],
      ['action', 0, 1],
      ['action', 1, 2],
      ['teardown', 0, 3],
    ])
    expect(rows[0].phaseLabel).toBe('Init')
  })

  it('scriptActionsForPhase reads the right slice', () => {
    const s = script({ init: [action('i')], actions: [action('a')], teardown: [action('t')] })
    expect(scriptActionsForPhase(s, 'init').map(a => a.name)).toEqual(['i'])
    expect(scriptActionsForPhase(s, 'action').map(a => a.name)).toEqual(['a'])
    expect(scriptActionsForPhase(s, 'teardown').map(a => a.name)).toEqual(['t'])
  })
})

describe('buildDebugRailRenderRows', () => {
  const seq = {
    name: 'txn',
    type: 'http_sequence',
    params: {
      requests: [
        { name: 'r0', method: 'POST', target: '/0' },
        { name: 'r1', method: 'GET', target: '/1' },
      ],
    },
  } as unknown as ScriptActionDefinition
  const rows = buildScriptPhaseActionRows(script({ actions: [action('plain'), seq] }))

  it('passes plain actions through and collapses containers by default', () => {
    const rendered = buildDebugRailRenderRows(rows, new Set())
    expect(rendered.map(r => r.kind)).toEqual(['action', 'container'])
    const parent = rendered[1]
    expect(parent.kind === 'container' && parent.leafCount).toBe(2)
    expect(parent.kind === 'container' && parent.expanded).toBe(false)
  })

  it('emits leaf rows when the container key is expanded', () => {
    const rendered = buildDebugRailRenderRows(rows, new Set([debugSequenceKey('action', 1)]))
    expect(rendered.map(r => r.kind)).toEqual(['action', 'container', 'leaf', 'leaf'])
    const leaf = rendered[2]
    if (leaf.kind !== 'leaf') throw new Error('expected leaf')
    expect(leaf.requestIndex).toBe(0)
    expect(leaf.key).toBe('action:1:0')
    expect(leaf.stepPath).toEqual(['requests[0]'])
  })

  it('expands control_loop body steps with loop[i] step paths', () => {
    const loop = {
      name: 'Retry', type: 'control_loop',
      control: { count: 3, steps: [
        { name: 'b0', type: 'GET', target: '/b0' },
        { name: 'b1', type: 'GET', target: '/b1' },
      ] },
    } as unknown as ScriptActionDefinition
    const loopRows = buildScriptPhaseActionRows(script({ actions: [loop] }))
    const rendered = buildDebugRailRenderRows(loopRows, new Set([debugSequenceKey('action', 0)]))
    expect(rendered.map(r => r.kind)).toEqual(['container', 'leaf', 'leaf'])
    const first = rendered[1]
    if (first.kind !== 'leaf') throw new Error('expected leaf')
    expect(first.stepPath).toEqual(['loop[0]'])
    expect(first.requestIndex).toBeUndefined()
  })

  it('expands control_if then/else branches with branch step paths', () => {
    const branch = {
      name: 'Maybe', type: 'control_if',
      control: { condition: { op: 'eq', left: 'a', right: 'a' },
        steps: [{ name: 't0', type: 'GET', target: '/t0' }],
        else: [{ name: 'e0', type: 'GET', target: '/e0' }] },
    } as unknown as ScriptActionDefinition
    const ifRows = buildScriptPhaseActionRows(script({ actions: [branch] }))
    const rendered = buildDebugRailRenderRows(ifRows, new Set([debugSequenceKey('action', 0)]))
    const leaves = rendered.filter(r => r.kind === 'leaf')
    expect(leaves.map(l => (l as { stepPath: string[] }).stepPath[0])).toEqual(['then[0]', 'else[0]'])
  })

	it('expands control_switch case/default branches with engine step paths', () => {
		const branch = {
			name: 'Route', type: 'control_switch',
			control: {
				cases: [
					{ condition: { expr: 'var.kind == "a"' }, steps: [{ name: 'a0', type: 'GET', target: '/a0' }, { name: 'a1', type: 'GET', target: '/a1' }] },
					{ condition: { expr: 'var.kind == "b"' }, steps: [{ name: 'b0', type: 'GET', target: '/b0' }] },
				],
				default: [{ name: 'd0', type: 'GET', target: '/default' }],
			},
		} as unknown as ScriptActionDefinition
		const switchRows = buildScriptPhaseActionRows(script({ actions: [branch] }))
		const rendered = buildDebugRailRenderRows(switchRows, new Set([debugSequenceKey('action', 0)]))
		const leaves = rendered.filter(row => row.kind === 'leaf')
		expect(leaves.map(row => (row as { stepPath: string[] }).stepPath[0])).toEqual([
			'case[0][0]', 'case[0][1]', 'case[1][0]', 'default[0]',
		])
	})

	it('expands control_parallel branches with deterministic engine step paths', () => {
		const parallel = {
			name: 'Fan out', type: 'control_parallel',
			control: { branches: [
				{ name: 'catalog', steps: [{ name: 'list', type: 'GET', target: '/catalog' }] },
				{ name: 'account', steps: [{ name: 'profile', type: 'GET', target: '/profile' }] },
			] },
		} as unknown as ScriptActionDefinition
		const rows = buildScriptPhaseActionRows(script({ actions: [parallel] }))
		const rendered = buildDebugRailRenderRows(rows, new Set([debugSequenceKey('action', 0)]))
		const leaves = rendered.filter(row => row.kind === 'leaf')
		expect(leaves.map(row => (row as { stepPath: string[] }).stepPath[0])).toEqual([
			'branches[0].steps[0]', 'branches[1].steps[0]',
		])
	})
})

describe('misc helpers', () => {
  it('codePathForDebugRow', () => {
    expect(codePathForDebugRow({ phase: 'action', actionIndex: 2 })).toBe('script.actions[2]')
    expect(codePathForDebugRow({ phase: 'init', actionIndex: 0 })).toBe('script.init[0]')
    expect(codePathForDebugRow({ phase: 'teardown', actionIndex: 1 })).toBe('script.teardown[1]')
    expect(codePathForDebugRow(null)).toBe('')
  })

  it('resolveDebugRowForEvent maps by phase/action_index, not flat event position', () => {
    const seq = {
      name: 'Seq', type: 'http_sequence',
      params: { requests: [{ name: 'r0', method: 'GET', target: '/0' }, { name: 'r1', method: 'GET', target: '/1' }] },
    } as unknown as ScriptActionDefinition
    const rows = buildScriptPhaseActionRows(script({ actions: [action('plain'), seq] }))
    // The http_sequence's 2nd sub-request event (whose flat event index would be 2,
    // out of the 2-row range) still resolves to the seq's top-level row (index 1).
    const row = resolveDebugRowForEvent(rows, { phase: 'action', action_index: 1 })
    expect(row?.actionIndex).toBe(1)
    expect(codePathForDebugRow(row)).toBe('script.actions[1]')
    expect(resolveDebugRowForEvent(rows, { phase: 'action', action_index: 0 })?.actionIndex).toBe(0)
    expect(resolveDebugRowForEvent(rows, { phase: 'action', action_index: 9 })).toBeNull()
    expect(resolveDebugRowForEvent(rows, null)).toBeNull()
    expect(resolveDebugRowForEvent(rows, { phase: 'action', action_index: 'x' })).toBeNull()
  })

  it('formatDebugPhaseLabel title-cases underscores', () => {
    expect(formatDebugPhaseLabel('think_time')).toBe('Think Time')
    expect(formatDebugPhaseLabel('action')).toBe('Action')
  })

  it('formatProtocolEvidenceSummary joins present fields', () => {
    expect(formatProtocolEvidenceSummary(undefined)).toBe('')
    const ev = { protocol: 'http', operation: 'GET', status: '', target: 'https://x' } as ProtocolEvidence
    expect(formatProtocolEvidenceSummary(ev)).toBe('http / GET / https://x')
  })

  it('findParameterReferencesInJson finds {{param.x}} occurrences', () => {
    const text = 'a {{param.user}} b {{ param.user }} c'
    const ranges = findParameterReferencesInJson(text, 'user')
    expect(ranges).toHaveLength(2)
    expect(text.slice(ranges[0].from, ranges[0].to)).toBe('{{param.user}}')
    expect(findParameterReferencesInJson(text, '')).toEqual([])
  })

  it('browser device profile lookup + label', () => {
    expect(lookupBrowserDeviceProfile('iphone_15_pro')?.engine).toBe('webkit')
    expect(lookupBrowserDeviceProfile('missing')).toBeUndefined()
    const webkit = BROWSER_DEVICE_PROFILES.find(p => p.engine === 'webkit')!
    expect(formatBrowserDeviceProfileLabel(webkit)).toContain('(WebKit)')
    const chromium = BROWSER_DEVICE_PROFILES.find(p => p.engine === 'chromium')!
    expect(formatBrowserDeviceProfileLabel(chromium)).not.toContain('(WebKit)')
  })
})

describe('filterScriptPhaseActionRows', () => {
  const rows = buildScriptPhaseActionRows(script({
    init: [{ name: 'Warmup', type: 'GET', target: '/health' }],
    actions: [
      { name: 'Login', type: 'POST', target: '/auth/login' },
      { name: 'Search products', type: 'GET', target: '/api/catalog' },
      { name: '', type: 'source_transaction' },
    ],
  } as unknown as Partial<ScriptDefinition>))

  it('returns all rows for an empty or whitespace query', () => {
    expect(filterScriptPhaseActionRows(rows, '')).toHaveLength(rows.length)
    expect(filterScriptPhaseActionRows(rows, '   ')).toHaveLength(rows.length)
  })

  it('matches on name, type, target and phase label (case-insensitive)', () => {
    expect(filterScriptPhaseActionRows(rows, 'login').map(r => r.action.name)).toEqual(['Login'])
    expect(filterScriptPhaseActionRows(rows, 'POST').map(r => r.action.name)).toEqual(['Login'])
    expect(filterScriptPhaseActionRows(rows, '/api/catalog').map(r => r.action.name)).toEqual(['Search products'])
    expect(filterScriptPhaseActionRows(rows, 'transaction').map(r => r.action.type)).toEqual(['source_transaction'])
    expect(filterScriptPhaseActionRows(rows, 'init')).toHaveLength(1)
  })

  it('preserves each row\'s original phase and actionIndex in the filtered view', () => {
    const [match] = filterScriptPhaseActionRows(rows, 'Search products')
    expect(match.phase).toBe('action')
    expect(match.actionIndex).toBe(1)
  })

  it('returns an empty array when nothing matches', () => {
    expect(filterScriptPhaseActionRows(rows, 'no-such-action')).toEqual([])
  })

  describe('action step list mutations', () => {
    const steps = (): ScriptActionDefinition[] => ([
      { name: 'a', type: 'GET', target: '/a' },
      { name: 'b', type: 'POST', target: '/b', params: { keep: 1 } },
      { name: 'c', type: 'GET', target: '/c' },
    ] as unknown as ScriptActionDefinition[])

    it('appends to an existing or undefined list', () => {
      expect(appendActionStep(undefined, { name: 'x' } as ScriptActionDefinition)).toHaveLength(1)
      expect(appendActionStep(steps(), { name: 'd' } as ScriptActionDefinition).map(s => s.name)).toEqual(['a', 'b', 'c', 'd'])
    })

    it('removes by index without touching siblings', () => {
      expect(removeActionStep(steps(), 1).map(s => s.name)).toEqual(['a', 'c'])
    })

    it('moves an item up/down and no-ops at the edges', () => {
      expect(moveActionStep(steps(), 0, 1).map(s => s.name)).toEqual(['b', 'a', 'c'])
      expect(moveActionStep(steps(), 2, -1).map(s => s.name)).toEqual(['a', 'c', 'b'])
      expect(moveActionStep(steps(), 0, -1).map(s => s.name)).toEqual(['a', 'b', 'c'])
      expect(moveActionStep(steps(), 2, 1).map(s => s.name)).toEqual(['a', 'b', 'c'])
    })

    it('updates one field and preserves the step\'s other (unknown) keys', () => {
      const original = steps()
      const next = updateActionStepField(original, 1, 'target', '/changed')
      expect(next[1]).toEqual({ name: 'b', type: 'POST', target: '/changed', params: { keep: 1 } })
      // siblings are returned by reference (untouched)
      expect(next[0]).toBe(original[0])
      expect(next[2]).toBe(original[2])
    })
  })

  describe('uniqueActionName', () => {
    it('appends "(copy)" then numbers to avoid collisions', () => {
      expect(uniqueActionName('Login', [])).toBe('Login (copy)')
      expect(uniqueActionName('Login', ['Login', 'Login (copy)'])).toBe('Login (copy 2)')
      expect(uniqueActionName('Login', ['Login (copy)', 'Login (copy 2)'])).toBe('Login (copy 3)')
    })

    it('falls back to "action" for an empty base', () => {
      expect(uniqueActionName('', [])).toBe('action (copy)')
    })
  })

  describe('duplicateActionInList', () => {
    const list = (): ScriptActionDefinition[] => ([
      { name: 'Login', type: 'POST', target: '/login', params: { mask: 'x' } },
      { name: 'Search', type: 'GET', target: '/search' },
    ] as unknown as ScriptActionDefinition[])

    it('inserts a uniquely-named deep copy right after the source', () => {
      const next = duplicateActionInList(list(), 0)
      expect(next.map(a => a.name)).toEqual(['Login', 'Login (copy)', 'Search'])
      expect(next[1]).toEqual({ name: 'Login (copy)', type: 'POST', target: '/login', params: { mask: 'x' } })
    })

    it('deep-clones so editing the copy never mutates the original', () => {
      const original = list()
      const next = duplicateActionInList(original, 0)
      ;(next[1].params as Record<string, unknown>).mask = 'changed'
      expect((original[0].params as Record<string, unknown>).mask).toBe('x')
    })

    it('returns the list unchanged for an out-of-range index', () => {
      const original = list()
      expect(duplicateActionInList(original, 9)).toBe(original)
    })
  })

  describe('transactions', () => {
    const tagged = { name: 'Login', type: 'POST', metadata: { source_transaction: 'Checkout', note: 'keep' } } as unknown as ScriptActionDefinition
    const untagged = { name: 'Search', type: 'GET' } as unknown as ScriptActionDefinition

    it('reads the transaction tag, incl. the legacy key', () => {
      expect(actionTransactionName(tagged)).toBe('Checkout')
      expect(actionTransactionName(untagged)).toBe('')
      expect(actionTransactionName({ metadata: { loadrunner_transaction: 'Legacy' } } as unknown as ScriptActionDefinition)).toBe('Legacy')
    })

    it('sets the tag while preserving other metadata', () => {
      const next = setActionTransactionName(untagged, 'Browse')
      expect(next.metadata).toEqual({ source_transaction: 'Browse' })
      const onExisting = setActionTransactionName(tagged, 'NewName')
      expect(onExisting.metadata).toEqual({ source_transaction: 'NewName', note: 'keep' })
    })

    it('clears the tag and drops metadata when it would become empty', () => {
      expect(setActionTransactionName({ name: 'x', type: 'GET', metadata: { source_transaction: 'A' } } as unknown as ScriptActionDefinition, '').metadata).toBeUndefined()
      // but keeps other metadata keys
      expect(setActionTransactionName(tagged, '').metadata).toEqual({ note: 'keep' })
    })

    it('tags only the selected indices for a group wrap', () => {
      const actions = [untagged, untagged, untagged] as ScriptActionDefinition[]
      const next = tagActionsTransaction(actions, [0, 2], 'Group')
      expect(next.map(actionTransactionName)).toEqual(['Group', '', 'Group'])
    })

    it('derives a unique default transaction name', () => {
      expect(uniqueTransactionName([])).toBe('Transaction 1')
      expect(uniqueTransactionName([tagged, { metadata: { source_transaction: 'Transaction 1' } } as unknown as ScriptActionDefinition])).toBe('Transaction 2')
    })
  })

  describe('transaction marker pairs (span timing)', () => {
    const acts = (): ScriptActionDefinition[] => ([
      { name: 'a', type: 'GET' },
      { name: 'b', type: 'POST' },
      { name: 'c', type: 'GET' },
    ] as unknown as ScriptActionDefinition[])

    it('wraps a contiguous span in a balanced lr_start/end marker pair', () => {
      const next = wrapActionsInTransactionMarkers(acts(), 0, 1, 'Checkout')
      expect(next.map(a => a.type)).toEqual(['source_transaction', 'GET', 'POST', 'source_transaction', 'GET'])
      expect(next[0]).toEqual({ name: 'Checkout', type: 'source_transaction', params: { function: 'lr_start_transaction' } })
      expect(next[3]).toEqual({ name: 'Checkout', type: 'source_transaction', params: { function: 'lr_end_transaction' } })
    })

    it('wraps a single action (lo === hi)', () => {
      const next = wrapActionsInTransactionMarkers(acts(), 1, 1, 'One')
      expect(next.map(a => a.name)).toEqual(['a', 'One', 'b', 'One', 'c'])
      expect(actionTransactionMarker(next[1])).toEqual({ role: 'start', name: 'One' })
      expect(actionTransactionMarker(next[3])).toEqual({ role: 'end', name: 'One' })
    })

    it('spans the whole min..max range, including actions between the endpoints', () => {
      // wrapping 0..2 brackets b (index 1) too — the classic span semantic
      const next = wrapActionsInTransactionMarkers(acts(), 2, 0, 'Flow')
      expect(next.map(a => a.type)).toEqual(['source_transaction', 'GET', 'POST', 'GET', 'source_transaction'])
    })

    it('is a no-op for empty names or out-of-range indices', () => {
      const original = acts()
      expect(wrapActionsInTransactionMarkers(original, 0, 1, '  ')).toBe(original)
      expect(wrapActionsInTransactionMarkers(original, 0, 9, 'X')).toBe(original)
      expect(wrapActionsInTransactionMarkers(original, -1, 1, 'X')).toBe(original)
    })

    it('detects markers and ignores tagged / plain actions', () => {
      expect(actionTransactionMarker({ type: 'source_transaction', name: 'T', params: { function: 'lr_start_transaction' } } as unknown as ScriptActionDefinition)).toEqual({ role: 'start', name: 'T' })
      expect(actionTransactionMarker({ type: 'GET', metadata: { source_transaction: 'T' } } as unknown as ScriptActionDefinition)).toBeNull()
      expect(actionTransactionMarker({ type: 'source_transaction', name: 'T', params: { function: 'lr_set_transaction_status' } } as unknown as ScriptActionDefinition)).toBeNull()
    })

    it('remaps indices around the inserted marker pair', () => {
      // wrap [1..2]: start at 1 (1,2 -> +1), end after 2 (>2 -> +2)
      expect([0, 1, 2].map(i => remapIndexAfterMarkerWrap(i, 1, 2))).toEqual([0, 2, 3])
      expect(remapIndexAfterMarkerWrap(3, 1, 2)).toBe(5)
    })
  })

  describe('computeTransactionBands', () => {
    const row = (actionIndex: number, action: Record<string, unknown>, phase = 'action') => ({
      phase, phaseLabel: phase, actionIndex, globalIndex: actionIndex, action,
    }) as unknown as Parameters<typeof computeTransactionBands>[0][number]
    const key = (phase: string, i: number) => `${phase}-${i}`

    it('bands a span from the start marker through the end marker', () => {
      const rows = [
        row(0, { type: 'GET' }),
        row(1, { type: 'source_transaction', name: 'Checkout', params: { function: 'lr_start_transaction' } }),
        row(2, { type: 'GET' }),
        row(3, { type: 'POST' }),
        row(4, { type: 'source_transaction', name: 'Checkout', params: { function: 'lr_end_transaction' } }),
        row(5, { type: 'GET' }),
      ]
      const bands = computeTransactionBands(rows)
      expect(bands.has(key('action', 0))).toBe(false)
      expect(bands.has(key('action', 5))).toBe(false)
      expect(bands.get(key('action', 1))).toEqual({ name: 'Checkout', first: true, last: false })
      expect(bands.get(key('action', 2))).toEqual({ name: 'Checkout', first: false, last: false })
      expect(bands.get(key('action', 4))).toEqual({ name: 'Checkout', first: false, last: true })
    })

    it('bands a contiguous run of metadata-tagged rows', () => {
      const rows = [
        row(0, { type: 'GET', metadata: { source_transaction: 'Login' } }),
        row(1, { type: 'POST', metadata: { source_transaction: 'Login' } }),
        row(2, { type: 'GET' }),
      ]
      const bands = computeTransactionBands(rows)
      expect(bands.get(key('action', 0))).toEqual({ name: 'Login', first: true, last: false })
      expect(bands.get(key('action', 1))).toEqual({ name: 'Login', first: false, last: true })
      expect(bands.has(key('action', 2))).toBe(false)
    })

    it('does not join same-named rows across a phase boundary', () => {
      const rows = [
        row(0, { type: 'GET', metadata: { source_transaction: 'T' } }, 'init'),
        row(0, { type: 'GET', metadata: { source_transaction: 'T' } }, 'action'),
      ]
      const bands = computeTransactionBands(rows)
      expect(bands.get(key('init', 0))).toEqual({ name: 'T', first: true, last: true })
      expect(bands.get(key('action', 0))).toEqual({ name: 'T', first: true, last: true })
    })

    it('returns an empty map when there are no transactions', () => {
      expect(computeTransactionBands([row(0, { type: 'GET' }), row(1, { type: 'POST' })]).size).toBe(0)
    })
  })

  // R3: the outline data path must stay O(n) so the action list (which becomes
  // the primary surface in Phase 2) is responsive on large generated imports.
  // The list itself is virtualized above LIST_VIRTUALIZE_THRESHOLD=100, so only
  // visible rows render; this guards the build/filter that feed it.
  it('builds and filters a 10k-action import without quadratic blow-up', () => {
    const actions = Array.from({ length: 10_000 }, (_, i) => ({
      name: `Step ${i}`,
      type: i % 2 ? 'GET' : 'POST',
      target: `/api/resource/${i}`,
    }))
    const bigScript = script({ actions } as unknown as Partial<ScriptDefinition>)

    const start = performance.now()
    const bigRows = buildScriptPhaseActionRows(bigScript)
    const filtered = filterScriptPhaseActionRows(bigRows, 'resource/9999')
    const elapsed = performance.now() - start

    expect(bigRows).toHaveLength(10_000)
    expect(filtered).toHaveLength(1)
    expect(filtered[0].actionIndex).toBe(9999)
    // O(n) build+filter over 10k rows is a few ms; a generous budget catches an
    // accidental O(n^2) regression without being flaky under load.
    expect(elapsed).toBeLessThan(1000)
  })
})
