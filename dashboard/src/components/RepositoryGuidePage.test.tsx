// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useActiveTopbarContribution } from '../hooks/TopbarContributionProvider'
import { TopbarContributionProvider } from '../hooks/TopbarContributionRoot'
import RepositoryGuidePage from './RepositoryGuidePage'
import { parseMarkdown } from './repositoryGuideMarkdown'

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

  it('parses fenced-code info strings containing punctuation without stalling', () => {
    const blocks = parseMarkdown(['```shell-session linenums', '$ apexctl tests list', '```'].join('\n'))
    expect(blocks).toEqual([{ kind: 'code', language: 'shell-session', code: '$ apexctl tests list' }])
  })

  it('treats a pipe-prefixed non-table line as paragraph text', () => {
    const blocks = parseMarkdown(['| this is not a table', 'continued text'].join('\n'))
    expect(blocks).toEqual([{ kind: 'paragraph', text: '| this is not a table continued text' }])
  })
})

describe('RepositoryGuidePage', () => {
  const guideSource = [
    '# APEX Load API User Guide',
    '',
    '## Authentication',
    '',
    'Use `Authorization` when calling the API.',
    '',
    '```bash',
    'curl http://localhost:8080/api/v1/system/health',
    '```',
    '',
    '| Method | Path |',
    '| --- | --- |',
    '| `GET` | `/api/v1/system/health` |',
  ].join('\n')
  let container: HTMLDivElement
  let root: Root
  let rootMounted: boolean

  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      text: async () => guideSource,
    }))
    window.history.replaceState(null, '', '/')
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    rootMounted = true
  })

  afterEach(() => {
    if (rootMounted) act(() => root.unmount())
    container.remove()
    vi.unstubAllGlobals()
  })

  async function renderRepositoryGuidePage() {
    await act(async () => {
      root.render(
        <TopbarContributionProvider>
          <TopbarContributionHarness />
          <RepositoryGuidePage />
        </TopbarContributionProvider>,
      )
    })
  }

  function topbarText() {
    return container.querySelector('[data-testid="topbar-contribution"]')?.textContent || ''
  }

  function findTopbarButton(label: string) {
    const topbar = container.querySelector('[data-testid="topbar-contribution"]')
    return Array.from(topbar?.querySelectorAll('button') || []).find(button => button.textContent?.trim() === label)
  }

  it('renders the in-repository guide with retained API Guide title toggles', async () => {
    await renderRepositoryGuidePage()

    expect(container.textContent).toContain('APEX Load API User Guide')
    expect(topbarText()).toContain('Repository Guide')
    expect(topbarText()).toContain('General')
    expect(topbarText()).toContain('MCP Server')
    expect(topbarText()).toContain('Quick Start')
    expect(topbarText()).toContain('Authentication')
    expect(topbarText()).toContain('Endpoint Map')
    expect(topbarText()).toContain('Swagger')
    expect(topbarText()).toContain('Repo Guide')
    expect(findTopbarButton('Repo Guide')?.getAttribute('aria-pressed')).toBe('true')
    expect(container.textContent).toContain('Authentication')
    expect(container.querySelector('.markdown-table-wrap table')).not.toBeNull()
    expect(container.querySelector('pre.markdown-code-block code')).not.toBeNull()
    expect(fetch).toHaveBeenCalledTimes(1)

    expect(container.querySelector('a[href="#/docs"]')).toBeNull()
    const endpointMapButton = findTopbarButton('Endpoint Map')
    expect(endpointMapButton).toBeDefined()
    await act(async () => {
      endpointMapButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(window.location.hash).toBe('#/docs?guide=endpoint-map')
  })

  it('shows an accessible error when the guide asset cannot be loaded', async () => {
    vi.mocked(fetch).mockResolvedValueOnce({
      ok: false,
      status: 503,
      text: async () => '',
    } as Response)

    await renderRepositoryGuidePage()

    expect(container.querySelector('[role="alert"]')?.textContent).toContain('status 503')
  })

  it('reports an empty successful guide response as an error instead of loading forever', async () => {
    vi.mocked(fetch).mockResolvedValueOnce({
      ok: true,
      status: 204,
      text: async () => '',
    } as Response)

    await renderRepositoryGuidePage()

    expect(container.querySelector('[role="status"]')).toBeNull()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('response was empty')
  })

  it('reports a rejected guide request as an accessible network error', async () => {
    vi.mocked(fetch).mockRejectedValueOnce(new TypeError('network unavailable'))

    await renderRepositoryGuidePage()

    expect(container.querySelector('[role="alert"]')?.textContent).toContain('network unavailable')
  })

  it('aborts the guide request on unmount and ignores a late body resolution', async () => {
    let resolveBody!: (source: string) => void
    const body = new Promise<string>(resolve => {
      resolveBody = resolve
    })
    vi.mocked(fetch).mockResolvedValueOnce({
      ok: true,
      status: 200,
      text: () => body,
    } as Response)

    await renderRepositoryGuidePage()
    const signal = vi.mocked(fetch).mock.calls[0]?.[1]?.signal
    expect(signal).toBeInstanceOf(AbortSignal)
    expect(signal?.aborted).toBe(false)

    act(() => root.unmount())
    rootMounted = false
    expect(signal?.aborted).toBe(true)

    await act(async () => {
      resolveBody('# Late guide')
      await body
      await Promise.resolve()
    })
    expect(container.textContent).toBe('')
  })
})
