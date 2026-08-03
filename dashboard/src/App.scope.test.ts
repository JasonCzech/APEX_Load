// @vitest-environment jsdom

import { act, createElement } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'

import {
  ScopedAppContextProviders,
} from './App'
import {
  appendProjectTelemetryDiagnostic,
  isCurrentRepositoryRequest,
  isProjectScopedMessageCurrent,
  projectForActiveDomain,
  telemetryDiagnosticsForProject,
} from './appProjectScope'
import type { Domain, Project, TelemetryDiagnosticEntry } from './types'
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
  it('does not retain a project from the previous domain when the active domain has no projects', () => {
    const emptyDomain = { ...domainA, id: 'domain-empty', name: 'Empty Domain' }

    expect(projectForActiveDomain(emptyDomain, projectA)).toBeNull()
    expect(projectForActiveDomain(null, projectA)).toBeNull()
    expect(projectForActiveDomain(domainA, projectA)).toBe(projectA)
  })

  it('provides descendants an empty project and application scope for an empty domain', async () => {
    const projectValue: ProjectContextValue = {
      projects: [],
      activeProject: null,
      setActiveProject: vi.fn(),
      refreshProjects: vi.fn().mockResolvedValue(undefined),
      createAndSelectProject: vi.fn(),
    }
    const applicationValue: ApplicationContextValue = {
      applications: [],
      releases: [],
      releasesLoading: false,
      releasesError: '',
      activeApplication: null,
      activeRelease: null,
      setActiveApplication: vi.fn(),
      setActiveRelease: vi.fn(),
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

  it('rejects repository responses from older generations and previous domains', () => {
    expect(isCurrentRepositoryRequest(2, 'domain-b', 2, 'domain-b')).toBe(true)
    expect(isCurrentRepositoryRequest(1, 'domain-a', 2, 'domain-b')).toBe(false)
    expect(isCurrentRepositoryRequest(2, 'domain-a', 2, 'domain-b')).toBe(false)
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
