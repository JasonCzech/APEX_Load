import { useCallback, useEffect, useMemo, useState } from 'react'

import { loadApiEndpointCatalog } from '../apiDocsClient'
import { buildEndpointCatalogMap, endpointCatalogKey, type ApiEndpointCatalog, type ApiEndpointCatalogRoute } from '../apiEndpointCatalog'
import { API_GUIDE_DEFAULT_PAGE, type ApiGuideContentPageID } from '../apiGuideNavigation'
import { buildAllowedOperationSet, canShowEndpointForDocs, docsVisibilityOperationsForEndpoint } from '../apiGuideAccess'
import { useHasTopbarContributionProvider } from '../hooks/TopbarContributionProvider'
import type { DashboardAuthEffectiveAccessResource } from '../types'
import { ApiGuideTopbarToggle } from './ApiGuideTopbarToggle'
import { useApiGuideTopbarContribution } from './useApiGuideTopbarContribution'

type EndpointDoc = ApiEndpointCatalogRoute & {
  operations: string[]
}

type EndpointGroup = {
  title: string
  endpoints: EndpointDoc[]
}

type CommandDoc = {
  command: string
  method: string
  path: string
}

const quickStartCommands: CommandDoc[] = [
  { command: 'curl http://localhost:8080/api/v1/system/health', method: 'GET', path: '/api/v1/system/health' },
  { command: 'curl -H "Authorization: Bearer $APEXLOAD_API_KEY" http://localhost:8080/api/v1/projects', method: 'GET', path: '/api/v1/projects' },
  { command: 'curl -X POST -H "Authorization: Bearer $APEXLOAD_API_KEY" -H "Content-Type: application/json" -d @script.json http://localhost:8080/api/v1/scripts', method: 'POST', path: '/api/v1/scripts' },
  { command: 'curl -H "Authorization: Bearer $APEXLOAD_API_KEY" http://localhost:8080/api/v1/integrations/github/diagnostics', method: 'GET', path: '/api/v1/integrations/github/diagnostics' },
]

const mcpResources = [
  'apexload://api-guide',
  'apexload://auth/access-matrix',
  'apexload://system/parity-status',
  'apexload://runtime-settings/support-matrix',
  'apexload://protocol-bundles/support-matrix',
]

const statusRows = [
  ['200', 'Request succeeded.'],
  ['201', 'Resource created.'],
  ['202', 'Long-running operation accepted.'],
  ['400', 'Invalid request body or query parameter.'],
  ['401', 'Authentication required or token invalid.'],
  ['403', 'Authenticated but blocked by RBAC or service-account scope.'],
  ['404', 'Resource not found.'],
  ['409', 'Conflict, such as a schedule conflict, capacity shortfall, or script_shakeout_failed review gate.'],
]

type ApiGuidePageProps = {
  activePage?: ApiGuideContentPageID
  onNavigate?: (page: 'access') => void
  effectiveAccessResources?: DashboardAuthEffectiveAccessResource[]
  effectiveAccessLoaded?: boolean
  effectiveAccessError?: boolean
}

export default function ApiGuidePage({
  activePage = API_GUIDE_DEFAULT_PAGE,
  onNavigate,
  effectiveAccessResources = [],
  effectiveAccessLoaded = true,
  effectiveAccessError = false,
}: ApiGuidePageProps) {
  const [activeGuideSection, setActiveGuideSection] = useState<ApiGuideContentPageID>(activePage)
  const [endpointCatalog, setEndpointCatalog] = useState<ApiEndpointCatalog | null>(null)
  const [endpointCatalogError, setEndpointCatalogError] = useState('')
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const allowedOperations = useMemo(() => buildAllowedOperationSet(effectiveAccessResources), [effectiveAccessResources])
  const endpointCatalogRoutes = useMemo(() => endpointCatalog?.routes ?? [], [endpointCatalog])
  const endpointCatalogByKey = useMemo(() => buildEndpointCatalogMap(endpointCatalogRoutes), [endpointCatalogRoutes])
  const catalogEndpointDocs = useMemo(() => endpointCatalogRoutes.map(route => ({
    ...route,
    operations: docsVisibilityOperationsForEndpoint(route),
  })), [endpointCatalogRoutes])
  const authRows = useMemo(
    () => catalogEndpointDocs.filter(route => route.path.startsWith('/api/v1/auth/')),
    [catalogEndpointDocs],
  )
  const endpointGroups = useMemo(() => groupEndpointDocs(catalogEndpointDocs), [catalogEndpointDocs])
  const visibleQuickStartCommands = quickStartCommands.filter(command => {
    const route = endpointCatalogByKey.get(endpointCatalogKey(command.method, command.path))
    return route ? canShowEndpointForDocs(route, allowedOperations) : false
  })
  const visibleAuthRows = authRows.filter(row => canShowEndpointForDocs(row, allowedOperations))
  const visibleEndpointGroups = endpointGroups
    .map(group => ({
      ...group,
      endpoints: group.endpoints.filter(endpoint => canShowEndpointForDocs(endpoint, allowedOperations)),
    }))
    .filter(group => group.endpoints.length > 0)
  const canUseAccessWorkspace = ['manage_users', 'manage_user_bindings', 'view_user_activity', 'manage_service_accounts'].some(operation => allowedOperations.has(operation))
  const handleContentPageChange = useCallback((page: ApiGuideContentPageID) => {
    setActiveGuideSection(page)
  }, [])
  useApiGuideTopbarContribution(activeGuideSection, 'API Guide', handleContentPageChange)

  useEffect(() => {
    setActiveGuideSection(activePage)
  }, [activePage])

  useEffect(() => {
    let cancelled = false
    setEndpointCatalogError('')
    loadApiEndpointCatalog()
      .then(catalog => {
        if (cancelled) return
        setEndpointCatalog(catalog)
      })
      .catch(error => {
        if (cancelled) return
        setEndpointCatalog(null)
        setEndpointCatalogError(error instanceof Error ? error.message : 'API endpoint catalog could not be loaded.')
      })
    return () => {
      cancelled = true
    }
  }, [])

  const activeContentPage = activeGuideSection
  const endpointCatalogLoaded = endpointCatalog !== null

  return (
    <div className="page api-guide-page">
      <div className="page-header">
        <div>
          <h2>API Guide</h2>
          <p className="page-subtitle">REST and WebSocket reference for automating APEX Load from CI/CD, integrations, and operator tooling.</p>
        </div>
      </div>
      {!hasTopbarContributionProvider && (
        <div className="inline-create-row api-guide-section-switcher">
          <ApiGuideTopbarToggle active={activeGuideSection} onContentPageChange={handleContentPageChange} />
        </div>
      )}

      {activeContentPage === 'general' && (
        <>
          <section className="api-guide-hero" aria-labelledby="api-guide-base-url">
            <div>
              <span className="api-guide-kicker">Base URL</span>
              <h3 id="api-guide-base-url">http://&lt;controller-host&gt;:8080/api/v1</h3>
              <p>Local dashboard development proxies <code>/api</code> from <code>localhost:3000</code>; direct clients should call the controller origin.</p>
            </div>
            <div className="api-guide-meta-grid">
              <div><span>Auth</span><strong>Dev, OIDC/trusted headers, local users, service accounts</strong></div>
              <div><span>Payloads</span><strong>JSON plus CSV, HTML, ZIP, and archive downloads</strong></div>
              <div><span>Streams</span><strong>WebSocket at /api/v1/ws</strong></div>
              <div><span>MCP</span><strong>FastMCP bridge for agent and CI automation</strong></div>
            </div>
          </section>

          <section className="page-section">
            <div className="api-guide-section-header">
              <h3>Responses And Errors</h3>
              <p>JSON error responses use <code>{'{"error":"message"}'}</code>. Download endpoints may return CSV, HTML, ZIP, or binary artifacts.</p>
            </div>
            <div className="api-guide-status-grid">
              {statusRows.map(([status, meaning]) => (
                <div className="api-guide-status-card" key={status}>
                  <strong>{status}</strong>
                  <span>{meaning}</span>
                </div>
              ))}
            </div>
          </section>

          <section className="api-guide-callout">
            <h3>Dev Preview Headers</h3>
            <p>In local dev auth mode only, API clients may send <code>X-APEXLoad-Dev-View: admin|user</code> and <code>X-APEXLoad-Dev-Project-ID: &lt;project-id&gt;</code> to simulate dashboard Admin or project editor User behavior. Production auth modes ignore these headers.</p>
          </section>
        </>
      )}

      {activeContentPage === 'mcp' && (
        <section className="page-section api-guide-mcp-section">
          <div className="api-guide-section-header">
            <h3>MCP Server</h3>
            <p>Run the APEX Load MCP bridge for agent-driven automation. It uses service-account auth and adds confirmation guardrails before mutating API calls.</p>
          </div>
          <div className="api-guide-mcp-grid">
            <article className="api-guide-mcp-card">
              <h4>Start The Server</h4>
              <pre><code>{'APEXLOAD_URL=http://localhost:8080/api/v1 \\\nAPEXLOAD_API_KEY=srk_... \\\npython3 mcp/server.py'}</code></pre>
              <a href="https://github.com/jasonczech/apexload/blob/main/mcp/README.md" target="_blank" rel="noreferrer">MCP setup guide</a>
            </article>
            <article className="api-guide-mcp-card">
              <h4>Tool Coverage</h4>
              <p>Structured tools cover system, auth, projects, scripts, scenarios, runs, reports, scheduling, fleet, data pools, imports, and AI assistance.</p>
              <div className="api-guide-mcp-tags">
                <span>service accounts</span>
                <span>confirm=true</span>
                <span>reason required</span>
              </div>
            </article>
            <article className="api-guide-mcp-card">
              <h4>Agent Resources</h4>
              <ul>
                {mcpResources.map(resource => (
                  <li key={resource}><code>{resource}</code></li>
                ))}
              </ul>
            </article>
          </div>
        </section>
      )}

      {activeContentPage === 'quick-start' && (
        <section className="page-section">
          <div className="api-guide-section-header">
            <h3>Quick Start</h3>
            <p>Use a service account key for automation and keep project/application/release ids in your pipeline context.</p>
          </div>
          {renderCatalogState(endpointCatalogLoaded, endpointCatalogError)}
          {endpointCatalogLoaded && renderAccessState(effectiveAccessLoaded, effectiveAccessError, visibleQuickStartCommands.length === 0, 'No quick-start calls are available for the current access context.')}
          {endpointCatalogLoaded && effectiveAccessLoaded && (
            <div className="api-guide-code-stack">
              {visibleQuickStartCommands.map(({ command }) => (
                <pre key={command}><code>{command}</code></pre>
              ))}
            </div>
          )}
        </section>
      )}

      {activeContentPage === 'authentication' && (
        <>
          <section className="page-section">
            <div className="api-guide-section-header">
              <h3>Authentication</h3>
              <p>OIDC or trusted headers prove human identity, but an active local APEX Load user record is required after bootstrap. Human roles come from local bindings; service accounts can use <code>Authorization: Bearer &lt;api-key&gt;</code> or <code>X-APEXLoad-API-Key</code>, with RBAC roles and token scopes both enforced.</p>
            </div>
            {renderCatalogState(endpointCatalogLoaded, endpointCatalogError)}
            {endpointCatalogLoaded && renderAccessState(effectiveAccessLoaded, effectiveAccessError, visibleAuthRows.length === 0, 'No authentication endpoints are visible for the current access context.')}
            {endpointCatalogLoaded && effectiveAccessLoaded && visibleAuthRows.length > 0 && (
              <div className="api-guide-table-card">
                <table>
                  <thead>
                    <tr><th>Method</th><th>Endpoint</th><th>Use</th></tr>
                  </thead>
                  <tbody>
                    {visibleAuthRows.map(row => (
                      <tr key={endpointCatalogKey(row.method, row.path)}>
                        <td><code>{row.method}</code></td>
                        <td><code>{row.path}</code></td>
                        <td>{row.summary}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          {endpointCatalogLoaded && effectiveAccessLoaded && canUseAccessWorkspace && (
            <section className="api-guide-callout api-guide-key-callout">
              <div>
                <h3>Access Workspace</h3>
                <p>Use Access to onboard human users, assign global/domain/project roles, review activity by user and scope, and generate service-account keys for MCP, CI/CD, reporting exports, and controlled external automation.</p>
              </div>
              {onNavigate && (
                <button className="btn btn-secondary btn-sm btn-accent-action" type="button" onClick={() => onNavigate('access')}>
                  Open Access
                </button>
              )}
            </section>
          )}
        </>
      )}

      {activeContentPage === 'endpoint-map' && (
        <section className="page-section">
          <div className="api-guide-section-header">
            <h3>Endpoint Map</h3>
            <p>Generated from the controller route catalog and filtered using your current effective operations. The controller remains the authorization authority; use Swagger for the complete registered route inventory and access requirements.</p>
          </div>
          {renderCatalogState(endpointCatalogLoaded, endpointCatalogError)}
          {endpointCatalogLoaded && renderAccessState(effectiveAccessLoaded, effectiveAccessError, visibleEndpointGroups.length === 0, 'No endpoints are visible for the current access context.')}
          {endpointCatalogLoaded && effectiveAccessLoaded && visibleEndpointGroups.length > 0 && (
            <div className="api-guide-endpoint-grid">
              {visibleEndpointGroups.map(group => (
                <article className="api-guide-card" key={group.title}>
                  <h4>{group.title}</h4>
                  <ul>
                    {group.endpoints.map(endpoint => (
                      <li key={endpointCatalogKey(endpoint.method, endpoint.path)}><code>{formatEndpoint(endpoint)}</code></li>
                    ))}
                  </ul>
                </article>
              ))}
            </div>
          )}
        </section>
      )}
    </div>
  )
}

function groupEndpointDocs(endpoints: EndpointDoc[]): EndpointGroup[] {
  const groups = endpoints.reduce<Map<string, EndpointDoc[]>>((nextGroups, endpoint) => {
    const group = nextGroups.get(endpoint.category) || []
    group.push(endpoint)
    nextGroups.set(endpoint.category, group)
    return nextGroups
  }, new Map())
  return Array.from(groups.entries()).map(([title, groupEndpoints]) => ({ title, endpoints: groupEndpoints }))
}

function renderAccessState(loaded: boolean, error: boolean, empty: boolean, emptyMessage: string) {
  if (!loaded) {
    return (
      <div className="api-guide-callout">
        <h3>Checking Access</h3>
        <p>Loading your effective access before filtering this guide section.</p>
      </div>
    )
  }
  if (error) {
    return (
      <div className="api-guide-callout">
        <h3>Unable To Filter Access</h3>
        <p>Effective access could not be loaded. Public endpoints are shown, and Swagger remains the complete generated API reference.</p>
      </div>
    )
  }
  if (empty) {
    return (
      <div className="api-guide-callout">
        <h3>No Endpoints Available</h3>
        <p>{emptyMessage}</p>
      </div>
    )
  }
  return null
}

function renderCatalogState(loaded: boolean, error: string) {
  if (loaded) return null
  if (error) {
    return (
      <div className="api-guide-callout">
        <h3>API Docs Unavailable</h3>
        <p>{error}</p>
      </div>
    )
  }
  return (
    <div className="api-guide-callout">
      <h3>Loading API Docs</h3>
      <p>Loading the controller-backed endpoint catalog.</p>
    </div>
  )
}

function formatEndpoint(endpoint: EndpointDoc): string {
  return `${endpoint.method} ${endpoint.path}`
}
