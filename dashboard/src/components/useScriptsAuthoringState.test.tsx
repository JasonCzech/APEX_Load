import { pairedAuthoringTestApi, savedAuthoringFixture } from '../test/scriptAuthoringFixtures'
// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useScriptsAuthoringState, type ScriptsAuthoringEntryView } from './useScriptsAuthoringState'
import type { ScriptImportLauncherDescriptor } from '../scriptImportLaunchers'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

type HarnessProps = {
  api: {
    getScript: (id: string) => Promise<{ script?: { id?: string; name?: string; protocol?: string } | null }>
  }
  initialMode?: ScriptsAuthoringEntryView
  initialImport?: ScriptImportLauncherDescriptor | null
  initialEditId?: string
}

function AuthoringStateHarness({ api, initialMode, initialImport, initialEditId, builderStep }: HarnessProps & { builderStep?: 'list' | 'create' | 'record' | 'draft' | null }) {
  const model = useScriptsAuthoringState({ api: { ...api, ...pairedAuthoringTestApi(api.getScript) } as never, initialMode, initialImport, initialEditId, builderStep })

  return (
    <div>
      <button data-testid="open-recorder" onClick={model.createOptionActions.openRecorder}>open-recorder</button>
      <span data-testid="subview">{model.authoringState.subView}</span>
      <span data-testid="import-launcher">{model.authoringState.importLauncher || 'none'}</span>
      <span data-testid="import-mode">{model.authoringState.importMode || 'none'}</span>
      <span data-testid="editing-id">{model.authoringState.editingId || 'none'}</span>
      <span data-testid="editing-creator">{model.authoringState.editingActivity?.creator?.actor_display_name || 'Unknown'}</span>
      <span data-testid="retained-name">{model.authoringState.editingRetainedDraft?.name || 'none'}</span>
      <span data-testid="retained-json">{model.authoringState.editingRetainedJsonText || 'none'}</span>
      <span data-testid="retained-created">{model.authoringState.editingRetainedCreatedSecretNames?.join(',') || 'none'}</span>
      <span data-testid="retained-review">{String(model.authoringState.editingRetainedNeedsReview || false)}</span>
      <span data-testid="authoring-authority">{model.authoringState.editingAuthoring?.etag || 'none'}</span>
      <span data-testid="editing-name">{model.authoringState.editingScript?.name || 'none'}</span>
      <span data-testid="draft-name">{model.authoringState.draftScript?.name || 'none'}</span>
      <span data-testid="draft-protocol">{model.authoringState.draftScript?.protocol || 'none'}</span>
      <span data-testid="draft-target-action">{model.authoringState.draftEditorFocus?.targetAction || 'none'}</span>
      <span data-testid="draft-target-section">{model.authoringState.draftEditorFocus?.targetSection || 'none'}</span>
      <span data-testid="draft-target-data-source">{model.authoringState.draftEditorFocus?.targetDataSource || 'none'}</span>
      <span data-testid="draft-target-runtime-area">{model.authoringState.draftEditorFocus?.targetRuntimeArea || 'none'}</span>
      <span data-testid="draft-return-label">{model.authoringState.draftReturnTarget?.label || 'none'}</span>
      <span data-testid="draft-return-subview">{model.authoringState.draftReturnTarget?.subView || 'none'}</span>
      <span data-testid="draft-dirty">{String(model.authoringState.draftInitiallyDirty)}</span>
      <span data-testid="authoring-notice">{model.authoringState.authoringNotice || 'none'}</span>
      <button data-testid="open-library" onClick={model.openLibrary}>open-library</button>
      <button data-testid="open-create-options" onClick={model.openCreateOptions}>open-create-options</button>
      <button data-testid="set-jmeter" onClick={model.createOptionActions.openJMeterImport}>set-jmeter</button>
      <button data-testid="start-draft" onClick={() => model.startDraft(() => ({ name: 'Draft Checkout', protocol: 'http', config: {}, actions: [] } as never))}>start-draft</button>
      <button data-testid="draft-edit" onClick={() => model.handleDraftEdit({ name: 'Edited Draft', protocol: 'http', config: {}, actions: [] } as never)}>draft-edit</button>
      <button data-testid="draft-edit-focused" onClick={() => model.handleDraftEdit({ name: 'Focused Draft', protocol: 'http', config: {}, actions: [] } as never, { targetAction: 'Submit Order' })}>draft-edit-focused</button>
      <button data-testid="draft-edit-runtime-focused" onClick={() => model.handleDraftEdit({ name: 'Runtime Draft', protocol: 'http', config: {}, actions: [] } as never, { targetSection: 'runtime' })}>draft-edit-runtime-focused</button>
      <button data-testid="draft-edit-runtime-area-focused" onClick={() => model.handleDraftEdit({ name: 'Runtime Area Draft', protocol: 'http', config: {}, actions: [] } as never, { targetSection: 'runtime', targetRuntimeArea: 'Logging' })}>draft-edit-runtime-area-focused</button>
      <button data-testid="draft-edit-parameter-data-focused" onClick={() => model.handleDraftEdit({ name: 'Parameter Draft', protocol: 'http', config: {}, actions: [] } as never, { targetSection: 'parameter-data' })}>draft-edit-parameter-data-focused</button>
      <button data-testid="draft-edit-data-source-focused" onClick={() => model.handleDraftEdit({ name: 'Data Source Draft', protocol: 'http', config: {}, actions: [] } as never, { targetSection: 'parameter-data', targetDataSource: 'login_users' })}>draft-edit-data-source-focused</button>
      <button data-testid="draft-edit-with-return" onClick={() => model.handleDraftEdit({ name: 'Review Draft', protocol: 'http', config: {}, actions: [] } as never, undefined, { subView: 'import', label: 'Back to Converted Script', importLauncher: 'enterprise_archive' })}>draft-edit-with-return</button>
      <button data-testid="return-draft-source" onClick={model.returnToDraftSource}>return-draft-source</button>
      <button data-testid="load-edit" onClick={() => void model.handleEdit('script-1')}>load-edit</button>
      <button data-testid="save-script" onClick={() => model.handleSavedScript({ id: 'saved-script-1', name: 'Saved Draft', protocol: 'http', version: '1.0.0', created_at: '2026-05-17T00:00:00Z' })}>save-script</button>
      <button data-testid="save-script-with-notice" onClick={() => model.handleSavedScript({ id: 'saved-script-3', name: 'Saved Notice Draft', protocol: 'http', version: '1.0.0', created_at: '2026-05-17T00:00:00Z' }, 'Saved 2 scripts. Reviewing the first saved script.')}>save-script-with-notice</button>
      <button data-testid="save-script-retained-summary" onClick={() => model.handleSavedScript({ id: 'saved-retained', name: 'Saved summary', protocol: 'http', version: '1', created_at: '' }, null, { name: 'Retained late draft', version: '1', protocol: 'http', config: { base_url: '' }, actions: [] }, { retainedJsonText: '{"name":"Incomplete late draft",', retainedCreatedSecretNames: ['CREATED_BEFORE_ACK'] })}>save-script-retained-summary</button>
      <button data-testid="save-script-with-payload" onClick={() => model.handleSavedScript(savedAuthoringFixture({ name: 'Persisted Checkout' }, 'saved-script-2'))}>save-script-with-payload</button>
      <button data-testid="open-sample" onClick={model.createOptionActions.openSampleImport}>open-sample</button>
      <button data-testid="open-java-import" onClick={model.createOptionActions.openJavaHttpImport}>open-java-import</button>
      <button data-testid="open-salesforce" onClick={model.createOptionActions.openSalesforceImport}>open-salesforce</button>
      <button data-testid="open-openapi" onClick={model.createOptionActions.openOpenAPIImport}>open-openapi</button>
      <button data-testid="open-raml" onClick={model.createOptionActions.openRAMLImport}>open-raml</button>
      <button data-testid="open-postman" onClick={model.createOptionActions.openPostmanImport}>open-postman</button>
      <button data-testid="open-wsdl" onClick={model.createOptionActions.openWSDLImport}>open-wsdl</button>
      <button data-testid="start-java-http" onClick={model.createOptionActions.startJavaHttpScript}>start-java-http</button>
      <button data-testid="start-template" onClick={() => model.createOptionActions.startTemplateScript({ name: 'Template Draft', protocol: 'browser', config: {}, actions: [] } as never)}>start-template</button>
      <button data-testid="compose-component" onClick={() => model.createOptionActions.composeComponentScript({ id: 'browser-login-transaction', kind: 'login_transaction', name: 'Browser Login Transaction', protocols: ['browser'], summary: 'Login sequence.', actions: [{ name: 'Open Login', type: 'browser_navigate', target: '/login' }] } as never)}>compose-component</button>
      <span data-testid="draft-action-count">{model.authoringState.draftScript?.actions.length ?? 'none'}</span>
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

  it('preserves attribution returned with the script detail view', async () => {
    const api = { getScript: vi.fn().mockResolvedValue({ script: { name: 'Checkout' }, activity: { creator: { actor_display_name: 'Alice' } } }) }
    await act(async () => { root.render(<AuthoringStateHarness api={api} />) })
    await click('load-edit')
    expect(readTestId('editing-creator')).toBe('Alice')
  })

  it('keeps recording open when pairing changes the composite API object', async () => {
    const getScript = vi.fn()
    await act(async () => { root.render(<AuthoringStateHarness api={{ getScript }} initialMode="list" />) })
    await click('open-recorder')
    expect(readTestId('subview')).toBe('recorder')
    await act(async () => { root.render(<AuthoringStateHarness api={{ getScript }} initialMode="list" />) })
    expect(readTestId('subview')).toBe('recorder')
    expect(getScript).not.toHaveBeenCalled()
  })

  it('normalizes create mode to create-options and drives draft entry transitions', async () => {
    const api = {
      getScript: vi.fn().mockResolvedValue({ script: { name: 'Canonical saved script', protocol: 'http' } }),
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

    await click('draft-edit-with-return')

    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('Review Draft')
    expect(readTestId('draft-return-label')).toBe('Back to Converted Script')
    expect(readTestId('draft-return-subview')).toBe('import')

    await click('return-draft-source')

    expect(readTestId('subview')).toBe('import')
    // The return target reopens the importer with the launcher that opened it.
    expect(readTestId('import-launcher')).toBe('enterprise_archive')
    expect(readTestId('draft-name')).toBe('none')
    expect(readTestId('draft-return-label')).toBe('none')
    expect(readTestId('draft-return-subview')).toBe('none')

    await click('draft-edit-data-source-focused')

    await click('save-script')

    expect(readTestId('subview')).toBe('edit')
    expect(readTestId('editing-id')).toBe('saved-script-1')
    expect(api.getScript).toHaveBeenCalledWith('saved-script-1')
    expect(readTestId('editing-name')).not.toBe('Data Source Draft')
    expect(readTestId('draft-name')).toBe('none')
    expect(readTestId('draft-target-action')).toBe('none')
    expect(readTestId('draft-target-section')).toBe('none')
    expect(readTestId('draft-target-data-source')).toBe('none')
    expect(readTestId('draft-target-runtime-area')).toBe('none')
    expect(readTestId('draft-return-label')).toBe('none')
    expect(readTestId('draft-return-subview')).toBe('none')
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
    expect(readTestId('draft-return-label')).toBe('none')
    expect(readTestId('draft-return-subview')).toBe('none')
    expect(readTestId('authoring-notice')).toBe('none')
  })

  it('starts builder drafts that return to the builder, and asks before discarding chosen content', async () => {
    const api = { getScript: vi.fn() }
    await act(async () => {
      root.render(<AuthoringStateHarness api={api} initialMode="create-options" />)
    })
    // A blank editor holds nothing yet: closing it untouched asks nothing.
    await click('start-java-http')
    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-dirty')).toBe('false')
    expect(readTestId('draft-return-label')).toBe('Back to Script builder')
    expect(readTestId('draft-return-subview')).toBe('create-options')
    await click('return-draft-source')
    expect(readTestId('subview')).toBe('create-options')
    expect(readTestId('draft-name')).toBe('none')

    // A template or a composed block is content: it opens unsaved.
    for (const start of ['start-template', 'compose-component']) {
      await click(start)
      expect(readTestId('draft-dirty'), start).toBe('true')
      expect(readTestId('draft-return-subview'), start).toBe('create-options')
      await click('return-draft-source')
      expect(readTestId('subview'), start).toBe('create-options')
    }

    // Saved, it is a library script whose editor still closes to the builder,
    // however many times it is saved again.
    await click('start-template')
    await click('save-script-with-payload')
    expect(readTestId('subview')).toBe('edit')
    expect(readTestId('draft-return-subview')).toBe('create-options')
    await click('save-script-with-payload')
    expect(readTestId('draft-return-subview')).toBe('create-options')
    await click('return-draft-source')
    expect(readTestId('subview')).toBe('create-options')

    // An importer's return is not carried past its save.
    await click('draft-edit-with-return')
    await click('save-script-with-payload')
    expect(readTestId('subview')).toBe('edit')
    expect(readTestId('draft-return-subview')).toBe('none')
  })

  it('keeps the return when a saved builder draft reopens by id', async () => {
    const api = { getScript: vi.fn().mockResolvedValue({ script: { id: 'saved-script-1', name: 'Saved Draft', protocol: 'http' } }) }
    await act(async () => {
      root.render(<AuthoringStateHarness api={api} initialMode="create-options" />)
    })
    await click('start-template')
    // The response has no projection: the script reopens by id.
    await click('save-script')
    await flushPromises()
    expect(readTestId('subview')).toBe('edit')
    expect(readTestId('editing-id')).toBe('saved-script-1')
    expect(readTestId('draft-return-subview')).toBe('create-options')
  })

  it('moves to the page step the route names, keeping a draft only while it is open', async () => {
    const api = { getScript: vi.fn() }
    const render = async (builderStep: 'list' | 'create' | 'record' | 'draft') => {
      await act(async () => {
        root.render(<AuthoringStateHarness api={api} initialMode="create" builderStep={builderStep} />)
      })
    }
    // A route to Recording Studio opens it from the first render.
    await render('record')
    expect(readTestId('subview')).toBe('recorder')
    await render('create')
    expect(readTestId('subview')).toBe('create-options')

    // A draft the page opened is the draft step; the route following it changes nothing.
    await click('start-template')
    await render('draft')
    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('Template Draft')
    // Back to the builder leaves the draft (the page asked about it first).
    await render('create')
    expect(readTestId('subview')).toBe('create-options')
    expect(readTestId('draft-name')).toBe('none')
    await render('list')
    expect(readTestId('subview')).toBe('list')
  })

  it('opens the builder for a route to a draft it cannot restore', async () => {
    await act(async () => {
      root.render(<AuthoringStateHarness api={{ getScript: vi.fn() }} initialMode="create" builderStep="draft" />)
    })
    expect(readTestId('subview')).toBe('create-options')
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
    expect(readTestId('import-launcher')).toBe('jmeter')
  })

  it('opens the importer a route names by its launcher descriptor', async () => {
    const api = { getScript: vi.fn() }
    const cases: [ScriptImportLauncherDescriptor, string, string][] = [
      [{ launcher: 'enterprise_archive', mode: null }, 'import', 'none'],
      [{ launcher: 'jmeter', mode: null }, 'jmeter-import', 'none'],
      [{ launcher: 'sample', mode: 'java_http' }, 'sample', 'java_http'],
      [{ launcher: 'traffic_har', mode: 'auto_detect' }, 'protocol-import', 'auto_detect'],
      [{ launcher: 'k6', mode: null }, 'protocol-import', 'none'],
    ]
    for (const [descriptor, subView, mode] of cases) {
      await act(async () => { root.render(<AuthoringStateHarness key={descriptor.launcher} api={api} initialMode="list" initialImport={descriptor} />) })
      expect(readTestId('subview')).toBe(subView)
      expect(readTestId('import-launcher')).toBe(descriptor.launcher)
      expect(readTestId('import-mode')).toBe(mode)
    }
  })

  it('surfaces edit-load failures as an authoring notice', async () => {
    const api = {
      getScript: vi.fn().mockRejectedValue(new Error('script lookup failed')),
    }

    await act(async () => {
      root.render(<AuthoringStateHarness api={api} initialMode="list" />)
    })

    await click('load-edit')
    await flushPromises()

    expect(api.getScript).toHaveBeenCalledWith('script-1')
    expect(readTestId('subview')).toBe('list')
    expect(readTestId('editing-id')).toBe('none')
    expect(readTestId('authoring-notice')).toBe('script lookup failed')
  })

  it('ignores an edit response after the user returns to the library', async () => {
    const pending = deferred<{ script: { id: string; name: string; protocol: string } }>()
    const api = {
      getScript: vi.fn().mockReturnValue(pending.promise),
    }

    await act(async () => {
      root.render(<AuthoringStateHarness api={api} initialMode="list" />)
    })

    await click('load-edit')
    expect(readTestId('subview')).toBe('list')
    expect(readTestId('editing-name')).toBe('none')

    await click('open-library')
    await act(async () => {
      pending.resolve({ script: { id: 'script-1', name: 'Late Script', protocol: 'http' } })
    })
    await flushPromises()

    expect(readTestId('subview')).toBe('list')
    expect(readTestId('editing-name')).toBe('none')
  })

  it('ignores a late initial edit response after the route requests another script', async () => {
    const first = deferred<{ script: { id: string; name: string; protocol: string } }>()
    const api = {
      getScript: vi.fn().mockImplementation((id: string) => (
        id === 'script-1'
          ? first.promise
          : Promise.resolve({ script: { id: 'script-2', name: 'Current Script', protocol: 'http' } })
      )),
    }

    await act(async () => {
      root.render(<AuthoringStateHarness api={api} initialMode="edit" initialEditId="script-1" />)
    })
    await act(async () => {
      root.render(<AuthoringStateHarness api={api} initialMode="edit" initialEditId="script-2" />)
    })
    await flushPromises()

    expect(readTestId('editing-id')).toBe('script-2')
    expect(readTestId('editing-name')).toBe('Current Script')

    await act(async () => {
      first.resolve({ script: { id: 'script-1', name: 'Late Script', protocol: 'http' } })
    })
    await flushPromises()

    expect(readTestId('editing-id')).toBe('script-2')
    expect(readTestId('editing-name')).toBe('Current Script')
  })

  it('keeps an unqualified saved draft and provenance when its canonical read fails', async () => {
    let reject!: (cause: Error) => void
    const api = { getScript: vi.fn().mockReturnValueOnce(new Promise((_, fail) => { reject = fail })) }
    await act(async () => { root.render(<AuthoringStateHarness api={api} initialMode="list" />) })
    await click('start-draft')
    await click('save-script-retained-summary')
    expect(api.getScript).toHaveBeenCalledTimes(1)
    await act(async () => { reject(new Error('Supported authoring view unavailable')) })
    await flushPromises()
    expect(readTestId('subview')).toBe('edit')
    expect(readTestId('editing-id')).toBe('saved-retained')
    expect(readTestId('editing-name')).toBe('Retained late draft')
    expect(readTestId('retained-name')).toBe('Retained late draft')
    expect(readTestId('retained-json')).toBe('{"name":"Incomplete late draft",')
    expect(readTestId('retained-created')).toBe('CREATED_BEFORE_ACK')
    expect(readTestId('retained-review')).toBe('true')
    expect(readTestId('authoring-authority')).toBe('none')
    expect(readTestId('authoring-notice')).toContain('Supported authoring view unavailable')
    expect(api.getScript).toHaveBeenCalledTimes(1)
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
    expect(readTestId('import-launcher')).toBe('sample')
    expect(readTestId('import-mode')).toBe('standard_api')
    expect(readTestId('draft-name')).toBe('none')

    await click('open-java-import')

    expect(readTestId('subview')).toBe('sample')
    expect(readTestId('import-launcher')).toBe('sample')
    expect(readTestId('import-mode')).toBe('java_http')

    await click('open-openapi')

    expect(readTestId('subview')).toBe('protocol-import')
    expect(readTestId('import-launcher')).toBe('openapi')
    expect(readTestId('import-mode')).toBe('none')

    await click('open-raml')

    expect(readTestId('subview')).toBe('protocol-import')
    expect(readTestId('import-launcher')).toBe('raml')
    expect(readTestId('import-mode')).toBe('none')

    await click('open-salesforce')

    expect(readTestId('subview')).toBe('protocol-import')
    expect(readTestId('import-launcher')).toBe('traffic_har')
    expect(readTestId('import-mode')).toBe('auto_detect')

    await click('open-postman')

    expect(readTestId('subview')).toBe('protocol-import')
    expect(readTestId('import-launcher')).toBe('postman')
    expect(readTestId('import-mode')).toBe('none')

    await click('open-wsdl')

    expect(readTestId('subview')).toBe('protocol-import')
    expect(readTestId('import-launcher')).toBe('wsdl')
    expect(readTestId('import-mode')).toBe('none')

    await click('start-java-http')

    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('none')
    expect(readTestId('draft-protocol')).toBe('java_http')

    await click('start-template')

    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('Template Draft')
    expect(readTestId('draft-protocol')).toBe('browser')

    await click('compose-component')

    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('Browser Login Transaction')
    expect(readTestId('draft-protocol')).toBe('browser')
    expect(readTestId('draft-action-count')).toBe('1')
  })

  it('returns to the library with a notice when an initial edit fetch fails', async () => {
    const api = {
      getScript: vi.fn().mockRejectedValue(new Error('network down')),
    }

    await act(async () => {
      root.render(<AuthoringStateHarness api={api} initialMode="edit" initialEditId="script-404" />)
    })

    await flushPromises()

    expect(api.getScript).toHaveBeenCalledWith('script-404')
    expect(readTestId('subview')).toBe('list')
    expect(readTestId('editing-id')).toBe('none')
    expect(readTestId('editing-name')).toBe('none')
    expect(readTestId('authoring-notice')).toBe('network down')
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

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(next => {
    resolve = next
  })
  return { promise, resolve }
}
