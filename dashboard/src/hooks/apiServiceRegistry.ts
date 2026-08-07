// The ApiContext value is a single flat object built by spreading every domain
// hook's return value. That makes `api.foo()` cheap at ~400 call sites, but it
// also means a name exported by two domain hooks silently resolves to whichever
// hook is spread last — a bug with no compile error and no runtime symptom
// until the wrong endpoint is called. mergeApiServiceGroups keeps the flat
// shape and adds a collision guard: loud at runtime for any collision outside
// the audited allowlist, and directly assertable from tests.

export type ApiServiceGroup = Record<string, unknown>
export type ApiServiceGroups = Record<string, ApiServiceGroup>

export type ApiServiceCollision = {
  key: string
  /** Domain-hook group names that export `key`, in spread order. The last one wins. */
  groups: string[]
}

export function findApiServiceCollisions(groups: ApiServiceGroups): ApiServiceCollision[] {
  const owners = new Map<string, string[]>()
  for (const [groupName, group] of Object.entries(groups)) {
    if (!group) continue
    for (const key of Object.keys(group)) {
      const existing = owners.get(key)
      if (existing) existing.push(groupName)
      else owners.set(key, [groupName])
    }
  }
  const collisions: ApiServiceCollision[] = []
  owners.forEach((owner, key) => {
    if (owner.length > 1) collisions.push({ key, groups: owner })
  })
  return collisions
}

/**
 * Duplicated service names that have been audited and declared harmless.
 *
 * `useApplicationApi` and `useRepositoryApi` each declare their own
 * byte-identical `archiveRelease`/`deleteRelease` (same path, method and
 * response type), and separate components call each hook directly, so the
 * shadowing has no observable effect. They are listed here — not only in the
 * test — so the runtime reporter stays silent on them: a guard that logs the
 * same two names on every app boot trains developers to ignore it, and a
 * genuinely new collision would then arrive as a superset of a message nobody
 * reads.
 *
 * Anything not on this list is a new silent shadowing. Add the endpoint to
 * exactly one domain hook instead of extending this array.
 */
export const KNOWN_DUPLICATE_SERVICE_NAMES: readonly string[] = ['archiveRelease', 'deleteRelease']

/** Collisions that are not on the audited allowlist — the ones worth reporting. */
export function unexpectedApiServiceCollisions(
  collisions: ApiServiceCollision[],
  allowed: readonly string[] = KNOWN_DUPLICATE_SERVICE_NAMES,
): ApiServiceCollision[] {
  return collisions.filter(({ key }) => !allowed.includes(key))
}

export function describeApiServiceCollisions(collisions: ApiServiceCollision[]): string {
  return collisions
    .map(({ key, groups }) => `${key} (${groups.join(' -> ')}; "${groups[groups.length - 1]}" wins)`)
    .join(', ')
}

const reportedCollisionMessages = new Set<string>()

// The guard warns rather than throws: a shadowed name is a bug worth surfacing,
// but a blank dashboard is worse than one screen calling the wrong hook's copy
// of a method. The hard gate is apiServiceRegistry.test.tsx, which pins the
// exact set of duplicated names so a new collision fails the suite.
export function reportApiServiceCollisions(message: string): void {
  if (reportedCollisionMessages.has(message)) return
  reportedCollisionMessages.add(message)
  console.error(message)
}

// Exposed for tests; the dedupe cache is process-wide by design.
export function resetApiServiceCollisionReports(): void {
  reportedCollisionMessages.clear()
}

type UnionToIntersection<U> = (U extends unknown ? (arg: U) => void : never) extends (
  arg: infer I,
) => void
  ? I
  : never

/**
 * Flattens the domain-hook groups into the single object exposed by ApiContext,
 * reporting any name exported by more than one group that is not on
 * `KNOWN_DUPLICATE_SERVICE_NAMES`.
 */
export function mergeApiServiceGroups<T extends ApiServiceGroups>(
  groups: T,
  onCollision: (message: string) => void = reportApiServiceCollisions,
): UnionToIntersection<T[keyof T]> {
  const collisions = unexpectedApiServiceCollisions(findApiServiceCollisions(groups))
  if (collisions.length > 0) {
    onCollision(
      `useApi: duplicate API service names across domain hooks: ${describeApiServiceCollisions(collisions)}`,
    )
  }
  return Object.assign({}, ...Object.values(groups)) as UnionToIntersection<T[keyof T]>
}
