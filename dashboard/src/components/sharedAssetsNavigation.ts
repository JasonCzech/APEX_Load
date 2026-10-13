// Shared assets: what a project's scripts and scenarios share, on one page
// with a section per kind. The order is the order of use: data first, then
// secrets, then what authoring reuses.

export type SharedAssetsSection = 'test-data' | 'live-tables' | 'secrets' | 'correlation-rules' | 'object-repositories'

/**
 * Each section's panel says what it holds; `usage` adds how a script reads
 * it, where a script names it in a value.
 */
export const SHARED_ASSET_SECTIONS: ReadonlyArray<{ id: SharedAssetsSection; title: string; usage?: string }> = [
  { id: 'test-data', title: 'Test data', usage: 'Scenarios bind a pool by alias, and scripts read its rows as {{data.alias.column}}.' },
  { id: 'live-tables', title: 'Live tables', usage: 'Scenarios bind a project table by alias, and scripts read it as {{data.alias.column}}, as they read a pool.' },
  { id: 'secrets', title: 'Secrets', usage: 'Scripts read a secret as {{secret.NAME}}. Its value is never shown once it is saved.' },
  { id: 'correlation-rules', title: 'Correlation rules' },
  { id: 'object-repositories', title: 'Object repositories' },
]

export const DEFAULT_SHARED_ASSETS_SECTION: SharedAssetsSection = 'test-data'

export function isSharedAssetsSection(value: string | null | undefined): value is SharedAssetsSection {
  return SHARED_ASSET_SECTIONS.some(section => section.id === value)
}

/**
 * A link to a Shared assets section (and a row in it), in the workspace the
 * current address names.
 */
export function sharedAssetsHref(section: SharedAssetsSection, resourceId?: string, currentHash = typeof window === 'undefined' ? '' : window.location.hash): string {
  const current = new URLSearchParams(currentHash.split('?')[1] || '')
  const params = new URLSearchParams({ section })
  if (resourceId) params.set('resourceId', resourceId)
  for (const key of ['projectId', 'applicationId', 'releaseId']) {
    const value = current.get(key)
    if (value) params.set(key, value)
  }
  return `#/shared-assets?${params}`
}
