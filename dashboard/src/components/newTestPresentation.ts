import type { StoredScenario } from '../types'

export function scenarioIdentity(scenario: Pick<StoredScenario, 'name' | 'ref_key' | 'id'>): string {
  return `${scenario.name} · ${scenario.ref_key || scenario.id}`
}

// Review origins, not credential-bearing paths, query strings or userinfo.
export function targetOrigin(value: string | undefined): string | null {
  if (!value) return null
  if (value.includes('{{')) return 'Dynamic target — resolved at runtime'
  try {
    const url = new URL(value)
    return ['http:', 'https:', 'ws:', 'wss:', 'grpc:', 'grpcs:'].includes(url.protocol)
      ? `${url.protocol}//${url.host}` : 'Protocol-specific target — review script'
  } catch { return null }
}
