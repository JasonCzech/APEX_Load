import { describe, expect, it } from 'vitest'
import type { BrowserReplayEvent } from '../types/replay'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import {
  buildCorrelationTable,
  correlationTableIsEmpty,
  correlationVariablesSet,
  isMaskedCorrelationValue,
  laterStepReads,
  unknownReadsText,
  replayEventStepPosition,
  requestVariableReads,
} from './inspectorCorrelationModel'

function event(overrides: Partial<BrowserReplayEvent> = {}): BrowserReplayEvent {
  return {
    phase: 'action', action_index: 0, action_name: 'Login', action_type: 'request', method: 'POST', target: '/login',
    success: true, started_at: '2026-11-10T12:00:00Z', completed_at: '2026-11-10T12:00:01Z', duration_ms: 10,
    ...overrides,
  } as BrowserReplayEvent
}

const login: ScriptActionDefinition = {
  name: 'Login', type: 'POST', target: '/login?u={{var.user}}',
  headers: { 'X-Csrf': '{{var.csrf}}', Accept: 'application/json' },
  body: 'a={{ var.user }}&b={{var.user}}&p={{param.password}}',
  extractors: [
    { name: 'token', source: '', type: 'regex', expression: 'token=(\\w+)' },
    { name: 'ids', source: 'header', type: 'boundary', expression: 'x', mode: 'all' },
  ],
}

describe('requestVariableReads', () => {
  it('lists each {{var.…}} by field, counting repeats, and ignores other references', () => {
    const reads = requestVariableReads(login)
    expect([...reads.keys()]).toEqual(['user', 'csrf'])
    expect(reads.get('user')).toEqual(['URL', 'Body ×2'])
    expect(reads.get('csrf')).toEqual(['Header X-Csrf'])
  })

  it('reads header_list when present and nothing without an action', () => {
    expect(requestVariableReads({ name: 'x', type: 'GET', target: '/', header_list: [{ name: 'Authorization', value: 'Bearer {{var.tok}}' }] }).get('tok')).toEqual(['Header Authorization'])
    expect(requestVariableReads(null).size).toBe(0)
  })
})

describe('laterStepReads', () => {
  const script: ScriptDefinition = {
    name: 's', version: '1', protocol: 'http', config: { base_url: 'https://api.example.test' },
    actions: [
      login,
      { name: 'Profile', type: 'GET', target: '/me?t={{var.token}}', headers: { A: '{{var.token}}' } },
      { name: 'Cart', type: 'GET', target: '/cart', body: '{{var.other}}' },
      { name: 'Seq', type: 'http_sequence', target: '', params: { requests: [{ name: 'r0', type: 'GET', target: '/a' }, { name: 'r1', type: 'GET', target: '/b?{{var.token}}' }, { name: 'r2', type: 'GET', target: '/c?{{var.seq}}' }] } },
    ],
  }

  it('counts later steps (not occurrences) that reference each name', () => {
    const reads = laterStepReads(script, { phase: 'action', actionIndex: 0 }, ['token', 'other', 'missing'])
    expect(reads.get('token')).toBe(2)
    expect(reads.get('other')).toBe(1)
    expect(reads.has('missing')).toBe(false)
  })

  it('counts the later requests of the source sequence on their own', () => {
    const reads = laterStepReads(script, { phase: 'action', actionIndex: 3, stepPath: ['requests[1]'] }, ['seq', 'token'])
    expect(reads.get('seq')).toBe(1)
    expect(reads.has('token')).toBe(false)
  })

  it('skips a concurrent sequence and reads nothing past an unknown position', () => {
    const concurrent: ScriptDefinition = { ...script, actions: [{ ...script.actions[3], params: { ...script.actions[3].params, execution_mode: 'concurrent' } }] }
    expect(laterStepReads(concurrent, { phase: 'action', actionIndex: 0, stepPath: ['requests[0]'] }, ['seq']).size).toBe(0)
    expect(laterStepReads(script, { phase: 'action', actionIndex: 9 }, ['token']).size).toBe(0)
    expect(laterStepReads(script, { phase: 'action', actionIndex: 0 }, []).size).toBe(0)
  })
})

describe('replayEventStepPosition', () => {
  it('maps the event to a script position, or null outside the phases', () => {
    expect(replayEventStepPosition(event({ action_index: 2, request_index: 1 }))).toEqual({ phase: 'action', actionIndex: 2, stepPath: ['requests[1]'] })
    expect(replayEventStepPosition(event({ action_index: undefined }))).toEqual({ phase: 'action', actionIndex: 0 })
    expect(replayEventStepPosition(event({ phase: 'async' }))).toBeNull()
  })
})

describe('buildCorrelationTable', () => {
  it('lists an applied extraction with its definition, before → after and later uses', () => {
    const model = buildCorrelationTable({
      event: event({ extractions: { token: 'abc', ids_2: 'x', loose: '[redacted]' } }),
      variableDiff: { token: { next: 'abc' }, ids_2: { previous: 'old', next: 'x' } },
      action: login,
      laterReads: new Map([['token', 3]]),
    })
    const [token, ids, loose] = model.rows
    expect(token).toMatchObject({ direction: 'Extracts', variable: 'token', where: 'body · regex', before: null, value: 'abc', result: 'Extracted', tone: 'ok', laterSteps: 3 })
    expect(ids).toMatchObject({ where: 'header · boundary', before: 'old', value: 'x', laterSteps: 0 })
    // No diff entry: no before; an engine-redacted value stays as published.
    expect(loose).toMatchObject({ where: null, value: '[redacted]' })
    expect(loose.before).toBeUndefined()
  })

  it('leaves Where unknown and later uses out without the authored action', () => {
    const [row] = buildCorrelationTable({ event: event({ extractions: { token: 'abc' } }), variableDiff: {} }).rows
    expect(row.where).toBeNull()
    expect(row.laterSteps).toBeUndefined()
  })

  it('adds the failing extractor as a bad row that names its later reads', () => {
    const failed = event({ success: false, error_message: 'extractor "token" found no match: regex token=(\\w+)' })
    const [row] = buildCorrelationTable({ event: failed, variableDiff: {}, action: login, laterReads: new Map([['token', 2]]) }).rows
    expect(row).toMatchObject({ direction: 'Extracts', variable: 'token', where: 'body · regex', value: null, missingValue: 'No value', result: 'No match', tone: 'bad', laterSteps: 2 })
    const rejected = event({ success: false, error_message: 'extractor "token" output rejected: too long' })
    expect(buildCorrelationTable({ event: rejected, variableDiff: {} }).rows[0].result).toBe('Failed')
  })

  it('lists unapplied extractions and their omitted count', () => {
    const model = buildCorrelationTable({ event: event({ unapplied_extractions: { late: 'v' }, unapplied_extractions_omitted_count: 2 }), variableDiff: {} })
    expect(model.rows).toEqual([expect.objectContaining({ variable: 'late', value: 'v', result: 'Not applied', tone: 'warn' })])
    expect(model.notes.map(note => note.text)).toEqual(['2 not applied extractions were omitted from the retained debugger evidence.'])
  })

  it('keeps the classification error and concurrent-group omission notes', () => {
    const model = buildCorrelationTable({
      event: event({ credential_classification_error: 'timeout', protocol_evidence: { concurrent_group_extractions_omitted: true, concurrent_group_extractions_omitted_count: 1 } }),
      variableDiff: {},
    })
    expect(model.notes.map(note => note.text)).toEqual([
      'Credential classification: timeout',
      '1 extraction was omitted from the retained debugger evidence. The displayed values may be incomplete.',
    ])
    const uncounted = buildCorrelationTable({ event: event({ protocol_evidence: { concurrent_group_extractions_omitted: true } }), variableDiff: {} })
    expect(uncounted.notes[0].text).toBe('Some extractions were omitted from the retained debugger evidence. The displayed values may be incomplete.')
    expect(correlationTableIsEmpty(uncounted)).toBe(false)
  })

  it('turns streamed children into rows, notes and a status list', () => {
    const model = buildCorrelationTable({
      event: event({
        extractions: { shared: 'parent' },
        streamed_children: [
          { action_name: 'Chunk', success: false, error_message: 'boom', extractions: { shared: 'child', part: 'p' }, unapplied_extractions: { skip: 's' }, extractions_omitted_count: 1, unapplied_extractions_omitted_count: 3, credential_classification_error: 'bad' },
          { action_name: '', success: true },
        ],
        streamed_children_omitted_count: 4,
      }),
      variableDiff: {},
    })
    // The child's extraction of the parent's name stays listed, as Overridden.
    expect(model.rows.map(row => [row.variable, row.where, row.value, row.result])).toEqual([
      ['shared', null, 'parent', 'Extracted'],
      ['shared', 'Streamed child Chunk', 'child', 'Overridden'],
      ['part', 'Streamed child Chunk', 'p', 'Extracted'],
      ['skip', 'Streamed child Chunk', 's', 'Not applied'],
    ])
    expect(model.notes.map(note => note.text)).toEqual([
      'Chunk: credential classification: bad',
      'Chunk: 1 applied extraction was omitted from retained evidence.',
      'Chunk: 3 not applied extractions were omitted from retained evidence.',
    ])
    expect(model.streamedChildren).toEqual([
      { key: 'child0', label: 'Chunk', passed: false, error: 'boom' },
      { key: 'child1', label: 'Streamed child 2', passed: true },
    ])
    expect(model.streamedChildrenOmitted).toBe(4)
  })

  it('reads: never-set values first, then resolved ones only when the engine checked', () => {
    const failed = event({ success: false, diagnosis: { category: 'correlation', severity: 'error', cause: 'x', unresolved_variables: ['csrf'] } })
    const rows = buildCorrelationTable({ event: failed, variableDiff: {}, action: login }).rows
    expect(rows.map(row => [row.direction, row.variable, row.where, row.value, row.result, row.tone])).toEqual([
      ['Reads', 'csrf', 'Header X-Csrf', 'Left as {{var.csrf}}', 'Never set this run', 'bad'],
      ['Reads', 'user', 'URL, Body ×2', null, 'Resolved', 'ok'],
    ])
    // A passed event carries no check: its reads fold into one quiet line, not rows of Unknown.
    const passed = buildCorrelationTable({ event: event(), variableDiff: {}, action: login })
    expect(passed.rows).toEqual([])
    expect(passed.unknownReads).toEqual(['user', 'csrf'])
    expect(unknownReadsText(passed.unknownReads)).toBe('Also reads user and csrf. The run did not record whether they had values.')
    expect(unknownReadsText([])).toBeNull()
    expect(correlationTableIsEmpty(passed)).toBe(false)
    // Without the authored action an unset value is still listed.
    expect(buildCorrelationTable({ event: failed, variableDiff: {} }).rows).toEqual([expect.objectContaining({ variable: 'csrf', where: null })])
  })

  it('marks a child extraction a later child replaced as Overridden, without before → after', () => {
    const model = buildCorrelationTable({
      event: event({
        streamed_children: [
          { action_name: 'One', success: true, extractions: { cursor: 'c1' } },
          { action_name: 'Two', success: true, extractions: { cursor: 'c2' } },
        ],
      }),
      variableDiff: { cursor: { previous: 'c0', next: 'c2' } },
    })
    expect(model.rows.map(row => [row.where, row.before, row.value, row.result, row.tone])).toEqual([
      ['Streamed child One', undefined, 'c1', 'Overridden', 'neutral'],
      ['Streamed child Two', 'c0', 'c2', 'Extracted', 'ok'],
    ])
    expect(new Set(model.rows.map(row => row.key)).size).toBe(2)
  })

  it('claims Resolved only from an engine list it sent, with runtime evidence and no classification error', () => {
    const diagnosis = { category: 'correlation', severity: 'error', cause: 'x' }
    // 'Unknown': folded into the not-recorded line instead of a row.
    const userResult = (overrides: Partial<BrowserReplayEvent>, runtimeEvidenceUnavailable = false) => {
      const model = buildCorrelationTable({ event: event({ success: false, ...overrides }), variableDiff: {}, action: login, runtimeEvidenceUnavailable })
      const row = model.rows.find(entry => entry.variable === 'user')
      if (row) return row.result
      return model.unknownReads.includes('user') ? 'Unknown' : 'missing'
    }
    expect(userResult({ diagnosis: { ...diagnosis, unresolved_variables: ['csrf'] } })).toBe('Resolved')
    // No list: the engine left it out (empty, withheld) or the event predates it.
    expect(userResult({ diagnosis })).toBe('Unknown')
    // Runtime evidence withheld for the run.
    expect(userResult({ diagnosis: { ...diagnosis, unresolved_variables: ['csrf'] } }, true)).toBe('Unknown')
    // A credential classification error.
    expect(userResult({ diagnosis: { ...diagnosis, unresolved_variables: ['csrf'] }, credential_classification_error: 'timeout' })).toBe('Unknown')
  })

  it('offers a copy value for extracted, unapplied and changed values, never a masked or redacted one', () => {
    const model = buildCorrelationTable({
      event: event({ extractions: { a: '1', m: '[masked]', r: 'x [REDACTED]', b: '••••' }, unapplied_extractions: { u: 'v' } }),
      variableDiff: { s: { next: 'n' } },
    })
    expect(Object.fromEntries(model.rows.map(row => [row.variable, row.copyValue]))).toEqual({ a: '1', m: undefined, r: undefined, b: undefined, u: 'v', s: 'n' })
    expect(isMaskedCorrelationValue('[Redacted]')).toBe(true)
    expect(isMaskedCorrelationValue('plain')).toBe(false)
  })

  it('does not claim Resolved when the engine list may be cut at ten', () => {
    const names = Array.from({ length: 10 }, (_, i) => `v${i}`)
    const failed = event({ success: false, diagnosis: { category: 'c', severity: 'e', cause: 'x', unresolved_variables: names } })
    const model = buildCorrelationTable({ event: failed, variableDiff: {}, action: login })
    expect(model.rows.some(row => row.variable === 'user')).toBe(false)
    expect(model.unknownReads).toEqual(['user', 'csrf'])
  })

  it('lists variable changes no extraction row covers', () => {
    const model = buildCorrelationTable({ event: event({ extractions: { a: '1' } }), variableDiff: { a: { next: '1' }, b: { previous: 'x', next: 'y' } }, laterReads: new Map([['b', 1]]) })
    expect(model.rows.map(row => [row.direction, row.variable, row.before, row.value, row.result, row.laterSteps])).toEqual([
      ['Extracts', 'a', null, '1', 'Extracted', 0],
      ['Sets', 'b', 'x', 'y', 'Changed', 1],
    ])
  })

  it('keeps a live table step to its changes and reads', () => {
    const model = buildCorrelationTable({ event: event({ action_type: 'live_table', extractions: { a: '1' }, credential_classification_error: 'x' }), variableDiff: { a: { next: '1' } } })
    expect(model.rows.map(row => [row.direction, row.result])).toEqual([['Sets', 'Changed']])
    expect(model.notes).toEqual([])
  })

  it('is empty with nothing recorded', () => {
    expect(correlationTableIsEmpty(buildCorrelationTable({ event: event(), variableDiff: {} }))).toBe(true)
  })
})

describe('correlationVariablesSet', () => {
  it('names extracted, child-extracted, failing and changed variables', () => {
    const failed = event({ success: false, error_message: 'extractor "miss" found no match', extractions: { a: '1' }, streamed_children: [{ action_name: 'c', success: true, extractions: { b: '2' } }] })
    expect(correlationVariablesSet(failed, { d: { next: 'x' } }).sort()).toEqual(['a', 'b', 'd', 'miss'])
  })
})
