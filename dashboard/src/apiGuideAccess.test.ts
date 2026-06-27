import { describe, expect, it } from 'vitest'

import { apiEndpointRoutes, endpointCatalogKey } from './apiEndpointCatalog'
import { classifyEndpointForDocs } from './apiGuideAccess'

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
    expect(examples.get('GET /api/v1/scripts')).toEqual(['view_scripts'])
    expect(examples.get('POST /api/v1/scripts')).toEqual(['manage_scripts'])
    expect(examples.get('GET /api/v1/execution-plans')).toEqual(['view_tests'])
    expect(examples.get('POST /api/v1/auth/users/onboard')).toEqual(['manage_users'])
    expect(examples.get('POST /api/v1/auth/users/{id}/bindings')).toEqual(['manage_user_bindings'])
  })
})
