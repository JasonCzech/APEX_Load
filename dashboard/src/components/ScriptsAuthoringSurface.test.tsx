// @vitest-environment jsdom

import { act, Suspense, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ScriptsAuthoringSurface from './ScriptsAuthoringSurface'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// The destination's folder-create busy callback, driven by the tests: the
// control renders inside the import gate it locks.
const destinationBusy = vi.hoisted(() => ({ change: null as ((busy: boolean) => void) | null }))

vi.mock('./ImportDestinationFolder', () => ({
  default: ({ disabled, onOperationBusyChange, suggestedName }: { disabled?: boolean; onOperationBusyChange?: (busy: boolean) => void; suggestedName?: string }) => {
    destinationBusy.change = onOperationBusyChange || null
    return (
      <div data-testid="outer-destination-folder">
        <select aria-label="Mock destination folder" disabled={disabled}><option>Outer destination</option></select>
        <span data-testid="suggested-folder">{suggestedName || ''}</span>
      </div>
    )
  },
}))

vi.mock('./BrunoImport', () => ({
  default: (props: { folderId?: string | null; onFolderChange?: (folderId: string) => void }) => (
    <div data-testid="bruno-import">
      <span data-testid="bruno-folder-value">{props.folderId || 'project-root'}</span>
      <button onClick={() => props.onFolderChange?.('folder-from-bruno')}>Choose Bruno folder</button>
    </div>
  ),
}))

// Like the real importer, the mock offers the surface's destination at its
// save step.
vi.mock('./JMeterImport', async () => {
  const { useImportDestination, useSuggestImportFolderName } = await import('./importDestinationContext')
  return {
    default: function MockJMeterImport(props: { onOperationBusyChange?: (busy: boolean) => void }) {
      const destination = useImportDestination()
      useSuggestImportFolderName('checkout_plan.jmx')
      return (
        <div data-testid="jmeter-import">
          {destination}
          <button onClick={() => props.onOperationBusyChange?.(true)}>Start deferred save</button>
          <button onClick={() => props.onOperationBusyChange?.(false)}>Finish deferred save</button>
        </div>
      )
    },
  }
})

vi.mock('./RecordingStudio', () => ({
  default: (props: { destinationControl?: ReactNode; onEdit?: (script: { name: string }) => void; onImported?: (result: { scripts: { id: string }[] }) => void }) => (
    <>
      {props.destinationControl}
      <button onClick={() => props.onEdit?.({ name: 'Recorded checkout' })}>Open recording in editor</button>
      <button onClick={() => props.onImported?.({ scripts: [{ id: 'saved-recording' }] })}>Save recording</button>
    </>
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

  it('offers the active importer\'s source name for a new folder, and none on the recorder', async () => {
    await import('./JMeterImport')
    await import('./RecordingStudio')
    const surface = (subView: 'jmeter-import' | 'recorder') => (
      <Suspense fallback={<div>Loading importer</div>}>
        <ScriptsAuthoringSurface
          subView={subView}
          draftScript={null}
          draftInitiallyDirty={false}
          draftEditorFocus={null}
          draftReturnTarget={null}
          authoringNotice={null}
          editingId={null}
          editingScript={null}
          projectId="project-1"
          folderId={null}
          activeProject={null}
          onSaved={vi.fn()}
          onOpenLibrary={vi.fn()}
          onOpenCreateOptions={vi.fn()}
          onImported={vi.fn()}
          onEditDraft={vi.fn()}
          onReturnToDraftSource={vi.fn()}
        />
      </Suspense>
    )
    await act(async () => {
      root.render(surface('jmeter-import'))
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.querySelector('[data-testid="suggested-folder"]')?.textContent).toBe('checkout plan')

    await act(async () => {
      root.render(surface('recorder'))
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.querySelector('[data-testid="suggested-folder"]')?.textContent).toBe('')
  })

  it('locks the visible shared destination for the lifetime of a child save', async () => {
    // Resolve the lazy module before the synchronous root render; this test is
    // about the busy handshake, not the lazy-loading fallback.
    await import('./JMeterImport')
    await act(async () => {
      root.render(
        <Suspense fallback={<div>Loading importer</div>}>
          <ScriptsAuthoringSurface
            subView="jmeter-import"
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
          />
        </Suspense>,
      )
      await Promise.resolve()
      await Promise.resolve()
    })

    const destination = container.querySelector<HTMLSelectElement>('[aria-label="Mock destination folder"]')!
    expect(destination.disabled).toBe(false)
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>('#root button, button'))
    await act(async () => buttons.find(button => button.textContent === 'Start deferred save')?.click())
    expect(destination.disabled).toBe(true)
    await act(async () => buttons.find(button => button.textContent === 'Finish deferred save')?.click())
    expect(destination.disabled).toBe(false)
  })

  it('fences child save controls for the lifetime of a deferred destination create', async () => {
    await import('./JMeterImport')
    await act(async () => {
      root.render(
        <Suspense fallback={<div>Loading importer</div>}>
          <ScriptsAuthoringSurface
            subView="jmeter-import"
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
          />
        </Suspense>,
      )
      await Promise.resolve()
      await Promise.resolve()
    })

    const startSave = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Start deferred save')!
    expect(destinationBusy.change).toBeTruthy()

    await act(async () => destinationBusy.change?.(true))
    expect(startSave.closest('fieldset')?.disabled).toBe(true)
    expect(startSave.matches(':disabled')).toBe(true)

    await act(async () => destinationBusy.change?.(false))
    expect(startSave.closest('fieldset')?.disabled).toBe(false)
    expect(startSave.matches(':disabled')).toBe(false)
  })

  it('opens a recording in the editor unsaved, returning to Recording Studio', async () => {
    await import('./RecordingStudio')
    const onEditDraft = vi.fn()
    const onImported = vi.fn()
    await act(async () => {
      root.render(
        <Suspense fallback={<div>Loading recorder</div>}>
          <ScriptsAuthoringSurface
            subView="recorder"
            draftScript={null}
            draftInitiallyDirty={false}
            draftEditorFocus={null}
            draftReturnTarget={null}
            authoringNotice={null}
            editingId={null}
            editingScript={null}
            projectId="project-1"
            activeProject={null}
            onSaved={vi.fn()}
            onOpenLibrary={vi.fn()}
            onOpenCreateOptions={vi.fn()}
            onImported={onImported}
            onEditDraft={onEditDraft}
            onReturnToDraftSource={vi.fn()}
          />
        </Suspense>,
      )
      await Promise.resolve()
      await Promise.resolve()
    })
    const open = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Open recording in editor')
    await act(async () => open?.click())
    expect(onEditDraft).toHaveBeenCalledWith(
      { name: 'Recorded checkout' },
      undefined,
      { subView: 'recorder', label: 'Back to Recording Studio' },
      { initiallyDirty: true },
    )
    // Saved instead, its editor still closes back to the studio.
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save recording')
    await act(async () => save?.click())
    expect(onImported).toHaveBeenCalledWith({ scripts: [{ id: 'saved-recording' }], returnTarget: { subView: 'recorder', label: 'Back to Recording Studio' } })
  })
})
