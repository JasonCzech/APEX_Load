import {
  Application,
  Domain,
  Project,
  Release,
  RepositoryCategory,
  SharedDataPool,
  StoredScenario,
  StoredScript,
  TestSummary,
} from '../types'

export type RepositoryAssetKind = 'project' | 'release' | 'script' | 'scenario' | 'test' | 'data_pool'
export type RepositoryCatalogTab = 'all' | RepositoryAssetKind | 'structure'
export type RepositorySortDirection = 'asc' | 'desc'
export type RepositorySortKey = 'name' | 'kind' | 'project' | 'release' | 'status' | 'protocol' | 'created_at' | 'updated_at' | 'last_activity'

export interface RepositoryCatalogFilters {
  query: string
  projectId: string
  releaseId: string
  status: string
  protocol: string
  timeframe: string
  customStartDate: string
  customEndDate: string
}

export interface RepositoryCatalogSort {
  key: RepositorySortKey
  direction: RepositorySortDirection
}

export interface RepositoryAssetRow {
  id: string
  kind: RepositoryAssetKind
  kindLabel: string
  name: string
  projectId: string
  projectName?: string
  categoryId?: string
  categoryName?: string
  releaseId?: string
  releaseName?: string
  releaseIds: string[]
  releaseNames: string[]
  protocol?: string
  status?: string
  summary: string
  detail?: string
  createdAt?: string
  updatedAt?: string
  lastActivityAt?: string
  archived?: boolean
  searchText: string
}

export interface RepositoryCatalogBuildInput {
  activeDomain: Domain | null
  activeApplication?: Application | null
  categories: RepositoryCategory[]
  projects: Project[]
  releasesByProject: Record<string, Release[]>
  scriptsByProject: Record<string, StoredScript[]>
  scenariosByProject: Record<string, StoredScenario[]>
  testsByProject: Record<string, TestSummary[]>
  dataPoolsByProject: Record<string, SharedDataPool[]>
}

const KIND_LABELS: Record<RepositoryAssetKind, string> = {
  project: 'Project',
  release: 'Release',
  script: 'Script',
  scenario: 'Scenario',
  test: 'Test Run',
  data_pool: 'Data Pool',
}

const TIMEFRAME_DAYS: Record<string, number> = {
  '7d': 7,
  '30d': 30,
  '90d': 90,
}

function plural(count: number, singular: string, pluralLabel = `${singular}s`) {
  return `${count} ${count === 1 ? singular : pluralLabel}`
}

function compact(values: Array<string | undefined | null>) {
  return values.map(value => value?.trim()).filter(Boolean) as string[]
}

function unique(values: Array<string | undefined | null>) {
  return Array.from(new Set(compact(values)))
}

function domainScopedProjects(activeDomain: Domain | null, projects: Project[]) {
  const domainId = activeDomain?.id || 'default'
  return projects.filter(project => (project.domain_id || 'default') === domainId)
}

function buildSearchText(parts: Array<string | number | undefined | null | string[]>) {
  return parts
    .flatMap(part => Array.isArray(part) ? part : [part])
    .filter(part => part !== undefined && part !== null && String(part).trim())
    .join(' ')
    .toLowerCase()
}

function newestTimestamp(values: Array<string | undefined>) {
  let newest = ''
  let newestMs = Number.NEGATIVE_INFINITY
  for (const value of values) {
    if (!value) continue
    const ms = Date.parse(value)
    if (Number.isFinite(ms) && ms > newestMs) {
      newest = value
      newestMs = ms
    }
  }
  return newest || undefined
}

function protocolListFromScenario(scenario: StoredScenario) {
  return unique((scenario.scripts || []).map(script => script.protocol || 'http')).join(', ')
}

function protocolListFromTest(test: TestSummary) {
  return unique((test.groups || []).map(group => group.protocol || 'http')).join(', ')
}

function releaseNamesFromRefs(refs?: Array<{ id: string; name: string }>) {
  return unique((refs || []).map(ref => ref.name))
}

function releaseIdsFromRefs(refs?: Array<{ id: string; name: string }>) {
  return unique((refs || []).map(ref => ref.id))
}

function releaseMatchesApplication(release: Release | undefined, activeApplicationId: string) {
	return !activeApplicationId
		|| release?.application_id === activeApplicationId
		|| Boolean(release?.member_ids?.[activeApplicationId])
}

function releaseRefMatchesApplication(ref: { id: string; application_id?: string } | undefined, activeApplicationId: string, releaseMap: Map<string, Release>) {
  if (!activeApplicationId || !ref) return !activeApplicationId
  if (ref.application_id) return ref.application_id === activeApplicationId
  return releaseMatchesApplication(releaseMap.get(ref.id), activeApplicationId)
}

function releaseRefsForApplication<T extends { id: string; application_id?: string }>(refs: T[] | undefined, activeApplicationId: string, releaseMap: Map<string, Release>) {
  const values = refs || []
  if (!activeApplicationId) return values
  return values.filter(ref => releaseRefMatchesApplication(ref, activeApplicationId, releaseMap))
}

function testMatchesApplication(test: TestSummary, activeApplicationId: string, releaseMap: Map<string, Release>) {
  if (!activeApplicationId) return true
  if (test.application_id) return test.application_id === activeApplicationId
  return releaseMatchesApplication(test.release_id ? releaseMap.get(test.release_id) : undefined, activeApplicationId)
}

function scenarioModeLabel(value?: StoredScenario['scenario_mode']) {
  return value === 'goal_oriented' ? 'Goal' : 'VUser'
}

export function repositoryKindLabel(kind: RepositoryAssetKind) {
  return KIND_LABELS[kind]
}

export function buildRepositoryCatalogRows(input: RepositoryCatalogBuildInput): RepositoryAssetRow[] {
  const scopedProjects = domainScopedProjects(input.activeDomain, input.projects)
  const projectMap = new Map(scopedProjects.map(project => [project.id, project]))
  const categoryMap = new Map(input.categories.map(category => [category.id, category]))
  const releaseMap = new Map<string, Release>()
  const activeApplicationId = input.activeApplication?.id || ''

	Object.values(input.releasesByProject).forEach(releases => {
		releases.forEach(release => {
			releaseMap.set(release.id, release)
			if (release.group_id) releaseMap.set(release.group_id, release)
			Object.entries(release.member_ids || {}).forEach(([applicationId, memberId]) => {
				releaseMap.set(memberId, { ...release, id: memberId, application_id: applicationId })
			})
		})
	})

  const rows: RepositoryAssetRow[] = []

  for (const project of scopedProjects) {
    const category = project.category_id ? categoryMap.get(project.category_id) : undefined
    const releaseCount = input.releasesByProject[project.id]?.length || 0
    rows.push({
      id: project.id,
      kind: 'project',
      kindLabel: KIND_LABELS.project,
      name: project.name,
      projectId: project.id,
      projectName: project.name,
      categoryId: project.category_id,
      categoryName: category?.name || 'Uncategorized',
      releaseIds: [],
      releaseNames: [],
      status: 'Active',
      summary: plural(releaseCount, 'release'),
      detail: category?.name || 'Uncategorized',
      createdAt: project.created_at,
      updatedAt: project.updated_at,
      lastActivityAt: project.updated_at || project.created_at,
      searchText: buildSearchText([project.id, project.name, category?.name, 'project', releaseCount]),
    })
  }

  for (const [projectId, releases] of Object.entries(input.releasesByProject)) {
    const project = projectMap.get(projectId)
    if (!project) continue
    const category = project.category_id ? categoryMap.get(project.category_id) : undefined
    for (const release of releases) {
      if (!releaseMatchesApplication(release, activeApplicationId)) continue
      const archived = Boolean(release.archived_at)
		const memberReleaseIds = Object.values(release.member_ids || {})
		rows.push({
			id: release.id,
        kind: 'release',
        kindLabel: KIND_LABELS.release,
        name: release.name,
        projectId,
        projectName: project.name,
        categoryId: project.category_id,
        categoryName: category?.name || 'Uncategorized',
        releaseId: release.id,
        releaseName: release.name,
			releaseIds: unique([release.id, release.group_id, ...memberReleaseIds]),
        releaseNames: [release.name],
        status: archived ? 'Archived' : 'Active',
        summary: release.application_id ? `Application ${release.application_id}` : 'Project release',
        detail: project.name,
        createdAt: release.created_at,
        updatedAt: release.updated_at,
        lastActivityAt: newestTimestamp([release.archived_at, release.updated_at, release.created_at]),
        archived,
        searchText: buildSearchText([release.id, release.name, release.application_id, project.name, category?.name, archived ? 'archived' : 'active', 'release']),
      })
    }
  }

  for (const [projectId, scripts] of Object.entries(input.scriptsByProject)) {
    const project = projectMap.get(projectId)
    if (!project) continue
    const category = project.category_id ? categoryMap.get(project.category_id) : undefined
    for (const script of scripts) {
      const scriptReleaseRefs = releaseRefsForApplication([
        ...(script.library_assignment?.releases || []),
        ...(script.associated_releases || []),
        ...(script.scenario_run_releases || []),
      ], activeApplicationId, releaseMap)
      if (activeApplicationId && script.library_assignment?.application?.id !== activeApplicationId && scriptReleaseRefs.length === 0) continue
      const releaseNames = unique([
        ...releaseNamesFromRefs(scriptReleaseRefs),
      ])
      const releaseIds = unique([
        ...releaseIdsFromRefs(scriptReleaseRefs),
      ])
      const status = script.authoring_health?.status || 'Not assessed'
      rows.push({
        id: script.id,
        kind: 'script',
        kindLabel: KIND_LABELS.script,
        name: script.name,
        projectId,
        projectName: project.name,
        categoryId: project.category_id,
        categoryName: category?.name || 'Uncategorized',
        releaseName: releaseNames.join(', '),
        releaseIds,
        releaseNames,
        protocol: script.protocol || 'http',
        status,
        summary: `${script.version || '1.0.0'} · ${plural(script.scenario_count || 0, 'scenario')}`,
        detail: `Assignment: ${script.library_assignment?.application?.name || 'Unassigned'} · Assigned releases: ${(script.library_assignment?.releases || []).map(release => release.name).join(', ') || 'None'} · Run releases: ${(script.associated_releases || []).map(release => release.name).join(', ') || 'None'}`,
        createdAt: script.created_at,
        lastActivityAt: newestTimestamp([script.last_run_at, script.created_at]),
        searchText: buildSearchText([script.id, script.name, script.protocol, script.version, status, project.name, category?.name, releaseNames, 'script']),
      })
    }
  }

  for (const [projectId, scenarios] of Object.entries(input.scenariosByProject)) {
    const project = projectMap.get(projectId)
    if (!project) continue
    const category = project.category_id ? categoryMap.get(project.category_id) : undefined
    for (const scenario of scenarios) {
      const scenarioReleaseRefs = releaseRefsForApplication([...(scenario.library_assignment?.releases || []), ...(scenario.associated_releases || [])], activeApplicationId, releaseMap)
      if (activeApplicationId && scenario.library_assignment?.application?.id !== activeApplicationId && scenarioReleaseRefs.length === 0) continue
      const releaseNames = releaseNamesFromRefs(scenarioReleaseRefs)
      const releaseIds = releaseIdsFromRefs(scenarioReleaseRefs)
      const protocols = protocolListFromScenario(scenario)
      const status = 'Saved'
      rows.push({
        id: scenario.id,
        kind: 'scenario',
        kindLabel: KIND_LABELS.scenario,
        name: scenario.name,
        projectId,
        projectName: project.name,
        categoryId: project.category_id,
        categoryName: category?.name || 'Uncategorized',
        releaseName: releaseNames.join(', '),
        releaseIds,
        releaseNames,
        protocol: protocols,
        status,
        summary: `${scenarioModeLabel(scenario.scenario_mode)} · ${scenario.duration || 'No duration'} · ${plural(scenario.script_count || scenario.scripts.length, 'script')} · ${plural(scenario.result_count || 0, 'run')}`,
        detail: `Assignment: ${scenario.library_assignment?.application?.name || 'Unassigned'} · Assigned releases: ${(scenario.library_assignment?.releases || []).map(release => release.name).join(', ') || 'None'} · Run releases: ${(scenario.associated_releases || []).map(release => release.name).join(', ') || 'None'}`,
        createdAt: scenario.created_at,
        updatedAt: scenario.updated_at,
        lastActivityAt: newestTimestamp([scenario.last_run_at, scenario.updated_at, scenario.created_at]),
        searchText: buildSearchText([
          scenario.id,
          scenario.name,
          status,
          scenario.duration,
          protocols,
          project.name,
          category?.name,
          releaseNames,
          (scenario.scripts || []).map(script => `${script.name || ''} ${script.script_name || ''} ${script.script_id || ''}`),
          (scenario.data_pools || []).map(pool => `${pool.alias || ''} ${pool.pool_name || ''} ${pool.pool_id || ''}`),
          'scenario',
        ]),
      })
    }
  }

  for (const [projectId, tests] of Object.entries(input.testsByProject)) {
    const project = projectMap.get(projectId)
    if (!project) continue
    const category = project.category_id ? categoryMap.get(project.category_id) : undefined
    for (const test of tests) {
      if (!testMatchesApplication(test, activeApplicationId, releaseMap)) continue
      const release = test.release_id ? releaseMap.get(test.release_id) : undefined
      const protocols = protocolListFromTest(test)
      rows.push({
        id: test.id,
        kind: 'test',
        kindLabel: KIND_LABELS.test,
        name: test.name,
        projectId,
        projectName: project.name,
        categoryId: project.category_id,
        categoryName: category?.name || 'Uncategorized',
        releaseId: test.release_id,
        releaseName: release?.name,
        releaseIds: compact([test.release_id]),
        releaseNames: compact([release?.name]),
        protocol: protocols,
        status: test.status,
        summary: `${test.total_vusers || 0} VUsers · ${test.duration || 'No duration'}`,
        detail: test.scenario_id ? `Scenario ${test.scenario_id}` : 'Ad hoc run',
        createdAt: test.created_at,
        updatedAt: test.ended_at || test.started_at,
        lastActivityAt: newestTimestamp([test.ended_at, test.started_at, test.created_at]),
        searchText: buildSearchText([
          test.id,
          test.name,
          test.status,
          protocols,
          test.duration,
          test.total_vusers,
          project.name,
          category?.name,
          release?.name,
          test.scenario_id,
          (test.groups || []).map(group => `${group.name || ''} ${group.script_name || ''} ${group.script_id || ''}`),
          'test run',
        ]),
      })
    }
  }

  for (const [projectId, dataPools] of Object.entries(input.dataPoolsByProject)) {
    const project = projectMap.get(projectId)
    if (!project) continue
    const category = project.category_id ? categoryMap.get(project.category_id) : undefined
    for (const pool of dataPools) {
      const fieldCount = Object.keys(pool.schema || {}).length
      const archived = Boolean(pool.archived_at)
      rows.push({
        id: pool.id,
        kind: 'data_pool',
        kindLabel: KIND_LABELS.data_pool,
        name: pool.name,
        projectId,
        projectName: project.name,
        categoryId: project.category_id,
        categoryName: category?.name || 'Uncategorized',
        releaseIds: [],
        releaseNames: [],
        status: archived ? 'Archived' : 'Active',
        summary: fieldCount ? plural(fieldCount, 'field') : 'No schema fields',
        detail: pool.description || 'Shared scenario data',
        createdAt: pool.created_at,
        updatedAt: pool.updated_at,
        lastActivityAt: newestTimestamp([pool.archived_at, pool.updated_at, pool.created_at]),
        archived,
        searchText: buildSearchText([pool.id, pool.name, pool.description, Object.keys(pool.schema || {}), project.name, category?.name, archived ? 'archived' : 'active', 'data pool']),
      })
    }
  }

  return rows
}

export function summarizeRepositoryRows(rows: RepositoryAssetRow[]) {
  return rows.reduce<Record<RepositoryAssetKind, number>>((summary, row) => {
    summary[row.kind] += 1
    return summary
  }, {
    project: 0,
    release: 0,
    script: 0,
    scenario: 0,
    test: 0,
    data_pool: 0,
  })
}

export function filterRepositoryRows(rows: RepositoryAssetRow[], tab: RepositoryCatalogTab, filters: RepositoryCatalogFilters) {
  const query = filters.query.trim().toLowerCase()
  const now = Date.now()
  const customStart = filters.customStartDate ? Date.parse(`${filters.customStartDate}T00:00:00`) : Number.NaN
  const customEnd = filters.customEndDate ? Date.parse(`${filters.customEndDate}T23:59:59.999`) : Number.NaN
  return rows.filter(row => {
    if (tab !== 'all' && tab !== 'structure' && row.kind !== tab) return false
    if (filters.projectId !== 'all' && row.projectId !== filters.projectId) return false
    if (filters.releaseId !== 'all' && !row.releaseIds.includes(filters.releaseId)) return false
    if (filters.status !== 'all' && (row.status || '').toLowerCase() !== filters.status.toLowerCase()) return false
    if (filters.protocol !== 'all') {
      const protocols = (row.protocol || '').split(',').map(value => value.trim().toLowerCase())
      if (!protocols.includes(filters.protocol.toLowerCase())) return false
    }
    if (filters.timeframe === 'custom') {
      const timestamp = row.lastActivityAt || row.updatedAt || row.createdAt
      const ms = timestamp ? Date.parse(timestamp) : Number.NaN
      if (!Number.isFinite(ms)) return false
      if (Number.isFinite(customStart) && ms < customStart) return false
      if (Number.isFinite(customEnd) && ms > customEnd) return false
    } else if (filters.timeframe !== 'all') {
      const days = TIMEFRAME_DAYS[filters.timeframe]
      const timestamp = row.lastActivityAt || row.updatedAt || row.createdAt
      const ms = timestamp ? Date.parse(timestamp) : Number.NaN
      if (!days || !Number.isFinite(ms) || ms < now - days * 24 * 60 * 60 * 1000) return false
    }
    if (query && !row.searchText.includes(query)) return false
    return true
  })
}

function sortValue(row: RepositoryAssetRow, key: RepositorySortKey) {
  switch (key) {
    case 'kind':
      return row.kindLabel
    case 'project':
      return row.projectName || ''
    case 'release':
      return row.releaseName || row.releaseNames.join(', ')
    case 'status':
      return row.status || ''
    case 'protocol':
      return row.protocol || ''
    case 'created_at':
      return row.createdAt || ''
    case 'updated_at':
      return row.updatedAt || ''
    case 'last_activity':
      return row.lastActivityAt || row.updatedAt || row.createdAt || ''
    case 'name':
    default:
      return row.name
  }
}

export function sortRepositoryRows(rows: RepositoryAssetRow[], sort: RepositoryCatalogSort) {
  const direction = sort.direction === 'asc' ? 1 : -1
  return [...rows].sort((a, b) => {
    const left = sortValue(a, sort.key)
    const right = sortValue(b, sort.key)
    if (sort.key === 'created_at' || sort.key === 'updated_at' || sort.key === 'last_activity') {
      return ((Date.parse(left) || 0) - (Date.parse(right) || 0)) * direction
    }
    return String(left).localeCompare(String(right), undefined, { numeric: true, sensitivity: 'base' }) * direction
  })
}
