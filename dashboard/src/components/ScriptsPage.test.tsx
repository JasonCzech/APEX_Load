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

vi.mock('./ScriptEditor', () => ({
  default: ({ initialScript, onSave, onClose }: { initialScript?: { name?: string }, onSave?: () => void, onClose?: () => void }) => (
    <div data-testid="script-editor">
      <span>editor:{initialScript?.name || 'blank'}</span>
      <button onClick={onSave}>mock-save</button>
      <button onClick={onClose}>mock-close</button>
    </div>
  ),
}))

vi.mock('./LoadRunnerImport', () => ({
  default: () => <div data-testid="loadrunner-import">loadrunner-import</div>,
}))

vi.mock('./JMeterImport', () => ({
  default: () => <div data-testid="jmeter-import">jmeter-import</div>,
}))

vi.mock('./RecordingStudio', () => ({
  default: () => <div data-testid="recording-studio">recording-studio</div>,
}))

vi.mock('./SampleScriptImport', () => ({
  default: ({ onEdit }: { onEdit?: (script: { name: string, version: string, protocol: string, config: { base_url: string }, actions: unknown[] }) => void }) => (
    <div data-testid="sample-import">
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

  it('routes sample import edit handoff through the draft editor and back to the library', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

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
    expect(container.textContent).toContain('Scripts')
    expect(container.textContent).toContain('Existing Script')
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}