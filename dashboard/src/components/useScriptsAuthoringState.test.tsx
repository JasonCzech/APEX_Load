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
      <button data-testid="open-library" onClick={model.openLibrary}>open-library</button>
      <button data-testid="open-create-options" onClick={model.openCreateOptions}>open-create-options</button>
      <button data-testid="set-jmeter" onClick={() => model.setSubview('jmeter-import')}>set-jmeter</button>
      <button data-testid="start-draft" onClick={() => model.startDraft(() => ({ name: 'Draft Checkout', protocol: 'http', config: {}, actions: [] } as never))}>start-draft</button>
      <button data-testid="draft-edit" onClick={() => model.handleDraftEdit({ name: 'Edited Draft', protocol: 'http', config: {}, actions: [] } as never)}>draft-edit</button>
      <button data-testid="load-edit" onClick={() => void model.handleEdit('script-1')}>load-edit</button>
      <button data-testid="open-sample" onClick={model.createOptionActions.openSampleImport}>open-sample</button>
      <button data-testid="start-java-http" onClick={model.createOptionActions.startJavaHttpScript}>start-java-http</button>
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

    await click('open-library')

    expect(readTestId('subview')).toBe('list')
    expect(readTestId('draft-name')).toBe('none')
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

    await click('start-java-http')

    expect(readTestId('subview')).toBe('create')
    expect(readTestId('draft-name')).toBe('none')
    expect(readTestId('draft-protocol')).toBe('java_http')
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