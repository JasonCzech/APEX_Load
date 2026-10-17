import { ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { Project } from '../types'
import { useProjectApi } from './api/useProjectApi'
import { ProjectContext, type ProjectMutation } from './projectContext'
import { catalogSnapshotCaughtUp, reconcileCommittedCatalog } from './catalogReconciliation'
import { readStoredString, removeStoredItem, writeStoredString } from '../utils/browserStorage'

const STORAGE_KEY = 'apexload.activeProjectId'

function projectRowsMatch(left: Project, right: Project) {
	return JSON.stringify(left) === JSON.stringify(right)
}


function deleteResponseFromError(cause: unknown): { deleted?: string } | null {
	if (!cause || typeof cause !== 'object') return null
	const payload = (cause as { payload?: unknown }).payload
	return payload && typeof payload === 'object' ? payload as { deleted?: string } : null
}

export function ProjectProvider({ children }: { children: ReactNode }) {
  const api = useProjectApi()
  const [projects, setProjects] = useState<Project[]>([])
  const [projectsLoading, setProjectsLoading] = useState(true)
  const [projectsError, setProjectsError] = useState('')
  const [activeProject, setActiveProjectState] = useState<Project | null>(null)
  const refreshGenerationRef = useRef(0)
  const projectMutationGenerationRef = useRef(0)
	const projectsRef = useRef<Project[]>([])
	const activeProjectIdRef = useRef<string | null>(null)
	const committedProjectsRef = useRef(new Map<string, Project>())
	const projectTombstonesRef = useRef(new Set<string>())
	projectsRef.current = projects

	const applyAuthoritativeProject = useCallback((project: Project, select: boolean) => {
    // An object returned by a successful project mutation is authoritative.
		// Keep it over later stale successful reads until storage catches up.
    projectMutationGenerationRef.current += 1
    refreshGenerationRef.current += 1
		projectTombstonesRef.current.delete(project.id)
		const currentProject = projectsRef.current.find(candidate => candidate.id === project.id)
		if (!currentProject || !projectRowsMatch(currentProject, project)) {
			committedProjectsRef.current.set(project.id, project)
		}
    setProjects(current => {
      const found = current.some(candidate => candidate.id === project.id)
      const next = found
        ? current.map(candidate => candidate.id === project.id ? project : candidate)
        : [...current, project]
			const sorted = next.sort((left, right) => left.name.localeCompare(right.name))
			projectsRef.current = sorted
			return sorted
    })
    setProjectsLoading(false)
    setProjectsError('')
		if (select || activeProjectIdRef.current === project.id) {
			activeProjectIdRef.current = project.id
			setActiveProjectState(project)
			writeStoredString(STORAGE_KEY, project.id)
		}
  }, [])

	const setActiveProject = useCallback((project: Project) => {
		applyAuthoritativeProject(project, true)
	}, [applyAuthoritativeProject])

	const removeProject = useCallback((projectId: string) => {
		projectMutationGenerationRef.current += 1
		refreshGenerationRef.current += 1
		projectTombstonesRef.current.add(projectId)
		committedProjectsRef.current.delete(projectId)
		const next = projectsRef.current
			.filter(project => project.id !== projectId)
			.sort((left, right) => left.name.localeCompare(right.name))
		projectsRef.current = next
		setProjects(next)
		setProjectsLoading(false)
		setProjectsError('')
		if (activeProjectIdRef.current !== projectId) return
		const fallback = next[0] || null
		activeProjectIdRef.current = fallback?.id || null
		setActiveProjectState(fallback)
		if (fallback) writeStoredString(STORAGE_KEY, fallback.id)
		else removeStoredItem(STORAGE_KEY)
	}, [])

  const refreshProjects = useCallback(async () => {
    const refreshGeneration = ++refreshGenerationRef.current
    const mutationGeneration = projectMutationGenerationRef.current
    const requestIsCurrent = () => (
      refreshGenerationRef.current === refreshGeneration
      && projectMutationGenerationRef.current === mutationGeneration
    )
    setProjectsLoading(true)
    setProjectsError('')
    try {
      const res = await api.listProjects()
      if (!requestIsCurrent()) return
      const reconciled = reconcileCommittedCatalog(res.projects || [], committedProjectsRef.current, {
        identity: project => project.id,
        belongsToScope: () => true,
        acknowledged: (listed, committed) => catalogSnapshotCaughtUp(listed, committed, projectRowsMatch, project => project.updated_at),
        tombstones: projectTombstonesRef.current,
      })
      committedProjectsRef.current = reconciled.committedById
      const nextProjects = reconciled.rows
			nextProjects.sort((left, right) => left.name.localeCompare(right.name))
			projectsRef.current = nextProjects
      setProjects(nextProjects)

      const storedId = readStoredString(STORAGE_KEY)
			const selected = nextProjects.find(project => project.id === activeProjectIdRef.current)
				|| nextProjects.find(project => project.id === storedId)
          || nextProjects[0]
          || null
			activeProjectIdRef.current = selected?.id || null
			setActiveProjectState(selected)
			if (selected) writeStoredString(STORAGE_KEY, selected.id)
			else removeStoredItem(STORAGE_KEY)
    } catch (cause: unknown) {
      if (requestIsCurrent()) {
        // A refresh failure does not invalidate the last catalog that loaded
        // successfully. Keep that scope usable and expose the read error.
        setProjectsError(cause instanceof Error && cause.message
          ? `Unable to load projects: ${cause.message}`
          : 'Unable to load projects.')
      }
      throw cause
    } finally {
      if (requestIsCurrent()) setProjectsLoading(false)
    }
  }, [api])

	const createAndSelectProject = useCallback(async (project: ProjectMutation | string) => {
    const payload = typeof project === 'string' ? { name: project } : project
    const created = await api.createProject(payload)
    setActiveProject(created)
    return created
  }, [api, setActiveProject])

	const updateProject = useCallback(async (projectId: string, project: ProjectMutation) => {
		const updated = await api.updateProject(projectId, project)
		applyAuthoritativeProject(updated, activeProjectIdRef.current === projectId)
		return updated
	}, [api, applyAuthoritativeProject])

	const deleteProject = useCallback(async (projectId: string) => {
		try {
			const result = await api.deleteProject(projectId)
			removeProject(result.deleted || projectId)
			return result
		} catch (cause) {
			if (deleteResponseFromError(cause)?.deleted === projectId) removeProject(projectId)
			throw cause
		}
	}, [api, removeProject])

  useEffect(() => {
    void refreshProjects().catch(() => undefined)
    return () => {
      refreshGenerationRef.current += 1
    }
  }, [refreshProjects])

  const value = useMemo(
		() => ({ projects, projectsLoading, projectsError, activeProject, setActiveProject, removeProject, refreshProjects, createAndSelectProject, updateProject, deleteProject }),
		[projects, projectsLoading, projectsError, activeProject, setActiveProject, removeProject, refreshProjects, createAndSelectProject, updateProject, deleteProject],
  )

  return <ProjectContext.Provider value={value}>{children}</ProjectContext.Provider>
}
