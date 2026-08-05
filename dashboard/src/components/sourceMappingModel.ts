// Pure helpers behind the conversion-validation view. They turn the backend's
// SourceReviewMapping records into the data the two linked code panes need:
//   - coverage counts for the confidence header,
//   - per-line status for the source and DSL gutters (the "purity" signal),
//   - click → mapping resolution for bidirectional linking.
// Everything here is side-effect free so it can be unit tested without a DOM.

import { type SourceReviewMapping } from '../types/scripts'
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
  /** Distinct EnterpriseArchive function names that did not convert cleanly. */
  unsupportedFunctions: string[]
  /** Count of top-level DSL entities with no traceable source origin. */
  dslWithoutOrigin: number
}

/** Roll up mappings into the counts shown in the confidence header. */
export function computeCoverage(
  mappings: SourceReviewMapping[],
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

function normalizeSourcePath(path?: string): string {
  if (!path) return ''
  const parts: string[] = []
  for (const part of path.trim().replace(/\\/g, '/').split('/')) {
    if (!part || part === '.') continue
    if (part === '..' && parts.length > 0 && parts[parts.length - 1] !== '..') {
      parts.pop()
      continue
    }
    parts.push(part)
  }
  return parts.join('/').toLowerCase()
}

interface SourcePathMatch {
  paths: string[]
}

interface SourcePathSuffixNode extends SourcePathMatch {
  children: Map<string, SourcePathSuffixNode>
}

export interface SourceFilePathResolver {
  candidates: (requestedPath?: string) => readonly string[]
  resolve: (requestedPath?: string) => string | null
}

type SourceFilePathLookup = readonly string[] | SourceFilePathResolver

function recordPathMatch(
  index: Map<string, SourcePathMatch>,
  key: string,
  path: string,
) {
  const existing = index.get(key)
  if (existing) {
    existing.paths.push(path)
  } else {
    index.set(key, { paths: [path] })
  }
}

/**
 * Build a reusable source-path resolver for one review payload.
 *
 * The indexes retain collisions so ambiguous paths fail closed. Resolution
 * results are cached by normalized request, which keeps files × mappings UI
 * operations bounded even when a review names thousands of omitted files.
 */
export function createSourceFilePathResolver(
  knownFilePaths: readonly string[],
): SourceFilePathResolver {
  const exactIndex = new Map<string, SourcePathMatch>()
  const suffixRoot: SourcePathSuffixNode = {
    paths: [],
    children: new Map(),
  }
  const normalizedCandidateCache = new Map<string, readonly string[]>()
  const requestCandidateCache = new Map<string, readonly string[]>()
  const normalizedCache = new Map<string, string | null>()
  const requestCache = new Map<string, string | null>()

  for (const path of knownFilePaths) {
    const normalized = normalizeSourcePath(path)
    if (!normalized) continue
    recordPathMatch(exactIndex, normalized, path)

    const parts = normalized.split('/')
    let node = suffixRoot
    for (let index = parts.length - 1; index >= 0; index -= 1) {
      const part = parts[index]
      let child = node.children.get(part)
      if (!child) {
        child = { paths: [], children: new Map() }
        node.children.set(part, child)
      }
      child.paths.push(path)
      node = child
    }
  }

  const resolver: SourceFilePathResolver = {
    candidates(requestedPath?: string): readonly string[] {
      if (!requestedPath) return []
      const requestCached = requestCandidateCache.get(requestedPath)
      if (requestCached) return requestCached

      const requested = normalizeSourcePath(requestedPath)
      if (!requested) return []
      const normalizedCached = normalizedCandidateCache.get(requested)
      if (normalizedCached) {
        requestCandidateCache.set(requestedPath, normalizedCached)
        return normalizedCached
      }

      const exact = exactIndex.get(requested)
      if (exact) {
        normalizedCandidateCache.set(requested, exact.paths)
        requestCandidateCache.set(requestedPath, exact.paths)
        return exact.paths
      }

      const requestedParts = requested.split('/')
      let currentNode = suffixRoot
      let basenameMatch: SourcePathSuffixNode | undefined
      let deepestSuffixMatch: SourcePathSuffixNode | undefined
      let depth = 0
      for (let index = requestedParts.length - 1; index >= 0; index -= 1) {
        const nextNode = currentNode.children.get(requestedParts[index])
        if (!nextNode) break
        currentNode = nextNode
        depth += 1
        if (depth === 1) basenameMatch = currentNode
        if (depth >= 2) deepestSuffixMatch = currentNode
      }

      const matches = (deepestSuffixMatch || basenameMatch)?.paths || []
      normalizedCandidateCache.set(requested, matches)
      requestCandidateCache.set(requestedPath, matches)
      return matches
    },
    resolve(requestedPath?: string): string | null {
      if (!requestedPath) return null
      if (requestCache.has(requestedPath)) return requestCache.get(requestedPath) ?? null

      const requested = normalizeSourcePath(requestedPath)
      if (!requested) return null
      if (normalizedCache.has(requested)) {
        const cached = normalizedCache.get(requested) ?? null
        requestCache.set(requestedPath, cached)
        return cached
      }

      const matches = resolver.candidates(requestedPath)
      const resolved = matches.length === 1 ? matches[0] : null
      normalizedCache.set(requested, resolved)
      requestCache.set(requestedPath, resolved)
      return resolved
    },
  }
  return resolver
}

function isSourceFilePathResolver(
  lookup: SourceFilePathLookup,
): lookup is SourceFilePathResolver {
  const resolver = lookup as SourceFilePathResolver
  return typeof resolver.resolve === 'function' && typeof resolver.candidates === 'function'
}

function sourceFilePathResolver(lookup: SourceFilePathLookup): SourceFilePathResolver {
  return isSourceFilePathResolver(lookup)
    ? lookup
    : createSourceFilePathResolver(lookup)
}

/**
 * Resolve a backend source path against all source files in the review.
 *
 * Exact/canonical paths win first, followed by the deepest unique path suffix.
 * A basename-only match is accepted only when that basename occurs once in the
 * complete source-file set. Ambiguous matches return null instead of depending
 * on source-file order.
 */
export function resolveSourceFilePath(
  requestedPath: string | undefined,
  knownFilePaths: SourceFilePathLookup,
): string | null {
  return sourceFilePathResolver(knownFilePaths).resolve(requestedPath)
}

/** Best-tier exact, deepest-suffix, or basename candidates for explicit review. */
export function sourceFilePathCandidates(
  requestedPath: string | undefined,
  knownFilePaths: SourceFilePathLookup,
): readonly string[] {
  return sourceFilePathResolver(knownFilePaths).candidates(requestedPath)
}

/** Whether a mapping's source_file refers to the file at `filePath`. */
export function mappingMatchesFile(
  mapping: SourceReviewMapping,
  filePath?: string,
  knownFilePaths?: SourceFilePathLookup,
): boolean {
  if (!filePath || !mapping.source_file) return false
  const resolved = resolveSourceFilePath(mapping.source_file, knownFilePaths || [filePath])
  return resolved !== null && normalizeSourcePath(resolved) === normalizeSourcePath(filePath)
}

function mappingEndLine(mapping: SourceReviewMapping): number {
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
  mappings: SourceReviewMapping[],
  filePath?: string,
  knownFilePaths?: SourceFilePathLookup,
): Map<number, MappingCategory> {
  const result = new Map<number, MappingCategory>()
  for (const mapping of mappings) {
    if (!mapping || !mapping.start_line || !mappingMatchesFile(mapping, filePath, knownFilePaths)) continue
    const category = statusCategory(mapping.status)
    for (let line = mapping.start_line; line <= mappingEndLine(mapping); line += 1) {
      setWithPriority(result, line, category)
    }
  }
  return result
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
  mappings: SourceReviewMapping[],
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
  const coveredNodes = new Set<string>()
  const coveredSections = new Set<string>()
  for (const target of targetPaths) {
    const node = target.match(/^(actions|init|teardown|parameters|data_sources)\[\d+\](?=\.|\[|$)/)?.[0]
    if (node) {
      coveredNodes.add(node)
      continue
    }
    const section = target.match(/^(actions|init|teardown|parameters|data_sources)$/)?.[0]
    if (section) coveredSections.add(section)
  }

  for (const nodePath of notableNodePaths(ranges)) {
    const section = nodePath.slice(0, nodePath.indexOf('['))
    const covered = coveredNodes.has(nodePath) || coveredSections.has(section)
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
  mappings: SourceReviewMapping[],
  filePath: string | undefined,
  line: number,
  knownFilePaths?: SourceFilePathLookup,
): SourceReviewMapping | null {
  let best: SourceReviewMapping | null = null
  let bestSpan = Number.POSITIVE_INFINITY
  for (const mapping of mappings) {
    if (!mapping || !mapping.start_line || !mappingMatchesFile(mapping, filePath, knownFilePaths)) continue
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
  mappings: SourceReviewMapping[],
  line: number,
): SourceReviewMapping | null {
  let best: SourceReviewMapping | null = null
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
