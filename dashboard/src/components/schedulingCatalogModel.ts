import type {
  CapacityTimeslot,
  CapacityTimeslotListResponse,
  ExecutionPlan,
  ExecutionPlanListResponse,
} from '../types'
import { drainBoundedOffsetCatalog } from './catalogPagination'

type ProjectCatalogParams = {
  project_id: string
  limit: number
  offset: number
}

export function listAllCapacityTimeslots(
  listPage: (params: ProjectCatalogParams) => Promise<CapacityTimeslotListResponse>,
  projectId: string,
): Promise<CapacityTimeslot[]> {
  return drainBoundedOffsetCatalog(
    async (limit, offset) => {
      const response = await listPage({ project_id: projectId, limit, offset })
      return { items: response.timeslots || [] }
    },
    timeslot => timeslot.id,
    'Capacity timeslot catalog',
  )
}

export function listAllExecutionPlans(
  listPage: (params: ProjectCatalogParams) => Promise<ExecutionPlanListResponse>,
  projectId: string,
): Promise<ExecutionPlan[]> {
  return drainBoundedOffsetCatalog(
    async (limit, offset) => {
      const response = await listPage({ project_id: projectId, limit, offset })
      return { items: response.execution_plans || [] }
    },
    plan => plan.id,
    'Execution plan catalog',
  )
}
