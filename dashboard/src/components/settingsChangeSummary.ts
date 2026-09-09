/** Field names only: never put old/new credential values in a pending summary. */
export function changedSettingsFields(before: unknown, after: unknown, prefix = ''): string[] {
  if (JSON.stringify(before) === JSON.stringify(after)) return []
  if (before && after && typeof before === 'object' && typeof after === 'object' && !Array.isArray(before) && !Array.isArray(after)) {
    const left = before as Record<string, unknown>, right = after as Record<string, unknown>
    return [...new Set([...Object.keys(left), ...Object.keys(right)])].filter(key => key !== 'revision').flatMap(key => changedSettingsFields(left[key], right[key], prefix ? `${prefix}.${key}` : key))
  }
  return [prefix.replace(/_/g, ' ').replace(/\./g, ' › ')]
}
