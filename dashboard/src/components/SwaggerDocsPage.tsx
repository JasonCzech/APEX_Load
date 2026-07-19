import { useEffect, useMemo, useState } from 'react'
import { loadApiEndpointCatalog, loadSwaggerYaml } from '../apiDocsClient'
import { buildEndpointCatalogMap, endpointCatalogKey, type ApiEndpointCatalog, type ApiEndpointCatalogRoute } from '../apiEndpointCatalog'
import { buildAllowedOperationSet, canShowEndpointForDocs, classifyEndpointForDocs } from '../apiGuideAccess'
import type { DashboardAuthEffectiveAccessResource } from '../types'
import { useApiGuideTopbarContribution } from './useApiGuideTopbarContribution'
import { parseSwaggerDocument, type SwaggerOperation } from './swaggerDocsParser'

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
  const document = useMemo(() => parseSwaggerDocument(docsState?.swaggerYaml || ''), [docsState?.swaggerYaml])
  const categories = useMemo(() => Array.from(new Set(document.operations.map(operation => operation.category))).sort(), [document.operations])
  const [query, setQuery] = useState('')
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
  const isAdmin = ['manage_users', 'manage_user_bindings', 'view_user_activity', 'manage_service_accounts', 'manage_system']
    .some(operation => allowedOperations.has(operation))
  useApiGuideTopbarContribution('swagger', 'Swagger Docs', undefined, isAdmin)

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
  }, [])

  return (
    <div className="page swagger-docs-page">
      <div className="page-header">
        <div>
          <h2>Swagger Docs</h2>
          <p className="page-subtitle">{stripMarkdown(document.description) || 'Searchable route and access reference generated from the APEX Load OpenAPI document.'}</p>
        </div>
      </div>

      {!docsState && (
        <section className="api-guide-callout">
          <h3>{loadError ? 'API Docs Unavailable' : 'Loading API Docs'}</h3>
          <p>{loadError || 'Loading the controller-backed OpenAPI document and endpoint catalog.'}</p>
        </section>
      )}

      {docsState && (
        <>
      <section className="swagger-docs-hero" aria-labelledby="swagger-docs-title">
        <div>
          <span className="api-guide-kicker">OpenAPI</span>
          <h3 id="swagger-docs-title">{document.title}</h3>
          <p>{document.serverUrl || 'http://localhost:8080'} · Version {document.version || '1.0.0'}</p>
        </div>
        <div className="swagger-docs-stats">
          <div><span>Operations</span><strong>{document.operations.length}</strong></div>
          <div><span>Categories</span><strong>{categories.length}</strong></div>
          <div><span>Auth</span><strong>Bearer, API key, OIDC</strong></div>
        </div>
      </section>

      <section className="swagger-docs-controls" aria-label="Swagger documentation filters">
        <label>
          <span>Search APIs</span>
          <input value={query} onChange={event => setQuery(event.target.value)} placeholder="Search method, path, category, role..." />
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
          <label className="swagger-docs-checkbox">
            <input
              type="checkbox"
              checked={myAccessOnly}
              disabled={!effectiveAccessLoaded || effectiveAccessError}
              onChange={event => setMyAccessOnly(event.target.checked)}
            />
            <span>My access only</span>
          </label>
        </label>
      </section>

      <section className="swagger-docs-results" aria-live="polite">
        <div className="api-guide-section-header">
          <h3>API Reference</h3>
          <p>{filteredOperations.length} endpoint{filteredOperations.length === 1 ? '' : 's'} shown. All registered routes are included by default. Query/header names and types, request media/top-level fields, declared response statuses/media, and access requirements are generated from route and handler code; detailed validation rules remain handler-defined.</p>
        </div>
        {filteredOperations.length === 0 ? (
          <div className="api-guide-callout">
            <h3>No APIs Found</h3>
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
    </div>
  )
}

function SwaggerOperationCard({
  operation,
  catalogRoute,
  allowedOperations,
  effectiveAccessLoaded,
  effectiveAccessError,
}: {
  operation: SwaggerOperation
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
              <dt>Access Control</dt>
              <dd>{operation.roles || 'Public or inherited'}</dd>
            </div>
            <div>
              <dt>Category</dt>
              <dd>{catalogRoute?.category || operation.category}</dd>
            </div>
            <div>
              <dt>OpenAPI Tags</dt>
              <dd>{operation.tags.length ? operation.tags.join(', ') : 'None'}</dd>
            </div>
          </dl>
          {operation.requestBody && (
            <div className="swagger-request-body-detail">
              <h5>Request Body {operation.requestBody.required ? '(required)' : '(optional)'}</h5>
              {operation.requestBody.media.map(media => (
                <div key={`${operation.operationId}-${media.mediaType}`}>
                  <p><code>{media.mediaType}</code>{media.schemaRef ? <> · <code>{media.schemaRef}</code></> : media.schemaType ? ` · ${media.schemaType}` : ''}</p>
                  {media.fields.length > 0 ? (
                    <table>
                      <thead><tr><th>Field</th><th>Type</th></tr></thead>
                      <tbody>
                        {media.fields.map(field => (
                          <tr key={`${operation.operationId}-${media.mediaType}-${field.name}`}>
                            <td><code>{field.name}</code></td>
                            <td>{field.type}{field.format ? ` (${field.format})` : ''}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  ) : <p>Payload fields are operation-specific or defined by the referenced schema.</p>}
                </div>
              ))}
            </div>
          )}
          <div className="swagger-detail-grid">
            <div>
              <h5>Parameters</h5>
              {operation.parameters.length > 0 ? (
                <table>
                  <thead><tr><th>Name</th><th>In</th><th>Required</th><th>Type</th></tr></thead>
                  <tbody>
                    {operation.parameters.map(parameter => (
                      <tr key={`${operation.operationId}-${parameter.location}-${parameter.name}`}>
                        <td><code>{parameter.name}</code></td>
                        <td>{parameter.location || 'n/a'}</td>
                        <td>{parameter.required ? 'Yes' : 'No'}</td>
                        <td>{parameter.type || 'string'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              ) : <p>No path or query parameters are declared.</p>}
            </div>
            <div>
              <h5>Responses</h5>
              {operation.responses.length > 0 ? (
                <table>
                  <thead><tr><th>Status</th><th>Description</th></tr></thead>
                  <tbody>
                    {operation.responses.map(response => (
                      <tr key={`${operation.operationId}-${response.status}`}>
                        <td><code>{response.status}</code></td>
                        <td>{response.description || 'Response'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              ) : <p>No documented responses.</p>}
            </div>
          </div>
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
