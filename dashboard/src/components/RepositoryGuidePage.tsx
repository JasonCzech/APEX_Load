import ApiGuidePage from './ApiGuidePage'
import type { DashboardAuthEffectiveAccessResource } from '../types'
// Preserve bookmarks to the former repository-sourced API guide.
export default function RepositoryGuidePage({ effectiveAccessResources = [] }: { effectiveAccessResources?: DashboardAuthEffectiveAccessResource[] }) {
  return <ApiGuidePage activePage="general" defaultArticle="api-workflows" effectiveAccessResources={effectiveAccessResources} />
}
