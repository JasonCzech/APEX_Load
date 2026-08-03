import { ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { Application, Release } from '../types'
import { useApplicationApi } from './api/useApplicationApi'
import { useProject } from './useProject'
import { ApplicationContext } from './applicationContext'
import { readStoredString, removeStoredItem, writeStoredString } from '../utils/browserStorage'

const STORAGE_KEY = 'apexload.activeApplicationId'
const RELEASE_STORAGE_KEY = 'apexload.activeReleaseId'

type TimestampedCatalogRow = {
  id: string
  updated_at: string
}

function catalogRowsMatch<T extends TimestampedCatalogRow>(left: T, right: T) {
  const keys = new Set([...Object.keys(left), ...Object.keys(right)])
  return Array.from(keys).every(key => Object.is(
    (left as unknown as Record<string, unknown>)[key],
    (right as unknown as Record<string, unknown>)[key],
  ))
}

function catalogSnapshotCaughtUp<T extends TimestampedCatalogRow>(listed: T, committed: T) {
  if (catalogRowsMatch(listed, committed)) return true
  const listedUpdatedAt = Date.parse(listed.updated_at)
  const committedUpdatedAt = Date.parse(committed.updated_at)
  return Number.isFinite(listedUpdatedAt)
    && Number.isFinite(committedUpdatedAt)
    && listedUpdatedAt > committedUpdatedAt
}

function reconcileCommittedCatalog<T extends TimestampedCatalogRow>(
  listed: T[],
  committedById: Map<string, T>,
  belongsToScope: (row: T) => boolean,
) {
  const listedIds = new Set<string>()
  const reconciled = listed.map(row => {
    listedIds.add(row.id)
    const committed = committedById.get(row.id)
    if (!committed) return row
    if (catalogSnapshotCaughtUp(row, committed)) {
      committedById.delete(row.id)
      return row
    }
    return committed
  })
  committedById.forEach(committed => {
    if (belongsToScope(committed) && !listedIds.has(committed.id)) reconciled.push(committed)
  })
  return reconciled
}

export function ApplicationProvider({ children }: { children: ReactNode }) {
  const api = useApplicationApi()
  const { activeProject } = useProject()
  const activeProjectId = activeProject?.id
  const [applications, setApplications] = useState<Application[]>([])
  const [applicationsLoading, setApplicationsLoading] = useState(false)
  const [applicationsError, setApplicationsError] = useState('')
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
  const activeReleaseIdRef = useRef<string | null>(null)
  const applicationsRef = useRef<Application[]>([])
  const releasesRef = useRef<Release[]>([])
  const applicationTombstonesRef = useRef(new Set<string>())
  const releaseTombstonesRef = useRef(new Set<string>())
  const committedApplicationsRef = useRef(new Map<string, Application>())
  const committedReleasesRef = useRef(new Map<string, Release>())

  applicationsRef.current = applications
  releasesRef.current = releases

  if (projectScopeRef.current.id !== activeProjectId) {
    projectScopeRef.current = {
      id: activeProjectId,
      generation: projectScopeRef.current.generation + 1,
    }
    applicationsRequestGenerationRef.current += 1
    releasesRequestGenerationRef.current += 1
    activeApplicationIdRef.current = null
    activeReleaseIdRef.current = null
  }

  const setActiveApplication = useCallback((application: Application) => {
    const applicationChanged = activeApplicationIdRef.current !== application.id
    const current = applicationsRef.current.find(candidate => candidate.id === application.id)
    if (!current || !catalogRowsMatch(current, application)) {
      committedApplicationsRef.current.set(application.id, application)
    }
    activeApplicationIdRef.current = application.id
    // Callers may hold a freshly-mutated application object (for example after
    // a rename). Keep the catalog row in sync immediately instead of waiting
    // for a best-effort follow-up list request.
    setApplications(prev => {
      const next = prev.map(current => current.id === application.id ? application : current)
      applicationsRef.current = next
      return next
    })
    setActiveApplicationState(application)
    writeStoredString(STORAGE_KEY, application.id)
    if (!applicationChanged) return
    releasesRequestGenerationRef.current += 1
    releasesRef.current = []
    activeReleaseIdRef.current = null
    setReleases([])
    setReleasesLoading(false)
    setReleasesError('')
    setActiveReleaseState(null)
    removeStoredItem(RELEASE_STORAGE_KEY)
  }, [])

  const setActiveRelease = useCallback((release: Release) => {
    const current = releasesRef.current.find(candidate => candidate.id === release.id)
    if (!current || !catalogRowsMatch(current, release)) {
      committedReleasesRef.current.set(release.id, release)
    }
    activeReleaseIdRef.current = release.id
    setReleases(prev => {
      const next = prev.map(current => current.id === release.id ? release : current)
      releasesRef.current = next
      return next
    })
    setActiveReleaseState(release)
    writeStoredString(RELEASE_STORAGE_KEY, release.id)
  }, [])

  const removeApplication = useCallback((applicationId: string) => {
    applicationTombstonesRef.current.add(applicationId)
    committedApplicationsRef.current.delete(applicationId)
    applicationsRequestGenerationRef.current += 1
    const next = applicationsRef.current.filter(application => application.id !== applicationId)
    applicationsRef.current = next
    setApplications(next)
    if (activeApplicationIdRef.current !== applicationId) return

    const fallback = next[0] || null
    activeApplicationIdRef.current = fallback?.id || null
    setActiveApplicationState(fallback)
    if (fallback) writeStoredString(STORAGE_KEY, fallback.id)
    else removeStoredItem(STORAGE_KEY)

    releasesRequestGenerationRef.current += 1
    releasesRef.current = []
    activeReleaseIdRef.current = null
    setReleases([])
    setReleasesLoading(false)
    setReleasesError('')
    setActiveReleaseState(null)
    removeStoredItem(RELEASE_STORAGE_KEY)
  }, [])

  const removeRelease = useCallback((releaseId: string) => {
    releaseTombstonesRef.current.add(releaseId)
    committedReleasesRef.current.delete(releaseId)
    releasesRequestGenerationRef.current += 1
    const next = releasesRef.current.filter(release => release.id !== releaseId)
    releasesRef.current = next
    setReleases(next)
    if (activeReleaseIdRef.current !== releaseId) return

    const fallback = next[0] || null
    activeReleaseIdRef.current = fallback?.id || null
    setActiveReleaseState(fallback)
    if (fallback) writeStoredString(RELEASE_STORAGE_KEY, fallback.id)
    else removeStoredItem(RELEASE_STORAGE_KEY)
  }, [])

  const refreshReleases = useCallback(async (applicationId?: string, options?: { preserveRelease?: Release }) => {
    const appId = applicationId || activeApplicationIdRef.current
    const scope = { ...projectScopeRef.current }
    const requestGeneration = ++releasesRequestGenerationRef.current
    if (!appId) {
      releasesRef.current = []
      activeReleaseIdRef.current = null
      setReleases([])
      setReleasesLoading(false)
      setReleasesError('')
      setActiveReleaseState(null)
      return
    }
    const preserved = options?.preserveRelease
    if (
      preserved
      && preserved.application_id === appId
      && !releaseTombstonesRef.current.has(preserved.id)
    ) {
      committedReleasesRef.current.set(preserved.id, preserved)
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
      const listed = reconcileCommittedCatalog(
        (res.releases || []).filter(release => !releaseTombstonesRef.current.has(release.id)),
        committedReleasesRef.current,
        release => release.application_id === appId && !releaseTombstonesRef.current.has(release.id),
      )
      const next = preserved
        && preserved.application_id === appId
        && !releaseTombstonesRef.current.has(preserved.id)
        && !listed.some(release => release.id === preserved.id)
        ? [...listed, preserved].sort((left, right) => left.name.localeCompare(right.name))
        : listed
      releasesRef.current = next
      setReleases(next)
      const storedId = readStoredString(RELEASE_STORAGE_KEY)
      const selected = next.find(release => release.id === activeReleaseIdRef.current)
        || next.find(release => release.id === storedId)
        || next[0]
        || null
      activeReleaseIdRef.current = selected?.id || null
      setActiveReleaseState(selected)
      if (selected) writeStoredString(RELEASE_STORAGE_KEY, selected.id)
      else removeStoredItem(RELEASE_STORAGE_KEY)
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

  const refreshApplications = useCallback(async (options?: { preserveApplication?: Application }) => {
    const projectId = activeProjectId
    const scope = { ...projectScopeRef.current }
    const requestGeneration = ++applicationsRequestGenerationRef.current
    if (!projectId) {
      applicationsRef.current = []
      setApplications([])
      setApplicationsLoading(false)
      setApplicationsError('')
      setActiveApplicationState(null)
      activeApplicationIdRef.current = null
      releasesRef.current = []
      activeReleaseIdRef.current = null
      setReleases([])
      setReleasesLoading(false)
      setReleasesError('')
      setActiveReleaseState(null)
      return
    }
    const preserved = options?.preserveApplication
    if (
      preserved
      && preserved.project_id === projectId
      && !applicationTombstonesRef.current.has(preserved.id)
    ) {
      committedApplicationsRef.current.set(preserved.id, preserved)
    }

    const requestIsCurrent = () => (
      projectScopeRef.current.id === scope.id
      && projectScopeRef.current.generation === scope.generation
      && applicationsRequestGenerationRef.current === requestGeneration
    )
    setApplicationsLoading(true)
    setApplicationsError('')
    try {
      const res = await api.listApplications(projectId)
      if (!requestIsCurrent()) return
      const listed = reconcileCommittedCatalog(
        (res.applications || []).filter(application => !applicationTombstonesRef.current.has(application.id)),
        committedApplicationsRef.current,
        application => application.project_id === projectId && !applicationTombstonesRef.current.has(application.id),
      )
      // A create response is authoritative for that row even if a list request
      // observes an older snapshot. Merge it into reconciliation so the newly
      // created selection cannot disappear after the mutation succeeds.
      const next = preserved && preserved.project_id === projectId && !listed.some(application => application.id === preserved.id)
        ? [...listed, preserved].sort((a, b) => a.name.localeCompare(b.name))
        : listed
      applicationsRef.current = next
      setApplications(next)
      setStateProjectId(projectId)
      const storedId = readStoredString(STORAGE_KEY)
      const selected = next.find(application => application.id === activeApplicationIdRef.current)
        || next.find(application => application.id === storedId)
        || next[0]
        || null
      if (selected) {
        const applicationChanged = activeApplicationIdRef.current !== selected.id
        activeApplicationIdRef.current = selected.id
        setActiveApplicationState(selected)
        writeStoredString(STORAGE_KEY, selected.id)
        if (applicationChanged) {
          releasesRequestGenerationRef.current += 1
          releasesRef.current = []
          activeReleaseIdRef.current = null
          setReleases([])
          setReleasesLoading(false)
          setReleasesError('')
          setActiveReleaseState(null)
        }
        return
      }
      activeApplicationIdRef.current = null
      setActiveApplicationState(null)
      releasesRef.current = []
      activeReleaseIdRef.current = null
      setReleases([])
      setReleasesLoading(false)
      setReleasesError('')
      setActiveReleaseState(null)
    } catch (cause: unknown) {
      if (requestIsCurrent()) {
        setApplicationsError(cause instanceof Error && cause.message
          ? `Unable to load applications: ${cause.message}`
          : 'Unable to load applications.')
      }
      throw cause
    } finally {
      if (requestIsCurrent()) setApplicationsLoading(false)
    }
  }, [activeProjectId, api])

  const createAndSelectApplication = useCallback(async (name: string, requestedProjectId?: string) => {
    const projectId = requestedProjectId || activeProjectId
    const scope = { ...projectScopeRef.current }
    const created = await api.createApplication({ name, project_id: projectId })
    if (
      projectScopeRef.current.id !== scope.id
      || projectScopeRef.current.generation !== scope.generation
      || projectScopeRef.current.id !== projectId
    ) {
      return created
    }
    setApplications(prev => {
      const next = [...prev.filter(application => application.id !== created.id), created]
        .sort((a, b) => a.name.localeCompare(b.name))
      applicationsRef.current = next
      return next
    })
    committedApplicationsRef.current.set(created.id, created)
    setActiveApplication(created)
    // Starting this request supersedes any list that began before creation.
    // If reconciliation fails, refreshApplications preserves the valid local
    // state and exposes the load error for an explicit retry.
    try {
      await refreshApplications({ preserveApplication: created })
    } catch {
      return created
    }
    return created
  }, [activeProjectId, api, refreshApplications, setActiveApplication])

  const createAndSelectRelease = useCallback(async (name: string) => {
    const applicationId = activeApplicationIdRef.current
    if (!applicationId) return null
    const scope = { ...projectScopeRef.current }
    const created = await api.createRelease(applicationId, { name })
    if (
      projectScopeRef.current.id !== scope.id
      || projectScopeRef.current.generation !== scope.generation
      || activeApplicationIdRef.current !== applicationId
    ) {
      return created
    }
    setReleases(prev => {
      const next = [created, ...prev.filter(release => release.id !== created.id)]
      releasesRef.current = next
      return next
    })
    committedReleasesRef.current.set(created.id, created)
    setActiveRelease(created)
    // Supersede any list that started before the mutation, then reconcile from
    // storage so creating during initial loading cannot replace the existing
    // release list with only the new row. A refresh failure is already surfaced
    // through releasesError; retain the successfully-created release locally.
    try {
      await refreshReleases(applicationId, { preserveRelease: created })
    } catch {
      return created
    }
    return created
  }, [api, refreshReleases, setActiveRelease])

  useEffect(() => {
    const projectId = activeProjectId

    applicationsRequestGenerationRef.current += 1
    releasesRequestGenerationRef.current += 1
    activeApplicationIdRef.current = null
    activeReleaseIdRef.current = null
    setStateProjectId(projectId)
    applicationsRef.current = []
    setApplications([])
    setApplicationsLoading(Boolean(projectId))
    setApplicationsError('')
    setActiveApplicationState(null)
    releasesRef.current = []
    setReleases([])
    setReleasesLoading(false)
    setReleasesError('')
    setActiveReleaseState(null)

    if (!projectId) {
      return
    }

    refreshApplications().catch(() => undefined)
    return () => {
      applicationsRequestGenerationRef.current += 1
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
    releasesRef.current = []
    activeReleaseIdRef.current = null
    setReleasesLoading(false)
    setReleasesError('')
    setActiveReleaseState(null)
  }, [activeApplication?.id, refreshReleases])

  const scopeIsCurrent = stateProjectId === activeProjectId
  const value = useMemo(
    () => ({
      applications: scopeIsCurrent ? applications : [],
      applicationsLoading: scopeIsCurrent ? applicationsLoading : false,
      applicationsError: scopeIsCurrent ? applicationsError : '',
      releases: scopeIsCurrent ? releases : [],
      releasesLoading: scopeIsCurrent ? releasesLoading : false,
      releasesError: scopeIsCurrent ? releasesError : '',
      activeApplication: scopeIsCurrent ? activeApplication : null,
      activeRelease: scopeIsCurrent ? activeRelease : null,
      setActiveApplication,
      setActiveRelease,
      removeApplication,
      removeRelease,
      refreshApplications,
      refreshReleases,
      createAndSelectApplication,
      createAndSelectRelease,
    }),
    [scopeIsCurrent, applications, applicationsLoading, applicationsError, releases, releasesLoading, releasesError, activeApplication, activeRelease, setActiveApplication, setActiveRelease, removeApplication, removeRelease, refreshApplications, refreshReleases, createAndSelectApplication, createAndSelectRelease],
  )

  return <ApplicationContext.Provider value={value}>{children}</ApplicationContext.Provider>
}
