// Pretty-prints a DSL script to JSON and records the line range every value
// occupies in that output. This lets the conversion-validation view map a
// source→DSL mapping's `target_path` (e.g. "actions[0].extractors[0]",
// "config.think_time") to a concrete line range in the rendered DSL document so
// the right-hand pane can highlight and scroll to it.
//
// The emitted text is byte-for-byte identical to JSON.stringify(value, null, 2),
// so it can be rendered directly in a code pane while the recorded ranges stay
// in sync with what the user sees.

export interface LineRange {
  startLine: number
  endLine: number
}

export interface DslDocument {
  text: string
  /** Path string (same form as SourceReviewMapping.target_path) → 1-based line range. */
  ranges: Map<string, LineRange>
}

function isSerializable(value: unknown): boolean {
  const type = typeof value
  return !(value === undefined || type === 'function' || type === 'symbol')
}

/**
 * Serialize `root` to pretty JSON (2-space indent, matching
 * JSON.stringify(root, null, 2)) while recording the line range of every nested
 * value keyed by its path. Array elements use bracket notation ("items[0]") and
 * object members use dot notation ("config.base_url"), matching the convention
 * used by SourceReviewMapping.target_path and resolvePathValue.
 */
export function buildDslDocument(root: unknown): DslDocument {
  const ranges = new Map<string, LineRange>()
  let out = ''
  let line = 1

  const emit = (text: string) => {
    out += text
    for (let index = 0; index < text.length; index += 1) {
      if (text.charCodeAt(index) === 10 /* \n */) line += 1
    }
  }

  const serialize = (value: unknown, indent: string, path: string) => {
    const startLine = line
    if (value === null) {
      emit('null')
    } else if (Array.isArray(value)) {
      if (value.length === 0) {
        emit('[]')
      } else {
        emit('[\n')
        const childIndent = `${indent}  `
        value.forEach((item, index) => {
          emit(childIndent)
          serialize(item, childIndent, `${path}[${index}]`)
          emit(index < value.length - 1 ? ',\n' : '\n')
        })
        emit(`${indent}]`)
      }
    } else if (typeof value === 'object') {
      const keys = Object.keys(value as Record<string, unknown>).filter(key =>
        isSerializable((value as Record<string, unknown>)[key]),
      )
      if (keys.length === 0) {
        emit('{}')
      } else {
        emit('{\n')
        const childIndent = `${indent}  `
        keys.forEach((key, index) => {
          emit(`${childIndent}${JSON.stringify(key)}: `)
          const childPath = path ? `${path}.${key}` : key
          serialize((value as Record<string, unknown>)[key], childIndent, childPath)
          emit(index < keys.length - 1 ? ',\n' : '\n')
        })
        emit(`${indent}}`)
      }
    } else {
      // string | number | boolean
      emit(JSON.stringify(value))
    }
    if (path) {
      ranges.set(path, { startLine, endLine: line })
    }
  }

  serialize(root, '', '')
  return { text: out, ranges }
}

/** Normalize a target_path so "[n]" and ".n" index forms both resolve. */
function normalizeTargetPath(targetPath: string): string {
  return targetPath.trim()
}

/**
 * Look up the line range for a SourceReviewMapping target_path. Tries the path
 * as-is, then a "[n]"→".n" normalized form, so either index convention resolves.
 */
export function lineRangeForTargetPath(
  ranges: Map<string, LineRange>,
  targetPath?: string | null,
): LineRange | null {
  if (!targetPath) return null
  const direct = ranges.get(normalizeTargetPath(targetPath))
  if (direct) return direct
  // Accept dotted index form ("actions.0") by converting to bracket form.
  const bracketed = normalizeTargetPath(targetPath).replace(/\.(\d+)(?=\.|$)/g, '[$1]')
  return ranges.get(bracketed) ?? null
}
