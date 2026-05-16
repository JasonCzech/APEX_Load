import { createContext } from 'react'

import { Project, ProjectSettings } from '../types'

export interface ProjectContextValue {
  projects: Project[]
  activeProject: Project | null
  setActiveProject: (project: Project) => void
  refreshProjects: () => Promise<void>
  createAndSelectProject: (project: { name: string; settings?: ProjectSettings } | string) => Promise<Project>
}

export const ProjectContext = createContext<ProjectContextValue | null>(null)