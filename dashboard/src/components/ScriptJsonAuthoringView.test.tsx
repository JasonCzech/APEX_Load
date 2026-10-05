// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ScriptCodePane } from './ScriptJsonAuthoringView'
import type { ScriptCodePaneProps } from './ScriptJsonAuthoringView'

const { captured } = vi.hoisted(() => ({ captured: [] as { basicSetup: unknown; extensions: unknown; onChange: (value: string) => void }[] }))
vi.mock('@uiw/react-codemirror', () => ({ default: (props: typeof captured[number]) => { captured.push(props); return <div data-testid="source-editor" /> } }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLDivElement
let root: Root
beforeEach(() => { captured.length = 0; container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })

it('keeps CodeMirror search authority stable across metadata renders while dispatching edits to the current owner', async () => {
  const first = vi.fn(), current = vi.fn()
  const props: ScriptCodePaneProps = { value: '{"name":"Current draft"}', status: 'synced', statusMessage: '', activePath: '', activeRange: null, actionCount: 0, onChange: first,
    onApply: vi.fn(), onCopy: vi.fn(), onDownload: vi.fn(), onFormat: vi.fn(), onReset: vi.fn() }
  await act(async () => { root.render(<ScriptCodePane {...props} />) })
  const observed = captured[captured.length - 1]
  await act(async () => { root.render(<ScriptCodePane {...props} statusMessage="Credential metadata refreshed" onChange={current} />) })
  const refreshed = captured[captured.length - 1]
  expect(refreshed.basicSetup).toBe(observed.basicSetup)
  expect(refreshed.extensions).toBe(observed.extensions)
  expect(refreshed.onChange).toBe(observed.onChange)
  refreshed.onChange('{"name":"Later edit"}')
  expect(current).toHaveBeenCalledExactlyOnceWith('{"name":"Later edit"}')
  expect(first).not.toHaveBeenCalled()
})
