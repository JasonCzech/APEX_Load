import { describe, expect, it } from 'vitest'

import { boundaryExpression, evaluateExtractor } from './extractorMatch'

describe('evaluateExtractor — regex', () => {
  it('returns the first capture group when present, else the whole match', () => {
    const withGroup = evaluateExtractor('regex', 'csrf"\\s*:\\s*"([^"]+)"', '{"csrf":"abc123"}')
    expect(withGroup.matches).toEqual(['abc123'])
    expect(withGroup.previewable).toBe(true)

    const wholeMatch = evaluateExtractor('regex', 'order-\\d+', 'see order-42 and order-43')
    expect(wholeMatch.matches).toEqual(['order-42', 'order-43'])
  })

  it('reports invalid regex without throwing', () => {
    const result = evaluateExtractor('regex', '([', 'anything')
    expect(result.matches).toEqual([])
    expect(result.error).toMatch(/invalid regular expression/i)
  })

  it('does not hang on a zero-width match', () => {
    const result = evaluateExtractor('regex', 'a*', 'aa bb')
    expect(result.matches.length).toBeGreaterThan(0)
  })
})

describe('evaluateExtractor — json path', () => {
  const body = JSON.stringify({ data: { id: 4471, items: [{ sku: 'A' }, { sku: 'B' }] }, token: null })

  it('resolves dotted and indexed paths', () => {
    expect(evaluateExtractor('jsonpath', '$.data.id', body).matches).toEqual(['4471'])
    expect(evaluateExtractor('jsonpath', 'data.items[1].sku', body).matches).toEqual(['B'])
  })

  it('expands an array result into one match per element', () => {
    expect(evaluateExtractor('json', '$.data.items', body).matches).toEqual(['{"sku":"A"}', '{"sku":"B"}'])
  })

  it('returns no matches for a missing path and reports non-JSON bodies', () => {
    expect(evaluateExtractor('jsonpath', '$.data.missing', body).matches).toEqual([])
    const notJson = evaluateExtractor('jsonpath', '$.x', '<html>nope</html>')
    expect(notJson.matches).toEqual([])
    expect(notJson.error).toMatch(/not valid json/i)
  })
})

describe('evaluateExtractor — non-previewable types', () => {
  it('marks boundary/xpath/css as not previewable', () => {
    for (const type of ['xpath', 'css', 'jmespath', 'sse_event']) {
      const result = evaluateExtractor(type, 'whatever', 'body')
      expect(result.previewable).toBe(false)
      expect(result.matches).toEqual([])
    }
  })
})

describe('evaluateExtractor — boundaries', () => {
  const body = 'a <input name="csrf" value="tok1"> b <input name="csrf" value="tok2">'
  it('finds each value between the boundaries, the first being what is saved', () => {
    expect(evaluateExtractor('boundary', 'value="|||"', body).matches).toEqual(['tok1', 'tok2'])
    expect(evaluateExtractor('boundary', 'nothing|||"', body).matches).toEqual([])
    expect(evaluateExtractor('boundary', 'no separator', body).error).toContain('left|||right')
  })
  it('reads the engine’s configured form, ordinal and case included', () => {
    expect(evaluateExtractor('boundary', '__apexload_boundary_config__:{"left":"VALUE=\\"","right":"\\"","ordinal":2,"case_insensitive":true}', body).matches).toEqual(['tok2'])
    expect(boundaryExpression('a', 'b')).toBe('a|||b')
    expect(evaluateExtractor('boundary', boundaryExpression('x|||', '|'), 'x|||val|').matches).toEqual(['val'])
  })
})

