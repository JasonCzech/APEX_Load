import { ReactNode, useCallback, useEffect, useMemo, useState } from 'react'

import { Application, Release } from '../types'
import { useApplicationApi } from './api/useApplicationApi'
import { useProject } from './useProject'
import { ApplicationContext } from './applicationContext'

const STORAGE_KEY = 'apexload.activeApplicationId'
const RELEASE_STORAGE_KEY = 'apexload.activeReleaseId'

export function ApplicationProvider({ children }: { children: ReactNode }) {
  const api = useApplicationApi()
  const { activeProject } = useProject()
  const [applications, setApplications] = useState<Application[]>([])
  const [releases, setReleases] = useState<Release[]>([])
  const [activeApplication, setActiveApplicationState] = useState<Application | null>(null)
  const [activeRelease, setActiveReleaseState] = useState<Release | null>(null)

  const setActiveApplication = useCallback((application: Application) => {
    setActiveApplicationState(application)
    localStorage.setItem(STORAGE_KEY, application.id)
  }, [])

  const setActiveRelease = useCallback((release: Release) => {
    setActiveReleaseState(release)
    localStorage.setItem(RELEASE_STORAGE_KEY, release.id)
  }, [])

  const refreshReleases = useCallback(async (applicationId?: string) => {
    const appId = applicationId || activeApplication?.id
    if (!appId) {
      setReleases([])
      setActiveReleaseState(null)
      return
    }
    const res = await api.listReleases(appId)
    const next = res.releases || []
    setReleases(next)
    const storedId = localStorage.getItem(RELEASE_STORAGE_KEY)
    const selected = next.find(release => release.id === storedId) || next[0] || null
    setActiveReleaseState(selected)
    if (selected) localStorage.setItem(RELEASE_STORAGE_KEY, selected.id)
  }, [api, activeApplication?.id])

  const refreshApplications = useCallback(async () => {
    if (!activeProject?.id) {
      setApplications([])
      setActiveApplicationState(null)
      setReleases([])
      setActiveReleaseState(null)
      return
    }

    const res = await api.listApplications(activeProject.id)
    let next = res.applications || []
    if (next.length === 0) {
      const created = await api.createApplication({ name: 'Legacy', project_id: activeProject.id })
      next = [created]
    }
    setApplications(next)
    const storedId = localStorage.getItem(STORAGE_KEY)
    const selected = next.find(application => application.id === storedId) || next[0] || null
    if (selected) {
      setActiveApplicationState(selected)
      localStorage.setItem(STORAGE_KEY, selected.id)
      return
    }
    setActiveApplicationState(null)
    setReleases([])
    setActiveReleaseState(null)
  }, [activeProject?.id, api])

  const createAndSelectApplication = useCallback(async (name: string) => {
    const created = await api.createApplication({ name, project_id: activeProject?.id })
    setApplications(prev => [...prev, created].sort((a, b) => a.name.localeCompare(b.name)))
    setActiveApplication(created)
    setReleases([])
    setActiveReleaseState(null)
    return created
  }, [activeProject?.id, api, setActiveApplication])

  const createAndSelectRelease = useCallback(async (name: string) => {
    if (!activeApplication) return null
    const created = await api.createRelease(activeApplication.id, { name })
    setReleases(prev => [created, ...prev])
    setActiveRelease(created)
    return created
  }, [api, activeApplication, setActiveRelease])

  useEffect(() => {
    if (!activeProject?.id) {
      setApplications([])
      setActiveApplicationState(null)
      setReleases([])
      setActiveReleaseState(null)
      return
    }

    refreshApplications().catch(() => {
      const fallback: Application = {
        id: 'legacy',
        project_id: activeProject.id,
        name: 'Legacy',
        created_at: new Date(0).toISOString(),
        updated_at: new Date(0).toISOString(),
      }
      setApplications([fallback])
      setActiveApplicationState(fallback)
      setReleases([])
      setActiveReleaseState(null)
    })
  }, [activeProject?.id, refreshApplications])

  useEffect(() => {
    if (activeApplication?.id) {
      refreshReleases(activeApplication.id).catch(() => undefined)
      return
    }
    setReleases([])
    setActiveReleaseState(null)
  }, [activeApplication?.id, refreshReleases])

  const value = useMemo(
    () => ({
      applications,
      releases,
      activeApplication,
      activeRelease,
      setActiveApplication,
      setActiveRelease,
      refreshApplications,
      refreshReleases,
      createAndSelectApplication,
      createAndSelectRelease,
    }),
    [applications, releases, activeApplication, activeRelease, setActiveApplication, setActiveRelease, refreshApplications, refreshReleases, createAndSelectApplication, createAndSelectRelease],
  )

  return <ApplicationContext.Provider value={value}>{children}</ApplicationContext.Provider>
}