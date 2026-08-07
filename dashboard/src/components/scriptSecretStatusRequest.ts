// Request tokens prevent a slower earlier secret-status request from replacing
// the result of a later retry for the same script.
export function beginScriptSecretStatusRequest(tokens: Map<string, number>, scriptId: string): number {
  const token = (tokens.get(scriptId) || 0) + 1
  tokens.set(scriptId, token)
  return token
}

export function isLatestScriptSecretStatusRequest(tokens: ReadonlyMap<string, number>, scriptId: string, token: number): boolean {
  return tokens.get(scriptId) === token
}
