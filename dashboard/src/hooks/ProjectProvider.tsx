import { ReactNode, useCallback, useEffect, useMemo, useState } from 'react'

import { Project } from '../types'
import { useProjectApi } from './api/useProjectApi'
import { ProjectContext } from './projectContext'

const STORAGE_KEY = 'apexload.activeProjectId'

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
    setActiveProjectState(prev => {
      const selected = nextProjects.find(project => project.id === storedId)
        || nextProjects.find(project => project.id === prev?.id)
        || nextProjects[0]
        || null
      if (selected) {
        localStorage.setItem(STORAGE_KEY, selected.id)
      }
      return selected
    })
  }, [api])

  const createAndSelectProject = useCallback(async (project: { name: string; domain_id?: string; category_id?: string; settings?: import('../types').ProjectSettings } | string) => {
    const payload = typeof project === 'string' ? { name: project } : project
    const created = await api.createProject(payload)
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
  }, [refreshProjects])

  const value = useMemo(
    () => ({ projects, activeProject, setActiveProject, refreshProjects, createAndSelectProject }),
    [projects, activeProject, setActiveProject, refreshProjects, createAndSelectProject],
  )

  return <ProjectContext.Provider value={value}>{children}</ProjectContext.Provider>
}
