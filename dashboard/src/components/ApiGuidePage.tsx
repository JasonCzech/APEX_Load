const quickStartCommands = [
  'curl http://localhost:8080/api/v1/system/health',
  'curl -H "Authorization: Bearer $APEXLOAD_API_KEY" http://localhost:8080/api/v1/projects',
  'curl -X POST -H "Authorization: Bearer $APEXLOAD_API_KEY" -H "Content-Type: application/json" -d @script.json http://localhost:8080/api/v1/scripts',
]

const mcpResources = [
  'apexload://api-guide',
  'apexload://auth/access-matrix',
  'apexload://system/parity-status',
  'apexload://runtime-settings/support-matrix',
  'apexload://protocol-bundles/support-matrix',
]

const authRows = [
  ['GET', '/api/v1/auth/me', 'Current auth mode, user, roles, and session metadata.'],
  ['GET', '/api/v1/auth/effective-access', 'Operations allowed for the current principal.'],
  ['GET', '/api/v1/auth/access-matrix', 'Role-to-operation catalog.'],
  ['GET', '/api/v1/auth/audit-events', 'Filtered governance audit feed.'],
  ['GET', '/api/v1/auth/audit-events/export', 'CSV audit export.'],
]

const endpointGroups = [
  {
    title: 'Domain, Repository, Projects, Releases',
    endpoints: [
      'GET /api/v1/domains',
      'GET /api/v1/domains/{id}/application-repository/categories',
      'GET /api/v1/projects',
      'POST /api/v1/projects',
      'GET /api/v1/projects/{id}/releases',
      'GET /api/v1/applications?project_id=<id>',
      'POST /api/v1/applications/{id}/releases',
      'GET /api/v1/releases/{id}/trend/export',
    ],
  },
  {
    title: 'Scripts And Imports',
    endpoints: [
      'GET /api/v1/scripts',
      'POST /api/v1/scripts',
      'POST /api/v1/scripts/validate',
      'POST /api/v1/scripts/import/loadrunner',
      'POST /api/v1/scripts/import/openapi',
      'GET /api/v1/scripts/web-api/support-matrix',
    ],
  },
  {
    title: 'Scenarios And Load Models',
    endpoints: [
      'GET /api/v1/scenarios',
      'POST /api/v1/scenarios',
      'POST /api/v1/scenarios/validate-load-model',
      'GET /api/v1/scenarios/load-models/support-matrix',
      'GET /api/v1/scenarios/{id}/runtime-settings/report',
    ],
  },
  {
    title: 'Runs And Results',
    endpoints: [
      'GET /api/v1/tests',
      'POST /api/v1/tests',
      'POST /api/v1/tests/{id}/start',
      'POST /api/v1/tests/{id}/stop',
      'GET /api/v1/tests/{id}/metrics',
      'GET /api/v1/tests/{id}/archive/report',
    ],
  },
  {
    title: 'System And Fleet',
    endpoints: [
      'GET /api/v1/system/health',
      'GET /api/v1/system/readiness',
      'GET /api/v1/system/inventory',
      'GET /api/v1/fleet/alerts',
      'GET /api/v1/fleet/capacity',
      'GET /api/v1/system/parity-status',
    ],
  },
  {
    title: 'AI And Governance',
    endpoints: [
      'GET /api/v1/ai/authoring/contract',
      'GET /api/v1/ai/policy',
      'POST /api/v1/ai/drafts',
      'POST /api/v1/ai/repairs',
      'POST /api/v1/archives/retention/plan',
      'POST /api/v1/archives/retention/apply',
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
}

export default function ApiGuidePage({ onNavigate }: ApiGuidePageProps) {
  return (
    <div className="page api-guide-page">
      <div className="page-header">
        <div>
          <h2>API Guide</h2>
          <p className="page-subtitle">REST and WebSocket reference for automating APEX Load from CI/CD, integrations, and operator tooling.</p>
        </div>
        <a className="btn btn-secondary btn-sm" href="https://github.com/jasonczech/apexload/blob/main/docs/API_USER_GUIDE.md" target="_blank" rel="noreferrer">
          Repository Guide
        </a>
      </div>

      <section className="api-guide-hero" aria-labelledby="api-guide-base-url">
        <div>
          <span className="api-guide-kicker">Base URL</span>
          <h3 id="api-guide-base-url">http://&lt;controller-host&gt;:8080/api/v1</h3>
          <p>Local dashboard development proxies `/api` from `localhost:3000`; direct clients should call the controller origin.</p>
        </div>
        <div className="api-guide-meta-grid">
          <div><span>Auth</span><strong>Dev, trusted headers, OIDC, service accounts</strong></div>
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

      <section className="api-guide-callout api-guide-key-callout">
        <div>
          <h3>API Keys</h3>
          <p>Generate service-account keys from Access & API Keys for MCP, CI/CD, reporting exports, and controlled external automation. Keys are returned once, then only hashed server-side.</p>
        </div>
        {onNavigate && (
          <button className="btn btn-secondary btn-sm" type="button" onClick={() => onNavigate('access')}>
            Open Access & API Keys
          </button>
        )}
      </section>

      <section className="page-section">
        <div className="api-guide-section-header">
          <h3>Quick Start</h3>
          <p>Use a service account key for automation and keep project/application/release ids in your pipeline context.</p>
        </div>
        <div className="api-guide-code-stack">
          {quickStartCommands.map(command => (
            <pre key={command}><code>{command}</code></pre>
          ))}
        </div>
      </section>

      <section className="page-section">
        <div className="api-guide-section-header">
          <h3>Authentication</h3>
          <p>Service accounts can use `Authorization: Bearer &lt;api-key&gt;` or `X-APEXLoad-API-Key`; RBAC roles and token scopes are both enforced.</p>
        </div>
        <div className="api-guide-table-card">
          <table>
            <thead>
              <tr><th>Method</th><th>Endpoint</th><th>Use</th></tr>
            </thead>
            <tbody>
              {authRows.map(([method, path, purpose]) => (
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
          <p>The repository guide at <code>docs/API_USER_GUIDE.md</code> includes the longer workflow notes and expanded catalogue.</p>
        </div>
        <div className="api-guide-endpoint-grid">
          {endpointGroups.map(group => (
            <article className="api-guide-card" key={group.title}>
              <h4>{group.title}</h4>
              <ul>
                {group.endpoints.map(endpoint => (
                  <li key={endpoint}><code>{endpoint}</code></li>
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
