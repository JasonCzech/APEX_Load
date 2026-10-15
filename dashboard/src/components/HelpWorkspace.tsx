import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { helpArticles, helpArticleHash, loadHelpSource, searchHelp, type HelpArticle } from '../helpContent'
import { type HelpAreaID } from '../apiGuideNavigation'
import { useHelpHash } from '../hooks/useHelpHash'
import './HelpWorkspace.css'
import { useSectionListKeys } from './ux/useSectionListKeys'

const UTILITY_ARTICLE_IDS = new Set(['capabilities', 'changes', 'support'])

export default function HelpWorkspace({ area, activeArticle, isAdmin, children }: { area: HelpAreaID; activeArticle: string; isAdmin: boolean; children: ReactNode }) {
  const hash = useHelpHash()
  const sidebarRef = useSectionListKeys()
  // Utility articles live in More help only, so no entry is listed twice.
  const articles = helpArticles.filter(article => article.area === area && (isAdmin || article.area !== 'admin-guide') && !UTILITY_ARTICLE_IDS.has(article.id))
  const groups = Array.from(new Set(articles.map(article => article.group || 'Articles')))
  // Keying the search form to navigation clears stale searches after opening a result.
  return <div className="help-workspace">
    <HelpSearch key={hash} isAdmin={isAdmin} />
    <div className="help-columns">
      {/* The same always-visible section list as Settings and Access. */}
      <aside ref={sidebarRef} className="section-nav help-sidebar">
        <nav aria-label="Help articles">
          {/* Named groups label the list visually; the area heading stays for screen readers. */}
          <h2 className={groups.length > 1 ? 'sr-only' : undefined}>In this area</h2>
          {groups.map(group => <div className="help-article-group" key={group}>
            {groups.length > 1 && <h3>{group}</h3>}
            <ul>{articles.filter(article => (article.group || 'Articles') === group).map(article => <li key={article.id}>
              <a href={helpArticleHash(article)} aria-current={activeArticle === article.id ? 'page' : undefined}>{article.title}</a>
            </li>)}</ul>
          </div>)}
          {area === 'general' && <a href="#/swagger" aria-current={activeArticle === 'api-reference' ? 'page' : undefined}>API Reference (OpenAPI)</a>}
        </nav>
        <nav aria-label="Help utilities" className="help-utilities">
          <h3>More help</h3>
          <a href="#/docs?guide=user-guide&article=capabilities" aria-current={activeArticle === 'capabilities' ? 'page' : undefined}>Capabilities & limitations</a>
          <a href="#/docs?article=changes" aria-current={activeArticle === 'changes' ? 'page' : undefined}>Changes & version</a>
          <a href="#/docs?guide=troubleshooting&article=support" aria-current={activeArticle === 'support' ? 'page' : undefined}>Contact support</a>
        </nav>
      </aside>
      <div className="help-reading-pane">{children}</div>
    </div>
    <footer className="help-version">
      Documentation bundled with dashboard {__APEXLOAD_UX_VERSION__} · Build {__APEXLOAD_UX_BUILD_TIME__}. Compare the controller version using the sidebar build information.
      <a href="#/docs?article=changes">Review dates, ownership & changes</a>
    </footer>
  </div>
}

function HelpSearch({ isAdmin }: { isAdmin: boolean }) {
  const [query, setQuery] = useState('')
  const [index, setIndex] = useState<{ article: HelpArticle; source: string }[]>([])
  const [loading, setLoading] = useState(false)
  const [failed, setFailed] = useState(0)
  const [attempt, setAttempt] = useState(0)
  const hasQuery = query.trim().length > 0
  useEffect(() => {
    if (!hasQuery) return
    let cancelled = false
    const timer = setTimeout(() => {
      setLoading(true)
      const articles = helpArticles.filter(article => isAdmin || article.area !== 'admin-guide')
      void Promise.allSettled(articles.map(async article => ({ article, source: await loadHelpSource(article) }))).then(results => {
        if (cancelled) return
        setIndex(results.flatMap(result => result.status === 'fulfilled' ? [result.value] : []))
        setFailed(results.filter(result => result.status === 'rejected').length)
        setLoading(false)
      })
    }, 150)
    return () => { cancelled = true; clearTimeout(timer) }
  }, [hasQuery, isAdmin, attempt])
  const results = useMemo(() => searchHelp(index.filter(({ article }) => isAdmin || article.area !== 'admin-guide'), query), [index, query, isAdmin])
  return <section className="help-search" aria-label="Search documentation">
    <label htmlFor="help-search">Search all help articles</label>
    <div className="help-search-input"><input id="help-search" type="search" autoComplete="off" value={query} onChange={event => setQuery(event.target.value)} placeholder="Try replay, permissions, pacing or backup" />
      {query && <button type="button" className="btn btn-secondary" onClick={() => setQuery('')}>Clear search</button>}
    </div>
    {hasQuery && <div className="help-search-results">
      <p role="status">{loading || (!index.length && !failed) ? 'Searching documentation…' : `${results.length} matching articles`}</p>
      {failed > 0 && <p role="alert">Some articles could not be searched. <button type="button" onClick={() => setAttempt(value => value + 1)}>Retry search</button></p>}
      {!loading && index.length > 0 && results.length === 0 && <p>No matching articles. Try a shorter phrase or use the article navigation.</p>}
      <ul>{results.map(result => <li key={result.article.id}><a href={helpArticleHash(result.article, result.section)}>{result.article.title}</a><p>{result.snippet}</p></li>)}</ul>
      <a href={`#/swagger?search=${encodeURIComponent(query)}`}>Search API operations for “{query}”</a>
    </div>}
  </section>
}
