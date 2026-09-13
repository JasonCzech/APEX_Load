import { describe, expect, it } from 'vitest'
import { actionBreadcrumb, changedDocumentLines, dedupeAuthorIssues, insertAuthorAction } from './authorExperience'
import { bodyLanguage, bodyTemplateReferences, extractedBodyReferences } from './actionBodyModel'
import { EMPTY_SCRIPT } from './scriptEditorUtils'
import type { ScriptDefinition } from '../types/scripts'

const script: ScriptDefinition = { ...EMPTY_SCRIPT, name: 'Checkout', actions: [
  { name: 'Poll', type: 'control_loop', target: '', control: { count: 2, steps: [{ name: 'Read', type: 'GET', target: '/read' }] } },
  { name: 'Finish', type: 'GET', target: '/finish' },
] }
const added = { name: 'Health', type: 'GET', target: '/health' }

describe('author insertion and navigation', () => {
  it.each(['before', 'after'] as const)('inserts %s a nested step without changing its siblings or loop contract', position => {
    const result = insertAuthorAction(script, added, 'action', position, { phase: 'action', actionIndex: 0, stepPath: ['loop[0]'] })
    expect(result.script.actions[0].control?.steps?.map(a => a.name)).toEqual(position === 'before' ? ['Health', 'Read'] : ['Read', 'Health'])
    expect(result.script.actions[0].control?.count).toBe(2)
    expect(result.script.actions[1]).toEqual(script.actions[1])
    expect(script.actions[0].control?.steps).toHaveLength(1)
    expect(actionBreadcrumb(result.script, result.focus)).toBe('Actions › Poll › Health')
  })
  it('inserts into empty phases and inside a loop, rejecting stale anchors', () => {
    const init = insertAuthorAction(script, added, 'init', 'end')
    expect(init.script.init).toEqual([added])
    expect(init.focus).toMatchObject({ phase: 'init', actionIndex: 0 })
    expect(insertAuthorAction(script, added, 'action', 'inside', { phase: 'action', actionIndex: 0 }).script.actions[0].control?.steps).toHaveLength(2)
    expect(() => insertAuthorAction(script, added, 'action', 'after', { phase: 'action', actionIndex: 9 })).toThrow('Select an action')
    expect(() => insertAuthorAction(script, added, 'action', 'inside', { phase: 'action', actionIndex: 1 })).toThrow('Select a loop')
  })
  it('preserves switch branch conditions when inserting after a branch child', () => {
    const branchScript = { ...script, actions: [{ name: 'Switch', type: 'control_switch', target: '', control: { cases: [{ condition: { op: 'exists', left: '{{var.ready}}' }, steps: [added] }] } }] }
    const result = insertAuthorAction(branchScript, { ...added, name: 'After' }, 'action', 'after', { phase: 'action', actionIndex: 0, stepPath: ['case[0][0]'] })
    expect(result.script.actions[0].control?.cases?.[0].steps).toHaveLength(2)
    expect(result.script.actions[0].control?.cases?.[0].condition).toEqual(branchScript.actions[0].control.cases[0].condition)
  })
})

describe('author diagnostics and body model', () => {
  it('deduplicates matching operation errors while preserving different severities', () => {
    expect(dedupeAuthorIssues([{ id: 'a', title: 'Missing name', severity: 'error' }, { id: 'b', title: 'Validation failed: Missing name', severity: 'error' }, { id: 'c', title: 'Missing name', severity: 'warning' }])).toHaveLength(2)
  })
  it('shows the exact changed region including deletions and empty documents', () => {
    expect(changedDocumentLines('a\nb\nc', 'a\nc')).toEqual({ line: 2, removed: 'b', added: '' })
    expect(changedDocumentLines('', 'new').added).toBe('new')
    expect(changedDocumentLines('same', 'same')).toEqual({ line: 2, removed: '', added: '' })
  })
  it('distinguishes syntax from wire encoding and lists references without resolving secrets', () => {
    expect(bodyLanguage({ ...added, body: '<order/>', headers: { 'Content-Type': 'text/xml' } })).toBe('xml')
    expect(bodyLanguage({ ...added, body: '{"id":1}' })).toBe('json')
    expect(bodyLanguage({ ...added, body: '{"id":1}', body_encoding: 'base64' })).toBe('text')
    expect(bodyTemplateReferences('{{param.id}} {{secret.token}} {{param.id}}')).toEqual(['param.id', 'secret.token'])
  })
})

it('retains identical errors on different actions', () => {
  expect(dedupeAuthorIssues([{ id: 'a', source: 'Actions › First', severity: 'error', title: 'Missing target' }, { id: 'b', source: 'Actions › Second', severity: 'error', title: 'Missing target' }])).toHaveLength(2)
})

it('remaps root and nested breakpoints on insertion without moving other branches', async () => {
  const { remapInsertedBreakpoints } = await import('./authorExperience')
  const breakpoints = [{ phase: 'action', action_index: 0, step_path: ['loop[0]'], condition: 'var.ready == true' }, { phase: 'action', action_index: 0, step_path: ['else[0]'] }, { phase: 'teardown', action_index: 0 }]
  expect(remapInsertedBreakpoints(breakpoints, { phase: 'action', actionIndex: 0, stepPath: ['loop[0]'] })).toEqual([{ ...breakpoints[0], step_path: ['loop[1]'] }, breakpoints[1], breakpoints[2]])
  expect(remapInsertedBreakpoints(breakpoints, { phase: 'action', actionIndex: 0 })[0].action_index).toBe(1)
  expect(breakpoints[0].step_path).toEqual(['loop[0]'])
})

it('offers extracted variables from control branches and request sequences without exposing captured values', () => {
  const extracted = { ...added, extractors: [{ name: 'order_id', type: 'jsonpath', source: 'body', expression: '$.id' }] }
  const nested = { ...added, type: 'control_switch', control: { cases: [{ condition: { op: 'exists', left: '{{var.ready}}' }, steps: [extracted] }], default: [{ ...added, type: 'http_sequence', params: { requests: [extracted, { ...added, extractors: [{ name: 'token', type: 'header', source: 'header', expression: 'Authorization' }] }] } }] } }
  expect(extractedBodyReferences([nested])).toEqual(['var.order_id', 'var.token'])
  expect(extractedBodyReferences([])).toEqual([])
})
