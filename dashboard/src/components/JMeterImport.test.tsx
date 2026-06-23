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

vi.mock('./LinkedCodePane', () => ({
  default: ({ value, ariaLabel }: { value: string; ariaLabel?: string }) => (
    <pre aria-label={ariaLabel}>{value}</pre>
  ),
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
      source_review: {
        source_files: [{ path: 'extractors.jmx', language: 'xml', content: '<HTTPSamplerProxy testname="Create Session" />' }],
        mappings: [{ id: 'sampler', source_file: 'extractors.jmx', start_line: 1, function: 'HTTPSamplerProxy', target_path: 'actions[0]', target_name: 'Create_Session', target_kind: 'action', status: 'converted' }],
      },
      stats: {
        thread_groups: 1,
        samplers: 1,
        total_samplers: 2,
        skipped_samplers: 1,
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

  it('renders the import close action as a Back button', async () => {
    const onClose = vi.fn()

    await act(async () => {
      root.render(<JMeterImport onClose={onClose} />)
    })

    const closeButtons = Array.from(container.querySelectorAll('button'))
      .filter(button => button.textContent?.trim() === 'Back')
    expect(closeButtons).toHaveLength(1)

    await act(async () => {
      closeButtons[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('renders inline coverage details and captured variables after validation', async () => {
    await act(async () => {
      root.render(<JMeterImport />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Coverage Details')
    expect(container.textContent).toContain('HTTP plans convert most completely')
    expect(container.textContent).toContain('native database/network scripts')
    expect(container.textContent).toContain('JMS')

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
    expect(container.textContent).toContain('Source Comparison')
    expect(container.textContent).toContain('HTTPSamplerProxy')
    expect(container.textContent).toContain('actions[0]')
    expect(container.textContent).toContain('Save Reviewed 1 Script')
    const stats = Array.from(container.querySelectorAll('.lr-stat')).map(item => item.textContent)
    expect(stats).toContain('1HTTP Samplers')
    expect(stats).toContain('2Total Samplers')
    expect(stats).toContain('1Skipped Samplers')
  })

  it('renders structured coverage categories including adapter_required', async () => {
    apiMock.getJMeterSupportMatrix.mockResolvedValueOnce({
      support_matrix: {
        supported: ['HTTP Request samplers'],
        partial: [],
        unsupported: ['JMS Publisher / Subscriber / point-to-point samplers'],
      },
      support_matrix_entries: [
        { id: 'jmeter-http-sampler', area: 'Samplers', construct: 'HTTP Request samplers', status: 'supported', execution_status: 'executable' },
        { id: 'jmeter-jsr223', area: 'Scripting', construct: 'JSR223 / BeanShell / Groovy scripting', status: 'partial', execution_status: 'review_required', review_required: true },
        { id: 'jmeter-jdbc', area: 'Samplers', construct: 'JDBC Request samplers', status: 'supported', execution_status: 'executable' },
        { id: 'jmeter-jms', area: 'Samplers', construct: 'JMS Publisher / Subscriber / point-to-point samplers', status: 'review_only', execution_status: 'adapter_required', review_required: true, notes: 'Requires selecting a JMS provider before running.' },
      ],
      notes: ['adapter_required marks constructs that need an operator-supplied adapter or provider.'],
    })

    await act(async () => {
      root.render(<JMeterImport />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Import Coverage')
    expect(container.textContent).toContain('Mapped')
    expect(container.textContent).toContain('Review required')
    expect(container.textContent).toContain('Adapter required')

    const adapterDetails = container.querySelector('details[data-coverage="adapter_required"]') as HTMLDetailsElement | null
    expect(adapterDetails).not.toBeNull()
    expect(adapterDetails?.textContent).toContain('JMS Publisher / Subscriber / point-to-point samplers')
    expect(adapterDetails?.textContent).toContain('Requires selecting a JMS provider before running')
    // adapter_required must be a distinct category from generic review.
    expect(container.querySelector('details[data-coverage="mapped"]')).not.toBeNull()
    expect(container.querySelector('.lr-preserved-card.coverage-adapter_required')).not.toBeNull()
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
      scripts: [expect.objectContaining({
        id: 'jmeter-script-1',
        name: 'Auth_Users',
        protocol: 'http',
        script: expect.objectContaining({
          name: 'Auth_Users',
          actions: expect.arrayContaining([
            expect.objectContaining({
              name: 'Create_Session',
            }),
          ]),
        }),
      })],
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
