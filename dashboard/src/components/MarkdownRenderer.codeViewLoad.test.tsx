// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import MarkdownRenderer from './MarkdownRenderer'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const chunk = vi.hoisted(() => {
  let release!: () => void
  const ready = new Promise<void>(resolve => { release = resolve })
  return { ready, release, started: 0, rejected: false }
})
const reported = vi.hoisted(() => vi.fn())
vi.mock('../utils/clientErrorReporting', async importOriginal => ({ ...await importOriginal<typeof import('../utils/clientErrorReporting')>(), reportClientError: reported }))
vi.mock('./HelpCodeView', async () => {
  chunk.started++
  // Bounded, so a renderer that imports the view statically fails this file
  // instead of hanging its import.
  await Promise.race([chunk.ready, new Promise(resolve => setTimeout(resolve, 5_000))])
  chunk.rejected = true
  throw new Error('code view chunk unavailable')
})

it('loads the code view only for a copyable example, and shows its plain source until then and if it cannot load, reporting the failure', async () => {
  const node = document.createElement('div')
  const root = createRoot(node)
  try {
    // Markdown outside Help, and a Mermaid example, never load the editor.
    await act(async () => root.render(<MarkdownRenderer source={'```json\n{ "a": 1 }\n```'} />))
    await act(async () => root.render(<MarkdownRenderer copyExamples source={'```mermaid\nflowchart LR\n  a --> b\n```'} />))
    expect(chunk.started).toBe(0)

    await act(async () => root.render(<MarkdownRenderer copyExamples source={'```json\n{ "a": 1 }\n```'} />))
    expect(chunk.started).toBe(1)
    const region = () => node.querySelector<HTMLElement>('.markdown-code-editor')
    // The named, focusable region and its source are there before the view.
    expect(region()?.getAttribute('role')).toBe('region')
    expect(region()?.getAttribute('aria-label')).toBe('json code example')
    expect(region()?.tabIndex).toBe(0)
    expect(region()?.dataset.code).toBe('{ "a": 1 }')
    expect(region()?.querySelector('.help-code-plain code')?.textContent).toBe('{ "a": 1 }')
    expect(reported).not.toHaveBeenCalled()

    await act(async () => {
      chunk.release()
      await vi.waitFor(() => expect(chunk.rejected).toBe(true))
    })
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
    expect(region()?.querySelector('.cm-editor')).toBeNull()
    expect(node.querySelector('[role="alert"]')).toBeNull()
    expect(reported).toHaveBeenCalledTimes(1)
    expect(reported.mock.calls[0][0]).toBe('render')
    const failure = reported.mock.calls[0][1] as Error & { cause?: Error }
    expect(failure.cause?.message ?? failure.message).toBe('code view chunk unavailable')
    // The example stays readable, named and copyable.
    expect(region()?.querySelector('.help-code-plain code')?.textContent).toBe('{ "a": 1 }')
    expect(region()?.getAttribute('aria-label')).toBe('json code example')
    expect(node.querySelector('.help-code-toolbar button')?.textContent).toBe('Copy example')
  } finally {
    await act(async () => {
      chunk.release()
      if (chunk.started) await vi.waitFor(() => expect(chunk.rejected).toBe(true))
      root.unmount()
    })
  }
})
