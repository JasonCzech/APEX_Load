import { createContext } from 'react'

import { Project, ProjectSettings } from '../types'
import type { DeleteEntityResponse } from './api/useProjectApi'

export type ProjectMutation = {
  name: string
  domain_id?: string
  category_id?: string
  settings?: ProjectSettings
}

export interface ProjectContextValue {
  projects: Project[]
  projectsLoading: boolean
  projectsError: string
  activeProject: Project | null
  setActiveProject: (project: Project) => void
	removeProject: (projectId: string) => void
  refreshProjects: () => Promise<void>
	createAndSelectProject: (project: ProjectMutation | string) => Promise<Project>
	updateProject: (projectId: string, project: ProjectMutation) => Promise<Project>
	deleteProject: (projectId: string) => Promise<DeleteEntityResponse>
}

export const ProjectContext = createContext<ProjectContextValue | null>(null)
