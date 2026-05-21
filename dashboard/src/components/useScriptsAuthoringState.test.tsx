// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useScriptsAuthoringState, type ScriptsAuthoringSubView } from './useScriptsAuthoringState'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

type HarnessProps = {
  api: {
    getScript: (id: string) => Promise<{ script?: { id?: string; name?: string; protocol?: string } | null }>
  }
  initialMode?: ScriptsAuthoringSubView
  initialEditId?: string
}

function AuthoringStateHarness({ api, initialMode, initialEditId }: HarnessProps) {
  const model = useScriptsAuthoringState({ api: api as never, initialMode, initialEditId })

  return (
    <div>
      <span data-testid="subview">{model.authoringState.subView}</span>
      <span data-testid="editing-id">{model.authoringState.editingId || 'none'}</span>
      <span data-testid="editing-name">{model.authoringState.editingScript?.name || 'none'}</span>
      <span data-testid="draft-name">{model.authoringState.draftScript?.name || 'none'}</span>
      <span data-testid="draft-protocol">{model.authoringState.draftScript?.protocol || 'none'}</span>
      <span data-testid="draft-target-action">{model.authoringState.draftEditorFocus?.targetAction || 'none'}</span>
      <span data-testid="draft-target-section">{model.authoringState.draftEditorFocus?.targetSection || 'none'}</span>
      <span data-testid="draft-target-data-source">{model.authoringState.draftEditorFocus?.targetDataSource || 'none'}</span>
      <span data-testid="draft-target-runtime-area">{model.authoringState.draftEditorFocus?.targetRuntimeArea || 'none'}</span>
      <span data-testid="authoring-notice">{model.authoringState.authoringNotice || 'none'}</span>
      <button data-testid="open-library" onClick={model.openLibrary}>open-library</button>
      <button data-testid="open-create-options" onClick={model.openCreateOptions}>open-create-options</button>
      <button data-testid="set-jmeter" onClick={() => model.setSubview('jmeter-import')}>set-jmeter</button>
      <button data-testid="start-draft" onClick={() => model.startDraft(() => ({ name: 'Draft Checkout', protocol: 'http', config: {}, actions: [] } as never))}>start-draft</button>
      <button data-testid="draft-edit" onClick={() => model.handleDraftEdit({ name: 'Edited Draft', protocol: 'http', config: {}, actions: [] } as never)}>draft-edit</button>
      <button data-testid="draft-edit-focused" onClick={() => model.handleDraftEdit({ name: 'Focused Draft', protocol: 'http', config: {}, actions: [] } as never, { targetAction: 'Submit Order' })}>draft-edit-focused</button>
      <button data-testid="draft-edit-runtime-focused" onClick={() => model.handleDraftEdit({ name: 'Runtime Draft', protocol: 'http', config: {}, actions: [] } as never, { targetSection: 'runtime' })}>draft-edit-runtime-focused</button>
      <button data-testid="draft-edit-runtime-area-focused" onClick={() => model.handleDraftEdit({ name: 'Runtime Area Draft', protocol: 'http', config: {}, actions: [] } as never, { targetSection: 'runtime', targetRuntimeArea: 'Logging' })}>draft-edit-runtime-area-focused</button>
      <button data-testid="draft-edit-parameter-data-focused" onClick={() => model.handleDraftEdit({ name: 'Parameter Draft', protocol: 'http', config: {}, actions: [] } as never, { targetSection: 'parameter-data' })}>draft-edit-parameter-data-focused</button>
      <button data-testid="draft-edit-data-source-focused" onClick={() => model.handleDraftEdit({ name: 'Data Source Draft', protocol: 'http', config: {}, actions: [] } as never, { targetSection: 'parameter-data', targetDataSource: 'login_users' })}>draft-edit-data-source-focused</button>
      <button data-testid="load-edit" onClick={() => void model.handleEdit('script-1')}>load-edit</button>
      <button data-testid="save-script" onClick={() => model.handleSavedScript({ id: 'saved-script-1', name: 'Saved Draft', protocol: 'http', version: '1.0.0', created_at: '2026-05-17T00:00:00Z' })}>save-script</button>
      <button data-testid="save-script-with-notice" onClick={() => model.handleSavedScript({ id: 'saved-script-3', name: 'Saved Notice Draft', protocol: 'http', version: '1.0.0', created_at: '2026-05-17T00:00:00Z' }, 'Saved 2 scripts. Reviewing the first saved script.')}>save-script-with-notice</button>
      <button data-testid="save-script-with-payload" onClick={() => model.handleSavedScript({ id: 'saved-script-2', name: 'Saved Payload', protocol: 'http', version: '1.0.0', created_at: '2026-05-17T00:00:00Z', script: { name: 'Persisted Checkout', protocol: 'http', config: {}, actions: [] } as never })}>save-script-with-payload</button>
      <button data-testid="open-sample" onClick={model.createOptionActions.openSampleImport}>open-sample</button>
      <button data-testid="open-openapi" onClick={model.createOptionActions.openOpenAPIImport}>open-openapi</button>
      <button data-testid="open-postman" onClick={model.createOptionActions.openPostmanImport}>open-postman</button>
      <button data-testid="open-wsdl" onClick={model.createOptionActions.openWSDLImport}>open-wsdl</button>
      <button data-testid="start-java-http" onClick={model.createOptionActions.startJavaHttpScript}>start-java-http</button>
      <button data-testid="start-template" onClick={() => model.createOptionActions.startTemplateScript({ name: 'Template Draft', protocol: 'browser', config: {}, actions: [] } as never)}>start-template</button>
    </div>
  )
}

describe('useScriptsAuthoringState', () => {
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

  it('normalizes create mode to create-options and drives draft entry transitions', async () => {
    const api = {
      getScript: vi.fn(),
    }

    await act(async () => {
      root.render(<AuthoringStateHarness api={api} initialMode="create" />)
    })

    expect(readTestId('subview')).toBe('create-options')
    expect(readTestId('draft-name')).toBe('none')

    await click('start-draft')

    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('Draft Checkout')
    expect(readTestId('editing-id')).toBe('none')

    await click('draft-edit')

    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('Edited Draft')
    expect(readTestId('draft-target-action')).toBe('none')

    await click('draft-edit-focused')

    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('Focused Draft')
    expect(readTestId('draft-target-action')).toBe('Submit Order')

    await click('draft-edit-runtime-focused')

    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('Runtime Draft')
    expect(readTestId('draft-target-action')).toBe('none')
    expect(readTestId('draft-target-section')).toBe('runtime')

    await click('draft-edit-runtime-area-focused')

    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('Runtime Area Draft')
    expect(readTestId('draft-target-action')).toBe('none')
    expect(readTestId('draft-target-section')).toBe('runtime')
    expect(readTestId('draft-target-runtime-area')).toBe('Logging')

    await click('draft-edit-parameter-data-focused')

    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('Parameter Draft')
    expect(readTestId('draft-target-action')).toBe('none')
    expect(readTestId('draft-target-section')).toBe('parameter-data')

    await click('draft-edit-data-source-focused')

    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('Data Source Draft')
    expect(readTestId('draft-target-action')).toBe('none')
    expect(readTestId('draft-target-section')).toBe('parameter-data')
    expect(readTestId('draft-target-data-source')).toBe('login_users')

    await click('save-script')

    expect(readTestId('subview')).toBe('edit')
    expect(readTestId('editing-id')).toBe('saved-script-1')
    expect(readTestId('editing-name')).toBe('Data Source Draft')
    expect(readTestId('draft-name')).toBe('none')
    expect(readTestId('draft-target-action')).toBe('none')
    expect(readTestId('draft-target-section')).toBe('none')
    expect(readTestId('draft-target-data-source')).toBe('none')
    expect(readTestId('draft-target-runtime-area')).toBe('none')
    expect(readTestId('authoring-notice')).toBe('none')

    await click('save-script-with-notice')

    expect(readTestId('subview')).toBe('edit')
    expect(readTestId('editing-id')).toBe('saved-script-3')
    expect(readTestId('authoring-notice')).toBe('Saved 2 scripts. Reviewing the first saved script.')

    await click('open-library')

    expect(readTestId('subview')).toBe('list')
    expect(readTestId('draft-name')).toBe('none')
    expect(readTestId('draft-target-action')).toBe('none')
    expect(readTestId('draft-target-section')).toBe('none')
    expect(readTestId('draft-target-data-source')).toBe('none')
    expect(readTestId('draft-target-runtime-area')).toBe('none')
    expect(readTestId('authoring-notice')).toBe('none')
  })

  it('loads the requested script into edit mode and resets when switching subviews', async () => {
    const api = {
      getScript: vi.fn().mockResolvedValue({
        script: {
          id: 'script-1',
          name: 'Checkout Script',
          protocol: 'http',
        },
      }),
    }

    await act(async () => {
      root.render(<AuthoringStateHarness api={api} initialMode="list" />)
    })

    await click('load-edit')
    await flushPromises()

    expect(api.getScript).toHaveBeenCalledWith('script-1')
    expect(readTestId('subview')).toBe('edit')
    expect(readTestId('editing-id')).toBe('script-1')
    expect(readTestId('editing-name')).toBe('Checkout Script')

    await click('open-create-options')

    expect(readTestId('subview')).toBe('create-options')
    expect(readTestId('editing-id')).toBe('none')
    expect(readTestId('editing-name')).toBe('none')

    await click('set-jmeter')

    expect(readTestId('subview')).toBe('jmeter-import')
  })

  it('prefers the persisted script payload when save returns one', async () => {
    const api = {
      getScript: vi.fn(),
    }

    await act(async () => {
      root.render(<AuthoringStateHarness api={api} initialMode="list" />)
    })

    await click('start-draft')
    await click('save-script-with-payload')

    expect(readTestId('subview')).toBe('edit')
    expect(readTestId('editing-id')).toBe('saved-script-2')
    expect(readTestId('editing-name')).toBe('Persisted Checkout')
    expect(readTestId('draft-name')).toBe('none')
    expect(readTestId('authoring-notice')).toBe('none')
  })

  it('owns semantic create and import route actions for the scripts page', async () => {
    const api = {
      getScript: vi.fn(),
    }

    await act(async () => {
      root.render(<AuthoringStateHarness api={api} initialMode="list" />)
    })

    await click('open-sample')

    expect(readTestId('subview')).toBe('sample')
    expect(readTestId('draft-name')).toBe('none')

    await click('open-openapi')

    expect(readTestId('subview')).toBe('openapi-import')

    await click('open-postman')

    expect(readTestId('subview')).toBe('postman-import')

    await click('open-wsdl')

    expect(readTestId('subview')).toBe('wsdl-import')

    await click('start-java-http')

    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('none')
    expect(readTestId('draft-protocol')).toBe('java_http')

    await click('start-template')

    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('Template Draft')
    expect(readTestId('draft-protocol')).toBe('browser')
  })

  it('keeps the current edit shell when the script fetch fails', async () => {
    const api = {
      getScript: vi.fn().mockRejectedValue(new Error('network down')),
    }

    await act(async () => {
      root.render(<AuthoringStateHarness api={api} initialMode="edit" initialEditId="script-404" />)
    })

    await flushPromises()

    expect(api.getScript).toHaveBeenCalledWith('script-404')
    expect(readTestId('subview')).toBe('edit')
    expect(readTestId('editing-id')).toBe('script-404')
    expect(readTestId('editing-name')).toBe('none')
  })

  async function click(testId: string) {
    await act(async () => {
      container.querySelector(`[data-testid="${testId}"]`)?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  function readTestId(testId: string) {
    return container.querySelector(`[data-testid="${testId}"]`)?.textContent
  }
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}
