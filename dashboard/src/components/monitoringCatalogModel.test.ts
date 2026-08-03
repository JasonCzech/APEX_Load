import { describe, expect, it, vi } from 'vitest'

import type { ImportedDashboard, MonitoringCollector } from '../types'
import { listAllImportedDashboards, listAllMonitoringCollectors } from './monitoringCatalogModel'

describe('monitoring catalog pagination', () => {
  it('drains every collector page for the active project', async () => {
    const collectors = Array.from({ length: 101 }, (_, index): MonitoringCollector => ({
      id: `collector-${index}`,
      project_id: 'project-a',
      name: `Collector ${index}`,
      health: 'healthy',
      allowed_metric_packs: [],
      created_at: '',
      updated_at: '',
    }))
    const listPage = vi.fn(async ({ limit, offset }: { project_id: string; limit: number; offset: number }) => ({
      collectors: collectors.slice(offset, offset + limit),
      count: Math.min(limit, collectors.length - offset),
      limit,
      offset,
    }))

    await expect(listAllMonitoringCollectors(listPage, 'project-a')).resolves.toHaveLength(101)
    expect(listPage).toHaveBeenNthCalledWith(2, { project_id: 'project-a', limit: 100, offset: 100 })
  })

  it('drains every imported-dashboard page for the active project', async () => {
    const dashboards = Array.from({ length: 101 }, (_, index): ImportedDashboard => ({
      id: `dashboard-${index}`,
      project_id: 'project-a',
      name: `Dashboard ${index}`,
      enabled: true,
      panels: [],
    }))
    const listPage = vi.fn(async ({ limit, offset }: { project_id: string; limit: number; offset: number }) => ({
      dashboards: dashboards.slice(offset, offset + limit),
      count: Math.min(limit, dashboards.length - offset),
      limit,
      offset,
    }))

    await expect(listAllImportedDashboards(listPage, 'project-a')).resolves.toHaveLength(101)
    expect(listPage).toHaveBeenNthCalledWith(2, { project_id: 'project-a', limit: 100, offset: 100 })
  })
})
