// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ScriptsPage from './ScriptsPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listScripts: vi.fn(),
  getScript: vi.fn(),
  deleteScript: vi.fn(),
  listTests: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => ({
    activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
  }),
}))

vi.mock('./ScriptEditor', () => ({
  default: ({ initialScript, scriptId, onSave, onClose }: { initialScript?: { name?: string }, scriptId?: string | null, onSave?: (script: { id: string; name: string }) => void, onClose?: () => void }) => (
    <div data-testid="script-editor">
      <span>editor:{initialScript?.name || 'blank'}</span>
      <span>script-id:{scriptId || 'none'}</span>
      <button onClick={() => onSave?.({ id: 'saved-script-1', name: initialScript?.name || 'Saved Script' })}>mock-save</button>
      <button onClick={onClose}>mock-close</button>
    </div>
  ),
}))

vi.mock('./LoadRunnerImport', () => ({
  default: ({ onImported }: { onImported?: (script: { id: string; name: string; protocol: string; script?: { name: string; version: string; protocol: string; config: { base_url: string }; actions: unknown[] } }) => void }) => (
    <div data-testid="loadrunner-import">
      <button onClick={() => onImported?.({
        id: 'imported-script-1',
        name: 'Imported LoadRunner Script',
        protocol: 'http',
        script: {
          name: 'Imported LoadRunner Script',
          version: '1.0.0',
          protocol: 'http',
          config: { base_url: 'https://import.example.com' },
          actions: [],
        },
      })}>mock-loadrunner-save</button>
    </div>
  ),
}))

vi.mock('./JMeterImport', () => ({
  default: ({ onEdit }: { onEdit?: (script: { name: string, version: string, protocol: string, config: { base_url: string }, actions: unknown[] }) => void }) => (
    <div data-testid="jmeter-import">
      <button onClick={() => onEdit?.({
        name: 'JMeter Draft',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://jmeter.example.com' },
        actions: [],
      })}
      >mock-jmeter-edit</button>
    </div>
  ),
}))

vi.mock('./RecordingStudio', () => ({
  default: () => <div data-testid="recording-studio">recording-studio</div>,
}))

vi.mock('./SampleScriptImport', () => ({
  default: ({ onEdit, preset }: { onEdit?: (script: { name: string, version: string, protocol: string, config: { base_url: string }, actions: unknown[] }) => void, preset?: string }) => (
    <div data-testid="sample-import">
      <span>sample-import:{preset || 'default'}</span>
      <button onClick={() => onEdit?.({
        name: 'Sample Draft',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://api.example.com' },
        actions: [],
      })}
      >mock-sample-edit</button>
    </div>
  ),
}))

vi.mock('./JavaHttpImport', () => ({
  default: ({ onEdit }: { onEdit?: (script: { name: string, version: string, protocol: string, config: { base_url: string }, actions: unknown[] }) => void }) => (
    <div data-testid="java-http-import">
      <button onClick={() => onEdit?.({
        name: 'Java Import Draft',
        version: '1.0.0',
        protocol: 'java_http',
        config: { base_url: 'https://java.example.com' },
        actions: [],
      })}
      >mock-java-import-edit</button>
    </div>
  ),
}))

describe('ScriptsPage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.listScripts.mockResolvedValue({
      scripts: [
        {
          id: 'script-1',
          name: 'Existing Script',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-12T20:00:00Z',
          scenario_count: 0,
          associated_releases: [],
          scenario_run_releases: [],
          last_run_at: '',
        },
      ],
    })
    apiMock.getScript.mockResolvedValue({ script: null })
    apiMock.deleteScript.mockResolvedValue({})
    apiMock.listTests.mockResolvedValue({ tests: [] })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('routes sample import edit handoff through the draft editor and into persisted edit mode after save', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenCalledWith('project-1')

    expect(container.textContent).toContain('Scripts')
    expect(container.textContent).toContain('Existing Script')

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButton).toBeDefined()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Create Script')

    const sampleButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Build From Sample'))
    expect(sampleButton).toBeDefined()
    if (!sampleButton) {
      return
    }

    await act(async () => {
      sampleButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="sample-import"]')).not.toBeNull()

    const sampleEditButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-sample-edit'))
    expect(sampleEditButton).toBeDefined()
    if (!sampleEditButton) {
      return
    }

    await act(async () => {
      sampleEditButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:Sample Draft')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-save'))
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenCalledTimes(2)
    expect(apiMock.listScripts).toHaveBeenLastCalledWith('project-1')
    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:Sample Draft')
    expect(container.textContent).toContain('script-id:saved-script-1')
  })

  it('routes jmeter import edit handoff through the draft editor', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButton).toBeDefined()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const jmeterButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Import JMeter'))
    expect(jmeterButton).toBeDefined()
    if (!jmeterButton) {
      return
    }

    await act(async () => {
      jmeterButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="jmeter-import"]')).not.toBeNull()

    const jmeterEditButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-jmeter-edit'))
    expect(jmeterEditButton).toBeDefined()
    if (!jmeterEditButton) {
      return
    }

    await act(async () => {
      jmeterEditButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:JMeter Draft')
  })

  it('keeps the persisted editor open after an import save handoff', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButton).toBeDefined()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const loadRunnerButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Import LoadRunner'))
    expect(loadRunnerButton).toBeDefined()
    if (!loadRunnerButton) {
      return
    }

    await act(async () => {
      loadRunnerButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="loadrunner-import"]')).not.toBeNull()

    const importSaveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-loadrunner-save'))
    expect(importSaveButton).toBeDefined()
    if (!importSaveButton) {
      return
    }

    await act(async () => {
      importSaveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenCalledTimes(2)
    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:Imported LoadRunner Script')
    expect(container.textContent).toContain('script-id:imported-script-1')
  })

  it('shows SOAP and Java-over-HTTP create options and routes them into the draft editor', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButton).toBeDefined()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Web Services (SOAP)')
    expect(container.textContent).toContain('Java over HTTP')
    expect(container.textContent).toContain('embedded or sidecar helper execution')

    const soapButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Web Services (SOAP)'))
    expect(soapButton).toBeDefined()
    if (!soapButton) {
      return
    }

    await act(async () => {
      soapButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:blank')

    const closeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-close'))
    expect(closeButton).toBeDefined()
    if (!closeButton) {
      return
    }

    await act(async () => {
      closeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const createButtonAgain = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButtonAgain).toBeDefined()
    if (!createButtonAgain) {
      return
    }

    await act(async () => {
      createButtonAgain.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const javaButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Java over HTTP'))
    expect(javaButton).toBeDefined()
    if (!javaButton) {
      return
    }

    await act(async () => {
      javaButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
  })

  it('routes the Java-over-HTTP import option into the dedicated Java import surface', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButton).toBeDefined()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const javaImportButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Import Java over HTTP'))
    expect(javaImportButton).toBeDefined()
    if (!javaImportButton) {
      return
    }

    await act(async () => {
      javaImportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="java-http-import"]')).not.toBeNull()
  })

  it('loads the requested script when opened in edit mode from the parent route', async () => {
    apiMock.getScript.mockResolvedValueOnce({
      script: {
        id: 'script-42',
        name: 'Loaded Script',
        version: '2.0.0',
        protocol: 'http',
        config: { base_url: 'https://loaded.example.com' },
        actions: [],
      },
    })

    await act(async () => {
      root.render(<ScriptsPage initialMode="edit" initialEditId="script-42" />)
    })

    await flushPromises()

    expect(apiMock.getScript).toHaveBeenCalledWith('script-42')
    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:Loaded Script')
    expect(container.textContent).toContain('script-id:script-42')
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}
