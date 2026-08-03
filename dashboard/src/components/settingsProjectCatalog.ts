import type { Project } from '../types'
import { sortProjects } from './settingsPageFormatters'

function projectCatalogRowsMatch(left: Project, right: Project) {
  return JSON.stringify(left) === JSON.stringify(right)
}

function projectCatalogSnapshotCaughtUp(listed: Project, committed: Project) {
  if (projectCatalogRowsMatch(listed, committed)) return true
  const listedUpdatedAt = Date.parse(listed.updated_at)
  const committedUpdatedAt = Date.parse(committed.updated_at)
  return Number.isFinite(listedUpdatedAt)
    && Number.isFinite(committedUpdatedAt)
    && listedUpdatedAt > committedUpdatedAt
}

export function reconcileSettingsProjectSnapshots(
  listed: Project[],
  committedById: Map<string, Project>,
) {
  const listedIds = new Set<string>()
  const reconciled = listed.map(project => {
    listedIds.add(project.id)
    const committed = committedById.get(project.id)
    if (!committed) return project
    if (projectCatalogSnapshotCaughtUp(project, committed)) {
      committedById.delete(project.id)
      return project
    }
    return committed
  })
  committedById.forEach(committed => {
    if (!listedIds.has(committed.id)) reconciled.push(committed)
  })
  return sortProjects(reconciled)
}
