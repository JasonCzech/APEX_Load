import { settingsHref } from './settingsNavigation'

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
  const target = settingsHref('test-data', `#/repository?${scope}`, poolId)
  const [path, query] = target.split('?')
  const params = new URLSearchParams(query)
  if (/^#\/repository(?:\?|$)/.test(origin) && !source.has('setting')) params.set('returnTo', origin)
  return `${path}?${params}`
}
