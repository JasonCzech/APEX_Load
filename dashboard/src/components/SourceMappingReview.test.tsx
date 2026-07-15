// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SourceMappingReview from './SourceMappingReview'
import { scrollMappingRowIntoTableView } from './sourceMappingScroll'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

async function readSourceMappingStylesheet(): Promise<string> {
  // @ts-expect-error node:fs is available in the Vitest runtime.
  const { readFileSync } = await import('node:fs')
  const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
  return readFileSync(`${cwd}/src/components/SourceMappingReview.css`, 'utf8')
}

function cssRule(css: string, selector: string) {
  const selectorIndex = css.indexOf(`${selector} {`)
  expect(selectorIndex).toBeGreaterThanOrEqual(0)
  const bodyStart = css.indexOf('{', selectorIndex) + 1
  const bodyEnd = css.indexOf('}', bodyStart)
  return css.slice(bodyStart, bodyEnd)
}

vi.mock('./LinkedCodePane', () => ({
  default: ({
    value,
    ariaLabel,
    activeRange,
    semanticMode,
    maxHeight,
  }: {
    value: string
    ariaLabel?: string
    activeRange?: { startLine: number; endLine: number } | null
    semanticMode?: string
    maxHeight?: string
  }) => {
    const activeText = activeRange
      ? value.split('\n').slice(activeRange.startLine - 1, activeRange.endLine).join('\n')
      : ''
    return (
      <pre
        aria-label={ariaLabel}
        data-active-range={activeRange ? `${activeRange.startLine}-${activeRange.endLine}` : ''}
        data-max-height={maxHeight || ''}
        data-semantic-mode={semanticMode || ''}
      >
        {activeText || value}
      </pre>
    )
  },
}))

const sourceReview = {
  source_files: [
    {
      path: 'Action.c',
      language: 'c',
      content: 'Action()\n{\n  web_url("Homepage", LAST);\n}',
    },
    {
      path: 'vuser_end.c',
      language: 'c',
      content: 'vuser_end()\n{\n  web_url("Logout", LAST);\n}',
    },
    {
      path: 'globals.h',
      language: 'h',
      content: '#define HOST "example.test"',
    },
  ],
  mappings: [
    {
      id: 'homepage',
      source_file: 'Action.c',
      start_line: 3,
      end_line: 3,
      function: 'web_url',
      target_path: 'actions[0]',
      target_name: 'Homepage',
      target_kind: 'action',
      status: 'converted',
      support_mode: 'executable',
    },
    {
      id: 'logout',
      source_file: 'vuser_end.c',
      start_line: 3,
      end_line: 3,
      function: 'web_url',
      target_path: 'actions[1]',
      target_name: 'Logout',
      target_kind: 'action',
      status: 'converted',
      support_mode: 'native_noop',
    },
  ],
}

const script = {
  name: 'Imported Enterprise Script Archive',
  version: '1.0',
  protocol: 'http',
  config: {
    base_url: 'https://example.test',
  },
  actions: [
    { name: 'Homepage', type: 'request', method: 'GET', target: '/home' },
    { name: 'Logout', type: 'request', method: 'GET', target: '/logout' },
  ],
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}

function sourceFileSelect(container: HTMLElement) {
  const select = container.querySelector('.source-map-toolbar select') as HTMLSelectElement | null
  if (!select) {
    throw new Error('Source file selector was not rendered')
  }
  return select
}

function generatedDslPane(container: HTMLElement) {
  const pane = container.querySelector('pre[aria-label="Generated DSL"]') as HTMLPreElement | null
  if (!pane) {
    throw new Error('Generated DSL pane was not rendered')
  }
  return pane
}

describe('SourceMappingReview', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => {
      root.unmount()
    })
    container.remove()
    vi.restoreAllMocks()
  })

  it('updates the generated DSL selection when the source file changes', async () => {
    await act(async () => {
      root.render(<SourceMappingReview sourceReview={sourceReview} script={script} />)
    })
    await flushPromises()

    expect(generatedDslPane(container).textContent).toContain('"name": "Homepage"')

    const select = sourceFileSelect(container)
    await act(async () => {
      select.value = 'vuser_end.c'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    const dslPane = generatedDslPane(container)
    expect(dslPane.textContent).toContain('"name": "Logout"')
    expect(dslPane.textContent).not.toContain('"name": "Homepage"')
    expect(container.textContent).toContain('actions[1]')
    expect(container.textContent).toContain('native noop')
  })

  it('applies APEX DSL semantic highlighting only to the generated DSL pane', async () => {
    await act(async () => {
      root.render(<SourceMappingReview sourceReview={sourceReview} script={script} />)
    })
    await flushPromises()

    const sourcePane = container.querySelector('pre[aria-label="Action.c source"]') as HTMLPreElement | null
    const dslPane = generatedDslPane(container)

    expect(sourcePane?.dataset.maxHeight).toBe('100%')
    expect(sourcePane?.dataset.semanticMode).toBe('')
    expect(dslPane.dataset.maxHeight).toBe('100%')
    expect(dslPane.dataset.semanticMode).toBe('apex-dsl')
  })

  it('exposes hover bubble copy on conversion coverage pills', async () => {
    await act(async () => {
      root.render(<SourceMappingReview sourceReview={sourceReview} script={script} />)
    })
    await flushPromises()

    const coverage = container.querySelector('.source-map-coverage')
    expect(coverage).not.toBeNull()
    const converted = Array.from(coverage?.querySelectorAll('.source-map-status') || []).find(pill => pill.textContent?.includes('Converted:')) as HTMLElement | undefined
    const preserved = Array.from(coverage?.querySelectorAll('.source-map-status') || []).find(pill => pill.textContent?.includes('Preserved:')) as HTMLElement | undefined
    const review = Array.from(coverage?.querySelectorAll('.source-map-status') || []).find(pill => pill.textContent?.includes('Needs review:')) as HTMLElement | undefined
    const unsupported = Array.from(coverage?.querySelectorAll('.source-map-status') || []).find(pill => pill.textContent?.includes('Unsupported:')) as HTMLElement | undefined
    const withoutOrigin = Array.from(coverage?.querySelectorAll('.source-map-status') || []).find(pill => pill.textContent?.includes('DSL without source origin:')) as HTMLElement | undefined

    expect(converted?.dataset.tooltip).toContain('converted into executable generated DSL')
    expect(converted?.getAttribute('title')).toBe(converted?.dataset.tooltip)
    expect(converted?.tabIndex).toBe(0)
    expect(preserved?.dataset.tooltip).toContain('supporting evidence')
    expect(review?.dataset.tooltip).toContain('reviewer attention')
    expect(unsupported?.dataset.tooltip).toContain('manual review')
    expect(withoutOrigin?.dataset.tooltip).toContain('no traceable source mapping')
  })

  it('filters the mapping table when a coverage pill is toggled', async () => {
    const mixedReview = {
      source_files: sourceReview.source_files,
      mappings: [
        { ...sourceReview.mappings[0] },
        {
          id: 'lr-think',
          source_file: 'Action.c',
          start_line: 4,
          end_line: 4,
          function: 'lr_think_time',
          target_path: '',
          target_name: '',
          target_kind: 'review',
          status: 'unsupported',
          support_mode: '',
        },
      ],
    }

    await act(async () => {
      root.render(<SourceMappingReview sourceReview={mixedReview} script={script} />)
    })
    await flushPromises()

    const rowText = () =>
      Array.from(container.querySelectorAll('.source-map-table tbody tr')).map(row => row.textContent || '')

    // Both mappings visible before any filter.
    expect(rowText().some(text => text.includes('web_url'))).toBe(true)
    expect(rowText().some(text => text.includes('lr_think_time'))).toBe(true)

    const unsupportedPill = Array.from(container.querySelectorAll('.source-map-status-filter')).find(
      pill => pill.textContent?.includes('Unsupported:'),
    ) as HTMLButtonElement | undefined
    expect(unsupportedPill).toBeDefined()

    await act(async () => {
      unsupportedPill?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(unsupportedPill?.getAttribute('aria-pressed')).toBe('true')
    expect(rowText().some(text => text.includes('lr_think_time'))).toBe(true)
    expect(rowText().some(text => text.includes('web_url'))).toBe(false)

    // Toggling the same pill again clears the filter.
    await act(async () => {
      unsupportedPill?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(unsupportedPill?.getAttribute('aria-pressed')).toBe('false')
    expect(rowText().some(text => text.includes('web_url'))).toBe(true)
  })

  it('disables coverage pills that have no matching mappings', async () => {
    await act(async () => {
      root.render(<SourceMappingReview sourceReview={sourceReview} script={script} />)
    })
    await flushPromises()

    const unsupportedPill = Array.from(container.querySelectorAll('.source-map-status-filter')).find(
      pill => pill.textContent?.includes('Unsupported:'),
    ) as HTMLButtonElement | undefined
    const convertedPill = Array.from(container.querySelectorAll('.source-map-status-filter')).find(
      pill => pill.textContent?.includes('Converted:'),
    ) as HTMLButtonElement | undefined

    // Fixture has no unsupported mappings, so that pill is disabled.
    expect(unsupportedPill?.disabled).toBe(true)
    expect(convertedPill?.disabled).toBe(false)
  })

  it('omits source files that have no mappings from the source file selector', async () => {
    await act(async () => {
      root.render(<SourceMappingReview sourceReview={sourceReview} script={script} />)
    })
    await flushPromises()

    const select = sourceFileSelect(container)
    const optionValues = Array.from(select.options).map(option => option.value)

    expect(optionValues).toEqual(['Action.c', 'vuser_end.c'])
    expect(optionValues).not.toContain('globals.h')
    expect(generatedDslPane(container).textContent).toContain('"name": "Homepage"')
  })

  it('renders source mappings with fixed review-table columns', async () => {
    await act(async () => {
      root.render(<SourceMappingReview sourceReview={sourceReview} script={script} />)
    })
    await flushPromises()

    const table = container.querySelector('.source-map-table')
    const columns = Array.from(table?.querySelectorAll('col') || []).map(col => col.className)
    const firstRow = table?.querySelector('tbody tr')

    expect(table?.getAttribute('aria-label')).toBe('Source conversion mappings')
    expect(container.querySelector('.source-map-summary')).toBeNull()
    expect(columns).toEqual([
      'source-map-col-source',
      'source-map-col-function',
      'source-map-col-target',
      'source-map-col-status',
    ])
    expect(firstRow?.querySelector('.source-map-source-cell')?.textContent).toContain('Action.c')
    expect(firstRow?.querySelector('.source-map-target-cell')?.textContent).toContain('actions[0]')
    expect(firstRow?.querySelector('.source-map-status-cell')).not.toBeNull()
  })

  it('uses a workspace maximize toggle in the source mapping title row', async () => {
    const handleToggle = vi.fn()
    await act(async () => {
      root.render(
        <SourceMappingReview
          onToggleWorkspaceMaximized={handleToggle}
          sourceReview={sourceReview}
          script={script}
          workspaceMaximized={false}
        />,
      )
    })
    await flushPromises()

    const maximizeButton = Array.from(container.querySelectorAll('.source-map-header button')).find(
      button => button.textContent?.trim() === 'Maximize',
    ) as HTMLButtonElement | undefined
    expect(container.querySelector('.source-map-summary')).toBeNull()
    expect(container.querySelector('.source-map-header .source-map-coverage')).not.toBeNull()
    expect(maximizeButton).toBeDefined()
    expect(maximizeButton?.getAttribute('aria-pressed')).toBe('false')

    await act(async () => {
      maximizeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(handleToggle).toHaveBeenCalledTimes(1)

    await act(async () => {
      root.render(
        <SourceMappingReview
          onToggleWorkspaceMaximized={handleToggle}
          sourceReview={sourceReview}
          script={script}
          workspaceMaximized
        />,
      )
    })

    const restoreButton = Array.from(container.querySelectorAll('.source-map-header button')).find(
      button => button.textContent?.trim() === 'Restore',
    ) as HTMLButtonElement | undefined
    expect(restoreButton).toBeDefined()
    expect(restoreButton?.getAttribute('aria-pressed')).toBe('true')
  })

  it('can hide the title while preserving source mapping controls', async () => {
    const handleToggle = vi.fn()
    await act(async () => {
      root.render(
        <SourceMappingReview
          onToggleWorkspaceMaximized={handleToggle}
          showTitle={false}
          sourceReview={sourceReview}
          script={script}
        />,
      )
    })
    await flushPromises()

    expect(container.querySelector('.source-map-header h4')).toBeNull()
    expect(container.querySelector('.source-map-header p')).toBeNull()
    expect(container.querySelector('.source-map-header')?.classList.contains('title-hidden')).toBe(true)
    expect(container.querySelector('.source-map-header .source-map-coverage')?.textContent).toContain('Converted:')
    expect(Array.from(container.querySelectorAll('.source-map-header button')).some(
      button => button.textContent?.trim() === 'Maximize',
    )).toBe(true)
  })

  it('keeps the mapping table visible above the source code panes', async () => {
    const stylesheet = await readSourceMappingStylesheet()
    const nonShrinkingSections = cssRule(stylesheet, '.source-map-header,\n.source-map-toolbar,\n.source-map-table-wrap')
    const headerControls = cssRule(stylesheet, '.source-map-header-controls')
    const hiddenTitleControls = cssRule(stylesheet, '.source-map-header.title-hidden .source-map-header-controls')
    const coverageRule = cssRule(stylesheet, '.source-map-coverage')
    const coverageStatusRule = cssRule(stylesheet, '.source-map-coverage .source-map-status')
    const sourcePanels = cssRule(stylesheet, '.source-map-panels')
    const linkedCodeEditor = cssRule(stylesheet, '.linked-code-pane .cm-editor')
    const sourceCodeFallback = cssRule(stylesheet, '.source-map-code,\n.source-map-dsl')

    expect(nonShrinkingSections).toContain('flex: 0 0 auto;')
    expect(headerControls).toContain('display: flex;')
    expect(headerControls).toContain('flex-wrap: nowrap;')
    expect(headerControls).toContain('align-items: center;')
    expect(hiddenTitleControls).toContain('justify-content: space-between;')
    expect(coverageRule).toContain('flex-wrap: nowrap;')
    expect(coverageRule).toContain('overflow-x: auto;')
    expect(coverageStatusRule).toContain('white-space: nowrap;')
    expect(stylesheet).toContain('.source-map-table-wrap {\n  display: block;\n  min-height: 120px;')
    expect(stylesheet).toContain('max-height: 220px;')
    expect(sourcePanels).toContain('height: 420px;')
    expect(sourcePanels).toContain('min-height: 360px;')
    expect(sourcePanels).toContain('max-height: min(620px, 58vh);')
    expect(linkedCodeEditor).toContain('font-size: 13px;')
    expect(sourceCodeFallback).toContain('font-size: 13px;')
    expect(stylesheet).toContain('.linked-code-pane > div {')
    expect(stylesheet).toContain('height: 100%;')
    expect(stylesheet).toContain('.linked-code-pane .cm-scroller {\n  overflow: auto;')
  })

  it('uses Reset for returning a maximized source or DSL pane to the split view', async () => {
    await act(async () => {
      root.render(<SourceMappingReview sourceReview={sourceReview} script={script} />)
    })
    await flushPromises()

    const sourceMaximize = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Maximize') as HTMLButtonElement | undefined
    expect(sourceMaximize).toBeDefined()
    if (!sourceMaximize) return

    await act(async () => {
      sourceMaximize.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Reset')
    expect(container.textContent).not.toContain('Restore')
  })

  it('scrolls selected mapping rows only within the mapping table scroller', () => {
    const tableScroller = document.createElement('div')
    const row = document.createElement('tr')

    Object.defineProperty(tableScroller, 'clientHeight', { configurable: true, value: 120 })
    Object.defineProperty(row, 'offsetTop', { configurable: true, value: 260 })
    Object.defineProperty(row, 'offsetHeight', { configurable: true, value: 24 })

    scrollMappingRowIntoTableView(tableScroller, row)
    expect(tableScroller.scrollTop).toBe(164)

    tableScroller.scrollTop = 200
    Object.defineProperty(row, 'offsetTop', { configurable: true, value: 80 })

    scrollMappingRowIntoTableView(tableScroller, row)
    expect(tableScroller.scrollTop).toBe(80)
  })

  it('leaves the mapping table scroller alone when the selected row is already visible', () => {
    const tableScroller = document.createElement('div')
    const row = document.createElement('tr')

    Object.defineProperty(tableScroller, 'clientHeight', { configurable: true, value: 120 })
    Object.defineProperty(row, 'offsetTop', { configurable: true, value: 140 })
    Object.defineProperty(row, 'offsetHeight', { configurable: true, value: 24 })
    tableScroller.scrollTop = 100

    scrollMappingRowIntoTableView(tableScroller, row)
    expect(tableScroller.scrollTop).toBe(100)
  })

  it('preserves the selected mapping when equivalent source review data is re-created upstream', async () => {
    await act(async () => {
      root.render(<SourceMappingReview sourceReview={sourceReview} script={script} />)
    })
    await flushPromises()

    const logoutRow = container.querySelector('tr[data-mapping-id="logout"]') as HTMLTableRowElement | null
    expect(logoutRow).not.toBeNull()
    if (!logoutRow) return

    await act(async () => {
      logoutRow.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(container.querySelector('tr.active')?.getAttribute('data-mapping-id')).toBe('logout')

    const clonedSourceReview = JSON.parse(JSON.stringify(sourceReview))
    await act(async () => {
      root.render(<SourceMappingReview sourceReview={clonedSourceReview} script={script} />)
    })
    await flushPromises()

    expect(container.querySelector('tr.active')?.getAttribute('data-mapping-id')).toBe('logout')
  })

  it('keeps a clicked table row from snapping back to an earlier external focus target', async () => {
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback: FrameRequestCallback) => {
      callback(0)
      return 1
    })

    await act(async () => {
      root.render(
        <SourceMappingReview
          focusedMappingId="logout"
          focusRequestId={1}
          sourceReview={sourceReview}
          script={script}
        />,
      )
    })
    await flushPromises()

    const tableScroller = container.querySelector('.source-map-table-wrap') as HTMLDivElement | null
    const homepageRow = container.querySelector('tr[data-mapping-id="homepage"]') as HTMLTableRowElement | null
    const logoutRow = container.querySelector('tr[data-mapping-id="logout"]') as HTMLTableRowElement | null

    expect(tableScroller).not.toBeNull()
    expect(homepageRow).not.toBeNull()
    expect(logoutRow).not.toBeNull()
    if (!tableScroller || !homepageRow || !logoutRow) return

    Object.defineProperty(tableScroller, 'clientHeight', { configurable: true, value: 20 })
    Object.defineProperty(homepageRow, 'offsetTop', { configurable: true, value: 0 })
    Object.defineProperty(homepageRow, 'offsetHeight', { configurable: true, value: 20 })
    Object.defineProperty(logoutRow, 'offsetTop', { configurable: true, value: 100 })
    Object.defineProperty(logoutRow, 'offsetHeight', { configurable: true, value: 20 })
    tableScroller.scrollTop = 0

    await act(async () => {
      homepageRow.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(tableScroller.scrollTop).toBe(0)

    await act(async () => {
      root.render(
        <SourceMappingReview
          focusedMappingId="logout"
          focusRequestId={1}
          sourceReview={{ ...sourceReview, mappings: [...sourceReview.mappings] }}
          script={script}
        />,
      )
    })
    await flushPromises()

    expect(container.querySelector('tr.active')?.getAttribute('data-mapping-id')).toBe('homepage')
  })
})
