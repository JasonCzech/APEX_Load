import { settingsHref } from './settingsNavigation'

function repositoryCatalogHref(hash = window.location.hash): string {
  const current = new URLSearchParams(hash.split('?')[1] || '')
  const params = new URLSearchParams()
  for (const key of ['projectId', 'applicationId', 'releaseId']) {
    const value = current.get(key)
    if (value) params.set(key, value)
  }
  const query = params.toString()
  return query ? `#/repository?${query}` : '#/repository'
}

/**
 * Top-bar tabs for the Repository page: the asset catalog and the shared test
 * data it owns. Like every page with a few views, they sit beside the title.
 */
export default function RepositorySectionTabs({ current }: { current: 'catalog' | 'test-data' }) {
  return (
    <nav className="topbar-page-section-toggle repository-section-tabs" aria-label="Repository sections">
      <a href={repositoryCatalogHref()} aria-current={current === 'catalog' ? 'page' : undefined}>Catalog</a>
      <a href={settingsHref('test-data')} aria-current={current === 'test-data' ? 'page' : undefined}>Shared test data</a>
    </nav>
  )
}
