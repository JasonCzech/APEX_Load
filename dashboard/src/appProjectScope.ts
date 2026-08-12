import { appendTelemetryDiagnostic } from './components/monitoringTelemetryDiagnostics'
import type { DashboardAuthStatus, Domain, Project, TelemetryDiagnosticEntry } from './types'

export function dashboardSnapshotAudience(
  authStatus: DashboardAuthStatus | undefined,
  devViewMode: string,
  activeProjectId?: string,
  activeDomainId?: string,
  activeApplicationRepositoryId?: string,
): 'admin' | 'user' {
  if (authStatus?.mode === 'dev') {
    return devViewMode === 'user' ? 'user' : 'admin'
  }
  if (!authStatus || !authStatus.enabled) {
    return 'admin'
  }

  const relevantRoles = [
    ...(authStatus.user?.global_roles || []),
    ...(activeDomainId ? authStatus.user?.domain_roles?.[activeDomainId] || [] : []),
    ...(activeApplicationRepositoryId
      ? authStatus.user?.application_repository_roles?.[activeApplicationRepositoryId] || []
      : []),
    ...(activeProjectId ? authStatus.user?.project_roles?.[activeProjectId] || [] : []),
  ]
  return relevantRoles.some(role => role === 'system_admin' || role === 'project_admin')
    ? 'admin'
    : 'user'
}

export function projectForActiveDomain(activeDomain: Domain | null, selectedProject: Project | null): Project | null {
  if (!activeDomain || !selectedProject) return null
  return (selectedProject.domain_id || 'default') === activeDomain.id ? selectedProject : null
}

export interface ApplicationCreateTarget {
  project: Project
  inDomain: boolean
}

/**
 * Resolve which project a new application belongs to. An explicit projectID
 * wins over the workspace project. A pinned project outside the active domain
 * is reported as out-of-domain so the caller can commit the create without
 * rescoping the view to a project the user can no longer see — a defensive
 * path: scope switches currently remount pages and clear drafts before a
 * cross-domain pin can exist.
 */
export function resolveApplicationCreateTarget(
  projects: Project[],
  domainProjects: Project[],
  activeProject: Project | null,
  requestedProjectID?: string,
): ApplicationCreateTarget | null {
  const project = requestedProjectID
    ? projects.find(candidate => candidate.id === requestedProjectID) || null
    : activeProject
  if (!project) return null
  return {
    project,
    inDomain: domainProjects.some(candidate => candidate.id === project.id),
  }
}

export function isCurrentRepositoryRequest(
  requestGeneration: number,
  requestDomainID: string,
  currentGeneration: number,
  currentDomainID: string,
): boolean {
  return requestGeneration === currentGeneration && requestDomainID === currentDomainID
}

export function isCurrentCatalogRequest(
  requestGeneration: number,
  currentGeneration: number,
): boolean {
  return requestGeneration === currentGeneration
}

export function isCurrentApplicationCreateScope(
  invocation: { projectId?: string; generation: number },
  current: { projectId?: string; generation: number },
): boolean {
  return invocation.generation === current.generation && invocation.projectId === current.projectId
}

export function isProjectScopedMessageCurrent(messageProjectID?: string, currentProjectID?: string): boolean {
  return !messageProjectID || messageProjectID === currentProjectID
}

export function appendProjectTelemetryDiagnostic(
  previous: Record<string, TelemetryDiagnosticEntry[]>,
  projectID: string,
  next: TelemetryDiagnosticEntry,
): Record<string, TelemetryDiagnosticEntry[]> {
  if (!projectID) return previous
  return {
    ...previous,
    [projectID]: appendTelemetryDiagnostic(previous[projectID] || [], next),
  }
}

export function telemetryDiagnosticsForProject(
  diagnosticsByProject: Record<string, TelemetryDiagnosticEntry[]>,
  projectID?: string,
): TelemetryDiagnosticEntry[] {
  return projectID ? diagnosticsByProject[projectID] || [] : []
}
