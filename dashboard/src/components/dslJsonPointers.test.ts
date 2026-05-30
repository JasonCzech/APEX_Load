import { describe, expect, it } from 'vitest'

import { buildDslDocument, lineRangeForTargetPath } from './dslJsonPointers'

describe('buildDslDocument', () => {
  it('emits text identical to JSON.stringify with 2-space indent', () => {
    const script = {
      name: 'Order_Flow',
      version: '1.0.0',
      config: { base_url: 'https://example.test', think_time: { type: 'fixed', mean_ms: 1000 } },
      actions: [
        { name: 'GET_Home', type: 'GET', target: '/', extractors: [{ name: 'sid', type: 'regex' }] },
        { name: 'POST_Order', type: 'POST', target: '/orders', assertions: [{ type: 'status_code', expected: 200 }] },
      ],
      parameters: [],
      data_sources: [{ name: 'users', type: 'csv' }],
    }
    const { text } = buildDslDocument(script)
    expect(text).toBe(JSON.stringify(script, null, 2))
  })

  it('records line ranges that match where each value renders', () => {
    const script = {
      config: { base_url: 'https://example.test', think_time: { type: 'fixed', mean_ms: 1000 } },
      actions: [{ name: 'GET_Home', type: 'GET' }],
    }
    const { text, ranges } = buildDslDocument(script)
    const lines = text.split('\n')

    const assertRange = (path: string, expectedFirst: string, expectedLast: string) => {
      const range = ranges.get(path)
      expect(range, `range for ${path}`).toBeTruthy()
      expect(lines[range!.startLine - 1]).toContain(expectedFirst)
      expect(lines[range!.endLine - 1]).toContain(expectedLast)
    }

    // Object spans from its opening brace line to its closing brace line.
    assertRange('config', '"config": {', '}')
    // Nested object.
    assertRange('config.think_time', '"think_time": {', '}')
    // Scalar: start and end land on the same single line.
    const meanRange = ranges.get('config.think_time.mean_ms')!
    expect(meanRange.startLine).toBe(meanRange.endLine)
    expect(lines[meanRange.startLine - 1]).toContain('"mean_ms": 1000')
    // Array element addressed with bracket notation.
    assertRange('actions[0]', '{', '}')
    expect(ranges.get('actions[0].name')).toBeTruthy()
  })

  it('omits undefined object members like JSON.stringify does', () => {
    const script = { a: 1, b: undefined, c: 3 }
    const { text } = buildDslDocument(script)
    expect(text).toBe(JSON.stringify(script, null, 2))
    expect(text).not.toContain('"b"')
  })

  it('renders empty objects and arrays inline', () => {
    const script = { actions: [], config: {} }
    const { text, ranges } = buildDslDocument(script)
    expect(text).toBe(JSON.stringify(script, null, 2))
    const actions = ranges.get('actions')!
    expect(actions.startLine).toBe(actions.endLine)
  })
})

describe('lineRangeForTargetPath', () => {
  const { ranges } = buildDslDocument({
    config: { think_time: { mean_ms: 1000 } },
    actions: [{ name: 'a', extractors: [{ name: 'sid' }] }],
  })

  it('resolves bracket-notation paths directly', () => {
    expect(lineRangeForTargetPath(ranges, 'actions[0].extractors[0]')).toBeTruthy()
    expect(lineRangeForTargetPath(ranges, 'config.think_time')).toBeTruthy()
  })

  it('resolves dotted index forms', () => {
    expect(lineRangeForTargetPath(ranges, 'actions.0.extractors.0')).toEqual(
      lineRangeForTargetPath(ranges, 'actions[0].extractors[0]'),
    )
  })

  it('returns null for empty or unknown paths', () => {
    expect(lineRangeForTargetPath(ranges, '')).toBeNull()
    expect(lineRangeForTargetPath(ranges, null)).toBeNull()
    expect(lineRangeForTargetPath(ranges, 'actions[5]')).toBeNull()
  })
})
