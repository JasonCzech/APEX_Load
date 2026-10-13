// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from 'vitest'
import { readRepositoryViewState, writeRepositoryViewState } from './repositoryViewState'
import { parseAppViewFromHash, viewToLocationHash } from '../appRouting'
import { repositoryDataPoolHref } from './repositoryAssetNavigation'

describe('repository context and pool navigation', () => {
  beforeEach(() => sessionStorage.clear())
  it('restores filters, explicit All tab, ordering and position independently by scope', () => {
    const state = readRepositoryViewState('a')
    state.filters.query = 'checkout'
    state.page = 2
    state.pageSize = 25
    state.scrollTop = 410
    state.sort = { key: 'name', direction: 'asc' }
    writeRepositoryViewState('a', state)
    expect(readRepositoryViewState('a')).toEqual(state)
    expect(readRepositoryViewState('b')).toMatchObject({ page: 0, filters: { query: '' } })
  })
  it('recovers from corrupt or invalid persisted state', () => {
    sessionStorage.setItem('a', '{')
    expect(readRepositoryViewState('a').pageSize).toBe(10)
    sessionStorage.setItem('a', JSON.stringify({ page: -1, pageSize: 999, scrollTop: -2, tab: 'invalid', filters: { query: 42 }, sort: { key: 'bad' } }))
    expect(readRepositoryViewState('a')).toMatchObject({ page: 0, pageSize: 10, scrollTop: 0, tab: 'all', filters: { query: '' }, sort: { key: 'last_activity' } })
  })
  it('opens the pool in Shared assets in its own project, without another project’s children', () => {
    const origin = '#/repository?projectId=a&applicationId=app&releaseId=rel'
    const route = parseAppViewFromHash(repositoryDataPoolHref('pool', 'b', origin))
    expect(route).toEqual({ page: 'shared-assets', section: 'test-data', resourceId: 'pool', projectId: 'b' })
    expect(parseAppViewFromHash(viewToLocationHash(route))).toEqual(route)
    expect(parseAppViewFromHash(repositoryDataPoolHref('pool', 'a', origin))).toEqual({ page: 'shared-assets', section: 'test-data', resourceId: 'pool', projectId: 'a', applicationId: 'app', releaseId: 'rel' })
  })

})
