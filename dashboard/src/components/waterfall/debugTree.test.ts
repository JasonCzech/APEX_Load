import { describe, expect, it } from 'vitest'
import type { BrowserReplayEvent } from '../../types/replay'
import type { DebugCompletionSnapshot } from '../debugCompletionModel'
import { captureDebugTransactionDefinitions, debugTransactionRows } from '../debugTransactionModel'
import { buildDebugWaterfallTree, isThinkTimeEvent, scriptIterationKeys, type DebugWaterfallNode } from './debugTree'

const at = (ms: number) => new Date(Date.UTC(2026, 10, 9, 12, 0, 0) + ms).toISOString()
const ev = (o: Partial<BrowserReplayEvent> & { start: number; dur: number }): BrowserReplayEvent => {
  const { start, dur, ...rest } = o
  return { phase: 'action', action_index: 0, action_name: 'Step', action_type: 'request', method: 'GET', target: 'https://shop.test/', success: true, started_at: at(start), completed_at: at(start + dur), duration_ms: dur, ...rest }
}
const shape = (n: DebugWaterfallNode, depth = 0): string[] => [`${'  '.repeat(depth)}${n.kind}:${n.label}`, ...n.children.flatMap(c => shape(c, depth + 1))]

describe('debug waterfall tree', () => {
  it('detects script iterations when execution returns to the first action', () => {
    const events = [
      ev({ phase: 'init', action_index: 0, start: 0, dur: 5 }),
      ev({ action_index: 0, start: 5, dur: 5 }),
      ev({ action_index: 1, start: 10, dur: 5 }),
      ev({ action_index: 0, start: 15, dur: 5 }),
      ev({ action_index: 1, start: 20, dur: 5 }),
      ev({ phase: 'teardown', action_index: 0, start: 25, dur: 5 }),
    ]
    expect(scriptIterationKeys(events)).toEqual(['init', 'it:1', 'it:1', 'it:2', 'it:2', 'teardown'])
    // A single-action script repeating the same location also starts a new iteration.
    expect(scriptIterationKeys([ev({ start: 0, dur: 1 }), ev({ start: 1, dur: 1 })])).toEqual(['it:1', 'it:2'])
    // Concurrent requests in one action are not mistaken for iterations.
    expect(scriptIterationKeys([ev({ request_index: 0, start: 0, dur: 1 }), ev({ request_index: 1, start: 0, dur: 1 })])).toEqual(['it:1', 'it:1'])
    const tree = buildDebugWaterfallTree({ events })
    expect(tree.roots.map(r => r.label)).toEqual(['Init', 'Iteration 1', 'Iteration 2', 'Teardown'])
    expect(tree.roots[1]).toMatchObject({ kind: 'iteration', startMs: 5, durationMs: 10 })
    expect(tree.spanMs).toBe(30)
  })

  it('nests requests under transactions per iteration and loop iteration', () => {
    const transactions = captureDebugTransactionDefinitions({
      name: 'Shop', version: '1.0', protocol: 'http', config: { base_url: '' },
      actions: [
        { name: 'Start Browse', type: 'source_transaction', target: '', params: { function: 'lr_start_transaction', args: ['Browse'] } },
        { name: 'Home', type: 'request', method: 'GET', target: '/' },
        { name: 'Think', type: 'think_time', target: '' },
        { name: 'Item', type: 'request', method: 'GET', target: '/item' },
        { name: 'End Browse', type: 'source_transaction', target: '', params: { function: 'lr_end_transaction', args: ['Browse', 'LR_AUTO'] } },
      ],
    } as never)
    const events = [
      ev({ event_index: 0, action_index: 0, action_name: 'Start Browse', action_type: 'source_transaction', method: undefined, target: '', start: 0, dur: 0 }),
      ev({ event_index: 1, action_index: 1, action_name: 'Home', status_code: 200, start: 0, dur: 100, protocol_evidence: { timings: { dns_ms: 10, connect_ms: 20, tls_ms: 30, ttfb_ms: 90, duration_ms: 100 } } }),
      ev({ event_index: 2, action_index: 2, action_name: 'Think', action_type: 'think_time', method: undefined, target: '', start: 100, dur: 500 }),
      ev({ event_index: 3, action_index: 3, action_name: 'Item', status_code: 200, start: 600, dur: 200, protocol_evidence: { timings: { ttfb_ms: 150, duration_ms: 200, redirect_count: 2, redirect_ms: 60 } } }),
      ev({ event_index: 4, action_index: 4, action_name: 'End Browse', action_type: 'source_transaction', method: undefined, target: '', start: 800, dur: 0 }),
    ]
    const snapshot = { key: 'k', scriptName: 'Shop', draft: true, transactions, report: { events }, session: { id: 's', events } } as unknown as DebugCompletionSnapshot
    const tree = buildDebugWaterfallTree({ events, transactions: debugTransactionRows(snapshot) })
    expect(tree.roots.flatMap(r => shape(r))).toEqual([
      'iteration:Actions',
      '  transaction:Browse',
      '    marker:Start Browse',
      '    request:Home',
      '    think:Think',
      '    request:Item',
      '      hop:2 redirects followed',
      '      hop:200 GET shop.test/',
      '    marker:End Browse',
    ])
    const browse = tree.roots[0].children[0]
    expect(browse).toMatchObject({ startMs: 0, durationMs: 800, status: 'passed' })
    const item = browse.children.find(c => c.label === 'Item')!
    expect(item.redirect).toEqual({ count: 2, ms: 60 })
    expect(item.timingSource).toBe('duration')
    expect(item.children[0]).toMatchObject({ derived: true, startMs: 600, durationMs: 60 })
    expect(item.children[1]).toMatchObject({ startMs: 660, durationMs: 140 })
    const home = browse.children.find(c => c.label === 'Home')!
    expect(home.timingSource).toBe('protocol_evidence')
    expect(home.phases.map(p => p.key)).toEqual(['dns', 'connect', 'tls', 'wait', 'receive'])
    expect(isThinkTimeEvent(events[2])).toBe(true)
  })

  it('groups untagged dock events by source transaction and maps caller indices', () => {
    const events = [
      ev({ action_index: 0, action_name: 'Login', source_transaction: 'Auth', start: 0, dur: 10 }),
      ev({ action_index: 1, action_name: 'Token', source_transaction: 'Auth', start: 10, dur: 10, success: false, error_message: 'HTTP 401', status_code: 401 }),
      ev({ action_index: 2, action_name: 'Logout', start: 20, dur: 10 }),
    ]
    const tree = buildDebugWaterfallTree({ events, eventIndices: [7, 8, 9] })
    expect(tree.roots.flatMap(r => shape(r))).toEqual(['iteration:Actions', '  transaction:Auth', '    request:Login', '    request:Token', '  request:Logout'])
    const auth = tree.roots[0].children[0]
    expect(auth.status).toBe('failed')
    expect(tree.roots[0].status).toBe('failed')
    expect(auth.children[1].payload).toEqual({ position: 1, eventIndex: 8 })
  })

  it('shows per-hop network entries as redirect hops', () => {
    const events = [ev({
      action_name: 'Pay', status_code: 200, start: 0, dur: 1000,
      network_entries: [
        { url: 'https://shop.test/pay', method: 'POST', status_code: 302, duration_ms: 400, har: { phases: [{ name: 'request_wait', start_ms: 0, end_ms: 400 }] } },
        { url: 'https://shop.test/done', method: 'GET', status_code: 200, duration_ms: 600, har: { phases: [{ name: 'request_wait', start_ms: 0, end_ms: 600 }] } },
      ],
    })]
    const [pay] = buildDebugWaterfallTree({ events }).roots[0].children
    expect(pay.timingSource).toBe('har')
    expect(pay.redirect).toEqual({ count: 1, ms: 400 })
    expect(pay.children.map(h => [h.label, h.startMs, h.durationMs])).toEqual([['302 POST shop.test/pay', 0, 400], ['200 GET shop.test/done', 400, 600]])
  })
})

describe('debug waterfall tree across script iterations', () => {
  it('splits a tagged transaction that spans two script iterations into one instance per iteration', () => {
    const transactions = captureDebugTransactionDefinitions({
      name: 'Tags', version: '1.0', protocol: 'http', config: { base_url: '' },
      actions: [
        { name: 'Login', type: 'request', method: 'GET', target: '/login', metadata: { source_transaction: 'Sign in' } },
        { name: 'Pay', type: 'request', method: 'GET', target: '/pay', metadata: { source_transaction: 'Checkout' } },
      ],
    } as never)
    const events = [
      ev({ event_index: 0, action_index: 0, action_name: 'Login', status_code: 200, start: 0, dur: 50 }),
      ev({ event_index: 1, action_index: 1, action_name: 'Pay', status_code: 200, start: 50, dur: 50 }),
      ev({ event_index: 2, action_index: 0, action_name: 'Login', status_code: 200, start: 100, dur: 50 }),
      ev({ event_index: 3, action_index: 1, action_name: 'Pay', status_code: 500, success: false, start: 150, dur: 50 }),
    ]
    const snapshot = { key: 'k', scriptName: 'Tags', draft: true, transactions, report: { events }, session: { id: 's', events } } as unknown as DebugCompletionSnapshot
    const tree = buildDebugWaterfallTree({ events, transactions: debugTransactionRows(snapshot) })
    expect(tree.roots.flatMap(r => shape(r))).toEqual([
      'iteration:Iteration 1', '  transaction:Sign in', '    request:Login', '  transaction:Checkout', '    request:Pay',
      'iteration:Iteration 2', '  transaction:Sign in', '    request:Login', '  transaction:Checkout', '    request:Pay',
    ])
    expect(tree.roots.map(r => [r.startMs, r.durationMs, r.status])).toEqual([[0, 100, 'passed'], [100, 100, 'failed']])
    expect(tree.roots[1].children[1]).toMatchObject({ startMs: 150, durationMs: 50, status: 'failed' })
  })
})

describe('debug waterfall tree on real-world LoadRunner shapes', () => {
  // Shapes from FAN_SalesforceApp_55_NoProxy: a marker transaction whose end
  // never ran, an HTTP sequence and its same-named tag span, and the engine's
  // "finalize" marker that closes the open transaction.
  const transactions = captureDebugTransactionDefinitions({
    name: 'FAN', version: '1.0', protocol: 'http', config: { base_url: '' },
    actions: [
      { name: 'Start_T02', type: 'source_transaction', target: '', params: { function: 'lr_start_transaction', args: ['T02_Login'] } },
      { name: 'T02_Login', type: 'http_sequence', target: '', params: { requests: [
        { name: 'T02_Login_authorize', type: 'request', method: 'GET', target: '/authorize', metadata: { source_transaction: 'T02_Login' } },
        { name: 'T02_Login_token', type: 'request', method: 'POST', target: '/token', metadata: { source_transaction: 'T02_Login' } },
      ] } },
      { name: 'End_T02', type: 'source_transaction', target: '', params: { function: 'lr_end_transaction', args: ['T02_Login', 'LR_AUTO'] } },
    ],
  } as never)
  const events = [
    ev({ event_index: 0, action_index: 0, action_name: 'Start_T02', action_type: 'source_transaction', method: undefined, target: '', start: 0, dur: 0 }),
    ev({ event_index: 1, action_index: 1, request_index: 0, step_path: ['requests[0]'], action_name: 'T02_Login_authorize', status_code: 200, start: 5, dur: 300,
      network_entries: [
        { url: 'https://login.test/authorize', method: 'GET', status_code: 302, duration_ms: 100, har: { phases: [{ name: 'request_wait', start_ms: 0, end_ms: 100 }] } },
        { url: 'https://login.test/consent', method: 'GET', status_code: 401, duration_ms: 200, har: { phases: [{ name: 'request_wait', start_ms: 0, end_ms: 200 }] } },
      ], success: false, error_message: 'unexpected login page' }),
    ev({ event_index: 2, phase: 'finalize', action_index: -1, action_name: 'T02_Login', action_type: 'source_transaction', method: undefined, target: '', start: 0, dur: 320, success: false, error_message: 'source transaction ended without a matching end marker during replay' }),
  ]
  const snapshot = { key: 'k', scriptName: 'FAN', draft: true, transactions, report: { events }, session: { id: 's', events } } as unknown as DebugCompletionSnapshot
  const tree = buildDebugWaterfallTree({ events, transactions: debugTransactionRows(snapshot) })

  it('nests the finalize marker under the transaction it closes and merges same-named levels', () => {
    expect(tree.roots.map(r => r.label)).toEqual(['Actions'])
    expect(tree.roots.flatMap(r => shape(r))).toEqual([
      'iteration:Actions',
      '  transaction:T02_Login',
      '    marker:Start_T02',
      '    marker:T02_Login',
      '    transaction:T02_Login',
      '      request:T02_Login_authorize',
      '        hop:302 GET login.test/authorize',
      '        hop:401 GET login.test/consent',
      '      step:T02_Login_token',
    ])
  })

  it('gives a failed transaction without an end marker the span its children ran', () => {
    const t02 = tree.roots[0].children[0]
    expect(t02).toMatchObject({ status: 'failed', startMs: 0, durationMs: 320 })
    expect(t02.children[2]).toMatchObject({ startMs: 5, durationMs: 300 })
  })

  it('counts one failure per execution, not per hop, and keeps markers out of the slowest requests', async () => {
    const { topTimeSinks } = await import('./analysis')
    const sinks = topTimeSinks(tree.roots, tree.spanMs)
    expect(sinks.find(s => s.id === 'failures')?.label).toBe('2 failures')
    expect(sinks.find(s => s.id === 'slowest')?.detail).toBe('T02_Login_authorize 300.0 ms')
  })
})

describe('debug waterfall tree ownership after review', () => {
  it('puts back-to-back start markers under their own transactions', () => {
    const transactions = captureDebugTransactionDefinitions({
      name: 'Nested', version: '1.0', protocol: 'http', config: { base_url: '' },
      actions: [
        { name: 'Start Outer', type: 'source_transaction', target: '', params: { function: 'lr_start_transaction', args: ['Outer'] } },
        { name: 'Start Inner', type: 'source_transaction', target: '', params: { function: 'lr_start_sub_transaction', args: ['Inner', 'Outer'] } },
        { name: 'Call', type: 'request', method: 'GET', target: '/call' },
        { name: 'End Inner', type: 'source_transaction', target: '', params: { function: 'lr_end_sub_transaction', args: ['Inner', 'LR_AUTO'] } },
        { name: 'End Outer', type: 'source_transaction', target: '', params: { function: 'lr_end_transaction', args: ['Outer', 'LR_AUTO'] } },
      ],
    } as never)
    const marker = (i: number, name: string, start: number) => ev({ event_index: i, action_index: i, action_name: name, action_type: 'source_transaction', method: undefined, target: '', start, dur: 0 })
    const events = [marker(0, 'Start Outer', 0), marker(1, 'Start Inner', 0.2), ev({ event_index: 2, action_index: 2, action_name: 'Call', status_code: 200, start: 1, dur: 50 }), marker(3, 'End Inner', 60), marker(4, 'End Outer', 70)]
    const snapshot = { key: 'k', scriptName: 'N', draft: true, transactions, report: { events }, session: { id: 's', events } } as unknown as DebugCompletionSnapshot
    const tree = buildDebugWaterfallTree({ events, transactions: debugTransactionRows(snapshot) })
    const outer = tree.roots[0].children.find(n => n.label === 'Outer')!
    const inner = outer.children.find(n => n.label === 'Inner')!
    expect(outer.children.map(c => c.label)).toContain('Start Outer')
    expect(inner.children.map(c => c.label)).not.toContain('Start Outer')
    expect(inner.children.map(c => c.label)).toContain('Start Inner')
  })

  it('keeps dropped-evidence transactions apart from ones that never ran', () => {
    const transactions = captureDebugTransactionDefinitions({
      name: 'Dropped', version: '1.0', protocol: 'http', config: { base_url: '' },
      actions: [{ name: 'Home', type: 'request', method: 'GET', target: '/', metadata: { source_transaction: 'Browse' } }],
    } as never)
    const snapshot = { key: 'k', scriptName: 'D', draft: true, transactions, report: { events: [], dropped_event_count: 3 }, session: { id: 's', events: [], dropped_event_count: 3 } } as unknown as DebugCompletionSnapshot
    const tree = buildDebugWaterfallTree({ events: [], transactions: debugTransactionRows(snapshot) })
    expect(tree.roots.map(r => [r.label, r.status])).toEqual([['Not observed', 'incomplete']])
    expect(tree.roots[0].children[0]).toMatchObject({ label: 'Browse', status: 'incomplete' })
  })
})

describe('finalize markers prefer the open execution', () => {
  it('closes the latest observed execution that has no end, not a completed one', () => {
    const transactions = captureDebugTransactionDefinitions({
      name: 'Open', version: '1.0', protocol: 'http', config: { base_url: '' },
      actions: [
        { name: 'Start B', type: 'source_transaction', target: '', params: { function: 'lr_start_transaction', args: ['B'] } },
        { name: 'Call', type: 'request', method: 'GET', target: '/call' },
        { name: 'End B', type: 'source_transaction', target: '', params: { function: 'lr_end_transaction', args: ['B', 'LR_AUTO'] } },
      ],
    } as never)
    const marker = (i: number, ai: number, name: string, start: number) => ev({ event_index: i, action_index: ai, action_name: name, action_type: 'source_transaction', method: undefined, target: '', start, dur: 0 })
    const events = [
      marker(0, 0, 'Start B', 0), ev({ event_index: 1, action_index: 1, action_name: 'Call', status_code: 200, start: 1, dur: 10 }), marker(2, 2, 'End B', 20),
      marker(3, 0, 'Start B', 30), ev({ event_index: 4, action_index: 1, action_name: 'Call', status_code: 500, success: false, start: 31, dur: 10 }),
      ev({ event_index: 5, phase: 'finalize', action_index: -1, action_name: 'B', action_type: 'source_transaction', method: undefined, target: '', start: 30, dur: 40, success: false }),
    ]
    const snapshot = { key: 'k', scriptName: 'O', draft: true, transactions, report: { events }, session: { id: 's', events } } as unknown as DebugCompletionSnapshot
    const tree = buildDebugWaterfallTree({ events, transactions: debugTransactionRows(snapshot) })
    expect(tree.roots.map(r => r.label)).toEqual(['Iteration 1', 'Iteration 2'])
    const [first, second] = tree.roots.map(r => r.children.find(c => c.label === 'B')!)
    expect(first.children.some(c => c.label === 'B' && c.kind === 'marker')).toBe(false)
    expect(second.children.some(c => c.label === 'B' && c.kind === 'marker')).toBe(true)
    expect(second).toMatchObject({ startMs: 30, durationMs: 40, status: 'failed' })
  })
})

describe('round-3 tree fixes', () => {
  it('uses the legacy iteration index as loop identity', () => {
    expect(scriptIterationKeys([ev({ iteration: 1, start: 0, dur: 1 }), ev({ iteration: 2, start: 1, dur: 1 })])).toEqual(['it:1', 'it:1'])
  })

  it('counts only document redirects, not sub-resources loaded with the page', () => {
    const events = [ev({
      action_name: 'Open page', action_type: 'browser_navigate', status_code: 200, start: 0, dur: 500,
      network_entries: [
        { url: 'https://shop.test/', method: 'GET', status_code: 302, resource_type: 'document', duration_ms: 50 },
        { url: 'https://shop.test/home', method: 'GET', status_code: 200, resource_type: 'document', duration_ms: 200 },
        { url: 'https://cdn.test/logo.png', method: 'GET', status_code: 301, resource_type: 'image', duration_ms: 20 },
        { url: 'https://cdn.test/app.js', method: 'GET', status_code: 307, resource_type: 'script', duration_ms: 30 },
      ],
    })]
    const [page] = buildDebugWaterfallTree({ events }).roots[0].children
    expect(page.redirect).toEqual({ count: 1, ms: 50 })
  })

  it('files an executed container without its own event under its iteration, not Not run', () => {
    const transactions = captureDebugTransactionDefinitions({
      name: 'If', version: '1.0', protocol: 'http', config: { base_url: '' },
      actions: [
        { name: 'Home', type: 'request', method: 'GET', target: '/', metadata: { source_transaction: 'Browse' } },
        { name: 'Maybe', type: 'control_if', target: '', control: { condition: 'false', steps: [{ name: 'Extra', type: 'request', method: 'GET', target: '/extra', metadata: { source_transaction: 'Bonus' } }] } },
      ],
    } as never)
    const events = [
      ev({ event_index: 0, action_index: 0, action_name: 'Home', status_code: 200, start: 0, dur: 50 }),
      ev({ event_index: 1, action_index: 1, action_name: 'Maybe', action_type: 'control_if', method: undefined, target: '', start: 60, dur: 1 }),
    ]
    const snapshot = { key: 'k', scriptName: 'If', draft: true, transactions, report: { events }, session: { id: 's', events } } as unknown as DebugCompletionSnapshot
    // The summary drops container events before building the tree.
    const tree = buildDebugWaterfallTree({ events: [events[0]], eventIndices: [0], transactions: debugTransactionRows(snapshot) })
    const rows = debugTransactionRows(snapshot)
    expect(rows.find(r => r.name === 'Maybe')).toMatchObject({ executions: 1 })
    expect(tree.roots.map(r => r.label)).toEqual(['Actions'])
    expect(tree.roots[0].children.map(c => c.label)).toContain('Maybe')
  })
})

describe('round-4 tree fixes', () => {
  it('finds script iterations on the full event list when containers were filtered out', () => {
    const all = [
      ev({ action_index: 0, action_name: 'Gate', action_type: 'control_if', method: undefined, target: '', start: 0, dur: 1 }),
      ev({ action_index: 0, step_path: ['then[0]'], action_name: 'Login', status_code: 200, start: 1, dur: 5 }),
      ev({ action_index: 1, step_path: ['requests[0]'], action_name: 'Browse', status_code: 200, start: 10, dur: 5 }),
      ev({ action_index: 0, action_name: 'Gate', action_type: 'control_if', method: undefined, target: '', start: 20, dur: 1 }),
      ev({ action_index: 1, step_path: ['requests[0]'], action_name: 'Browse', status_code: 200, start: 30, dur: 5 }),
    ]
    const kept = [1, 2, 4]
    const filtered = kept.map(i => all[i])
    expect(buildDebugWaterfallTree({ events: filtered, eventIndices: kept }).roots.map(r => r.label)).toEqual(['Actions'])
    expect(buildDebugWaterfallTree({ events: filtered, eventIndices: kept, allEvents: all }).roots.map(r => r.label)).toEqual(['Iteration 1', 'Iteration 2'])
  })
})

describe('round-6 tree fixes', () => {
  it('reads an omitted action_index as the first action when detecting iterations', () => {
    const noIndex = (start: number) => { const e = ev({ start, dur: 1 }); delete (e as Partial<BrowserReplayEvent>).action_index; return e }
    const events = [noIndex(0), ev({ action_index: 1, start: 1, dur: 1 }), ev({ action_index: 2, start: 2, dur: 1 }), noIndex(3), ev({ action_index: 1, start: 4, dur: 1 })]
    expect(scriptIterationKeys(events)).toEqual(['it:1', 'it:1', 'it:1', 'it:2', 'it:2'])
  })
})

describe('round-8 tree fixes', () => {
  it('turns dock container events into containers that adopt their requests', () => {
    const events = [
      ev({ action_index: 0, request_index: 0, step_path: ['requests[0]'], action_name: 'A', status_code: 200, start: 0, dur: 100 }),
      ev({ action_index: 0, request_index: 1, step_path: ['requests[1]'], action_name: 'B', status_code: 200, start: 100, dur: 100 }),
      ev({ action_index: 0, action_name: 'Checkout flow', action_type: 'http_sequence', method: undefined, target: '', start: 0, dur: 200 }),
    ]
    const tree = buildDebugWaterfallTree({ events })
    expect(tree.roots.flatMap(r => shape(r))).toEqual(['iteration:Actions', '  container:Checkout flow', '    request:A', '    request:B'])
  })

  it('ignores re-delivered events when detecting iterations', () => {
    const first = ev({ event_id: 'e0', action_index: 0, start: 0, dur: 1 })
    expect(scriptIterationKeys([first, ev({ event_id: 'e1', action_index: 1, start: 1, dur: 1 }), first])).toEqual(['it:1', 'it:1', 'it:1'])
  })

  it('keeps hop ids unique when the same event appears twice', () => {
    const e = ev({ event_id: 'dup', status_code: 200, start: 0, dur: 100, network_entries: [
      { url: 'https://a.test/', method: 'GET', status_code: 302, duration_ms: 40 },
      { url: 'https://a.test/b', method: 'GET', status_code: 200, duration_ms: 60 },
    ] })
    const ids: string[] = []
    const visit = (n: DebugWaterfallNode) => { ids.push(n.id); n.children.forEach(visit) }
    buildDebugWaterfallTree({ events: [e, { ...e }] }).roots.forEach(visit)
    expect(new Set(ids).size).toBe(ids.length)
  })
})

describe('round-9 dock shapes', () => {
  it('keeps tagged requests inside their same-named HTTP sequence', () => {
    const events = [
      ev({ action_index: 0, request_index: 0, step_path: ['requests[0]'], action_name: 'authorize', source_transaction: 'T02_Login', status_code: 200, start: 0, dur: 100 }),
      ev({ action_index: 0, request_index: 1, step_path: ['requests[1]'], action_name: 'token', source_transaction: 'T02_Login', status_code: 200, start: 100, dur: 100 }),
      ev({ action_index: 0, action_name: 'T02_Login', action_type: 'http_sequence', method: undefined, target: '', start: 0, dur: 200 }),
    ]
    expect(buildDebugWaterfallTree({ events }).roots.flatMap(r => shape(r))).toEqual(['iteration:Actions', '  container:T02_Login', '    request:authorize', '    request:token'])
  })

  it('nests a differently named tag group inside the container it ran in', () => {
    const events = [
      ev({ action_index: 0, step_path: ['then[0]'], action_name: 'a', source_transaction: 'Pay', status_code: 200, start: 0, dur: 50 }),
      ev({ action_index: 0, step_path: ['then[1]'], action_name: 'b', source_transaction: 'Pay', status_code: 200, start: 50, dur: 50 }),
      ev({ action_index: 0, action_name: 'If paid', action_type: 'control_if', method: undefined, target: '', start: 0, dur: 100 }),
    ]
    expect(buildDebugWaterfallTree({ events }).roots.flatMap(r => shape(r))).toEqual(['iteration:Actions', '  container:If paid', '    transaction:Pay', '      request:a', '      request:b'])
  })
})
