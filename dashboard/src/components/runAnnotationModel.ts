// Preserve the server timestamp at execution commit: Date would truncate its
// sub-millisecond precision and move a zero-offset note before the run.
export function runAnnotationTimestamp(startedAt: string, elapsedSeconds: number): string {
  if (!Number.isFinite(Date.parse(startedAt)) || !Number.isFinite(elapsedSeconds) || elapsedSeconds < 0 || elapsedSeconds > 86400 || (elapsedSeconds > 0 && elapsedSeconds < 0.001)) throw new Error('Choose a valid elapsed time in milliseconds')
  return elapsedSeconds === 0 ? startedAt : new Date(Date.parse(startedAt) + elapsedSeconds * 1000).toISOString()
}
