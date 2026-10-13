// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { NavigationGuardRegisterContext, NavigationGuardRunnerContext, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import type { ScriptDefinition } from '../types/scripts'
import type { ScriptAuthoringBaseline } from '../types/storedContent'
import { formatScriptJson } from './scriptEditorJsonModel'
import { scriptSaveAvailability, type ScriptSaveInputs } from './scriptAuthoringSession'
import { useScriptAuthoringSession } from './useScriptAuthoringSession'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const script = (name: string): ScriptDefinition => ({ name, version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [] })
const baselineOf = (name: string, revision: string): ScriptAuthoringBaseline => ({
  etag: `"${revision}"`,
  projection: { script: script(name), content_revision: revision } as unknown as ScriptAuthoringBaseline['projection'],
})

type Session = ReturnType<typeof useScriptAuthoringSession>
let session: Session
let props: {
  scriptId?: string
  document: ScriptDefinition
  saved: ScriptDefinition
  initiallyDirty?: boolean
  jsonDirty?: boolean
  pending?: number
}
const getScriptAuthoring = vi.fn<(id: string, projectId?: string, signal?: AbortSignal) => Promise<ScriptAuthoringBaseline>>()
const onHydrate = vi.fn()
const onAdopt = vi.fn()
const onClose = vi.fn()
const onReturn = vi.fn()
const scope = { current: {} as unknown }
const mounted = { current: true }
const documentKeyRef = { current: null as string | null }

function Harness() {
  const key = formatScriptJson(props.document)
  documentKeyRef.current = key
  session = useScriptAuthoringSession({
    initiallyDirty: props.initiallyDirty,
    resolveCurrentScript: () => props.document,
    flushPendingEdits: base => ({ script: base, editGeneration: 0 }),
    documentKey: key,
    context: '["p",null,null]',
    baseline: {
      scriptId: props.scriptId,
      projectId: 'p',
      getScriptAuthoring,
      resourceScopeRef: scope,
      mountedRef: mounted,
      documentKeyRef,
      hasPendingEdits: () => (props.pending ?? 0) > 0,
      onHydrate,
      onAdopt,
    },
    document: {
      jsonDirty: Boolean(props.jsonDirty),
      pendingEditCount: props.pending ?? 0,
      formattedDocument: key,
      savedDocumentKey: formatScriptJson(props.saved),
    },
    navigation: { id: 'script-editor-s1', label: 'Edit script', save: async () => true, onClose, onReturn, retainedGuardIds: ['source-review'] },
  })
  return null
}

describe('useScriptAuthoringSession ownership (A8)', () => {
  let container: HTMLDivElement
  let root: Root
  let registration: { guard: NavigationGuard; dirty: boolean } | undefined
  let guardedRuns: number
  let retainedOnLeave: (readonly string[] | undefined)[]
  const render = async () => {
    await act(async () => root.render(
      <NavigationGuardRegisterContext.Provider value={(guard, dirty) => { registration = { guard, dirty }; return () => undefined }}>
        <NavigationGuardRunnerContext.Provider value={{ runGuarded: (proceed: () => void, retained?: readonly string[]) => { guardedRuns += 1; retainedOnLeave.push(retained); if (!registration?.dirty) proceed() } } as never}>
          <Harness />
        </NavigationGuardRunnerContext.Provider>
      </NavigationGuardRegisterContext.Provider>,
    ))
  }
  beforeEach(() => {
    container = document.createElement('div')
    root = createRoot(container)
    registration = undefined
    guardedRuns = 0
    retainedOnLeave = []
    for (const fn of [getScriptAuthoring, onHydrate, onAdopt, onClose, onReturn]) fn.mockReset()
    props = { scriptId: 's1', document: script('Draft'), saved: script('Draft') }
  })
  afterEach(() => { act(() => root.unmount()) })

  it('pairs a stored script with its loaded baseline and hydrates the editor from it', async () => {
    getScriptAuthoring.mockResolvedValue(baselineOf('Stored', 'rev-1'))
    await render()
    expect(getScriptAuthoring).toHaveBeenCalledWith('s1', 'p', expect.any(AbortSignal))
    expect(session.authoringRevisionState).toBe('ready')
    expect(session.authoringBaseline?.projection.content_revision).toBe('rev-1')
    expect(session.currentBinding?.contentRevision).toBe('rev-1')
    expect(onHydrate).toHaveBeenCalledWith(script('Stored'))
  })

  it('blocks a draft edited while its baseline loaded, and adopts the reviewed baseline on request', async () => {
    let resolve!: (value: ScriptAuthoringBaseline) => void
    getScriptAuthoring.mockReturnValue(new Promise(r => { resolve = r }))
    await render()
    expect(session.authoringRevisionState).toBe('loading')
    props.document = script('Draft edited')
    await render()
    await act(async () => { resolve(baselineOf('Stored', 'rev-2')) })
    expect(session.authoringRevisionState).toBe('blocked')
    expect(session.reviewedAuthoringBaseline?.projection.content_revision).toBe('rev-2')
    expect(onHydrate).not.toHaveBeenCalled()

    act(() => session.adoptReviewedAuthoringBaseline(false))
    expect(onAdopt).toHaveBeenCalledWith(script('Stored'), script('Stored'), false)
    expect(session.authoringRevisionState).toBe('ready')
    expect(session.authoringBaseline?.projection.content_revision).toBe('rev-2')
    expect(session.reviewedAuthoringBaseline).toBeNull()
  })

  it('reports a baseline that could not be loaded, and a save that needs reconciliation', async () => {
    getScriptAuthoring.mockRejectedValue(new Error('controller unavailable'))
    await render()
    expect(session.authoringRevisionState).toBe('blocked')
    expect(session.authoringRevisionReason).toContain('controller unavailable')

    getScriptAuthoring.mockResolvedValue(baselineOf('Stored', 'rev-3'))
    await act(async () => { await session.loadCurrentAuthoringBaseline() })
    expect(session.reviewedAuthoringBaseline?.projection.content_revision).toBe('rev-3')
    act(() => session.blockForReconciliation('The authored script changed.'))
    expect(session.authoringRevisionState).toBe('blocked')
    expect(session.reviewedAuthoringBaseline).toBeNull()
    expect(session.authoringRevisionReason).toBe('The authored script changed.')
  })

  it('is the one dirty calculation, and markSaved clears an initial save', async () => {
    props = { scriptId: undefined, document: script('Draft'), saved: script('Draft'), initiallyDirty: true }
    await render()
    expect(session.authoringRevisionState).toBe('ready')
    expect(session.dirty).toBe(true)
    expect(registration?.dirty).toBe(true)
    act(() => session.markSaved())
    await render()
    expect(session.dirty).toBe(false)
    for (const change of [{ jsonDirty: true }, { pending: 2 }, { document: script('Edited') }]) {
      Object.assign(props, { jsonDirty: false, pending: 0, document: script('Draft') }, change)
      await render()
      expect(session.dirty, JSON.stringify(change)).toBe(true)
    }
  })

  it('leaves the editor, by Close or by its return target, only through the guard', async () => {
    getScriptAuthoring.mockResolvedValue(baselineOf('Draft', 'rev-1'))
    props.document = script('Edited')
    await render()
    act(() => session.returnToSource())
    act(() => session.close())
    expect(guardedRuns).toBe(2)
    // The source stays open behind the editor, so its guards are kept.
    expect(retainedOnLeave).toEqual([['source-review'], ['source-review']])
    expect(onReturn).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
    props.document = script('Draft')
    await render()
    act(() => session.returnToSource())
    expect(onReturn).toHaveBeenCalledOnce()
  })
})

describe('scriptSaveAvailability', () => {
  const ready: ScriptSaveInputs = { stored: true, revisionState: 'ready', hasBaseline: true, saving: false, secretBindingsOpen: false, validating: false, dirty: true, hasName: true, jsonParseMessage: '', baseURLError: '' }
  it('saves a dirty, named, valid draft against a ready baseline', () => {
    expect(scriptSaveAvailability(ready)).toEqual({ disabled: false, title: 'Save script' })
  })
  it('refuses to save, and says why, for each reason', () => {
    const reasons: [Partial<typeof ready>, string][] = [
      [{ opening: true }, 'The saved script is opening'],
      [{ revisionState: 'blocked' }, 'Load and review a protected authoring baseline before saving'],
      [{ revisionState: 'loading' }, 'Load and review a protected authoring baseline before saving'],
      [{ saving: true }, 'Saving script'],
      [{ validating: true }, 'Wait for validation to finish before saving'],
      [{ jsonParseMessage: 'bad' }, 'Fix invalid Script JSON before saving'],
      [{ dirty: false }, 'No unsaved changes to save'],
      [{ hasName: false }, 'Enter a script name before saving'],
      [{ baseURLError: 'Base URL is invalid' }, 'Base URL is invalid'],
      // These keep the plain title: the button is off while they hold.
      [{ hasBaseline: false }, 'Save script'],
      [{ secretBindingsOpen: true }, 'Save script'],
    ]
    for (const [change, title] of reasons) {
      expect(scriptSaveAvailability({ ...ready, ...change }), JSON.stringify(change)).toEqual({ disabled: true, title })
    }
  })
  it('gives the first reason when several hold', () => {
    const titleOf = (change: Partial<typeof ready>) => scriptSaveAvailability({ ...ready, ...change }).title
    expect(titleOf({ opening: true, revisionState: 'blocked', saving: true })).toBe('The saved script is opening')
    expect(titleOf({ revisionState: 'blocked', saving: true, validating: true })).toBe('Load and review a protected authoring baseline before saving')
    expect(titleOf({ saving: true, validating: true, jsonParseMessage: 'bad' })).toBe('Saving script')
    expect(titleOf({ validating: true, jsonParseMessage: 'bad', dirty: false })).toBe('Wait for validation to finish before saving')
    expect(titleOf({ jsonParseMessage: 'bad', dirty: false, hasName: false })).toBe('Fix invalid Script JSON before saving')
    expect(titleOf({ dirty: false, hasName: false, baseURLError: 'Base URL is invalid' })).toBe('No unsaved changes to save')
    expect(titleOf({ hasName: false, baseURLError: 'Base URL is invalid' })).toBe('Enter a script name before saving')
  })
  it('lets an unsaved script save with no baseline to wait for', () => {
    expect(scriptSaveAvailability({ ...ready, stored: false, revisionState: 'loading', hasBaseline: false })).toEqual({ disabled: false, title: 'Save script' })
  })
  it('saves a script not yet in the library as it stands, with or without edits', () => {
    // A template, a composed block or a recording is content worth saving
    // before any edit; only a stored script has nothing to save until it changes.
    const unstored = { ...ready, stored: false, hasBaseline: false, dirty: false }
    expect(scriptSaveAvailability(unstored)).toEqual({ disabled: false, title: 'Save script' })
    expect(scriptSaveAvailability({ ...unstored, hasName: false })).toEqual({ disabled: true, title: 'Enter a script name before saving' })
  })
})

describe('useScriptAuthoringSession without a baseline', () => {
  it('is ready to save, and the baseline calls do nothing', async () => {
    let unowned!: ReturnType<typeof useScriptAuthoringSession>
    function Unowned() {
      unowned = useScriptAuthoringSession({
        resolveCurrentScript: () => script('Draft'),
        flushPendingEdits: base => ({ script: base, editGeneration: 0 }),
        documentKey: formatScriptJson(script('Draft')),
        contentRevision: 'rev-9',
        context: '[]',
      })
      return null
    }
    const root = createRoot(document.createElement('div'))
    await act(async () => root.render(<Unowned />))
    expect(unowned.authoringRevisionState).toBe('ready')
    act(() => unowned.blockForReconciliation('changed'))
    act(() => unowned.acceptSavedBaseline(baselineOf('Stored', 'rev-1')))
    expect(unowned.authoringRevisionState).toBe('ready')
    expect(unowned.authoringBaseline).toBeNull()
    expect(unowned.currentBinding?.contentRevision).toBe('rev-9')
    act(() => root.unmount())
  })
})
