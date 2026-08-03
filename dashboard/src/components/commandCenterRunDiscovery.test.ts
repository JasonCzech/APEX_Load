import { describe, expect, it, vi } from 'vitest'

import type { TestSummary } from '../types'
import { listAllCommandCenterActiveRuns } from './commandCenterRunDiscovery'

describe('Command Center active-run discovery', () => {
  it('requests active summaries and drains an old active run beyond the first page', async () => {
    const runs = Array.from({ length: 201 }, (_, index) => ({
      id: index === 200 ? 'old-active-run' : `active-run-${index}`,
      name: `Active run ${index}`,
      status: 'RUNNING',
      groups: [],
      total_vusers: 1,
      duration: '1m',
      created_at: '',
    })) as TestSummary[]
    const listPage = vi.fn(async ({ limit, offset }: { limit: number; offset: number }) => ({
      tests: runs.slice(offset, offset + limit),
      total: runs.length,
    }))

    const discovered = await listAllCommandCenterActiveRuns(listPage, {
      application_id: 'application-1',
      project_id: 'project-1',
    })

    expect(discovered[discovered.length - 1]?.id).toBe('old-active-run')
    expect(listPage).toHaveBeenNthCalledWith(1, {
      application_id: 'application-1',
      project_id: 'project-1',
      summary: true,
      active_only: true,
      limit: 200,
      offset: 0,
    })
    expect(listPage).toHaveBeenNthCalledWith(2, {
      application_id: 'application-1',
      project_id: 'project-1',
      summary: true,
      active_only: true,
      limit: 200,
      offset: 200,
    })
  })
})
