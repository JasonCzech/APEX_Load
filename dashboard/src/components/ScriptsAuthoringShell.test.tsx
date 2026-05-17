// @vitest-environment jsdom

import { act } from 'react'
import { type ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ScriptsAuthoringShell from './ScriptsAuthoringShell'
import { type StoredScript } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import { type AuthoringState, type ScriptsCreateOptionActions } from './useScriptsAuthoringState'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('./ScriptsAuthoringSurface', () => ({
  default: ({
    subView,
    draftScript,
    authoringNotice,
    editingId,
    projectId,
    onSaved,
    onOpenLibrary,
    onOpenCreateOptions,
    onImported,
    onEditDraft,
  }: {
    subView: string
    draftScript?: { name?: string } | null
    authoringNotice?: string | null
    editingId?: string | null
    projectId?: string
    onSaved?: (script: StoredScript) => void
    onOpenLibrary?: () => void
    onOpenCreateOptions?: () => void
    onImported?: (result: { scripts: StoredScript[]; scenario?: { name?: string } | null }) => void
    onEditDraft?: (script: { name: string; version: string; protocol: string; config: { base_url: string }; actions: unknown[] }) => void
  }) => (
    <div data-testid="authoring-surface">
      <span>subview:{subView}</span>
      <span>draft:{draftScript?.name || 'none'}</span>
      <span>notice:{authoringNotice || 'none'}</span>
      <span>editing:{editingId || 'none'}</span>
      <span>project:{projectId || 'none'}</span>
      <button onClick={() => onSaved?.({
        id: 'saved-script-1',
        name: 'Saved Script',
        protocol: 'http',
        version: '1.0.0',
        created_at: '2026-05-17T00:00:00Z',
      })}>surface-save</button>
      <button onClick={onOpenLibrary}>surface-library</button>
      <button onClick={onOpenCreateOptions}>surface-create-options</button>
      <button onClick={() => onImported?.({
        scripts: [{
          id: 'imported-script-1',
          name: 'Imported Script',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-17T00:00:00Z',
        }],
      })}>surface-imported</button>
      <button onClick={() => onEditDraft?.({
        name: 'Surface Draft',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://example.com' },
        actions: [],
      })}
      >surface-edit-draft</button>
    </div>
  ),
}))

function createActions(overrides: Partial<ScriptsCreateOptionActions> = {}): ScriptsCreateOptionActions {
  return {
    startApiScript: vi.fn(),
    startSoapScript: vi.fn(),
    startJavaHttpScript: vi.fn(),
    startWebScript: vi.fn(),
    startJmsScript: vi.fn(),
    openLoadRunnerImport: vi.fn(),
    openJMeterImport: vi.fn(),
    openRecorder: vi.fn(),
    openSampleImport: vi.fn(),
    openJavaHttpImport: vi.fn(),
    ...overrides,
  }
}

function createState(subView: AuthoringState['subView'], overrides: Partial<AuthoringState> = {}): AuthoringState {
  return {
    subView,
    editingId: null,
    editingScript: null,
    draftScript: null,
    draftEditorFocus: null,
    authoringNotice: null,
    ...overrides,
  }
}

describe('ScriptsAuthoringShell', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('renders library children when the authoring state is list', async () => {
    await act(async () => {
      root.render(renderShell({
        authoringState: createState('list'),
        children: <div data-testid="library">library-view</div>,
      }))
    })

    expect(container.querySelector('[data-testid="library"]')).not.toBeNull()
    expect(container.textContent).toContain('library-view')
  })

  it('renders create options and delegates launcher actions', async () => {
    const actions = createActions()
    const onOpenLibrary = vi.fn()

    await act(async () => {
      root.render(renderShell({
        authoringState: createState('create-options'),
        createOptionActions: actions,
        onOpenLibrary,
      }))
    })

    expect(container.textContent).toContain('Create Script')
    expect(container.textContent).toContain('Import LoadRunner')

    await clickButton('Import LoadRunner')
    expect(actions.openLoadRunnerImport).toHaveBeenCalled()

    await clickButton('Back to Library')
    expect(onOpenLibrary).toHaveBeenCalled()
  })

  it('renders authoring subviews through the shared authoring surface', async () => {
    const onSaved = vi.fn()
    const onOpenLibrary = vi.fn()
    const onOpenCreateOptions = vi.fn()
    const onImported = vi.fn()
    const onEditDraft = vi.fn()

    await act(async () => {
      root.render(renderShell({
        authoringState: createState('create', {
          draftScript: {
            name: 'Draft Script',
            version: '1.0.0',
            protocol: 'http',
            config: { base_url: 'https://example.com' },
            actions: [],
          },
          authoringNotice: 'Saved 2 scripts. Reviewing the first saved script.',
        }),
        onSaved,
        onOpenLibrary,
        onOpenCreateOptions,
        onImported,
        onEditDraft,
      }))
    })

    expect(container.querySelector('[data-testid="authoring-surface"]')).not.toBeNull()
    expect(container.textContent).toContain('subview:create')
    expect(container.textContent).toContain('draft:Draft Script')
    expect(container.textContent).toContain('notice:Saved 2 scripts. Reviewing the first saved script.')
    expect(container.textContent).toContain('project:project-1')

    await clickButton('surface-save')
    await clickButton('surface-library')
    await clickButton('surface-create-options')
    await clickButton('surface-imported')
    await clickButton('surface-edit-draft')

    expect(onSaved).toHaveBeenCalledWith(expect.objectContaining({ id: 'saved-script-1', name: 'Saved Script' }))
    expect(onOpenLibrary).toHaveBeenCalled()
    expect(onOpenCreateOptions).toHaveBeenCalled()
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'imported-script-1', name: 'Imported Script' })],
    }))
    expect(onEditDraft).toHaveBeenCalledWith(expect.objectContaining({ name: 'Surface Draft' }))
  })

  function renderShell({
    authoringState,
    createOptionActions = createActions(),
    onSaved = vi.fn(),
    onOpenLibrary = vi.fn(),
    onOpenCreateOptions = vi.fn(),
    onImported = vi.fn(),
    onEditDraft = vi.fn(),
    children = <div />,
  }: {
    authoringState: AuthoringState
    createOptionActions?: ScriptsCreateOptionActions
    onSaved?: (script: StoredScript) => void
    onOpenLibrary?: () => void
    onOpenCreateOptions?: () => void
    onImported?: (result: { scripts: StoredScript[]; scenario?: { name?: string } | null }) => void
    onEditDraft?: (script: ScriptDefinition) => void
    children?: ReactNode
  }) {
    return (
      <ScriptsAuthoringShell
        authoringState={authoringState}
        projectId="project-1"
        activeProject={{ id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }}
        createOptionActions={createOptionActions}
        onSaved={onSaved}
        onOpenLibrary={onOpenLibrary}
        onOpenCreateOptions={onOpenCreateOptions}
        onImported={onImported}
        onEditDraft={onEditDraft}
      >
        {children}
      </ScriptsAuthoringShell>
    )
  }

  async function clickButton(label: string) {
    const button = Array.from(container.querySelectorAll('button')).find(element => element.textContent?.includes(label))
    expect(button).toBeDefined()
    if (!button) {
      return
    }
    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }
})
