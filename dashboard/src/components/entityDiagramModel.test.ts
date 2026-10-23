import { describe, expect, it } from 'vitest'

import dataModel from '../../../docs/DATA_MODEL.md?raw'
import { DIAGRAM, layoutErDiagram, parseErDiagram, type DiagramLayout } from './entityDiagramModel'

const SAMPLE = [
  'erDiagram',
  '    projects {',
  '        TEXT id PK',
  '        TEXT name',
  '        etc more "+5 columns"',
  '    }',
  '    tests {',
  '        TEXT id PK',
  '        TEXT project_id FK',
  '        TEXT timeslot_id FK',
  '    }',
  '    user_role_bindings {',
  '        TEXT scope_id "any resource"',
  '    }',
  '    projects ||--o{ tests : "project_id"',
  '    capacity_reservations |o..o| tests : "timeslot_id"',
].join('\n')

function overlaps(layout: DiagramLayout) {
  const pairs: string[] = []
  layout.nodes.forEach((a, i) => layout.nodes.slice(i + 1).forEach(b => {
    if (a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height) pairs.push(`${a.entity.name}/${b.entity.name}`)
  }))
  return pairs
}

// No part of a line (straight runs or curves) may pass through a box other
// than the two it connects.
function runsThroughBoxes(layout: DiagramLayout) {
  const hits = new Set<string>()
  for (const { relationship, path } of layout.edges) {
    if (relationship.parent === relationship.child) continue
    const segments = [...path.matchAll(/([MLC])([^MLC]+)/g)].map(([, command, values]) => ({ command, numbers: values.trim().split(/[\s,]+/).map(Number) }))
    let x0 = 0
    let y0 = 0
    const samples: [number, number][] = []
    for (const { command, numbers } of segments) {
      const [x, y] = numbers.slice(-2)
      for (let t = 0.05; t < 1; t += 0.05) {
        if (command === 'L') samples.push([x0 + (x - x0) * t, y0 + (y - y0) * t])
        if (command === 'C') {
          const [x1, y1, x2, y2] = numbers
          const u = 1 - t
          samples.push([u ** 3 * x0 + 3 * u * u * t * x1 + 3 * u * t * t * x2 + t ** 3 * x, u ** 3 * y0 + 3 * u * u * t * y1 + 3 * u * t * t * y2 + t ** 3 * y])
        }
      }
      x0 = x
      y0 = y
    }
    for (const node of layout.nodes) {
      if (node.entity.name === relationship.child || node.entity.name === relationship.parent) continue
      if (samples.some(([px, py]) => px > node.x + 1 && px < node.x + node.width - 1 && py > node.y + 1 && py < node.y + node.height - 1)) hits.add(`${relationship.child}→${relationship.parent} through ${node.entity.name}`)
    }
  }
  return [...hits]
}

describe('parseErDiagram', () => {
  it('reads entities, keys, notes, hidden-column counts and relationships', () => {
    const spec = parseErDiagram(SAMPLE)!
    expect(spec.entities.map(entity => [entity.name, entity.stub])).toEqual([
      ['projects', false], ['tests', false], ['user_role_bindings', false], ['capacity_reservations', true],
    ])
    expect(spec.entities[0]).toMatchObject({ hiddenColumns: 5, attributes: [{ name: 'id', keys: ['PK'] }, { name: 'name', keys: [] }] })
    expect(spec.entities[2].attributes[0]).toEqual({ type: 'TEXT', name: 'scope_id', keys: [], comment: 'any resource' })
    expect(spec.relationships).toEqual([
      { parent: 'projects', child: 'tests', label: 'project_id', logical: false, optional: false, many: true },
      { parent: 'capacity_reservations', child: 'tests', label: 'timeslot_id', logical: true, optional: true, many: false },
    ])
  })

  it('accepts combined keys and unquoted labels', () => {
    const spec = parseErDiagram('erDiagram\n  a {\n    TEXT id PK, FK\n  }\n  b ||--|{ a : id')!
    expect(spec.entities[0].attributes[0].keys).toEqual(['PK', 'FK'])
    expect(spec.relationships[0]).toMatchObject({ parent: 'b', child: 'a', label: 'id', many: true })
  })

  it('returns null for other Mermaid diagrams and unsupported syntax, so they stay code', () => {
    expect(parseErDiagram('flowchart LR\n  a --> b')).toBeNull()
    expect(parseErDiagram('erDiagram\n  a {\n    TEXT id PK\n')).toBeNull()
    expect(parseErDiagram('erDiagram\n  a ||--o{ b : "x"\n  style a fill:#f00')).toBeNull()
    expect(parseErDiagram('erDiagram')).toBeNull()
    // The parent must come first; a many-side-first line would draw reversed.
    expect(parseErDiagram('erDiagram\n  child }o--|| parent : "parent_id"')).toBeNull()
  })

  it('draws an overview that declares no columns with every table as a table', () => {
    const spec = parseErDiagram('erDiagram\n  domains ||--o{ projects : "domain_id"')!
    expect(spec.entities.every(entity => !entity.stub)).toBe(true)
  })
})

describe('layoutErDiagram', () => {
  it('puts a referenced table above the tables that hold its ID', () => {
    const layout = layoutErDiagram(parseErDiagram(SAMPLE)!, 880)
    const node = (name: string) => layout.nodes.find(n => n.entity.name === name)!
    expect(node('projects').y + node('projects').height).toBeLessThan(node('tests').y)
    expect(node('capacity_reservations').y + node('capacity_reservations').height).toBeLessThan(node('tests').y)
    expect(node('capacity_reservations').height).toBe(DIAGRAM.stubHeight)
    expect(overlaps(layout)).toEqual([])
    // Each path ends on the parent's bottom edge, where the arrowhead sits.
    const toProjects = layout.edges.find(edge => edge.relationship.parent === 'projects')!
    expect(toProjects.path.endsWith(`,${node('projects').y + node('projects').height}`)).toBe(true)
  })

  it('wraps a wide rank into rows that fit the available width', () => {
    const children = Array.from({ length: 12 }, (_, i) => `child_table_number_${i}`)
    const source = ['erDiagram', 'hub {', 'TEXT id PK', '}', ...children.map(name => `hub ||--o{ ${name} : "hub_id"`)].join('\n')
    const layout = layoutErDiagram(parseErDiagram(source)!, 700)
    expect(layout.width).toBeLessThanOrEqual(700)
    const rows = new Set(layout.nodes.filter(n => n.entity.name !== 'hub').map(n => n.y))
    expect(rows.size).toBeGreaterThan(1)
    expect(overlaps(layout)).toEqual([])
  })

  it('routes a line that skips a row around the boxes in that row', () => {
    const middle = Array.from({ length: 6 }, (_, i) => `middle_table_${i}`)
    const source = ['erDiagram', ...middle.map(name => `top ||--o{ ${name} : "top_id"`), `middle_table_0 ||--o{ bottom : "middle_id"`, 'top ||--o{ bottom : "top_id"'].join('\n')
    const layout = layoutErDiagram(parseErDiagram(source)!, 880)
    const skip = layout.edges.find(edge => edge.relationship.child === 'bottom' && edge.relationship.parent === 'top')!
    expect(skip.path).toContain(' L')
    expect(runsThroughBoxes(layout)).toEqual([])
  })

  it('wraps a rank only as far as the lines crossing it require', () => {
    // At 420 px the APM domain once came out one table per row (13 rows);
    // room for its crossings needs far fewer.
    const diagram = (title: string) => parseErDiagram([...dataModel.matchAll(new RegExp(`## ${title}[\\s\\S]*?\`\`\`mermaid\\n([\\s\\S]*?)\`\`\``, 'g'))][0][1])!
    const rowCount = (layout: DiagramLayout) => new Set(layout.nodes.map(node => node.y)).size
    for (const [title, width, rows] of [['APM & observability connections', 420, 9], ['Applications, releases & library', 467, 8]] as const) {
      const layout = layoutErDiagram(diagram(title), width)
      expect(rowCount(layout), title).toBeLessThanOrEqual(rows)
      expect(layout.width, title).toBeLessThanOrEqual(width)
    }
  })

  it('routes the back edge of a reference cycle around the rows it skips', () => {
    // a → b → c down the page, and c holds a back reference to a's child b's sibling.
    const source = ['erDiagram', 'a ||--o{ b : "a_id"', 'b ||--o{ c : "b_id"', 'c ||--o{ d : "c_id"', 'd |o--o{ a : "d_id"',
      'a ||--o{ wide_sibling_number_one : "a_id"', 'a ||--o{ wide_sibling_number_two : "a_id"', 'b ||--o{ wide_sibling_number_three : "b_id"'].join('\n')
    for (const width of [880, 300]) {
      const layout = layoutErDiagram(parseErDiagram(source)!, width)
      expect(runsThroughBoxes(layout)).toEqual([])
      expect(overlaps(layout)).toEqual([])
    }
  })

  it('keeps a self-reference loop clear of the next box', () => {
    const source = ['erDiagram', 'parent ||--o{ folders : "parent_id"', 'parent ||--o{ neighbour : "parent_id"', 'folders |o--o{ folders : "folder_id"'].join('\n')
    const layout = layoutErDiagram(parseErDiagram(source)!, 880)
    const [folders, neighbour] = ['folders', 'neighbour'].map(name => layout.nodes.find(node => node.entity.name === name)!)
    const loopTip = Math.max(...[...layout.edges.find(edge => edge.relationship.child === edge.relationship.parent)!.path.matchAll(/(-?[\d.]+),/g)].map(match => Number(match[1])))
    const [first, second] = folders.x < neighbour.x ? [folders, neighbour] : [neighbour, folders]
    if (first === folders) expect(loopTip).toBeLessThan(second.x)
    expect(loopTip).toBeLessThanOrEqual(layout.width)
  })

  it('survives reference cycles and self references', () => {
    const source = ['erDiagram', 'a ||--o{ b : "a_id"', 'b ||--o{ a : "b_id"', 'a |o--o{ a : "parent_id"'].join('\n')
    const layout = layoutErDiagram(parseErDiagram(source)!, 880)
    expect(layout.nodes).toHaveLength(2)
    expect(layout.edges.every(edge => /^M[\d.-]+,[\d.-]+ C/.test(edge.path))).toBe(true)
  })

  it('lays out every diagram in the data model reference without overlaps at article width', () => {
    const blocks = [...dataModel.matchAll(/```mermaid\n([\s\S]*?)```/g)].map(match => match[1])
    expect(blocks.length).toBeGreaterThan(10)
    for (const block of blocks) {
      const spec = parseErDiagram(block)
      expect(spec, block.slice(0, 80)).not.toBeNull()
      const widestBox = Math.max(...spec!.entities.map(entity => layoutErDiagram({ entities: [entity], relationships: [] }, 2000).width))
      for (const width of [1000, 880, 700, 420]) {
        const layout = layoutErDiagram(spec!, width)
        expect(overlaps(layout)).toEqual([])
        expect(runsThroughBoxes(layout)).toEqual([])
        expect(layout.width).toBeLessThanOrEqual(Math.max(width, widestBox + 2 * DIAGRAM.selfLoop))
        expect(layout.edges).toHaveLength(spec!.relationships.length)
      }
      // Placeholders are budgeted when wrapping, so the drawing fits every width it is given.
      for (let width = 420; width <= 1400; width += 1) {
        expect(layoutErDiagram(spec!, width).width, `width ${width}`).toBeLessThanOrEqual(Math.max(width, widestBox + 2 * DIAGRAM.selfLoop))
      }
    }
  }, 30_000) // About 14,000 layouts: allow for a loaded runner.
})
