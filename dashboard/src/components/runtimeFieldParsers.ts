// Small, self-contained parse/format helpers for the per-group runtime tabs
// (list and key=value fields). Kept separate so the Web/HTTP tab and any future
// tab can share them without depending on the Script Editor's internals.

/** Join a string array for display in a single-line comma input. */
export function formatList(values?: string[]): string {
  return values && values.length ? values.join(', ') : ''
}

/** Parse a comma- or newline-separated list into a trimmed, non-empty string array. */
export function parseList(raw: string): string[] {
  return raw
    .split(/[,\n]/)
    .map(value => value.trim())
    .filter(Boolean)
}

/** Render a string→string map as `key=value` lines for a textarea. */
export function formatKeyValues(map?: Record<string, string>): string {
  if (!map) return ''
  return Object.entries(map)
    .map(([key, value]) => `${key}=${value}`)
    .join('\n')
}

/** Parse `key=value` lines (one per line) into a string→string map. */
export function parseKeyValues(raw: string): Record<string, string> {
  const out: Record<string, string> = {}
  raw
    .split('\n')
    .map(line => line.trim())
    .filter(Boolean)
    .forEach(line => {
      const idx = line.indexOf('=')
      if (idx > 0) {
        out[line.slice(0, idx).trim()] = line.slice(idx + 1).trim()
      }
    })
  return out
}
