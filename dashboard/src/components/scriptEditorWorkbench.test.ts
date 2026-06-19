// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from 'vitest'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import type { ProtocolEvidence } from '../types'
import {
  BROWSER_DEVICE_PROFILES,
  SCRIPT_JSON_TAB_ID,
  appendActionStep,
  buildScriptPhaseActionRows,
  clampDebuggerPaneSize,
  codePathForDebugRow,
  defaultCenterTabsState,
  filterScriptPhaseActionRows,
  findParameterReferencesInJson,
  moveActionStep,
  removeActionStep,
  updateActionStepField,
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
    expect(isCenterWorkbenchTabId('script-json')).toBe(true)
    expect(isCenterWorkbenchTabId('metadata-json')).toBe(true)
    expect(isCenterWorkbenchTabId('conversion-mapping')).toBe(false)
    expect(isCenterWorkbenchTabId('nope')).toBe(false)
    expect(isCenterWorkbenchTabId(42)).toBe(false)
  })

  it('returns default when storage empty', () => {
    expect(readCenterTabsState('k')).toEqual(defaultCenterTabsState())
  })

  it('always keeps script-json first and dedups it', () => {
    window.localStorage.setItem('k', JSON.stringify({ tabs: ['configuration', 'script-json'], activeTabId: 'configuration' }))
    const state = readCenterTabsState('k')
    expect(state.tabs[0]).toBe(SCRIPT_JSON_TAB_ID)
    expect(state.tabs.filter(t => t === SCRIPT_JSON_TAB_ID)).toHaveLength(1)
    expect(state.tabs).toEqual([SCRIPT_JSON_TAB_ID, 'runtime-settings'])
    expect(state.activeTabId).toBe('runtime-settings')
  })

  it('falls back to script-json for an unknown active tab', () => {
    window.localStorage.setItem('k', JSON.stringify({ tabs: ['configuration'], activeTabId: 'bogus' }))
    expect(readCenterTabsState('k').activeTabId).toBe(SCRIPT_JSON_TAB_ID)
  })

  it('drops stale standalone conversion mapping tabs from storage', () => {
    window.localStorage.setItem('k', JSON.stringify({ tabs: ['conversion-mapping', 'imported-metadata'], activeTabId: 'conversion-mapping' }))
    expect(readCenterTabsState('k')).toEqual({
      tabs: [SCRIPT_JSON_TAB_ID, 'imported-metadata'],
      activeTabId: SCRIPT_JSON_TAB_ID,
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

describe('misc helpers', () => {
  it('codePathForDebugRow', () => {
    expect(codePathForDebugRow({ phase: 'action', actionIndex: 2 })).toBe('script.actions[2]')
    expect(codePathForDebugRow({ phase: 'init', actionIndex: 0 })).toBe('script.init[0]')
    expect(codePathForDebugRow({ phase: 'teardown', actionIndex: 1 })).toBe('script.teardown[1]')
    expect(codePathForDebugRow(null)).toBe('')
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
