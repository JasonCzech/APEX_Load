import { useMemo } from 'react'
import guideMarkdown from '../../../docs/API_USER_GUIDE.md?raw'
import { buildAllowedOperationSet } from '../apiGuideAccess'
import type { DashboardAuthEffectiveAccessResource } from '../types'
import { useApiGuideTopbarContribution } from './useApiGuideTopbarContribution'
import GuideMarkdownArticle from './GuideMarkdownArticle'
import { parseMarkdown } from './repositoryGuideMarkdown'

type RepositoryGuidePageProps = {
  effectiveAccessResources?: DashboardAuthEffectiveAccessResource[]
}

export default function RepositoryGuidePage({ effectiveAccessResources = [] }: RepositoryGuidePageProps) {
  const headingTitle = useMemo(() => {
    const title = parseMarkdown(guideMarkdown).find(block => block.kind === 'heading' && block.level === 1)
    return title && title.kind === 'heading' ? title.text : 'API User Guide'
  }, [])
  const allowedOperations = useMemo(() => buildAllowedOperationSet(effectiveAccessResources), [effectiveAccessResources])
  // Mirror ApiGuidePage's "any administrator" gate so the admin-only guide chip stays
  // visible while navigating across the shared API-guide toggle from the Repo Guide tab.
  const isAdmin = ['manage_users', 'manage_user_bindings', 'view_user_activity', 'manage_service_accounts', 'manage_system']
    .some(operation => allowedOperations.has(operation))
  useApiGuideTopbarContribution('repo-guide', 'Repository Guide', undefined, isAdmin)

  return (
    <div className="page repository-guide-page">
      <div className="page-header">
        <div>
          <h2>Repository Guide</h2>
          <p className="page-subtitle">{headingTitle} — rendered from the in-repository documentation.</p>
        </div>
      </div>

      <GuideMarkdownArticle source={guideMarkdown} />
    </div>
  )
}
