import type { ObjectRepository, ObjectRepositoryListResponse } from '../types/objectRepository'
import { drainBoundedOffsetCatalog } from './catalogPagination'

export async function loadObjectRefSuggestions(
  projectId: string,
  listRepositories: (params: { project_id: string; limit: number; offset: number }) => Promise<ObjectRepositoryListResponse>,
): Promise<string[]> {
  const repositories = await drainBoundedOffsetCatalog<ObjectRepository>(
    async (limit, offset) => {
      const response = await listRepositories({ project_id: projectId, limit, offset })
      const items = response.object_repositories || []
      return { items, hasMore: items.length === limit }
    },
    repository => repository.id,
    'Object repository catalog',
  )
  const names = new Set<string>()
  repositories.forEach(repository => repository.objects?.forEach(object => {
    if (object.logical_name) names.add(object.logical_name)
  }))
  return Array.from(names).sort()
}
