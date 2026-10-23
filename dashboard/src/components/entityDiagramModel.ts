// Parses the Mermaid `erDiagram` subset that docs use (entities with typed
// attributes, and relationships between entities) and lays it out for the
// EntityDiagram component. Anything outside that subset returns null, and the
// Markdown renderer then shows the source as an ordinary code block.

export type EntityAttribute = { type: string; name: string; keys: string[]; comment?: string }
export type Entity = { name: string; attributes: EntityAttribute[]; hiddenColumns?: number; stub: boolean }
/** `child` holds the ID of `parent`; `label` names the referencing column. */
export type Relationship = { parent: string; child: string; label: string; logical: boolean; optional: boolean; many: boolean }
export type EntityDiagramSpec = { entities: Entity[]; relationships: Relationship[] }

export type DiagramNode = { entity: Entity; x: number; y: number; width: number; height: number }
export type DiagramEdge = { relationship: Relationship; path: string }
export type DiagramLayout = { width: number; height: number; nodes: DiagramNode[]; edges: DiagramEdge[] }

const NAME = /[A-Za-z_][\w-]*/.source
const ENTITY_OPEN = new RegExp(`^(${NAME})\\s*\\{$`)
const ATTRIBUTE = new RegExp(`^(\\S+)\\s+(${NAME})(?:\\s+((?:PK|FK|UK)(?:\\s*,\\s*(?:PK|FK|UK))*))?(?:\\s+"([^"]*)")?$`)
// The parent (the "one" side) comes first; many-side-first lines stay code.
const RELATIONSHIP = new RegExp(`^(${NAME})\\s+(\\|[o|])(--|\\.\\.)([o|][|{])\\s+(${NAME})\\s*:\\s*(?:"([^"]*)"|(\\S+))$`)
const HIDDEN_COLUMNS = /^\+(\d+) columns?$/

export function parseErDiagram(source: string): EntityDiagramSpec | null {
  const lines = source.split('\n').map(line => line.trim()).filter(line => line && !line.startsWith('%%'))
  if (lines[0] !== 'erDiagram') return null
  const entities = new Map<string, Entity>()
  const relationships: Relationship[] = []
  const entity = (name: string) => {
    let found = entities.get(name)
    if (!found) {
      found = { name, attributes: [], stub: true }
      entities.set(name, found)
    }
    return found
  }
  let open: Entity | null = null
  for (const line of lines.slice(1)) {
    if (open) {
      if (line === '}') {
        open = null
        continue
      }
      const match = line.match(ATTRIBUTE)
      if (!match) return null
      const [, type, name, keys, comment] = match
      const hidden = type === 'etc' && name === 'more' ? comment?.match(HIDDEN_COLUMNS) : null
      if (hidden) open.hiddenColumns = Number(hidden[1])
      else open.attributes.push({ type, name, keys: keys ? keys.split(',').map(key => key.trim()) : [], ...(comment ? { comment } : {}) })
      continue
    }
    const opening = line.match(ENTITY_OPEN)
    if (opening) {
      open = entity(opening[1])
      open.stub = false
      continue
    }
    const relation = line.match(RELATIONSHIP)
    if (!relation) return null
    const [, parent, parentCardinality, link, childCardinality, child, quoted, bare] = relation
    entity(parent)
    entity(child)
    relationships.push({
      parent,
      child,
      label: quoted ?? bare,
      logical: link === '..',
      optional: parentCardinality === '|o',
      many: childCardinality.endsWith('{'),
    })
  }
  if (open || entities.size === 0) return null
  // A diagram that declares no entity bodies (an overview) draws every table
  // as a table; stubs only mark tables borrowed from elsewhere.
  const list = [...entities.values()]
  if (list.every(item => item.stub)) list.forEach(item => { item.stub = false })
  return { entities: list, relationships }
}

// Geometry, in SVG user units. Text is monospace, so widths follow from length.
export const DIAGRAM = {
  headerHeight: 26,
  rowHeight: 18,
  paddingX: 10,
  paddingBottom: 6,
  nameCharWidth: 7.3,
  rowCharWidth: 6.7,
  keyWidth: 34,
  stubHeight: 30,
  minWidth: 132,
  gapX: 20,
  gapRank: 46,
  gapWrap: 26,
  passWidth: 4,
  passGap: 8,
  selfLoop: 26,
  margin: 8,
}

export function entitySize(entity: Entity): { width: number; height: number } {
  const header = entity.name.length * DIAGRAM.nameCharWidth + DIAGRAM.paddingX * 2
  if (entity.stub) return { width: Math.max(DIAGRAM.minWidth, Math.ceil(header)), height: DIAGRAM.stubHeight }
  const rows = entity.attributes.map(attribute => {
    const side = attribute.keys.length ? DIAGRAM.keyWidth : attribute.comment ? attribute.comment.length * DIAGRAM.rowCharWidth + 8 : 0
    return attribute.name.length * DIAGRAM.rowCharWidth + side + DIAGRAM.paddingX * 2
  })
  const rowCount = entity.attributes.length + (entity.hiddenColumns ? 1 : 0)
  return {
    width: Math.ceil(Math.max(DIAGRAM.minWidth, header, ...rows)),
    height: DIAGRAM.headerHeight + rowCount * DIAGRAM.rowHeight + (rowCount ? DIAGRAM.paddingBottom : 0),
  }
}

// Ranks put a referenced table above the tables that hold its ID. A table's
// rank is one below its lowest-ranked parent; a table with no parent sits one
// above its highest child, so a lone parent stays next to what it owns.
function rankEntities(spec: EntityDiagramSpec): Map<string, number> {
  const parents = new Map<string, string[]>()
  const children = new Map<string, string[]>()
  for (const { name } of spec.entities) {
    parents.set(name, [])
    children.set(name, [])
  }
  for (const { parent, child } of spec.relationships) {
    if (parent === child) continue
    parents.get(child)!.push(parent)
    children.get(parent)!.push(child)
  }
  const rank = new Map<string, number>()
  const visiting = new Set<string>()
  const visit = (name: string): number => {
    const known = rank.get(name)
    if (known !== undefined) return known
    if (visiting.has(name)) return -1 // A reference cycle: ignore the back edge.
    visiting.add(name)
    const value = Math.max(-1, ...parents.get(name)!.map(visit)) + 1
    visiting.delete(name)
    rank.set(name, value)
    return value
  }
  spec.entities.forEach(({ name }) => visit(name))
  const order = [...spec.entities].sort((a, b) => rank.get(b.name)! - rank.get(a.name)!)
  for (const { name } of order) {
    const own = children.get(name)!.map(child => rank.get(child)!).filter(value => value > rank.get(name)!)
    if (parents.get(name)!.length === 0 && own.length) rank.set(name, Math.min(...own) - 1)
  }
  return rank
}

/** Lays the diagram out in rank rows, wrapping any rank wider than maxWidth. */
export function layoutErDiagram(spec: EntityDiagramSpec, maxWidth: number): DiagramLayout {
  const rank = rankEntities(spec)
  const sizes = new Map(spec.entities.map(entity => [entity.name, entitySize(entity)]))
  const declared = new Map(spec.entities.map((entity, index) => [entity.name, index]))
  const rankCount = Math.max(0, ...rank.values()) + 1
  const ranks: string[][] = Array.from({ length: rankCount }, () => [])
  spec.entities.forEach(({ name }) => ranks[rank.get(name)!].push(name))

  // Order each rank by the average position of its neighbours (barycentre),
  // sweeping down then up, so lines between ranks cross less.
  const neighbours = (name: string, towardsParents: boolean) => spec.relationships
    .filter(r => r.parent !== r.child && (towardsParents ? r.child === name : r.parent === name))
    .map(r => towardsParents ? r.parent : r.child)
  const position = new Map<string, number>()
  const indexRanks = () => ranks.forEach(names => names.forEach((name, index) => position.set(name, (index + 0.5) / names.length)))
  indexRanks()
  for (let sweep = 0; sweep < 4; sweep += 1) {
    const down = sweep % 2 === 0
    const sequence = down ? ranks.slice(1) : ranks.slice(0, -1).reverse()
    for (const names of sequence) {
      const score = (name: string) => {
        const values = neighbours(name, down).map(other => position.get(other)!).filter(value => value !== undefined)
        return values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : position.get(name)!
      }
      const scored = names.map(name => ({ name, score: score(name), tie: declared.get(name)! }))
      scored.sort((a, b) => a.score - b.score || a.tie - b.tie)
      names.splice(0, names.length, ...scored.map(item => item.name))
      names.forEach((name, index) => position.set(name, (index + 0.5) / names.length))
    }
  }

  const usable = Math.max(maxWidth - DIAGRAM.margin * 2, DIAGRAM.minWidth)
  // A self-reference loops out past the box's right edge; keep it clear of the neighbour.
  const selfReferencing = new Set(spec.relationships.filter(r => r.parent === r.child).map(r => r.child))
  const space = (name: string) => sizes.get(name)!.width + (selfReferencing.has(name) ? DIAGRAM.selfLoop : 0)
  const passSpace = DIAGRAM.passWidth + DIAGRAM.passGap

  // Wrap each rank into rows that fit in the space left after the rank's
  // reserve for the placeholders of lines that cross its rows.
  type Row = { names: string[]; width: number; height: number; wrapped: boolean; rank: number }
  const wrap = (reserve: number[]) => {
    const out: Row[] = []
    ranks.forEach((names, rankIndex) => {
      let row: Row | null = null
      for (const name of names) {
        const width = space(name)
        if (!row || row.width + DIAGRAM.gapX + width > usable - reserve[rankIndex]) {
          row = { names: [], width: -DIAGRAM.gapX, height: 0, wrapped: row !== null, rank: rankIndex }
          out.push(row)
        }
        row.names.push(name)
        row.width += DIAGRAM.gapX + width
        row.height = Math.max(row.height, sizes.get(name)!.height)
      }
    })
    return out
  }
  // A line that skips rows (in either direction) gets a thin placeholder in
  // each row it crosses, so the boxes there make room for it.
  const crossingsOf = (wrapped: Row[]) => {
    const rowOf = new Map<string, number>()
    wrapped.forEach((row, index) => row.names.forEach(name => rowOf.set(name, index)))
    const out = new Map<number, number[]>()
    spec.relationships.forEach((relationship, edge) => {
      const a = rowOf.get(relationship.parent)!
      const b = rowOf.get(relationship.child)!
      for (let row = Math.min(a, b) + 1; row < Math.max(a, b); row += 1) out.set(row, [...(out.get(row) ?? []), edge])
    })
    return out
  }
  // Places boxes and placeholders row by row, and reports how wide each row
  // actually came out (placeholders between two boxes replace their gap).
  const place = (rows: Row[], crossings: Map<number, number[]>) => {
    const contentWidth = Math.max(DIAGRAM.minWidth, ...rows.map(row => row.width))
    const left = DIAGRAM.margin
    const nodes = new Map<string, DiagramNode>()
    const passes = new Map<number, Waypoint[]>()
    const rowBottom = new Map<string, number>()
    const center = (node: DiagramNode) => node.x + node.width / 2
    let y = DIAGRAM.margin
    let widest = left + contentWidth
    const rowNeeded: number[] = []
    rows.forEach((row, index) => {
      if (index > 0) y += row.wrapped ? DIAGRAM.gapWrap : DIAGRAM.gapRank
      // Start from a centred row, then pull each box under the average of its
      // already-placed parents (and each placeholder under its line) and push
      // neighbours apart.
      type Item = { name?: string; edge?: number; width: number; wanted: number; order: number }
      const items: Item[] = []
      let packed = left + (contentWidth - row.width) / 2
      row.names.forEach((name, order) => {
        const width = space(name)
        const placedParents = neighbours(name, true).map(parent => nodes.get(parent)).filter((node): node is DiagramNode => Boolean(node))
        const wanted = placedParents.length ? placedParents.reduce((sum, node) => sum + center(node), 0) / placedParents.length : packed + sizes.get(name)!.width / 2
        items.push({ name, width, wanted, order })
        packed += width + DIAGRAM.gapX
      })
      for (const edge of crossings.get(index) ?? []) {
        const crossed = passes.get(edge) ?? []
        // The end of the line that is already placed is the one above this row.
        const { parent, child } = spec.relationships[edge]
        const placed = nodes.get(parent) ?? nodes.get(child)!
        const above = crossed.length ? crossed[crossed.length - 1].x : center(placed)
        items.push({ edge, width: DIAGRAM.passWidth, wanted: above, order: row.names.length + edge })
      }
      items.sort((a, b) => a.wanted - b.wanted || a.order - b.order)
      const gap = (a: Item, b: Item) => a.name && b.name ? DIAGRAM.gapX : DIAGRAM.passGap
      // Boxes aim their centre at `wanted`; a self-loop's extra space sits on the right.
      const xs = items.map(item => item.wanted - (item.name ? sizes.get(item.name)!.width : item.width) / 2)
      const needed = items.reduce((sum, item, i) => sum + item.width + (i ? gap(items[i - 1], item) : 0), 0)
      const right = left + Math.max(contentWidth, needed)
      for (let i = 0; i < xs.length; i += 1) xs[i] = Math.max(xs[i], i ? xs[i - 1] + items[i - 1].width + gap(items[i - 1], items[i]) : left)
      for (let i = xs.length - 1; i >= 0; i -= 1) xs[i] = Math.min(xs[i], i < xs.length - 1 ? xs[i + 1] - gap(items[i], items[i + 1]) - items[i].width : right - items[i].width)
      items.forEach((item, i) => {
        if (item.name) {
          const { width, height } = sizes.get(item.name)!
          nodes.set(item.name, { entity: spec.entities[declared.get(item.name)!], x: xs[i], y, width, height })
          rowBottom.set(item.name, y + row.height)
        } else {
          passes.set(item.edge!, [...(passes.get(item.edge!) ?? []), { x: xs[i] + item.width / 2, top: y, bottom: y + row.height }])
        }
      })
      widest = Math.max(widest, right)
      rowNeeded.push(needed)
      y += row.height
    })

    return { nodes, passes, rowBottom, widest, bottom: y, rowNeeded }
  }

  const reserve = ranks.map(() => 0)
  let rows = wrap(reserve)
  let crossings = crossingsOf(rows)
  let placed = place(rows, crossings)
  // A row that came out wider than the space is re-wrapped with a larger
  // reserve for its rank, one placeholder at a time: any re-wrap moves a whole
  // box, so a bigger step would split rows that fit. The reserve never passes
  // the room all the row's crossings need, so the loop ends; the cap is a backstop.
  const maxPasses = spec.relationships.length * ranks.length + 1
  for (let attempt = 0; attempt < maxPasses; attempt += 1) {
    let changed = false
    rows.forEach((row, index) => {
      const need = (crossings.get(index)?.length ?? 0) * passSpace
      const over = placed.rowNeeded[index] - usable
      if (over > 0 && row.names.length > 1 && need > reserve[row.rank]) {
        reserve[row.rank] += passSpace
        changed = true
      }
    })
    if (!changed) break
    rows = wrap(reserve)
    crossings = crossingsOf(rows)
    placed = place(rows, crossings)
  }

  return {
    width: Math.ceil(placed.widest + DIAGRAM.margin),
    height: Math.ceil(placed.bottom + DIAGRAM.margin),
    nodes: spec.entities.map(entity => placed.nodes.get(entity.name)!),
    edges: routeEdges(spec.relationships, placed.nodes, placed.passes, placed.rowBottom),
  }
}

/** Where a line crosses a row it skips, top row first: its x and the row's vertical band. */
type Waypoint = { x: number; top: number; bottom: number }

type Side = 'top' | 'bottom'
type Port = { node: string; side: Side; toward: number; edge: number }

// Lines leave the child and enter the parent on facing sides. Ports that share
// a side are spread along it in the order of the node at the other end.
function routeEdges(relationships: Relationship[], nodes: Map<string, DiagramNode>, passes: Map<number, Waypoint[]>, rowBottom: Map<string, number>): DiagramEdge[] {
  const center = (name: string) => nodes.get(name)!.x + nodes.get(name)!.width / 2
  const ports: Port[] = []
  const sides = relationships.map((relationship, edge) => {
    const child = nodes.get(relationship.child)!
    const parent = nodes.get(relationship.parent)!
    let childSide: Side = 'top'
    let parentSide: Side = 'bottom'
    if (parent.y >= child.y + child.height) {
      childSide = 'bottom'
      parentSide = 'top'
    } else if (parent.y + parent.height > child.y) {
      parentSide = 'top' // Same row: both ends leave from the top and arc over.
    }
    if (relationship.parent !== relationship.child) {
      // Order shared ports by where the line heads next: its nearest
      // placeholder, or the node at the other end.
      // Placeholders run top-down, so the child's nearest one is the last
      // when the child is below, and the first when it is above.
      const via = passes.get(edge) ?? []
      const childBelow = childSide === 'top'
      const nearChild = childBelow ? via[via.length - 1] : via[0]
      const nearParent = childBelow ? via[0] : via[via.length - 1]
      ports.push({ node: relationship.child, side: childSide, toward: nearChild?.x ?? center(relationship.parent), edge })
      ports.push({ node: relationship.parent, side: parentSide, toward: nearParent?.x ?? center(relationship.child), edge })
    }
    return { childSide, parentSide }
  })
  const offset = new Map<string, number>()
  const groups = new Map<string, Port[]>()
  for (const port of ports) {
    const key = `${port.node}|${port.side}`
    groups.set(key, [...(groups.get(key) ?? []), port])
  }
  for (const group of groups.values()) {
    group.sort((a, b) => a.toward - b.toward || a.edge - b.edge)
    const node = nodes.get(group[0].node)!
    const span = Math.min(node.width * 0.7, group.length * 14)
    group.forEach((port, index) => offset.set(`${port.edge}|${port.node}`,
      group.length === 1 ? 0 : -span / 2 + (span * index) / (group.length - 1)))
  }
  const point = (name: string, side: Side, edge: number) => {
    const node = nodes.get(name)!
    return { x: node.x + node.width / 2 + (offset.get(`${edge}|${name}`) ?? 0), y: side === 'top' ? node.y : node.y + node.height }
  }
  return relationships.map((relationship, edge) => {
    if (relationship.parent === relationship.child) {
      const node = nodes.get(relationship.child)!
      const x = node.x + node.width
      const top = node.y + DIAGRAM.headerHeight / 2
      return { relationship, path: `M${x},${top + 10} C${x + 28},${top + 10} ${x + 28},${top - 10} ${x},${top - 10}` }
    }
    const { childSide, parentSide } = sides[edge]
    const start = point(relationship.child, childSide, edge)
    const end = point(relationship.parent, parentSide, edge)
    if (childSide === parentSide) {
      // Same row: arc over the top.
      const lift = Math.min(start.y, end.y) - 24
      return { relationship, path: `M${round(start.x)},${round(start.y)} C${round(start.x)},${round(lift)} ${round(end.x)},${round(lift)} ${round(end.x)},${round(end.y)}` }
    }
    // A box shorter than its row leaves the line to run straight between its
    // bottom edge and the row's, so the curve never cuts a taller neighbour.
    // Lines then pass each skipped row's placeholder: a curve between rows,
    // a straight run across each row.
    const via = passes.get(edge) ?? []
    const points: { x: number; y: number }[] = [start]
    if (childSide === 'bottom') points.push({ x: start.x, y: rowBottom.get(relationship.child)! })
    if (childSide === 'top') points.push(...[...via].reverse().flatMap(pass => [{ x: pass.x, y: pass.bottom }, { x: pass.x, y: pass.top }]))
    else points.push(...via.flatMap(pass => [{ x: pass.x, y: pass.top }, { x: pass.x, y: pass.bottom }]))
    if (parentSide === 'bottom') points.push({ x: end.x, y: rowBottom.get(relationship.parent)! })
    points.push(end)
    let path = `M${round(start.x)},${round(start.y)}`
    for (let i = 1; i < points.length; i += 1) {
      const from = points[i - 1]
      const to = points[i]
      if (from.x === to.x && from.y === to.y) continue
      const middle = (from.y + to.y) / 2
      path += from.x === to.x ? ` L${round(to.x)},${round(to.y)}` : ` C${round(from.x)},${round(middle)} ${round(to.x)},${round(middle)} ${round(to.x)},${round(to.y)}`
    }
    return { relationship, path }
  })
}

const round = (value: number) => Math.round(value * 10) / 10
