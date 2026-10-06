// parseBusinessKeyValues splits the lookup textarea into distinct,
// non-empty, trimmed values, in order.
export function parseBusinessKeyValues(text: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const line of text.split('\n')) {
    const value = line.trim()
    if (value && !seen.has(value)) {
      seen.add(value)
      out.push(value)
    }
  }
  return out
}
