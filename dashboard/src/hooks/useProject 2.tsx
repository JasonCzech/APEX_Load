import { createContext, ReactNode, useCallback, useContext, useEffect, useMemo, useState } from 'react'

import { Project } from '../types'
import { useProjectApi } from './api/useProjectApi'

const STORAGE_KEY = 'stormrunner.activeProjectId'

interface ProjectContextValue {
  projects: Project[]
  activeProject: Project | null
  setActiveProject: (project: Project) => void
  refreshProjects: () => Promise<void>
  createAndSelectProject: (name: string) => Promise<Project>
}

const ProjectContext = createContext<ProjectContextValue | null>(null)

export function ProjectProvider({ children }: { children: ReactNode }) {
  const api = useProjectApi()
  const [projects, setProjects] = useState<Project[]>([])
  const [activeProject, setActiveProjectState] = useState<Project | null>(null)

  const setActiveProject = useCallback((project: Project) => {
    setActiveProjectState(project)
    localStorage.setItem(STORAGE_KEY, project.id)
  }, [])

  const refreshProjects = useCallback(async () => {
    const res = await api.listProjects()
    let nextProjects = res.projects || []
    if (nextProjects.length === 0) {
      const created = await api.createProject({ name: 'Unassigned' })
      nextProjects = [created]
    }
    setProjects(nextProjects)

    const storedId = localStorage.getItem(STORAGE_KEY)
    const selected = nextProjects.find(project => project.id === storedId) || activeProject || nextProjects[0] || null
    if (selected) {
      setActiveProjectState(selected)
      localStorage.setItem(STORAGE_KEY, selected.id)
    }
  }, [api, activeProject])

  const createAndSelectProject = useCallback(async (name: string) => {
    const created = await api.createProject({ name })
    setProjects(prev => [...prev, created].sort((a, b) => a.name.localeCompare(b.name)))
    setActiveProject(created)
    return created
  }, [api, setActiveProject])

  useEffect(() => {
    refreshProjects().catch(() => {
      const fallback: Project = {
        id: 'unassigned',
        name: 'Unassigned',
        created_at: new Date(0).toISOString(),
        updated_at: new Date(0).toISOString(),
      }
      setProjects([fallback])
      setActiveProjectState(fallback)
    })
  }, [])

  const value = useMemo(
    () => ({ projects, activeProject, setActiveProject, refreshProjects, createAndSelectProject }),
    [projects, activeProject, setActiveProject, refreshProjects, createAndSelectProject],
  )

  return <ProjectContext.Provider value={value}>{children}</ProjectContext.Provider>
}

export function useProject() {
  const context = useContext(ProjectContext)
  if (!context) {
    throw new Error('useProject must be used within ProjectProvider')
  }
  return context
}
