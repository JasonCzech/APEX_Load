import { describe, expect, it } from 'vitest'

import {
  buildParameterizeSelectionRequest,
  buildReplayStartPayload,
  countUniqueColumnValues,
  detectInlineDataDelimiter,
  findJsonStringOccurrences,
  inferDelimiter,
  parseInlineData,
  parseReplayVariablesText,
  resolveJsonStringSelection,
  stringifyInlineData,
} from './scriptEditorUtils'
import { type ScriptDefinition } from '../types/scripts'

describe('resolveJsonStringSelection', () => {
  const text = JSON.stringify(
    { script: { name: 'Order Flow', config: { base_url: 'https://api.example.com' } } },
    null,
    2,
  )
  const offsetsOf = (needle: string) => {
    const from = text.indexOf(needle)
    return { from, to: from + needle.length }
  }

  it('accepts a selection inside a string value and clamps to the interior', () => {
    const { from, to } = offsetsOf('Order Flow')
    const result = resolveJsonStringSelection(text, from, to)
    expect(result.ok).toBe(true)
    expect(result.selectedText).toBe('Order Flow')
    expect(text.slice(result.innerFrom, result.innerTo)).toBe('Order Flow')
  })

  it('accepts a partial selection inside a string value', () => {
    const { from, to } = offsetsOf('api.example.com')
    const result = resolveJsonStringSelection(text, from, to)
    expect(result.ok).toBe(true)
    expect(result.selectedText).toBe('api.example.com')
  })

  it('clamps a selection that includes the surrounding quotes', () => {
    const { from, to } = offsetsOf('"Order Flow"')
    const result = resolveJsonStringSelection(text, from, to)
    expect(result.ok).toBe(true)
    expect(result.selectedText).toBe('Order Flow')
  })

  it('rejects a selection over a property key', () => {
    const { from, to } = offsetsOf('base_url')
    expect(resolveJsonStringSelection(text, from, to).ok).toBe(false)
  })

  it('rejects a selection that spans structural tokens', () => {
    const from = text.indexOf('Order Flow') + 'Order Flow'.length
    const to = text.indexOf('base_url')
    expect(to).toBeGreaterThan(from)
    expect(resolveJsonStringSelection(text, from, to).ok).toBe(false)
  })

  it('rejects an empty selection', () => {
    const from = text.indexOf('Order Flow')
    expect(resolveJsonStringSelection(text, from, from).ok).toBe(false)
  })
})

describe('buildParameterizeSelectionRequest', () => {
  const text = JSON.stringify(
    { script: { name: 'Order Flow', config: { base_url: 'https://api.example.com' } } },
    null,
    2,
  )

  it('returns the clamped range and text for a string-value selection', () => {
    const from = text.indexOf('api.example.com')
    const to = from + 'api.example.com'.length
    const request = buildParameterizeSelectionRequest(text, from, to)
    expect(request).not.toBeNull()
    expect(request?.selectedText).toBe('api.example.com')
    expect(text.slice(request!.start, request!.end)).toBe('api.example.com')
  })

  it('returns null for a property-key selection', () => {
    const from = text.indexOf('base_url')
    expect(buildParameterizeSelectionRequest(text, from, from + 'base_url'.length)).toBeNull()
  })

  it('returns null for an empty selection', () => {
    const from = text.indexOf('Order Flow')
    expect(buildParameterizeSelectionRequest(text, from, from)).toBeNull()
  })
})

describe('findJsonStringOccurrences', () => {
  it('finds exact values in nested objects and arrays', () => {
    const text = JSON.stringify({
      config: { base_url: 'https://api.example.com' },
      actions: [
        { target: '/login', headers: { referer: '/login' } },
        { target: '/orders' },
        { target: '/login' },
      ],
    }, null, 2)
    const occurrences = findJsonStringOccurrences(text, '/login')

    expect(occurrences.map(occurrence => occurrence.path)).toEqual([
      '$.actions[0].target',
      '$.actions[0].headers.referer',
      '$.actions[2].target',
    ])
    expect(occurrences.every(occurrence => text.slice(occurrence.from, occurrence.to) === '/login')).toBe(true)
  })

  it('ignores JSON property keys', () => {
    const text = JSON.stringify({ token: 'value', nested: { token: 'value' } }, null, 2)

    expect(findJsonStringOccurrences(text, 'token')).toEqual([])
    expect(findJsonStringOccurrences(text, 'value')).toHaveLength(2)
  })

  it('ignores partial string-value matches', () => {
    const text = JSON.stringify({ exact: 'demo', partial: 'demo-user', array: ['demo'] }, null, 2)

    expect(findJsonStringOccurrences(text, 'demo').map(occurrence => occurrence.path)).toEqual([
      '$.exact',
      '$.array[0]',
    ])
  })

  it('handles escaped JSON strings safely', () => {
    const text = JSON.stringify({ first: 'a"b', second: 'a"b', other: 'a\\b' }, null, 2)
    const escapedNeedle = 'a\\"b'
    const occurrences = findJsonStringOccurrences(text, escapedNeedle)

    expect(occurrences).toHaveLength(2)
    expect(occurrences.map(occurrence => occurrence.value)).toEqual(['a"b', 'a"b'])
    expect(occurrences.every(occurrence => occurrence.rawText === escapedNeedle)).toBe(true)
  })
})

describe('scriptEditorUtils replay handoff helpers', () => {
  it('parses replay variables text into string values', () => {
    expect(parseReplayVariablesText('')).toBeUndefined()
    expect(parseReplayVariablesText('{}')).toBeUndefined()
    expect(parseReplayVariablesText('{"token":"abc","count":3,"empty":null}')).toEqual({
      token: 'abc',
      count: '3',
      empty: '',
    })
  })

  it('rejects non-object replay variables', () => {
    expect(() => parseReplayVariablesText('[]')).toThrow('Replay variables must be a JSON object')
    expect(() => parseReplayVariablesText('"token"')).toThrow('Replay variables must be a JSON object')
  })

  it('builds a browser replay payload with normalized browser defaults', () => {
    const script: ScriptDefinition = {
      name: 'Mobile Checkout',
      version: '1.0.0',
      protocol: 'browser',
      config: {
        base_url: 'https://shop.example.com',
        browser: {
          engine: 'webkit',
          headless: false,
          device_profile: 'iphone_15_pro',
          touch_mode: true,
          orientation: 'portrait',
          client_metrics: true,
          capture_screenshots: 'on_failure',
          viewport: { width: 393 },
        },
      },
      actions: [],
    }

    expect(buildReplayStartPayload(script, {
      projectId: 'project-1',
      groupName: '  ',
      replaySessionId: 'replay-1',
      variables: { token: 'abc' },
      ignoreThinkTime: true,
      startPaused: true,
    })).toEqual({
      group_name: 'Mobile Checkout-replay',
      project_id: 'project-1',
      replay_session_id: 'replay-1',
      ignore_think_time: true,
      start_paused: true,
      variables: { token: 'abc' },
      script: expect.objectContaining({
        name: 'Mobile Checkout',
        project_id: 'project-1',
      }),
      browser: {
        engine: 'webkit',
        headless: false,
        slow_mo_ms: undefined,
        user_agent: undefined,
        device_profile: 'iphone_15_pro',
        touch_mode: true,
        orientation: 'portrait',
        client_metrics: true,
        capture_screenshots: 'per_action',
        viewport: { width: 393, height: 900 },
      },
    })
  })

  it('builds a non-browser replay payload without browser-only options', () => {
    const script: ScriptDefinition = {
      name: 'API Smoke',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.com' },
      actions: [],
      project_id: 'existing-project',
    }

    expect(buildReplayStartPayload(script, {
      projectId: 'project-1',
      groupName: 'api-group',
      ignoreThinkTime: false,
      startPaused: true,
    })).toEqual({
      group_name: 'api-group',
      project_id: 'project-1',
      replay_session_id: undefined,
      ignore_think_time: false,
      start_paused: undefined,
      variables: undefined,
      script: { ...script, project_id: 'project-1' },
      browser: undefined,
    })
  })

  it('detects comma and tab inline data delimiters', () => {
    expect(detectInlineDataDelimiter('username,password\nalice,secret')).toBe(',')
    expect(detectInlineDataDelimiter('username\tpassword\nalice\tsecret')).toBe('\t')
    expect(detectInlineDataDelimiter('username\nalice')).toBeNull()
  })

  it('uses auto delimiter detection while preserving manual overrides', () => {
    const tabData = 'username\tpassword\nalice\tsecret'

    expect(inferDelimiter({
      name: 'users',
      type: 'csv',
      path: '',
      strategy: 'sequential',
      delimiter: 'auto',
      inline_data: tabData,
    })).toBe('\t')

    expect(parseInlineData({
      name: 'users',
      type: 'csv',
      path: '',
      strategy: 'sequential',
      delimiter: ',',
      first_row_header: true,
      inline_data: tabData,
    }).columns).toEqual(['username', 'password'])

    expect(parseInlineData({
      name: 'users',
      type: 'csv',
      path: '',
      strategy: 'sequential',
      delimiter: 'comma',
      first_row_header: true,
      inline_data: tabData,
    }).columns).toEqual(['username\tpassword'])
  })
})

describe('countUniqueColumnValues', () => {
  const parsed = {
    columns: ['city', 'country'],
    rows: [
      ['Paris', 'France'],
      ['Lyon', 'France'],
      ['Paris', 'France'],
      ['', ''],
    ],
  }

  it('returns 0 for empty data', () => {
    expect(countUniqueColumnValues({ columns: ['a'], rows: [] }, [0])).toBe(0)
  })

  it('counts distinct values in the mapped column', () => {
    // city column: Paris, Lyon, Paris, '' -> 2 distinct non-empty
    expect(countUniqueColumnValues(parsed, [0])).toBe(2)
    // country column: France x3, '' -> 1 distinct
    expect(countUniqueColumnValues(parsed, [1])).toBe(1)
  })

  it('counts distinct across multiple mapped columns', () => {
    // (Paris,France),(Lyon,France),(Paris,France),(,) -> 2 distinct non-empty combos
    expect(countUniqueColumnValues(parsed, [0, 1])).toBe(2)
  })

  it('falls back to all columns when no indexes are supplied', () => {
    expect(countUniqueColumnValues(parsed, [])).toBe(2)
  })
})

describe('stringifyInlineData multi-row seeding', () => {
  const source = {
    name: 'auto_params',
    type: 'csv',
    path: '',
    strategy: 'sequential',
    delimiter: 'auto',
    first_row_header: true,
  } as const

  it('emits a header plus every seed row, keeping the detected value in the first row', () => {
    const columns = ['user_id', 'email']
    // Mirrors the editor's auto_params build: row 0 holds detected literals, extra rows blank.
    const rows = [
      ['12345', 'jane@example.test'],
      ['', ''],
      ['', ''],
    ]

    const csv = stringifyInlineData(source, columns, rows)
    const lines = csv.split('\n')
    expect(lines[0]).toBe('user_id,email')
    expect(lines[1]).toBe('12345,jane@example.test')
    expect(lines).toHaveLength(4) // header + 3 seed rows

    // Round-trips back through parseInlineData (blank rows are preserved, not dropped).
    const reparsed = parseInlineData({ ...source, inline_data: csv })
    expect(reparsed.columns).toEqual(columns)
    expect(reparsed.rows[0]).toEqual(['12345', 'jane@example.test'])
    expect(reparsed.rows).toHaveLength(3)
  })
})
