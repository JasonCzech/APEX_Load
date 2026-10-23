// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import MarkdownRenderer from './MarkdownRenderer'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const DIAGRAM_SOURCE = [
  '## Runs',
  '',
  '```mermaid',
  'erDiagram',
  '    tests {',
  '        TEXT id PK',
  '        TEXT release_id FK',
  '        etc more "+40 columns"',
  '    }',
  '    run_archives {',
  '        TEXT test_id PK, FK',
  '    }',
  '    releases ||--o{ tests : "release_id"',
  '    tests ||..o| run_archives : "test_id"',
  '```',
].join('\n')

describe('EntityDiagram in Markdown', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    container = document.createElement('div')
    document.body.append(container)
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })
  const render = async (source: string, copyExamples = true) => {
    await act(async () => root.render(<MarkdownRenderer source={source} copyExamples={copyExamples} />))
  }

  it('draws a Mermaid erDiagram as a labelled diagram instead of code', async () => {
    await render(DIAGRAM_SOURCE)
    const svg = container.querySelector('figure.entity-diagram svg[role="img"]')!
    expect(svg.getAttribute('aria-label')).toBe('Runs: entity relationship diagram of 2 tables and 2 relationships')
    expect(container.querySelector('pre')).toBeNull()
    const names = Array.from(svg.querySelectorAll('.entity-diagram-name')).map(node => node.textContent)
    expect(names.sort()).toEqual(['releases', 'run_archives', 'tests'])
    expect(svg.querySelector('.entity-diagram-node.is-stub .entity-diagram-name')?.textContent).toBe('releases')
    expect(Array.from(svg.querySelectorAll('.entity-diagram-key')).map(node => node.textContent)).toEqual(['PK', 'FK', 'PK FK'])
    expect(Array.from(svg.querySelectorAll('.entity-diagram-note')).map(node => node.textContent)).toEqual(['+40 more columns'])
    const edges = Array.from(svg.querySelectorAll('.entity-diagram-edges path'))
    expect(edges.map(edge => edge.querySelector('title')?.textContent)).toEqual([
      'tests.release_id → releases',
      'run_archives.test_id → tests (no constraint)',
    ])
    expect(edges[1].getAttribute('class')).toBe('is-logical')
    const marker = svg.querySelector('marker')!.id
    expect(edges.every(edge => edge.getAttribute('marker-end') === `url(#${marker})`)).toBe(true)
  })

  it('highlights the lines of the table under the pointer', async () => {
    await render(DIAGRAM_SOURCE)
    const archive = Array.from(container.querySelectorAll('.entity-diagram-node')).find(node => node.textContent?.startsWith('run_archives'))!
    await act(async () => archive.dispatchEvent(new MouseEvent('mouseover', { bubbles: true })))
    const edges = Array.from(container.querySelectorAll('.entity-diagram-edges path'))
    expect(container.querySelector('svg')!.getAttribute('class')).toBe('has-active')
    expect(edges.map(edge => edge.classList.contains('is-active'))).toEqual([false, true])
    // The highlighted line switches to the accent arrowhead; markers carry their own theme fill.
    const markers = Array.from(container.querySelectorAll('marker')).map(marker => marker.id)
    expect(markers).toHaveLength(2)
    expect(edges.map(edge => edge.getAttribute('marker-end'))).toEqual([`url(#${markers[0]})`, `url(#${markers[1]})`])
    expect(container.querySelector(`marker#${CSS.escape(markers[1])}`)?.getAttribute('class')).toBe('is-active')
    await act(async () => archive.dispatchEvent(new MouseEvent('mouseout', { bubbles: true })))
    expect(container.querySelector('svg')!.getAttribute('class')).toBeNull()
  })

  it('draws a table with no listed columns as one rounded header', async () => {
    await render('```mermaid\nerDiagram\n    domains ||--o{ projects : "domain_id"\n```')
    const nodes = Array.from(container.querySelectorAll('.entity-diagram-node'))
    expect(nodes).toHaveLength(2)
    expect(nodes.every(node => !node.classList.contains('is-stub'))).toBe(true)
    expect(nodes.map(node => node.querySelector('.entity-diagram-header')?.tagName.toLowerCase())).toEqual(['rect', 'rect'])
  })

  it('keeps other Mermaid diagrams as copyable source', async () => {
    await render('```mermaid\nflowchart LR\n  a --> b\n```')
    expect(container.querySelector('figure.entity-diagram')).toBeNull()
    expect(container.querySelector('pre code')?.textContent).toBe('flowchart LR\n  a --> b')
    await render('```mermaid\nflowchart TD\n  a --> b\n```', false)
    expect(container.querySelector('pre.markdown-code-block code')?.getAttribute('data-language')).toBe('mermaid')
  })
})
