/**
 * Fixed-decimal number in the viewer's locale. Percentages and timings use it
 * so every number on a page shares one decimal separator; toFixed always
 * printed a point beside localized values such as "21,0" in de-DE.
 */
export function formatFixed(value: number, digits: number): string {
  return value.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** A number in the viewer's locale with at most `digits` decimals: "12", "12.5", "1,234.57". */
export function formatUpTo(value: number, digits = 2): string {
  return value.toLocaleString(undefined, { maximumFractionDigits: digits })
}

/** A percentage with fixed decimals; a positive value below the last digit reads "<0.01%". */
export function formatPercentValue(value: number, digits = 2): string {
  if (value > 0 && value < 10 ** -digits) return `<${formatFixed(10 ** -digits, digits)}%`
  return `${formatFixed(value, digits)}%`
}

/** "1 request", "2 requests": a count with its noun in the matching number. */
export function countLabel(count: number, singular: string, plural = `${singular}s`): string {
  return `${count} ${count === 1 ? singular : plural}`
}
