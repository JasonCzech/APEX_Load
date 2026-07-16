import { describe, expect, it } from 'vitest'
import endpointCatalogRaw from '../../docs/api-endpoint-catalog.json?raw'

import { endpointCatalogKey, parseEndpointCatalog } from './apiEndpointCatalog'
import { canShowEndpointForDocs, classifyEndpointForDocs } from './apiGuideAccess'

const apiEndpointRoutes = parseEndpointCatalog(endpointCatalogRaw).routes

describe('apiGuideAccess docs visibility classification', () => {
  it('classifies every generated catalog route', () => {
    const unclassified = apiEndpointRoutes
      .filter(route => !classifyEndpointForDocs(route).classified)
      .map(route => endpointCatalogKey(route.method, route.path))

    expect(unclassified).toEqual([])
  })

  it('keeps representative access metadata aligned with intended guide filtering', () => {
    const examples = new Map(
      apiEndpointRoutes.map(route => [endpointCatalogKey(route.method, route.path), classifyEndpointForDocs(route).operations]),
    )

    expect(examples.get('GET /api/v1/system/health')).toEqual([])
    expect(examples.get('GET /api/v1/docs/swagger.yaml')).toEqual(['view_system'])
    expect(examples.get('GET /api/v1/scripts')).toEqual(['view_scripts'])
    expect(examples.get('POST /api/v1/scripts')).toEqual(['manage_scripts'])
    expect(examples.get('GET /api/v1/execution-plans')).toEqual(['view_tests'])
    expect(examples.get('POST /api/v1/auth/users/onboard')).toEqual(['manage_users'])
    expect(examples.get('POST /api/v1/auth/users/{id}/bindings')).toEqual(['manage_user_bindings'])
    expect(examples.get('POST /api/v1/auth/ci/github/exchange')).toEqual([])
    expect(examples.get('POST /api/v1/ci/runs')).toEqual(['manage_tests'])
    expect(examples.get('GET /api/v1/ci/runs/{id}')).toEqual(['view_tests'])
    expect(examples.get('POST /api/v1/ci/runs/{id}/abort')).toEqual(['manage_tests'])
  })

  it('fails closed for unclassified routes at runtime', () => {
    expect(canShowEndpointForDocs({
      method: 'GET',
      path: '/api/v1/new-sensitive-surface',
      operationId: 'getNewSensitiveSurface',
      summary: 'New Sensitive Surface',
      category: 'Other',
      public: false,
      authenticated: true,
      access: 'authenticated',
    }, new Set(['manage_system']))).toBe(false)
  })
})
