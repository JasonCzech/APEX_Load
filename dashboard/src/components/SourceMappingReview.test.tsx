// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SourceMappingReview from './SourceMappingReview'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('./LinkedCodePane', () => ({
  default: ({
    value,
    ariaLabel,
    activeRange,
  }: {
    value: string
    ariaLabel?: string
    activeRange?: { startLine: number; endLine: number } | null
  }) => {
    const activeText = activeRange
      ? value.split('\n').slice(activeRange.startLine - 1, activeRange.endLine).join('\n')
      : ''
    return (
      <pre aria-label={ariaLabel} data-active-range={activeRange ? `${activeRange.startLine}-${activeRange.endLine}` : ''}>
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
    },
  ],
}

const script = {
  name: 'Imported LoadRunner Script',
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
  })

  it('clears the stale DSL selection when the selected source file has no mapping', async () => {
    await act(async () => {
      root.render(<SourceMappingReview sourceReview={sourceReview} script={script} />)
    })
    await flushPromises()

    const select = sourceFileSelect(container)
    await act(async () => {
      select.value = 'globals.h'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    const dslPane = generatedDslPane(container)
    expect(dslPane.dataset.activeRange).toBe('')

    const copyDsl = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy DSL')) as HTMLButtonElement | undefined
    expect(copyDsl).toBeDefined()
    expect(copyDsl?.disabled).toBe(true)
  })
})
