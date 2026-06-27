export type ApiEndpointCatalogRoute = {
  method: string
  path: string
  operationId: string
  summary: string
  category: string
  public: boolean
  authenticated: boolean
  access: string
}

export type ApiEndpointCatalog = {
  version: number
  source: string
  routes: ApiEndpointCatalogRoute[]
}

export function parseEndpointCatalog(source: string): ApiEndpointCatalog {
  try {
    const parsed = JSON.parse(source) as Partial<ApiEndpointCatalog>
    return {
      version: typeof parsed.version === 'number' ? parsed.version : 0,
      source: typeof parsed.source === 'string' ? parsed.source : '',
      routes: Array.isArray(parsed.routes)
        ? parsed.routes.filter(isCatalogRoute)
        : [],
    }
  } catch {
    return { version: 0, source: '', routes: [] }
  }
}

function isCatalogRoute(value: unknown): value is ApiEndpointCatalogRoute {
  if (!value || typeof value !== 'object') return false
  const route = value as Record<string, unknown>
  return typeof route.method === 'string'
    && typeof route.path === 'string'
    && typeof route.operationId === 'string'
    && typeof route.summary === 'string'
    && typeof route.category === 'string'
    && typeof route.public === 'boolean'
    && typeof route.authenticated === 'boolean'
    && typeof route.access === 'string'
}

export function endpointCatalogKey(method: string, path: string) {
  return `${method.toUpperCase()} ${path}`
}

export function buildEndpointCatalogMap(routes: ApiEndpointCatalogRoute[]) {
  return new Map(routes.map(route => [endpointCatalogKey(route.method, route.path), route]))
}
