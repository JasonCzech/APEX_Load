// A percentage to two decimals, as the app states them, with at least two
// significant digits below 1%, so a small ceiling (0.0030%) stays readable.
export function formatAcceptancePercent(ratio: number): string {
  const percent = ratio * 100
  if (percent === 0 || Math.abs(percent) >= 1) return `${percent.toFixed(2)}%`
  const digits = Math.min(8, Math.ceil(-Math.log10(Math.abs(percent))) + 1)
  return `${percent.toFixed(digits)}%`
}
