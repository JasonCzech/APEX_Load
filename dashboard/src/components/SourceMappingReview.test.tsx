// @vitest-environment jsdom

import { act, StrictMode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SourceMappingReview from './SourceMappingReview'
import { ImportSourceIssueFocusContext } from './ImportSourceIssueFocus'
import { scrollMappingRowIntoTableView } from './sourceMappingScroll'
import { CODE_LINE_WRAP_STORAGE_KEY } from './codeLineWrapPreference'

const { downloadTextFileMock } = vi.hoisted(() => ({
  downloadTextFileMock: vi.fn(),
}))

vi.mock('../utils/fileUtils', () => ({
  downloadTextFile: downloadTextFileMock,
}))

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
    contextLines,
    semanticMode,
    maxHeight,
    wrapLines,
  }: {
    value: string
    ariaLabel?: string
    activeRange?: { startLine: number; endLine: number } | null
    contextLines?: number
    semanticMode?: string
    maxHeight?: string
    wrapLines?: boolean
  }) => {
    const activeText = activeRange
      ? value.split('\n').slice(activeRange.startLine - 1, activeRange.endLine).join('\n')
      : ''
    return (
      <pre
        aria-label={ariaLabel}
        data-active-range={activeRange ? `${activeRange.startLine}-${activeRange.endLine}` : ''}
        data-context-lines={contextLines ?? ''}
        data-max-height={maxHeight || ''}
        data-semantic-mode={semanticMode || ''}
        data-wrap-lines={wrapLines === false ? 'false' : 'true'}
      >
        {contextLines == null && ariaLabel?.endsWith(' source') ? value : activeText || value}
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
    await new Promise(resolve => setTimeout(resolve, 30))
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

  it('keeps the selected comparison available while navigating and filtering mappings', async () => {
    await act(async () => { root.render(<SourceMappingReview sourceReview={sourceReview} script={script} />) })
    await flushPromises()
    const workspace = container.querySelector('.source-map-workspace')!
    const comparison = workspace.querySelector('.source-map-comparison')!
    expect(comparison.querySelector('[aria-label="Generated DSL"]')).not.toBeNull()
    expect(container.querySelector<HTMLDetailsElement>('.source-map-view-options')?.open).toBe(false)
    const show = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.startsWith('Show mappings'))!
    await act(async () => { show.click() })
    expect(workspace.classList.contains('with-navigator')).toBe(true)
    const logout = workspace.querySelector<HTMLButtonElement>('[data-mapping-id="logout"] button')!
    await act(async () => { logout.click() })
    await flushPromises()
    expect(sourceFileSelect(container).value).toBe('vuser_end.c')
    expect(comparison.querySelector('[aria-label="Generated DSL"]')?.textContent).toContain('/logout')
    const hide = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Hide mappings')!
    await act(async () => { hide.click() })
    expect(workspace.classList.contains('with-navigator')).toBe(false)
    expect(comparison.querySelector('[aria-label="Generated DSL"]')?.textContent).toContain('/logout')
  })

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    downloadTextFileMock.mockReset()
    downloadTextFileMock.mockResolvedValue('saved')
    window.localStorage.removeItem(CODE_LINE_WRAP_STORAGE_KEY)
  })

  afterEach(() => {
    act(() => {
      root.unmount()
    })
    container.remove()
    vi.restoreAllMocks()
    window.localStorage.removeItem(CODE_LINE_WRAP_STORAGE_KEY)
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

  it('shows commented-out source sections in the default code review view', async () => {
    const commentedSource = [
      'Action()',
      '{',
      '  web_url("Homepage", LAST);',
      '}',
      '',
      '',
      '',
      '/*',
      '// Disabled legacy flow remains reviewable.',
      'web_url("Old checkout", "URL=http://example.test/old", LAST);',
      '*/',
    ].join('\n')
    const review = {
      source_files: [{ path: 'Action.c', language: 'c', content: commentedSource }],
      mappings: [{
        id: 'homepage', source_file: 'Action.c', start_line: 3, end_line: 3,
        function: 'web_url', target_path: 'actions[0]', target_name: 'Homepage',
        target_kind: 'action', status: 'converted',
      }],
    }

    await act(async () => {
      root.render(<SourceMappingReview sourceReview={review as never} script={script} />)
    })
    await flushPromises()

    const sourcePane = container.querySelector('pre[aria-label="Action.c source"]')
    expect(sourcePane?.getAttribute('data-context-lines')).toBe('')
    expect(sourcePane?.textContent).toContain('// Disabled legacy flow remains reviewable.')
    expect(sourcePane?.textContent).toContain('web_url("Old checkout"')
  })

  it('toggles word wrapping for both native source and generated DSL panes', async () => {
    await act(async () => {
      root.render(<SourceMappingReview sourceReview={sourceReview} script={script} />)
    })
    await flushPromises()

    const toggle = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Word Wrap: On')
    expect(toggle).toBeDefined()
    expect(container.querySelectorAll('[data-wrap-lines="true"]')).toHaveLength(2)

    await act(async () => {
      toggle?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelectorAll('[data-wrap-lines="false"]')).toHaveLength(2)
    expect(window.localStorage.getItem(CODE_LINE_WRAP_STORAGE_KEY)).toBe('off')
  })

  it('selects the canonical mapped source when duplicate basenames exist regardless of file order', async () => {
    const duplicateBasenameReview = {
      source_files: [
        {
          path: 'aaa-backup/Actions.java',
          language: 'java',
          content: 'class Actions {\n  // stale backup\n}',
        },
        {
          path: 'src/Actions.java',
          language: 'java',
          content: 'class Actions {\n  // governed source\n}',
        },
      ],
      mappings: [
        {
          id: 'java-action',
          source_file: 'src/Actions.java',
          start_line: 2,
          end_line: 2,
          function: 'action',
          target_path: 'actions[0]',
          target_name: 'Homepage',
          target_kind: 'action',
          status: 'converted',
          support_mode: 'external_required',
        },
      ],
    }

    await act(async () => {
      root.render(<SourceMappingReview sourceReview={duplicateBasenameReview} script={script} />)
    })
    await flushPromises()

    expect(Array.from(sourceFileSelect(container).options).map(option => option.value)).toEqual([
      'aaa-backup/Actions.java',
      'src/Actions.java',
    ])
    expect(container.querySelector('pre[aria-label="aaa-backup/Actions.java source"]')).toBeNull()
    expect(container.querySelector('pre[aria-label="src/Actions.java source"]')?.textContent).toContain('governed source')

    await act(async () => {
      root.render(
        <SourceMappingReview
          sourceReview={{
            ...duplicateBasenameReview,
            source_files: [...duplicateBasenameReview.source_files].reverse(),
          }}
          script={script}
        />,
      )
    })
    await flushPromises()

    expect(sourceFileSelect(container).value).toBe('src/Actions.java')
    expect(container.querySelector('pre[aria-label="src/Actions.java source"]')?.textContent).toContain('governed source')
  })

  it('realigns the source pane when a surviving mapping ID changes its canonical file', async () => {
    const sourceFiles = [
      {
        path: 'aaa/Actions.java',
        language: 'java',
        content: 'class Actions { /* first */ }',
      },
      {
        path: 'src/Actions.java',
        language: 'java',
        content: 'class Actions { /* second */ }',
      },
    ]
    const reviewFor = (sourceFile: string) => ({
      source_files: sourceFiles,
      mappings: [
        {
          id: 'stable-action',
          source_file: sourceFile,
          start_line: 1,
          end_line: 1,
          function: 'action',
          target_path: 'actions[0]',
          target_name: 'Homepage',
          target_kind: 'action',
          status: 'converted',
          support_mode: 'external_required',
        },
      ],
    })

    await act(async () => {
      root.render(<SourceMappingReview sourceReview={reviewFor('aaa/Actions.java')} script={script} />)
    })
    await flushPromises()
    expect(sourceFileSelect(container).value).toBe('aaa/Actions.java')

    await act(async () => {
      root.render(<SourceMappingReview sourceReview={reviewFor('src/Actions.java')} script={script} />)
    })
    await flushPromises()

    expect(sourceFileSelect(container).value).toBe('src/Actions.java')
    expect(container.querySelector('pre[aria-label="aaa/Actions.java source"]')).toBeNull()
    expect(container.querySelector('pre[aria-label="src/Actions.java source"]')?.textContent).toContain('second')
  })

  it('fails closed for an ambiguous basename mapping but preserves explicit file inspection', async () => {
    const ambiguousReview = {
      source_files: [
        {
          path: 'aaa-backup/Actions.java',
          language: 'java',
          content: 'class Actions { /* stale backup */ }',
        },
        {
          path: 'src/Actions.java',
          language: 'java',
          content: 'class Actions { /* governed source */ }',
        },
      ],
      mappings: [
        {
          id: 'ambiguous-java-action',
          source_file: 'Actions.java',
          start_line: 1,
          end_line: 1,
          function: 'action',
          target_path: 'actions[0]',
          target_name: 'Homepage',
          target_kind: 'action',
          status: 'review_only',
          support_mode: 'external_required',
        },
      ],
    }

    await act(async () => {
      root.render(<SourceMappingReview sourceReview={ambiguousReview} script={script} />)
    })
    await flushPromises()

    expect(sourceFileSelect(container).value).toBe('')
    expect(container.querySelector('pre[aria-label$=" source"]')).toBeNull()
    expect(container.textContent).toContain('No source file content was captured.')

    const select = sourceFileSelect(container)
    await act(async () => {
      select.value = 'aaa-backup/Actions.java'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    expect(sourceFileSelect(container).value).toBe('aaa-backup/Actions.java')
    const sourcePane = container.querySelector('pre[aria-label="aaa-backup/Actions.java source"]')
    expect(sourcePane?.textContent).toContain('stale backup')
    expect(sourcePane?.getAttribute('data-active-range')).toBe('')
    expect(container.querySelector('tr[data-mapping-id="ambiguous-java-action"]')?.classList).not.toContain('active')
  })

  it('permits exact inspection of case-colliding files without attributing the ambiguous mapping', async () => {
    const collidingReview = {
      source_files: [
        {
          path: 'Src/Actions.java',
          language: 'java',
          content: 'class Actions { /* upper path */ }',
        },
        {
          path: 'src/Actions.java',
          language: 'java',
          content: 'class Actions { /* lower path */ }',
        },
      ],
      mappings: [
        {
          id: 'case-colliding-action',
          source_file: 'src/Actions.java',
          start_line: 1,
          end_line: 1,
          function: 'action',
          target_path: 'actions[0]',
          target_name: 'Homepage',
          target_kind: 'action',
          status: 'review_only',
          support_mode: 'external_required',
        },
      ],
    }

    await act(async () => {
      root.render(<SourceMappingReview sourceReview={collidingReview} script={script} />)
    })
    await flushPromises()

    expect(sourceFileSelect(container).value).toBe('')

    const select = sourceFileSelect(container)
    await act(async () => {
      select.value = 'Src/Actions.java'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    expect(sourceFileSelect(container).value).toBe('Src/Actions.java')
    const sourcePane = container.querySelector('pre[aria-label="Src/Actions.java source"]')
    expect(sourcePane?.textContent).toContain('upper path')
    expect(sourcePane?.getAttribute('data-active-range')).toBe('')
    expect(container.querySelector('tr[data-mapping-id="case-colliding-action"]')?.classList).not.toContain('active')
  })

  it('keeps unresolved mapping candidates inspectable alongside a resolved source', async () => {
    const mixedReview = {
      source_files: [
        {
          path: 'src/Known.java',
          language: 'java',
          content: 'class Known { /* mapped */ }',
        },
        {
          path: 'a/Actions.java',
          language: 'java',
          content: 'class Actions { /* candidate a */ }',
        },
        {
          path: 'b/Actions.java',
          language: 'java',
          content: 'class Actions { /* candidate b */ }',
        },
        {
          path: 'backup/Unrelated.java',
          language: 'java',
          content: 'class Unrelated {}',
        },
      ],
      mappings: [
        {
          id: 'known-mapping',
          source_file: 'src/Known.java',
          start_line: 1,
          end_line: 1,
          function: 'known',
          target_path: 'actions[0]',
          target_kind: 'action',
          status: 'converted',
          support_mode: 'external_required',
        },
        {
          id: 'ambiguous-actions',
          source_file: 'Actions.java',
          start_line: 1,
          end_line: 1,
          function: 'action',
          target_path: 'actions[1]',
          target_kind: 'action',
          status: 'review_only',
          support_mode: 'external_required',
        },
      ],
    }

    await act(async () => {
      root.render(<SourceMappingReview sourceReview={mixedReview} script={script} />)
    })
    await flushPromises()

    expect(Array.from(sourceFileSelect(container).options).map(option => option.value)).toEqual([
      'src/Known.java',
      'a/Actions.java',
      'b/Actions.java',
      'backup/Unrelated.java',
    ])

    const select = sourceFileSelect(container)
    await act(async () => {
      select.value = 'b/Actions.java'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    expect(sourceFileSelect(container).value).toBe('b/Actions.java')
    const sourcePane = container.querySelector('pre[aria-label="b/Actions.java source"]')
    expect(sourcePane?.textContent).toContain('candidate b')
    expect(sourcePane?.getAttribute('data-active-range')).toBe('')
    expect(container.querySelector('tr.active')).toBeNull()
  })

  it('does not redirect a mapping to a captured backup when its canonical source was omitted', async () => {
    const boundedReview = {
      total_source_files: 2,
      omitted_source_files: ['src/Actions.java'],
      source_files: [
        {
          path: 'aaa-backup/Actions.java',
          language: 'java',
          content: 'class Actions { /* stale backup */ }',
        },
      ],
      mappings: [
        {
          id: 'omitted-java-action',
          source_file: 'src/Actions.java',
          start_line: 1,
          end_line: 1,
          function: 'action',
          target_path: 'actions[0]',
          target_name: 'Homepage',
          target_kind: 'action',
          status: 'review_only',
          support_mode: 'external_required',
        },
      ],
    }

    await act(async () => {
      root.render(<SourceMappingReview sourceReview={boundedReview} script={script} />)
    })
    await flushPromises()

    expect(sourceFileSelect(container).value).toBe('')
    expect(container.querySelector('pre[aria-label="aaa-backup/Actions.java source"]')).toBeNull()
    expect(container.textContent).toContain('No source file content was captured.')
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
    const converted = Array.from(coverage?.querySelectorAll('.source-map-status') || []).find(pill => pill.textContent?.includes('Converted mappings:')) as HTMLElement | undefined
    const preserved = Array.from(coverage?.querySelectorAll('.source-map-status') || []).find(pill => pill.textContent?.includes('Preserved mappings:')) as HTMLElement | undefined
    const review = Array.from(coverage?.querySelectorAll('.source-map-status') || []).find(pill => pill.textContent?.includes('Needs review:')) as HTMLElement | undefined
    const unsupported = Array.from(coverage?.querySelectorAll('.source-map-status') || []).find(pill => pill.textContent?.includes('Unsupported:')) as HTMLElement | undefined
    const withoutOrigin = Array.from(coverage?.querySelectorAll('.source-map-status') || []).find(pill => pill.textContent?.includes('DSL without source origin:')) as HTMLElement | undefined

    for (const [control, copy] of [[converted, 'converted into executable generated DSL'], [preserved, 'supporting evidence'], [review, 'reviewer attention'], [unsupported, 'manual review'], [withoutOrigin, 'no traceable source mapping']] as const) {
      expect(control?.hasAttribute('title')).toBe(false)
      await act(async () => { ((control as HTMLButtonElement)?.disabled ? control?.parentElement : control)?.focus() })
      expect(document.querySelector('[role="tooltip"]')?.textContent).toContain(copy)
      await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })))
    }
  })

  it('explains preview limits separately from import issues and opens a shortened file', async () => {
    await act(async () => { root.render(<SourceMappingReview script={script} sourceReview={{
      ...sourceReview,
      total_source_files: 4,
      omitted_source_files: ['support/extra.dat'],
      source_files: sourceReview.source_files.map(file => ({ ...file, truncated: file.path === 'globals.h' })),
    }} />) })
    await flushPromises()
    const notice = container.querySelector('[aria-label="Source preview limits"]')!
    expect(notice.getAttribute('role')).toBe('note')
    expect(notice.querySelector('[role="alert"]')).toBeNull()
    expect(notice.textContent).toContain('do not truncate the imported script or remove data from the import')
    expect(notice.textContent).toContain('Any conversion issues are reported separately')
    const details = notice.querySelector('details')!
    expect(details.open).toBe(false)
    await act(async () => { details.querySelector('summary')!.click() })
    expect(details.open).toBe(true)
    const rows = Array.from(notice.querySelectorAll('li'))
    expect(rows).toHaveLength(2)
    expect(rows[0].textContent).toContain('globals.hShortened in preview')
    expect(rows[1].textContent).toContain('support/extra.datNot shown in preview')
    expect(rows[1].querySelector('button')).toBeNull()
    await act(async () => { rows[0].querySelector('button')!.click() })
    await flushPromises()
    expect(sourceFileSelect(container).value).toBe('globals.h')
  })

  it('still reports inconsistent preview file counts as a separate alert', async () => {
    await act(async () => { root.render(<SourceMappingReview script={script} sourceReview={{ ...sourceReview, total_source_files: 4 }} />) })
    await flushPromises()
    expect(container.querySelector('.source-map-preview-integrity[role="alert"]')?.textContent).toContain('Review file counts are inconsistent')
    expect(container.querySelector('.source-map-preview-files')).toBeNull()
  })

  it('warns when the bounded source-review payload omits or truncates executable source', async () => {
    const digest = `sha256:${'a'.repeat(64)}`
    const incompleteReview = {
      ...sourceReview,
      total_source_files: 5,
      omitted_source_files: ['Helper.java', 'Support.java'],
      source_files: sourceReview.source_files.map((file, index) => index === 0 ? { ...file, path: 'Actions.java', language: 'java', truncated: true } : file),
    }
    await act(async () => {
      root.render(<SourceMappingReview
        sourceReview={incompleteReview}
        script={{
          ...script,
          protocol: 'java_vuser',
          metadata: {
            import: {
              preserved: {
                review_artifact: {
                  metadata: {
                    java_source_manifest: {
                      contract: 'apexload.java_vuser.source_manifest.v1',
                      digest,
                      file_count: 5,
                      total_bytes: 123456,
                    },
                  },
                },
              },
            },
          },
        }}
      />)
    })
    await flushPromises()

    const warning = container.querySelector('.source-map-incomplete')
    expect(warning).not.toBeNull()
    expect(warning?.textContent).toContain('Preview limits only — not an import issue')
    expect(warning?.querySelectorAll('li')).toHaveLength(3)
    expect(warning?.textContent).toContain('Shortened in preview')
    expect(warning?.textContent).toContain('out-of-band review of the governed original archive before approval')
    const manifest = container.querySelector('[aria-label="Java full-source manifest"]')
    expect(manifest?.textContent).toContain('5 Java files')
    expect(manifest?.textContent).toContain('123,456 bytes')
    expect(manifest?.textContent).toContain(digest)
  })

  it('blocks the review guidance when a bounded Java preview has no full-source manifest', async () => {
    const incompleteReview = {
      ...sourceReview,
      total_source_files: 4,
      omitted_source_files: ['Helper.java'],
      source_files: sourceReview.source_files.map((file, index) => index === 0 ? { ...file, path: 'Actions.java', language: 'java', truncated: true } : file),
    }
    await act(async () => {
      root.render(<SourceMappingReview sourceReview={incompleteReview} script={{ ...script, protocol: 'java_vuser' }} />)
    })
    await flushPromises()

    expect(container.querySelector('.source-map-incomplete')?.textContent).toContain('full Java source manifest is missing; re-import before approval')
    expect(container.querySelector('[aria-label="Java full-source manifest"]')).toBeNull()
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
      pill => pill.textContent?.includes('Converted mappings:'),
    ) as HTMLButtonElement | undefined

    // Fixture has no unsupported mappings, so that pill is disabled.
    expect(unsupportedPill?.disabled).toBe(true)
    expect(convertedPill?.disabled).toBe(false)
  })

  it('includes captured source files with no mappings for inspection', async () => {
    await act(async () => {
      root.render(<SourceMappingReview sourceReview={sourceReview} script={script} />)
    })
    await flushPromises()

    const select = sourceFileSelect(container)
    const optionValues = Array.from(select.options).map(option => option.value)

    expect(optionValues).toEqual(['Action.c', 'vuser_end.c', 'globals.h'])
    expect(optionValues).toContain('globals.h')
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

  it('uses the bounded download helper and surfaces a desktop export failure', async () => {
    downloadTextFileMock.mockRejectedValueOnce(new Error('generated export exceeds the desktop limit'))
    await act(async () => {
      root.render(<SourceMappingReview sourceReview={sourceReview} script={script} />)
    })
    await flushPromises()

    const exportButton = Array.from(container.querySelectorAll('button')).find(
      button => button.textContent?.trim() === 'Export filtered mappings',
    )
    await act(async () => {
      exportButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(downloadTextFileMock).toHaveBeenCalledOnce()
    expect(downloadTextFileMock).toHaveBeenCalledWith(
      expect.stringContaining('Source File,Start Line,Function'),
      'source_mapping_review_export.csv',
      'text/csv;charset=utf-8;',
    )
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('exceeds the desktop limit')
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
      button => button.textContent?.trim() === 'Expand workspace',
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
      button => button.textContent?.trim() === 'Restore workspace',
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
    expect(container.querySelector('.source-map-header .source-map-coverage')?.textContent).toContain('Converted mappings:')
    expect(Array.from(container.querySelectorAll('.source-map-header button')).some(
      button => button.textContent?.trim() === 'Expand workspace',
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
    expect(headerControls).toContain('flex-wrap: wrap;')
    expect(headerControls).toContain('align-items: center;')
    expect(hiddenTitleControls).toContain('justify-content: space-between;')
    expect(coverageRule).toContain('flex-wrap: wrap;')
    expect(coverageRule).toContain('overflow: visible;')
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

    const sourceMaximize = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Expand source') as HTMLButtonElement | undefined
    expect(sourceMaximize).toBeDefined()
    if (!sourceMaximize) return

    await act(async () => {
      sourceMaximize.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Return to comparison')
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

  it('focuses a runtime source file without a request mapping and retains the operator selection', async () => {
    const withRuntime = { ...sourceReview, source_files: [...sourceReview.source_files, { path: 'default.cfg', language: 'text', content: '[Browser]\nUseCustomAgent=1' }] }
    await act(async () => { root.render(<SourceMappingReview sourceReview={withRuntime} script={script} focusedSourceFile="default.cfg" focusRequestId={1} />) })
    await flushPromises()
    const select = container.querySelector('.source-map-toolbar select') as HTMLSelectElement
    expect(select.value).toBe('default.cfg')
    expect(container.textContent).toContain('UseCustomAgent=1')
    await act(async () => { select.value = sourceReview.source_files[0].path; select.dispatchEvent(new Event('change', { bubbles: true })) })
    await act(async () => { root.render(<SourceMappingReview sourceReview={{ ...withRuntime }} script={script} focusedSourceFile="default.cfg" focusRequestId={1} />) })
    expect(select.value).toBe(sourceReview.source_files[0].path)
  })

  it('shows the reported source line without selecting unrelated mappings on that line', async () => {
    const sample = {
      source_files: [{ path: 'sample.json', language: 'json', content: '{\n "request": {\n  "method": "POST",\n  "url": "/session",\n  "headers": {},\n  "body": {}\n }\n}' }],
      mappings: [
        { id: 'response_status', source_file: 'sample.json', start_line: 2, end_line: 7, function: 'response_status', target_path: 'actions[0]', status: 'converted' },
        { id: 'request', source_file: 'sample.json', start_line: 5, end_line: 5, function: 'rest_json_request', target_path: 'actions[0]', status: 'converted' },
      ],
    }
    await act(async () => {
      root.render(<ImportSourceIssueFocusContext.Provider value={{ revision: 1, title: 'Confirm synthesized source order', source: 'Import conversion', description: 'headers at line:5 [info]: order was synthesized' }}>
        <SourceMappingReview sourceReview={sample} script={script} />
      </ImportSourceIssueFocusContext.Provider>)
    })
    await flushPromises()
    expect(container.querySelector('.source-map-issue-focus')?.textContent).toContain('sample.json:5')
    expect(container.querySelector('tr.active')).toBeNull()
    expect(container.querySelector('[aria-label="sample.json source"]')?.getAttribute('data-active-range')).toBe('5-5')
    expect(container.querySelector('[aria-label="Generated DSL"]')).toBeNull()
    expect(container.textContent).toContain('No destination mapping is linked to this finding')
  })

  it('retains the requested mapping when StrictMode replays mount effects', async () => {
    await act(async () => {
      root.render(<StrictMode><SourceMappingReview focusedMappingId="logout" focusRequestId={1} sourceReview={sourceReview} script={script} /></StrictMode>)
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

  it('does not run global review shortcuts while typing in editable content or while hidden', async () => {
    const reviewMappings = sourceReview.mappings.map(mapping => ({ ...mapping, status: 'review_only' }))
    await act(async () => {
      root.render(
        <div>
          <div contentEditable="true" data-testid="editor">editing</div>
          <SourceMappingReview sourceReview={{ ...sourceReview, mappings: reviewMappings }} script={script} />
        </div>,
      )
    })
    await flushPromises()
    expect(container.querySelector('tr.active')?.getAttribute('data-mapping-id')).toBe('homepage')

    const editor = container.querySelector<HTMLElement>('[data-testid="editor"]')
    editor?.focus()
    await act(async () => window.dispatchEvent(new KeyboardEvent('keydown', { key: ']' })))
    expect(container.querySelector('tr.active')?.getAttribute('data-mapping-id')).toBe('homepage')

    await act(async () => {
      root.render(
        <div hidden>
          <SourceMappingReview sourceReview={{ ...sourceReview, mappings: reviewMappings }} script={script} />
        </div>,
      )
    })
    await flushPromises()
    await act(async () => window.dispatchEvent(new KeyboardEvent('keydown', { key: ']' })))
    expect(container.querySelector('tr.active')?.getAttribute('data-mapping-id')).toBe('homepage')
  })
})
