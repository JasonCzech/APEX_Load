// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ScriptsAuthoringSurface from './ScriptsAuthoringSurface'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('./ImportDestinationFolder', () => ({
  default: () => <div data-testid="outer-destination-folder">Outer destination</div>,
}))

vi.mock('./BrunoImport', () => ({
  default: (props: { folderId?: string | null; onFolderChange?: (folderId: string) => void }) => (
    <div data-testid="bruno-import">
      <span data-testid="bruno-folder-value">{props.folderId || 'project-root'}</span>
      <button onClick={() => props.onFolderChange?.('folder-from-bruno')}>Choose Bruno folder</button>
    </div>
  ),
}))

describe('ScriptsAuthoringSurface Bruno destination integration', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.clearAllMocks()
  })

  it('uses the Bruno destination control as the single folder source', async () => {
    await act(async () => {
      root.render(
        <ScriptsAuthoringSurface
          subView="bruno-import"
          importLauncher="bruno"
          importMode="native_requests"
          draftScript={null}
          draftInitiallyDirty={false}
          draftEditorFocus={null}
          draftReturnTarget={null}
          authoringNotice={null}
          editingId={null}
          editingScript={null}
          projectId="project-1"
          folderId="folder-seeded"
          activeProject={null}
          onSaved={vi.fn()}
          onOpenLibrary={vi.fn()}
          onOpenCreateOptions={vi.fn()}
          onImported={vi.fn()}
          onEditDraft={vi.fn()}
          onReturnToDraftSource={vi.fn()}
        />,
      )
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.querySelector('[data-testid="outer-destination-folder"]')).toBeNull()
    expect(container.querySelector('[data-testid="bruno-folder-value"]')?.textContent).toBe('folder-seeded')

    const chooseFolder = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Choose Bruno folder')
    await act(async () => chooseFolder?.click())
    expect(container.querySelector('[data-testid="bruno-folder-value"]')?.textContent).toBe('folder-from-bruno')
  })
})
