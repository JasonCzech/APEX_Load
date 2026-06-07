// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import RepositoryGuidePage from './RepositoryGuidePage'
import { parseMarkdown } from './repositoryGuideMarkdown'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('parseMarkdown', () => {
  it('parses headings, paragraphs, lists, code blocks, and tables', () => {
    const blocks = parseMarkdown(
      [
        '# Title',
        '',
        'Intro paragraph with `code` and **bold**.',
        '',
        '## Section',
        '',
        '- first item',
        '- second item',
        '',
        '```bash',
        'curl http://localhost:8080/api/v1/system/health',
        '```',
        '',
        '| Method | Path |',
        '| --- | --- |',
        '| `GET` | `/api/v1/system/health` |',
      ].join('\n'),
    )

    expect(blocks).toContainEqual({ kind: 'heading', level: 1, text: 'Title', id: 'title' })
    expect(blocks).toContainEqual({ kind: 'heading', level: 2, text: 'Section', id: 'section' })
    expect(blocks).toContainEqual({ kind: 'list', items: ['first item', 'second item'] })
    expect(blocks).toContainEqual({ kind: 'code', language: 'bash', code: 'curl http://localhost:8080/api/v1/system/health' })

    const table = blocks.find(block => block.kind === 'table')
    expect(table).toEqual({
      kind: 'table',
      headers: ['Method', 'Path'],
      rows: [['`GET`', '`/api/v1/system/health`']],
    })
  })

  it('does not treat code-fence content as block markup', () => {
    const blocks = parseMarkdown(['```', '# not a heading', '- not a list', '```'].join('\n'))
    expect(blocks).toEqual([{ kind: 'code', language: '', code: '# not a heading\n- not a list' }])
  })
})

describe('RepositoryGuidePage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('renders the in-repository guide with a back link to the API Guide', async () => {
    await act(async () => {
      root.render(<RepositoryGuidePage />)
    })

    expect(container.textContent).toContain('APEX Load API User Guide')
    expect(container.textContent).toContain('Authentication')
    expect(container.querySelector('.markdown-table-wrap table')).not.toBeNull()
    expect(container.querySelector('pre.markdown-code-block code')).not.toBeNull()

    const backLink = container.querySelector('a[href="#/docs"]')
    expect(backLink?.textContent).toContain('API Guide')
  })
})
