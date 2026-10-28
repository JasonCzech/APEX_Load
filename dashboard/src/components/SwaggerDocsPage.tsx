import HelpWorkspace from './HelpWorkspace'
import { useHelpHash } from '../hooks/useHelpHash'
import { ApiGuideTopbarToggle } from './ApiGuideTopbarToggle'
import { useHasTopbarContributionProvider } from '../hooks/TopbarContributionProvider'
import { useEffect, useMemo, useState } from 'react'
import { loadApiEndpointCatalog, loadSwaggerYaml } from '../apiDocsClient'
import { buildEndpointCatalogMap, endpointCatalogKey, type ApiEndpointCatalog, type ApiEndpointCatalogRoute } from '../apiEndpointCatalog'
import { buildAllowedOperationSet, canShowEndpointForDocs, classifyEndpointForDocs } from '../apiGuideAccess'
import type { DashboardAuthEffectiveAccessResource } from '../types'
import { useApiGuideTopbarContribution } from './useApiGuideTopbarContribution'
import { parseSwaggerDocument, type SwaggerOperation, type SwaggerDocument } from './swaggerDocsParser'

import { canSaveApiResourceViaBridge, saveApiResourceViaBridge } from '../desktop/desktopFiles'
import { downloadTextFile } from '../utils/fileUtils'
import SwaggerOperationDetail from './SwaggerOperationDetail'

function stripMarkdown(value: string) {
  return value
    .replace(/\*\*/g, '')
    .replace(/`/g, '')
    .replace(/\[(.*?)\]\(.*?\)/g, '$1')
    .trim()
}

function operationMatches(operation: SwaggerOperation, query: string, category: string) {
  const normalizedQuery = query.trim().toLowerCase()
  const matchesCategory = category === 'all' || operation.category === category
  if (!matchesCategory) return false
  if (!normalizedQuery) return true
  return [
    operation.method,
    operation.path,
    operation.category,
    operation.summary,
    operation.description,
    operation.operationId,
    operation.roles,
    ...operation.tags,
  ].join(' ').toLowerCase().includes(normalizedQuery)
}

function groupByCategory(operations: OperationCatalogJoin[]) {
  return operations.reduce<Record<string, OperationCatalogJoin[]>>((groups, operationJoin) => {
    const category = operationJoin.operation.category
    groups[category] = groups[category] || []
    groups[category].push(operationJoin)
    return groups
  }, {})
}

function descriptionLines(description: string) {
  return description
    .split(/\n+/)
    .map(stripMarkdown)
    .filter(Boolean)
}

type SwaggerDocsPageProps = {
  effectiveAccessResources?: DashboardAuthEffectiveAccessResource[]
  effectiveAccessLoaded?: boolean
  effectiveAccessError?: boolean
}

type OperationCatalogJoin = {
  operation: SwaggerOperation
  catalogRoute?: ApiEndpointCatalogRoute
}

type SwaggerDocsState = {
  swaggerYaml: string
  catalog: ApiEndpointCatalog
}

export default function SwaggerDocsPage({
  effectiveAccessResources = [],
  effectiveAccessLoaded = true,
  effectiveAccessError = false,
}: SwaggerDocsPageProps) {
  const [docsState, setDocsState] = useState<SwaggerDocsState | null>(null)
  const [loadError, setLoadError] = useState('')
  const [loadAttempt, setLoadAttempt] = useState(0)
  const [downloadMessage, setDownloadMessage] = useState('')
  const document = useMemo(() => parseSwaggerDocument(docsState?.swaggerYaml || ''), [docsState?.swaggerYaml])
  const categories = useMemo(() => Array.from(new Set(document.operations.map(operation => operation.category))).sort(), [document.operations])
  const hash = useHelpHash()
  const hasTopbar = useHasTopbarContributionProvider()
  const initialQuery = new URLSearchParams(hash.split('?')[1] || '').get('search') || ''
  const [query, setQuery] = useState(initialQuery)
  useEffect(() => { setQuery(initialQuery) }, [initialQuery])
  const [category, setCategory] = useState('all')
  const [myAccessOnly, setMyAccessOnly] = useState(false)
  const allowedOperations = useMemo(() => buildAllowedOperationSet(effectiveAccessResources), [effectiveAccessResources])
  const endpointCatalogByKey = useMemo(
    () => buildEndpointCatalogMap(docsState?.catalog.routes ?? []),
    [docsState?.catalog.routes],
  )
  const operationsWithCatalog = useMemo<OperationCatalogJoin[]>(() => (
    document.operations.map(operation => ({
      operation,
      catalogRoute: endpointCatalogByKey.get(endpointCatalogKey(operation.method, operation.path)),
    }))
  ), [document.operations, endpointCatalogByKey])
  const filteredOperations = useMemo(
    () => operationsWithCatalog
      .filter(({ operation }) => operationMatches(operation, query, category))
      .filter(({ catalogRoute }) => !myAccessOnly || (catalogRoute ? canShowEndpointForDocs(catalogRoute, allowedOperations) : false)),
    [allowedOperations, category, myAccessOnly, operationsWithCatalog, query],
  )
  const groupedOperations = useMemo(() => groupByCategory(filteredOperations), [filteredOperations])
  // Mirror ApiGuidePage's "any administrator" gate so the admin-only guide chip stays
  // visible while navigating across the shared API-guide toggle from the Swagger tab.
  const isAdmin = effectiveAccessLoaded && !effectiveAccessError && ['manage_users', 'manage_user_bindings', 'view_user_activity', 'manage_service_accounts', 'manage_system']
    .some(operation => allowedOperations.has(operation))
  useApiGuideTopbarContribution('swagger', 'Help & documentation', undefined, isAdmin)

  useEffect(() => {
    let cancelled = false
    setLoadError('')
    Promise.all([loadSwaggerYaml(), loadApiEndpointCatalog()])
      .then(([swaggerYaml, catalog]) => {
        if (cancelled) return
        setDocsState({ swaggerYaml, catalog })
      })
      .catch(error => {
        if (cancelled) return
        setDocsState(null)
        setLoadError(error instanceof Error ? error.message : 'API documentation could not be loaded.')
      })
    return () => {
      cancelled = true
    }
  }, [loadAttempt])
  useEffect(() => { window.document.querySelector('.main-content')?.scrollTo?.({ top: 0, behavior: 'instant' }) }, [])

  return (
    <div className="page swagger-docs-page help-page">
      {!hasTopbar && <ApiGuideTopbarToggle active="swagger" includeAdminGuide={isAdmin} />}
      <HelpWorkspace area="general" activeArticle="api-reference" isAdmin={isAdmin}>
      <h2 className="sr-only">API Reference (OpenAPI)</h2>
      {!docsState && (
        <section className="api-guide-callout">
          <h3>{loadError ? 'API docs unavailable' : 'Loading API docs'}</h3>
          <p role={loadError ? 'alert' : 'status'}>{loadError || 'Loading the controller-backed OpenAPI document and endpoint catalog.'}</p>
          {loadError && <button type="button" onClick={() => setLoadAttempt(value => value + 1)}>Retry API reference</button>}
        </section>
      )}

      {docsState && (
        <>
      <section className="swagger-docs-hero" aria-labelledby="swagger-docs-title">
        <div>
          <h3 id="swagger-docs-title">{document.title}</h3>
          <p>{document.serverUrl || 'http://localhost:8080'} · API schema version {document.version || '1.0.0'} (separate from the application build)</p>
        </div>
        <div className="swagger-docs-stats">
          <div><span>Operations</span><strong>{document.operations.length}</strong></div>
          <div><span>Categories</span><strong>{categories.length}</strong></div>
          <div><span>Auth</span><strong>Bearer, API key, OIDC</strong></div>
        </div>
      </section>

      <div className="swagger-reference-links">
        <button className="btn btn-secondary btn-sm" type="button" onClick={async () => {
          try {
            const outcome = canSaveApiResourceViaBridge()
              ? await saveApiResourceViaBridge('/api/v1/docs/swagger.yaml', 'apexload-openapi.yaml')
              : await downloadTextFile(docsState.swaggerYaml, 'apexload-openapi.yaml', 'application/yaml')
            if (outcome === 'unavailable') throw new Error('The native save bridge became unavailable. Try the download again.')
            setDownloadMessage(outcome === 'cancelled' ? 'Download cancelled.' : outcome === 'saved' ? 'OpenAPI document saved.' : 'OpenAPI download started.')
          } catch (error) { setDownloadMessage(error instanceof Error ? error.message : 'OpenAPI download failed.') }
        }}>Download OpenAPI</button>
        <a href="#/docs?guide=general&article=authentication">Authentication guide</a>
        <a href="#/docs?guide=quick-start">API workflow examples</a>
      </div>
      <span role="status">{downloadMessage}</span>
      <section className="swagger-docs-controls" aria-label="Swagger documentation filters">
        <label>
          <span>Search APIs</span>
          <input value={query} onChange={event => setQuery(event.target.value)} placeholder="Search method, path, category, role…" />
        </label>
        <label>
          <span>Category</span>
          <select value={category} onChange={event => setCategory(event.target.value)}>
            <option value="all">All categories</option>
            {categories.map(nextCategory => <option key={nextCategory} value={nextCategory}>{nextCategory}</option>)}
          </select>
        </label>
        <label className="swagger-docs-access-filter">
          <span>Access</span>
          <span className="swagger-docs-checkbox">
            <input
              type="checkbox"
              checked={myAccessOnly}
              disabled={!effectiveAccessLoaded || effectiveAccessError}
              onChange={event => setMyAccessOnly(event.target.checked)}
            />
            <span>My access only</span>
          </span>
        </label>
      </section>

      <section className="swagger-docs-results" aria-live="polite">
        <div className="api-guide-section-header">
          <h3 className="sr-only">API Reference</h3>
          <p>{filteredOperations.length} endpoint{filteredOperations.length === 1 ? '' : 's'} shown. All registered routes are included by default. Expand an endpoint for request examples, body schemas, response formats, and authentication. Examples use placeholder values; copying a request does not execute it.</p>
        </div>
        {filteredOperations.length === 0 ? (
          <div className="api-guide-callout">
            <h3>No APIs found</h3>
            <p>Clear the search or choose another category to see the generated OpenAPI endpoints.</p>
          </div>
        ) : Object.entries(groupedOperations).map(([group, operations]) => (
          <div className="swagger-docs-tag-group" key={group}>
            <h4>{group}</h4>
            <div className="swagger-docs-operation-list">
              {operations.map(({ operation, catalogRoute }) => (
                <SwaggerOperationCard
                  key={`${operation.method}-${operation.path}-${operation.operationId}`}
                  operation={operation}
                  document={document}
                  catalogRoute={catalogRoute}
                  allowedOperations={allowedOperations}
                  effectiveAccessLoaded={effectiveAccessLoaded}
                  effectiveAccessError={effectiveAccessError}
                />
              ))}
            </div>
          </div>
        ))}
      </section>
        </>
      )}
      </HelpWorkspace>
    </div>
  )
}

function SwaggerOperationCard({
  operation,
  document,
  catalogRoute,
  allowedOperations,
  effectiveAccessLoaded,
  effectiveAccessError,
}: {
  operation: SwaggerOperation
  document: SwaggerDocument
  catalogRoute?: ApiEndpointCatalogRoute
  allowedOperations: Set<string>
  effectiveAccessLoaded: boolean
  effectiveAccessError: boolean
}) {
  const [open, setOpen] = useState(false)
  return (
    <details className="swagger-operation-card" open={open} onToggle={event => setOpen(event.currentTarget.open)}>
      <summary>
        <span className={`swagger-method swagger-method-${operation.method.toLowerCase()}`}>{operation.method}</span>
        <code>{operation.path}</code>
        <strong>{operation.summary}</strong>
        {renderAccessBadge(catalogRoute, allowedOperations, effectiveAccessLoaded, effectiveAccessError)}
      </summary>
      {open && (
        <div className="swagger-operation-detail">
          {descriptionLines(operation.description).map(line => <p key={line}>{line}</p>)}
          <dl>
            <div>
              <dt>Operation ID</dt>
              <dd><code>{operation.operationId || 'n/a'}</code></dd>
            </div>
            <div>
              <dt>Access control</dt>
              <dd>{operation.roles || 'Public or inherited'}</dd>
            </div>
            <div>
              <dt>Category</dt>
              <dd>{catalogRoute?.category || operation.category}</dd>
            </div>
            <div>
              <dt>OpenAPI tags</dt>
              <dd>{operation.tags.length ? operation.tags.join(', ') : 'None'}</dd>
            </div>
          </dl>
          <SwaggerOperationDetail operation={operation} document={document} />
        </div>
      )}
    </details>
  )
}

function renderAccessBadge(
  catalogRoute: ApiEndpointCatalogRoute | undefined,
  allowedOperations: Set<string>,
  effectiveAccessLoaded: boolean,
  effectiveAccessError: boolean,
) {
  if (!catalogRoute) return <span className="swagger-access-badge swagger-access-muted">Unmatched</span>
  if (catalogRoute.path === '/metrics') {
    return <span className="swagger-access-badge swagger-access-muted">Conditional auth</span>
  }
  const visibility = classifyEndpointForDocs(catalogRoute)
  if (!visibility.classified) return <span className="swagger-access-badge swagger-access-muted">Unclassified</span>
  if (visibility.operations.length === 0) {
    return catalogRoute.public
      ? <span className="swagger-access-badge swagger-access-public">Public</span>
      : <span className="swagger-access-badge swagger-access-available">Authenticated</span>
  }
  if (!effectiveAccessLoaded) return <span className="swagger-access-badge swagger-access-muted">Checking access</span>
  if (effectiveAccessError) return <span className="swagger-access-badge swagger-access-muted">Access unavailable</span>
  const visible = visibility.operations.some(operation => allowedOperations.has(operation))
  return visible
    ? <span className="swagger-access-badge swagger-access-available">My access</span>
    : <span className="swagger-access-badge swagger-access-restricted">Not in my access</span>
}
