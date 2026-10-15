// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SourceMappingReview from './SourceMappingReview'
import { clearSourceReviewFullTextCache } from './useSourceReviewFullText'
import { type SourceReview, type SourceReviewFile } from '../types/scripts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const { requestMock } = vi.hoisted(() => ({ requestMock: vi.fn() }))

vi.mock('../hooks/apiClient', async importOriginal => ({
  ...await importOriginal<typeof import('../hooks/apiClient')>(),
  request: requestMock,
}))

vi.mock('./LinkedCodePane', () => ({
  default: ({ value, ariaLabel, activeRange, revealRequestId }: {
    value: string
    ariaLabel?: string
    activeRange?: { startLine: number; endLine: number } | null
    revealRequestId?: number
  }) => (
    <pre
      aria-label={ariaLabel}
      data-active-range={activeRange ? `${activeRange.startLine}-${activeRange.endLine}` : ''}
      data-reveal-request={revealRequestId ?? ''}
    >
      {value}
    </pre>
  ),
}))

const SHA = 'a'.repeat(64)
const FULL = 'Action()\n{\n  lr_start_transaction("t");\n  web_url("Home", LAST);\n  web_url("Deep", LAST);\n}'
const PREVIEW = 'Action()\n{\n  lr_start_transaction("t");\n\n[truncated for source review]'

function review(file: Partial<SourceReviewFile> = {}): SourceReview {
  return {
    source_files: [
      { path: 'Action.c', language: 'c', content: PREVIEW, truncated: true, original_bytes: FULL.length, full_sha256: SHA, full_bytes: FULL.length, full_lines: 6, ...file },
      { path: 'pre_cci.c', language: 'c', content: 'int x;\n\n[truncated for source review]', truncated: true, original_bytes: 900_000 },
    ],
    mappings: [{
      id: 'deep', source_file: 'Action.c', start_line: 5, end_line: 5, function: 'web_url',
      target_path: 'actions[0]', target_name: 'Deep', target_kind: 'action', status: 'converted', support_mode: 'executable',
    }],
  } as SourceReview
}

const script = {
  name: 'Imported', version: '1.0', protocol: 'http', project_id: 'project-1',
  config: { base_url: 'https://example.test' },
  actions: [{ name: 'Deep', type: 'request', method: 'GET', target: '/deep' }],
}

function sourcePane(container: HTMLElement) {
  return container.querySelector<HTMLPreElement>('pre[aria-label="Action.c source"]')!
}

function note(container: HTMLElement) {
  return container.querySelector('.source-map-text-limit')?.textContent || ''
}

async function flush() {
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)) })
}

describe('SourceMappingReview full source text', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    requestMock.mockReset()
    clearSourceReviewFullTextCache()
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('loads a truncated file in full and reveals a mapped line past its preview', async () => {
    let resolve: (value: unknown) => void = () => {}
    requestMock.mockReturnValue(new Promise(done => { resolve = done }))
    await act(async () => { root.render(<SourceMappingReview sourceReview={review()} script={script} />) })

    expect(requestMock).toHaveBeenCalledWith(`/projects/project-1/source-review/texts/${SHA}`, expect.objectContaining({ maxResponseBytes: 40 << 20 }))
    expect(sourcePane(container).textContent).toBe(PREVIEW)
    expect(note(container)).toBe('Loading all 6 lines of Action.c. Until then this shows the first 3.')
    const revealBefore = Number(sourcePane(container).dataset.revealRequest)

    await act(async () => { resolve({ sha256: SHA, size_bytes: FULL.length, lines: 6, content: FULL }) })
    await flush()

    expect(sourcePane(container).textContent).toBe(FULL)
    expect(sourcePane(container).dataset.activeRange).toBe('5-5')
    expect(Number(sourcePane(container).dataset.revealRequest)).toBeGreaterThan(revealBefore)
    expect(note(container)).toBe('')

    // A later visit uses the loaded text without asking again.
    act(() => root.unmount())
    root = createRoot(container)
    await act(async () => { root.render(<SourceMappingReview sourceReview={review()} script={script} />) })
    expect(sourcePane(container).textContent).toBe(FULL)
    expect(requestMock).toHaveBeenCalledTimes(1)
  })

  it('loads from the project the caller names when the review has no script', async () => {
    requestMock.mockResolvedValue({ sha256: SHA, size_bytes: FULL.length, lines: 6, content: FULL })
    await act(async () => { root.render(<SourceMappingReview sourceReview={review()} script={null} projectId="project-2" />) })
    await flush()
    expect(requestMock).toHaveBeenCalledWith(`/projects/project-2/source-review/texts/${SHA}`, expect.anything())
    expect(sourcePane(container).textContent).toBe(FULL)
  })

  it('says why a mapped line past the preview cannot be shown', async () => {
    await act(async () => { root.render(<SourceMappingReview sourceReview={review({ full_sha256: undefined, full_bytes: undefined, full_lines: undefined })} script={script} />) })
    expect(requestMock).not.toHaveBeenCalled()
    expect(sourcePane(container).dataset.activeRange).toBe('5-5')
    expect(note(container)).toBe('Line 5 is past the first 3 lines kept of Action.c. This import kept only the preview; inspect the line in the original source.')

    act(() => root.unmount())
    root = createRoot(container)
    await act(async () => { root.render(<SourceMappingReview sourceReview={review({ full_sha256: undefined, original_bytes: 20 << 20 })} script={script} />) })
    expect(note(container)).toBe('Line 5 is past the first 3 lines kept of Action.c. The file is too large to keep in full (over 16 MB); inspect it in the original source.')

    act(() => root.unmount())
    root = createRoot(container)
    requestMock.mockRejectedValue(new Error('full source text is not available; the review shows its preview'))
    await act(async () => { root.render(<SourceMappingReview sourceReview={review()} script={script} />) })
    await flush()
    expect(sourcePane(container).textContent).toBe(PREVIEW)
    expect(note(container)).toBe('Line 5 is past the first 3 lines kept of Action.c, and the full file could not be loaded: full source text is not available; the review shows its preview')
  })

  it('lists only files that stay previews under preview limits', async () => {
    requestMock.mockResolvedValue({ sha256: SHA, size_bytes: FULL.length, lines: 6, content: FULL })
    await act(async () => { root.render(<SourceMappingReview sourceReview={review()} script={script} />) })
    await flush()
    const limited = Array.from(container.querySelectorAll('[aria-label="Files with limited previews"] li code')).map(item => item.textContent)
    expect(limited).toEqual(['pre_cci.c'])
    const options = Array.from(container.querySelectorAll<HTMLOptionElement>('.source-map-toolbar select option')).map(option => option.textContent)
    expect(options).toEqual(['Action.c', 'pre_cci.c (truncated)'])

    // With every truncated file loadable in full, there is no limit to report.
    act(() => root.unmount())
    root = createRoot(container)
    const loadable = review()
    loadable.source_files = loadable.source_files!.slice(0, 1)
    await act(async () => { root.render(<SourceMappingReview sourceReview={loadable} script={script} />) })
    await flush()
    expect(container.querySelector('[aria-label="Source preview limits"]')).toBeNull()
  })

  it('keeps the Java approval warning when the Java source loads in full', async () => {
    requestMock.mockResolvedValue({ sha256: SHA, size_bytes: FULL.length, lines: 6, content: FULL })
    const java = review({ path: 'Actions.java', language: 'java' })
    java.source_files = java.source_files!.slice(0, 1)
    java.mappings = java.mappings!.map(mapping => ({ ...mapping, source_file: 'Actions.java' }))
    await act(async () => { root.render(<SourceMappingReview sourceReview={java} script={{ ...script, protocol: 'java_vuser' }} />) })
    await flush()
    expect(container.querySelector('[aria-label="Source preview limits"]')?.textContent).toContain('full Java source manifest is missing; re-import before approval')
  })
})
