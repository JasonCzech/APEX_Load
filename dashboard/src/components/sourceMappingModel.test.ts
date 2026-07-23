import { describe, expect, it } from 'vitest'

import { type SourceArchiveReviewMapping } from '../types/scripts'
import { buildDslDocument } from './dslJsonPointers'
import {
  computeCoverage,
  createSourceFilePathResolver,
  dslLineStatus,
  lineStatusForSource,
  mappingForDslLine,
  mappingForSourceLine,
  mappingMatchesFile,
  resolveSourceFilePath,
  sourceFilePathCandidates,
  statusCategory,
} from './sourceMappingModel'

function mapping(overrides: Partial<SourceArchiveReviewMapping>): SourceArchiveReviewMapping {
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

  it('globally prefers canonical and suffix paths over duplicate basenames', () => {
    const files = ['aaa-backup/Actions.java', 'src/Actions.java']
    const sourceMapping = mapping({ source_file: './src/Actions.java' })

    expect(resolveSourceFilePath(sourceMapping.source_file, files)).toBe('src/Actions.java')
    expect(resolveSourceFilePath(sourceMapping.source_file, [...files].reverse())).toBe('src/Actions.java')
    expect(mappingMatchesFile(sourceMapping, 'src/Actions.java', files)).toBe(true)
    expect(mappingMatchesFile(sourceMapping, 'aaa-backup/Actions.java', files)).toBe(false)

    const prefixedFiles = ['aaa-backup/Actions.java', 'archive/src/Actions.java']
    expect(resolveSourceFilePath('src/Actions.java', prefixedFiles)).toBe('archive/src/Actions.java')
    expect(resolveSourceFilePath('project/src/Actions.java', prefixedFiles)).toBe('archive/src/Actions.java')

    const competingSuffixes = ['archive/a/src/Actions.java', 'backup/src/Actions.java']
    expect(resolveSourceFilePath('project/a/src/Actions.java', competingSuffixes)).toBe('archive/a/src/Actions.java')
    expect(resolveSourceFilePath('src/Actions.java', competingSuffixes)).toBeNull()
  })

  it('uses basename fallback only when it is unambiguous', () => {
    expect(resolveSourceFilePath('Action.c', ['scripts/Action.c', 'vuser_end.c'])).toBe('scripts/Action.c')
    expect(mappingMatchesFile(
      mapping({ source_file: 'Action.c' }),
      'scripts/Action.c',
      ['scripts/Action.c', 'vuser_end.c'],
    )).toBe(true)

    const duplicateBasenames = ['aaa-backup/Actions.java', 'src/Actions.java']
    expect(resolveSourceFilePath('Actions.java', duplicateBasenames)).toBeNull()
    expect(sourceFilePathCandidates('Actions.java', duplicateBasenames)).toEqual(duplicateBasenames)
    expect(mappingMatchesFile(
      mapping({ source_file: 'Actions.java' }),
      'aaa-backup/Actions.java',
      duplicateBasenames,
    )).toBe(false)
    expect(mappingMatchesFile(
      mapping({ source_file: 'Actions.java' }),
      'src/Actions.java',
      duplicateBasenames,
    )).toBe(false)
  })

  it('treats case-only normalized path collisions as ambiguous', () => {
    const collidingPaths = ['Src/Actions.java', 'src/Actions.java']
    expect(resolveSourceFilePath('src/Actions.java', collidingPaths)).toBeNull()
    expect(mappingMatchesFile(
      mapping({ source_file: 'src/Actions.java' }),
      'src/Actions.java',
      collidingPaths,
    )).toBe(false)
  })

  it('indexes a high-cardinality path set once and reuses it across files and mappings', () => {
    const paths = Array.from(
      { length: 4096 },
      (_, index) => `archive-${String(index).padStart(4, '0')}/src/Action${index}.java`,
    )
    let iteratorRequests = 0
    const trackedPaths = new Proxy(paths, {
      get(target, property, receiver) {
        if (property === Symbol.iterator) {
          iteratorRequests += 1
        }
        return Reflect.get(target, property, receiver)
      },
    })

    const resolver = createSourceFilePathResolver(trackedPaths)
    expect(iteratorRequests).toBe(1)

    const mappings = Array.from(
      { length: 1000 },
      (_, index) => mapping({
        id: `mapping-${index}`,
        source_file: `src/Action${index}.java`,
      }),
    )
    const capturedFiles = paths.slice(0, 20)
    for (const filePath of capturedFiles) {
      for (const sourceMapping of mappings) {
        mappingMatchesFile(sourceMapping, filePath, resolver)
      }
    }

    expect(iteratorRequests).toBe(1)
    expect(resolveSourceFilePath('src/Action999.java', resolver)).toBe(paths[999])
    expect(mappingMatchesFile(mappings[0], paths[0], resolver)).toBe(true)
    expect(mappingMatchesFile(mappings[0], paths[1], resolver)).toBe(false)
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

  it('does not colour a duplicate-basename file that the mapping does not identify', () => {
    const files = ['aaa-backup/Actions.java', 'src/Actions.java']
    const mappings = [
      mapping({ source_file: 'src/Actions.java', start_line: 2, end_line: 2 }),
    ]

    expect(lineStatusForSource(mappings, 'aaa-backup/Actions.java', files).has(2)).toBe(false)
    expect(lineStatusForSource(mappings, 'src/Actions.java', files).get(2)).toBe('converted')
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

  it('does not resolve a source line through an unrelated duplicate basename', () => {
    const files = ['aaa-backup/Actions.java', 'src/Actions.java']
    const sourceMapping = mapping({
      id: 'real-actions',
      source_file: 'src/Actions.java',
      start_line: 4,
      end_line: 4,
    })

    expect(mappingForSourceLine([sourceMapping], 'aaa-backup/Actions.java', 4, files)).toBeNull()
    expect(mappingForSourceLine([sourceMapping], 'src/Actions.java', 4, files)?.id).toBe('real-actions')
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
