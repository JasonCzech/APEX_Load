// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ScriptCodePane, ScriptJsonDocument } from './ScriptJsonAuthoringView'
import type { ScriptCodePaneProps } from './ScriptJsonAuthoringView'

const { captured } = vi.hoisted(() => ({ captured: [] as { basicSetup: unknown; extensions: unknown; editable?: boolean; onChange: (value: string) => void }[] }))
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

it('unlocks direct JSON editing in a single click on Edit in Full Script JSON while preserving read-only default when opened directly', async () => {
  const sourcePane: ScriptCodePaneProps = {
    value: '{"name":"Full script"}',
    status: 'synced',
    statusMessage: 'In sync',
    activePath: 'script.actions[0]',
    activeRange: null,
    actionCount: 1,
    onChange: vi.fn(),
    onApply: vi.fn(),
    onCopy: vi.fn(),
    onDownload: vi.fn(),
    onFormat: vi.fn(),
    onReset: vi.fn(),
  }
  const scope = {
    key: 'action:0',
    label: 'Action 1 · Login',
    path: 'script.actions[0]',
    action: { name: 'Login', type: 'GET', target: '/login' },
    actionIndex: 0,
    phase: 'action' as const,
    subAction: false,
  }
  let showFull = false
  const renderDoc = () => (
    <ScriptJsonDocument
      scope={scope}
      selectedJsonText='{"name":"Login"}'
      showSelectedActionJson={!showFull}
      showFullScriptJson={showFull}
      selectedJsonUnavailable={false}
      onShowFullScriptJson={next => { showFull = next; root.render(renderDoc()) }}
      onCopySelected={vi.fn()}
      onDownloadSelected={vi.fn()}
      sourcePane={sourcePane}
      selectionState={null}
      onOpenParameterEditor={vi.fn()}
      onOpenExistingParameter={vi.fn()}
      onOpenOccurrenceReview={vi.fn()}
    />
  )

  await act(async () => { root.render(renderDoc()) })
  expect(container.querySelector('.script-code-editor')?.getAttribute('data-readonly')).toBe('true')
  expect(captured[captured.length - 1].editable).toBe(false)

  // Clicking "Full Script JSON" in the scope bar opens Full Script JSON in read-only mode by default.
  const scopeFullBtn = Array.from(container.querySelectorAll<HTMLButtonElement>('.script-json-scope-toggle button'))
    .find(button => button.textContent === 'Full Script JSON')!
  await act(async () => { scopeFullBtn.click() })
  expect(showFull).toBe(true)
  expect(container.querySelector('.script-code-editor')?.getAttribute('data-readonly')).toBe('true')
  expect(container.querySelector('.script-code-readonly-pill')?.textContent).toBe('Read-only')
  expect(captured[captured.length - 1].editable).toBe(false)

  // Return to Selected Action JSON, then click "Edit in Full Script JSON" for 1-click unlock.
  const scopeSelectedBtn = Array.from(container.querySelectorAll<HTMLButtonElement>('.script-json-scope-toggle button'))
    .find(button => button.textContent === 'Selected Action')!
  await act(async () => { scopeSelectedBtn.click() })
  expect(showFull).toBe(false)

  const editInFullBtn = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
    .find(button => button.textContent === 'Edit in Full Script JSON')!
  await act(async () => { editInFullBtn.click() })
  expect(showFull).toBe(true)
  expect(container.querySelector('.script-code-editor')?.getAttribute('data-readonly')).toBe('false')
  expect(container.querySelector('.script-code-readonly-pill')).toBeNull()
  expect(captured[captured.length - 1].editable).toBe(true)
  const discardBtn = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
    .find(button => button.textContent === 'Discard JSON edits')!
  expect(discardBtn).toBeDefined()

  // Clicking "Discard JSON edits" exits direct edit mode back to read-only.
  await act(async () => { discardBtn.click() })
  expect(container.querySelector('.script-code-editor')?.getAttribute('data-readonly')).toBe('true')
  expect(container.querySelector('.script-code-readonly-pill')?.textContent).toBe('Read-only')
  expect(captured[captured.length - 1].editable).toBe(false)

  // Standalone ScriptCodePane with defaultEditEnabled=true also exits edit mode on Discard JSON edits.
  await act(async () => { root.render(<ScriptCodePane {...sourcePane} defaultEditEnabled={true} />) })
  expect(container.querySelector('.script-code-editor')?.getAttribute('data-readonly')).toBe('false')
  expect(captured[captured.length - 1].editable).toBe(true)
  const standaloneDiscardBtn = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
    .find(button => button.textContent === 'Discard JSON edits')!
  await act(async () => { standaloneDiscardBtn.click() })
  expect(container.querySelector('.script-code-editor')?.getAttribute('data-readonly')).toBe('true')
  expect(captured[captured.length - 1].editable).toBe(false)
})
