import { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react'

import {
  Application,
  ApplicationRepository,
  Domain,
  Project,
  Release,
  RepositoryCategory,
  SharedDataPool,
  StoredScenario,
  StoredScript,
  TestSummary,
} from '../types'
import { useRepositoryApi } from '../hooks/api/useRepositoryApi'
import { useProjectApi, type DeleteEntityResponse } from '../hooks/api/useProjectApi'
import { useScriptApi } from '../hooks/api/useScriptApi'
import { useTestApi } from '../hooks/api/useTestApi'
import StatusBadge from './StatusBadge'
import { SortHeader } from './SortHeader'
import {
  RepositoryAssetKind,
  RepositoryAssetRow,
  RepositoryCatalogFilters,
  RepositoryCatalogSort,
  RepositoryCatalogTab,
  RepositorySortKey,
  buildRepositoryCatalogRows,
  filterRepositoryRows,
  repositoryKindLabel,
  sortRepositoryRows,
  summarizeRepositoryRows,
} from './repositoryCatalogUtils'

interface Props {
  domains: Domain[]
  activeDomain: Domain | null
  activeApplication: Application | null
  applicationRepository: ApplicationRepository | null
  categories: RepositoryCategory[]
  projects: Project[]
  canManageRepository: boolean
  canManageProjects: boolean
  canManageReleases: boolean
  onDomainChange: (domain: Domain) => void
  onRepositoryRefresh: () => Promise<void>
  onProjectsRefresh: () => Promise<void>
  onProjectCreated: (project: { name: string; domain_id?: string; category_id?: string }) => Promise<Project>
  onOpenScript?: (scriptId: string) => void
  onOpenTest?: (testId: string) => void
  onOpenCommandCenter?: (testId: string) => void
  onCreateTestFromScenario?: (scenarioId: string) => void
}

type ProjectAssetResult<T> = {
  map: Record<string, T[]>
  warning?: string
}

type StructureSection = {
  id: string
  name: string
  description?: string
  category?: RepositoryCategory
}

const UNCATEGORIZED_ID = '__uncategorized__'

const CATALOG_TABS: Array<{ id: RepositoryCatalogTab; label: string; kind?: RepositoryAssetKind }> = [
  { id: 'all', label: 'All' },
  { id: 'project', label: 'Projects', kind: 'project' },
  { id: 'release', label: 'Releases', kind: 'release' },
  { id: 'script', label: 'Scripts', kind: 'script' },
  { id: 'scenario', label: 'Scenarios', kind: 'scenario' },
  { id: 'test', label: 'Tests', kind: 'test' },
  { id: 'data_pool', label: 'Data Pools', kind: 'data_pool' },
  { id: 'structure', label: 'Structure' },
]

const DEFAULT_FILTERS: RepositoryCatalogFilters = {
  query: '',
  projectId: 'all',
  releaseId: 'all',
  status: 'all',
  protocol: 'all',
  timeframe: 'all',
  customStartDate: '',
  customEndDate: '',
}

const DEFAULT_SORT: RepositoryCatalogSort = {
  key: 'last_activity',
  direction: 'desc',
}

const BULK_DELETE_ORDER: Record<RepositoryAssetKind, number> = {
  test: 0,
  scenario: 1,
  script: 2,
  data_pool: 3,
  release: 4,
  project: 5,
}

const TIMEFRAME_OPTIONS = [
  { value: 'all', label: 'Any time' },
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
  { value: '90d', label: 'Last 90 days' },
  { value: 'custom', label: 'Custom' },
]

function getErrorMessage(err: unknown, fallback = 'Unable to load assets') {
  if (err instanceof Error && err.message) return err.message
  return fallback
}

function deleteEntityResponseFromError(err: unknown): DeleteEntityResponse | null {
  if (!err || typeof err !== 'object') return null
  const payload = (err as { payload?: unknown }).payload
  if (!payload || typeof payload !== 'object') return null
  const candidate = payload as Partial<DeleteEntityResponse>
  return typeof candidate.deleted === 'string' && candidate.deleted.trim()
    ? candidate as DeleteEntityResponse
    : null
}

function plural(count: number, singular: string, pluralLabel = `${singular}s`) {
  return `${count} ${count === 1 ? singular : pluralLabel}`
}

async function loadProjectAssets<T>(
  projects: Project[],
  label: string,
  loader: (project: Project) => Promise<T[]>,
): Promise<ProjectAssetResult<T>> {
  const results = await Promise.all(projects.map(async project => {
    try {
      return { projectId: project.id, items: await loader(project) }
    } catch (err) {
      return { projectId: project.id, items: [] as T[], error: getErrorMessage(err) }
    }
  }))
  const failures = results.filter(result => result.error)
  const map = Object.fromEntries(results.map(result => [result.projectId, result.items]))
  const warning = failures.length
    ? `${label}: unable to load ${plural(failures.length, 'project')} (${failures.slice(0, 2).map(result => result.error).join('; ')})`
    : undefined
  return { map, warning }
}

function formatDate(value?: string) {
  if (!value) return 'Never'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return 'Never'
  return parsed.toLocaleString(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' })
}

function formatCompactDate(value?: string) {
  if (!value) return 'Never'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return 'Never'
  return parsed.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
}

function statusTone(status?: string) {
  const value = (status || '').toLowerCase()
  if (/(failed|aborted|blocked|error|danger)/.test(value)) return 'danger'
  if (/(running|complete|active|ready|passed|healthy)/.test(value)) return 'healthy'
  if (/(queued|pending|paused|warning|archived|shakeout)/.test(value)) return 'warning'
  return 'neutral'
}

function splitProtocols(value?: string) {
  return (value || '')
    .split(',')
    .map(item => item.trim())
    .filter(Boolean)
}

function renderStatus(row: RepositoryAssetRow) {
  if (row.kind === 'test' && row.status) {
    return <StatusBadge status={row.status} />
  }
  return <span className={`repository-status-pill ${statusTone(row.status)}`}>{row.status || 'Unknown'}</span>
}

function repositoryRowKey(row: RepositoryAssetRow) {
  return `${row.kind}:${row.id}`
}

export default function ApplicationRepositoryPage({
  domains,
  activeDomain,
  activeApplication,
  applicationRepository,
  categories,
  projects,
  canManageRepository,
  canManageProjects,
  canManageReleases,
  onDomainChange,
  onRepositoryRefresh,
  onProjectsRefresh,
  onProjectCreated,
  onOpenScript,
  onOpenTest,
  onOpenCommandCenter,
  onCreateTestFromScenario,
}: Props) {
  const repositoryApi = useRepositoryApi()
  const projectApi = useProjectApi()
  const scriptApi = useScriptApi()
  const testApi = useTestApi()
  const [newCategoryName, setNewCategoryName] = useState('')
  const [newProjectNameByCategory, setNewProjectNameByCategory] = useState<Record<string, string>>({})
  const [newReleaseNameByProject, setNewReleaseNameByProject] = useState<Record<string, string>>({})
  const [releasesByProject, setReleasesByProject] = useState<Record<string, Release[]>>({})
  const [scriptsByProject, setScriptsByProject] = useState<Record<string, StoredScript[]>>({})
  const [scenariosByProject, setScenariosByProject] = useState<Record<string, StoredScenario[]>>({})
  const [testsByProject, setTestsByProject] = useState<Record<string, TestSummary[]>>({})
  const [dataPoolsByProject, setDataPoolsByProject] = useState<Record<string, SharedDataPool[]>>({})
  const [activeTab, setActiveTab] = useState<RepositoryCatalogTab>('all')
  const [filters, setFilters] = useState<RepositoryCatalogFilters>(DEFAULT_FILTERS)
  const [sort, setSort] = useState<RepositoryCatalogSort>(DEFAULT_SORT)
  const [catalogLoading, setCatalogLoading] = useState(false)
  const [catalogWarnings, setCatalogWarnings] = useState<Record<string, string>>({})
  const [catalogLoadedAt, setCatalogLoadedAt] = useState('')
  const [selectedRowKeys, setSelectedRowKeys] = useState<Set<string>>(() => new Set())
  const [bulkBusy, setBulkBusy] = useState(false)
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')
  const catalogLoadGenerationRef = useRef(0)
  const activeDomainIdRef = useRef(activeDomain?.id)
  activeDomainIdRef.current = activeDomain?.id

  const scopedProjects = useMemo(() => {
    const domainId = activeDomain?.id || 'default'
    return projects.filter(project => (project.domain_id || 'default') === domainId)
  }, [activeDomain?.id, projects])

  const categoryIds = useMemo(() => new Set(categories.map(category => category.id)), [categories])

  const structureSections = useMemo<StructureSection[]>(() => {
    const sections: StructureSection[] = categories.map(category => ({
      id: category.id,
      name: category.name,
      description: category.description,
      category,
    }))
    const hasUncategorized = scopedProjects.some(project => !project.category_id || !categoryIds.has(project.category_id))
    if (hasUncategorized) {
      sections.push({
        id: UNCATEGORIZED_ID,
        name: 'Uncategorized',
        description: 'Projects without a repository category.',
      })
    }
    return sections
  }, [categories, categoryIds, scopedProjects])

  const projectsByCategory = useMemo(() => {
    const grouped: Record<string, Project[]> = {}
    for (const section of structureSections) {
      grouped[section.id] = []
    }
    for (const project of scopedProjects) {
      const categoryId = project.category_id && categoryIds.has(project.category_id) ? project.category_id : UNCATEGORIZED_ID
      grouped[categoryId] = grouped[categoryId] || []
      grouped[categoryId].push(project)
    }
    Object.values(grouped).forEach(items => items.sort((a, b) => a.name.localeCompare(b.name)))
    return grouped
  }, [categoryIds, scopedProjects, structureSections])

  const loadCatalogAssets = useCallback(async () => {
    if (scopedProjects.length === 0) {
      return {
        releases: { map: {} as Record<string, Release[]> },
        scripts: { map: {} as Record<string, StoredScript[]> },
        scenarios: { map: {} as Record<string, StoredScenario[]> },
        tests: { map: {} as Record<string, TestSummary[]> },
        dataPools: { map: {} as Record<string, SharedDataPool[]> },
      }
    }

    const [releases, scripts, scenarios, tests, dataPools] = await Promise.all([
      loadProjectAssets(scopedProjects, 'Releases', async project => {
        const response = await repositoryApi.listProjectReleases(project.id, true)
        return response.releases || []
      }),
      loadProjectAssets(scopedProjects, 'Scripts', async project => {
        const response = await scriptApi.listScripts(project.id)
        return response.scripts || []
      }),
      loadProjectAssets(scopedProjects, 'Scenarios', async project => {
        const response = await scriptApi.listScenarios(project.id)
        return response.scenarios || []
      }),
      loadProjectAssets(scopedProjects, 'Tests', async project => {
        const response = await testApi.listTests({ project_id: project.id })
        return response.tests || []
      }),
      loadProjectAssets(scopedProjects, 'Data Pools', async project => {
        const response = await scriptApi.listDataPools(project.id, true)
        return response.data_pools || []
      }),
    ])

    return { releases, scripts, scenarios, tests, dataPools }
  }, [repositoryApi, scopedProjects, scriptApi, testApi])

  const applyCatalogSnapshot = useCallback((snapshot: Awaited<ReturnType<typeof loadCatalogAssets>>) => {
    setReleasesByProject(snapshot.releases.map)
    setScriptsByProject(snapshot.scripts.map)
    setScenariosByProject(snapshot.scenarios.map)
    setTestsByProject(snapshot.tests.map)
    setDataPoolsByProject(snapshot.dataPools.map)
    setCatalogWarnings(Object.fromEntries([
      ['releases', snapshot.releases.warning],
      ['scripts', snapshot.scripts.warning],
      ['scenarios', snapshot.scenarios.warning],
      ['tests', snapshot.tests.warning],
      ['data_pools', snapshot.dataPools.warning],
    ].filter((entry): entry is [string, string] => Boolean(entry[1]))))
    setCatalogLoadedAt(new Date().toISOString())
  }, [])

  const refreshCatalogAssets = useCallback(async () => {
    const domainId = activeDomain?.id
    // A bulk action can finish after the user has already changed domains.
    // Its render-time callback must never take ownership of the new catalog.
    if (domainId !== activeDomainIdRef.current) return false
    const generation = ++catalogLoadGenerationRef.current
    setCatalogLoading(true)
    try {
      const snapshot = await loadCatalogAssets()
      if (generation !== catalogLoadGenerationRef.current || domainId !== activeDomainIdRef.current) return false
      applyCatalogSnapshot(snapshot)
      return true
    } finally {
      if (generation === catalogLoadGenerationRef.current && domainId === activeDomainIdRef.current) {
        setCatalogLoading(false)
      }
    }
  }, [activeDomain?.id, applyCatalogSnapshot, loadCatalogAssets])

  useEffect(() => {
    let cancelled = false
    const domainId = activeDomain?.id
    const generation = ++catalogLoadGenerationRef.current
    setCatalogLoading(true)
    loadCatalogAssets()
      .then(snapshot => {
        if (
          !cancelled
          && generation === catalogLoadGenerationRef.current
          && domainId === activeDomainIdRef.current
        ) {
          applyCatalogSnapshot(snapshot)
        }
      })
      .catch(err => {
        if (
          !cancelled
          && generation === catalogLoadGenerationRef.current
          && domainId === activeDomainIdRef.current
        ) {
          setCatalogWarnings({ catalog: getErrorMessage(err, 'Unable to load repository catalog.') })
        }
      })
      .finally(() => {
        if (
          !cancelled
          && generation === catalogLoadGenerationRef.current
          && domainId === activeDomainIdRef.current
        ) {
          setCatalogLoading(false)
        }
      })
    return () => {
      cancelled = true
    }
  }, [activeDomain?.id, applyCatalogSnapshot, loadCatalogAssets])

  const catalogRows = useMemo(() => buildRepositoryCatalogRows({
    activeDomain,
    activeApplication,
    categories,
    projects,
    releasesByProject,
    scriptsByProject,
    scenariosByProject,
    testsByProject,
    dataPoolsByProject,
  }), [activeApplication, activeDomain, categories, dataPoolsByProject, projects, releasesByProject, scenariosByProject, scriptsByProject, testsByProject])

  const summary = useMemo(() => summarizeRepositoryRows(catalogRows), [catalogRows])

  const statusOptions = useMemo(() => {
    const options = new Map<string, string>()
    catalogRows.forEach(row => {
      if (row.status) options.set(row.status.toLowerCase(), row.status)
    })
    return Array.from(options.values()).sort((a, b) => a.localeCompare(b))
  }, [catalogRows])

  const protocolOptions = useMemo(() => {
    const options = new Set<string>()
    catalogRows.forEach(row => splitProtocols(row.protocol).forEach(protocol => options.add(protocol)))
    return Array.from(options).sort((a, b) => a.localeCompare(b))
  }, [catalogRows])

  const releaseOptions = useMemo(() => {
    const options = new Map<string, { id: string; name: string; projectName?: string }>()
    catalogRows.forEach(row => {
      if (row.kind === 'release' && row.releaseId) {
        options.set(row.releaseId, { id: row.releaseId, name: row.name, projectName: row.projectName })
      }
    })
    return Array.from(options.values())
      .filter(option => filters.projectId === 'all' || catalogRows.some(row => row.kind === 'release' && row.id === option.id && row.projectId === filters.projectId))
      .sort((a, b) => `${a.name} ${a.projectName || ''}`.localeCompare(`${b.name} ${b.projectName || ''}`))
  }, [catalogRows, filters.projectId])

  const filteredRows = useMemo(
    () => filterRepositoryRows(catalogRows, activeTab, filters),
    [activeTab, catalogRows, filters],
  )

  const visibleRows = useMemo(() => sortRepositoryRows(filteredRows, sort), [filteredRows, sort])

  useEffect(() => {
    const validKeys = new Set(catalogRows.map(repositoryRowKey))
    setSelectedRowKeys(prev => {
      if (prev.size === 0) return prev
      let changed = false
      const next = new Set<string>()
      prev.forEach(key => {
        if (validKeys.has(key)) {
          next.add(key)
        } else {
          changed = true
        }
      })
      return changed ? next : prev
    })
  }, [catalogRows])

  const selectedRows = useMemo(
    () => catalogRows.filter(row => selectedRowKeys.has(repositoryRowKey(row))),
    [catalogRows, selectedRowKeys],
  )

  const selectedVisibleRows = useMemo(
    () => visibleRows.filter(row => selectedRowKeys.has(repositoryRowKey(row))),
    [selectedRowKeys, visibleRows],
  )

  const allVisibleRowsSelected = visibleRows.length > 0 && selectedVisibleRows.length === visibleRows.length
  const hasPartialVisibleSelection = selectedVisibleRows.length > 0 && !allVisibleRowsSelected

  const filterActive = filters.query.trim()
    || filters.projectId !== 'all'
    || filters.releaseId !== 'all'
    || filters.status !== 'all'
    || filters.protocol !== 'all'
    || filters.timeframe !== 'all'
    || filters.customStartDate
    || filters.customEndDate

  const submitCategory = async (event: FormEvent) => {
    event.preventDefault()
    if (!activeDomain || !newCategoryName.trim()) return
    setError('')
    setStatus('')
    try {
      await repositoryApi.createRepositoryCategory(activeDomain.id, { name: newCategoryName.trim(), sort_order: categories.length + 1 })
      setNewCategoryName('')
      await onRepositoryRefresh()
      setStatus('Category created.')
    } catch (err) {
      setError(getErrorMessage(err, 'Unable to create category.'))
    }
  }

  const submitProject = async (event: FormEvent, section: StructureSection) => {
    event.preventDefault()
    const name = (newProjectNameByCategory[section.id] || '').trim()
    if (!activeDomain || !name) return
    setError('')
    setStatus('')
    try {
      await onProjectCreated({ name, domain_id: activeDomain.id, category_id: section.category?.id })
      setNewProjectNameByCategory(prev => ({ ...prev, [section.id]: '' }))
      await onProjectsRefresh()
      setStatus('Project created.')
    } catch (err) {
      setError(getErrorMessage(err, 'Unable to create project.'))
    }
  }

  const submitRelease = async (event: FormEvent, project: Project) => {
    event.preventDefault()
    const name = (newReleaseNameByProject[project.id] || '').trim()
    if (!name) return
    setError('')
    setStatus('')
    try {
      const release = await repositoryApi.createProjectRelease(project.id, {
        name,
        application_id: activeApplication?.project_id === project.id ? activeApplication.id : undefined,
      })
      setReleasesByProject(prev => ({ ...prev, [project.id]: [release, ...(prev[project.id] || [])] }))
      setNewReleaseNameByProject(prev => ({ ...prev, [project.id]: '' }))
      setStatus('Release created.')
    } catch (err) {
      setError(getErrorMessage(err, 'Unable to create release.'))
    }
  }

  const updateSort = (key: RepositorySortKey) => {
    setSort(prev => prev.key === key
      ? { key, direction: prev.direction === 'asc' ? 'desc' : 'asc' }
      : { key, direction: key === 'name' || key === 'kind' || key === 'project' || key === 'release' ? 'asc' : 'desc' })
  }

  const toggleVisibleRowsSelection = () => {
    setSelectedRowKeys(prev => {
      const next = new Set(prev)
      if (allVisibleRowsSelected) {
        visibleRows.forEach(row => next.delete(repositoryRowKey(row)))
      } else {
        visibleRows.forEach(row => next.add(repositoryRowKey(row)))
      }
      return next
    })
  }

  const toggleRowSelection = (row: RepositoryAssetRow) => {
    const key = repositoryRowKey(row)
    setSelectedRowKeys(prev => {
      const next = new Set(prev)
      if (next.has(key)) {
        next.delete(key)
      } else {
        next.add(key)
      }
      return next
    })
  }

  const clearSelectedRows = () => {
    setSelectedRowKeys(new Set())
  }

  const canDeleteRow = useCallback((row: RepositoryAssetRow) => {
    if (row.kind === 'project') return canManageProjects
    if (row.kind === 'release') return canManageReleases
    return canManageRepository
  }, [canManageProjects, canManageReleases, canManageRepository])

  const canArchiveRow = useCallback((row: RepositoryAssetRow) => {
    if (row.archived) return false
    if (row.kind === 'release') return canManageReleases
    if (row.kind === 'data_pool') return canManageRepository
    return false
  }, [canManageReleases, canManageRepository])

  const deletableSelectedRows = useMemo(
    () => selectedRows.filter(canDeleteRow).sort((left, right) => BULK_DELETE_ORDER[left.kind] - BULK_DELETE_ORDER[right.kind]),
    [canDeleteRow, selectedRows],
  )

  const archiveableSelectedRows = useMemo(
    () => selectedRows.filter(canArchiveRow),
    [canArchiveRow, selectedRows],
  )

  const deleteRepositoryRow = useCallback(async (row: RepositoryAssetRow) => {
    switch (row.kind) {
      case 'project':
        return projectApi.deleteProject(row.id)
      case 'release':
        await repositoryApi.deleteRelease(row.id)
        break
      case 'script':
        await scriptApi.deleteScript(row.id)
        break
      case 'scenario':
        await scriptApi.deleteScenario(row.id)
        break
      case 'test':
        await testApi.deleteTest(row.id)
        break
      case 'data_pool':
        await scriptApi.deleteDataPool(row.id)
        break
      default:
        break
    }
  }, [projectApi, repositoryApi, scriptApi, testApi])

  const archiveRepositoryRow = useCallback(async (row: RepositoryAssetRow) => {
    if (row.kind === 'release') {
      await repositoryApi.archiveRelease(row.id)
      return
    }
    if (row.kind === 'data_pool') {
      const details = await scriptApi.getDataPool(row.id)
      const pool = details.data_pool
      await scriptApi.updateDataPool(row.id, {
        name: pool.name || row.name,
        description: pool.description,
        schema: pool.schema,
        archived_at: new Date().toISOString(),
        updated_at: pool.updated_at,
      })
    }
  }, [repositoryApi, scriptApi])

  const refreshAfterBulkChange = useCallback(async (rows: RepositoryAssetRow[]) => {
    if (rows.some(row => row.kind === 'project')) {
      await onProjectsRefresh()
      return
    }
    await refreshCatalogAssets()
  }, [onProjectsRefresh, refreshCatalogAssets])

  const handleBulkDelete = async () => {
    if (bulkBusy || deletableSelectedRows.length === 0) return
    const count = deletableSelectedRows.length
    if (!window.confirm(`Delete ${plural(count, 'selected item')}? This cannot be undone.`)) return
    setBulkBusy(true)
    setError('')
    setStatus('')
    const externalSecretCleanupNames = new Set<string>()
    const externalSecretCleanupProviders = new Set<string>()
    const externalSecretCleanupPrefixes = new Set<string>()
    const deletedRows: RepositoryAssetRow[] = []
    let externalSecretCleanupRequired = false
    let failureStage: 'delete' | 'refresh' = 'delete'

    const cleanupNotice = () => {
      const cleanupProviders = externalSecretCleanupProviders.size > 0
        ? ` (${Array.from(externalSecretCleanupProviders).sort().join(', ')})`
        : ''
      if (externalSecretCleanupNames.size > 0) {
        return ` External project secrets retained for administrator cleanup: ${Array.from(externalSecretCleanupNames).sort().join(', ')}${cleanupProviders}.`
      }
      if (externalSecretCleanupPrefixes.size > 0) {
        return ` External project secrets retained for administrator cleanup under ${Array.from(externalSecretCleanupPrefixes).sort().map(prefix => `prefix “${prefix}”`).join(', ')}${cleanupProviders}.`
      }
      return externalSecretCleanupRequired
        ? ` External project secrets were retained and require administrator cleanup${cleanupProviders}.`
        : ''
    }

    const publishDeletionStatus = () => {
      if (deletedRows.length === 0) return
      const unchanged = selectedRows.length - deletedRows.length
      setStatus(`Deleted ${plural(deletedRows.length, 'selected item')}${unchanged > 0 ? `; ${plural(unchanged, 'item')} unchanged.` : '.'}${cleanupNotice()}`)
    }

    const removeDeletedSelections = () => {
      if (deletedRows.length === 0) return
      const deletedKeys = new Set(deletedRows.map(repositoryRowKey))
      setSelectedRowKeys(prev => new Set(Array.from(prev).filter(key => !deletedKeys.has(key))))
    }

    const recordExternalSecretCleanup = (result?: DeleteEntityResponse) => {
      if (!result?.external_secret_cleanup_required) return
      externalSecretCleanupRequired = true
      ;(result.external_secret_cleanup_names || []).forEach(name => externalSecretCleanupNames.add(name))
      ;(result.external_secret_cleanup_providers || []).forEach(provider => externalSecretCleanupProviders.add(provider))
      if (result.external_secret_cleanup_prefix) externalSecretCleanupPrefixes.add(result.external_secret_cleanup_prefix)
      if (result.secret_provider) externalSecretCleanupProviders.add(result.secret_provider)
    }

    try {
      for (const row of deletableSelectedRows) {
        try {
          const result = await deleteRepositoryRow(row)
          deletedRows.push(row)
          recordExternalSecretCleanup(result)
        } catch (err) {
          // Project deletion can durably complete while cleanup/index removal
          // reports a non-2xx response. The API preserves the deletion and
          // external-provider guidance in that error payload; treat the row as
          // deleted before surfacing the failure.
          const partial = deleteEntityResponseFromError(err)
          if (row.kind === 'project' && partial?.deleted === row.id) {
            deletedRows.push(row)
            recordExternalSecretCleanup(partial)
          }
          throw err
        }
      }
      removeDeletedSelections()
      publishDeletionStatus()
      failureStage = 'refresh'
      await refreshAfterBulkChange(deletedRows)
    } catch (err) {
      removeDeletedSelections()
      publishDeletionStatus()
      let refreshAlsoFailed = false
      if (failureStage === 'delete' && deletedRows.length > 0) {
        try {
          await refreshAfterBulkChange(deletedRows)
        } catch {
          refreshAlsoFailed = true
        }
      }
      const fallback = failureStage === 'refresh'
        ? 'Items were deleted, but the repository could not be refreshed.'
        : 'Unable to delete all selected repository items.'
      const suffix = refreshAlsoFailed ? ' The repository refresh also failed; reload before continuing.' : ''
      setError(`${getErrorMessage(err, fallback)}${suffix}`)
    } finally {
      setBulkBusy(false)
    }
  }

  const handleBulkArchive = async () => {
    if (bulkBusy || archiveableSelectedRows.length === 0) return
    const count = archiveableSelectedRows.length
    if (!window.confirm(`Archive ${plural(count, 'selected item')}?`)) return
    setBulkBusy(true)
    setError('')
    setStatus('')
    try {
      const results = await Promise.allSettled(archiveableSelectedRows.map(async row => {
        await archiveRepositoryRow(row)
        return row
      }))
      const archivedRows: RepositoryAssetRow[] = []
      const failures: Array<{ row: RepositoryAssetRow; error: unknown }> = []
      results.forEach((result, index) => {
        if (result.status === 'fulfilled') {
          archivedRows.push(result.value)
        } else {
          failures.push({ row: archiveableSelectedRows[index], error: result.reason })
        }
      })

      if (archivedRows.length > 0) {
        const archivedKeys = new Set(archivedRows.map(repositoryRowKey))
        setSelectedRowKeys(prev => new Set(Array.from(prev).filter(key => !archivedKeys.has(key))))
        const unchanged = selectedRows.length - archivedRows.length
        setStatus(`Archived ${plural(archivedRows.length, 'selected item')}${unchanged > 0 ? `; ${plural(unchanged, 'item')} unchanged.` : '.'}`)
      }

      let refreshError: unknown
      try {
        // Refresh once, after every mutation has settled. Even an API failure
        // can represent an ambiguous server-side outcome, so refresh when no
        // archive was confirmed as well.
        await refreshAfterBulkChange(archiveableSelectedRows)
      } catch (err) {
        refreshError = err
      }

      const errorMessages: string[] = []
      if (failures.length > 0) {
        const details = failures
          .slice(0, 2)
          .map(failure => `${failure.row.name}: ${getErrorMessage(failure.error, 'archive failed')}`)
          .join('; ')
        const additional = failures.length > 2 ? `; ${plural(failures.length - 2, 'additional failure')}` : ''
        errorMessages.push(
          `Unable to archive ${plural(failures.length, 'selected item')}; ${plural(failures.length, 'failed item')} ${failures.length === 1 ? 'remains' : 'remain'} selected: ${details}${additional}.`,
        )
      }
      if (refreshError) {
        errorMessages.push(`The repository refresh failed after the archive attempts: ${getErrorMessage(refreshError, 'refresh failed')}.`)
      }
      if (errorMessages.length > 0) {
        setError(errorMessages.join(' '))
      }
    } finally {
      setBulkBusy(false)
    }
  }

  const emptyCatalogMessage = activeApplication
    ? `No assets found for ${activeApplication.name}.`
    : 'No assets found for this domain.'

  const renderRowAction = (row: RepositoryAssetRow) => {
    if (row.kind === 'script' && onOpenScript) {
      return <button type="button" className="repository-row-action" onClick={() => onOpenScript(row.id)}>Edit</button>
    }
    if (row.kind === 'scenario' && onCreateTestFromScenario) {
      return <button type="button" className="repository-row-action" onClick={() => onCreateTestFromScenario(row.id)}>New Test</button>
    }
    if (row.kind === 'test' && onOpenTest) {
      return (
        <div className="repository-row-actions">
          <button type="button" className="repository-row-action" onClick={() => onOpenTest(row.id)}>Open</button>
          {onOpenCommandCenter && (
            <button type="button" className="repository-row-action" onClick={() => onOpenCommandCenter(row.id)}>Run Dashboard</button>
          )}
        </div>
      )
    }
    if (row.kind === 'project' || row.kind === 'release') {
      return <span className="repository-row-action-muted">Structure</span>
    }
    return <span className="repository-row-action-muted">Catalog</span>
  }

  return (
    <div className="page repository-page">
      <div className="page-header">
        <div>
          <p className="page-kicker">Repository</p>
          <h1>{applicationRepository?.name || 'Application Repository'}</h1>
          <p className="page-subtitle">
            Search, sort, and inspect repository assets
            {activeApplication ? ` for ${activeApplication.name}` : ' across the selected domain'}.
          </p>
        </div>
        <label className="repository-domain-select">
          <span>Domain</span>
          <select
            value={activeDomain?.id || ''}
            onChange={event => {
              const next = domains.find(domain => domain.id === event.target.value)
              if (next) onDomainChange(next)
            }}
          >
            {domains.map(domain => <option key={domain.id} value={domain.id}>{domain.name}</option>)}
          </select>
        </label>
      </div>

      {status && <div className="form-success" role="status">{status}</div>}
      {error && <div className="form-error" role="alert">{error}</div>}

      <section className="repository-catalog-shell">
        <div className="repository-catalog-toolbar">
          <label className="library-search repository-search">
            <span>Search repository</span>
            <input
              value={filters.query}
              onChange={event => setFilters(prev => ({ ...prev, query: event.target.value }))}
              placeholder="Find by name, ID, project, release, protocol, status..."
            />
          </label>
          <div className="repository-refresh-panel">
            <span>{catalogLoading ? 'Loading assets...' : catalogLoadedAt ? `Updated ${formatDate(catalogLoadedAt)}` : 'Not loaded yet'}</span>
            <button type="button" className="repository-row-action" onClick={refreshCatalogAssets} disabled={catalogLoading}>Refresh</button>
          </div>
        </div>

        <div className="repository-filter-row" aria-label="Repository filters">
          <label>
            <span>Project</span>
            <select value={filters.projectId} onChange={event => setFilters(prev => ({ ...prev, projectId: event.target.value, releaseId: 'all' }))}>
              <option value="all">All projects</option>
              {scopedProjects.map(project => <option key={project.id} value={project.id}>{project.name}</option>)}
            </select>
          </label>
          <label>
            <span>Release</span>
            <select value={filters.releaseId} onChange={event => setFilters(prev => ({ ...prev, releaseId: event.target.value }))}>
              <option value="all">All releases</option>
              {releaseOptions.map(option => (
                <option key={option.id} value={option.id}>
                  {option.name}{filters.projectId === 'all' && option.projectName ? ` · ${option.projectName}` : ''}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>Status</span>
            <select value={filters.status} onChange={event => setFilters(prev => ({ ...prev, status: event.target.value }))}>
              <option value="all">All statuses</option>
              {statusOptions.map(option => <option key={option} value={option}>{option}</option>)}
            </select>
          </label>
          <label>
            <span>Protocol</span>
            <select value={filters.protocol} onChange={event => setFilters(prev => ({ ...prev, protocol: event.target.value }))}>
              <option value="all">All protocols</option>
              {protocolOptions.map(option => <option key={option} value={option}>{option}</option>)}
            </select>
          </label>
          <label>
            <span>Activity</span>
            <select
              value={filters.timeframe}
              onChange={event => setFilters(prev => ({
                ...prev,
                timeframe: event.target.value,
                customStartDate: event.target.value === 'custom' ? prev.customStartDate : '',
                customEndDate: event.target.value === 'custom' ? prev.customEndDate : '',
              }))}
            >
              {TIMEFRAME_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
            </select>
          </label>
          <button
            type="button"
            className="repository-reset-filter"
            onClick={() => setFilters(DEFAULT_FILTERS)}
            disabled={!filterActive}
          >
            Reset Filters
          </button>
        </div>
        {filters.timeframe === 'custom' && (
          <div className="repository-custom-time-row" aria-label="Custom activity range">
            <label>
              <span>Activity From</span>
              <input
                type="date"
                value={filters.customStartDate}
                onChange={event => setFilters(prev => ({ ...prev, customStartDate: event.target.value }))}
              />
            </label>
            <label>
              <span>Activity To</span>
              <input
                type="date"
                value={filters.customEndDate}
                onChange={event => setFilters(prev => ({ ...prev, customEndDate: event.target.value }))}
              />
            </label>
          </div>
        )}

        <div className="repository-tab-row" role="tablist" aria-label="Repository asset types">
          {CATALOG_TABS.map(tab => {
            const count = tab.id === 'all'
              ? catalogRows.length
              : tab.id === 'structure'
                ? categories.length + scopedProjects.length
                : summary[tab.kind || 'project']
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                aria-selected={activeTab === tab.id}
                className={`repository-tab ${activeTab === tab.id ? 'active' : ''}`}
                onClick={() => setActiveTab(tab.id)}
              >
                <span>{tab.label}</span>
                <strong>{count}</strong>
              </button>
            )
          })}
        </div>

        {Object.values(catalogWarnings).length > 0 && (
          <div className="repository-warning-list" role="status">
            {Object.entries(catalogWarnings).map(([key, message]) => <span key={key}>{message}</span>)}
          </div>
        )}

        {activeTab !== 'structure' && selectedRows.length > 0 && (
          <div className="repository-bulk-bar" role="region" aria-label="Bulk repository actions">
            <div className="repository-bulk-summary">
              <strong>{plural(selectedRows.length, 'item')} selected</strong>
              <span>{archiveableSelectedRows.length > 0 ? plural(archiveableSelectedRows.length, 'archiveable item') : 'No archiveable items'}</span>
            </div>
            <div className="repository-bulk-actions">
              <button
                type="button"
                className="repository-row-action"
                onClick={handleBulkArchive}
                disabled={bulkBusy || archiveableSelectedRows.length === 0}
              >
                Archive selected ({archiveableSelectedRows.length})
              </button>
              <button
                type="button"
                className="repository-row-action danger"
                onClick={handleBulkDelete}
                disabled={bulkBusy || deletableSelectedRows.length === 0}
              >
                Delete selected ({deletableSelectedRows.length})
              </button>
              <button type="button" className="repository-row-action" onClick={clearSelectedRows} disabled={bulkBusy}>
                Clear
              </button>
            </div>
          </div>
        )}

        {activeTab === 'structure' ? (
          <div className="repository-structure-panel">
            {canManageRepository && activeDomain && (
              <form className="repository-inline-form" onSubmit={submitCategory}>
                <label>
                  <span>New Category</span>
                  <input value={newCategoryName} onChange={event => setNewCategoryName(event.target.value)} placeholder="Payments, Claims, Search" />
                </label>
                <button type="submit" disabled={!newCategoryName.trim()}>Add Category</button>
              </form>
            )}

            <div className="repository-category-grid">
              {structureSections.map(section => (
                <section key={section.id} className="repository-category">
                  <div className="repository-category-header">
                    <div>
                      <h2>{section.name}</h2>
                      {section.description && <p>{section.description}</p>}
                    </div>
                    <span>{projectsByCategory[section.id]?.length || 0} projects</span>
                  </div>

                  {canManageProjects && (
                    <form className="repository-compact-form" onSubmit={event => submitProject(event, section)}>
                      <input
                        value={newProjectNameByCategory[section.id] || ''}
                        onChange={event => setNewProjectNameByCategory(prev => ({ ...prev, [section.id]: event.target.value }))}
                        placeholder="New project name"
                      />
                      <button type="submit" disabled={!(newProjectNameByCategory[section.id] || '').trim()}>Create Project</button>
                    </form>
                  )}

                  <div className="repository-project-list">
                    {(projectsByCategory[section.id] || []).map(project => (
                      <article key={project.id} className="repository-project">
                        <div className="repository-project-title">
                          <div>
                            <h3>{project.name}</h3>
                            <p>{project.id}</p>
                          </div>
                          <span>{(releasesByProject[project.id] || []).length} releases</span>
                        </div>
                        {canManageReleases && (
                          <form className="repository-compact-form" onSubmit={event => submitRelease(event, project)}>
                            <input
                              value={newReleaseNameByProject[project.id] || ''}
                              onChange={event => setNewReleaseNameByProject(prev => ({ ...prev, [project.id]: event.target.value }))}
                              placeholder="New release name"
                            />
                            <button type="submit" disabled={!(newReleaseNameByProject[project.id] || '').trim()}>Add Release</button>
                          </form>
                        )}
                        <div className="repository-release-list">
                          {(releasesByProject[project.id] || []).map(release => (
                            <span key={release.id}>{release.name}{release.archived_at ? ' · archived' : ''}</span>
                          ))}
                          {(releasesByProject[project.id] || []).length === 0 && <em>No releases yet</em>}
                        </div>
                      </article>
                    ))}
                    {(projectsByCategory[section.id] || []).length === 0 && (
                      <div className="repository-empty">No projects in this category.</div>
                    )}
                  </div>
                </section>
              ))}
              {structureSections.length === 0 && (
                <div className="repository-empty wide">No repository structure exists for this domain yet.</div>
              )}
            </div>
          </div>
        ) : (
          <div className="data-table repository-asset-table">
            <table>
              <thead>
                <tr>
                  <th className="repository-select-cell">
                    <input
                      type="checkbox"
                      className="repository-select-checkbox"
                      aria-label="Select all visible repository items"
                      aria-checked={allVisibleRowsSelected ? 'true' : hasPartialVisibleSelection ? 'mixed' : 'false'}
                      checked={allVisibleRowsSelected}
                      disabled={visibleRows.length === 0}
                      onChange={toggleVisibleRowsSelection}
                    />
                  </th>
                  <SortHeader label="Asset" field="name" sort={{ field: sort.key, direction: sort.direction }} onSort={updateSort} />
                  <SortHeader label="Type" field="kind" sort={{ field: sort.key, direction: sort.direction }} onSort={updateSort} />
                  <SortHeader label="Project" field="project" sort={{ field: sort.key, direction: sort.direction }} onSort={updateSort} />
                  <SortHeader label="Release" field="release" sort={{ field: sort.key, direction: sort.direction }} onSort={updateSort} />
                  <SortHeader label="Status" field="status" sort={{ field: sort.key, direction: sort.direction }} onSort={updateSort} />
                  <SortHeader label="Protocol" field="protocol" sort={{ field: sort.key, direction: sort.direction }} onSort={updateSort} />
                  <SortHeader label="Activity" field="last_activity" sort={{ field: sort.key, direction: sort.direction }} onSort={updateSort} />
                  <th>Action</th>
                </tr>
              </thead>
              <tbody>
                {visibleRows.map(row => (
                  <tr key={repositoryRowKey(row)}>
                    <td className="repository-select-cell">
                      <input
                        type="checkbox"
                        className="repository-select-checkbox"
                        aria-label={`Select ${row.name}`}
                        checked={selectedRowKeys.has(repositoryRowKey(row))}
                        onChange={() => toggleRowSelection(row)}
                      />
                    </td>
                        <td aria-label="Repository asset">
                      <div className="repository-asset-name">
                        <strong>{row.name}</strong>
                        <span>{row.summary}</span>
                        <code>{row.id}</code>
                      </div>
                    </td>
                    <td><span className="repository-kind-pill">{repositoryKindLabel(row.kind)}</span></td>
                        <td aria-label="Repository project and category">
                      <div className="repository-table-stack">
                        <strong>{row.projectName || 'None'}</strong>
                        <span>{row.categoryName || 'Uncategorized'}</span>
                      </div>
                    </td>
                    <td>
                      {row.releaseName || row.releaseNames.length ? (
                        <div className="repository-table-stack">
                          <strong>{row.releaseName || row.releaseNames.join(', ')}</strong>
                        </div>
                      ) : (
                        <span className="cell-muted">None</span>
                      )}
                    </td>
                    <td>{renderStatus(row)}</td>
                    <td>
                      {row.protocol ? (
                        <div className="repository-protocol-list">
                          {splitProtocols(row.protocol).map(protocol => <span key={protocol} className="protocol-badge">{protocol}</span>)}
                        </div>
                      ) : (
                        <span className="cell-muted">N/A</span>
                      )}
                    </td>
                    <td>
                      <div className="repository-table-stack">
                        <strong>{formatCompactDate(row.lastActivityAt || row.updatedAt || row.createdAt)}</strong>
                        {row.detail && <span>{row.detail}</span>}
                      </div>
                    </td>
                    <td>{renderRowAction(row)}</td>
                  </tr>
                ))}
                {visibleRows.length === 0 && (
                  <tr className="no-hover">
                    <td colSpan={9}>
                      <div className="repository-empty table-empty">
                        {catalogLoading ? 'Loading repository assets...' : filterActive ? 'No assets match the current filters.' : emptyCatalogMessage}
                      </div>
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}
