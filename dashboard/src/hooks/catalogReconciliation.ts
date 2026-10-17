/** A successful mutation stays authoritative until a list acknowledges it. */
export function catalogSnapshotCaughtUp<Row>(
  listed: Row,
  committed: Row,
  rowsMatch: (left: Row, right: Row) => boolean,
  updatedAt: (row: Row) => string,
): boolean {
  if (rowsMatch(listed, committed)) return true
  const listedRevision = Date.parse(updatedAt(listed))
  const committedRevision = Date.parse(updatedAt(committed))
  return Number.isFinite(listedRevision)
    && Number.isFinite(committedRevision)
    && listedRevision > committedRevision
}

export function reconcileCommittedCatalog<Row>(
  listed: readonly Row[],
  committedById: ReadonlyMap<string, Row>,
  policy: {
    identity: (row: Row) => string
    belongsToScope: (row: Row) => boolean
    acknowledged: (listed: Row, committed: Row) => boolean
    tombstones: ReadonlySet<string>
  },
): { rows: Row[]; committedById: Map<string, Row> } {
  const pending = new Map(committedById)
  const listedIds = new Set<string>()
  const visible = (row: Row) => policy.belongsToScope(row) && !policy.tombstones.has(policy.identity(row))
  const rows = listed.filter(visible).map(row => {
    const id = policy.identity(row)
    listedIds.add(id)
    const committed = pending.get(id)
    if (!committed || !visible(committed)) return row
    if (policy.acknowledged(row, committed)) {
      pending.delete(id)
      return row
    }
    return committed
  })
  committedById.forEach(committed => {
    if (visible(committed) && !listedIds.has(policy.identity(committed))) rows.push(committed)
  })
  return { rows, committedById: pending }
}
