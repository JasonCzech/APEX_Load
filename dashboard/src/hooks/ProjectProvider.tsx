import { ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { Project } from '../types'
import { useProjectApi } from './api/useProjectApi'
import { ProjectContext } from './projectContext'
import { readStoredString, writeStoredString } from '../utils/browserStorage'

const STORAGE_KEY = 'apexload.activeProjectId'

export function ProjectProvider({ children }: { children: ReactNode }) {
  const api = useProjectApi()
  const [projects, setProjects] = useState<Project[]>([])
  const [activeProject, setActiveProjectState] = useState<Project | null>(null)
  const refreshGenerationRef = useRef(0)
  const projectMutationGenerationRef = useRef(0)

  const setActiveProject = useCallback((project: Project) => {
    setActiveProjectState(project)
    writeStoredString(STORAGE_KEY, project.id)
  }, [])

  const refreshProjects = useCallback(async () => {
    const refreshGeneration = ++refreshGenerationRef.current
    const mutationGeneration = projectMutationGenerationRef.current
    const res = await api.listProjects()
    if (refreshGenerationRef.current !== refreshGeneration || projectMutationGenerationRef.current !== mutationGeneration) return
    let nextProjects = res.projects || []
    if (nextProjects.length === 0) {
      const created = await api.createProject({ name: 'Unassigned' })
      if (refreshGenerationRef.current !== refreshGeneration || projectMutationGenerationRef.current !== mutationGeneration) return
      nextProjects = [created]
    }
    setProjects(nextProjects)

    const storedId = readStoredString(STORAGE_KEY)
    setActiveProjectState(prev => {
      const selected = nextProjects.find(project => project.id === storedId)
        || nextProjects.find(project => project.id === prev?.id)
        || nextProjects[0]
        || null
      if (selected) {
        writeStoredString(STORAGE_KEY, selected.id)
      }
      return selected
    })
  }, [api])

  const createAndSelectProject = useCallback(async (project: { name: string; domain_id?: string; category_id?: string; settings?: import('../types').ProjectSettings } | string) => {
    const payload = typeof project === 'string' ? { name: project } : project
    const created = await api.createProject(payload)
    projectMutationGenerationRef.current += 1
    refreshGenerationRef.current += 1
    setProjects(prev => [...prev, created].sort((a, b) => a.name.localeCompare(b.name)))
    setActiveProject(created)
    return created
  }, [api, setActiveProject])

  useEffect(() => {
    let cancelled = false
    const mutationGeneration = projectMutationGenerationRef.current
    const refreshPromise = refreshProjects()
    const refreshGeneration = refreshGenerationRef.current
    refreshPromise.catch(() => {
      if (cancelled
        || refreshGenerationRef.current !== refreshGeneration
        || projectMutationGenerationRef.current !== mutationGeneration) return
      const fallback: Project = {
        id: 'unassigned',
        name: 'Unassigned',
        created_at: new Date(0).toISOString(),
        updated_at: new Date(0).toISOString(),
      }
      setProjects([fallback])
      setActiveProjectState(fallback)
    })
    return () => { cancelled = true }
  }, [refreshProjects])

  const value = useMemo(
    () => ({ projects, activeProject, setActiveProject, refreshProjects, createAndSelectProject }),
    [projects, activeProject, setActiveProject, refreshProjects, createAndSelectProject],
  )

  return <ProjectContext.Provider value={value}>{children}</ProjectContext.Provider>
}
