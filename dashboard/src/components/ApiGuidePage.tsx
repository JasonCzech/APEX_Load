import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { API_GUIDE_DEFAULT_PAGE, helpAreaForPage, isApiGuideContentPageID, type ApiGuideContentPageID } from '../apiGuideNavigation'
import { buildAllowedOperationSet } from '../apiGuideAccess'
import { articleBlocks, articleForSection, helpArticles, helpArticleHash, loadHelpSource, resolveHelpLink, type HelpArticle } from '../helpContent'
import { useHasTopbarContributionProvider } from '../hooks/TopbarContributionProvider'
import type { DashboardAuthEffectiveAccessResource } from '../types'
import { ApiGuideTopbarToggle } from './ApiGuideTopbarToggle'
import GuideMarkdownArticle from './GuideMarkdownArticle'
import HelpWorkspace from './HelpWorkspace'
import { useHelpHash } from '../hooks/useHelpHash'
import { useApiGuideTopbarContribution } from './useApiGuideTopbarContribution'
import dslSchemaURL from '../../../docs/dsl.schema.json?url'
import { copyTextToClipboard } from '../utils/clipboardUtils'

const CloudTestParityDashboard = lazy(() => import('./CloudTestParityDashboard'))

type Props = {
  activePage?: ApiGuideContentPageID
  defaultArticle?: string
  onNavigate?: (page: 'access') => void
  effectiveAccessResources?: DashboardAuthEffectiveAccessResource[]
  effectiveAccessLoaded?: boolean
  effectiveAccessError?: boolean
}
export default function ApiGuidePage({ activePage = API_GUIDE_DEFAULT_PAGE, defaultArticle, onNavigate, effectiveAccessResources = [], effectiveAccessLoaded = true, effectiveAccessError = false }: Props) {
  const hash = useHelpHash()
  const params = new URLSearchParams(hash.split('?')[1] || '')
  const routedPage = hash.startsWith('#/docs') && isApiGuideContentPageID(params.get('guide')) ? params.get('guide') as ApiGuideContentPageID : activePage
  const operations = useMemo(() => buildAllowedOperationSet(effectiveAccessResources), [effectiveAccessResources])
  const isAdmin = effectiveAccessLoaded && !effectiveAccessError && ['manage_users', 'manage_user_bindings', 'view_user_activity', 'manage_service_accounts', 'manage_system'].some(operation => operations.has(operation))
  const requested = params.get('article') || (routedPage === 'mcp' ? 'mcp' : routedPage === 'authentication' ? 'authentication' : defaultArticle)
  const match = helpArticles.find(article => article.id === requested)
  const denied = (match?.area || helpAreaForPage(routedPage)) === 'admin-guide' && !isAdmin
  const area = denied ? 'getting-started' : match?.area || helpAreaForPage(routedPage)
  const article = (!denied && match) || helpArticles.find(article => article.area === area)!
  const section = params.get('section') || undefined
  const hasTopbar = useHasTopbarContributionProvider()
  useApiGuideTopbarContribution(area, 'Help & documentation', undefined, isAdmin)
  return <div className="page api-guide-page help-page">
    {!hasTopbar && <ApiGuideTopbarToggle active={area} includeAdminGuide={isAdmin} />}
    <HelpWorkspace area={area} activeArticle={article.id} isAdmin={isAdmin}>
      {denied && <p role="status">Administration requires administrative access. Start with the public help articles or contact your administrator.</p>}
      {requested && !match && <p role="status">That article is not in this build. Choose an article below.</p>}
      <HelpArticleContent key={article.id} article={article} section={section} isAdmin={isAdmin} />
      {article.id === 'administration' && <details><summary>CloudTest compatibility and delivery reference</summary><Suspense fallback={<p role="status">Loading compatibility reference…</p>}><CloudTestParityDashboard /></Suspense></details>}
      {article.id === 'authentication' && isAdmin && onNavigate && <button type="button" className="btn btn-secondary" onClick={() => onNavigate('access')}>Open Access</button>}
    </HelpWorkspace>
  </div>
}

function HelpArticleContent({ article, section, isAdmin }: { article: HelpArticle; section?: string; isAdmin: boolean }) {
  const [state, setState] = useState<{ source?: string; error?: string }>({})
  const [attempt, setAttempt] = useState(0)
  const [copied, setCopied] = useState('')
  const [controllerURL, setControllerURL] = useState('http://localhost:8080/api/v1')
  useEffect(() => {
    let cancelled = false
    loadHelpSource(article).then(source => { if (!cancelled) setState({ source }) }, () => { if (!cancelled) setState({ error: 'This article could not be loaded. Retry or choose another article.' }) })
    return () => { cancelled = true }
  }, [article, attempt])
  const resolvedArticle = state.source && section ? articleForSection(article.path, section, state.source) : undefined
  const displayArticle = resolvedArticle && (isAdmin || resolvedArticle.area !== 'admin-guide') ? resolvedArticle : article
  useEffect(() => {
    if (displayArticle.id === article.id) return
    const url = new URL(window.location.href)
    const params = new URLSearchParams(url.hash.split('?')[1] || '')
    params.set('guide', displayArticle.area)
    params.set('article', displayArticle.id)
    url.hash = `/docs?${params}`
    window.history.replaceState(window.history.state, '', url)
    window.dispatchEvent(new HashChangeEvent('hashchange'))
  }, [article.id, displayArticle.id, displayArticle.area])
  const validURL = /^https?:\/\//.test(controllerURL) && (() => { try { const u = new URL(controllerURL); return !u.username && !u.password && !u.search && !u.hash && /^[a-zA-Z0-9:/._~%[\]-]+$/.test(controllerURL) } catch { return false } })()
  const source = state.source && article.id === 'api-overview' && validURL ? state.source.split('http://localhost:8080/api/v1').join(controllerURL.replace(/\/$/, '')) : state.source || ''
  const blocks = useMemo(() => articleBlocks(displayArticle, source), [displayArticle, source])
  return <>
    <header className="help-article-header"><div><p className="api-guide-kicker">{displayArticle.group || 'Guide'}</p><h2>{displayArticle.title}</h2></div>
      <button type="button" className="btn btn-secondary btn-sm" onClick={async () => {
        const url = `${window.location.href.split('#')[0]}${helpArticleHash(displayArticle, section)}`
        setCopied(await copyTextToClipboard(url) ? 'Link copied' : 'Copy unavailable. Copy the address from your browser.')
      }}>Copy article link</button><span role="status">{copied}</span>
    </header>
    {article.id === 'dsl' && <p><a href={dslSchemaURL} download="dsl.schema.json">Download the generated DSL JSON schema</a></p>}
    {article.id === 'api-overview' && <label className="help-controller-url">Controller API URL
      <input type="url" value={controllerURL} onChange={event => setControllerURL(event.target.value)} aria-invalid={!validURL} />
      <span>{validURL ? 'Examples below use this URL. No request is sent.' : 'Enter an HTTP(S) URL without credentials, query parameters or shell characters.'}</span>
    </label>}
    {state.source && section && !resolvedArticle && <p role="status">That section is not available in this build. Choose a section from On this page.</p>}
    {state.error ? <div role="alert"><p>{state.error}</p><button type="button" onClick={() => { setState({}); setAttempt(value => value + 1) }}>Retry article</button></div> : state.source === undefined ? <p role="status">Loading article…</p> : <GuideMarkdownArticle
      source={source} blocks={blocks} section={section}
      sectionHref={id => helpArticleHash(displayArticle, id)}
      resolveLink={href => resolveHelpLink(href, displayArticle, state.source!)}
    />}
    {state.source && <p className="help-article-owner">Maintained by {displayArticle.owner}. <a href="#/docs?article=changes">Review scope & version</a></p>}
  </>
}
