// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import LoadRunnerImport from './LoadRunnerImport'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  importLoadRunner: vi.fn(),
  validateLoadRunnerImport: vi.fn(),
  createScript: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

describe('LoadRunnerImport', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.importLoadRunner.mockResolvedValue({
      script: {
        name: 'Demo LoadRunner Script',
        protocol: 'http',
        actions: [{ name: 'Homepage' }],
        metadata: {
          import: {
            source: 'loadrunner',
            source_script_name: 'Demo.usr',
            preserved: {
              runtime_files: { 'default.cfg': {} },
              project_settings: { General: { Type: 'Web' } },
              unsupported_functions: ['web_set_max_html_param_len'],
            },
          },
        },
      },
      warnings: ['manual review recommended'],
      unsupported_elements: ['web_set_max_html_param_len'],
      assumptions: [],
      review_sections: [
        {
          key: 'unsupported_elements',
          title: 'Unsupported LoadRunner Elements',
          severity: 'blocker',
          summary: 'These source constructs were preserved for review but were not converted into executable Stormrunner actions.',
          items: ['web_set_max_html_param_len'],
        },
        {
          key: 'preserved_metadata',
          title: 'Preserved LoadRunner Metadata',
          severity: 'info',
          summary: 'Source project metadata was saved with the generated script for audit and repair workflows.',
          items: ['Project file: Demo.usr'],
        },
      ],
      repair_suggestions: [
        {
          id: 'unsupported:web-set-max-html-param-len',
          category: 'unsupported',
          severity: 'blocker',
          title: 'Replace unsupported web_set_max_html_param_len',
          description: 'Review the preserved source construct.',
          action: 'Open the generated script in the visual editor.',
        },
      ],
      summary: {
        project_file: 'Demo.usr',
        mapped_runtime_file: 'default.cfg',
        project_sections: ['Actions', 'General'],
        runtime_files: ['default.cfg'],
        unsupported_function_count: 1,
      },
      stats: {
        total_functions: 12,
        converted_actions: 1,
        extractors: 1,
        think_times: 2,
        transactions: 1,
        skipped: 0,
      },
    })
    apiMock.validateLoadRunnerImport.mockResolvedValue({
      script: {
        name: 'Demo LoadRunner Script',
        protocol: 'http',
        actions: [{ name: 'Homepage' }],
      },
      warnings: ['validation review'],
      unsupported_elements: ['web_set_max_html_param_len'],
      assumptions: [],
      review_sections: [
        {
          key: 'unsupported_elements',
          title: 'Unsupported LoadRunner Elements',
          severity: 'blocker',
          summary: 'These source constructs were preserved for review but were not converted into executable Stormrunner actions.',
          items: ['web_set_max_html_param_len'],
        },
      ],
      repair_suggestions: [],
      summary: {
        project_file: 'Demo.usr',
        mapped_runtime_file: 'default.cfg',
        project_sections: ['Actions', 'General'],
        runtime_files: ['default.cfg'],
        unsupported_function_count: 1,
      },
      stats: {
        total_functions: 12,
        converted_actions: 1,
        extractors: 1,
        think_times: 2,
        transactions: 1,
        skipped: 0,
      },
    })
    apiMock.createScript.mockResolvedValue({ id: 'saved-script' })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('imports a zip and saves the converted script', async () => {
    const onImported = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onImported={onImported} />)
    })

    expect(container.textContent).toContain('SOAP-aware detection is enabled for LoadRunner request assets')
    expect(container.textContent).toContain('Java-over-HTTP import remains unavailable')

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.importLoadRunner).toHaveBeenCalledWith(file, {
      name: undefined,
      project_id: undefined,
      base_url: undefined,
      include_assertions: true,
      preserve_transactions: true,
    })
    expect(container.textContent).toContain('Conversion Complete')
    expect(container.textContent).toContain('Demo LoadRunner Script')
    expect(container.textContent).toContain('manual review recommended')
    expect(container.textContent).toContain('Unsupported LoadRunner Functions (1)')
    expect(container.textContent).toContain('Import Review Sections (2)')
    expect(container.textContent).toContain('Repair Suggestions (1)')
    expect(container.textContent).toContain('Replace unsupported web_set_max_html_param_len')
    expect(container.textContent).toContain('Unsupported LoadRunner Elements')
    expect(container.textContent).toContain('blocker')
    expect(container.textContent).toContain('web_set_max_html_param_len')
    expect(container.textContent).toContain('Demo.usr')
    expect(container.textContent).toContain('default.cfg')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save to Script Library'))
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), undefined)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }))
  })


  it('runs validate-only review with preserve transaction option', async () => {
    await act(async () => {
      root.render(<LoadRunnerImport />)
    })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const checkboxes = Array.from(container.querySelectorAll('input[type="checkbox"]')) as HTMLInputElement[]
    expect(checkboxes).toHaveLength(2)
    await act(async () => {
      checkboxes[1].dispatchEvent(new MouseEvent('click', { bubbles: true }))
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

    expect(apiMock.validateLoadRunnerImport).toHaveBeenCalledWith(file, {
      name: undefined,
      project_id: undefined,
      base_url: undefined,
      include_assertions: true,
      preserve_transactions: false,
    })
    expect(container.textContent).toContain('Validation Review')
    expect(container.textContent).toContain('validation review')
    expect(container.textContent).toContain('Unsupported LoadRunner Functions (1)')
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}
