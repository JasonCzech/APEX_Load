import { describe, expect, it, vi } from 'vitest'

import type { ObjectRepository } from '../types/objectRepository'
import { loadObjectRefSuggestions } from './scriptObjectRefCatalog'

describe('loadObjectRefSuggestions', () => {
  it('drains every repository page before building autocomplete names', async () => {
    const firstPage = Array.from({ length: 100 }, (_, index) => repository(`repo-${index}`, `object_${index}`))
    const list = vi.fn(async ({ offset }: { offset: number }) => ({
      object_repositories: offset === 0 ? firstPage : [repository('repo-100', 'last_object')],
      count: offset === 0 ? 100 : 1,
      limit: 100,
      offset,
    }))

    const names = await loadObjectRefSuggestions('project-1', list)

    expect(list).toHaveBeenNthCalledWith(1, { project_id: 'project-1', limit: 100, offset: 0 })
    expect(list).toHaveBeenNthCalledWith(2, { project_id: 'project-1', limit: 100, offset: 100 })
    expect(names).toHaveLength(101)
    expect(names).toContain('last_object')
  })
})

function repository(id: string, logicalName: string): ObjectRepository {
  return {
    id,
    name: id,
    objects: [{ logical_name: logicalName }],
    created_at: '',
    updated_at: '',
  }
}
