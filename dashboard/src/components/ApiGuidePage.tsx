import { DashboardAuthEffectiveAccessResource } from '../types'

type EndpointDoc = {
  method: string
  path: string
  purpose?: string
  operations?: string[]
}

type CommandDoc = {
  command: string
  operations?: string[]
}

const quickStartCommands: CommandDoc[] = [
  { command: 'curl http://localhost:8080/api/v1/system/health' },
  { command: 'curl -H "Authorization: Bearer $APEXLOAD_API_KEY" http://localhost:8080/api/v1/projects', operations: ['view_projects'] },
  { command: 'curl -X POST -H "Authorization: Bearer $APEXLOAD_API_KEY" -H "Content-Type: application/json" -d @script.json http://localhost:8080/api/v1/scripts', operations: ['manage_scripts'] },
]

const mcpResources = [
  'apexload://api-guide',
  'apexload://auth/access-matrix',
  'apexload://system/parity-status',
  'apexload://runtime-settings/support-matrix',
  'apexload://protocol-bundles/support-matrix',
]

const authRows: EndpointDoc[] = [
  { method: 'GET', path: '/api/v1/auth/me', purpose: 'Current auth mode, local user status, locally resolved roles, and session metadata.' },
  { method: 'GET', path: '/api/v1/auth/effective-access', purpose: 'Operations allowed for the current principal from local user bindings or service-account roles.' },
  { method: 'GET', path: '/api/v1/auth/access-matrix', purpose: 'Role-to-operation catalog.' },
  { method: 'GET', path: '/api/v1/auth/users', purpose: 'Search local human users available for APEX Load access.', operations: ['manage_users'] },
  { method: 'POST', path: '/api/v1/auth/users', purpose: 'Onboard a human user subject from OIDC or trusted headers.', operations: ['manage_users'] },
  { method: 'PATCH', path: '/api/v1/auth/users/{id}', purpose: 'Update local user metadata.', operations: ['manage_users'] },
  { method: 'POST', path: '/api/v1/auth/users/{id}/bindings', purpose: 'Grant a global, domain, or project role binding.', operations: ['manage_users'] },
  { method: 'DELETE', path: '/api/v1/auth/users/{id}/bindings/{binding_id}', purpose: 'Remove a local role binding.', operations: ['manage_users'] },
  { method: 'POST', path: '/api/v1/auth/users/{id}/disable', purpose: 'Disable a local user while preserving audit history.', operations: ['manage_users'] },
  { method: 'POST', path: '/api/v1/auth/users/{id}/enable', purpose: 'Re-enable a local user record.', operations: ['manage_users'] },
  { method: 'GET', path: '/api/v1/auth/user-activity', purpose: 'Audit events and attributed run usage filtered by user, timeframe, domain, project, action, or status.', operations: ['view_user_activity'] },
  { method: 'GET', path: '/api/v1/auth/user-activity/export', purpose: 'CSV user activity export.', operations: ['view_user_activity'] },
  { method: 'GET', path: '/api/v1/auth/audit-events', purpose: 'Filtered governance audit feed.', operations: ['view_audit'] },
  { method: 'GET', path: '/api/v1/auth/audit-events/export', purpose: 'CSV audit export.', operations: ['view_audit'] },
]

const endpointGroups = [
  {
    title: 'Domain, Repository, Projects, Releases',
    endpoints: [
      { method: 'GET', path: '/api/v1/domains', operations: ['view_domains'] },
      { method: 'GET', path: '/api/v1/domains/{id}/application-repository/categories', operations: ['view_application_repository'] },
      { method: 'GET', path: '/api/v1/projects', operations: ['view_projects'] },
      { method: 'POST', path: '/api/v1/projects', operations: ['manage_repository_projects'] },
      { method: 'GET', path: '/api/v1/projects/{id}/releases', operations: ['view_projects'] },
      { method: 'GET', path: '/api/v1/applications?project_id=<id>', operations: ['view_applications'] },
      { method: 'POST', path: '/api/v1/applications/{id}/releases', operations: ['manage_applications', 'manage_repository_releases'] },
      { method: 'GET', path: '/api/v1/releases/{id}/trend/export', operations: ['view_applications'] },
    ],
  },
  {
    title: 'Scripts And Imports',
    endpoints: [
      { method: 'GET', path: '/api/v1/scripts', operations: ['view_scripts'] },
      { method: 'POST', path: '/api/v1/scripts', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/validate', operations: ['view_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/import/loadrunner', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/import/openapi', operations: ['manage_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/web-api/support-matrix', operations: ['view_scripts'] },
    ],
  },
  {
    title: 'Scenarios And Load Models',
    endpoints: [
      { method: 'GET', path: '/api/v1/scenarios', operations: ['view_tests'] },
      { method: 'POST', path: '/api/v1/scenarios', operations: ['manage_tests'] },
      { method: 'POST', path: '/api/v1/scenarios/validate-load-model', operations: ['manage_tests'] },
      { method: 'GET', path: '/api/v1/scenarios/load-models/support-matrix', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/scenarios/{id}/runtime-settings/report', operations: ['view_tests'] },
    ],
  },
  {
    title: 'Runs And Results',
    endpoints: [
      { method: 'GET', path: '/api/v1/tests', operations: ['view_tests'] },
      { method: 'POST', path: '/api/v1/tests', operations: ['manage_tests'] },
      { method: 'POST', path: '/api/v1/tests/{id}/start', operations: ['manage_tests'] },
      { method: 'POST', path: '/api/v1/tests/{id}/stop', operations: ['manage_tests'] },
      { method: 'PATCH', path: '/api/v1/tests/{id}/vusers', purpose: 'Run, pause, stop, or ramp a specific VUser group.', operations: ['manage_tests'] },
      { method: 'PATCH', path: '/api/v1/tests/{id}/duration', operations: ['manage_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/metrics', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/archive/report', operations: ['view_tests'] },
    ],
  },
  {
    title: 'System And Fleet',
    endpoints: [
      { method: 'GET', path: '/api/v1/system/health' },
      { method: 'GET', path: '/api/v1/system/readiness' },
      { method: 'GET', path: '/api/v1/system/inventory', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/fleet/alerts', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/fleet/capacity', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/system/parity-status', operations: ['view_system'] },
    ],
  },
  {
    title: 'AI And Governance',
    endpoints: [
      { method: 'GET', path: '/api/v1/ai/authoring/contract', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/ai/policy', operations: ['view_system', 'view_tests'] },
      { method: 'POST', path: '/api/v1/ai/drafts', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/ai/repairs', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/archives/retention/plan', operations: ['manage_system'] },
      { method: 'POST', path: '/api/v1/archives/retention/apply', operations: ['manage_system'] },
    ],
  },
]

const statusRows = [
  ['200', 'Request succeeded.'],
  ['201', 'Resource created.'],
  ['400', 'Invalid request body or query parameter.'],
  ['401', 'Authentication required or token invalid.'],
  ['403', 'Authenticated but blocked by RBAC or service-account scope.'],
  ['404', 'Resource not found.'],
  ['409', 'Conflict, such as a schedule or governance conflict.'],
]

type ApiGuidePageProps = {
  onNavigate?: (page: 'access') => void
  effectiveAccessResources?: DashboardAuthEffectiveAccessResource[]
}

export default function ApiGuidePage({ onNavigate, effectiveAccessResources = [] }: ApiGuidePageProps) {
  const allowedOperations = buildAllowedOperationSet(effectiveAccessResources)
  const visibleQuickStartCommands = quickStartCommands.filter(command => canShowEndpoint(command, allowedOperations))
  const visibleAuthRows = authRows.filter(row => canShowEndpoint(row, allowedOperations))
  const visibleEndpointGroups = endpointGroups
    .map(group => ({
      ...group,
      endpoints: group.endpoints.filter(endpoint => canShowEndpoint(endpoint, allowedOperations)),
    }))
    .filter(group => group.endpoints.length > 0)
  const canUseAccessWorkspace = ['manage_users', 'view_user_activity', 'manage_service_accounts'].some(operation => allowedOperations.has(operation))
  const canViewFullRepositoryGuide = allowedOperations.has('manage_system') && allowedOperations.has('view_audit')

  return (
    <div className="page api-guide-page">
      <div className="page-header">
        <div>
          <h2>API Guide</h2>
          <p className="page-subtitle">REST and WebSocket reference for automating APEX Load from CI/CD, integrations, and operator tooling.</p>
        </div>
        {canViewFullRepositoryGuide && (
          <a className="btn btn-secondary btn-sm" href="https://github.com/jasonczech/apexload/blob/main/docs/API_USER_GUIDE.md" target="_blank" rel="noreferrer">
            Repository Guide
          </a>
        )}
      </div>

      <section className="api-guide-hero" aria-labelledby="api-guide-base-url">
        <div>
          <span className="api-guide-kicker">Base URL</span>
          <h3 id="api-guide-base-url">http://&lt;controller-host&gt;:8080/api/v1</h3>
          <p>Local dashboard development proxies `/api` from `localhost:3000`; direct clients should call the controller origin.</p>
        </div>
        <div className="api-guide-meta-grid">
          <div><span>Auth</span><strong>Dev, OIDC/trusted headers, local users, service accounts</strong></div>
          <div><span>Payloads</span><strong>JSON plus CSV, HTML, ZIP, and archive downloads</strong></div>
          <div><span>Streams</span><strong>WebSocket at /api/v1/ws</strong></div>
          <div><span>MCP</span><strong>FastMCP bridge for agent and CI automation</strong></div>
        </div>
      </section>

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

      {canUseAccessWorkspace && (
        <section className="api-guide-callout api-guide-key-callout">
          <div>
            <h3>Access Workspace</h3>
            <p>Use Access to onboard human users, assign global/domain/project roles, review activity by user and scope, and generate service-account keys for MCP, CI/CD, reporting exports, and controlled external automation.</p>
          </div>
          {onNavigate && (
            <button className="btn btn-secondary btn-sm" type="button" onClick={() => onNavigate('access')}>
              Open Access
            </button>
          )}
        </section>
      )}

      <section className="page-section">
        <div className="api-guide-section-header">
          <h3>Quick Start</h3>
          <p>Use a service account key for automation and keep project/application/release ids in your pipeline context.</p>
        </div>
        <div className="api-guide-code-stack">
          {visibleQuickStartCommands.map(({ command }) => (
            <pre key={command}><code>{command}</code></pre>
          ))}
        </div>
      </section>

      <section className="page-section">
        <div className="api-guide-section-header">
          <h3>Authentication</h3>
          <p>OIDC or trusted headers prove human identity, but an active local APEX Load user record is required after bootstrap. Human roles come from local bindings; service accounts can use `Authorization: Bearer &lt;api-key&gt;` or `X-APEXLoad-API-Key`, with RBAC roles and token scopes both enforced.</p>
        </div>
        <div className="api-guide-table-card">
          <table>
            <thead>
              <tr><th>Method</th><th>Endpoint</th><th>Use</th></tr>
            </thead>
            <tbody>
              {visibleAuthRows.map(({ method, path, purpose }) => (
                <tr key={`${method}-${path}`}>
                  <td><code>{method}</code></td>
                  <td><code>{path}</code></td>
                  <td>{purpose}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="page-section">
        <div className="api-guide-section-header">
          <h3>Endpoint Map</h3>
          <p>{canViewFullRepositoryGuide ? <>The repository guide at <code>docs/API_USER_GUIDE.md</code> includes the longer workflow notes and expanded catalogue.</> : 'This endpoint map is filtered to your current domain, project, and role access.'}</p>
        </div>
        <div className="api-guide-endpoint-grid">
          {visibleEndpointGroups.map(group => (
            <article className="api-guide-card" key={group.title}>
              <h4>{group.title}</h4>
              <ul>
                {group.endpoints.map(endpoint => (
                  <li key={`${endpoint.method}-${endpoint.path}`}><code>{formatEndpoint(endpoint)}</code></li>
                ))}
              </ul>
            </article>
          ))}
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
    </div>
  )
}

function buildAllowedOperationSet(resources: DashboardAuthEffectiveAccessResource[]): Set<string> {
  const allowed = new Set<string>()
  resources.forEach(resource => {
    resource.operations.forEach(operation => {
      if (operation.allowed) {
        allowed.add(operation.id)
      }
    })
  })
  return allowed
}

function canShowEndpoint(item: { operations?: string[] }, allowedOperations: Set<string>): boolean {
  if (!item.operations || item.operations.length === 0) {
    return true
  }
  return item.operations.some(operation => allowedOperations.has(operation))
}

function formatEndpoint(endpoint: EndpointDoc): string {
  return `${endpoint.method} ${endpoint.path}`
}
