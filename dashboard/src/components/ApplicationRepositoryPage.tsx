import { FormEvent, useCallback, useEffect, useMemo, useState } from 'react'

import {
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
import { useScriptApi } from '../hooks/api/useScriptApi'
import { useTestApi } from '../hooks/api/useTestApi'
import StatusBadge from './StatusBadge'
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

function sortIndicator(sort: RepositoryCatalogSort, key: RepositorySortKey) {
  if (sort.key !== key) return '↕'
  return sort.direction === 'asc' ? '↑' : '↓'
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

export default function ApplicationRepositoryPage({
  domains,
  activeDomain,
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
  onCreateTestFromScenario,
}: Props) {
  const repositoryApi = useRepositoryApi()
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
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')

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
    setCatalogLoading(true)
    try {
      const snapshot = await loadCatalogAssets()
      applyCatalogSnapshot(snapshot)
    } finally {
      setCatalogLoading(false)
    }
  }, [applyCatalogSnapshot, loadCatalogAssets])

  useEffect(() => {
    let cancelled = false
    setCatalogLoading(true)
    loadCatalogAssets()
      .then(snapshot => {
        if (!cancelled) applyCatalogSnapshot(snapshot)
      })
      .catch(err => {
        if (!cancelled) setCatalogWarnings({ catalog: getErrorMessage(err, 'Unable to load repository catalog.') })
      })
      .finally(() => {
        if (!cancelled) setCatalogLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [applyCatalogSnapshot, loadCatalogAssets])

  const catalogRows = useMemo(() => buildRepositoryCatalogRows({
    activeDomain,
    categories,
    projects,
    releasesByProject,
    scriptsByProject,
    scenariosByProject,
    testsByProject,
    dataPoolsByProject,
  }), [activeDomain, categories, dataPoolsByProject, projects, releasesByProject, scenariosByProject, scriptsByProject, testsByProject])

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
      const release = await repositoryApi.createProjectRelease(project.id, { name })
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

  const renderRowAction = (row: RepositoryAssetRow) => {
    if (row.kind === 'script' && onOpenScript) {
      return <button type="button" className="repository-row-action" onClick={() => onOpenScript(row.id)}>Edit</button>
    }
    if (row.kind === 'scenario' && onCreateTestFromScenario) {
      return <button type="button" className="repository-row-action" onClick={() => onCreateTestFromScenario(row.id)}>New Test</button>
    }
    if (row.kind === 'test' && onOpenTest) {
      return <button type="button" className="repository-row-action" onClick={() => onOpenTest(row.id)}>Open</button>
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
          <p className="page-subtitle">Search, sort, and inspect the domain inventory across scripts, scenarios, tests, releases, projects, and shared data.</p>
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

      <section className="repository-summary-band catalog">
        <div>
          <span className="metric-label">Projects</span>
          <strong>{summary.project}</strong>
        </div>
        <div>
          <span className="metric-label">Releases</span>
          <strong>{summary.release}</strong>
        </div>
        <div>
          <span className="metric-label">Scripts</span>
          <strong>{summary.script}</strong>
        </div>
        <div>
          <span className="metric-label">Scenarios</span>
          <strong>{summary.scenario}</strong>
        </div>
        <div>
          <span className="metric-label">Tests</span>
          <strong>{summary.test}</strong>
        </div>
        <div>
          <span className="metric-label">Data Pools</span>
          <strong>{summary.data_pool}</strong>
        </div>
      </section>

      {(status || error) && (
        <div className={error ? 'form-error' : 'form-success'}>{error || status}</div>
      )}

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
                  <th className="sortable" onClick={() => updateSort('name')}>Asset <span className={`sort-icon ${sort.key === 'name' ? 'active' : ''}`}>{sortIndicator(sort, 'name')}</span></th>
                  <th className="sortable" onClick={() => updateSort('kind')}>Type <span className={`sort-icon ${sort.key === 'kind' ? 'active' : ''}`}>{sortIndicator(sort, 'kind')}</span></th>
                  <th className="sortable" onClick={() => updateSort('project')}>Project <span className={`sort-icon ${sort.key === 'project' ? 'active' : ''}`}>{sortIndicator(sort, 'project')}</span></th>
                  <th className="sortable" onClick={() => updateSort('release')}>Release <span className={`sort-icon ${sort.key === 'release' ? 'active' : ''}`}>{sortIndicator(sort, 'release')}</span></th>
                  <th className="sortable" onClick={() => updateSort('status')}>Status <span className={`sort-icon ${sort.key === 'status' ? 'active' : ''}`}>{sortIndicator(sort, 'status')}</span></th>
                  <th className="sortable" onClick={() => updateSort('protocol')}>Protocol <span className={`sort-icon ${sort.key === 'protocol' ? 'active' : ''}`}>{sortIndicator(sort, 'protocol')}</span></th>
                  <th className="sortable" onClick={() => updateSort('last_activity')}>Activity <span className={`sort-icon ${sort.key === 'last_activity' ? 'active' : ''}`}>{sortIndicator(sort, 'last_activity')}</span></th>
                  <th>Action</th>
                </tr>
              </thead>
              <tbody>
                {visibleRows.map(row => (
                  <tr key={`${row.kind}:${row.id}`}>
                    <td>
                      <div className="repository-asset-name">
                        <strong>{row.name}</strong>
                        <span>{row.summary}</span>
                        <code>{row.id}</code>
                      </div>
                    </td>
                    <td><span className="repository-kind-pill">{repositoryKindLabel(row.kind)}</span></td>
                    <td>
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
                    <td colSpan={8}>
                      <div className="repository-empty table-empty">
                        {catalogLoading ? 'Loading repository assets...' : filterActive ? 'No assets match the current filters.' : 'No assets found for this domain.'}
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
