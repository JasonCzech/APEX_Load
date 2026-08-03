import type { TestSummary } from '../types'
import { drainBoundedOffsetCatalog } from './catalogPagination'

const COMMAND_CENTER_DISCOVERY_PAGE_SIZE = 200

type TestListParams = {
  application_id?: string
  project_id?: string
  summary: boolean
  active_only: boolean
  limit: number
  offset: number
}

type TestListPage = {
  tests: TestSummary[]
  total?: number
}

export function listAllCommandCenterActiveRuns(
  listPage: (params: TestListParams) => Promise<TestListPage>,
  scope: { application_id?: string; project_id?: string },
): Promise<TestSummary[]> {
  return drainBoundedOffsetCatalog(
    async (limit, offset) => {
      const response = await listPage({
        ...scope,
        summary: true,
        active_only: true,
        limit,
        offset,
      })
      const items = response.tests || []
      return {
        items,
        hasMore: typeof response.total === 'number'
          ? offset + items.length < response.total
          : undefined,
      }
    },
    test => test.id,
    'Command Center active-run catalog',
    { pageSize: COMMAND_CENTER_DISCOVERY_PAGE_SIZE },
  )
}
