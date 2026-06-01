// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SampleScriptImport from './SampleScriptImport'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  importSample: vi.fn(),
  createScript: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('./LinkedCodePane', () => ({
  default: ({ value, ariaLabel }: { value: string; ariaLabel?: string }) => (
    <pre aria-label={ariaLabel}>{value}</pre>
  ),
}))

describe('SampleScriptImport', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.importSample.mockResolvedValue({
      script: {
        name: 'POST_v1_session',
        protocol: 'http',
        config: {
          base_url: 'https://api.example.com',
        },
        actions: [
          {
            name: '01 - POST v1 session',
            extractors: [],
          },
        ],
      },
      warnings: ['no reusable correlation values were detected across the provided samples'],
      correlations: [
        {
          name: 'session_id',
          source: 'response',
          expression: '$.sessionId',
          sample_value: 'session-12345',
          found_in_step: 1,
          used_in_steps: [2],
        },
      ],
      stats: {
        format: 'rest_json',
        total_requests: 1,
        converted_actions: 1,
        assertions: 1,
        extractors: 1,
        correlations_detected: 1,
      },
      source_review: {
        source_files: [{ path: 'sample.json', language: 'json', content: '{"requests":[]}' }],
        mappings: [{ id: 'sample-request', source_file: 'sample.json', start_line: 1, function: 'rest_json_request', target_path: 'actions[0]', target_name: '01 - POST v1 session', target_kind: 'action', status: 'converted' }],
      },
    })
    apiMock.createScript.mockImplementation(async (script: { name?: string; protocol?: string; version?: string }) => ({
      id: 'script-1',
      name: script.name || 'Saved Script',
      protocol: script.protocol || 'http',
      version: script.version || '1.0.0',
      created_at: '2026-05-17T00:00:00Z',
    }))
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('converts a sample response and saves the generated script', async () => {
    const onImported = vi.fn()

    await act(async () => {
      root.render(<SampleScriptImport onImported={onImported} />)
    })

    expect(container.textContent).toContain('SOAP-shaped raw HTTP, curl, REST JSON, and HAR request samples can now be promoted into SOAP scripts')
    expect(container.textContent).toContain('Mixed sample sets still stay on the HTTP protocol surface')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Sample'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.importSample).toHaveBeenCalledWith(expect.objectContaining({
      project_id: undefined,
      format: 'rest_json',
      options: expect.objectContaining({
        include_assertions: true,
        infer_extractors: true,
        sanitize_sensitive: true,
      }),
    }))
    expect(container.textContent).toContain('Review Generated Script')
    expect(container.textContent).toContain('POST_v1_session')
    expect(container.textContent).toContain('Source Comparison')
    expect(container.textContent).toContain('rest_json_request')
    expect(container.textContent).toContain('actions[0]')
    expect(container.textContent).toContain('session_id')
    expect(container.textContent).toContain('no reusable correlation values were detected across the provided samples')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save To Script Library'))
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      name: 'POST_v1_session',
      protocol: 'http',
      metadata: expect.objectContaining({
        import: expect.objectContaining({
          source: 'sample-import',
          preserved: expect.objectContaining({
            correlation_suggestions: [expect.objectContaining({
              name: 'session_id',
              found_at: 'Step 1',
              used_at: ['Step 2'],
            })],
          }),
        }),
      }),
      config: expect.objectContaining({
        base_url: 'https://api.example.com',
      }),
    }), undefined)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({
        id: 'script-1',
        name: 'POST_v1_session',
        protocol: 'http',
        script: expect.objectContaining({
          metadata: expect.objectContaining({
            import: expect.objectContaining({
              source: 'sample-import',
            }),
          }),
        }),
      })],
    }))
  })

  it('sends the HAR static asset option and surfaces filtered asset counts', async () => {
    apiMock.importSample.mockResolvedValueOnce({
      script: {
        name: 'Imported_HAR_Script',
        protocol: 'http',
        config: { base_url: 'https://shop.example.com' },
        actions: [{ name: 'GET api orders', type: 'request', method: 'GET', target: '/api/orders' }],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: {
        format: 'har',
        total_requests: 3,
        converted_actions: 1,
        filtered_assets: 2,
        assertions: 1,
        extractors: 0,
        correlations_detected: 0,
      },
    })

    await act(async () => {
      root.render(<SampleScriptImport />)
    })

    const formatSelect = container.querySelector('select') as HTMLSelectElement | null
    expect(formatSelect).not.toBeNull()
    if (!formatSelect) return

    await act(async () => {
      formatSelect.value = 'har'
      formatSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const includeStatic = Array.from(container.querySelectorAll('input[type="checkbox"]')).find(input => input.parentElement?.textContent?.includes('Include static assets')) as HTMLInputElement | undefined
    expect(includeStatic).toBeDefined()
    if (!includeStatic) return

    await act(async () => {
      includeStatic.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Sample'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importSample).toHaveBeenCalledWith(expect.objectContaining({
      format: 'har',
      options: expect.objectContaining({
        include_static_assets: true,
      }),
    }))
    expect(container.textContent).toContain('Filtered assets')
    expect(container.textContent).toContain('2')
  })

  it('promotes Java-over-HTTP sample imports into java_http drafts with bounded warnings', async () => {
    const onImported = vi.fn()

    await act(async () => {
      root.render(<SampleScriptImport preset="java-http" onImported={onImported} />)
    })

    expect(container.textContent).toContain('Import Java over HTTP Samples')
    expect(container.textContent).toContain('base64-encoded serialized Java payloads')
    expect(container.textContent).not.toContain('REST JSON')
    expect(container.textContent).not.toContain('HAR / HAR Entry')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Sample'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.importSample).toHaveBeenCalledWith(expect.objectContaining({
      project_id: undefined,
      format: 'raw_http',
    }))
    expect(container.textContent).toContain('Java-over-HTTP imports assume request and response payloads are already base64-encoded serialized Java bytes')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save To Script Library'))
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      protocol: 'java_http',
      config: expect.objectContaining({
        java_http: expect.objectContaining({
          codec: 'java_serialized_object_base64',
          body_encoding: 'base64',
        }),
      }),
    }), undefined)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({
        protocol: 'java_http',
      })],
    }))
  })

  it('passes the active project id into sample conversion previews', async () => {
    await act(async () => {
      root.render(<SampleScriptImport projectId="project-1" />)
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Sample'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.importSample).toHaveBeenCalledWith(expect.objectContaining({ project_id: 'project-1' }))
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}
