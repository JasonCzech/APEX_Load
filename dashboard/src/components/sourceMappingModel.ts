// Pure helpers behind the conversion-validation view. They turn the backend's
// SourceReviewMapping records into the data the two linked code panes need:
//   - coverage counts for the confidence header,
//   - per-line status for the source and DSL gutters (the "purity" signal),
//   - click → mapping resolution for bidirectional linking.
// Everything here is side-effect free so it can be unit tested without a DOM.

import { type LoadRunnerSourceReviewMapping } from '../types/scripts'
import { type LineRange, lineRangeForTargetPath } from './dslJsonPointers'

export type MappingCategory = 'converted' | 'preserved' | 'review' | 'unsupported'
export type GutterStatus = MappingCategory | 'no-origin'

// Higher number wins when two mappings cover the same line: surface the entry
// that most needs a reviewer's attention rather than a clean "converted".
const CATEGORY_PRIORITY: Record<MappingCategory, number> = {
  unsupported: 4,
  review: 3,
  preserved: 2,
  converted: 1,
}

// Top-level DSL entities whose presence should be traceable to source. config is
// deliberately excluded: much of it is synthesized runtime defaults that legitimately
// have no source origin, so flagging it would be noise rather than signal.
const NOTABLE_NODE_PATTERN = /^(actions|init|teardown|parameters|data_sources)\[\d+\]$/

export function statusCategory(status?: string): MappingCategory {
  switch ((status || '').trim().toLowerCase()) {
    case 'converted':
      return 'converted'
    case 'contributor':
    case 'preserved':
      return 'preserved'
    case 'unsupported':
      return 'unsupported'
    default:
      // '', 'review', 'review_only', and anything unrecognized.
      return 'review'
  }
}

export interface CoverageSummary {
  total: number
  converted: number
  preserved: number
  review: number
  unsupported: number
  /** Distinct LoadRunner function names that did not convert cleanly. */
  unsupportedFunctions: string[]
  /** Count of top-level DSL entities with no traceable source origin. */
  dslWithoutOrigin: number
}

/** Roll up mappings into the counts shown in the confidence header. */
export function computeCoverage(
  mappings: LoadRunnerSourceReviewMapping[],
  dslWithoutOrigin = 0,
): CoverageSummary {
  const summary: CoverageSummary = {
    total: 0,
    converted: 0,
    preserved: 0,
    review: 0,
    unsupported: 0,
    unsupportedFunctions: [],
    dslWithoutOrigin,
  }
  const seenFunctions = new Set<string>()
  for (const mapping of mappings) {
    if (!mapping) continue
    summary.total += 1
    const category = statusCategory(mapping.status)
    summary[category] += 1
    if ((category === 'unsupported' || category === 'review') && mapping.function) {
      if (!seenFunctions.has(mapping.function)) {
        seenFunctions.add(mapping.function)
        summary.unsupportedFunctions.push(mapping.function)
      }
    }
  }
  return summary
}

function fileBasename(path?: string): string {
  if (!path) return ''
  return path.toLowerCase().split('/').pop() || ''
}

/** Whether a mapping's source_file refers to the file at `filePath`. */
export function mappingMatchesFile(mapping: LoadRunnerSourceReviewMapping, filePath?: string): boolean {
  if (!filePath || !mapping.source_file) return false
  const file = filePath.toLowerCase()
  const source = mapping.source_file.toLowerCase()
  return file === source || file.endsWith(`/${source}`) || fileBasename(file) === fileBasename(source)
}

function mappingEndLine(mapping: LoadRunnerSourceReviewMapping): number {
  return mapping.end_line && mapping.end_line >= mapping.start_line ? mapping.end_line : mapping.start_line
}

function setWithPriority(map: Map<number, MappingCategory>, line: number, category: MappingCategory) {
  const existing = map.get(line)
  if (!existing || CATEGORY_PRIORITY[category] > CATEGORY_PRIORITY[existing]) {
    map.set(line, category)
  }
}

/** Status colour for every mapped line in a source file (drives the left gutter). */
export function lineStatusForSource(
  mappings: LoadRunnerSourceReviewMapping[],
  filePath?: string,
): Map<number, MappingCategory> {
  const result = new Map<number, MappingCategory>()
  for (const mapping of mappings) {
    if (!mapping || !mapping.start_line || !mappingMatchesFile(mapping, filePath)) continue
    const category = statusCategory(mapping.status)
    for (let line = mapping.start_line; line <= mappingEndLine(mapping); line += 1) {
      setWithPriority(result, line, category)
    }
  }
  return result
}

/** True when two DSL paths sit on the same ancestor chain (one contains the other). */
function pathsRelated(a: string, b: string): boolean {
  if (a === b) return true
  const isDescendant = (child: string, parent: string) =>
    child.startsWith(`${parent}.`) || child.startsWith(`${parent}[`)
  return isDescendant(a, b) || isDescendant(b, a)
}

function notableNodePaths(ranges: Map<string, LineRange>): string[] {
  const paths: string[] = []
  for (const path of ranges.keys()) {
    if (NOTABLE_NODE_PATTERN.test(path)) paths.push(path)
  }
  return paths
}

export interface DslLineStatusResult {
  lines: Map<number, GutterStatus>
  /** Notable DSL node paths that no mapping traces back to source. */
  withoutOrigin: string[]
}

/**
 * Status colour for DSL lines (drives the right gutter). Mapped targets take their
 * mapping's category; notable nodes that no mapping covers are flagged 'no-origin'
 * — the reverse-coverage half of the purity signal.
 */
export function dslLineStatus(
  ranges: Map<string, LineRange>,
  mappings: LoadRunnerSourceReviewMapping[],
): DslLineStatusResult {
  const mapped = new Map<number, MappingCategory>()
  for (const mapping of mappings) {
    if (!mapping || !mapping.target_path) continue
    const range = lineRangeForTargetPath(ranges, mapping.target_path)
    if (!range) continue
    const category = statusCategory(mapping.status)
    for (let line = range.startLine; line <= range.endLine; line += 1) {
      setWithPriority(mapped, line, category)
    }
  }

  const lines = new Map<number, GutterStatus>(mapped)
  const withoutOrigin: string[] = []
  const targetPaths = mappings.map(mapping => mapping?.target_path).filter((path): path is string => Boolean(path))

  for (const nodePath of notableNodePaths(ranges)) {
    const covered = targetPaths.some(target => pathsRelated(nodePath, target))
    if (covered) continue
    withoutOrigin.push(nodePath)
    const range = ranges.get(nodePath)
    if (!range) continue
    for (let line = range.startLine; line <= range.endLine; line += 1) {
      if (!lines.has(line)) lines.set(line, 'no-origin')
    }
  }

  return { lines, withoutOrigin }
}

function rangeSpan(start: number, end: number): number {
  return Math.max(0, end - start)
}

/** Resolve a clicked source line to the most specific covering mapping. */
export function mappingForSourceLine(
  mappings: LoadRunnerSourceReviewMapping[],
  filePath: string | undefined,
  line: number,
): LoadRunnerSourceReviewMapping | null {
  let best: LoadRunnerSourceReviewMapping | null = null
  let bestSpan = Number.POSITIVE_INFINITY
  for (const mapping of mappings) {
    if (!mapping || !mapping.start_line || !mappingMatchesFile(mapping, filePath)) continue
    const end = mappingEndLine(mapping)
    if (line < mapping.start_line || line > end) continue
    const span = rangeSpan(mapping.start_line, end)
    if (span < bestSpan) {
      best = mapping
      bestSpan = span
    }
  }
  return best
}

/** Resolve a clicked DSL line to the most specific mapping whose target covers it. */
export function mappingForDslLine(
  ranges: Map<string, LineRange>,
  mappings: LoadRunnerSourceReviewMapping[],
  line: number,
): LoadRunnerSourceReviewMapping | null {
  let best: LoadRunnerSourceReviewMapping | null = null
  let bestSpan = Number.POSITIVE_INFINITY
  for (const mapping of mappings) {
    if (!mapping || !mapping.target_path) continue
    const range = lineRangeForTargetPath(ranges, mapping.target_path)
    if (!range || line < range.startLine || line > range.endLine) continue
    const span = rangeSpan(range.startLine, range.endLine)
    if (span < bestSpan) {
      best = mapping
      bestSpan = span
    }
  }
  return best
}
