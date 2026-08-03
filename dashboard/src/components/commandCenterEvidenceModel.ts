export function liveEvidenceCacheKey(testId: string, signature: string) {
  return testId && signature ? `${testId}\u001f${signature}` : ''
}
