import { useMemo, useState } from 'react'
import swaggerYaml from '../../../docs/swagger.yaml?raw'
import { useApiGuideTopbarContribution } from './ApiGuideTopbarToggle'
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

function groupByCategory(operations: SwaggerOperation[]) {
  return operations.reduce<Record<string, SwaggerOperation[]>>((groups, operation) => {
    groups[operation.category] = groups[operation.category] || []
    groups[operation.category].push(operation)
    return groups
  }, {})
}

function descriptionLines(description: string) {
  return description
    .split(/\n+/)
    .map(stripMarkdown)
    .filter(Boolean)
}

export default function SwaggerDocsPage() {
  const document = useMemo(() => parseSwaggerDocument(swaggerYaml), [])
  const categories = useMemo(() => Array.from(new Set(document.operations.map(operation => operation.category))).sort(), [document.operations])
  const [query, setQuery] = useState('')
  const [category, setCategory] = useState('all')
  const filteredOperations = useMemo(
    () => document.operations.filter(operation => operationMatches(operation, query, category)),
    [document.operations, query, category],
  )
  const groupedOperations = useMemo(() => groupByCategory(filteredOperations), [filteredOperations])
  useApiGuideTopbarContribution('swagger', 'Swagger Docs')

  return (
    <div className="page swagger-docs-page">
      <div className="page-header">
        <div>
          <h2>Swagger Docs</h2>
          <p className="page-subtitle">{stripMarkdown(document.description) || 'Interactive API reference generated from the APEX Load OpenAPI document.'}</p>
        </div>
      </div>

      <section className="swagger-docs-hero" aria-labelledby="swagger-docs-title">
        <div>
          <span className="api-guide-kicker">OpenAPI</span>
          <h3 id="swagger-docs-title">{document.title}</h3>
          <p>{document.serverUrl || 'http://localhost:8080'} · Version {document.version || '1.0.0'}</p>
        </div>
        <div className="swagger-docs-stats">
          <div><span>Operations</span><strong>{document.operations.length}</strong></div>
          <div><span>Categories</span><strong>{categories.length}</strong></div>
          <div><span>Auth</span><strong>Bearer</strong></div>
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
      </section>

      <section className="swagger-docs-results" aria-live="polite">
        <div className="api-guide-section-header">
          <h3>API Reference</h3>
          <p>{filteredOperations.length} endpoint{filteredOperations.length === 1 ? '' : 's'} shown.</p>
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
              {operations.map(operation => (
                <details className="swagger-operation-card" key={`${operation.method}-${operation.path}-${operation.operationId}`}>
                  <summary>
                    <span className={`swagger-method swagger-method-${operation.method.toLowerCase()}`}>{operation.method}</span>
                    <code>{operation.path}</code>
                    <strong>{operation.summary}</strong>
                  </summary>
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
                        <dd>{operation.category}</dd>
                      </div>
                      <div>
                        <dt>OpenAPI Tags</dt>
                        <dd>{operation.tags.length ? operation.tags.join(', ') : 'None'}</dd>
                      </div>
                    </dl>
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
                        ) : <p>No parameters.</p>}
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
                </details>
              ))}
            </div>
          </div>
        ))}
      </section>
    </div>
  )
}
