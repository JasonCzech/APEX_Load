// Shared normalization for generator capability / required labels: trim keys and
// values, drop empty entries, and return undefined when nothing remains. Used by the
// group placement editor, the scenario/test payload builders, and the New Test launch
// flow so the inherit/auto/required_labels encoding never drifts between surfaces.
export function normalizeRequiredLabels(labels?: Record<string, string>): Record<string, string> | undefined {
  if (!labels) return undefined
  const next = Object.fromEntries(Object.entries(labels)
    .map(([key, value]) => [key.trim(), value.trim()])
    .filter(([key, value]) => key && value))
  return Object.keys(next).length > 0 ? next : undefined
}
