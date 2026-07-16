import type { ApiEndpointCatalogRoute } from './apiEndpointCatalog'
import type { DashboardAuthEffectiveAccessResource } from './types'

export type DocsVisibilityClassification = {
  classified: boolean
  operations: string[]
}

const READ_METHODS = new Set(['GET', 'HEAD'])

function unique(operations: string[]) {
  return Array.from(new Set(operations))
}

function classification(operations: string[]): DocsVisibilityClassification {
  return { classified: true, operations: unique(operations) }
}

function readWrite(method: string, readOperations: string[], writeOperations: string[]) {
  return classification(READ_METHODS.has(method.toUpperCase()) ? readOperations : writeOperations)
}

function pathSegment(path: string) {
  if (path === '/metrics') return 'metrics'
  return path.replace(/^\/api\/v1\/?/, '').split('/')[0] || ''
}

function includesAny(path: string, fragments: string[]) {
  return fragments.some(fragment => path.includes(fragment))
}

function classifyAuth(route: ApiEndpointCatalogRoute) {
  const { method, path } = route
  if ([
    '/api/v1/auth/login',
    '/api/v1/auth/callback',
    '/api/v1/auth/logout',
    '/api/v1/auth/me',
    '/api/v1/auth/access-matrix',
    '/api/v1/auth/roles',
    '/api/v1/auth/policy',
    '/api/v1/auth/effective-access',
    '/api/v1/auth/ci/github/exchange',
  ].includes(path)) {
    return classification([])
  }
  if (path === '/api/v1/auth/diagnostics') return classification(['view_auth_diagnostics'])
  if (path === '/api/v1/auth/effective-access/principal') return classification(['view_audit', 'manage_user_bindings', 'manage_users'])
  if (path.startsWith('/api/v1/auth/audit-events')) return classification(['view_audit'])
  if (path === '/api/v1/auth/access-review') return classification(['view_audit'])
  if (path.startsWith('/api/v1/auth/user-activity')) return classification(['view_user_activity'])
  if (path.startsWith('/api/v1/auth/service-accounts')) return classification(['manage_service_accounts'])
  if (path === '/api/v1/auth/users') {
    return method === 'GET' ? classification(['manage_users', 'manage_user_bindings']) : classification(['manage_users'])
  }
  if (path === '/api/v1/auth/users/onboard') return classification(['manage_users'])
  if (path.includes('/bindings')) return classification(['manage_user_bindings'])
  if (path.startsWith('/api/v1/auth/users/')) {
    return method === 'GET' ? classification(['manage_users', 'manage_user_bindings']) : classification(['manage_users'])
  }
  return { classified: false, operations: [] }
}

function classifyWorkspace(route: ApiEndpointCatalogRoute) {
  const { method, path } = route
  if (path.includes('/application-repository') || path.startsWith('/api/v1/application-repository/')) {
    return readWrite(method, ['view_application_repository'], ['manage_application_repository'])
  }
  if (path.startsWith('/api/v1/domains')) {
    return readWrite(method, ['view_domains'], ['manage_domains'])
  }
  if (path.startsWith('/api/v1/projects/') && path.endsWith('/github/policy')) {
    return method === 'GET' ? classification(['view_projects', 'view_scripts']) : classification(['manage_projects'])
  }
  if (path.includes('/releases')) {
    return method === 'GET' ? classification(['view_projects']) : classification(['manage_repository_releases'])
  }
  if (path.startsWith('/api/v1/projects')) {
    if (method === 'POST' && path === '/api/v1/projects') return classification(['manage_repository_projects'])
    return readWrite(method, ['view_projects'], ['manage_projects'])
  }
  if (path.startsWith('/api/v1/folders')) return classification(['manage_projects'])
  if (path.startsWith('/api/v1/applications')) {
    return readWrite(method, ['view_applications'], ['manage_applications'])
  }
  if (path.startsWith('/api/v1/releases')) {
    return readWrite(method, ['view_applications'], ['manage_applications', 'manage_repository_releases'])
  }
  return { classified: false, operations: [] }
}

function classifyScriptAuthoring(route: ApiEndpointCatalogRoute) {
  const { method, path } = route
  if (path.includes('/download') || path.includes('/export') || path.endsWith('/support-matrix') || path.includes('/runtime-settings/report')) {
    return classification(['view_scripts'])
  }
  if (path.includes('/validate') && method === 'POST') return classification(['view_scripts'])
  if (path.includes('/authoring/capabilities') || path.includes('/authoring/templates') || path.includes('/authoring/components')) {
    return classification(['view_scripts'])
  }
  return readWrite(method, ['view_scripts'], ['manage_scripts'])
}

function classifyRecording(route: ApiEndpointCatalogRoute) {
  const { method, path } = route
  if (path.startsWith('/api/v1/browser-recorder/install')) {
    return method === 'GET' ? classification(['view_replay']) : classification(['manage_replay'])
  }
  if (path.startsWith('/api/v1/browser-recorder/devices')) return classification(['view_replay'])
  if (path.includes('/export') || path.includes('/storage-state') || path.includes('/transactions/suggestions') || path.endsWith('/status') || path.endsWith('/ca.pem')) {
    return classification(['view_replay'])
  }
  if (path.startsWith('/api/v1/replay') || path.startsWith('/api/v1/script-debug')) {
    return readWrite(method, ['view_replay', 'view_scripts'], ['manage_replay', 'manage_scripts'])
  }
  return readWrite(method, ['view_replay'], ['manage_replay'])
}

function classifyTestExecution(route: ApiEndpointCatalogRoute) {
  const { method, path } = route
  if (path.includes('/data-pools')) {
    return method === 'GET'
      ? classification(['view_tests', 'view_data_pools'])
      : classification(['manage_tests', 'manage_data_pools'])
  }
  if (path.includes('/archive') || path.includes('/metrics') || path.includes('/report') || path.includes('/results') || path.includes('/sla-status') || path.includes('/manifest') || path.includes('/notes')) {
    return method === 'GET' ? classification(['view_tests']) : classification(['manage_tests'])
  }
  if (path.includes('/support-matrix') || path.includes('/templates')) return classification(['view_tests'])
  return readWrite(method, ['view_tests'], ['manage_tests'])
}

function classifySystem(route: ApiEndpointCatalogRoute) {
  const { method, path } = route
  if ([
    '/api/v1/system/health',
    '/api/v1/system/readiness',
    '/api/v1/system/version',
    '/metrics',
  ].includes(path)) {
    return classification([])
  }
  if (path === '/api/v1/runtime-settings/support-matrix') return classification(['view_system', 'view_scripts', 'view_tests'])
  if (path.startsWith('/api/v1/downloads')) return classification(['view_system'])
  if (path.includes('/settings') || path.includes('/snapshots') || path.includes('/generators/drain') || path.includes('/restarts')) {
    return readWrite(method, ['view_system'], ['manage_system'])
  }
  return readWrite(method, ['view_system'], ['manage_system'])
}

function classifyIntegrations(route: ApiEndpointCatalogRoute) {
  const { method, path } = route
  if (path === '/api/v1/integrations/github/webhook') return classification([])
  if (path.includes('/github/config') || path.includes('/github/diagnostics')) return classification(['view_scripts', 'view_system'])
  if (path.includes('/github/installations') || path.includes('/github/webhooks')) return classification(['view_system'])
  if (path.includes('/github/check-links')) return classification(['view_tests', 'view_system'])
  if (path.includes('/github/login') || path.includes('/github/callback')) return classification(['manage_scripts'])
  if (path.includes('/github/connection')) return method === 'GET' ? classification(['view_scripts']) : classification(['manage_scripts'])
  if (includesAny(path, ['/github/repos', '/github/branches', '/github/contents', '/github/sync-jobs'])) {
    return readWrite(method, ['view_scripts'], ['manage_scripts'])
  }
  if (path.includes('/dynatrace/dry-run')) return classification(['view_system'])
  return classification(['view_system'])
}

function classifyLibrary(route: ApiEndpointCatalogRoute) {
  const { method, path } = route
  if (path.endsWith('/capabilities')) return classification(['view_system', 'view_scripts', 'view_tests'])
  if (path.endsWith('/catalog') || path.includes('/diff')) return classification(['view_scripts', 'view_tests'])
  if (path.includes('/results/')) return classification(['view_tests'])
  if (path === '/api/v1/library/remotes') return readWrite(method, ['view_system'], ['manage_system'])
  if (path.includes('/probe')) return classification(['manage_system'])
  if (includesAny(path, ['/pull', '/push', '/import'])) return classification(['manage_scripts', 'manage_tests'])
  if (path.includes('/export')) return classification(['view_scripts', 'view_tests'])
  return readWrite(method, ['view_system'], ['manage_system'])
}

export function classifyEndpointForDocs(route: ApiEndpointCatalogRoute): DocsVisibilityClassification {
  const segment = pathSegment(route.path)

  if (segment === 'auth') return classifyAuth(route)
  if (route.public) return classification([])

  switch (segment) {
    case 'ai':
      return route.method === 'GET' ? classification(['view_system', 'view_tests']) : classification(['manage_scripts'])
    case 'application-repository':
    case 'applications':
    case 'domains':
    case 'folders':
    case 'projects':
    case 'releases':
      return classifyWorkspace(route)
    case 'archives':
      return classification(['manage_system'])
    case 'browser-recorder':
    case 'browser-replay':
    case 'recorder':
    case 'replay':
    case 'script-debug':
      return classifyRecording(route)
    case 'capacity':
    case 'ci':
    case 'execution-plans':
    case 'run-calendar':
    case 'scenarios':
    case 'schedules':
    case 'tests':
      return classifyTestExecution(route)
    case 'correlation-rulesets':
    case 'correlations':
    case 'dsl':
    case 'object-repositories':
    case 'scripts':
    case 'selectors':
      return classifyScriptAuthoring(route)
    case 'data-pools':
      return readWrite(route.method, ['view_data_pools'], ['manage_data_pools'])
    case 'desktop':
    case 'docs':
    case 'downloads':
    case 'protocol-bundles':
    case 'protocols':
    case 'runtime-settings':
    case 'system':
      return classifySystem(route)
    case 'fleet':
    case 'metrics':
    case 'monitoring':
    case 'reports':
    case 'ws':
      return readWrite(route.method, ['view_system'], ['manage_system'])
    case 'integrations':
      return classifyIntegrations(route)
    case 'library':
      return classifyLibrary(route)
    case 'remotes':
      return readWrite(route.method, ['view_tests'], ['manage_tests'])
    case 'results':
      return readWrite(route.method, ['view_tests', 'view_system'], ['manage_tests'])
    default:
      return { classified: false, operations: [] }
  }
}

export function docsVisibilityOperationsForEndpoint(route: ApiEndpointCatalogRoute) {
  return classifyEndpointForDocs(route).operations
}

export function buildAllowedOperationSet(resources: DashboardAuthEffectiveAccessResource[]): Set<string> {
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

export function canShowEndpointForDocs(route: ApiEndpointCatalogRoute, allowedOperations: Set<string>): boolean {
  const classification = classifyEndpointForDocs(route)
  if (!classification.classified) return false
  if (classification.operations.length === 0) return true
  return classification.operations.some(operation => allowedOperations.has(operation))
}
