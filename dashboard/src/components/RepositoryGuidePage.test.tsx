// @vitest-environment jsdom

import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'

import { useActiveTopbarContribution } from '../hooks/TopbarContributionProvider'
import { TopbarContributionProvider } from '../hooks/TopbarContributionRoot'
import ApiGuidePage from './ApiGuidePage'
import { parseAppViewFromHash, viewToLocationHash } from '../appRouting'
import { markdownPlainText, parseMarkdown } from './repositoryGuideMarkdown'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function TopbarContributionHarness() {
  const contribution = useActiveTopbarContribution()
  return (
    <div data-testid="topbar-contribution">
      {contribution?.titleNode || contribution?.title}
      {contribution?.controls}
    </div>
  )
}

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
    expect(blocks).toContainEqual({
      kind: 'list',
      ordered: false,
      items: [{ text: 'first item' }, { text: 'second item' }],
    })
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

  it('parses fenced-code info strings containing punctuation without stalling', () => {
    const blocks = parseMarkdown(['```shell-session linenums', '$ apexctl tests list', '```'].join('\n'))
    expect(blocks).toEqual([{ kind: 'code', language: 'shell-session', code: '$ apexctl tests list' }])
  })

  it('treats a pipe-prefixed non-table line as paragraph text', () => {
    const blocks = parseMarkdown(['| this is not a table', 'continued text'].join('\n'))
    expect(blocks).toEqual([{ kind: 'paragraph', text: '| this is not a table continued text' }])
  })

  it('parses horizontal rules and blockquotes as dedicated blocks', () => {
    const blocks = parseMarkdown(['Paragraph before.', '', '---', '', '> Important note', '> continued on next line'].join('\n'))
    expect(blocks).toEqual([
      { kind: 'paragraph', text: 'Paragraph before.' },
      { kind: 'hr' },
      { kind: 'blockquote', text: 'Important note continued on next line' },
    ])
    // A rule needs three markers; two asterisks alone stay text.
    expect(parseMarkdown('* * *')).toEqual([{ kind: 'hr' }])
    expect(parseMarkdown('**')).toEqual([{ kind: 'paragraph', text: '**' }])
  })

  // The guides wrap bullets across several source lines. Terminating the list at the
  // first continuation line split every wrapped bullet into a one-item list plus an
  // orphan paragraph, which is what made the rendered guides look fragmented.
  it('keeps wrapped continuation lines inside the list item that owns them', () => {
    const blocks = parseMarkdown(
      [
        '- **Virtual users** simulate concurrent clients. During a run they execute',
        "  your script's actions repeatedly against the target system.",
        '- **Scenarios** combine one or more scripts into a runnable load model.',
      ].join('\n'),
    )

    expect(blocks).toEqual([
      {
        kind: 'list',
        ordered: false,
        items: [
          {
            text: "**Virtual users** simulate concurrent clients. During a run they execute your script's actions repeatedly against the target system.",
          },
          { text: '**Scenarios** combine one or more scripts into a runnable load model.' },
        ],
      },
    ])
  })

  it('parses ordered lists and preserves a non-default start number', () => {
    const blocks = parseMarkdown(['1. set your scope', '2. author a script', '3. launch the run'].join('\n'))
    expect(blocks).toEqual([
      {
        kind: 'list',
        ordered: true,
        items: [{ text: 'set your scope' }, { text: 'author a script' }, { text: 'launch the run' }],
      },
    ])

    const resumed = parseMarkdown(['4) validate the load model', '5) launch'].join('\n'))
    expect(resumed).toEqual([
      {
        kind: 'list',
        ordered: true,
        start: 4,
        items: [{ text: 'validate the load model' }, { text: 'launch' }],
      },
    ])
  })

  it('nests an indented list under the item that introduces it', () => {
    const blocks = parseMarkdown(
      [
        '1. Open the Script Editor.',
        '   - Pick a protocol.',
        '   - Set the base URL.',
        '2. Validate the script.',
      ].join('\n'),
    )

    expect(blocks).toEqual([
      {
        kind: 'list',
        ordered: true,
        items: [
          {
            text: 'Open the Script Editor.',
            children: {
              kind: 'list',
              ordered: false,
              items: [{ text: 'Pick a protocol.' }, { text: 'Set the base URL.' }],
            },
          },
          { text: 'Validate the script.' },
        ],
      },
    ])
  })

  it('starts a new block when the list marker style changes at the same level', () => {
    const blocks = parseMarkdown(['- a bullet', '1. a numbered step'].join('\n'))
    expect(blocks).toEqual([
      { kind: 'list', ordered: false, items: [{ text: 'a bullet' }] },
      { kind: 'list', ordered: true, items: [{ text: 'a numbered step' }] },
    ])
  })

  it('does not swallow the block that follows a wrapped list item', () => {
    const blocks = parseMarkdown(
      ['- a bullet that wraps', '  onto a second line', '', '## Next Section', '', 'Body text.'].join('\n'),
    )

    expect(blocks).toEqual([
      { kind: 'list', ordered: false, items: [{ text: 'a bullet that wraps onto a second line' }] },
      { kind: 'heading', level: 2, text: 'Next Section', id: 'next-section' },
      { kind: 'paragraph', text: 'Body text.' },
    ])
  })
})

describe('legacy repository-guide address', () => {
  it('opens API workflows under the consolidated Help title', async () => {
    const { helpArticles, loadHelpSource } = await import('../helpContent')
    // The old bookmark resolves to the docs article; render what that address shows.
    const hash = viewToLocationHash(parseAppViewFromHash('#/repository-guide'))
    expect(hash).toBe('#/docs?article=api-workflows')
    const container = document.createElement('div'); document.body.append(container)
    const root = createRoot(container)
    window.history.replaceState(null, '', `/${hash}`)
    await act(async () => {
      root.render(<TopbarContributionProvider><TopbarContributionHarness /><ApiGuidePage activePage="general" defaultArticle="api-workflows" /></TopbarContributionProvider>)
      await loadHelpSource(helpArticles.find(article => article.id === 'api-workflows')!)
    })
    expect(container.textContent).toContain('API workflows')
    expect(container.textContent).toContain('Help & documentation')
    expect(container.textContent).toContain('java_vuser_persistent_lifecycle_v1')
    expect(container.textContent).toContain('one JVM and one Actions instance alive per VUser')
    expect(container.querySelector('a[href="#/swagger"]')).not.toBeNull()
    await act(async () => root.unmount()); container.remove()
  })
})


it('gives repeated reference headings unique section IDs and preserves escaped table pipes', () => {
  const blocks = parseMarkdown('## Fields\n\n## Fields\n\n## Fields-1\n\n| Mode | Detail |\n| --- | --- |\n| `admin\\|user` | Two modes |')
  expect(blocks.filter(block => block.kind === 'heading').map(block => block.id)).toEqual(['fields', 'fields-1', 'fields-1-1'])
  expect(blocks.find(block => block.kind === 'table')).toMatchObject({ headers: ['Mode', 'Detail'], rows: [['`admin|user`', 'Two modes']] })
  expect(markdownPlainText('Use *italic*, `__*delimiter*__`, and */…/*ERROR*/')).toBe('Use italic, __*delimiter*__, and */…/*ERROR*/')
})
