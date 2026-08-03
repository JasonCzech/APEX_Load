import { describe, expect, it } from 'vitest'

import type { ApplicationRepository, RepositoryCategory } from './types'
import {
  createRepositoryCatalogState,
  createRepositoryCategoryMutationLedger,
  mergeRepositoryCatalogRefresh,
  reconcileRepositoryCategorySnapshot,
  recordCommittedRepositoryCategory,
  recordDeletedRepositoryCategory,
  repositoryCatalogForDomain,
} from './repositoryCatalogState'

const repositoryA: ApplicationRepository = {
  id: 'repository-a',
  domain_id: 'domain-a',
  name: 'Repository A',
  created_at: '2026-08-03T10:00:00Z',
  updated_at: '2026-08-03T10:00:00Z',
}

const repositoryB: ApplicationRepository = {
  ...repositoryA,
  id: 'repository-b',
  domain_id: 'domain-b',
  name: 'Repository B',
}

function category(
  id: string,
  domainID = 'domain-a',
  repositoryID = 'repository-a',
  name = id,
): RepositoryCategory {
  return {
    id,
    domain_id: domainID,
    repository_id: repositoryID,
    name,
    created_at: '2026-08-03T10:00:00Z',
    updated_at: '2026-08-03T10:00:00Z',
  }
}

describe('repository catalog refresh state', () => {
  it('retains each last-known-good half when the paired request fails', () => {
    const originalCategory = category('category-original')
    const refreshedCategory = category('category-refreshed')
    const original = mergeRepositoryCatalogRefresh(
      createRepositoryCatalogState(),
      'domain-a',
      { repository: repositoryA, categories: [originalCategory] },
    )

    const categoryOnlyRefresh = mergeRepositoryCatalogRefresh(original, 'domain-a', {
      categories: [refreshedCategory],
    })
    expect(repositoryCatalogForDomain(categoryOnlyRefresh, 'domain-a')).toEqual({
      repository: repositoryA,
      categories: [refreshedCategory],
    })

    const repositoryOnlyRefresh = mergeRepositoryCatalogRefresh(categoryOnlyRefresh, 'domain-a', {
      repository: { ...repositoryA, name: 'Repository A refreshed' },
    })
    expect(repositoryCatalogForDomain(repositoryOnlyRefresh, 'domain-a')).toEqual({
      repository: { ...repositoryA, name: 'Repository A refreshed' },
      categories: [refreshedCategory],
    })
  })

  it('fences the previous domain while retaining independent per-domain caches', () => {
    const categoryA = category('category-a')
    const categoryB = category('category-b', 'domain-b', 'repository-b')
    const domainAState = mergeRepositoryCatalogRefresh(
      createRepositoryCatalogState(),
      'domain-a',
      { repository: repositoryA, categories: [categoryA] },
    )

    expect(repositoryCatalogForDomain(domainAState, 'domain-b')).toEqual({
      repository: null,
      categories: [],
    })

    const domainBPartialState = mergeRepositoryCatalogRefresh(domainAState, 'domain-b', {
      categories: [categoryB],
    })
    expect(repositoryCatalogForDomain(domainBPartialState, 'domain-b')).toEqual({
      repository: null,
      categories: [categoryB],
    })
    expect(repositoryCatalogForDomain(domainBPartialState, 'domain-a')).toEqual({
      repository: repositoryA,
      categories: [categoryA],
    })

    const completeState = mergeRepositoryCatalogRefresh(domainBPartialState, 'domain-b', {
      repository: repositoryB,
    })
    expect(repositoryCatalogForDomain(completeState, 'domain-b').repository).toBe(repositoryB)
  })

  it('keeps a committed create across a stale category read and a paired repository failure', () => {
    const ledger = createRepositoryCategoryMutationLedger()
    const existing = category('category-existing')
    const created = category('category-created', 'domain-a', 'repository-a', 'Created Category')
    recordCommittedRepositoryCategory(ledger, created)

    const reconciled = reconcileRepositoryCategorySnapshot('domain-a', [existing], ledger)
    const state = mergeRepositoryCatalogRefresh(
      mergeRepositoryCatalogRefresh(createRepositoryCatalogState(), 'domain-a', {
        repository: repositoryA,
        categories: [existing, created],
      }),
      'domain-a',
      // A missing repository member models the failed paired GET.
      { categories: reconciled.categories },
    )

    expect(repositoryCatalogForDomain(state, 'domain-a')).toEqual({
      repository: repositoryA,
      categories: [existing, created],
    })
    expect(reconciled.acknowledgedCommittedIDs).toEqual([])
  })

  it('does not resurrect a committed delete from a stale read or erase it when categories fail', () => {
    const ledger = createRepositoryCategoryMutationLedger()
    const kept = category('category-kept')
    const deleted = category('category-deleted')
    recordDeletedRepositoryCategory(ledger, deleted.id)

    const reconciled = reconcileRepositoryCategorySnapshot('domain-a', [deleted, kept], ledger)
    expect(reconciled.categories).toEqual([kept])

    const afterDelete = mergeRepositoryCatalogRefresh(
      createRepositoryCatalogState(),
      'domain-a',
      { repository: repositoryA, categories: reconciled.categories },
    )
    const afterCategoryFailure = mergeRepositoryCatalogRefresh(afterDelete, 'domain-a', {
      repository: { ...repositoryA, name: 'Repository A refreshed' },
    })
    expect(repositoryCatalogForDomain(afterCategoryFailure, 'domain-a').categories).toEqual([kept])

    const laterStaleRead = reconcileRepositoryCategorySnapshot('domain-a', [deleted, kept], ledger)
    expect(laterStaleRead.categories).toEqual([kept])
  })

  it('retires a committed overlay once a current read includes it', () => {
    const ledger = createRepositoryCategoryMutationLedger()
    const created = category('category-created')
    recordCommittedRepositoryCategory(ledger, created)

    const reconciled = reconcileRepositoryCategorySnapshot('domain-a', [created], ledger)
    expect(reconciled.categories).toEqual([created])
    expect(reconciled.acknowledgedCommittedIDs).toEqual([created.id])
  })
})
