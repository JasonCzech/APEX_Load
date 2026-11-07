import { describe, expect, it } from 'vitest'

import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import {
  defaultTransactionName,
  groupSpan,
  nestedNameConflict,
  remapLocationKey,
  removeTopLevelRows,
  removeTransaction,
  renameTransaction,
  resolveWrapTarget,
  scriptTransactionNames,
  spanCrossing,
  transactionNameProblem,
  wrapInTransaction,
  type WrapTarget,
} from './outlineTransactionEdits'
import { buildScriptPhaseActionRows, debugSequenceKey } from './scriptEditorWorkbench'
import { buildOutlineModel, type OutlineGroup, type OutlineModel } from './scriptOutlineModel'

type Raw = Record<string, unknown>
const step = (name: string, extra: Raw = {}): Raw => ({ name, type: 'request', method: 'GET', target: `/${name}`, ...extra })
const marker = (role: 'start' | 'end', name: string, sub = false): Raw => ({
  name: `${role === 'start' ? 'Start' : 'End'} ${name} transaction`,
  type: 'source_transaction',
  // A sub-transaction starts as (name, parent), as the engine requires.
  params: { function: `lr_${role}_${sub ? 'sub_' : ''}transaction`, args: role === 'end' ? [name, 'LR_AUTO'] : sub ? [name, name.split('||')[0]] : [name] },
})
const scriptOf = (actions: Raw[], init: Raw[] = [], teardown: Raw[] = []): ScriptDefinition => ({
  name: 'wrap', protocol: 'http',
  actions: actions as unknown as ScriptActionDefinition[],
  init: init as unknown as ScriptActionDefinition[],
  teardown: teardown as unknown as ScriptActionDefinition[],
} as unknown as ScriptDefinition)
const modelOf = (script: ScriptDefinition): OutlineModel => buildOutlineModel(buildScriptPhaseActionRows(script))
const names = (actions: ScriptActionDefinition[] | undefined) => (actions || []).map(action => action.name)
const target = (script: ScriptDefinition, keys: string[]): WrapTarget => {
  const resolved = resolveWrapTarget(script, modelOf(script), keys)
  if (!resolved.ok) throw new Error(resolved.reason)
  return resolved.target
}
const groupNamed = (model: OutlineModel, name: string): OutlineGroup => {
  const group = [...model.groupsByKey.values()].find(candidate => candidate.name === name)
  if (!group) throw new Error(`no group ${name}`)
  return group
}
const loop = (name: string, steps: Raw[]): Raw => ({ name, type: 'control_loop', control: { count: 2, steps } })

describe('resolveWrapTarget', () => {
  it('spans the selected top-level steps and counts the steps between them', () => {
    const script = scriptOf([step('a'), step('b'), step('c'), step('d')])
    expect(target(script, ['action:0', 'action:2'])).toMatchObject({ phase: 'action', rootIndex: null, lo: 0, hi: 2, selected: 2, between: 1, perStep: true, stepIndices: [0, 2] })
  })

  it('takes a transaction row as its whole extent, markers included', () => {
    const script = scriptOf([step('a'), marker('start', 'Browse'), step('b'), marker('end', 'Browse'), step('c')])
    const model = modelOf(script)
    const resolved = resolveWrapTarget(script, model, [groupNamed(model, 'Browse').key, 'action:0'])
    expect(resolved).toMatchObject({ ok: true, target: { lo: 0, hi: 3, between: 0, perStep: false } })
  })

  it('refuses a span that cuts across a transaction', () => {
    const script = scriptOf([step('a'), marker('start', 'Browse'), step('b'), marker('end', 'Browse')])
    const resolved = resolveWrapTarget(script, modelOf(script), ['action:0', 'action:2'])
    expect(resolved).toEqual({ ok: false, reason: expect.stringContaining('“Browse”') })
  })

  it('refuses steps of different levels or phases', () => {
    const script = scriptOf([step('a'), loop('l', [step('x')])], [step('i')])
    expect(resolveWrapTarget(script, modelOf(script), ['action:0', 'action:1#loop[0]'])).toEqual({ ok: false, reason: expect.stringContaining('same level') })
    expect(resolveWrapTarget(script, modelOf(script), ['action:0', 'init:0'])).toEqual({ ok: false, reason: expect.stringContaining('same level') })
  })

  it('refuses requests of a sequence, which the sequence times', () => {
    const script = scriptOf([{ name: 'seq', type: 'http_sequence', params: { requests: [step('r1'), step('r2')] } }])
    expect(resolveWrapTarget(script, modelOf(script), ['action:0:0'])).toEqual({ ok: false, reason: expect.stringContaining('sequence') })
  })

  it('resolves steps inside a block to that block’s list, without per-step tagging', () => {
    const script = scriptOf([loop('l', [step('x'), step('y'), step('z')])])
    expect(target(script, ['action:0#loop[0]', 'action:0#loop[1]'])).toMatchObject({ rootIndex: 0, parentPath: [], lo: 0, hi: 1, perStep: false })
  })

  it('refuses an empty selection or keys it does not know', () => {
    const script = scriptOf([step('a')])
    expect(resolveWrapTarget(script, modelOf(script), []).ok).toBe(false)
    expect(resolveWrapTarget(script, modelOf(script), ['phase:action']).ok).toBe(false)
  })
})

describe('wrapInTransaction', () => {
  it('brackets a top-level span with a marker pair and remaps later steps', () => {
    const script = scriptOf([step('a'), step('b'), step('c')])
    const edit = wrapInTransaction(script, target(script, ['action:0', 'action:1']), ' Login ', 'span')
    expect(names(edit.script.actions)).toEqual(['Start Login transaction', 'a', 'b', 'End Login transaction', 'c'])
    expect(edit.script.actions[0].params).toEqual({ function: 'lr_start_transaction', args: ['Login'] })
    expect(edit.focus).toEqual({ phase: 'action', actionIndex: 0 })
    expect(edit.remap({ phase: 'action', actionIndex: 1 })).toEqual({ phase: 'action', actionIndex: 2 })
    expect(edit.remap({ phase: 'action', actionIndex: 2 })).toEqual({ phase: 'action', actionIndex: 4 })
    expect(edit.remap({ phase: 'init', actionIndex: 0 })).toEqual({ phase: 'init', actionIndex: 0 })
  })

  it('wraps in Init and Teardown too', () => {
    const script = scriptOf([step('a')], [step('i1'), step('i2')], [step('t1')])
    expect(names(wrapInTransaction(script, target(script, ['init:1']), 'Setup', 'span').script.init)).toEqual(['i1', 'Start Setup transaction', 'i2', 'End Setup transaction'])
    expect(names(wrapInTransaction(script, target(script, ['teardown:0']), 'Close', 'span').script.teardown)).toEqual(['Start Close transaction', 't1', 'End Close transaction'])
  })

  it('tags each step for per-step timing and leaves the list in place', () => {
    const script = scriptOf([step('a'), step('b'), step('c')])
    const edit = wrapInTransaction(script, target(script, ['action:0', 'action:2']), 'Grouped', 'per-step')
    expect(edit.script.actions.map(action => action.metadata?.source_transaction)).toEqual(['Grouped', undefined, 'Grouped'])
    expect(edit.remap({ phase: 'action', actionIndex: 2 })).toEqual({ phase: 'action', actionIndex: 2 })
  })

  it('wraps steps inside each kind of block list and remaps paths in that list only', () => {
    const ifBlock: Raw = { name: 'if', type: 'control_if', control: { condition: 'x', steps: [step('t0'), step('t1')], else: [step('e0'), step('e1')] } }
    const script = scriptOf([ifBlock])
    const edit = wrapInTransaction(script, target(script, ['action:0#else[1]']), 'Else', 'span')
    expect(names(edit.script.actions[0].control?.else)).toEqual(['e0', 'Start Else transaction', 'e1', 'End Else transaction'])
    expect(names(edit.script.actions[0].control?.steps)).toEqual(['t0', 't1'])
    expect(edit.focus).toEqual({ phase: 'action', actionIndex: 0, stepPath: ['else[1]'] })
    expect(edit.remap({ phase: 'action', actionIndex: 0, stepPath: ['else[1]'] })).toEqual({ phase: 'action', actionIndex: 0, stepPath: ['else[2]'] })
    expect(edit.remap({ phase: 'action', actionIndex: 0, stepPath: ['then[1]'] })).toEqual({ phase: 'action', actionIndex: 0, stepPath: ['then[1]'] })

    const switchBlock: Raw = { name: 'sw', type: 'control_switch', control: { cases: [{ value: '1', steps: [step('c0')] }, { value: '2', steps: [step('d0'), step('d1')] }] } }
    const parallel: Raw = { name: 'par', type: 'control_parallel', control: { branches: [{ name: 'b0', steps: [step('p0')] }, { name: 'b1', steps: [step('q0'), step('q1')] }] } }
    const more = scriptOf([switchBlock, parallel])
    const cased = wrapInTransaction(more, target(more, ['action:0#case[1][0]', 'action:0#case[1][1]']), 'Case', 'span')
    expect(names(cased.script.actions[0].control?.cases?.[1].steps)).toEqual(['Start Case transaction', 'd0', 'd1', 'End Case transaction'])
    expect(names(cased.script.actions[0].control?.cases?.[0].steps)).toEqual(['c0'])
    expect(cased.remap({ phase: 'action', actionIndex: 0, stepPath: ['case[1][1]'] })).toEqual({ phase: 'action', actionIndex: 0, stepPath: ['case[1][2]'] })
    const branched = wrapInTransaction(more, target(more, ['action:1#branches[1].steps[1]']), 'Branch', 'span')
    expect(names(branched.script.actions[1].control?.branches?.[1].steps)).toEqual(['q0', 'Start Branch transaction', 'q1', 'End Branch transaction'])
  })

  it('wraps inside a nested block and keeps the outer block’s other fields', () => {
    const script = scriptOf([loop('outer', [step('o0'), loop('inner', [step('i0'), step('i1')])])])
    const edit = wrapInTransaction(script, target(script, ['action:0#loop[1]/loop[0]']), 'Inner', 'span')
    const inner = edit.script.actions[0].control?.steps?.[1]
    expect(names(inner?.control?.steps)).toEqual(['Start Inner transaction', 'i0', 'End Inner transaction', 'i1'])
    expect(edit.script.actions[0].control?.count).toBe(2)
    expect(edit.remap({ phase: 'action', actionIndex: 0, stepPath: ['loop[1]', 'loop[1]'] })).toEqual({ phase: 'action', actionIndex: 0, stepPath: ['loop[1]', 'loop[3]'] })
    expect(edit.remap({ phase: 'action', actionIndex: 0, stepPath: ['loop[0]'] })).toEqual({ phase: 'action', actionIndex: 0, stepPath: ['loop[0]'] })
    // The input script is untouched.
    expect(names(script.actions[0].control?.steps?.[1].control?.steps)).toEqual(['i0', 'i1'])
  })

  it('moves steps inside a wrapped container with it', () => {
    const script = scriptOf([step('a'), loop('l', [step('x')])])
    const edit = wrapInTransaction(script, target(script, ['action:1']), 'Loop', 'span')
    expect(edit.remap({ phase: 'action', actionIndex: 1, stepPath: ['loop[0]'] })).toEqual({ phase: 'action', actionIndex: 2, stepPath: ['loop[0]'] })
  })

  it('changes nothing for an empty name', () => {
    const script = scriptOf([step('a')])
    expect(wrapInTransaction(script, target(script, ['action:0']), '  ', 'span').script).toBe(script)
  })
})

describe('spanCrossing', () => {
  it('allows nested and complete transactions and names the one a span cuts', () => {
    const items = [marker('start', 'A'), marker('start', 'B'), step('x'), marker('end', 'B'), marker('end', 'A')] as unknown as ScriptActionDefinition[]
    expect(spanCrossing(items, 0, 4)).toBeNull()
    expect(spanCrossing(items, 1, 3)).toBeNull()
    expect(spanCrossing(items, 1, 4)).toBe('A')
    expect(spanCrossing(items, 0, 2)).toBe('A')
  })
})

describe('renameTransaction and removeTransaction', () => {
  const calls = (actions: ScriptActionDefinition[] | undefined) => (actions || []).map(action => { const params = action.params as Raw | undefined; return params?.function ? `${params.function}(${(params.args as unknown[]).join(', ')})` : action.name })
  const measured = () => scriptOf([
    marker('start', 'Login'),
    marker('start', 'Login||submit', true), step('submit'), marker('end', 'Login||submit', true),
    step('home'),
    marker('end', 'Login'),
    step('after'),
  ])

  it('renames a marker pair and the sub-transactions named under it', () => {
    const script = measured()
    const next = renameTransaction(script, groupNamed(modelOf(script), 'Login'), 'Sign in')
    expect(next.actions.map(action => (action.params as Raw | undefined)?.args)).toEqual([
      ['Sign in'], ['Sign in||submit', 'Sign in'], undefined, ['Sign in||submit', 'LR_AUTO'], undefined, ['Sign in', 'LR_AUTO'], undefined,
    ])
    expect(next.actions[0].name).toBe('Start Sign in transaction')
  })

  it('renames the sub-transactions inside blocks, and their parent argument', () => {
    const script = scriptOf([
      marker('start', 'P'),
      loop('l', [step('x'), marker('start', 'P||s', true), step('y'), marker('end', 'P||s', true)]),
      marker('end', 'P'),
    ])
    const next = renameTransaction(script, groupNamed(modelOf(script), 'P'), 'Q')
    expect(next.actions[1].control?.steps?.map(action => (action.params as Raw | undefined)?.args)).toEqual([undefined, ['Q||s', 'Q'], undefined, ['Q||s', 'LR_AUTO']])
    expect(script.actions[1].control?.steps?.[1].params).toEqual({ function: 'lr_start_sub_transaction', args: ['P||s', 'P'] })
  })

  it('follows sub-transactions tied to the parent by argument only, ignoring case', () => {
    const tied = (name: string, parent: string): Raw => ({ name: `Start ${name}`, type: 'source_transaction', params: { function: 'lr_start_sub_transaction', args: [name, parent] } })
    const script = scriptOf([
      marker('start', 'Login'),
      tied('Submit', 'login'), step('submit'), marker('end', 'Submit', true),
      tied('login||Other', 'Login'), step('other'), marker('end', 'login||Other', true),
      marker('end', 'Login'),
    ])
    const group = groupNamed(modelOf(script), 'Login')
    const renamed = renameTransaction(script, group, 'Sign in')
    expect(renamed.actions.map(action => (action.params as Raw | undefined)?.args)).toEqual([
      ['Sign in'], ['Submit', 'Sign in'], undefined, ['Submit', 'LR_AUTO'], ['Sign in||Other', 'Sign in'], undefined, ['Sign in||Other', 'LR_AUTO'], ['Sign in', 'LR_AUTO'],
    ])
    // Removing Login keeps both measurements, as transactions of their own.
    const removal = removeTransaction(script, group)
    expect(calls(removal.script.actions)).toEqual([
      'lr_start_transaction(Submit)', 'submit', 'lr_end_transaction(Submit, LR_AUTO)',
      'lr_start_transaction(Other)', 'other', 'lr_end_transaction(Other, LR_AUTO)',
    ])
    expect([removal.movedUp, removal.promoted]).toEqual([2, 2])
  })

  it('follows sub-transactions of sub-transactions, and renames a sub-transaction’s own', () => {
    const tied = (name: string, parent: string): Raw => ({ name: `Start ${name}`, type: 'source_transaction', params: { function: 'lr_start_sub_transaction', args: [name, parent] } })
    const script = scriptOf([
      marker('start', 'Login'),
      tied('Login||a', 'Login'), tied('Login||a||b', 'Login||a'), tied('Deep', 'Login||a||b'), step('x'),
      marker('end', 'Deep', true), marker('end', 'Login||a||b', true), marker('end', 'Login||a', true),
      marker('end', 'Login'),
    ])
    const model = modelOf(script)
    const args = (next: ScriptDefinition) => next.actions.map(action => (action.params as Raw | undefined)?.args)
    expect(args(renameTransaction(script, groupNamed(model, 'Login'), 'Sign in'))).toEqual([
      ['Sign in'], ['Sign in||a', 'Sign in'], ['Sign in||a||b', 'Sign in||a'], ['Deep', 'Sign in||a||b'], undefined,
      ['Deep', 'LR_AUTO'], ['Sign in||a||b', 'LR_AUTO'], ['Sign in||a', 'LR_AUTO'], ['Sign in', 'LR_AUTO'],
    ])
    // Each moves up a level: a becomes a transaction, a||b its sub-transaction.
    const removal = removeTransaction(script, groupNamed(model, 'Login'))
    expect(calls(removal.script.actions)).toEqual([
      'lr_start_transaction(a)', 'lr_start_sub_transaction(a||b, a)', 'lr_start_sub_transaction(Deep, a||b)', 'x',
      'lr_end_sub_transaction(Deep, LR_AUTO)', 'lr_end_sub_transaction(a||b, LR_AUTO)', 'lr_end_transaction(a, LR_AUTO)',
    ])
    expect([removal.movedUp, removal.promoted]).toEqual([3, 1])
    // Removing the sub-transaction a moves its own up under Login.
    const subRemoval = removeTransaction(script, groupNamed(model, 'Login||a'))
    expect(calls(subRemoval.script.actions)).toEqual([
      'lr_start_transaction(Login)', 'lr_start_sub_transaction(Login||b, Login)', 'lr_start_sub_transaction(Deep, Login||b)', 'x',
      'lr_end_sub_transaction(Deep, LR_AUTO)', 'lr_end_sub_transaction(Login||b, LR_AUTO)', 'lr_end_transaction(Login, LR_AUTO)',
    ])
  })

  it('keeps another transaction’s sub-transaction that shares a name', () => {
    const tied = (name: string, parent: string): Raw => ({ name: `Start ${name}`, type: 'source_transaction', params: { function: 'lr_start_sub_transaction', args: [name, parent] } })
    const script = scriptOf([
      marker('start', 'Login'),
      tied('Step', 'Login'), step('a'), marker('end', 'Step', true),
      marker('start', 'Inner'), tied('Step', 'Inner'), step('b'), marker('end', 'Step', true), marker('end', 'Inner'),
      marker('end', 'Login'),
    ])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'Login'))
    expect(calls(removal.script.actions)).toEqual([
      'lr_start_transaction(Step)', 'a', 'lr_end_transaction(Step, LR_AUTO)',
      'lr_start_transaction(Inner)', 'lr_start_sub_transaction(Step, Inner)', 'b', 'lr_end_sub_transaction(Step, LR_AUTO)', 'lr_end_transaction(Inner, LR_AUTO)',
    ])
    expect([removal.movedUp, removal.promoted]).toEqual([1, 1])
  })

  it('renames a parent named in call_args too', () => {
    const start = { ...marker('start', 'P||s', true), params: { function: 'lr_start_sub_transaction', args: ['P||s', 'P'], call_args: [{ value: 'P||s', raw: '"P||s"' }, { value: 'P', raw: '"P"' }] } }
    const script = scriptOf([marker('start', 'P'), start, step('x'), marker('end', 'P||s', true), marker('end', 'P')])
    const params = renameTransaction(script, groupNamed(modelOf(script), 'P'), 'Q').actions[1].params as Raw
    expect(params.call_args).toEqual([{ value: 'Q||s', raw: '"Q||s"' }, { value: 'Q', raw: '"Q"' }])
  })

  it('renames a tagged run by re-tagging its steps', () => {
    const script = scriptOf([step('a', { metadata: { source_transaction: 'T' } }), step('b', { metadata: { source_transaction: 'T' } }), step('c')])
    const next = renameTransaction(script, groupNamed(modelOf(script), 'T'), 'U')
    expect(next.actions.map(action => action.metadata?.source_transaction)).toEqual(['U', 'U', undefined])
  })

  it('removes only the wrapper: its sub-transactions become transactions and its tags go', () => {
    const tagged = (name: string) => step(name, { metadata: { source_transaction: 'Login', keep: 1 } })
    const script = scriptOf([
      marker('start', 'Login'),
      marker('start', 'Login||submit', true), tagged('submit'), marker('end', 'Login||submit', true),
      tagged('home'),
      marker('end', 'Login'),
      step('after'),
    ])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'Login'))
    expect(calls(removal.script.actions)).toEqual(['lr_start_transaction(submit)', 'submit', 'lr_end_transaction(submit, LR_AUTO)', 'home', 'after'])
    expect(names(removal.script.actions)).toEqual(['Start submit transaction', 'submit', 'End submit transaction', 'home', 'after'])
    expect(removal.script.actions.map(action => action.metadata)).toEqual([undefined, { keep: 1 }, undefined, { keep: 1 }, undefined])
    expect([removal.movedUp, removal.promoted]).toEqual([1, 1])
    expect(removal.focus).toEqual({ phase: 'action', actionIndex: 0 })
    expect(removal.remap({ phase: 'action', actionIndex: 2 })).toEqual({ phase: 'action', actionIndex: 1 })
    expect(removal.remap({ phase: 'action', actionIndex: 6 })).toEqual({ phase: 'action', actionIndex: 4 })
    expect(removal.remap({ phase: 'action', actionIndex: 0 })).toBeNull()
    // The outline now shows the measurement as its own transaction, and no Login.
    const after = modelOf(removal.script)
    expect([...after.groupsByKey.values()].map(group => `${group.kind}:${group.name}`)).toEqual(['transaction:submit'])
  })

  it('keeps sub-transactions inside blocks, and the block’s steps in place', () => {
    const script = scriptOf([
      marker('start', 'P'),
      loop('l', [step('x'), marker('start', 'P||s', true), step('y'), marker('end', 'P||s', true), step('z')]),
      marker('end', 'P'),
    ])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'P'))
    expect([removal.movedUp, removal.promoted]).toEqual([1, 1])
    expect(names(removal.script.actions)).toEqual(['l'])
    expect(calls(removal.script.actions[0].control?.steps)).toEqual(['x', 'lr_start_transaction(s)', 'y', 'lr_end_transaction(s, LR_AUTO)', 'z'])
    expect(removal.remap({ phase: 'action', actionIndex: 1, stepPath: ['loop[4]'] })).toEqual({ phase: 'action', actionIndex: 0, stepPath: ['loop[4]'] })
  })

  const tiedStart = (name: string, parent: string): Raw => ({ name: `Start ${name}`, type: 'source_transaction', params: { function: 'lr_start_sub_transaction', args: [name, parent] } })
  const branchEnds = (end: () => Raw, then: Raw[] = []): Raw => ({ name: 'status', type: 'control_if', control: { condition: 'ok', steps: [end(), ...then], else: [end()] } })

  it('removes a transaction that ends in an if/else, and the if with nothing else in it', () => {
    const script = scriptOf([marker('start', 'T'), step('req'), branchEnds(() => marker('end', 'T')), step('after')])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'T'))
    expect(removal.problem).toBe('')
    expect(names(removal.script.actions)).toEqual(['req', 'after'])
    expect(removal.remap({ phase: 'action', actionIndex: 2 })).toBeNull()
    expect(removal.remap({ phase: 'action', actionIndex: 2, stepPath: ['then[0]'] })).toBeNull()
    expect(removal.remap({ phase: 'action', actionIndex: 3 })).toEqual({ phase: 'action', actionIndex: 1 })
  })

  it('keeps an if that still has steps, and follows them', () => {
    const script = scriptOf([marker('start', 'T'), step('req'), branchEnds(() => marker('end', 'T'), [step('x')])])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'T'))
    expect(names(removal.script.actions)).toEqual(['req', 'status'])
    expect(names(removal.script.actions[1].control?.steps)).toEqual(['x'])
    expect(removal.script.actions[1].control?.else).toEqual([])
    expect(removal.remap({ phase: 'action', actionIndex: 2, stepPath: ['then[1]'] })).toEqual({ phase: 'action', actionIndex: 1, stepPath: ['then[0]'] })
  })

  it('refuses to leave a switch case with no steps', () => {
    const script = scriptOf([marker('start', 'T'), step('req'), { name: 'sw', type: 'control_switch', control: { cases: [{ value: '1', steps: [marker('end', 'T')] }], default: [step('d')] } }])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'T'))
    expect(removal.problem).toContain('switch case')
    expect(removal.script).toBe(script)
  })

  it('rewrites a sub-transaction’s ends inside a block with its start outside', () => {
    const script = scriptOf([
      marker('start', 'T'), tiedStart('T||A', 'T'), step('req'), branchEnds(() => marker('end', 'T||A', true)), marker('end', 'T'),
    ])
    const group = groupNamed(modelOf(script), 'T')
    const removal = removeTransaction(script, group)
    expect(calls(removal.script.actions)).toEqual(['lr_start_transaction(A)', 'req', 'status'])
    expect(calls(removal.script.actions[2].control?.steps)).toEqual(['lr_end_transaction(A, LR_AUTO)'])
    expect(calls(removal.script.actions[2].control?.else)).toEqual(['lr_end_transaction(A, LR_AUTO)'])
    const renamed = renameTransaction(script, group, 'R')
    expect(calls(renamed.actions[3].control?.steps)).toEqual(['lr_end_sub_transaction(R||A, LR_AUTO)'])
  })

  it('ends each sub-transaction as its own start was moved, whatever their names', () => {
    const script = scriptOf([
      marker('start', 'T'),
      tiedStart('Step', 'T'), step('a'), marker('end', 'Step', true),
      tiedStart('T||a', 'T'), tiedStart('Step', 'T||a'), step('b'), marker('end', 'Step', true), marker('end', 'T||a', true),
      marker('end', 'T'),
    ])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'T'))
    expect(calls(removal.script.actions)).toEqual([
      'lr_start_transaction(Step)', 'a', 'lr_end_transaction(Step, LR_AUTO)',
      'lr_start_transaction(a)', 'lr_start_sub_transaction(Step, a)', 'b', 'lr_end_sub_transaction(Step, LR_AUTO)', 'lr_end_transaction(a, LR_AUTO)',
    ])
    expect([removal.movedUp, removal.promoted]).toEqual([3, 2])
  })

  it('treats lr_end_sub_trans as a sub-transaction end', () => {
    const script = scriptOf([
      marker('start', 'T'), tiedStart('T||A', 'T'), step('req'),
      { name: 'End A', type: 'source_transaction', params: { function: 'lr_end_sub_trans', args: ['T||A', 'LR_AUTO'] } },
      marker('end', 'T'),
    ])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'T'))
    expect(calls(removal.script.actions)).toEqual(['lr_start_transaction(A)', 'req', 'lr_end_transaction(A, LR_AUTO)'])
  })

  it('re-tags a sequence’s requests with the transaction', () => {
    const sequence = { name: 'seq', type: 'http_sequence', metadata: { source_transaction: 'T' }, params: { requests: [step('r1', { metadata: { source_transaction: 'T' } }), step('r2')] } }
    const script = scriptOf([marker('start', 'T'), sequence, marker('end', 'T')])
    const group = groupNamed(modelOf(script), 'T')
    const requests = (next: ScriptDefinition, index: number) => ((next.actions[index].params as Raw).requests as ScriptActionDefinition[]).map(request => request.metadata?.source_transaction)
    expect(requests(renameTransaction(script, group, 'U'), 1)).toEqual(['U', undefined])
    const removal = removeTransaction(script, group)
    expect(removal.script.actions[0].metadata).toBeUndefined()
    expect(requests(removal.script, 0)).toEqual([undefined, undefined])
  })

  it('stops a transaction that ends in an if/else at the next use of its name', () => {
    const script = scriptOf([
      marker('start', 'T'), step('a'), branchEnds(() => marker('end', 'T')), step('b'),
      marker('start', 'T'), step('c'), branchEnds(() => marker('end', 'T')),
    ])
    const first = [...modelOf(script).groupsByKey.values()].find(group => group.name === 'T')!
    const removal = removeTransaction(script, first)
    expect(calls(removal.script.actions)).toEqual(['a', 'b', 'lr_start_transaction(T)', 'c', 'status'])
    expect(calls(renameTransaction(script, first, 'U').actions)).toEqual(['lr_start_transaction(U)', 'a', 'status', 'b', 'lr_start_transaction(T)', 'c', 'status'])
  })

  it('keeps the think time an end marker carries, on the step before it', () => {
    const think = { type: 'fixed', mean_ms: 5000 }
    const script = scriptOf([marker('start', 'T'), step('a'), { ...marker('end', 'T'), think_time: think, think_time_after: true }, step('b')])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'T'))
    expect(names(removal.script.actions)).toEqual(['a', 'b'])
    expect(removal.script.actions[0]).toMatchObject({ think_time: think, think_time_after: true })
    // The step before it already thinks: nothing is lost silently.
    const crowded = scriptOf([marker('start', 'T'), step('a', { think_time: think, think_time_after: true }), { ...marker('end', 'T'), think_time: think, think_time_after: true }])
    const refused = removeTransaction(crowded, groupNamed(modelOf(crowded), 'T'))
    expect(refused.problem).toContain('think time')
    expect(refused.script).toBe(crowded)
  })

  it('moves think time out of an if it drops', () => {
    const think = { type: 'fixed', mean_ms: 1000 }
    const end = () => ({ ...marker('end', 'T'), think_time: think, think_time_after: true })
    const script = scriptOf([marker('start', 'T'), step('a'), branchEnds(end)])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'T'))
    expect(names(removal.script.actions)).toEqual(['a'])
    expect(removal.script.actions[0]).toMatchObject({ think_time: think, think_time_after: true })
  })

  it('does not match a later sub-transaction end to one an if/else already closed', () => {
    const script = scriptOf([
      marker('start', 'T'), tiedStart('Step', 'T'), step('x'), branchEnds(() => marker('end', 'Step', true)),
      marker('start', 'Other'), tiedStart('Step', 'Other'), step('y'), marker('end', 'Step', true), marker('end', 'Other'),
      marker('end', 'T'),
    ])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'T'))
    expect(calls(removal.script.actions)).toEqual([
      'lr_start_transaction(Step)', 'x', 'status',
      'lr_start_transaction(Other)', 'lr_start_sub_transaction(Step, Other)', 'y', 'lr_end_sub_transaction(Step, LR_AUTO)', 'lr_end_transaction(Other, LR_AUTO)',
    ])
  })

  it('lets a switch keep an empty default', () => {
    const script = scriptOf([marker('start', 'T'), step('req'), { name: 'sw', type: 'control_switch', control: { cases: [{ value: '1', steps: [step('one')] }], default: [marker('end', 'T')] } }])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'T'))
    expect(removal.problem).toBe('')
    expect(removal.script.actions[1].control?.default).toEqual([])
  })

  it('renames and removes a paused transaction’s stop and resume with it', () => {
    const call = (fn: string, args: string[]): Raw => ({ name: fn, type: 'source_transaction', params: { function: fn, args } })
    const script = scriptOf([marker('start', 'T'), step('a'), call('lr_stop_transaction', ['T']), step('think'), call('lr_resume_transaction', ['T']), step('b'), marker('end', 'T')])
    const group = groupNamed(modelOf(script), 'T')
    expect(group.stepCount).toBe(5)
    expect(calls(renameTransaction(script, group, 'U').actions)).toEqual(['lr_start_transaction(U)', 'a', 'lr_stop_transaction(U)', 'think', 'lr_resume_transaction(U)', 'b', 'lr_end_transaction(U, LR_AUTO)'])
    expect(calls(removeTransaction(script, group).script.actions)).toEqual(['a', 'think', 'b'])
  })

  it('re-tags a sequence’s requests in a tagged run too', () => {
    const sequence = { name: 'seq', type: 'http_sequence', metadata: { source_transaction: 'Run' }, params: { requests: [step('r1', { metadata: { source_transaction: 'Run' } })] } }
    const script = scriptOf([sequence, step('b', { metadata: { source_transaction: 'Run' } })])
    const group = groupNamed(modelOf(script), 'Run')
    const tagOf = (next: ScriptDefinition) => ((next.actions[0].params as Raw).requests as ScriptActionDefinition[])[0].metadata?.source_transaction
    expect(tagOf(renameTransaction(script, group, 'Walk'))).toBe('Walk')
    expect(tagOf(removeTransaction(script, group).script)).toBeUndefined()
  })

  it('moves a start marker’s think time to the step before the transaction', () => {
    const think = { type: 'fixed', mean_ms: 12000 }
    const script = scriptOf([step('home'), { ...marker('start', 'T'), think_time: think, think_time_after: true }, step('a'), marker('end', 'T')])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'T'))
    expect(removal.problem).toBe('')
    expect(names(removal.script.actions)).toEqual(['home', 'a'])
    expect(removal.script.actions[0]).toMatchObject({ think_time: think, think_time_after: true })
    // First in the phase: nowhere to put it.
    const first = scriptOf([{ ...marker('start', 'T'), think_time: think, think_time_after: true }, step('a'), marker('end', 'T')])
    expect(removeTransaction(first, groupNamed(modelOf(first), 'T')).problem).toContain('think time')
  })

  it('refuses to make a wait only one branch made run on every path', () => {
    const think = { type: 'fixed', mean_ms: 3000 }
    const script = scriptOf([marker('start', 'T'), step('a'), { name: 'status', type: 'control_if', control: { condition: 'ok', steps: [{ ...marker('end', 'T'), think_time: think, think_time_after: true }], else: [marker('end', 'T')] } }])
    expect(removeTransaction(script, groupNamed(modelOf(script), 'T')).problem).toContain('think time')
  })

  it('stops the span before a later block that starts the transaction again', () => {
    const script = scriptOf([
      marker('start', 'T'), step('a'), branchEnds(() => marker('end', 'T')), step('b'),
      { name: 'retry', type: 'control_if', control: { condition: 'again', steps: [marker('start', 'T'), step('c'), marker('end', 'T')] } },
    ])
    const first = [...modelOf(script).groupsByKey.values()].find(group => group.name === 'T')!
    const removal = removeTransaction(script, first)
    expect(names(removal.script.actions)).toEqual(['a', 'b', 'retry'])
    expect(calls(removal.script.actions[2].control?.steps)).toEqual(['lr_start_transaction(T)', 'c', 'lr_end_transaction(T, LR_AUTO)'])
    // The rename check covers the same span: a later transaction's name is free to use.
    const later = scriptOf([marker('start', 'T'), step('a'), branchEnds(() => marker('end', 'T')), marker('start', 'Later'), step('l'), marker('end', 'Later')])
    const group = [...modelOf(later).groupsByKey.values()].find(candidate => candidate.name === 'T')!
    expect(nestedNameConflict(later, groupSpan(later, group), 'Later', 'T')).toBe('')
  })

  it('ends the span at an if/else that ends it and then retries it', () => {
    const script = scriptOf([
      marker('start', 'T'), step('submit'),
      { name: 'outcome', type: 'control_if', control: { condition: 'fail', steps: [marker('end', 'T'), marker('start', 'T'), step('retry'), marker('end', 'T')], else: [marker('end', 'T')] } },
      step('after'),
    ])
    const group = [...modelOf(script).groupsByKey.values()].find(candidate => candidate.name === 'T')!
    const removal = removeTransaction(script, group)
    expect(names(removal.script.actions)).toEqual(['submit', 'outcome', 'after'])
    expect(names(removal.script.actions[1].control?.steps)).toEqual(['retry'])
    expect(calls(renameTransaction(script, group, 'U').actions[2].control?.steps)).toEqual([
      'lr_end_transaction(U, LR_AUTO)', 'lr_start_transaction(U)', 'retry', 'lr_end_transaction(U, LR_AUTO)',
    ])
  })

  it('stops the span before a later block that starts it again, when no if/else ended it first', () => {
    const script = scriptOf([
      marker('start', 'T'), step('a'),
      { name: 'only then', type: 'control_if', control: { condition: 'ok', steps: [marker('end', 'T')] } },
      step('b'),
      { name: 'retry', type: 'control_loop', control: { count: 2, steps: [marker('start', 'T'), step('c'), marker('end', 'T')] } },
    ])
    const group = [...modelOf(script).groupsByKey.values()].find(candidate => candidate.name === 'T')!
    expect(groupSpan(script, group).hi).toBe(3)
    const removal = removeTransaction(script, group)
    expect(names(removal.script.actions)).toEqual(['a', 'b', 'retry'])
    expect(calls(removal.script.actions[2].control?.steps)).toEqual(['lr_start_transaction(T)', 'c', 'lr_end_transaction(T, LR_AUTO)'])
  })

  it('does not name a transparent concurrent group as timing its requests', () => {
    const group = { name: 'Burst', type: 'http_sequence', metadata: { source_transaction: 'T', transparent_request_group: true }, params: { execution_mode: 'concurrent', requests: [step('r1')] } }
    const script = scriptOf([marker('start', 'T'), group, marker('end', 'T')])
    expect(removeTransaction(script, groupNamed(modelOf(script), 'T')).sequences).toEqual([])
  })

  it('renames a pause that spells the transaction in another case', () => {
    const call = (fn: string, args: string[]): Raw => ({ name: fn, type: 'source_transaction', params: { function: fn, args } })
    const script = scriptOf([marker('start', 'Login'), call('lr_stop_transaction', ['login']), step('a'), call('lr_resume_transaction', ['LOGIN']), marker('end', 'Login')])
    expect(calls(renameTransaction(script, groupNamed(modelOf(script), 'Login'), 'Auth').actions)).toEqual([
      'lr_start_transaction(Auth)', 'lr_stop_transaction(Auth)', 'a', 'lr_resume_transaction(Auth)', 'lr_end_transaction(Auth, LR_AUTO)',
    ])
  })

  it('does not count an if with no then list as closing a sub-transaction on every path', () => {
    const script = scriptOf([
      marker('start', 'T'), tiedStart('Step', 'T'), step('x'),
      { name: 'only else', type: 'control_if', control: { condition: 'c', else: [marker('end', 'Step', true)] } },
      marker('end', 'Step', true), marker('end', 'T'),
    ])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'T'))
    expect(calls(removal.script.actions)).toEqual(['lr_start_transaction(Step)', 'x', 'only else', 'lr_end_transaction(Step, LR_AUTO)'])
    expect(calls(removal.script.actions[2].control?.else)).toEqual(['lr_end_transaction(Step, LR_AUTO)'])
  })

  it('names the request groups that now time their own requests', () => {
    const sequence = { name: 'Checkout', type: 'http_sequence', metadata: { source_transaction: 'Checkout' }, params: { requests: [step('r1')] } }
    const script = scriptOf([marker('start', 'Checkout'), sequence, marker('end', 'Checkout')])
    expect(removeTransaction(script, groupNamed(modelOf(script), 'Checkout')).sequences).toEqual(['Checkout'])
  })

  it('re-tags the steps of a renamed marker transaction', () => {
    const script = scriptOf([marker('start', 'T'), step('a', { metadata: { source_transaction: 'T' } }), marker('end', 'T')])
    const next = renameTransaction(script, groupNamed(modelOf(script), 'T'), 'U')
    expect(next.actions[1].metadata?.source_transaction).toBe('U')
    expect([...modelOf(next).groupsByKey.values()].map(group => group.name)).toEqual(['U'])
  })

  it('clears a tagged run’s tags', () => {
    const script = scriptOf([step('a', { metadata: { source_transaction: 'T', keep: 1 } }), step('b', { metadata: { source_transaction: 'T' } })])
    const removal = removeTransaction(script, groupNamed(modelOf(script), 'T'))
    expect(removal.script.actions.map(action => action.metadata)).toEqual([{ keep: 1 }, undefined])
  })
})

describe('removeTopLevelRows', () => {
  it('deletes steps and whole transactions across phases', () => {
    const script = scriptOf([step('a'), marker('start', 'T'), step('b'), marker('end', 'T'), step('c')], [step('i')])
    const model = modelOf(script)
    const removal = removeTopLevelRows(script, model, [groupNamed(model, 'T').key, 'init:0'])
    expect(removal && names(removal.script.actions)).toEqual(['a', 'c'])
    expect(removal?.script.init).toEqual([])
    expect(removal?.removed).toBe(4)
    expect(removal?.remap({ phase: 'action', actionIndex: 4 })).toEqual({ phase: 'action', actionIndex: 1 })
    expect(removal?.remap({ phase: 'action', actionIndex: 2 })).toBeNull()
  })

  it('takes a measured step’s markers with it and declines nested steps', () => {
    const script = scriptOf([marker('start', 'P'), marker('start', 'P||s', true), step('s'), marker('end', 'P||s', true), marker('end', 'P')])
    const model = modelOf(script)
    const stepKey = [...model.stepsByKey.keys()][0]
    expect(names(removeTopLevelRows(script, model, [stepKey])?.script.actions)).toEqual(['Start P transaction', 'End P transaction'])
    expect(removeTopLevelRows(script, model, ['action:0#loop[0]'])).toBeNull()
  })
})

describe('nestedNameConflict', () => {
  it('refuses the name of a transaction open around or inside the span, not one before or after it', () => {
    const script = scriptOf([
      marker('start', 'Before'), step('b'), marker('end', 'Before'),
      marker('start', 'Outer'), step('a'), loop('l', [marker('start', 'Deep'), step('d'), marker('end', 'Deep')]), marker('end', 'Outer'),
    ])
    const model = modelOf(script)
    const inner = target(script, ['action:4'])
    expect(nestedNameConflict(script, inner, 'outer')).toContain('already open')
    expect(nestedNameConflict(script, inner, 'Before')).toBe('')
    expect(nestedNameConflict(script, target(script, ['action:5']), 'Deep')).toContain('already open')
    expect(nestedNameConflict(script, target(script, ['action:5#loop[1]']), 'Outer')).toContain('already open')
    expect(nestedNameConflict(script, target(script, ['action:5#loop[1]']), 'Deep')).toContain('already open')
    expect(nestedNameConflict(script, target(script, ['action:5#loop[1]']), 'Fresh')).toBe('')
    const outer = groupNamed(model, 'Outer')
    expect(nestedNameConflict(script, groupSpan(script, outer), 'Outer', 'Outer')).toBe('')
    expect(nestedNameConflict(script, groupSpan(script, outer), 'Deep', 'Outer')).toContain('already open')
  })
})

describe('names', () => {
  it('collects names at any depth and picks an unused default', () => {
    const script = scriptOf([marker('start', 'Transaction 1'), step('a', { metadata: { source_transaction: 'Tagged' } }), loop('l', [marker('start', 'Inner')]), marker('end', 'Transaction 1')])
    expect([...scriptTransactionNames(script)].sort()).toEqual(['inner', 'tagged', 'transaction 1'])
    expect(defaultTransactionName(script)).toBe('Transaction 2')
  })

  it('reports names the engine and reports cannot use', () => {
    expect(transactionNameProblem(' ')).toBe('Enter a name.')
    expect(transactionNameProblem('a||b')).toContain('||')
    expect(transactionNameProblem('x'.repeat(257))).toContain('256')
    expect(transactionNameProblem('Login')).toBe('')
  })
})

describe('remapLocationKey', () => {
  it('keeps each key form', () => {
    const script = scriptOf([step('a'), loop('l', [step('x'), step('y')])])
    const top = wrapInTransaction(script, target(script, ['action:0']), 'T', 'span').remap
    expect(remapLocationKey(debugSequenceKey('action', 1), top)).toBe('action:3')
    expect(remapLocationKey('action:1|loop[0]', top)).toBe('action:3|loop[0]')
    expect(remapLocationKey('action:1#loop[1]', top)).toBe('action:3#loop[1]')
    expect(remapLocationKey('transaction:action:T#1', top)).toBe('transaction:action:T#1')
    const inner = wrapInTransaction(script, target(script, ['action:1#loop[0]']), 'L', 'span').remap
    expect(remapLocationKey('action:1#loop[1]', inner)).toBe('action:1#loop[3]')
  })
})
