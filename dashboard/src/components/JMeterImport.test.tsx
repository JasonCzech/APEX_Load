// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import JMeterImport from './JMeterImport'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getJMeterSupportMatrix: vi.fn(),
  validateJMeterImport: vi.fn(),
  importJMeter: vi.fn(),
  saveJMeterImport: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

describe('JMeterImport', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.getJMeterSupportMatrix.mockResolvedValue({
      support_matrix: {
        supported: ['Thread Group', 'Regex Extractor', 'JSON Extractor'],
        partial: ['Variable substitution across URLs, headers, and bodies'],
        unsupported: ['JSR223 / BeanShell / Groovy scripting'],
        notes: ['Phase 1 targets HTTP-oriented JMX plans.'],
      },
    })
    apiMock.validateJMeterImport.mockResolvedValue({
      scripts: [
        {
          name: 'Auth_Users',
          actions: [
            {
              name: 'Create_Session',
              extractors: [
                { name: 'session_token', type: 'regex', expression: 'token=([^&]+)' },
                { name: 'access_token', type: 'jsonpath', expression: '$.access_token' },
              ],
            },
          ],
          data_sources: [],
        },
      ],
      warnings: [],
      unsupported_elements: [],
      assumptions: ['Flattened safely'],
      review_sections: [],
      repair_suggestions: [],
      stats: {
        thread_groups: 1,
        samplers: 1,
        converted_actions: 1,
        headers: 0,
        variables: 0,
        csv_sources: 0,
        assertions: 0,
        timers: 0,
        unsupported: 0,
      },
      summary: { file_name: 'extractors.jmx' },
    })
    apiMock.importJMeter.mockReset()
    apiMock.saveJMeterImport.mockReset()
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('renders inline coverage details and captured variables after validation', async () => {
    await act(async () => {
      root.render(<JMeterImport />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Coverage Details')
    expect(container.textContent).toContain('JMeter import is still centered on HTTP plans')
    expect(container.textContent).toContain('SOAP-shaped HTTP Request samplers can now be promoted into SOAP scripts')
    expect(container.textContent).toContain('Dedicated SOAP plugins and Java-over-HTTP style transports remain unsupported')

    const coverageDetails = container.querySelector('details') as HTMLDetailsElement | null
    expect(coverageDetails).not.toBeNull()
    if (!coverageDetails) {
      return
    }

    coverageDetails.open = true
    coverageDetails.dispatchEvent(new Event('toggle'))

    expect(container.textContent).toContain('Regex Extractor')
    expect(container.textContent).toContain('JSON Extractor')

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['<jmeterTestPlan />'], 'extractors.jmx', { type: 'application/xml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Only'))
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.validateJMeterImport).toHaveBeenCalledTimes(1)
    expect(apiMock.validateJMeterImport).toHaveBeenCalledWith(file, expect.objectContaining({ project_id: undefined }))
    expect(container.textContent).toContain('Captured Variables (2)')
    expect(container.textContent).toContain('session_token')
    expect(container.textContent).toContain('access_token')
    expect(container.textContent).toContain('1 actions / 0 data sources / 2 extractors')
    expect(container.textContent).toContain('Validation Review')
    expect(container.textContent).toContain('Save Reviewed 1 Script')
  })

  it('routes the reviewed script through the shared edit handoff', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<JMeterImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['<jmeterTestPlan />'], 'extractors.jmx', { type: 'application/xml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Only'))
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const editButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open In Visual Editor'))
    expect(editButton).toBeDefined()
    if (!editButton) {
      return
    }

    await act(async () => {
      editButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Auth_Users',
      actions: expect.any(Array),
    }))
  })

  it('adds the active project id when saving reviewed scripts', async () => {
    apiMock.saveJMeterImport.mockResolvedValueOnce({
      count: 1,
      scripts: [
        {
          id: 'jmeter-script-1',
          name: 'Auth_Users',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-17T00:00:00Z',
        },
      ],
    })

    await act(async () => {
      root.render(<JMeterImport projectId="project-1" />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['<jmeterTestPlan />'], 'extractors.jmx', { type: 'application/xml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Only'))
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Reviewed 1 Script'))
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(apiMock.saveJMeterImport).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ project_id: 'project-1' })],
    }))
  })

  it('returns the first persisted script through the import save handoff', async () => {
    const onImported = vi.fn()
    apiMock.saveJMeterImport.mockResolvedValueOnce({
      count: 1,
      scripts: [
        {
          id: 'jmeter-script-1',
          name: 'Auth_Users',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-17T00:00:00Z',
        },
      ],
    })

    await act(async () => {
      root.render(<JMeterImport onImported={onImported} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['<jmeterTestPlan />'], 'extractors.jmx', { type: 'application/xml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Only'))
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Reviewed 1 Script'))
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      id: 'jmeter-script-1',
      name: 'Auth_Users',
      protocol: 'http',
    }))
  })

  it('passes the active project id into JMeter preview conversion', async () => {
    await act(async () => {
      root.render(<JMeterImport projectId="project-1" />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['<jmeterTestPlan />'], 'extractors.jmx', { type: 'application/xml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Only'))
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.validateJMeterImport).toHaveBeenCalledWith(file, expect.objectContaining({ project_id: 'project-1' }))
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}
