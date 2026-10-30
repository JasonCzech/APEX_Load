import { describe, expect, it } from 'vitest'
import { captureDebugTransactionDefinitions, debugTransactionRows, debugTransactionTimingBreakdown } from './debugTransactionModel'
import type { DebugCompletionSnapshot } from './debugCompletionModel'
import type { BrowserReplayEvent } from '../types/replay'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'

const marker = (name: string, end = false, sub = false): ScriptActionDefinition => ({ name: `${end ? 'End' : 'Start'} ${name}`, type: 'source_transaction', target: '', params: { function: `lr_${end ? 'end' : 'start'}_${sub ? 'sub_' : ''}transaction`, args: end ? [name, 'LR_AUTO'] : sub ? [name, 'Parent'] : [name] } })
const request = (name: string): ScriptActionDefinition => ({ name, type: 'request', method: 'GET', target: '/private-target', body: 'private-request-body' })
const script = (actions: ScriptActionDefinition[], extra: Partial<ScriptDefinition> = {}): ScriptDefinition => ({ name: 'Transactions', version: '1.0', protocol: 'http', config: { base_url: '' }, actions, ...extra })
const event = (action_index: number, start: number, end: number, extra: Partial<BrowserReplayEvent> = {}): BrowserReplayEvent => ({ event_id: `event-${action_index}-${start}`, event_index: action_index, phase: 'action', action_index, action_type: 'request', action_name: 'step', target: '/', success: true, started_at: new Date(start).toISOString(), completed_at: new Date(end).toISOString(), duration_ms: end - start, ...extra })
const snapshot = (source: ScriptDefinition, events: BrowserReplayEvent[], overrides: Partial<DebugCompletionSnapshot['session']> = {}): DebugCompletionSnapshot => ({ key: 'run', scriptName: source.name, draft: true, transactions: captureDebugTransactionDefinitions(source), report: { group: 'run', protocol: 'http', success: true, started_at: new Date(0).toISOString(), completed_at: new Date(100).toISOString(), events }, session: { id: 'run', protocol: 'http', status: 'completed', group: 'run', log_level: 'info', current_event_index: 0, ...overrides } })

describe('debug transaction breakdown', () => {
  it('captures marker hierarchies, subtransactions, sequences and phases without retaining request data', () => {
    const source = script([marker('Checkout'), marker('Cart', false, true), request('Open cart'), marker('Cart', true, true), { name: 'Pay', type: 'http_sequence', target: '', params: { requests: [{ name: 'Authorize', method: 'POST', target: '/authorize' }, { name: 'Capture', method: 'POST', target: '/capture' }] } }, marker('Checkout', true)], { init: [marker('Sign in'), request('Login'), marker('Sign in', true)] })
    const definitions = captureDebugTransactionDefinitions(source)
    expect(definitions.map(d => [d.name, d.phase])).toEqual([['Sign in', 'init'], ['Checkout', 'action']])
    expect(definitions[1].children.map(d => [d.name, d.kind])).toEqual([['Cart', 'subtransaction'], ['Pay', 'transaction']])
    expect(definitions[1].children[1].children.map(d => d.name)).toEqual(['Authorize', 'Capture'])
    expect(JSON.stringify(definitions)).not.toContain('private-request-body')
    expect(JSON.stringify(definitions)).not.toContain('private-target')
  })
  it('uses transaction boundaries and the recorded end outcome, including explicit pass over a failed request', () => {
    const source = script([marker('Checkout'), request('Cart'), marker('Checkout', true)])
    const run = snapshot(source, [event(0, 0, 0, { action_type: 'source_transaction' }), event(1, 10, 30, { success: false }), event(2, 100, 100, { action_type: 'source_transaction' })])
    const [row] = debugTransactionRows(run)
    expect(row).toMatchObject({ name: 'Checkout', status: 'Passed', durationMs: 100, executions: 1 })
    expect(row.children[0]).toMatchObject({ name: 'Cart', status: 'Failed', durationMs: 20, eventIndex: 1 })
    run.report!.events[2].success = false
    expect(debugTransactionRows(run)[0].status).toBe('Failed')
  })
  it('keeps overlapping transactions independent and retains their matching completion evidence', () => {
    const source = script([marker('First'), request('First page'), marker('Second'), request('Shared page'), marker('First', true), request('Second page'), marker('Second', true)])
    const rows = debugTransactionRows(snapshot(source, [event(0, 0, 0), event(1, 10, 20), event(2, 30, 30), event(3, 40, 50), event(4, 60, 60), event(5, 70, 80), event(6, 90, 90)]))
    expect(rows.map(row => [row.name, row.status, row.durationMs, row.eventIndex])).toEqual([['First', 'Passed', 60, 4], ['Second', 'Passed', 60, 6]])
    expect(rows[1].children.map(row => row.name)).toEqual(['Shared page', 'Second page'])
  })
  it.each([false, true])('uses the declared parent for overlapping sibling subtransactions (earlier end first: %s)', earlierEndFirst => {
    const endings = earlierEndFirst ? [marker('One', true, true), marker('Two', true, true)] : [marker('Two', true, true), marker('One', true, true)]
    const source = script([marker('Parent'), marker('One', false, true), marker('Two', false, true), ...endings, marker('Parent', true)])
    const [row] = debugTransactionRows(snapshot(source, [event(0, 0, 0), event(1, 10, 10), event(2, 20, 20), event(3, 60, 60), event(4, 80, 80), event(5, 100, 100)]))
    expect(row.children.map(child => child.name)).toEqual(['One', 'Two'])
    expect(row.children.every(child => child.children.length === 0 && child.status === 'Passed')).toBe(true)
    expect(debugTransactionTimingBreakdown(row)).toEqual({ childSpanTotalMs: 110, overlapMs: 40, outsideChildrenMs: 30, parentElapsedMs: 100 })
  })
  it('keeps an explicitly nested subtransaction under its declared parent', () => {
    const inner = { ...marker('Inner', false, true), params: { function: 'lr_start_sub_transaction', args: ['Inner', 'Outer'] } }
    const source = script([marker('Parent'), marker('Outer', false, true), inner, marker('Inner', true, true), marker('Outer', true, true), marker('Parent', true)])
    const [row] = debugTransactionRows(snapshot(source, [event(0, 0, 0), event(1, 10, 10), event(2, 20, 20), event(3, 60, 60), event(4, 80, 80), event(5, 100, 100)]))
    expect(row.children.map(child => child.name)).toEqual(['Outer'])
    expect(row.children[0].children.map(child => child.name)).toEqual(['Inner'])
    expect(debugTransactionTimingBreakdown(row)).toEqual({ childSpanTotalMs: 70, overlapMs: 0, outsideChildrenMs: 30, parentElapsedMs: 100 })
  })
  it('marks missing completion and unexecuted transactions without inventing end times', () => {
    const source = script([marker('Open'), request('Page'), marker('Open', true), marker('Later'), request('Later page'), marker('Later', true)])
    const run = snapshot(source, [event(0, 0, 0), event(1, 10, 30)], { status: 'stopped' })
    expect(debugTransactionRows(run)).toMatchObject([{ status: 'Incomplete', completedAt: null, durationMs: null, eventIndex: 0 }, { name: 'Later', status: 'Not run', startedAt: null }])
    run.report!.dropped_event_count = 1
    expect(debugTransactionRows(run)[1].status).toBe('Not observed')
  })
  it('keeps repeated executions and nested-loop requests separate using static paths and iteration identity', () => {
    const source = script([{ name: 'Repeat', type: 'control_loop', target: '', control: { count: 2, steps: [marker('Visit'), request('Page'), marker('Visit', true)] } }])
    const events = [1, 2].flatMap((iteration, i) => [event(0, i * 100, i * 100, { event_id: `${iteration}-start`, event_index: i * 3, step_path: ['loop[0]'], iteration_path: [iteration] }), event(0, i * 100 + 10, i * 100 + 40, { event_id: `${iteration}-page`, event_index: i * 3 + 1, step_path: ['loop[1]'], iteration_path: [iteration], success: iteration === 1 }), event(0, i * 100 + 50, i * 100 + 50, { event_id: `${iteration}-end`, event_index: i * 3 + 2, step_path: ['loop[2]'], iteration_path: [iteration], success: iteration === 1 })])
    const [loop] = debugTransactionRows(snapshot(source, events))
    const transaction = loop.children[0]
    expect(transaction).toMatchObject({ name: 'Visit', executions: 2, status: 'Failed', durationMs: 150 })
    expect(transaction.children.map(row => [row.name, row.durationMs, row.status])).toEqual([['Execution 1', 50, 'Passed'], ['Execution 2', 50, 'Failed']])
    expect(transaction.children[0].children[0].eventIndex).toBe(1)
    expect(transaction.children[1].children[0].eventIndex).toBe(4)
    expect(transaction.children[0].children[0].key).not.toBe(transaction.children[1].children[0].key)
    expect(debugTransactionTimingBreakdown(transaction)).toEqual({ childSpanTotalMs: 100, overlapMs: 0, outsideChildrenMs: 50, parentElapsedMs: 150 })
  })
  it('lists no transaction for a transparent request group, which records none at run time', () => {
    const transparent = script([{ name: 'Concurrent requests 1 (2 requests)', type: 'http_sequence', target: '', metadata: { transparent_request_group: true }, params: { execution_mode: 'concurrent', requests: [{ name: 'First', target: '/1' }, { name: 'Second', target: '/2' }] } }])
    expect(captureDebugTransactionDefinitions(transparent)).toEqual([])
    const sequential = script([{ name: 'Requests in order', type: 'http_sequence', target: '', metadata: { transparent_request_group: true }, params: { requests: [{ name: 'First', target: '/1' }, { name: 'Second', target: '/2' }] } }])
    expect(captureDebugTransactionDefinitions(sequential)[0]).toMatchObject({ name: 'Requests in order', kind: 'transaction', mode: 'sequence' })
    const inStep = script([marker('Step'), { name: 'Concurrent requests 1 (2 requests)', type: 'http_sequence', target: '', metadata: { transparent_request_group: true }, params: { execution_mode: 'concurrent', requests: [{ name: 'First', target: '/1' }, { name: 'Second', target: '/2' }] } }, marker('Step', true)])
    const [step] = captureDebugTransactionDefinitions(inStep)
    expect(step).toMatchObject({ name: 'Step', kind: 'transaction' })
    expect(step.children[0]).toMatchObject({ kind: 'container', mode: 'control' })
  })
  it('uses elapsed span for concurrent requests, handles legacy request indices, and deduplicates evidence', () => {
    const source = script([{ name: 'Parallel page', type: 'http_sequence', target: '', params: { requests: [{ name: 'First', target: '/1' }, { name: 'Second', target: '/2' }] } }])
    const first = event(0, 10, 60, { event_index: 0, request_index: 0 })
    const second = event(0, 20, 80, { event_index: 1, request_index: 1 })
    const [row] = debugTransactionRows(snapshot(source, [first, second, first]))
    expect(row).toMatchObject({ name: 'Parallel page', status: 'Passed', durationMs: 70 })
    expect(row.children.map(child => child.durationMs)).toEqual([50, 60])
    expect(debugTransactionTimingBreakdown(row)).toEqual({ childSpanTotalMs: 110, overlapMs: 40, outsideChildrenMs: 0, parentElapsedMs: 70 })
  })
  it('preserves sub-millisecond elapsed spans and separates protocol action measurements', () => {
    const source = script([marker('Parent'), request('Page'), marker('Parent', true)])
    const run = snapshot(source, [event(0, 0, 0, { started_at: '2026-10-26T12:00:00.000100000Z' }), event(1, 0, 0, { started_at: '2026-10-26T12:00:00.000200000Z', completed_at: '2026-10-26T12:00:00.000600000Z', duration_ms: 0.25 }), event(2, 0, 0, { completed_at: '2026-10-26T12:00:00.000900000Z' })])
    const [row] = debugTransactionRows(run)
    expect(row.durationMs).toBe(0.8)
    expect(row.children[0]).toMatchObject({ durationMs: 0.4, recordedDurationMs: 0.25 })
    expect(debugTransactionTimingBreakdown(row)).toEqual({ childSpanTotalMs: 0.4, overlapMs: 0, outsideChildrenMs: 0.4, parentElapsedMs: 0.8 })
  })
  it('uses high precision when ordering concurrent boundaries within one millisecond', () => {
    const source = script([{ name: 'Page', type: 'http_sequence', target: '', params: { requests: [{ name: 'First', target: '/1' }, { name: 'Second', target: '/2' }] } }])
    const [row] = debugTransactionRows(snapshot(source, [event(0, 0, 0, { event_index: 0, request_index: 0, started_at: '2026-10-26T08:00:00.000300-04:00', completed_at: '2026-10-26T08:00:00.000900-04:00' }), event(0, 0, 0, { event_id: 'second', event_index: 1, request_index: 1, started_at: '2026-10-26T12:00:00.000100Z', completed_at: '2026-10-26T12:00:00.000700Z' })]))
    expect(row.durationMs).toBe(0.8)
    expect(row.startedAt).toBe('2026-10-26T12:00:00.000100Z')
    expect(row.completedAt).toBe('2026-10-26T08:00:00.000900-04:00')
    expect(debugTransactionTimingBreakdown(row)).toEqual({ childSpanTotalMs: 1.2, overlapMs: 0.4, outsideChildrenMs: 0, parentElapsedMs: 0.8 })
  })
  it('accounts for gaps before, between, and after child subtransactions once', () => {
    const source = script([marker('Parent'), marker('One', false, true), marker('One', true, true), marker('Two', false, true), marker('Two', true, true), marker('Parent', true)])
    const [row] = debugTransactionRows(snapshot(source, [event(0, 0, 0), event(1, 10, 10), event(2, 30, 30), event(3, 50, 50), event(4, 80, 80), event(5, 100, 100)]))
    expect(debugTransactionTimingBreakdown(row)).toEqual({ childSpanTotalMs: 50, overlapMs: 0, outsideChildrenMs: 50, parentElapsedMs: 100 })
  })
  it('does not double count nested grandchildren or three overlapping siblings', () => {
    const source = script([{ name: 'Page', type: 'http_sequence', target: '', params: { requests: [{ name: 'A', target: '/1' }, { name: 'B', target: '/2' }, { name: 'C', target: '/3' }] } }])
    const [row] = debugTransactionRows(snapshot(source, [event(0, 0, 100, { event_index: 0, request_index: 0 }), event(0, 10, 90, { event_index: 1, request_index: 1 }), event(0, 20, 80, { event_index: 2, request_index: 2 })]))
    expect(debugTransactionTimingBreakdown(row)).toEqual({ childSpanTotalMs: 240, overlapMs: 140, outsideChildrenMs: 0, parentElapsedMs: 100 })
    row.children[0].children = [{ ...row.children[1], children: [] }]
    expect(debugTransactionTimingBreakdown(row)?.childSpanTotalMs).toBe(240)
  })
  it('excludes unexecuted branches from child coverage without treating them as missing evidence', () => {
    const source = script([marker('Parent'), { name: 'Unused branch', type: 'control_if', target: '', control: { condition: { expr: 'false' }, steps: [request('Unused')] } }, marker('Parent', true)])
    const [row] = debugTransactionRows(snapshot(source, [event(0, 0, 0), event(2, 40, 40)]))
    expect(row.children[0].status).toBe('Not run')
    expect(debugTransactionTimingBreakdown(row)).toEqual({ childSpanTotalMs: 0, overlapMs: 0, outsideChildrenMs: 40, parentElapsedMs: 40 })
  })
  it('withholds accounting when timing is missing, incomplete, or outside the parent', () => {
    const source = script([marker('Parent'), request('Child'), marker('Parent', true)])
    const run = snapshot(source, [event(0, 0, 0), event(1, 10, 30), event(2, 40, 40)])
    const [row] = debugTransactionRows(run)
    row.children[0].completedAt = null
    expect(debugTransactionTimingBreakdown(row)).toBeNull()
    row.children[0].completedAt = new Date(50).toISOString()
    expect(debugTransactionTimingBreakdown(row)).toBeNull()
    run.report!.dropped_event_count = 1
    expect(debugTransactionTimingBreakdown(debugTransactionRows(run)[0])).toBeNull()
  })
  it('does not derive a full page duration from partially retained timestamps', () => {
    const source = script([{ name: 'Page', type: 'http_sequence', target: '', params: { requests: [{ name: 'First', target: '/1' }, { name: 'Second', target: '/2' }] } }])
    const run = snapshot(source, [event(0, 10, 20, { event_index: 0, request_index: 0, started_at: '' }), event(0, 20, 40, { event_index: 1, request_index: 1 })])
    expect(debugTransactionRows(run)[0]).toMatchObject({ status: 'Passed', incomplete: true, startedAt: null, durationMs: null })
  })
  it('keeps transaction outcomes authoritative when only payload evidence was truncated', () => {
    const source = script([{ name: 'Page', type: 'http_sequence', target: '', params: { requests: [{ name: 'Request', target: '/' }] } }, marker('Later'), marker('Later', true)])
    const run = snapshot(source, [event(0, 10, 20, { request_index: 0 })])
    run.report!.truncated_event_evidence_count = 1
    expect(debugTransactionRows(run)).toMatchObject([{ status: 'Passed', durationMs: 10 }, { status: 'Not run' }])
  })
  it('does not mistake a manually reported transaction call for the transaction timing', () => {
    const source = script([{ name: 'Report duration', type: 'source_transaction', target: '', params: { function: 'lr_set_transaction', args: ['Manual', '1.5', 'LR_PASS'] } }])
    expect(debugTransactionRows(snapshot(source, [event(0, 10, 10)]))[0]).toMatchObject({ name: 'Manual', status: 'Passed', startedAt: null, completedAt: null, durationMs: null, incomplete: true, eventIndex: 0 })
  })
  it('keeps stop and resume calls inside a transaction until its end marker', () => {
    const source = script([marker('Visit'), { name: 'Stop timer', type: 'source_transaction', target: '', params: { function: 'lr_stop_transaction', args: ['Visit'] } }, { name: 'Resume timer', type: 'source_transaction', target: '', params: { function: 'lr_resume_transaction', args: ['Visit'] } }, marker('Visit', true)])
    const [row] = debugTransactionRows(snapshot(source, [event(0, 0, 0), event(1, 10, 10), event(2, 20, 20), event(3, 30, 30)]))
    expect(row).toMatchObject({ status: 'Passed', durationMs: 30 })
    expect(row.children.map(child => child.name)).toEqual(['Stop timer', 'Resume timer'])
  })
  it('does not merge same-named transactions across phases or unrelated conditional branches', () => {
    const source = script([{ name: 'Choose', type: 'control_if', target: '', control: { condition: { expr: 'true' }, steps: [marker('Branch'), request('Yes'), marker('Branch', true)], else: [marker('Branch'), request('No'), marker('Branch', true)] } }], { init: [marker('Branch'), request('Init'), marker('Branch', true)] })
    const definitions = captureDebugTransactionDefinitions(source)
    expect(definitions).toHaveLength(2)
    expect(definitions[1].children).toHaveLength(2)
    expect(definitions[1].children[0].key).not.toBe(definitions[1].children[1].key)
  })
  it('pairs case-insensitive transaction identities across phase boundaries', () => {
    const source = script([request('Action page'), marker('checkout', true)], { init: [marker('Checkout')] })
    const [row] = debugTransactionRows(snapshot(source, [event(0, 0, 0, { event_index: 0, phase: 'init' }), event(0, 10, 20, { event_index: 1 }), event(1, 30, 30, { event_index: 2 })]))
    expect(row).toMatchObject({ name: 'Checkout', phase: 'init', status: 'Passed', durationMs: 30 })
    expect(row.children[0]).toMatchObject({ name: 'Action page', phase: 'action' })
  })
  it('preserves unavailable times, hides disclosed evidence, and snapshots source structure independently', () => {
    const source = script([marker('Original'), request('Page'), marker('Original', true)])
    const run = snapshot(source, [event(0, 0, 0, { started_at: 'invalid' }), event(1, 10, 20), event(2, 30, 30)])
    source.actions[0].params!.args = ['Edited']
    expect(debugTransactionRows(run)[0]).toMatchObject({ name: 'Original', startedAt: null, durationMs: null })
    run.session.runtime_evidence_unavailable = true
    expect(debugTransactionRows(run)).toEqual([])
  })
  it('groups transaction-tagged actions and leaves ungrouped steps out of the transaction list', () => {
    const source = script([{ ...request('A'), metadata: { source_transaction: 'Tagged' } }, { ...request('B'), metadata: { source_transaction: 'Tagged' } }, request('Not a transaction')])
    const [row] = debugTransactionRows(snapshot(source, [event(0, 0, 10), event(1, 20, 30), event(2, 40, 50)]))
    expect(row).toMatchObject({ name: 'Tagged', durationMs: 30, status: 'Passed' })
    expect(row.children).toHaveLength(2)
  })
})
