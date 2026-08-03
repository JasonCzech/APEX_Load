import type { ApplicationRepository, RepositoryCategory } from './types'

export interface RepositoryCatalogState {
  repositoriesByDomain: Partial<Record<string, ApplicationRepository>>
  categoriesByDomain: Partial<Record<string, RepositoryCategory[]>>
}

export interface RepositoryCatalogRefreshPatch {
  repository?: ApplicationRepository
  categories?: RepositoryCategory[]
}

export interface RepositoryCategoryMutationLedger {
  committedByID: Map<string, RepositoryCategory>
  deletedIDs: Set<string>
}

export function createRepositoryCatalogState(): RepositoryCatalogState {
  return {
    repositoriesByDomain: {},
    categoriesByDomain: {},
  }
}

export function createRepositoryCategoryMutationLedger(): RepositoryCategoryMutationLedger {
  return {
    committedByID: new Map(),
    deletedIDs: new Set(),
  }
}

function categorySort(left: RepositoryCategory, right: RepositoryCategory): number {
  const order = (left.sort_order || 0) - (right.sort_order || 0)
  return order || left.name.localeCompare(right.name) || left.id.localeCompare(right.id)
}

function sameCategory(left: RepositoryCategory, right: RepositoryCategory): boolean {
  return left.id === right.id
    && left.domain_id === right.domain_id
    && left.repository_id === right.repository_id
    && left.name === right.name
    && (left.description || '') === (right.description || '')
    && (left.sort_order || 0) === (right.sort_order || 0)
}

function remoteCategoryIncludesCommit(remote: RepositoryCategory, committed: RepositoryCategory): boolean {
  if (sameCategory(remote, committed)) return true
  const remoteUpdatedAt = Date.parse(remote.updated_at)
  const committedUpdatedAt = Date.parse(committed.updated_at)
  return Number.isFinite(remoteUpdatedAt)
    && Number.isFinite(committedUpdatedAt)
    && remoteUpdatedAt >= committedUpdatedAt
}

/**
 * Mutation responses are authoritative until a catalog read demonstrably
 * includes them. Deletion tombstones intentionally live for the session: IDs
 * are immutable, and keeping the tombstone prevents a later stale replica read
 * from resurrecting a deleted category.
 */
export function reconcileRepositoryCategorySnapshot(
  domainID: string,
  remoteCategories: RepositoryCategory[],
  ledger: RepositoryCategoryMutationLedger,
): { categories: RepositoryCategory[]; acknowledgedCommittedIDs: string[] } {
  const reconciled = new Map<string, RepositoryCategory>()
  const acknowledgedCommittedIDs: string[] = []

  for (const category of remoteCategories) {
    if (category.domain_id !== domainID || ledger.deletedIDs.has(category.id)) continue
    reconciled.set(category.id, category)
  }

  for (const category of ledger.committedByID.values()) {
    if (category.domain_id !== domainID || ledger.deletedIDs.has(category.id)) continue
    const remote = reconciled.get(category.id)
    if (remote && remoteCategoryIncludesCommit(remote, category)) {
      acknowledgedCommittedIDs.push(category.id)
      continue
    }
    reconciled.set(category.id, category)
  }

  return {
    categories: [...reconciled.values()].sort(categorySort),
    acknowledgedCommittedIDs,
  }
}

export function recordCommittedRepositoryCategory(
  ledger: RepositoryCategoryMutationLedger,
  category: RepositoryCategory,
): void {
  ledger.deletedIDs.delete(category.id)
  ledger.committedByID.set(category.id, category)
}

export function recordDeletedRepositoryCategory(
  ledger: RepositoryCategoryMutationLedger,
  categoryID: string,
): void {
  ledger.committedByID.delete(categoryID)
  ledger.deletedIDs.add(categoryID)
}

/** Undefined patch members represent failed independent reads and preserve the
 * corresponding last-known-good value. */
export function mergeRepositoryCatalogRefresh(
  previous: RepositoryCatalogState,
  domainID: string,
  patch: RepositoryCatalogRefreshPatch,
): RepositoryCatalogState {
  const repository = patch.repository?.domain_id === domainID ? patch.repository : undefined
  const categories = patch.categories?.filter(category => category.domain_id === domainID)
  if (!repository && categories === undefined) return previous

  return {
    repositoriesByDomain: repository
      ? { ...previous.repositoriesByDomain, [domainID]: repository }
      : previous.repositoriesByDomain,
    categoriesByDomain: categories !== undefined
      ? { ...previous.categoriesByDomain, [domainID]: [...categories].sort(categorySort) }
      : previous.categoriesByDomain,
  }
}

export function repositoryCatalogForDomain(
  state: RepositoryCatalogState,
  domainID?: string,
): { repository: ApplicationRepository | null; categories: RepositoryCategory[] } {
  if (!domainID) return { repository: null, categories: [] }
  const repository = state.repositoriesByDomain[domainID]
  return {
    repository: repository?.domain_id === domainID ? repository : null,
    categories: (state.categoriesByDomain[domainID] || []).filter(category => category.domain_id === domainID),
  }
}
