// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TestNotesDialog from './TestNotesDialog'

const apiMock = vi.hoisted(() => ({
  listTestNotes: vi.fn(),
  createTestNote: vi.fn(),
  updateTestNote: vi.fn(),
  deleteTestNote: vi.fn(),
}))

vi.mock('../hooks/api/useTestApi', () => ({
  useTestApi: () => apiMock,
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('TestNotesDialog', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.listTestNotes.mockReset()
    apiMock.createTestNote.mockReset()
    apiMock.updateTestNote.mockReset()
    apiMock.deleteTestNote.mockReset()
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.restoreAllMocks()
    vi.clearAllMocks()
  })

  it('loads an existing note and renders markdown without executing raw HTML links', async () => {
    apiMock.listTestNotes.mockResolvedValue({
      test_id: 'test-1',
      notes: [{
        id: 'note-1',
        test_id: 'test-1',
        body: '# Ramp Notes\n\n<script>alert(1)</script>\n\n[bad](javascript:alert(1)), [authority](//attacker.example/replace), [mixed](/\\attacker.example/replace), and **bold** text.',
        created_at: '2026-06-24T12:00:00Z',
        updated_at: '2026-06-24T12:05:00Z',
        created_by: 'Ava',
        updated_by: 'Ava',
      }],
    })

    await renderDialog()

    expect(container.textContent).toContain('Ramp Notes')
    expect(container.textContent).toContain('<script>alert(1)</script>')
    expect(container.querySelector('script')).toBeNull()
    expect(container.querySelector('a[href^="javascript"]')).toBeNull()
    expect(container.querySelector('a[href^="//"]')).toBeNull()
    expect(container.querySelector('a[href*="attacker.example"]')).toBeNull()
    expect(container.querySelector('.test-notes-preview strong')?.textContent).toBe('bold')
    expect(container.textContent).toContain('Created')
    expect(container.textContent).toContain('Updated')
  })

  it('rejects root-relative links that would replace the macOS Wails webview', async () => {
    apiMock.listTestNotes.mockResolvedValue({
      test_id: 'test-1',
      notes: [note('note-1', '[root](/reports), [hash](#/reports), [absolute](https://docs.example.test/report), [mail](mailto:ops@example.test), [credentials](https://user:pass@docs.example.test/report), and [malformed](https:).')],
    })

    await renderDialog()

    expect(container.querySelector('a[href="/reports"]')).toBeNull()
    expect(container.querySelector('a[href="#/reports"]')?.textContent).toBe('hash')
    expect(container.querySelector('a[href="https://docs.example.test/report"]')?.textContent).toBe('absolute')
    expect(container.querySelector('a[href="mailto:ops@example.test"]')?.textContent).toBe('mail')
    expect(container.querySelector('a[href*="user:pass"]')).toBeNull()
    expect(Array.from(container.querySelectorAll('a')).some(anchor => anchor.textContent === 'malformed')).toBe(false)
  })

  it('exposes a labelled modal and gives it a deterministic initial focus target', async () => {
    apiMock.listTestNotes.mockResolvedValue({
      test_id: 'test-1',
      notes: [note('note-1', 'Existing note')],
    })

    await renderDialog()
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(document.getElementById(dialog?.getAttribute('aria-labelledby') || '')?.textContent).toBe('Run notes')
    expect(document.getElementById(dialog?.getAttribute('aria-describedby') || '')?.textContent).toBe('Checkout Run')
    expect(document.activeElement).toBe(button('Close'))
  })

  it('creates, edits, and deletes notes', async () => {
    apiMock.listTestNotes.mockResolvedValue({
      test_id: 'test-1',
      notes: [note('note-1', 'Existing note')],
    })
    apiMock.createTestNote.mockImplementation(async (_testId: string, body: string) => note('note-2', body))
    apiMock.updateTestNote.mockImplementation(async (_testId: string, noteId: string, body: string) => note(noteId, body, '2026-06-24T12:15:00Z'))
    apiMock.deleteTestNote.mockResolvedValue({ deleted: 'note-1' })

    await renderDialog()

    await click(button('New Note'))
    setField(container.querySelector('textarea'), '# New note\n\n- one')
    await click(button('Save'))
    await flush()
    expect(apiMock.createTestNote).toHaveBeenCalledWith('test-1', '# New note\n\n- one')
    expect(container.textContent).toContain('New note')

    await click(buttonContaining('Existing note'))
    await click(button('Edit'))
    setField(container.querySelector('textarea'), 'Edited **body**')
    await click(button('Save'))
    await flush()
    expect(apiMock.updateTestNote).toHaveBeenCalledWith('test-1', 'note-1', 'Edited **body**')
    expect(container.querySelector('.test-notes-preview strong')?.textContent).toBe('body')

    vi.spyOn(window, 'confirm').mockReturnValue(true)
    await click(button('Delete'))
    await flush()
    expect(apiMock.deleteTestNote).toHaveBeenCalledWith('test-1', 'note-1')
  })

  it('opens a new note holding a root-cause draft beside existing notes', async () => {
    apiMock.listTestNotes.mockResolvedValue({ test_id: 'test-1', notes: [note('note-1', 'Existing note')] })
    apiMock.createTestNote.mockImplementation(async (_testId: string, body: string) => note('note-2', body))
    await act(async () => {
      root.render(<TestNotesDialog testId="test-1" testName="Checkout Run" onClose={() => {}} initialDraft={'Root cause #1: Pool exhaustion\n\nhttps://apex.example/#/report/test-1'} />)
    })
    await flush()
    expect((container.querySelector('textarea') as HTMLTextAreaElement).value).toBe('Root cause #1: Pool exhaustion\n\nhttps://apex.example/#/report/test-1')
    await click(button('Save'))
    await flush()
    expect(apiMock.createTestNote).toHaveBeenCalledWith('test-1', 'Root cause #1: Pool exhaustion\n\nhttps://apex.example/#/report/test-1')
  })

  it('confirms before discarding an unsaved draft', async () => {
    const onClose = vi.fn()
    apiMock.listTestNotes.mockResolvedValue({
      test_id: 'test-1',
      notes: [],
    })
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)

    await renderDialog(onClose)
    setField(container.querySelector('textarea'), 'Unsaved observation')

    await click(button('Close'))

    expect(confirm).toHaveBeenCalledWith('Discard unsaved note?')
    expect(onClose).not.toHaveBeenCalled()

    confirm.mockReturnValue(true)
    await click(button('Close'))

    expect(onClose).toHaveBeenCalledOnce()
  })

  async function renderDialog(onClose = () => {}) {
    await act(async () => {
      root.render(<TestNotesDialog testId="test-1" testName="Checkout Run" onClose={onClose} />)
    })
    await flush()
  }

  function button(label: string) {
    const match = Array.from(container.querySelectorAll('button')).find((item) => item.textContent?.trim() === label)
    if (!match) throw new Error(`button not found: ${label}`)
    return match
  }

  function buttonContaining(label: string) {
    const match = Array.from(container.querySelectorAll('button')).find((item) => item.textContent?.includes(label))
    if (!match) throw new Error(`button not found containing: ${label}`)
    return match
  }
})

function note(id: string, body: string, updatedAt = '2026-06-24T12:10:00Z') {
  return {
    id,
    test_id: 'test-1',
    body,
    created_at: '2026-06-24T12:00:00Z',
    updated_at: updatedAt,
    created_by: 'Ava',
    updated_by: 'Ava',
  }
}

async function flush() {
  await act(async () => {
    await Promise.resolve()
  })
}

async function click(element: Element | undefined) {
  if (!element) throw new Error('element not found')
  await act(async () => {
    element.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
}

function setField(element: HTMLTextAreaElement | HTMLInputElement | null | undefined, value: string) {
  if (!element) throw new Error('field not found')
  const prototype = element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(element, value)
  act(() => {
    element.dispatchEvent(new Event('input', { bubbles: true }))
  })
}
