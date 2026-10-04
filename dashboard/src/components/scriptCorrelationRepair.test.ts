import { describe, expect, it } from 'vitest'

import {
  addCorrelationExtractorIfMissing,
  applyCorrelationRepairToAction,
  correlationEditableActionsKey,
  correlationExtractorScope,
  correlationRepairRequiresStructuralBodyReview,
  currentCorrelationEditableActionsKey,
  resolveCorrelationExtractorAndRepair,
  resolveCorrelationExtractorName,
} from './scriptCorrelationRepair'
import type { ScriptActionDefinition } from '../types/scripts'

describe('applyCorrelationRepairToAction', () => {
  it('replaces stale diagnosed values in replayed request fields only', () => {
    const action: ScriptActionDefinition = {
      name: 'Use OLD literally in name',
      type: 'POST',
      method: 'POST',
      target: '/orders?csrf=OLD',
      headers: { Authorization: 'Bearer OLD', 'X-Static': 'ok' },
      header_list: [{ name: 'Authorization', value: 'Bearer OLD' }],
      body: '{"csrf":"OLD"}',
      value: 'OLD',
      params: { token: 'OLD', nested: ['keep', 'OLD'] },
      metadata: { recorded: 'OLD' },
      extractors: [{ name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.OLD' }],
    }

    const repaired = applyCorrelationRepairToAction(action, {
      failingActionIndex: 1,
      requestLocations: [
        { field: 'request_url', start: 13, end: 16 },
        { field: 'request_body', start: 9, end: 12 },
        { field: 'request_headers.Authorization', start: 7, end: 10 },
      ],
      replacement: '{{var.csrf}}',
    })

    expect(repaired.target).toBe('/orders?csrf={{var.csrf}}')
    expect(repaired.headers?.Authorization).toBe('Bearer {{var.csrf}}')
    expect(repaired.header_list?.[0].value).toBe('Bearer {{var.csrf}}')
    expect(repaired.body).toBe('{"csrf":"{{var.csrf}}"}')
    expect(repaired.value).toBe('OLD')
    expect(repaired.params).toEqual({ token: 'OLD', nested: ['keep', 'OLD'] })
    expect(repaired.name).toBe('Use OLD literally in name')
    expect(repaired.metadata).toEqual({ recorded: 'OLD' })
    expect(repaired.extractors?.[0].expression).toBe('$.OLD')
  })

  it('returns the original action when no replay request field contains the stale value', () => {
    const action: ScriptActionDefinition = {
      name: 'Submit',
      type: 'POST',
      method: 'POST',
      target: '/orders',
      headers: { Authorization: 'Bearer live' },
      params: { page: '1' },
    }

    const repaired = applyCorrelationRepairToAction(action, {
      failingActionIndex: 1,
      requestLocations: [{ field: 'request_headers.Authorization', start: 70, end: 73 }],
      replacement: '{{var.csrf}}',
    })

    expect(repaired).toBe(action)
  })

  it('rejects all request edits when a body repair would invalidate retained Aura template occurrences', () => {
    const body = `message=${encodeURIComponent('{"actions":[{"id":"a","descriptor":"fixture","params":{"echo":"OLD \\u007b\\u007bvar.literal\\u007d\\u007d"}}]}')}`
    const action: ScriptActionDefinition = {
      name: 'Aura submit', type: 'POST', target: '/submit?csrf=OLD', body,
      headers: { 'X-CSRF': 'OLD' },
      metadata: {
        body_template_value_encoding: 'aura_json_strings',
        body_aura_template_contract: {
          version: 1,
          form_wire_occurrences: [],
          literal_occurrences: [{ context_id: 'a'.repeat(64), occurrence_index: 0, expression_sha256: 'b'.repeat(64) }],
        },
      },
    }
    const bodyStart = body.indexOf('OLD')
    const repair = {
      failingActionIndex: 1,
      requestLocations: [
        { field: 'request_url', start: 13, end: 16 },
        { field: 'request_body', start: bodyStart, end: bodyStart + 3 },
        { field: 'request_headers.X-CSRF', start: 0, end: 3 },
      ],
      replacement: '{{var.csrf}}',
    }

    expect(correlationRepairRequiresStructuralBodyReview(action, repair)).toBe(true)
    const repaired = applyCorrelationRepairToAction(action, repair)
    expect(repaired).toBe(action)
    expect(repaired.body).toBe(body)
    expect(repaired.target).toBe('/submit?csrf=OLD')
    expect(repaired.headers).toEqual({ 'X-CSRF': 'OLD' })
  })

  it('allows URL and header repairs on an Aura action without changing its body or template contract', () => {
    const action: ScriptActionDefinition = {
      name: 'Aura submit', type: 'POST', target: '/submit?csrf=OLD',
      body: 'message=retained-body',
      headers: { 'X-CSRF': 'OLD' },
      metadata: {
        body_template_value_encoding: 'aura_json_strings',
        body_aura_template_contract: { version: 1, form_wire_occurrences: [], literal_occurrences: [] },
      },
    }
    const repair = {
      failingActionIndex: 1,
      requestLocations: [
        { field: 'request_url', start: 13, end: 16 },
        { field: 'request_headers.X-CSRF', start: 0, end: 3 },
      ],
      replacement: '{{var.csrf}}',
    }

    expect(correlationRepairRequiresStructuralBodyReview(action, repair)).toBe(false)
    const repaired = applyCorrelationRepairToAction(action, repair)
    expect(repaired.target).toBe('/submit?csrf={{var.csrf}}')
    expect(repaired.headers).toEqual({ 'X-CSRF': '{{var.csrf}}' })
    expect(repaired.body).toBe(action.body)
    expect(repaired.metadata).toBe(action.metadata)
  })

  it('replaces raw and exact query-encoded literals in one non-recursive pass', () => {
    const action: ScriptActionDefinition = {
      name: 'Submit',
      type: 'POST',
      target: '/submit?csrf=token%2B%2Fvalue',
      body: 'raw=token+/value&fresh=token+/value-rotated',
    }

    const repaired = applyCorrelationRepairToAction(action, {
      failingActionIndex: 1,
      requestLocations: [
        { field: 'request_url', start: 13, end: 29 },
        { field: 'request_body', start: 4, end: 16 },
      ],
      replacement: '{{var.csrf_encoded}}',
    })

    expect(repaired.target).toBe('/submit?csrf={{var.csrf_encoded}}')
    expect(repaired.body).toBe('raw={{var.csrf_encoded}}&fresh=token+/value-rotated')
  })

  it('limits a location-backed repair to the diagnosed request surface', () => {
    const action: ScriptActionDefinition = {
      name: 'Submit',
      type: 'POST',
      target: '/OLD',
      body: 'csrf=OLD',
    }
    const repaired = applyCorrelationRepairToAction(action, {
      failingActionIndex: 1,
      requestLocations: [{ field: 'request_body', start: 5, end: 8 }],
      replacement: '{{var.csrf}}',
    })
    expect(repaired.target).toBe('/OLD')
    expect(repaired.body).toBe('csrf={{var.csrf}}')
  })

  it('repairs only the diagnosed header name in maps and repeated header lists', () => {
    const action: ScriptActionDefinition = {
      name: 'Submit',
      type: 'POST',
      target: '/submit',
      headers: { authorization: 'Bearer OLD', 'X-Audit': 'OLD' },
      header_list: [
        { name: 'Authorization', value: 'Bearer OLD' },
        { name: 'X-Audit', value: 'OLD' },
      ],
    }
    const repaired = applyCorrelationRepairToAction(action, {
      failingActionIndex: 1,
      requestLocations: [{ field: 'request_headers.Authorization', start: 7, end: 10 }],
      replacement: '{{var.csrf}}',
    })
    expect(repaired.headers).toEqual({ Authorization: 'Bearer {{var.csrf}}', 'X-Audit': 'OLD' })
    expect(repaired.header_list).toEqual([
      { name: 'Authorization', value: 'Bearer {{var.csrf}}' },
      { name: 'X-Audit', value: 'OLD' },
    ])
  })

  it('maps a repeated-header offset to only the matching header-list occurrence', () => {
    const action: ScriptActionDefinition = {
      name: 'Submit',
      type: 'GET',
      target: '/submit',
      headers: { 'X-Session': 'first\nSECOND' },
      header_list: [
        { name: 'X-Session', value: 'first' },
        { name: 'x-session', value: 'SECOND' },
        { name: 'X-Audit', value: 'SECOND' },
      ],
    }
    const repaired = applyCorrelationRepairToAction(action, {
      failingActionIndex: 1,
      requestLocations: [{ field: 'request_headers.X-Session', start: 6, end: 12 }],
      replacement: '{{var.session}}',
    })
    expect(repaired.headers).toEqual({
      'x-session': '{{var.session}}',
      'X-Audit': 'SECOND',
    })
    expect(repaired.header_list).toEqual([
      { name: 'X-Session', value: 'first' },
      { name: 'x-session', value: '{{var.session}}' },
      { name: 'X-Audit', value: 'SECOND' },
    ])
  })

  it('groups header-list occurrences by trimmed name exactly like the server aggregate', () => {
    // The server (rawAuthoredReplayRequestHeaders) trims header names before
    // grouping, so both entries below share the aggregate 'AAAA\nBBBB' and the
    // diagnosed range [0,4) addresses the FIRST occurrence. A client that
    // skips the whitespace-named entry would map the range onto 'BBBB'.
    const action: ScriptActionDefinition = {
      name: 'Submit',
      type: 'GET',
      target: '/submit',
      header_list: [
        { name: 'X-Session ', value: 'AAAA' },
        { name: 'X-Session', value: 'BBBB' },
      ],
    }
    const repaired = applyCorrelationRepairToAction(action, {
      failingActionIndex: 1,
      requestLocations: [{ field: 'request_headers.X-Session', start: 0, end: 4 }],
      replacement: '{{var.session}}',
    })
    expect(repaired.header_list).toEqual([
      { name: 'X-Session ', value: '{{var.session}}' },
      { name: 'X-Session', value: 'BBBB' },
    ])
  })

  it('rejects the whole repair when one of several spans is out of bounds', () => {
    const action: ScriptActionDefinition = {
      name: 'Submit',
      type: 'POST',
      target: '/submit?csrf=OLD',
      body: 'csrf=OLD',
    }
    const repaired = applyCorrelationRepairToAction(action, {
      failingActionIndex: 1,
      requestLocations: [
        { field: 'request_url', start: 13, end: 16 },
        { field: 'request_body', start: 99, end: 102 },
      ],
      replacement: '{{var.csrf}}',
    })
    expect(repaired).toBe(action)
  })

  it('rejects the whole repair when valid and invalid spans share one surface', () => {
    const action: ScriptActionDefinition = { name: 'Submit', type: 'POST', target: '/submit', body: 'first=OLD&second=OLD' }
    const repaired = applyCorrelationRepairToAction(action, {
      failingActionIndex: 1,
      requestLocations: [
        { field: 'request_body', start: 6, end: 9 },
        { field: 'request_body', start: 200, end: 203 },
      ],
      replacement: '{{var.csrf}}',
    })
    expect(repaired).toBe(action)
  })

  it('rejects the whole repair when any diagnosed header cannot be mapped', () => {
    const action: ScriptActionDefinition = { name: 'Submit', type: 'POST', target: '/submit', body: 'csrf=OLD' }
    const repaired = applyCorrelationRepairToAction(action, {
      failingActionIndex: 1,
      requestLocations: [
        { field: 'request_body', start: 5, end: 8 },
        { field: 'request_headers.X-CSRF', start: 0, end: 3 },
      ],
      replacement: '{{var.csrf}}',
    })
    expect(repaired).toBe(action)
  })

  it('rejects a repeated-header range that touches the aggregate delimiter', () => {
    const action: ScriptActionDefinition = {
      name: 'Submit',
      type: 'GET',
      target: '/submit',
      header_list: [
        { name: 'X-Session', value: 'first' },
        { name: 'X-Session', value: 'second' },
      ],
    }
    const repaired = applyCorrelationRepairToAction(action, {
      failingActionIndex: 1,
      requestLocations: [{ field: 'request_headers.X-Session', start: 4, end: 7 }],
      replacement: '{{var.session}}',
    })
    expect(repaired).toBe(action)
  })

  it('does not append another extractor with the same variable name', () => {
    const action: ScriptActionDefinition = {
      name: 'Login',
      type: 'POST',
      target: '/login',
      extractors: [{ name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf' }],
    }
    const next = addCorrelationExtractorIfMissing(action, {
      name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf',
    })
    expect(next).toBe(action)
    expect(next.extractors).toHaveLength(1)
  })

  it('claims a new variable name when an existing extractor name has a different identity', () => {
    const action: ScriptActionDefinition = {
      name: 'Login',
      type: 'POST',
      target: '/login',
      extractors: [
        { name: 'csrf', source: 'header', type: 'header', expression: 'X-CSRF' },
        { name: 'csrf_2', source: 'body', type: 'jsonpath', expression: '$.other' },
      ],
    }
    const resolved = resolveCorrelationExtractorName(action, [action], {
      name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf',
    })
    expect(resolved.name).toBe('csrf_3')
    expect(resolved.expression).toBe('$.csrf')
  })

  it('resolves names across the whole script and updates the repair placeholder', () => {
    const source: ScriptActionDefinition = { name: 'Login', type: 'POST', target: '/login' }
    const other: ScriptActionDefinition = {
      name: 'Other',
      type: 'GET',
      target: '/other',
      extractors: [{ name: 'csrf', source: 'header', type: 'header', expression: 'X-CSRF' }],
    }
    const { extractor, repair } = resolveCorrelationExtractorAndRepair(
      source,
      [source, other],
      { name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf' },
      {
        failingActionIndex: 1,
        requestLocations: [{ field: 'request_body', start: 5, end: 8 }],
        replacement: '{{var.csrf}}',
      },
    )
    expect(extractor.name).toBe('csrf_2')
    expect(repair?.replacement).toBe('{{var.csrf_2}}')
  })

  it('does not reuse an identical global variable name from a different source action', () => {
    const source: ScriptActionDefinition = { name: 'Login', type: 'POST', target: '/login' }
    const other: ScriptActionDefinition = {
      name: 'Other login',
      type: 'POST',
      target: '/other-login',
      extractors: [{ name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf' }],
    }
    const resolved = resolveCorrelationExtractorName(
      source,
      [source, other],
      { name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf' },
    )
    expect(resolved.name).toBe('csrf_2')
  })

  it('reuses an identical extractor name already present on the source action', () => {
    const source: ScriptActionDefinition = {
      name: 'Login',
      type: 'POST',
      target: '/login',
      extractors: [{ name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf', on_miss: 'fail' }],
    }
    const resolved = resolveCorrelationExtractorName(
      source,
      [source],
      { name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf', on_miss: 'fail' },
    )
    expect(resolved.name).toBe('csrf')
  })

  it('does not reuse an identical-but-fail-open extractor for a correlation repair', () => {
    const source: ScriptActionDefinition = {
      name: 'Login',
      type: 'POST',
      target: '/login',
      extractors: [{ name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf', on_miss: 'ignore' }],
    }
    const { extractor, repair } = resolveCorrelationExtractorAndRepair(
      source,
      [source],
      { name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf', on_miss: 'fail' },
      {
        failingActionIndex: 1,
        requestLocations: [{ field: 'request_body', start: 5, end: 8 }],
        replacement: '{{var.csrf}}',
      },
    )
    const updated = addCorrelationExtractorIfMissing(source, extractor)

    expect(extractor.name).toBe('csrf_2')
    expect(extractor.on_miss).toBe('fail')
    expect(repair?.replacement).toBe('{{var.csrf_2}}')
    expect(updated.extractors).toEqual([
      { name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf', on_miss: 'ignore' },
      { name: 'csrf_2', source: 'body', type: 'jsonpath', expression: '$.csrf', on_miss: 'fail' },
    ])
  })

  it('does not reuse a source extractor name when that name is also occupied elsewhere', () => {
    const source: ScriptActionDefinition = {
      name: 'Login',
      type: 'POST',
      target: '/login',
      extractors: [{ name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf' }],
    }
    const other: ScriptActionDefinition = {
      name: 'Other',
      type: 'GET',
      target: '/other',
      extractors: [{ name: 'csrf', source: 'header', type: 'header', expression: 'X-CSRF' }],
    }
    const resolved = resolveCorrelationExtractorName(
      source,
      [source, other],
      { name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf' },
    )
    expect(resolved.name).toBe('csrf_2')
  })

  it('resolves global extractor conflicts in nested control-flow branches', () => {
    const source: ScriptActionDefinition = { name: 'Login', type: 'POST', target: '/login' }
    const nestedStep: ScriptActionDefinition = {
      name: 'Nested step',
      type: 'GET',
      target: '/step',
      extractors: [{ name: 'csrf', source: 'header', type: 'header', expression: 'X-CSRF' }],
    }
    const nestedElse: ScriptActionDefinition = {
      name: 'Nested else',
      type: 'GET',
      target: '/else',
      extractors: [{ name: 'csrf_2', source: 'body', type: 'jsonpath', expression: '$.other' }],
    }
    const control: ScriptActionDefinition = {
      name: 'Branch',
      type: 'control_if',
      target: '',
      control: { steps: [nestedStep], else: [nestedElse] },
    }
    const scope = correlationExtractorScope({ init: [], actions: [source, control], teardown: [] })

    expect(scope).toEqual([source, control, nestedStep, nestedElse])
    expect(resolveCorrelationExtractorName(
      source,
      scope,
      { name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf' },
    ).name).toBe('csrf_3')
  })

  it('resolves extractor conflicts against http_sequence child requests', () => {
    const source: ScriptActionDefinition = { name: 'Login', type: 'POST', target: '/login' }
    const sequenceChild: ScriptActionDefinition = {
      name: 'Sequence child',
      type: 'POST',
      target: '/token',
      extractors: [{ name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.token' }],
    }
    const sequence: ScriptActionDefinition = {
      name: 'Token sequence',
      type: 'http_sequence',
      target: '',
      params: { requests: [sequenceChild] },
    }
    const scope = correlationExtractorScope({ init: [], actions: [source, sequence], teardown: [] })

    expect(scope).toEqual([source, sequence, sequenceChild])
    // The child's 'csrf' occupies the shared VUser namespace, so a new
    // extractor claiming that name must be renamed.
    expect(resolveCorrelationExtractorName(
      source,
      scope,
      { name: 'csrf', source: 'header', type: 'header', expression: 'X-CSRF' },
    ).name).toBe('csrf_2')
  })

  it('keys the replay safety snapshot on editable action content and order', () => {
    const script = {
      init: [],
      actions: [{ name: 'Submit', type: 'POST', target: '/submit', body: 'csrf=OLD' }],
      teardown: [],
    }
    const key = correlationEditableActionsKey(script)
    expect(correlationEditableActionsKey({ ...script, actions: [{ ...script.actions[0], body: 'csrf=NEW' }] })).not.toBe(key)
    expect(correlationEditableActionsKey({ ...script, actions: [...script.actions] })).toBe(key)
  })

  it('does not trust a replay safety snapshot while the JSON draft is dirty or invalid', () => {
    const script = {
      init: [],
      actions: [{ name: 'Submit', type: 'POST', target: '/submit', body: 'csrf=OLD' }],
      teardown: [],
    }
    const key = correlationEditableActionsKey(script)

    expect(currentCorrelationEditableActionsKey(script, false, null)).toBe(key)
    expect(currentCorrelationEditableActionsKey(script, true, null)).toBeNull()
    expect(currentCorrelationEditableActionsKey(script, false, 'Invalid JSON')).toBeNull()
  })
})
