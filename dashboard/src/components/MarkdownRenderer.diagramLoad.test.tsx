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
vi.mock('./MermaidErDiagram', async () => {
  chunk.started++
  await chunk.ready
  chunk.rejected = true
  throw new Error('diagram chunk unavailable')
})

const ER_SOURCE = 'erDiagram\n    releases ||--o{ tests : "release_id"'

it('loads the diagram renderer only for an erDiagram, and shows its source until then and if it cannot load, reporting the failure', async () => {
  const node = document.createElement('div')
  const root = createRoot(node)
  try {
    await act(async () => root.render(<MarkdownRenderer copyExamples source={'```mermaid\nflowchart LR\n  a --> b\n```'} />))
    expect(chunk.started).toBe(0)
    expect(node.querySelector('pre code')?.textContent).toBe('flowchart LR\n  a --> b')

    await act(async () => root.render(<MarkdownRenderer copyExamples source={`## Runs\n\n\`\`\`mermaid\n${ER_SOURCE}\n\`\`\``} />))
    expect(chunk.started).toBe(1)
    expect(reported).not.toHaveBeenCalled()
    expect(node.querySelector('h3')?.textContent).toBe('Runs')
    expect(node.querySelector('pre code')?.textContent).toBe(ER_SOURCE)
    expect(node.querySelector('.help-code-toolbar button')?.textContent).toBe('Copy example')

    await act(async () => {
      chunk.release()
      await vi.waitFor(() => expect(chunk.rejected).toBe(true))
    })
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
    expect(node.querySelector('figure.entity-diagram')).toBeNull()
    expect(node.querySelector('[role="alert"]')).toBeNull()
    // The failure is reported like any other lazy surface's.
    expect(reported).toHaveBeenCalledTimes(1)
    expect(reported.mock.calls[0][0]).toBe('render')
    // Vitest wraps a failing mock factory's error; the chunk's own is its cause.
    const failure = reported.mock.calls[0][1] as Error & { cause?: Error }
    expect(failure).toBeInstanceOf(Error)
    expect(failure.cause?.message ?? failure.message).toBe('diagram chunk unavailable')
    expect(node.querySelector('h3')?.textContent).toBe('Runs')
    expect(node.querySelector('pre code')?.textContent).toBe(ER_SOURCE)
    expect(node.querySelector('.help-code-toolbar button')?.textContent).toBe('Copy example')
  } finally {
    await act(async () => {
      chunk.release()
      if (chunk.started) await vi.waitFor(() => expect(chunk.rejected).toBe(true))
    })
    await act(async () => root.unmount())
  }
})
