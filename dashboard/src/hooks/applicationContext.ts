import { createContext } from 'react'

import { Application, Release } from '../types'

export interface ApplicationContextValue {
  applications: Application[]
  applicationsLoading: boolean
  applicationsError: string
  releases: Release[]
  releasesLoading: boolean
  releasesError: string
  activeApplication: Application | null
  activeRelease: Release | null
  setActiveApplication: (application: Application) => void
  setActiveRelease: (release: Release) => void
  removeApplication: (applicationId: string) => void
  removeRelease: (releaseId: string) => void
  refreshApplications: (options?: { preserveApplication?: Application }) => Promise<void>
  refreshReleases: (applicationId?: string, options?: { preserveRelease?: Release }) => Promise<void>
  createAndSelectApplication: (name: string, projectId?: string) => Promise<Application>
  createAndSelectRelease: (name: string) => Promise<Release | null>
}

export const ApplicationContext = createContext<ApplicationContextValue | null>(null)
