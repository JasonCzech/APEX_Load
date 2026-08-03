import { describe, expect, it, vi } from 'vitest'

import type { CapacityTimeslot, ExecutionPlan } from '../types'
import { listAllCapacityTimeslots, listAllExecutionPlans } from './schedulingCatalogModel'

describe('scheduling catalog pagination', () => {
  it('drains more than one page of capacity timeslots', async () => {
    const timeslots = Array.from({ length: 101 }, (_, index) => ({ id: `slot-${index}` })) as CapacityTimeslot[]
    const listPage = vi.fn(async ({ limit, offset }: { limit: number; offset: number }) => {
      const page = timeslots.slice(offset, offset + limit)
      return { timeslots: page, count: page.length, limit, offset }
    })

    await expect(listAllCapacityTimeslots(listPage, 'project-1')).resolves.toEqual(timeslots)
    expect(listPage).toHaveBeenNthCalledWith(1, { project_id: 'project-1', limit: 100, offset: 0 })
    expect(listPage).toHaveBeenNthCalledWith(2, { project_id: 'project-1', limit: 100, offset: 100 })
  })

  it('drains more than one page of execution plans', async () => {
    const plans = Array.from({ length: 205 }, (_, index) => ({ id: `plan-${index}` })) as ExecutionPlan[]
    const listPage = vi.fn(async ({ limit, offset }: { limit: number; offset: number }) => {
      const page = plans.slice(offset, offset + limit)
      return { execution_plans: page, count: page.length, limit, offset }
    })

    await expect(listAllExecutionPlans(listPage, 'project-2')).resolves.toEqual(plans)
    expect(listPage).toHaveBeenLastCalledWith({ project_id: 'project-2', limit: 100, offset: 200 })
  })
})
