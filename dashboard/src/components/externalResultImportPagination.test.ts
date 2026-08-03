import { describe, expect, it, vi } from 'vitest'

import { loadAllExternalResultImports } from './externalResultImportPagination'

function importedResult(id: string) {
  return {
    id,
    provider: 'k6',
    name: `${id}.json`,
    application_id: 'app-1',
    created_at: '2026-08-03T12:00:00Z',
  }
}

describe('loadAllExternalResultImports', () => {
  it('drains every page within the requested application scope', async () => {
    const loadPage = vi.fn()
      .mockResolvedValueOnce({
        imports: [importedResult('import-1'), importedResult('import-2')],
        count: 2,
        limit: 2,
        offset: 0,
        has_more: true,
      })
      .mockResolvedValueOnce({
        imports: [importedResult('import-3')],
        count: 1,
        limit: 2,
        offset: 2,
        has_more: false,
      })

    const imports = await loadAllExternalResultImports(loadPage, {
      project_id: 'project-1',
      application_id: 'app-1',
      release_id: 'release-1',
    }, { pageSize: 2 })

    expect(imports?.map(item => item.id)).toEqual(['import-1', 'import-2', 'import-3'])
    expect(loadPage).toHaveBeenNthCalledWith(1, {
      project_id: 'project-1',
      application_id: 'app-1',
      release_id: 'release-1',
      limit: 2,
      offset: 0,
    })
    expect(loadPage).toHaveBeenNthCalledWith(2, {
      project_id: 'project-1',
      application_id: 'app-1',
      release_id: 'release-1',
      limit: 2,
      offset: 2,
    })
  })

  it('stops a stale request before loading another page', async () => {
    let current = true
    const loadPage = vi.fn().mockImplementation(async () => {
      current = false
      return {
        imports: [importedResult('stale-import')],
        count: 1,
        limit: 1,
        offset: 0,
        has_more: true,
      }
    })

    await expect(loadAllExternalResultImports(
      loadPage,
      { application_id: 'app-1' },
      { isCurrent: () => current, pageSize: 1 },
    )).resolves.toBeNull()
    expect(loadPage).toHaveBeenCalledTimes(1)
  })

  it('rejects a non-advancing page instead of looping forever', async () => {
    const loadPage = vi.fn().mockResolvedValue({
      imports: [],
      count: 0,
      limit: 1000,
      offset: 0,
      has_more: true,
    })

    await expect(loadAllExternalResultImports(loadPage, { application_id: 'app-1' }))
      .rejects.toThrow('pagination did not advance')
    expect(loadPage).toHaveBeenCalledTimes(1)
  })
})
