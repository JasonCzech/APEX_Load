import { describe, expect, it } from 'vitest'

import { type LoadRunnerSourceReviewMapping } from '../types/scripts'
import { buildDslDocument } from './dslJsonPointers'
import {
  computeCoverage,
  dslLineStatus,
  lineStatusForSource,
  mappingForDslLine,
  mappingForSourceLine,
  mappingMatchesFile,
  statusCategory,
} from './sourceMappingModel'

function mapping(overrides: Partial<LoadRunnerSourceReviewMapping>): LoadRunnerSourceReviewMapping {
  return {
    id: overrides.id || 'm',
    source_file: overrides.source_file ?? 'Action.c',
    start_line: overrides.start_line ?? 1,
    end_line: overrides.end_line,
    function: overrides.function,
    target_path: overrides.target_path,
    target_name: overrides.target_name,
    target_kind: overrides.target_kind,
    status: overrides.status ?? 'converted',
    notes: overrides.notes,
  }
}

describe('statusCategory', () => {
  it('buckets backend statuses into stable categories', () => {
    expect(statusCategory('converted')).toBe('converted')
    expect(statusCategory('contributor')).toBe('preserved')
    expect(statusCategory('preserved')).toBe('preserved')
    expect(statusCategory('unsupported')).toBe('unsupported')
    expect(statusCategory('review_only')).toBe('review')
    expect(statusCategory('')).toBe('review')
    expect(statusCategory(undefined)).toBe('review')
  })
})

describe('computeCoverage', () => {
  it('counts mappings by category and collects unsupported functions', () => {
    const summary = computeCoverage(
      [
        mapping({ status: 'converted' }),
        mapping({ status: 'converted' }),
        mapping({ status: 'preserved' }),
        mapping({ status: 'unsupported', function: 'web_custom_request' }),
        mapping({ status: 'review_only', function: 'web_set_sockets_option' }),
        mapping({ status: 'review_only', function: 'web_set_sockets_option' }),
      ],
      2,
    )
    expect(summary.total).toBe(6)
    expect(summary.converted).toBe(2)
    expect(summary.preserved).toBe(1)
    expect(summary.review).toBe(2)
    expect(summary.unsupported).toBe(1)
    expect(summary.unsupportedFunctions).toEqual(['web_custom_request', 'web_set_sockets_option'])
    expect(summary.dslWithoutOrigin).toBe(2)
  })

  it('handles an empty mapping set', () => {
    expect(computeCoverage([])).toMatchObject({ total: 0, converted: 0, dslWithoutOrigin: 0 })
  })
})

describe('mappingMatchesFile', () => {
  it('matches on exact path, suffix, and basename', () => {
    expect(mappingMatchesFile(mapping({ source_file: 'Action.c' }), 'Action.c')).toBe(true)
    expect(mappingMatchesFile(mapping({ source_file: 'Action.c' }), 'scripts/Action.c')).toBe(true)
    expect(mappingMatchesFile(mapping({ source_file: 'a/Action.c' }), 'b/Action.c')).toBe(true)
    expect(mappingMatchesFile(mapping({ source_file: 'Action.c' }), 'vuser_init.c')).toBe(false)
  })
})

describe('lineStatusForSource', () => {
  it('colours every covered line and prefers the higher-priority status on overlap', () => {
    const result = lineStatusForSource(
      [
        mapping({ source_file: 'Action.c', start_line: 1, end_line: 3, status: 'converted' }),
        mapping({ source_file: 'Action.c', start_line: 2, end_line: 2, status: 'review_only' }),
        mapping({ source_file: 'other.c', start_line: 1, end_line: 1, status: 'converted' }),
      ],
      'Action.c',
    )
    expect(result.get(1)).toBe('converted')
    expect(result.get(2)).toBe('review') // review outranks converted on the shared line
    expect(result.get(3)).toBe('converted')
    expect(result.has(4)).toBe(false)
  })
})

describe('dslLineStatus', () => {
  const script = {
    config: { base_url: 'https://x.test', think_time: { type: 'fixed' } },
    actions: [
      { name: 'GET_Home', type: 'GET', extractors: [{ name: 'sid' }] },
      { name: 'Synthesized', type: 'POST' },
    ],
  }
  const { ranges } = buildDslDocument(script)

  it('colours mapped DSL lines and flags notable nodes with no origin', () => {
    const mappings = [
      mapping({ target_path: 'actions[0]', status: 'converted' }),
      mapping({ target_path: 'actions[0].extractors[0]', status: 'preserved' }),
    ]
    const { lines, withoutOrigin } = dslLineStatus(ranges, mappings)

    // actions[1] has no mapping → flagged no-origin.
    expect(withoutOrigin).toContain('actions[1]')
    expect(withoutOrigin).not.toContain('actions[0]')

    const action1 = ranges.get('actions[1]')!
    expect(lines.get(action1.startLine)).toBe('no-origin')

    const extractor = ranges.get('actions[0].extractors[0]')!
    expect(lines.get(extractor.startLine)).toBe('preserved')
  })

  it('treats an ancestor mapping as covering its descendants', () => {
    const { withoutOrigin } = dslLineStatus(ranges, [
      mapping({ target_path: 'actions[0]', status: 'converted' }),
      mapping({ target_path: 'actions[1]', status: 'converted' }),
    ])
    expect(withoutOrigin).toEqual([])
  })
})

describe('mappingForSourceLine / mappingForDslLine', () => {
  const script = { actions: [{ name: 'GET_Home', extractors: [{ name: 'sid' }] }] }
  const { ranges } = buildDslDocument(script)

  it('returns the tightest covering mapping for a source line', () => {
    const broad = mapping({ id: 'broad', start_line: 1, end_line: 10 })
    const tight = mapping({ id: 'tight', start_line: 4, end_line: 5 })
    expect(mappingForSourceLine([broad, tight], 'Action.c', 4)?.id).toBe('tight')
    expect(mappingForSourceLine([broad, tight], 'Action.c', 9)?.id).toBe('broad')
    expect(mappingForSourceLine([broad, tight], 'Action.c', 99)).toBeNull()
  })

  it('returns the tightest covering mapping for a DSL line', () => {
    const actionLine = ranges.get('actions[0]')!.startLine
    const extractorRange = ranges.get('actions[0].extractors[0]')!
    const mappings = [
      mapping({ id: 'action', target_path: 'actions[0]' }),
      mapping({ id: 'extractor', target_path: 'actions[0].extractors[0]' }),
    ]
    expect(mappingForDslLine(ranges, mappings, actionLine)?.id).toBe('action')
    expect(mappingForDslLine(ranges, mappings, extractorRange.startLine)?.id).toBe('extractor')
  })
})
