// Compact UTC ticks keep boundary labels inside narrow analytical charts.
// Tooltips retain the full source timestamp, including the year and offset.
export function formatAnalysisTimeTick(value: unknown): string {
  const time = new Date(String(value))
  if (!Number.isFinite(time.getTime())) return '—'
  const utc = time.toISOString()
  return `${utc.slice(5, 10)} ${utc.slice(11, 19)}`
}
