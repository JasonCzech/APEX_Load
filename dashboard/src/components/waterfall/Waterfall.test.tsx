// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Waterfall } from './Waterfall'
import type { WaterfallNode } from './types'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let cleanup: (() => void) | null = null
afterEach(() => {
  cleanup?.()
  cleanup = null
})

function sampleRoot(id = 'req-1'): WaterfallNode<null> {
  return {
    id,
    kind: 'request',
    label: 'Checkout request',
    status: 'failed',
    startMs: 0,
    durationMs: 250,
    phases: [{ key: 'wait', startMs: 0, endMs: 250 }],
    children: [],
    payload: null,
  }
}

describe('Waterfall sinks and drawer styles', () => {
  it('labels interactive .wf-sink buttons with s.label and describes them via unique <small> ids', () => {
    const onSelect = vi.fn()
    const longDetail = 'Start_iPadTest_LrgeBANs_ViewServProm_T08_ClkBackToAccInfo||aura_RecordUiController$ACTION$getRecord_transaction (250.0 ms, 100%)'
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    cleanup = () => {
      act(() => root.unmount())
      container.remove()
    }
    act(() => {
      root.render(
        <div>
          <Waterfall
            roots={[sampleRoot('req-1')]}
            spanMs={250}
            ariaLabel="First waterfall"
            selectedId={null}
            onSelect={onSelect}
            sinks={[
              { id: 'slowest', label: 'Slowest = 250.0 ms (100%)', detail: longDetail, targetId: 'req-1', tone: 'warning' },
              { id: 'empty-detail', label: '1 failure', detail: '', targetId: 'req-1', tone: 'danger' },
              { id: 'static', label: 'Wait = 250.0 ms', detail: 'Dominant phase across requests', tone: 'neutral' },
            ]}
          />
          <Waterfall
            roots={[sampleRoot('req-2')]}
            spanMs={250}
            ariaLabel="Second waterfall"
            selectedId={null}
            onSelect={vi.fn()}
            sinks={[
              { id: 'slowest', label: 'Slowest = 250.0 ms (100%)', detail: 'Second instance detail', targetId: 'req-2', tone: 'neutral' },
            ]}
          />
        </div>,
      )
    })

    const buttons = [...container.querySelectorAll<HTMLButtonElement>('button.wf-sink')]
    expect(buttons).toHaveLength(3)

    // First interactive sink: aria-label matches visible <strong>{s.label}</strong> (WCAG 2.5.3), detail is aria-describedby.
    const first = buttons[0]!
    expect(first.getAttribute('aria-label')).toBe('Slowest = 250.0 ms (100%)')
    expect(first.querySelector('strong')?.textContent).toBe('Slowest = 250.0 ms (100%)')
    const firstDescId = first.getAttribute('aria-describedby')
    expect(firstDescId).toBeTruthy()
    expect(document.getElementById(firstDescId!)?.textContent).toBe(longDetail)

    // Empty detail omits aria-describedby rather than pointing to an empty element.
    const emptyDetailBtn = buttons[1]!
    expect(emptyDetailBtn.getAttribute('aria-label')).toBe('1 failure')
    expect(emptyDetailBtn.hasAttribute('aria-describedby')).toBe(false)

    // Second Waterfall instance gets a distinct useId()-scoped description id (no DOM id collision).
    const third = buttons[2]!
    const thirdDescId = third.getAttribute('aria-describedby')
    expect(thirdDescId).toBeTruthy()
    expect(thirdDescId).not.toBe(firstDescId)
    expect(document.querySelectorAll(`[id="${firstDescId}"]`)).toHaveLength(1)
    expect(document.querySelectorAll(`[id="${thirdDescId}"]`)).toHaveLength(1)

    // Non-interactive sink remains a plain <span>.
    const staticSink = container.querySelector('span.wf-sink')
    expect(staticSink?.querySelector('strong')?.textContent).toBe('Wait = 250.0 ms')
    expect(staticSink?.hasAttribute('aria-label')).toBe(false)

    act(() => first.click())
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 'req-1' }))
  })

  it('keeps .wf-drawer-tabs on a single scrollable row and drawer action buttons non-wrapping', async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    // @ts-expect-error node:url is available in the Vitest runtime.
    const { fileURLToPath, URL: NodeURL } = await import('node:url')
    const css = readFileSync(fileURLToPath(new NodeURL('./Waterfall.css', import.meta.url)), 'utf8') as string
    expect(css).toMatch(/\.wf-drawer-tabs\s*\{[^}]*flex-wrap:\s*nowrap[^}]*overflow-x:\s*auto/)
    expect(css).toMatch(/\.wf-drawer-tabs\s+\[role='tab'\]\s*\{[^}]*white-space:\s*nowrap/)
    expect(css).toMatch(/\.wf-drawer-actions\s+button\s*\{[^}]*white-space:\s*nowrap/)
  })
})
