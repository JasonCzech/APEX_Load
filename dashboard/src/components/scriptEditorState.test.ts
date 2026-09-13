import { describe, expect, it } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import { EMPTY_SCRIPT } from './scriptEditorUtils'
import {
  cloneScriptForImportBaseline,
  createScriptEditorDispatchers,
  createScriptEditorState,
  scriptEditorReducer,
  scriptHasImportMetadata,
  selectScriptEditorDocument,
  selectScriptEditorIsBusy,
  selectScriptEditorReplayDebug,
  type ScriptEditorEvent,
} from './scriptEditorState'

function makeScript(name = 'Checkout'): ScriptDefinition {
  return {
    ...EMPTY_SCRIPT,
    name,
    actions: [{ name: 'Home', type: 'GET', target: '/', headers: {}, body: '' }],
  }
}

describe('script editor state', () => {
  it('creates explicit domain slices with the legacy defaults', () => {
    const state = createScriptEditorState(makeScript())

    expect(state.documentHistory.script.name).toBe('Checkout')
    expect(state.documentHistory.jsonDirty).toBe(false)
    expect(state.documentHistory.editorHistory).toEqual({ past: [], future: [] })
    expect(state.navigation).toEqual({
      activeAction: null,
      openSections: {
        config: false,
        actions: true,
        runtime: false,
        import: false,
        replay: false,
      },
    })
    expect(state.parametersData.dataSourceHistory).toEqual({})
    expect(state.importReview.baselineScript).toBeNull()
    expect(state.replayDebug.replayOptions.ignoreThinkTime).toBe(true)
    expect(state.asyncOperations).toEqual({
      saving: false,
      validating: false,
      error: '',
      success: '',
      warnings: [],
    })
  })

  it('supports functional field updates through stable compatibility dispatchers', () => {
    let state = createScriptEditorState(makeScript())
    const dispatch = (event: ScriptEditorEvent) => {
      state = scriptEditorReducer(state, event)
    }
    const setters = createScriptEditorDispatchers(dispatch)

    setters.setWarnings(current => [...current, 'First warning'])
    setters.setWarnings(current => [...current, 'Second warning'])
    setters.setEditorHistory(current => ({ ...current, past: [state.documentHistory.script] }))
    setters.setReplayOptions(current => ({ ...current, startPaused: true }))
    setters.setOpenSections(current => ({ ...current, runtime: true }))

    expect(state.asyncOperations.warnings).toEqual(['First warning', 'Second warning'])
    expect(state.documentHistory.editorHistory.past).toHaveLength(1)
    expect(state.replayDebug.replayOptions.startPaused).toBe(true)
    expect(state.navigation.openSections.runtime).toBe(true)

    const currentState = state
    setters.setScript(state.documentHistory.script)
    expect(state).toBe(currentState)
  })

  it('hydrates document state and resets only the same transient state as the prior hook', () => {
    const initial = createScriptEditorState(makeScript('Before'))
    const dirty = scriptEditorReducer(initial, {
      type: 'documentHistory/update',
      update: current => ({ ...current, jsonDirty: true, jsonText: '{ invalid' }),
    })
    const withTransientState = scriptEditorReducer(dirty, {
      type: 'parametersData/update',
      update: current => ({
        ...current,
        openDataEditor: 2,
        dataEditorMode: { 2: 'raw' },
        parameterCandidates: [{
          id: 'candidate-1',
          keyHint: 'user',
          value: 'alice',
          varName: 'user',
          occurrences: [],
          selected: true,
        }],
      }),
    })
    const nextScript: ScriptDefinition = {
      ...makeScript('Imported'),
      metadata: { import: { source: 'har', preserved: { archive: 'checkout.har' } } },
    }
    const hydrated = scriptEditorReducer(withTransientState, { type: 'editor/hydrate', script: nextScript })

    expect(hydrated.documentHistory.script).toBe(nextScript)
    expect(hydrated.documentHistory.jsonDirty).toBe(false)
    expect(hydrated.documentHistory.jsonText).toContain('"Imported"')
    expect(hydrated.navigation.openSections.import).toBe(true)
    expect(hydrated.parametersData.openDataEditor).toBeNull()
    expect(hydrated.parametersData.dataEditorMode).toEqual({})
    expect(hydrated.parametersData.parameterCandidates).toEqual([])
    expect(hydrated.importReview.baselineScript).not.toBe(nextScript)
    expect(hydrated.importReview.baselineScript?.metadata).toBe(nextScript.metadata)
  })

  it('keeps imported metadata shared while cloning the editable baseline body', () => {
    const source: ScriptDefinition = {
      ...makeScript(),
      metadata: { import: { source: 'loadrunner', preserved: { archive: { size: 3_000_000 } } } },
    }

    const baseline = cloneScriptForImportBaseline(source)

    expect(baseline).not.toBe(source)
    expect(baseline.actions).not.toBe(source.actions)
    expect(baseline.metadata).toBe(source.metadata)
  })

  it.each([
    ['source name', { source: 'har' }, true],
    ['source script name', { source_script_name: 'Action.c' }, true],
    ['preserved metadata', { preserved: { source_review: {} } }, true],
    ['empty import metadata', {}, false],
  ])('detects %s', (_label, metadata, expected) => {
    const script: ScriptDefinition = { ...makeScript(), metadata: { import: metadata } }
    expect(scriptHasImportMetadata(script)).toBe(expected)
  })

  it('exposes pure selectors for domain state and aggregate busy status', () => {
    const state = scriptEditorReducer(createScriptEditorState(makeScript()), {
      type: 'replayDebug/update',
      update: current => ({ ...current, replaying: true }),
    })

    expect(selectScriptEditorDocument(state)).toBe(state.documentHistory)
    expect(selectScriptEditorReplayDebug(state)).toBe(state.replayDebug)
    expect(selectScriptEditorIsBusy(state)).toBe(true)
  })
})

describe('complete document edit history', () => {
  it('groups typing in one field and separates other fields, structural edits and pauses', () => {
    let state = createScriptEditorState(makeScript())
    for (const [name, group, time] of [['C', 'name', 1000], ['Ch', 'name', 1200], ['Checkout2', 'name', 2200], ['Another', 'target', 2300]] as const) {
      state = scriptEditorReducer(state, { type: 'document/edit', update: current => ({ ...current, name }), group, time })
    }
    expect(state.documentHistory.editorHistory.past.map(s => s.name)).toEqual(['Checkout', 'Ch', 'Checkout2'])
    const snapshot = state.documentHistory.script
    state = scriptEditorReducer(state, { type: 'document/edit', update: current => ({ ...current, actions: [] }), time: 2400 })
    expect(state.documentHistory.editorHistory.past[state.documentHistory.editorHistory.past.length - 1]?.actions).toEqual(snapshot.actions)
    expect(snapshot.actions).toHaveLength(1)
  })
  it('ignores no-op edits, invalidates redo on new edits and bounds snapshots', () => {
    let state = createScriptEditorState(makeScript())
    expect(scriptEditorReducer(state, { type: 'document/edit', update: current => ({ ...current }), time: 1 })).toBe(state)
    state = scriptEditorReducer(state, { type: 'documentHistory/update', update: current => ({ ...current, editorHistory: { past: [], future: [makeScript('Future')] } }) })
    for (let i = 0; i < 45; i++) state = scriptEditorReducer(state, { type: 'document/edit', update: current => ({ ...current, name: `Edit ${i}` }), time: i })
    expect(state.documentHistory.editorHistory.past).toHaveLength(30)
    expect(state.documentHistory.editorHistory.future).toEqual([])
  })
})
