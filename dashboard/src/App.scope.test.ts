// @vitest-environment jsdom

import { act, createElement } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'

import {
  ScopedAppContextProviders,
} from './App'
import {
  appendProjectTelemetryDiagnostic,
  dashboardSnapshotAudience,
  isCurrentApplicationCreateScope,
  isCurrentCatalogRequest,
  isCurrentRepositoryRequest,
  isProjectScopedMessageCurrent,
  projectForActiveDomain,
  resolveApplicationCreateTarget,
  telemetryDiagnosticsForProject,
} from './appProjectScope'
import type { DashboardAuthStatus, Domain, Project, TelemetryDiagnosticEntry } from './types'
import type { ProjectContextValue } from './hooks/projectContext'
import type { ApplicationContextValue } from './hooks/applicationContext'
import { useProject } from './hooks/useProject'
import { useApplication } from './hooks/useApplication'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const domainA: Domain = {
  id: 'domain-a',
  name: 'Domain A',
  created_at: '2026-07-14T00:00:00Z',
  updated_at: '2026-07-14T00:00:00Z',
}

const projectA: Project = {
  id: 'project-a',
  domain_id: 'domain-a',
  name: 'Project A',
  created_at: '2026-07-14T00:00:00Z',
  updated_at: '2026-07-14T00:00:00Z',
}

function diagnostic(testID: string, summary: string): TelemetryDiagnosticEntry {
  return {
    test_id: testID,
    timestamp: '2026-07-14T00:00:00Z',
    diagnostic: {
      severity: 'warning',
      summary,
    },
  }
}

describe('App project-scoped state', () => {
  it('uses active domain, repository, and project administrator roles for the dashboard audience', () => {
    const authStatus: DashboardAuthStatus = {
      enabled: true,
      mode: 'local',
      authenticated: true,
      user: {
        subject: 'local:domainadmin',
        global_roles: ['viewer'],
        domain_roles: { 'domain-a': ['project_admin'] },
        application_repository_roles: { 'repository-b': ['project_admin'] },
        project_roles: { 'project-c': ['project_admin'] },
      },
    }

    expect(dashboardSnapshotAudience(authStatus, 'admin', undefined, 'domain-a')).toBe('admin')
    expect(dashboardSnapshotAudience(authStatus, 'admin', undefined, 'domain-b', 'repository-b')).toBe('admin')
    expect(dashboardSnapshotAudience(authStatus, 'admin', 'project-c', 'domain-b')).toBe('admin')
    expect(dashboardSnapshotAudience(authStatus, 'admin', 'project-b', 'domain-b', 'repository-a')).toBe('user')
  })

  it('does not retain a project from the previous domain when the active domain has no projects', () => {
    const emptyDomain = { ...domainA, id: 'domain-empty', name: 'Empty Domain' }

    expect(projectForActiveDomain(emptyDomain, projectA)).toBeNull()
    expect(projectForActiveDomain(null, projectA)).toBeNull()
    expect(projectForActiveDomain(domainA, projectA)).toBe(projectA)
  })

  it('provides descendants an empty project and application scope for an empty domain', async () => {
    const projectValue: ProjectContextValue = {
      projects: [],
      projectsLoading: false,
      projectsError: '',
      activeProject: null,
      setActiveProject: vi.fn(),
			removeProject: vi.fn(),
      refreshProjects: vi.fn().mockResolvedValue(undefined),
      createAndSelectProject: vi.fn(),
			updateProject: vi.fn(),
			deleteProject: vi.fn(),
    }
    const applicationValue: ApplicationContextValue = {
      applications: [],
      applicationsLoading: false,
      applicationsError: '',
      releases: [],
      releasesLoading: false,
      releasesError: '',
      activeApplication: null,
      activeRelease: null,
      setActiveApplication: vi.fn(),
      setActiveRelease: vi.fn(),
      removeApplication: vi.fn(),
      removeRelease: vi.fn(),
      refreshApplications: vi.fn().mockResolvedValue(undefined),
      refreshReleases: vi.fn().mockResolvedValue(undefined),
      createAndSelectApplication: vi.fn(),
      createAndSelectRelease: vi.fn(),
    }
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)

    function ScopeConsumer() {
      const project = useProject()
      const application = useApplication()
      return createElement(
        'output',
        null,
        `${project.activeProject?.id || 'no-project'}:${application.activeApplication?.id || 'no-application'}`,
      )
    }

    await act(async () => {
      root.render(createElement(
        ScopedAppContextProviders,
        { projectValue, applicationValue, children: createElement(ScopeConsumer) },
      ))
    })
    expect(container.textContent).toBe('no-project:no-application')

    await act(async () => root.unmount())
    container.remove()
  })

  it('files a new application under the workspace project by default', () => {
    const target = resolveApplicationCreateTarget([projectA], [projectA], projectA)

    expect(target).toEqual({ project: projectA, inDomain: true })
  })

  it('files a new application under an explicitly requested sibling project', () => {
    const sibling: Project = { ...projectA, id: 'project-sibling', name: 'Sibling' }
    const target = resolveApplicationCreateTarget(
      [projectA, sibling],
      [projectA, sibling],
      projectA,
      sibling.id,
    )

    expect(target).toEqual({ project: sibling, inDomain: true })
  })

  it('commits a guarded draft to its originating project outside the active domain', () => {
    const retailProject: Project = { ...projectA, id: 'project-retail', domain_id: 'domain-retail' }
    const target = resolveApplicationCreateTarget(
      [projectA, retailProject],
      [retailProject],
      retailProject,
      projectA.id,
    )

    expect(target).toEqual({ project: projectA, inDomain: false })
  })

  it('has no application target when the active domain has no project', () => {
    expect(resolveApplicationCreateTarget([projectA], [], null)).toBeNull()
    expect(resolveApplicationCreateTarget([projectA], [], null, 'project-missing')).toBeNull()
  })

  it('rejects repository responses from older generations and previous domains', () => {
    expect(isCurrentRepositoryRequest(2, 'domain-b', 2, 'domain-b')).toBe(true)
    expect(isCurrentRepositoryRequest(1, 'domain-a', 2, 'domain-b')).toBe(false)
    expect(isCurrentRepositoryRequest(2, 'domain-a', 2, 'domain-b')).toBe(false)
  })

  it('rejects out-of-order global catalog responses', () => {
    expect(isCurrentCatalogRequest(3, 3)).toBe(true)
    expect(isCurrentCatalogRequest(2, 3)).toBe(false)
  })

  it('rejects an application create completion after the workspace project changes', () => {
    expect(isCurrentApplicationCreateScope(
      { projectId: 'project-a', generation: 4 },
      { projectId: 'project-a', generation: 4 },
    )).toBe(true)
    expect(isCurrentApplicationCreateScope(
      { projectId: 'project-a', generation: 4 },
      { projectId: 'project-b', generation: 5 },
    )).toBe(false)
  })

  it('partitions telemetry diagnostics by project', () => {
    let state = appendProjectTelemetryDiagnostic({}, 'project-a', diagnostic('test-a', 'A warning'))
    state = appendProjectTelemetryDiagnostic(state, 'project-b', diagnostic('test-b', 'B warning'))

    expect(telemetryDiagnosticsForProject(state, 'project-a').map(entry => entry.test_id)).toEqual(['test-a'])
    expect(telemetryDiagnosticsForProject(state, 'project-b').map(entry => entry.test_id)).toEqual(['test-b'])
    expect(telemetryDiagnosticsForProject(state)).toEqual([])
  })

  it('rejects late WebSocket messages from the previous project scope', () => {
    expect(isProjectScopedMessageCurrent(undefined, 'project-b')).toBe(true)
    expect(isProjectScopedMessageCurrent('project-b', 'project-b')).toBe(true)
    expect(isProjectScopedMessageCurrent('project-a', 'project-b')).toBe(false)
    expect(isProjectScopedMessageCurrent('project-a', undefined)).toBe(false)
  })
})
