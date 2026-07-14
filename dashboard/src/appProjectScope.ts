import { appendTelemetryDiagnostic } from './components/monitoringTelemetryDiagnostics'
import type { Domain, Project, TelemetryDiagnosticEntry } from './types'

export function projectForActiveDomain(activeDomain: Domain | null, selectedProject: Project | null): Project | null {
  if (!activeDomain || !selectedProject) return null
  return (selectedProject.domain_id || 'default') === activeDomain.id ? selectedProject : null
}

export function isCurrentRepositoryRequest(
  requestGeneration: number,
  requestDomainID: string,
  currentGeneration: number,
  currentDomainID: string,
): boolean {
  return requestGeneration === currentGeneration && requestDomainID === currentDomainID
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
