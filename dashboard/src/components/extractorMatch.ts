// Client-side preview of DSL extractor expressions against a captured replay
// response. This is a fast authoring aid — the backend remains the source of
// truth at replay time — so we only preview the two unambiguous, standard
// expression types (regex and JSON path). Other types (boundary/xpath/css/
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

export const PREVIEWABLE_EXTRACTOR_TYPES = new Set(['regex', 'json', 'jsonpath'])

/** Evaluate an extractor expression against `text`. Pure; exported for tests. */
export function evaluateExtractor(type: string, expression: string, text: string): ExtractorMatchResult {
  const previewable = PREVIEWABLE_EXTRACTOR_TYPES.has(type)
  const expr = expression.trim()
  if (!previewable) return { matches: [], previewable: false }
  if (!expr) return { matches: [], previewable: true, error: 'Enter an expression to preview matches.' }
  if (type === 'regex') return evaluateRegex(expr, text)
  return evaluateJsonPath(expr, text)
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
