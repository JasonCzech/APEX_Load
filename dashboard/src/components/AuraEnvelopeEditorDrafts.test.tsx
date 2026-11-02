// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { ScriptDebugBreakpoint } from '../types/replay'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { useAuraEnvelopeDrafts } from './AuraEnvelopeEditorDrafts'
import { remapBreakpointsAfterNestedRemoval, removeNestedAction } from './scriptEditorJsonModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

type Drafts = ReturnType<typeof useAuraEnvelopeDrafts>

const message = JSON.stringify({ actions: [{ id: '1;a', descriptor: 'aura://RecordUiController/ACTION$getRecord', params: { recordId: '001OLD' } }] })
const auraBody = `message=${encodeURIComponent(message)}&aura.token={{var.aura_token}}`
const leaf = (name: string): ScriptActionDefinition => ({ name, type: 'request', method: 'GET', target: `/${name}` }) as ScriptActionDefinition
const aura = (): ScriptActionDefinition => ({ name: 'Aura_Record', type: 'request', method: 'POST', target: '/aura', body: auraBody }) as ScriptActionDefinition
const scriptOf = (actions: ScriptActionDefinition[]): ScriptDefinition => ({ name: 'aura', version: '1', protocol: 'http', config: {}, actions }) as unknown as ScriptDefinition
const sentParams = (action: ScriptActionDefinition) => JSON.parse(decodeURIComponent(action.body!.split('&')[0].slice('message='.length))).actions[0].params

describe('useAuraEnvelopeDrafts', () => {
  let container: HTMLDivElement
  let root: Root
  let drafts: Drafts
  const Harness = ({ script, placedOn }: { script: ScriptDefinition; placedOn?: ScriptDefinition }) => { drafts = useAuraEnvelopeDrafts(script, () => undefined, placedOn); return null }
  const render = (script: ScriptDefinition, placedOn?: ScriptDefinition) => act(async () => { root.render(<Harness script={script} placedOn={placedOn} />) })

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  // Unapplied Parameters JSON on the Aura step at actions[2].
  const typeOnAura = async (script: ScriptDefinition) => {
    await render(script)
    await act(async () => drafts.forAction('action:2', script.actions[2]).change({ params: { 0: '{"recordId":"001NEW"}' }, context: null }))
  }

  it('keeps unapplied edits on their step when a step above it is duplicated', async () => {
    const before = scriptOf([leaf('a0'), leaf('a1'), aura()])
    await typeOnAura(before)
    // Duplicating actions[0] inserts its copy at 1: the Aura step moves to 3.
    const after = scriptOf([leaf('a0'), leaf('a0 (copy)'), leaf('a1'), aura()])
    await act(async () => drafts.follow(locations => locations.map(bp => bp.action_index > 0 ? { ...bp, action_index: bp.action_index + 1 } : bp)))
    await render(after)
    expect(drafts.problem).toBe('')
    expect(drafts.forAction('action:3', after.actions[3]).drafts.params[0]).toBe('{"recordId":"001NEW"}')
    const resolved = drafts.resolve(after)
    expect(sentParams(resolved.actions[3])).toEqual({ recordId: '001NEW' })
    expect(resolved.actions[2]).toBe(after.actions[2])
  })

  it('drops unapplied edits with their deleted step, never passing them to an identical copy', async () => {
    const before = scriptOf([leaf('a0'), leaf('a1'), aura()])
    await typeOnAura(before)
    // Duplicate the Aura step (copy at 3), then delete the original at 2.
    await act(async () => drafts.follow(locations => locations.map(bp => bp.action_index > 2 ? { ...bp, action_index: bp.action_index + 1 } : bp)))
    const removeAt = (removed: number) => (locations: ScriptDebugBreakpoint[]) => locations
      .filter(bp => bp.action_index !== removed)
      .map(bp => bp.action_index > removed ? { ...bp, action_index: bp.action_index - 1 } : bp)
    await act(async () => drafts.follow(removeAt(2)))
    const after = scriptOf([leaf('a0'), leaf('a1'), aura()])
    await render(after)
    expect(drafts.count).toBe(0)
    expect(drafts.hasPending()).toBe(false)
    expect(sentParams(drafts.resolve(after).actions[2])).toEqual({ recordId: '001OLD' })
  })

  it('follows a nested step through a removal of a sibling before it', async () => {
    const sequence = (requests: ScriptActionDefinition[]) => ({ name: 'Seq', type: 'http_sequence', params: { requests } }) as unknown as ScriptActionDefinition
    const before = scriptOf([sequence([leaf('r0'), leaf('r1'), aura()])])
    await render(before)
    await act(async () => drafts.forAction('action:0#requests[2]', aura()).change({ params: { 0: '{"recordId":"001NEW"}' }, context: null }))
    // Delete blocked action removes request 1: the Aura request is now 1.
    const removal = removeNestedAction(before.actions[0], ['requests[1]'])!
    await act(async () => drafts.follow(locations => remapBreakpointsAfterNestedRemoval(locations, 'action', 0, removal)))
    const after = scriptOf([sequence([leaf('r0'), aura()])])
    await render(after)
    expect(drafts.problem).toBe('')
    const resolved = drafts.resolve(after)
    expect(sentParams((resolved.actions[0].params!.requests as ScriptActionDefinition[])[1])).toEqual({ recordId: '001NEW' })
  })

  const removeAt = (removed: number) => (locations: ScriptDebugBreakpoint[]) => locations
    .filter(bp => bp.action_index !== removed)
    .map(bp => bp.action_index > removed ? { ...bp, action_index: bp.action_index - 1 } : bp)

  it('returns unapplied edits to their step when a structural edit is undone', async () => {
    const before = scriptOf([leaf('a0'), leaf('a1'), aura()])
    await typeOnAura(before)
    const saved = drafts.snapshot()
    // Delete actions[0]: the Aura step and its edits move to 1.
    await act(async () => drafts.follow(removeAt(0)))
    const after = scriptOf([leaf('a1'), aura()])
    await render(after)
    // More text typed after the delete stays with the step through Undo.
    await act(async () => drafts.forAction('action:1', after.actions[1]).change({ params: { 0: '{"recordId":"001NEWER"}' }, context: null }))
    let outcome = { discarded: ['?'], reverted: ['?'] }
    await act(async () => { outcome = drafts.restore(saved, before) })
    await render(before)
    expect(outcome).toEqual({ discarded: [], reverted: [] })
    expect(drafts.problem).toBe('')
    expect(drafts.forAction('action:1', before.actions[1]).drafts.params).toEqual({})
    expect(drafts.forAction('action:2', before.actions[2]).drafts.params[0]).toBe('{"recordId":"001NEWER"}')
    expect(sentParams(drafts.resolve(before).actions[2])).toEqual({ recordId: '001NEWER' })
  })

  it('discards on undo the edits started after the restored state, which has no place for them', async () => {
    const before = scriptOf([leaf('a0'), aura()])
    await render(before)
    const saved = drafts.snapshot()
    // Duplicate the Aura step, then type on the copy at 2.
    await act(async () => drafts.follow(locations => locations))
    const after = scriptOf([leaf('a0'), aura(), aura()])
    await render(after)
    await act(async () => drafts.forAction('action:2', after.actions[2]).change({ params: { 0: '{"recordId":"001NEW"}' }, context: null }))
    let dropped: string[] = []
    await act(async () => { dropped = drafts.restore(saved, before).discarded })
    await render(before)
    expect(dropped).toEqual(['Aura_Record'])
    expect(drafts.count).toBe(0)
    expect(sentParams(drafts.resolve(before).actions[1])).toEqual({ recordId: '001OLD' })
  })

  it('discards the drafts undo cannot place without a record of where they were', async () => {
    const script = scriptOf([leaf('a0'), leaf('a1'), aura()])
    await typeOnAura(script)
    let dropped: string[] = []
    await act(async () => { dropped = drafts.restore(undefined, scriptOf([leaf('a1'), aura()])).discarded })
    expect(dropped).toEqual(['Aura_Record'])
    expect(drafts.hasPending()).toBe(false)
  })

  it('keeps edits typed after the last document edit when undo leaves their step and body in place', async () => {
    // Renaming actions[0] recorded no drafts; the edits came after it.
    const before = scriptOf([leaf('a0'), leaf('a1'), aura()])
    await render(before)
    const saved = drafts.snapshot()
    const renamed = scriptOf([leaf('renamed'), leaf('a1'), aura()])
    await typeOnAura(renamed)
    let outcome = { discarded: ['?'], reverted: ['?'] }
    await act(async () => { outcome = drafts.restore(saved, before) })
    await render(before)
    expect(outcome).toEqual({ discarded: [], reverted: [] })
    expect(sentParams(drafts.resolve(before).actions[2])).toEqual({ recordId: '001NEW' })
  })

  it('never brings back on undo the edits the user reset or discarded', async () => {
    const script = scriptOf([aura(), leaf('a1'), aura()])
    await typeOnAura(script)
    await act(async () => drafts.forAction('action:0', script.actions[0]).change({ params: { 0: '{"recordId":"001A"}' }, context: null }))
    const saved = drafts.snapshot()
    // Reset on actions[2], the banner's discard on actions[0].
    await act(async () => drafts.forAction('action:2', script.actions[2]).change({ params: {}, context: null }))
    await act(async () => drafts.discard('action:0'))
    await act(async () => { drafts.restore(saved, script) })
    await render(script)
    expect(drafts.count).toBe(0)
  })

  it('returns an applied edit on undo of its Apply, and reports text a later Apply moved', async () => {
    const before = scriptOf([leaf('a0'), leaf('a1'), aura()])
    await typeOnAura(before)
    await act(async () => drafts.forAction('action:2', before.actions[2]).change({ params: { 0: '{"recordId":"001NEW"}' }, context: '{"mode":"PROD"}' }))
    const saved = drafts.snapshot()
    // Apply the context: the Parameters draft now applies to the written body,
    // then is edited further.
    const applied = scriptOf([leaf('a0'), leaf('a1'), { ...aura(), body: `${auraBody}&aura.context=%7B%7D` } as ScriptActionDefinition])
    await act(async () => drafts.forAction('action:2', before.actions[2]).change({ params: { 0: '{"recordId":"001NEW"}' }, context: null }, applied.actions[2].body))
    await render(applied)
    await act(async () => drafts.forAction('action:2', applied.actions[2]).change({ params: { 0: '{"recordId":"001NEWER"}' }, context: null }))
    let outcome = { discarded: ['?'], reverted: ['?'] }
    await act(async () => { outcome = drafts.restore(saved, before) })
    await render(before)
    expect(outcome).toEqual({ discarded: [], reverted: ['Aura_Record'] })
    expect(drafts.forAction('action:2', before.actions[2]).drafts).toEqual({ params: { 0: '{"recordId":"001NEW"}' }, context: '{"mode":"PROD"}' })
  })

  it('returns an applied edit on undo of its Apply after the step\'s other sub-action was reset', async () => {
    const before = scriptOf([leaf('a0'), leaf('a1'), aura()])
    await render(before)
    await act(async () => drafts.forAction('action:2', before.actions[2]).change({ params: { 0: 'a', 1: 'b' }, context: null }))
    const saved = drafts.snapshot()
    // Apply sub-action 0, then Reset sub-action 1, which empties the entry.
    const applied = scriptOf([leaf('a0'), leaf('a1'), { ...aura(), body: `${auraBody}&applied=1` } as ScriptActionDefinition])
    await act(async () => drafts.forAction('action:2', before.actions[2]).change({ params: { 1: 'b' }, context: null }, applied.actions[2].body))
    await render(applied)
    await act(async () => drafts.forAction('action:2', applied.actions[2]).change({ params: {}, context: null }))
    expect(drafts.count).toBe(0)
    await act(async () => { drafts.restore(saved, before) })
    await render(before)
    expect(drafts.forAction('action:2', before.actions[2]).drafts).toEqual({ params: { 0: 'a' }, context: null })
  })

  it('discards only the named step, keeping valid edits on another step', async () => {
    const script = scriptOf([aura(), leaf('a1'), aura()])
    await typeOnAura(script)
    await act(async () => drafts.forAction('action:0', script.actions[0]).change({ params: { 0: '{"recordId":"001A"}' }, context: null }))
    // A raw edit on actions[2] leaves its drafts stale.
    const edited = scriptOf([aura(), leaf('a1'), { ...aura(), body: `${auraBody}&x=1` } as ScriptActionDefinition])
    await render(edited)
    expect(drafts.problemKey).toBe('action:2')
    await act(async () => drafts.discard(drafts.problemKey))
    expect(drafts.problem).toBe('')
    expect(drafts.count).toBe(1)
    expect(sentParams(drafts.resolve(edited).actions[0])).toEqual({ recordId: '001A' })
  })

  it('holds edits stale while a Script JSON draft adds steps, even before an identical copy', async () => {
    const placed = scriptOf([leaf('a0'), leaf('a1'), aura()])
    await typeOnAura(placed)
    // Script JSON inserts a copy of the Aura step at 2: actions[2] now holds
    // the copy, whose body is identical, and the typed-on step is at 3.
    const jsonDraft = scriptOf([leaf('a0'), leaf('a1'), aura(), aura()])
    await render(jsonDraft, placed)
    // The edits are intact: resetting the Script JSON draft keeps them.
    expect(drafts.problem).toContain('Reset the Script JSON draft to keep them')
    expect(drafts.forAction('action:2', jsonDraft.actions[2]).stale).toBe(true)
    expect(drafts.forAction('action:2', jsonDraft.actions[2]).staleMessage).toContain('unapplied Script JSON draft adds or removes steps')
    expect(drafts.forAction('action:2', jsonDraft.actions[2]).staleMessage).not.toContain('request body changed')
    expect(() => drafts.resolve(jsonDraft)).toThrow(/unapplied Script JSON draft adds or removes steps/)
    // Applying the draft detaches the edits: they stay stale on the applied script.
    await act(async () => drafts.detach())
    await render(jsonDraft)
    expect(drafts.problem).toContain('Steps were added or removed in Script JSON')
    expect(() => drafts.resolve(jsonDraft)).toThrow(/Steps were added or removed/)
  })

  it('keeps edits through a Script JSON draft that leaves every step in place', async () => {
    const placed = scriptOf([leaf('a0'), leaf('a1'), aura()])
    await typeOnAura(placed)
    const jsonDraft = scriptOf([leaf('renamed'), leaf('a1'), aura()])
    await render(jsonDraft, placed)
    expect(drafts.problem).toBe('')
    expect(sentParams(drafts.resolve(jsonDraft).actions[2])).toEqual({ recordId: '001NEW' })
  })
})
