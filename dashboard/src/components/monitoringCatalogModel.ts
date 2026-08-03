import type { ImportedDashboard, MonitoringCollector, MonitoringCollectorListResponse } from '../types'
import { drainBoundedOffsetCatalog } from './catalogPagination'

type ProjectCatalogParams = {
  project_id: string
  limit: number
  offset: number
}

type ImportedDashboardListResponse = {
  dashboards: ImportedDashboard[]
  count: number
  limit?: number
  offset?: number
}

export function listAllMonitoringCollectors(
  listPage: (params: ProjectCatalogParams) => Promise<MonitoringCollectorListResponse>,
  projectId: string,
): Promise<MonitoringCollector[]> {
  return drainBoundedOffsetCatalog(
    async (limit, offset) => ({ items: (await listPage({ project_id: projectId, limit, offset })).collectors || [] }),
    collector => collector.id,
    'Monitoring collector catalog',
  )
}

export function listAllImportedDashboards(
  listPage: (params: ProjectCatalogParams) => Promise<ImportedDashboardListResponse>,
  projectId: string,
): Promise<ImportedDashboard[]> {
  return drainBoundedOffsetCatalog(
    async (limit, offset) => ({ items: (await listPage({ project_id: projectId, limit, offset })).dashboards || [] }),
    dashboard => dashboard.id,
    'Imported dashboard catalog',
  )
}
