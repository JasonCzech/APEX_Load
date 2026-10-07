// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import { formatScriptJson } from './scriptEditorJsonModel'
import { draftFingerprint } from './scriptAuthoringSession'
import { useScriptAuthoringSession } from './useScriptAuthoringSession'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const script = (name: string): ScriptDefinition => ({ name, version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [] })
type Session = ReturnType<typeof useScriptAuthoringSession>
let session: Session
let props: { document: ScriptDefinition; revision: string; context: string }
const flush = vi.fn((base: ScriptDefinition) => ({ script: { ...base, name: `${base.name} + cells` }, editGeneration: 7 }))

function Harness() {
  session = useScriptAuthoringSession({
    resolveCurrentScript: () => props.document,
    flushPendingEdits: flush,
    documentKey: formatScriptJson(props.document),
    contentRevision: props.revision,
    context: props.context,
  })
  return null
}

describe('useScriptAuthoringSession', () => {
  let container: HTMLDivElement
  let root: Root
  const render = async () => { await act(async () => root.render(<Harness />)) }
  beforeEach(async () => {
    container = document.createElement('div')
    root = createRoot(container)
    props = { document: script('Draft'), revision: `scr-v1-${'a'.repeat(64)}`, context: '["p",null,null]' }
    flush.mockClear()
    await render()
  })
  afterEach(() => { act(() => root.unmount()) })

  it('resolves the draft once with pending cells flushed and fingerprints that exact document', () => {
    const draft = session.resolveDraft()
    expect(flush).toHaveBeenCalledOnce()
    expect(draft.script.name).toBe('Draft + cells')
    expect(draft.editGeneration).toBe(7)
    expect(draft.fingerprint).toBe(draftFingerprint(formatScriptJson(draft.script)))
  })

  it('drops a late response once an edit, a newer request or a rebaseline intervenes', async () => {
    props.document = script('Draft + cells')
    await render()
    const first = session.beginValidation(session.resolveDraft(), false)
    expect(first.sent.fingerprint).toBe(draftFingerprint(formatScriptJson(script('Draft + cells + cells'))))

    // The flushed document is what renders next; the request describes it.
    props.document = script('Draft + cells + cells')
    await render()
    expect(session.admitValidation(first, { validated_fingerprint: first.sent.fingerprint })).toBe('current')
    act(() => session.adoptValidated(first, formatScriptJson(props.document)))
    await render()
    expect(session.validationIsCurrent).toBe(true)

    // An edit makes the adopted result stale.
    props.document = script('Edited after validation')
    await render()
    expect(session.validationIsCurrent).toBe(false)

    // A newer request supersedes an older one even if it echoes correctly.
    const older = session.beginValidation(session.resolveDraft(), false)
    const newer = session.beginValidation(session.resolveDraft(), false)
    props.document = { ...flush.mock.results[flush.mock.results.length - 1].value.script }
    await render()
    expect(session.validationRequestIsCurrent(older)).toBe(false)
    expect(session.admitValidation(older, { validated_fingerprint: older.sent.fingerprint })).toBe('superseded')
    expect(session.validationRequestIsCurrent(newer)).toBe(true)

    // A new baseline revision (another save) supersedes the in-flight request.
    props.revision = `scr-v1-${'b'.repeat(64)}`
    await render()
    expect(session.admitValidation(newer, { validated_fingerprint: newer.sent.fingerprint })).toBe('superseded')
  })

  it('makes an adopted result stale when its context or baseline revision changes', async () => {
    props.document = script('Draft + cells')
    await render()
    const request = session.beginValidation(session.resolveDraft(), false)
    props.document = script('Draft + cells + cells')
    await render()
    act(() => session.adoptValidated(request, formatScriptJson(props.document)))
    await render()
    expect(session.validationIsCurrent).toBe(true)
    props.context = '["p",null,{"token":"seeded"}]'
    await render()
    expect(session.validationIsCurrent).toBe(false)
    props.context = '["p",null,null]'
    await render()
    expect(session.validationIsCurrent).toBe(true)
    props.revision = `scr-v1-${'c'.repeat(64)}`
    await render()
    expect(session.validationIsCurrent).toBe(false)
  })

  it('invalidates every in-flight request and the current result on a scope change', async () => {
    props.document = script('Draft + cells')
    await render()
    const request = session.beginValidation(session.resolveDraft(), false)
    props.document = script('Draft + cells + cells')
    await render()
    act(() => session.adoptValidated(request, formatScriptJson(props.document)))
    await render()
    expect(session.validationIsCurrent).toBe(true)
    act(() => session.invalidateValidation())
    await render()
    expect(session.validationIsCurrent).toBe(false)
    expect(session.admitValidation(request, { validated_fingerprint: request.sent.fingerprint })).toBe('superseded')
  })
})
