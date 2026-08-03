import { ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { Application, Release } from '../types'
import { useApplicationApi } from './api/useApplicationApi'
import { useProject } from './useProject'
import { ApplicationContext } from './applicationContext'
import { readStoredString, writeStoredString } from '../utils/browserStorage'

const STORAGE_KEY = 'apexload.activeApplicationId'
const RELEASE_STORAGE_KEY = 'apexload.activeReleaseId'

export function ApplicationProvider({ children }: { children: ReactNode }) {
  const api = useApplicationApi()
  const { activeProject } = useProject()
  const activeProjectId = activeProject?.id
  const [applications, setApplications] = useState<Application[]>([])
  const [releases, setReleases] = useState<Release[]>([])
  const [releasesLoading, setReleasesLoading] = useState(false)
  const [releasesError, setReleasesError] = useState('')
  const [activeApplication, setActiveApplicationState] = useState<Application | null>(null)
  const [activeRelease, setActiveReleaseState] = useState<Release | null>(null)
  const [stateProjectId, setStateProjectId] = useState(activeProjectId)
  const projectScopeRef = useRef({ id: activeProjectId, generation: 0 })
  const applicationsRequestGenerationRef = useRef(0)
  const releasesRequestGenerationRef = useRef(0)
  const activeApplicationIdRef = useRef<string | null>(null)

  if (projectScopeRef.current.id !== activeProjectId) {
    projectScopeRef.current = {
      id: activeProjectId,
      generation: projectScopeRef.current.generation + 1,
    }
    applicationsRequestGenerationRef.current += 1
    releasesRequestGenerationRef.current += 1
    activeApplicationIdRef.current = null
  }

  const setActiveApplication = useCallback((application: Application) => {
    const applicationChanged = activeApplicationIdRef.current !== application.id
    activeApplicationIdRef.current = application.id
    setActiveApplicationState(application)
    writeStoredString(STORAGE_KEY, application.id)
    if (!applicationChanged) return
    releasesRequestGenerationRef.current += 1
    setReleases([])
    setReleasesLoading(false)
    setReleasesError('')
    setActiveReleaseState(null)
  }, [])

  const setActiveRelease = useCallback((release: Release) => {
    setActiveReleaseState(release)
    writeStoredString(RELEASE_STORAGE_KEY, release.id)
  }, [])

  const refreshReleases = useCallback(async (applicationId?: string) => {
    const appId = applicationId || activeApplicationIdRef.current
    const scope = { ...projectScopeRef.current }
    const requestGeneration = ++releasesRequestGenerationRef.current
    if (!appId) {
      setReleases([])
      setReleasesLoading(false)
      setReleasesError('')
      setActiveReleaseState(null)
      return
    }
    const requestIsCurrent = () => (
      projectScopeRef.current.id === scope.id
      && projectScopeRef.current.generation === scope.generation
      && releasesRequestGenerationRef.current === requestGeneration
      && activeApplicationIdRef.current === appId
    )
    setReleasesLoading(true)
    setReleasesError('')
    try {
      const res = await api.listReleases(appId)
      if (!requestIsCurrent()) return
      const next = res.releases || []
      setReleases(next)
      const storedId = readStoredString(RELEASE_STORAGE_KEY)
      const selected = next.find(release => release.id === storedId) || next[0] || null
      setActiveReleaseState(selected)
      if (selected) writeStoredString(RELEASE_STORAGE_KEY, selected.id)
    } catch (cause: unknown) {
      if (requestIsCurrent()) {
        setReleasesError(cause instanceof Error && cause.message
          ? `Unable to load releases: ${cause.message}`
          : 'Unable to load releases.')
      }
      throw cause
    } finally {
      if (requestIsCurrent()) setReleasesLoading(false)
    }
  }, [api])

  const refreshApplications = useCallback(async () => {
    const projectId = activeProjectId
    const scope = { ...projectScopeRef.current }
    const requestGeneration = ++applicationsRequestGenerationRef.current
    if (!projectId) {
      setApplications([])
      setActiveApplicationState(null)
      activeApplicationIdRef.current = null
      setReleases([])
      setReleasesLoading(false)
      setReleasesError('')
      setActiveReleaseState(null)
      return
    }

    const res = await api.listApplications(projectId)
    if (
      projectScopeRef.current.id !== scope.id
      || projectScopeRef.current.generation !== scope.generation
      || applicationsRequestGenerationRef.current !== requestGeneration
    ) {
      return
    }
    let next = res.applications || []
    if (next.length === 0) {
      const created = await api.createApplication({ name: 'Legacy', project_id: projectId })
      if (
        projectScopeRef.current.id !== scope.id
        || projectScopeRef.current.generation !== scope.generation
        || applicationsRequestGenerationRef.current !== requestGeneration
      ) {
        return
      }
      next = [created]
    }
    setApplications(next)
    setStateProjectId(projectId)
    const storedId = readStoredString(STORAGE_KEY)
    const selected = next.find(application => application.id === storedId) || next[0] || null
    if (selected) {
      const applicationChanged = activeApplicationIdRef.current !== selected.id
      activeApplicationIdRef.current = selected.id
      setActiveApplicationState(selected)
      writeStoredString(STORAGE_KEY, selected.id)
      if (applicationChanged) {
        releasesRequestGenerationRef.current += 1
        setReleases([])
        setReleasesLoading(false)
        setReleasesError('')
        setActiveReleaseState(null)
      }
      return
    }
    activeApplicationIdRef.current = null
    setActiveApplicationState(null)
    setReleases([])
    setReleasesLoading(false)
    setReleasesError('')
    setActiveReleaseState(null)
  }, [activeProjectId, api])

  const createAndSelectApplication = useCallback(async (name: string) => {
    const projectId = activeProjectId
    const scope = { ...projectScopeRef.current }
    const created = await api.createApplication({ name, project_id: projectId })
    if (
      projectScopeRef.current.id !== scope.id
      || projectScopeRef.current.generation !== scope.generation
    ) {
      return created
    }
    setApplications(prev => [...prev, created].sort((a, b) => a.name.localeCompare(b.name)))
    setActiveApplication(created)
    return created
  }, [activeProjectId, api, setActiveApplication])

  const createAndSelectRelease = useCallback(async (name: string) => {
    const applicationId = activeApplicationIdRef.current
    if (!applicationId) return null
    const scope = { ...projectScopeRef.current }
    const requestGeneration = ++releasesRequestGenerationRef.current
    const created = await api.createRelease(applicationId, { name })
    if (
      projectScopeRef.current.id !== scope.id
      || projectScopeRef.current.generation !== scope.generation
      || releasesRequestGenerationRef.current !== requestGeneration
      || activeApplicationIdRef.current !== applicationId
    ) {
      return created
    }
    setReleases(prev => [created, ...prev])
    setActiveRelease(created)
    return created
  }, [api, setActiveRelease])

  useEffect(() => {
    const projectId = activeProjectId
    const scope = { ...projectScopeRef.current }
    let cancelled = false

    applicationsRequestGenerationRef.current += 1
    releasesRequestGenerationRef.current += 1
    activeApplicationIdRef.current = null
    setStateProjectId(projectId)
    setApplications([])
    setActiveApplicationState(null)
    setReleases([])
    setReleasesLoading(false)
    setReleasesError('')
    setActiveReleaseState(null)

    if (!projectId) {
      return
    }

    const refreshPromise = refreshApplications()
    const requestGeneration = applicationsRequestGenerationRef.current
    refreshPromise.catch(() => {
      if (
        cancelled
        || projectScopeRef.current.id !== scope.id
        || projectScopeRef.current.generation !== scope.generation
        || applicationsRequestGenerationRef.current !== requestGeneration
      ) {
        return
      }
      const fallback: Application = {
        id: 'legacy',
        project_id: projectId,
        name: 'Legacy',
        created_at: new Date(0).toISOString(),
        updated_at: new Date(0).toISOString(),
      }
      activeApplicationIdRef.current = fallback.id
      setApplications([fallback])
      setActiveApplicationState(fallback)
      setReleases([])
      setReleasesLoading(false)
      setReleasesError('')
      setActiveReleaseState(null)
    })
    return () => {
      cancelled = true
    }
  }, [activeProjectId, refreshApplications])

  useEffect(() => {
    if (activeApplication?.id) {
      refreshReleases(activeApplication.id).catch(() => undefined)
      return () => {
        releasesRequestGenerationRef.current += 1
      }
    }
    setReleases([])
    setReleasesLoading(false)
    setReleasesError('')
    setActiveReleaseState(null)
  }, [activeApplication?.id, refreshReleases])

  const scopeIsCurrent = stateProjectId === activeProjectId
  const value = useMemo(
    () => ({
      applications: scopeIsCurrent ? applications : [],
      releases: scopeIsCurrent ? releases : [],
      releasesLoading: scopeIsCurrent ? releasesLoading : false,
      releasesError: scopeIsCurrent ? releasesError : '',
      activeApplication: scopeIsCurrent ? activeApplication : null,
      activeRelease: scopeIsCurrent ? activeRelease : null,
      setActiveApplication,
      setActiveRelease,
      refreshApplications,
      refreshReleases,
      createAndSelectApplication,
      createAndSelectRelease,
    }),
    [scopeIsCurrent, applications, releases, releasesLoading, releasesError, activeApplication, activeRelease, setActiveApplication, setActiveRelease, refreshApplications, refreshReleases, createAndSelectApplication, createAndSelectRelease],
  )

  return <ApplicationContext.Provider value={value}>{children}</ApplicationContext.Provider>
}
