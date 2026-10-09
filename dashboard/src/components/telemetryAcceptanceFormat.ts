// A percentage with at least two significant digits, so a small ceiling
// (0.03%) stays readable.
export function formatAcceptancePercent(ratio: number): string {
  const percent = ratio * 100
  if (percent === 0 || Math.abs(percent) >= 1) return `${percent.toFixed(1)}%`
  const digits = Math.min(8, Math.ceil(-Math.log10(Math.abs(percent))) + 1)
  return `${percent.toFixed(digits)}%`
}
