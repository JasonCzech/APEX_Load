// Client-side preview of DSL extractor expressions against a captured replay
// response. This is a fast authoring aid — the backend remains the source of
// truth at replay time — so we only preview the unambiguous expression types:
// regex, JSON path and LoadRunner-style boundaries. Other types (xpath/css/
// jmespath/sse_*) are still addable; they're just evaluated at replay.

const MAX_MATCHES = 50

export interface ExtractorMatchResult {
  /** Matched values (stringified). Empty when nothing matched. */
  matches: string[]
  /** Set when the expression itself is invalid or the source can't be parsed. */
  error?: string
  /** False when this extractor type has no client-side preview. */
  previewable: boolean
}

export const PREVIEWABLE_EXTRACTOR_TYPES = new Set(['regex', 'json', 'jsonpath', 'json_path', 'boundary'])

// pkg/protocol/http extract.go: "left|||right", or this prefix and a JSON config.
const BOUNDARY_CONFIG_PREFIX = '__apexload_boundary_config__:'

/** A boundary expression's parts, as the engine reads them; null when malformed. */
export function parseBoundaryExpression(expression: string): { left: string; right: string; ordinal: number; caseInsensitive: boolean } | null {
  if (expression.startsWith(BOUNDARY_CONFIG_PREFIX)) {
    try {
      const config = JSON.parse(expression.slice(BOUNDARY_CONFIG_PREFIX.length)) as { left?: string; right?: string; ordinal?: number; case_insensitive?: boolean }
      if (!config.left && !config.right) return null
      return { left: config.left || '', right: config.right || '', ordinal: Math.max(1, config.ordinal || 1), caseInsensitive: Boolean(config.case_insensitive) }
    } catch { return null }
  }
  const at = expression.indexOf('|||')
  return at < 0 ? null : { left: expression.slice(0, at), right: expression.slice(at + 3), ordinal: 1, caseInsensitive: false }
}

/** A boundary expression for a left and a right boundary. */
export function boundaryExpression(left: string, right: string): string {
  return left.includes('|||') || right.includes('|||') ? `${BOUNDARY_CONFIG_PREFIX}${JSON.stringify({ left, right })}` : `${left}|||${right}`
}

/** Evaluate an extractor expression against `text`. Pure; exported for tests. */
export function evaluateExtractor(type: string, expression: string, text: string): ExtractorMatchResult {
  const previewable = PREVIEWABLE_EXTRACTOR_TYPES.has(type)
  const expr = expression.trim()
  if (!previewable) return { matches: [], previewable: false }
  if (!expr) return { matches: [], previewable: true, error: 'Enter an expression to preview matches.' }
  if (type === 'regex') return evaluateRegex(expr, text)
  if (type === 'boundary') return evaluateBoundary(expression, text)
  return evaluateJsonPath(expr, text)
}

// Every value between the boundaries, in order; the engine saves the ordinal-th.
function evaluateBoundary(expression: string, text: string): ExtractorMatchResult {
  const boundary = parseBoundaryExpression(expression)
  if (!boundary) return { matches: [], previewable: true, error: 'Write the boundaries as left|||right.' }
  const fold = (value: string) => boundary.caseInsensitive ? value.toLowerCase() : value
  const haystack = fold(text), left = fold(boundary.left), right = fold(boundary.right)
  const values: string[] = []
  let position = 0
  while (values.length < MAX_MATCHES) {
    const found = haystack.indexOf(left, position)
    if (found < 0) break
    const start = found + left.length
    const end = right ? haystack.indexOf(right, start) : haystack.length
    if (end < 0) break
    values.push(text.slice(start, end))
    position = Math.max(end + right.length, start + 1)
    if (!right) break
  }
  return { matches: values.slice(boundary.ordinal - 1), previewable: true }
}

function evaluateRegex(expression: string, text: string): ExtractorMatchResult {
  let regex: RegExp
  try {
    regex = new RegExp(expression, 'g')
  } catch (error) {
    return { matches: [], previewable: true, error: `Invalid regular expression: ${(error as Error).message}` }
  }
  const matches: string[] = []
  let match: RegExpExecArray | null
  while ((match = regex.exec(text)) !== null && matches.length < MAX_MATCHES) {
    // Prefer the first capture group (the correlation idiom), else the whole match.
    matches.push(match[1] ?? match[0])
    if (match.index === regex.lastIndex) regex.lastIndex += 1 // guard zero-width matches
  }
  return { matches, previewable: true }
}

type PathSegment = string | number

function parseJsonPath(expression: string): PathSegment[] {
  const path = expression.replace(/^\$/, '').replace(/^\./, '')
  const segments: PathSegment[] = []
  const matcher = /([^.[\]]+)|\[(\d+)\]/g
  for (let match = matcher.exec(path); match; match = matcher.exec(path)) {
    if (match[2] !== undefined) segments.push(Number(match[2]))
    else segments.push(match[1])
  }
  return segments
}

function stringifyValue(value: unknown): string {
  if (value === null) return 'null'
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  return JSON.stringify(value)
}

function evaluateJsonPath(expression: string, text: string): ExtractorMatchResult {
  let data: unknown
  try {
    data = JSON.parse(text)
  } catch {
    return { matches: [], previewable: true, error: 'Response body is not valid JSON.' }
  }
  const segments = parseJsonPath(expression)
  if (segments.length === 0) return { matches: [], previewable: true, error: 'Enter a JSON path such as $.data.id.' }
  let current: unknown = data
  for (const segment of segments) {
    if (current === null || current === undefined) return { matches: [], previewable: true }
    if (typeof segment === 'number') {
      current = Array.isArray(current) ? current[segment] : undefined
    } else if (typeof current === 'object') {
      current = (current as Record<string, unknown>)[segment]
    } else {
      current = undefined
    }
  }
  if (current === undefined) return { matches: [], previewable: true }
  const matches = Array.isArray(current)
    ? current.slice(0, MAX_MATCHES).map(stringifyValue)
    : [stringifyValue(current)]
  return { matches, previewable: true }
}
