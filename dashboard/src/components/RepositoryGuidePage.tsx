import { useEffect, useMemo, useState } from 'react'
import guideMarkdownUrl from '../../../docs/API_USER_GUIDE.md?url'
import { buildAllowedOperationSet } from '../apiGuideAccess'
import type { DashboardAuthEffectiveAccessResource } from '../types'
import { useApiGuideTopbarContribution } from './useApiGuideTopbarContribution'
import GuideMarkdownArticle from './GuideMarkdownArticle'
import { parseMarkdown } from './repositoryGuideMarkdown'

type RepositoryGuidePageProps = {
  effectiveAccessResources?: DashboardAuthEffectiveAccessResource[]
}

type GuideLoadState =
  | { status: 'loading' }
  | { status: 'ready'; source: string }
  | { status: 'error'; message: string }

export default function RepositoryGuidePage({ effectiveAccessResources = [] }: RepositoryGuidePageProps) {
  const [guideLoadState, setGuideLoadState] = useState<GuideLoadState>({ status: 'loading' })

  useEffect(() => {
    const controller = new AbortController()
    let active = true

    void fetch(guideMarkdownUrl, { signal: controller.signal })
      .then(async response => {
        if (!response.ok) throw new Error(`guide request failed with status ${response.status}`)
        return response.text()
      })
      .then(source => {
        if (!active) return
        if (!source.trim()) {
          setGuideLoadState({ status: 'error', message: 'guide asset response was empty' })
          return
        }
        setGuideLoadState({ status: 'ready', source })
      })
      .catch(error => {
        if (!active || (error instanceof DOMException && error.name === 'AbortError')) return
        setGuideLoadState({ status: 'error', message: error instanceof Error ? error.message : 'guide request failed' })
      })

    return () => {
      active = false
      controller.abort()
    }
  }, [])

  const headingTitle = useMemo(() => {
    if (guideLoadState.status !== 'ready') return 'API User Guide'
    const title = parseMarkdown(guideLoadState.source).find(block => block.kind === 'heading' && block.level === 1)
    return title && title.kind === 'heading' ? title.text : 'API User Guide'
  }, [guideLoadState])
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

      {guideLoadState.status === 'error' ? (
        <div className="error-message" role="alert">Unable to load the repository guide: {guideLoadState.message}</div>
      ) : guideLoadState.status === 'ready' ? (
        <GuideMarkdownArticle source={guideLoadState.source} />
      ) : (
        <p role="status">Loading repository guide…</p>
      )}
    </div>
  )
}
