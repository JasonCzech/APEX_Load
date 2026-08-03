import { createContext } from 'react'

import { Application, Release } from '../types'

export interface ApplicationContextValue {
  applications: Application[]
  releases: Release[]
  releasesLoading: boolean
  releasesError: string
  activeApplication: Application | null
  activeRelease: Release | null
  setActiveApplication: (application: Application) => void
  setActiveRelease: (release: Release) => void
  refreshApplications: () => Promise<void>
  refreshReleases: (applicationId?: string) => Promise<void>
  createAndSelectApplication: (name: string) => Promise<Application>
  createAndSelectRelease: (name: string) => Promise<Release | null>
}

export const ApplicationContext = createContext<ApplicationContextValue | null>(null)
