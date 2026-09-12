import type { RepositoryCatalogFilters, RepositoryCatalogSort, RepositoryCatalogTab } from './repositoryCatalogUtils'

export const DEFAULT_REPOSITORY_FILTERS: RepositoryCatalogFilters = {
  query: '', projectId: 'all', releaseId: 'all', status: 'all', protocol: 'all',
  timeframe: 'all', customStartDate: '', customEndDate: '',
}
export const DEFAULT_REPOSITORY_SORT: RepositoryCatalogSort = { key: 'last_activity', direction: 'desc' }
export interface RepositoryViewState {
  tab: RepositoryCatalogTab
  filters: RepositoryCatalogFilters
  sort: RepositoryCatalogSort
  page: number
  pageSize: 10 | 25 | 'all'
  scrollTop: number
}
const tabs = ['all', 'project', 'release', 'script', 'scenario', 'test', 'data_pool', 'structure']
const sortKeys = ['name', 'kind', 'project', 'release', 'status', 'protocol', 'created_at', 'updated_at', 'last_activity']

export function readRepositoryViewState(key: string): RepositoryViewState {
  const fallback: RepositoryViewState = { tab: 'all', filters: { ...DEFAULT_REPOSITORY_FILTERS }, sort: { ...DEFAULT_REPOSITORY_SORT }, page: 0, pageSize: 10, scrollTop: 0 }
  try {
    const value = JSON.parse(window.sessionStorage.getItem(key) || 'null')
    if (!value || typeof value !== 'object') return fallback
    const filters = { ...fallback.filters }
    for (const field of Object.keys(filters) as Array<keyof RepositoryCatalogFilters>) {
      if (typeof value.filters?.[field] === 'string') filters[field] = value.filters[field]
    }
    return {
      tab: tabs.includes(value.tab) ? value.tab : fallback.tab,
      filters,
      sort: sortKeys.includes(value.sort?.key) && ['asc', 'desc'].includes(value.sort?.direction) ? value.sort : fallback.sort,
      page: Number.isSafeInteger(value.page) && value.page >= 0 ? value.page : 0,
      pageSize: [10, 25, 'all'].includes(value.pageSize) ? value.pageSize : 10,
      scrollTop: Number.isFinite(value.scrollTop) && value.scrollTop >= 0 ? value.scrollTop : 0,
    }
  } catch { return fallback }
}

export function writeRepositoryViewState(key: string, value: RepositoryViewState) {
  try { window.sessionStorage.setItem(key, JSON.stringify(value)) } catch { /* Browsing still works without storage. */ }
}
