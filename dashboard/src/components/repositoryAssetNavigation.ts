import { sharedAssetsHref } from './sharedAssetsNavigation'

/** A pool belongs to a project; never carry another project's application/release. */
export function repositoryDataPoolHref(poolId: string, projectId: string, origin: string) {
  const source = new URLSearchParams(origin.split('?')[1] || '')
  const scope = new URLSearchParams({ projectId })
  if (source.get('projectId') === projectId) {
    for (const field of ['applicationId', 'releaseId']) {
      const value = source.get(field)
      if (value) scope.set(field, value)
    }
  }
  return sharedAssetsHref('test-data', poolId, `#/repository?${scope}`)
}
