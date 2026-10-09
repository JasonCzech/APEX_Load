import LibraryToolbar from './ux/LibraryToolbar'
import { DEFAULT_REPOSITORY_FILTERS, readRepositoryViewState, writeRepositoryViewState, type RepositoryViewState } from './repositoryViewState'
import './RepositoryWorkspace.css'
import { repositoryDataPoolHref } from './repositoryAssetNavigation'
import TabList from './ux/TabList'
import { confirmAction } from './ux/confirmAction'
import { handleTabKey } from './tabKeyboard'
import { settingsHref } from './settingsNavigation'
import { FormEvent, useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'

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
import { useNavigationGuard, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { readStoredString, writeStoredString } from '../utils/browserStorage'
import StatusBadge from './StatusBadge'
import RepositorySectionTabs from './RepositorySectionTabs'
import { useTopbarContribution } from '../hooks/TopbarContributionProvider'
import { SortHeader } from './SortHeader'
import {
  RepositoryAssetKind,
  RepositoryAssetRow,
  RepositoryCatalogFilters,
  RepositoryCatalogSort,
  RepositoryCatalogTab,
  RepositorySortKey,
  buildRepositoryCatalogRows,
  customRangeInverted,
  filterRepositoryRows,
  repositoryKindLabel,
  sortRepositoryRows,
  summarizeRepositoryRows,
} from './repositoryCatalogUtils'
import { drainBoundedOffsetCatalog } from './catalogPagination'
import {
  createRepositoryCatalogTaskLimiter,
  type RepositoryCatalogTaskLimiter,
} from './applicationRepositoryConcurrency'

interface Props {
  domains: Domain[]
  activeDomain: Domain | null
  activeApplication: Application | null
  activeProject?: Project | null
  activeRelease?: Release | null
  applicationRepository: ApplicationRepository | null
  categories: RepositoryCategory[]
  projects: Project[]
  canManageRepository: boolean
  canManageProjects: boolean
  canManageReleases: boolean
  onDomainChange: (domain: Domain) => void
  onProjectSelect?: (project: Project) => void
  onRepositoryRefresh: () => Promise<void>
  onRepositoryCategoryCreate?: (
    domainId: string,
    data: { name: string; description?: string; sort_order?: number },
  ) => Promise<RepositoryCategory>
  onProjectsRefresh: () => Promise<void>
  onProjectCreated: (project: { name: string; domain_id?: string; category_id?: string }) => Promise<Project>
	onProjectDelete?: (projectId: string) => Promise<DeleteEntityResponse>
  onOpenScript?: (scriptId: string, projectId: string) => void
  onOpenTest?: (testId: string, projectId: string) => void
  onOpenCommandCenter?: (testId: string, projectId: string) => void
  onCreateTestFromScenario?: (scenarioId: string, projectId: string) => void
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

type CategoryCreationDraft = {
  domainId: string
  name: string
  sortOrder: number
}

type ProjectCreationDraft = {
  domainId: string
  categoryId?: string
  name: string
}

type ReleaseCreationDraft = {
  projectId: string
  applicationId?: string
  name: string
}

type ReconciliationTarget = 'repository' | 'projects'

const UNCATEGORIZED_ID = '__uncategorized__'
const REPOSITORY_CATALOG_VIRTUALIZE_THRESHOLD = 100
const REPOSITORY_CATALOG_INITIAL_WINDOW = 20

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

// The page subtree remounts whenever the workspace scope in the route hash
// changes (LazyLoadBoundary is keyed on it), and Structure's "Set as active"
// changes that scope — without persistence the click would also bounce the
// operator back to the All tab.
function readStoredCatalogTab(): RepositoryCatalogTab {
  const stored = readStoredString('apexload.repository.tab')
  return CATALOG_TABS.some(tab => tab.id === stored) ? stored as RepositoryCatalogTab : 'all'
}

const DEFAULT_FILTERS = DEFAULT_REPOSITORY_FILTERS


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
  limitTask: RepositoryCatalogTaskLimiter = createRepositoryCatalogTaskLimiter(),
): Promise<ProjectAssetResult<T>> {
  const results = await Promise.all(projects.map(async project => {
    try {
      return { projectId: project.id, items: await limitTask(() => loader(project)) }
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
  return <><span className={`repository-status-pill ${statusTone(row.status)}`}>{row.kind === 'script' ? `Authoring: ${row.status || 'Not assessed'}` : row.kind === 'scenario' ? 'Saved' : row.status || 'Unknown'}</span>
    {(row.kind === 'script' || row.kind === 'scenario') && <span className="repository-readiness-note">Validation, debug and approval evidence must be checked before execution.</span>}</>
}

function repositoryRowKey(row: RepositoryAssetRow) {
  return `${row.kind}:${row.id}`
}

export default function ApplicationRepositoryPage({
  domains,
  activeDomain,
  activeApplication,
  activeProject,
  activeRelease,
  applicationRepository,
  categories,
  projects,
  canManageRepository,
  canManageProjects,
  canManageReleases,
  onDomainChange,
  onProjectSelect,
  onRepositoryRefresh,
  onRepositoryCategoryCreate,
  onProjectsRefresh,
  onProjectCreated,
	onProjectDelete,
  onOpenScript,
  onOpenTest,
  onOpenCommandCenter,
  onCreateTestFromScenario,
}: Props) {
  const repositoryApi = useRepositoryApi()
  const projectApi = useProjectApi()
  const scriptApi = useScriptApi()
  const testApi = useTestApi()
  const [categoryCreationDraft, setCategoryCreationDraft] = useState<CategoryCreationDraft | null>(null)
  const [projectCreationDrafts, setProjectCreationDrafts] = useState<Record<string, ProjectCreationDraft>>({})
  const [releaseCreationDrafts, setReleaseCreationDrafts] = useState<Record<string, ReleaseCreationDraft>>({})
  const [releasesByProject, setReleasesByProject] = useState<Record<string, Release[]>>({})
  const [scriptsByProject, setScriptsByProject] = useState<Record<string, StoredScript[]>>({})
  const [scenariosByProject, setScenariosByProject] = useState<Record<string, StoredScenario[]>>({})
  const [testsByProject, setTestsByProject] = useState<Record<string, TestSummary[]>>({})
  const [dataPoolsByProject, setDataPoolsByProject] = useState<Record<string, SharedDataPool[]>>({})
  const viewKey = `apexload.repository.view:${activeDomain?.id || ''}:${activeProject?.id || ''}:${activeApplication?.id || ''}`
  const [initialView] = useState(() => {
    const restored = readRepositoryViewState(viewKey)
    try { if (!window.sessionStorage.getItem(viewKey)) restored.tab = readStoredCatalogTab() } catch { /* Use defaults. */ }
    return restored
  })
  const [viewScope, setViewScope] = useState(viewKey)
  const [activeTab, setActiveTab] = useState<RepositoryCatalogTab>(() => initialView.tab)
  const [filters, setFilters] = useState<RepositoryCatalogFilters>(initialView.filters)
  const [sort, setSort] = useState<RepositoryCatalogSort>(initialView.sort)
  const [page, setPage] = useState(initialView.page)
  const [pageSize, setPageSize] = useState<RepositoryViewState['pageSize']>(initialView.pageSize)
  const [inspectedKey, setInspectedKey] = useState<string | null>(null)
  const pageRef = useRef<HTMLDivElement | null>(null)
  const restoreScrollRef = useRef<number | null>(initialView.scrollTop)
  if (viewScope !== viewKey) {
    const restored = readRepositoryViewState(viewKey)
    try { if (!window.sessionStorage.getItem(viewKey)) restored.tab = readStoredCatalogTab() } catch { /* Use defaults. */ }
    setViewScope(viewKey)
    setActiveTab(restored.tab)
    setFilters(restored.filters)
    setSort(restored.sort)
    setPage(restored.page)
    setPageSize(restored.pageSize)
    setInspectedKey(null)
    restoreScrollRef.current = restored.scrollTop
  }
  const savedViewRef = useRef<RepositoryViewState>(initialView)
  savedViewRef.current = { tab: activeTab, filters, sort, page, pageSize, scrollTop: savedViewRef.current.scrollTop }
  useEffect(() => { writeRepositoryViewState(viewKey, savedViewRef.current) }, [viewKey, activeTab, filters, sort, page, pageSize])
  useEffect(() => {
    const scrollElement = pageRef.current?.closest('main')
    const save = () => {
      if (restoreScrollRef.current !== null) return
      savedViewRef.current.scrollTop = scrollElement?.scrollTop || 0
      writeRepositoryViewState(viewKey, savedViewRef.current)
    }
    scrollElement?.addEventListener('scroll', save, { passive: true })
    return () => { scrollElement?.removeEventListener('scroll', save) }
  }, [viewKey])
  const [catalogLoading, setCatalogLoading] = useState(false)
  const [catalogWarnings, setCatalogWarnings] = useState<Record<string, string>>({})
  const [catalogLoadedAt, setCatalogLoadedAt] = useState('')
  const [selectedRowKeys, setSelectedRowKeys] = useState<Set<string>>(() => new Set())
  const [bulkBusy, setBulkBusy] = useState(false)
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')
  const [reconciliationWarnings, setReconciliationWarnings] = useState<Partial<Record<ReconciliationTarget, string>>>({})
  const catalogLoadGenerationRef = useRef(0)
  const categoryCreationDraftRef = useRef(categoryCreationDraft)
  const projectCreationDraftsRef = useRef(projectCreationDrafts)
  const releaseCreationDraftsRef = useRef(releaseCreationDrafts)
  categoryCreationDraftRef.current = categoryCreationDraft
  projectCreationDraftsRef.current = projectCreationDrafts
  releaseCreationDraftsRef.current = releaseCreationDrafts
  const activeDomainIdRef = useRef(activeDomain?.id)
  activeDomainIdRef.current = activeDomain?.id

  const repositoryTopbarContribution = useMemo(() => ({
    id: 'repository-page',
    title: 'Repository',
    controls: <RepositorySectionTabs current="catalog" />,
    priority: 10,
  }), [])
  useTopbarContribution(repositoryTopbarContribution)

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

    const limitTask = createRepositoryCatalogTaskLimiter()
    const [releases, scripts, scenarios, tests, dataPools] = await Promise.all([
      loadProjectAssets(scopedProjects, 'Releases', async project => {
        const response = await repositoryApi.listProjectReleases(project.id, true)
        return response.releases || []
      }, limitTask),
      loadProjectAssets(scopedProjects, 'Scripts', async project => {
        const response = await scriptApi.listScripts(project.id)
        return response.scripts || []
      }, limitTask),
      loadProjectAssets(scopedProjects, 'Scenarios', async project => {
        const response = await scriptApi.listScenarios(project.id)
        return response.scenarios || []
      }, limitTask),
      loadProjectAssets(scopedProjects, 'Tests', async project => {
        return drainBoundedOffsetCatalog(
          async (limit, offset) => {
            const response = await testApi.listTests({ project_id: project.id, limit, offset })
            const items = response.tests || []
            return {
              items,
              hasMore: response.total === undefined
                ? undefined
                : offset + items.length < response.total,
            }
          },
          test => test.id,
          `Tests for project ${project.id}`,
        )
      }, limitTask),
      loadProjectAssets(scopedProjects, 'Data Pools', async project => {
        const response = await scriptApi.listDataPools(project.id, true)
        return response.data_pools || []
      }, limitTask),
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
  const catalogTableScrollRef = useRef<HTMLDivElement | null>(null)
  const pageCount = pageSize === 'all' ? 1 : Math.max(1, Math.ceil(visibleRows.length / pageSize))
  const currentPage = Math.min(page, pageCount - 1)
  const pageStart = pageSize === 'all' ? 0 : currentPage * pageSize
  const pageRows = pageSize === 'all' ? visibleRows : visibleRows.slice(pageStart, pageStart + pageSize)
  const virtualizeCatalogRows = pageSize === 'all' && visibleRows.length > REPOSITORY_CATALOG_VIRTUALIZE_THRESHOLD
  const catalogRowVirtualizer = useVirtualizer({
    count: visibleRows.length,
    getScrollElement: () => catalogTableScrollRef.current,
    estimateSize: () => 82,
    overscan: 10,
    enabled: virtualizeCatalogRows,
    initialRect: { width: 1240, height: 720 },
    getItemKey: index => visibleRows[index] ? repositoryRowKey(visibleRows[index]) : index,
  })
  const virtualCatalogRows = virtualizeCatalogRows ? catalogRowVirtualizer.getVirtualItems() : []
  const renderedCatalogRows = virtualizeCatalogRows
    ? (virtualCatalogRows.length > 0
        ? virtualCatalogRows.flatMap(item => {
            const row = visibleRows[item.index]
            return row ? [{ row, virtualIndex: item.index }] : []
          })
        : visibleRows.slice(0, REPOSITORY_CATALOG_INITIAL_WINDOW).map((row, index) => ({ row, virtualIndex: index })))
    : pageRows.map(row => ({ row, virtualIndex: null }))
  const catalogPaddingTop = virtualCatalogRows[0]?.start || 0
  const catalogPaddingBottom = virtualCatalogRows.length > 0
    ? Math.max(0, catalogRowVirtualizer.getTotalSize() - virtualCatalogRows[virtualCatalogRows.length - 1].end)
    : (virtualizeCatalogRows
        ? Math.max(0, (visibleRows.length - REPOSITORY_CATALOG_INITIAL_WINDOW) * 82)
        : 0)
  const filterSignature = JSON.stringify([viewKey, activeTab, filters, sort])
  const previousFiltersRef = useRef(filterSignature)
  const previousFilterScopeRef = useRef(viewKey)
  useEffect(() => {
    if (previousFiltersRef.current !== filterSignature) {
      previousFiltersRef.current = filterSignature
      if (previousFilterScopeRef.current === viewKey) setPage(0)
      previousFilterScopeRef.current = viewKey
      if (catalogTableScrollRef.current) catalogTableScrollRef.current.scrollTop = 0
      if (virtualizeCatalogRows) catalogRowVirtualizer.scrollToOffset(0)
    }
  }, [filterSignature, viewKey, catalogRowVirtualizer, virtualizeCatalogRows])
  useEffect(() => {
    if (!catalogLoadedAt || catalogLoading || restoreScrollRef.current === null) return
    const scrollElement = pageRef.current?.closest('main')
    if (scrollElement) scrollElement.scrollTop = restoreScrollRef.current
    savedViewRef.current.scrollTop = restoreScrollRef.current
    restoreScrollRef.current = null
  }, [catalogLoadedAt, catalogLoading])
  useEffect(() => {
    writeStoredString('apexload.repository.tab', activeTab)
  }, [activeTab])
  const selectableVisibleRows = useMemo(
    () => activeTab === 'structure' ? [] : visibleRows,
    [activeTab, visibleRows],
  )

  useEffect(() => {
    const visibleKeys = new Set(selectableVisibleRows.map(repositoryRowKey))
    setSelectedRowKeys(prev => {
      if (prev.size === 0) return prev
      let changed = false
      const next = new Set<string>()
      prev.forEach(key => {
        if (visibleKeys.has(key)) {
          next.add(key)
        } else {
          changed = true
        }
      })
      return changed ? next : prev
    })
  }, [selectableVisibleRows])

  const selectedVisibleRows = useMemo(
    () => selectableVisibleRows.filter(row => selectedRowKeys.has(repositoryRowKey(row))),
    [selectableVisibleRows, selectedRowKeys],
  )

  const selectedPageRows = pageRows.filter(row => selectedRowKeys.has(repositoryRowKey(row)))
  const allVisibleRowsSelected = pageRows.length > 0 && selectedPageRows.length === pageRows.length
  const hasPartialVisibleSelection = selectedPageRows.length > 0 && !allVisibleRowsSelected

  const customRangeErrorId = useId()
  const customRangeError = filters.timeframe === 'custom' && customRangeInverted(filters)

  const filterActive = filters.query.trim()
    || filters.projectId !== 'all'
    || filters.releaseId !== 'all'
    || filters.status !== 'all'
    || filters.protocol !== 'all'
    || filters.timeframe !== 'all'
    || filters.customStartDate
    || filters.customEndDate

  const updateReconciliationWarning = useCallback((target: ReconciliationTarget, message?: string) => {
    setReconciliationWarnings(previous => {
      const next = { ...previous }
      if (message) next[target] = message
      else delete next[target]
      return next
    })
  }, [])

  const reconcileCommittedDrafts = useCallback(async (target: ReconciliationTarget) => {
    try {
      if (target === 'repository') await onRepositoryRefresh()
      else await onProjectsRefresh()
      updateReconciliationWarning(target)
      return true
    } catch {
      updateReconciliationWarning(
        target,
        target === 'repository'
          ? 'Repository changes were saved, but repository data could not be fully refreshed. The mutation succeeded—do not save it again.'
          : 'Project changes were saved, but the project list could not be refreshed. The mutation succeeded—do not create the project again.',
      )
      return false
    }
  }, [onProjectsRefresh, onRepositoryRefresh, updateReconciliationWarning])

  const commitCategoryDraft = useCallback(async (draft: CategoryCreationDraft) => {
    const name = draft.name.trim()
    if (!name) return false
    const createCategory = onRepositoryCategoryCreate || repositoryApi.createRepositoryCategory
    await createCategory(draft.domainId, { name, sort_order: draft.sortOrder })
    if (categoryCreationDraftRef.current === draft) {
      categoryCreationDraftRef.current = null
      setCategoryCreationDraft(null)
    }
    return true
  }, [onRepositoryCategoryCreate, repositoryApi.createRepositoryCategory])

  const commitProjectDraft = useCallback(async (key: string, draft: ProjectCreationDraft) => {
    const name = draft.name.trim()
    if (!name) return false
    await onProjectCreated({ name, domain_id: draft.domainId, category_id: draft.categoryId })
    if (projectCreationDraftsRef.current[key] === draft) {
      const next = { ...projectCreationDraftsRef.current }
      delete next[key]
      projectCreationDraftsRef.current = next
      setProjectCreationDrafts(next)
    }
    return true
  }, [onProjectCreated])

  const commitReleaseDraft = useCallback(async (key: string, draft: ReleaseCreationDraft) => {
    const name = draft.name.trim()
    if (!name) return false
    // Bind the release to the application that is active AT COMMIT TIME when
    // it belongs to the draft's project. The id frozen at the first keystroke
    // can go stale: application switches no longer remount this page, so a
    // draft typed under application A and submitted after switching to B
    // would otherwise silently bind to A while every visible cue says B.
    // (Guard-saves run before scope switches, so their binding is unchanged.)
    const applicationId = activeApplication?.project_id === draft.projectId
      ? activeApplication.id
      : draft.applicationId
    const release = await repositoryApi.createProjectRelease(draft.projectId, {
      name,
      application_id: applicationId,
    })
    setReleasesByProject(previous => ({
      ...previous,
      [draft.projectId]: [release, ...(previous[draft.projectId] || []).filter(candidate => candidate.id !== release.id)],
    }))
    if (releaseCreationDraftsRef.current[key] === draft) {
      const next = { ...releaseCreationDraftsRef.current }
      delete next[key]
      releaseCreationDraftsRef.current = next
      setReleaseCreationDrafts(next)
    }
    return true
  }, [activeApplication, repositoryApi])

  const saveAllCreationDrafts = useCallback(async () => {
    setError('')
    setStatus('')
    let repositoryChanged = false
    let projectsChanged = false

    const categoryDraft = categoryCreationDraftRef.current
    if (categoryDraft?.name.trim()) {
      repositoryChanged = await commitCategoryDraft(categoryDraft)
      if (repositoryChanged) {
        await reconcileCommittedDrafts('repository')
        repositoryChanged = false
      }
    }
    for (const [key, draft] of Object.entries(projectCreationDraftsRef.current)) {
      if (!draft.name.trim()) continue
      projectsChanged = (await commitProjectDraft(key, draft)) || projectsChanged
      if (projectsChanged) {
        await reconcileCommittedDrafts('projects')
        projectsChanged = false
      }
    }
    for (const [key, draft] of Object.entries(releaseCreationDraftsRef.current)) {
      if (!draft.name.trim()) continue
      await commitReleaseDraft(key, draft)
    }

    // Reconciliation is GET-only and best effort. A failure here must not keep
    // the guard wired to replay an already-committed POST.
    if (repositoryChanged) await reconcileCommittedDrafts('repository')
    if (projectsChanged) await reconcileCommittedDrafts('projects')
    setStatus('Repository drafts saved.')
    return true
  }, [commitCategoryDraft, commitProjectDraft, commitReleaseDraft, reconcileCommittedDrafts])

  const creationDraftDirty = Boolean(
    categoryCreationDraft?.name.trim()
    || Object.values(projectCreationDrafts).some(draft => draft.name.trim())
    || Object.values(releaseCreationDrafts).some(draft => draft.name.trim()),
  )
  const creationDraftDirtyRef = useRef(false)
  creationDraftDirtyRef.current = creationDraftDirty
  const saveAllCreationDraftsRef = useRef(saveAllCreationDrafts)
  saveAllCreationDraftsRef.current = saveAllCreationDrafts
  const creationDraftGuard = useMemo<NavigationGuard>(() => ({
    id: 'application-repository-creation-drafts',
    label: 'Repository creation form',
    isDirty: () => creationDraftDirtyRef.current,
    save: () => saveAllCreationDraftsRef.current(),
    discard: () => {
      categoryCreationDraftRef.current = null
      projectCreationDraftsRef.current = {}
      releaseCreationDraftsRef.current = {}
      setCategoryCreationDraft(null)
      setProjectCreationDrafts({})
      setReleaseCreationDrafts({})
    },
  }), [])
  useNavigationGuard(creationDraftGuard, creationDraftDirty)

  const submitCategory = async (event: FormEvent) => {
    event.preventDefault()
    const draft = categoryCreationDraftRef.current
    if (!draft?.name.trim()) return
    setError('')
    setStatus('')
    try {
      await commitCategoryDraft(draft)
      setStatus('Category created.')
      await reconcileCommittedDrafts('repository')
    } catch (err) {
      setError(getErrorMessage(err, 'Unable to create category.'))
    }
  }

  const submitProject = async (event: FormEvent, section: StructureSection) => {
    event.preventDefault()
    if (!activeDomain) return
    const key = `${activeDomain.id}:${section.id}`
    const draft = projectCreationDraftsRef.current[key]
    if (!draft?.name.trim()) return
    setError('')
    setStatus('')
    try {
      await commitProjectDraft(key, draft)
      setStatus('Project created.')
      await reconcileCommittedDrafts('projects')
    } catch (err) {
      setError(getErrorMessage(err, 'Unable to create project.'))
    }
  }

  const submitRelease = async (event: FormEvent, project: Project) => {
    event.preventDefault()
    const draft = releaseCreationDraftsRef.current[project.id]
    if (!draft?.name.trim()) return
    setError('')
    setStatus('')
    try {
      await commitReleaseDraft(project.id, draft)
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
        pageRows.forEach(row => next.delete(repositoryRowKey(row)))
      } else {
        pageRows.forEach(row => next.add(repositoryRowKey(row)))
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
    () => selectedVisibleRows.filter(canDeleteRow).sort((left, right) => BULK_DELETE_ORDER[left.kind] - BULK_DELETE_ORDER[right.kind]),
    [canDeleteRow, selectedVisibleRows],
  )

  const archiveableSelectedRows = useMemo(
    () => selectedVisibleRows.filter(canArchiveRow),
    [canArchiveRow, selectedVisibleRows],
  )

  const deleteRepositoryRow = useCallback(async (row: RepositoryAssetRow) => {
    switch (row.kind) {
      case 'project':
				return onProjectDelete ? onProjectDelete(row.id) : projectApi.deleteProject(row.id)
      case 'release':
        await repositoryApi.deleteRelease(row.id)
        break
      case 'script':
        await scriptApi.deleteScript(row.id)
        break
      case 'scenario':
        await scriptApi.deleteScenario(row.id, row.updatedAt)
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
	}, [onProjectDelete, projectApi, repositoryApi, scriptApi, testApi])

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
    if (!(await confirmAction(`Delete ${plural(count, 'selected item')}? This cannot be undone.`, { title: 'Delete items', confirmLabel: 'Delete', destructive: true }))) return
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
      const unchanged = selectedVisibleRows.length - deletedRows.length
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
    if (!(await confirmAction(`Archive ${plural(count, 'selected item')}? Archived scripts and scenarios can be restored from their library's archived view.`, { title: 'Archive items', confirmLabel: 'Archive' }))) return
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
        const unchanged = selectedVisibleRows.length - archivedRows.length
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

  const scopedScenarioIds = useMemo(() => new Set(catalogRows.filter(row => row.kind === 'scenario').map(row => `${row.projectId}:${row.id}`)), [catalogRows])
  const scopedRunIds = useMemo(() => new Set(catalogRows.filter(row => row.kind === 'test').map(row => `${row.projectId}:${row.id}`)), [catalogRows])
  const inspected = catalogRows.find(row => repositoryRowKey(row) === inspectedKey)
  const inspectedScenario = inspected?.kind === 'scenario' ? scenariosByProject[inspected.projectId]?.find(item => item.id === inspected.id) : undefined
  const relatedScenarios = inspected?.kind === 'script' ? (scenariosByProject[inspected.projectId] || []).filter(item => scopedScenarioIds.has(`${inspected.projectId}:${item.id}`) && item.scripts.some(script => script.script_id === inspected.id)) : []
  const relatedRuns = inspected ? (testsByProject[inspected.projectId] || []).filter(test =>
    scopedRunIds.has(`${inspected.projectId}:${test.id}`) && (inspected.kind === 'scenario' ? test.scenario_id === inspected.id : inspected.kind === 'script' && test.groups?.some(group => group.script_id === inspected.id))) : []
  useEffect(() => {
    if (inspectedKey) pageRef.current?.querySelector<HTMLElement>('.repository-inspection')?.focus()
  }, [inspectedKey])
  const [structureFocus, setStructureFocus] = useState('')
  useEffect(() => {
    if (activeTab !== 'structure' || !structureFocus) return
    const element = document.getElementById(`repository-project-${structureFocus}`)
    element?.scrollIntoView?.({ block: 'center' })
    element?.focus()
  }, [activeTab, structureFocus])
  const repositoryOriginParams = new URLSearchParams(window.location.hash.split('?')[1] || '')
  if (activeProject) repositoryOriginParams.set('projectId', activeProject.id)
  if (activeApplication) repositoryOriginParams.set('applicationId', activeApplication.id)
  if (activeRelease) repositoryOriginParams.set('releaseId', activeRelease.id)
  const repositoryOrigin = `#/repository${repositoryOriginParams.size ? `?${repositoryOriginParams}` : ''}`
  const renderRowAction = (row: RepositoryAssetRow) => {
    if (row.kind === 'script' && onOpenScript) {
      return <button type="button" className="btn btn-sm btn-secondary" aria-label={`Edit ${row.name}`} onClick={() => onOpenScript(row.id, row.projectId)}>Edit</button>
    }
    if (row.kind === 'scenario' && onCreateTestFromScenario) {
      return <button type="button" className="btn btn-sm btn-secondary" aria-label={`New test from ${row.name}`} onClick={() => onCreateTestFromScenario(row.id, row.projectId)}>New Test</button>
    }
    if (row.kind === 'test' && onOpenTest) {
      return (
        <div className="repository-row-actions">
          <button type="button" className="btn btn-sm btn-secondary" aria-label={`Open ${row.name}`} onClick={() => onOpenTest(row.id, row.projectId)}>Open</button>
          {onOpenCommandCenter && (
            <button type="button" className="btn btn-sm btn-secondary" aria-label={`Open ${row.name} in Command Center`} onClick={() => onOpenCommandCenter(row.id, row.projectId)}>Command Center</button>
          )}
        </div>
      )
    }
    if (row.kind === 'project' || row.kind === 'release') {
      return <button type="button" className="btn btn-sm btn-secondary" aria-label={`Open structure for ${row.name}`} onClick={() => { setStructureFocus(row.projectId); setActiveTab('structure') }}>Open structure</button>
    }
    if (row.kind === 'data_pool') return <a className="btn btn-sm btn-secondary" aria-label={`Open data pool ${row.name}`} href={repositoryDataPoolHref(row.id, row.projectId, repositoryOrigin)}>Open data pool</a>
    return null
  }

  return (
    <div className="page repository-page" ref={pageRef}>
      <nav className="related-links" aria-label="Related administration"><a className="related-link" href={settingsHref('projects')}>Manage workspaces in Settings</a></nav>
      <div className="page-header">
        <div>
          <p className="page-kicker">Repository</p>
          {/* The top bar holds the page h1. */}
          <h2>{applicationRepository?.name || 'Application Repository'}</h2>
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
      {Object.entries(reconciliationWarnings).map(([target, message]) => (
        <div className="repository-warning-list" role="alert" key={target}>
          <span>{message}</span>
          <button
            type="button"
            className="btn btn-sm btn-secondary"
            onClick={() => { void reconcileCommittedDrafts(target as ReconciliationTarget) }}
          >
            Retry {target === 'repository' ? 'repository' : 'project'} synchronization
          </button>
        </div>
      ))}

      <section className="repository-catalog-shell">
        <div className="settings-inline-actions" aria-label="Repository scope">
          <div className="repository-scope-toggle" role="group" aria-label="Catalog scope">
            <button type="button" className="btn btn-secondary" disabled={!activeProject} aria-pressed={!!activeProject && filters.projectId === activeProject.id} onClick={() => setFilters(prev => ({ ...prev, projectId: activeProject?.id || 'all', releaseId: 'all' }))}>Current workspace</button>
            <button type="button" className="btn btn-secondary" aria-pressed={filters.projectId === 'all'} onClick={() => setFilters(prev => ({ ...prev, projectId: 'all', releaseId: 'all' }))}>All accessible projects</button>
          </div>
          <span>Catalog: {activeDomain?.name || 'Selected domain'} · {filters.projectId === 'all' ? 'All accessible projects' : scopedProjects.find(project => project.id === filters.projectId)?.name || filters.projectId} · {activeApplication ? `Application ${activeApplication.name} for releases, scripts, scenarios and runs; projects, data pools and unassigned scripts and scenarios are project-wide.` : 'All applications'}</span>
        </div>
        <LibraryToolbar filtersActive={Boolean(filterActive)} label="Repository controls" search={<label className="library-search repository-search">
            <span>Search repository</span>
            <input
              value={filters.query}
              onChange={event => setFilters(prev => ({ ...prev, query: event.target.value }))}
              placeholder="Find by name, ID, project, release, protocol, status..."
            />
          </label>} actions={<button className="btn btn-secondary" type="button" onClick={refreshCatalogAssets} disabled={catalogLoading}>Refresh</button>} filters={<><div className="repository-filter-row" aria-label="Repository filters">
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
          <div className="repository-custom-time-row" role="group" aria-label="Custom activity range">
            <label>
              <span>Activity from</span>
              <input
                type="date"
                value={filters.customStartDate}
                max={filters.customEndDate || undefined}
                aria-invalid={customRangeError || undefined}
                aria-describedby={customRangeError ? customRangeErrorId : undefined}
                onChange={event => setFilters(prev => ({ ...prev, customStartDate: event.target.value }))}
              />
            </label>
            <label>
              <span>Activity to</span>
              <input
                type="date"
                value={filters.customEndDate}
                min={filters.customStartDate || undefined}
                aria-invalid={customRangeError || undefined}
                aria-describedby={customRangeError ? customRangeErrorId : undefined}
                onChange={event => setFilters(prev => ({ ...prev, customEndDate: event.target.value }))}
              />
            </label>
            {customRangeError && <p id={customRangeErrorId} className="settings-error-inline" role="alert">Activity from must be on or before Activity to. The date range is not applied until it is corrected.</p>}
          </div>
        )}

        </>} />
        <p className="setting-desc" role="status">{catalogLoading ? 'Loading assets…' : catalogLoadedAt ? `Updated ${formatDate(catalogLoadedAt)}` : 'Not loaded yet'}</p>
        <TabList className="repository-tab-row" role="tablist" aria-label="Repository asset types" onKeyDown={handleTabKey}>
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
                onClick={() => setActiveTab(tab.id)} tabIndex={(activeTab === tab.id) ? 0 : -1}
              >
                <span>{tab.label}</span>
                <strong>{tab.id === 'structure' || !filterActive ? count : `${filterRepositoryRows(catalogRows, tab.id, filters).length} / ${count}`}</strong>
              </button>
            )
          })}
        </TabList>

        {Object.values(catalogWarnings).length > 0 && (
          <div className="repository-warning-list" role="status">
            {Object.entries(catalogWarnings).map(([key, message]) => <span key={key}>{message}</span>)}
          </div>
        )}

        {inspected && activeTab !== 'structure' && <section tabIndex={-1} className="repository-inspection" aria-label={`Inspect ${inspected.name}`}>
          <header><h2>{inspected.name}</h2><button className="btn btn-secondary btn-sm" type="button" onClick={() => setInspectedKey(null)}>Close inspection</button></header>
          <dl><dt>Type</dt><dd>{repositoryKindLabel(inspected.kind)}</dd><dt>Project</dt><dd>{inspected.projectName}</dd><dt>Release</dt><dd>{inspected.releaseNames.join(', ') || inspected.releaseName || 'None'}</dd><dt>Summary</dt><dd>{inspected.summary}</dd><dt>Identifier</dt><dd><code>{inspected.id}</code></dd></dl>
          {renderRowAction(inspected)}
          {inspectedScenario && <><h3>Scenario scripts</h3><ul>{inspectedScenario.scripts.map(script => <li key={script.script_id}><button className="btn btn-sm btn-secondary" type="button" disabled={!onOpenScript} onClick={() => onOpenScript?.(script.script_id, inspected.projectId)}>{script.script_name || script.script_id}</button></li>)}</ul></>}
          {inspected.kind === 'script' && <><h3>Used by scenarios</h3>{relatedScenarios.length ? <ul>{relatedScenarios.map(scenario => <li key={scenario.id}><button className="btn btn-sm btn-secondary" type="button" onClick={() => { setFilters(DEFAULT_FILTERS); setActiveTab('scenario'); setInspectedKey(`scenario:${scenario.id}`) }}>{scenario.name}</button></li>)}</ul> : <p>No scenarios in the loaded catalog reference this script.</p>}</>}
          {(inspected.kind === 'scenario' || inspected.kind === 'script') && <><h3>Run history</h3>{relatedRuns.length ? <ul>{relatedRuns.map(test => <li key={test.id}><button className="btn btn-sm btn-secondary" type="button" disabled={!onOpenTest} onClick={() => onOpenTest?.(test.id, inspected.projectId)}>{test.name} · {test.ref_key || test.id} · {test.status}</button></li>)}</ul> : <p>No runs in the loaded catalog reference this asset.</p>}</>}
        </section>}
        {activeTab !== 'structure' && (
          <nav className="repository-pagination" aria-label="Repository pages">
            <span role="status">Showing {pageRows.length ? pageStart + 1 : 0}–{pageStart + pageRows.length} of {visibleRows.length} matching items</span>
            <label>Rows <select aria-label="Repository rows per page" value={pageSize} onChange={event => { setPageSize(event.target.value === 'all' ? 'all' : Number(event.target.value) as 10 | 25); setPage(0) }}>
              <option value="10">10</option><option value="25">25</option><option value="all">All</option>
            </select></label>
            <button type="button" className="btn btn-secondary btn-sm" disabled={currentPage === 0 || bulkBusy} onClick={() => setPage(currentPage - 1)}>Previous page</button>
            <button type="button" className="btn btn-secondary btn-sm" disabled={currentPage + 1 >= pageCount || bulkBusy} onClick={() => setPage(currentPage + 1)}>Next page</button>
          </nav>
        )}
        {activeTab !== 'structure' && selectedVisibleRows.length > 0 && (
          <div className="repository-bulk-bar" role="region" aria-label="Bulk repository actions">
            <div className="repository-bulk-summary">
              <strong>{plural(selectedVisibleRows.length, 'item')} selected ({selectedPageRows.length} on this page)</strong>
              <span>{archiveableSelectedRows.length > 0 ? plural(archiveableSelectedRows.length, 'archiveable item') : 'No archiveable items'}</span>
            </div>
            <div className="repository-bulk-actions">
              {selectedVisibleRows.length < selectableVisibleRows.length && <button type="button" className="btn btn-sm btn-secondary" disabled={bulkBusy} onClick={() => setSelectedRowKeys(new Set(selectableVisibleRows.map(repositoryRowKey)))}>Select all {selectableVisibleRows.length} matching items</button>}
              <button
                type="button"
                className="btn btn-sm btn-secondary"
                onClick={handleBulkArchive}
                disabled={bulkBusy || archiveableSelectedRows.length === 0}
              >
                Archive selected ({archiveableSelectedRows.length})
              </button>
              <button
                type="button"
                className="btn btn-sm btn-danger-outline"
                onClick={handleBulkDelete}
                disabled={bulkBusy || deletableSelectedRows.length === 0}
              >
                Delete selected ({deletableSelectedRows.length})
              </button>
              <button type="button" className="btn btn-sm btn-secondary" onClick={clearSelectedRows} disabled={bulkBusy}>
                Clear
              </button>
            </div>
          </div>
        )}

        {activeTab === 'structure' ? (
          <div className="repository-structure-panel"><p className="setting-desc">Full domain structure · All projects and applications. Catalog filters do not apply here.</p>
            {canManageRepository && activeDomain && (
              <form className="repository-inline-form" onSubmit={submitCategory}>
                <label>
                  <span>New Category</span>
                  <input
                    value={categoryCreationDraft?.domainId === activeDomain.id ? categoryCreationDraft.name : ''}
                    onChange={event => {
                      const name = event.target.value
                      const draft = name
                        ? { domainId: activeDomain.id, name, sortOrder: categories.length + 1 }
                        : null
                      categoryCreationDraftRef.current = draft
                      setCategoryCreationDraft(draft)
                    }}
                    placeholder="Payments, Claims, Search"
                  />
                </label>
                <button type="submit" disabled={!categoryCreationDraft?.name.trim()}>Add Category</button>
              </form>
            )}

            <div className="repository-category-grid">
              {structureSections.map(section => {
                const projectDraftKey = `${activeDomain?.id || ''}:${section.id}`
                const projectDraft = projectCreationDrafts[projectDraftKey]
                return (
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
                        value={projectDraft?.name || ''}
                        onChange={event => {
                          if (!activeDomain) return
                          const name = event.target.value
                          const next = { ...projectCreationDraftsRef.current }
                          if (name) {
                            next[projectDraftKey] = {
                              domainId: activeDomain.id,
                              categoryId: section.category?.id,
                              name,
                            }
                          } else {
                            delete next[projectDraftKey]
                          }
                          projectCreationDraftsRef.current = next
                          setProjectCreationDrafts(next)
                        }}
                        placeholder="New project name"
                      />
                      <button type="submit" disabled={!projectDraft?.name.trim()}>Create Project</button>
                    </form>
                  )}

                  <div className="repository-project-list">
                    {(projectsByCategory[section.id] || []).map(project => (
                      <article id={`repository-project-${project.id}`} tabIndex={-1} key={project.id} className="repository-project">
                        <div className="repository-project-title">
                          <div>
                            <h3>{project.name}</h3>
                            <p>{project.id}</p>
                          </div>
                          <div className="repository-project-actions">
                            <span>{(releasesByProject[project.id] || []).length} releases</span>
                            {onProjectSelect && (
                              activeProject?.id === project.id ? (
                                <span className="repository-project-active">Active project</span>
                              ) : (
                                <button
                                  type="button"
                                  className="btn btn-secondary btn-sm"
                                  onClick={() => onProjectSelect(project)}
                                >
                                  Set as active
                                </button>
                              )
                            )}
                          </div>
                        </div>
                        {canManageReleases && (
                          <form className="repository-compact-form" onSubmit={event => submitRelease(event, project)}>
                            <input
                              value={releaseCreationDrafts[project.id]?.name || ''}
                              onChange={event => {
                                const name = event.target.value
                                const next = { ...releaseCreationDraftsRef.current }
                                if (name) {
                                  next[project.id] = {
                                    projectId: project.id,
                                    applicationId: activeApplication?.project_id === project.id ? activeApplication.id : undefined,
                                    name,
                                  }
                                } else {
                                  delete next[project.id]
                                }
                                releaseCreationDraftsRef.current = next
                                setReleaseCreationDrafts(next)
                              }}
                              placeholder="New release name"
                            />
                            <button type="submit" disabled={!releaseCreationDrafts[project.id]?.name.trim()}>Add Release</button>
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
                )
              })}
              {structureSections.length === 0 && (
                <div className="repository-empty wide">No repository structure exists for this domain yet.</div>
              )}
            </div>
          </div>
        ) : (
          <div
            ref={catalogTableScrollRef}
            className={`data-table repository-asset-table${virtualizeCatalogRows ? ' repository-asset-table-virtualized' : ''}`}
            data-table-pagination="off"
          >
            <table>
              <thead>
                <tr>
                  <th className="repository-select-cell">
                    <input
                      type="checkbox"
                      className="repository-select-checkbox"
                      aria-label="Select all items on this page" title="Select all items on this page"
                      aria-checked={allVisibleRowsSelected ? 'true' : hasPartialVisibleSelection ? 'mixed' : 'false'}
                      checked={allVisibleRowsSelected}
                      disabled={pageRows.length === 0 || bulkBusy}
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
                {virtualizeCatalogRows && catalogPaddingTop > 0 && (
                  <tr aria-hidden="true">
					<td aria-hidden="true" colSpan={9} style={{ height: catalogPaddingTop, padding: 0, border: 0 }} />
                  </tr>
                )}
                {renderedCatalogRows.map(({ row, virtualIndex }) => (
                  <tr
                    key={repositoryRowKey(row)}
                    data-index={virtualIndex ?? undefined}
                    ref={node => {
                      if (node && virtualIndex !== null) catalogRowVirtualizer.measureElement(node)
                    }}
                  >
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
                        <button type="button" className="btn btn-sm btn-secondary" onClick={() => setInspectedKey(repositoryRowKey(row))}>{row.name}</button>
                        <span>{row.summary}</span>
                        <details><summary>Identifier</summary><code>{row.id}</code></details>
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
                    <td><div className="repository-row-actions"><button type="button" className="btn btn-sm btn-secondary" aria-label={`Inspect ${row.name}`} onClick={() => setInspectedKey(repositoryRowKey(row))}>Inspect</button>{renderRowAction(row)}</div></td>
                  </tr>
                ))}
                {virtualizeCatalogRows && catalogPaddingBottom > 0 && (
                  <tr aria-hidden="true">
					<td aria-hidden="true" colSpan={9} style={{ height: catalogPaddingBottom, padding: 0, border: 0 }} />
                  </tr>
                )}
                {visibleRows.length === 0 && (
                  <tr className="no-hover">
                    <td colSpan={9}>
                      <div className="repository-empty table-empty">
                        {catalogLoading ? 'Loading repository assets…' : filterActive ? 'No assets match the current filters.' : emptyCatalogMessage}
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
