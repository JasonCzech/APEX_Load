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
    })
    apiMock.createScript.mockResolvedValue({ id: 'script-1' })
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
      format: 'rest_json',
      options: expect.objectContaining({
        include_assertions: true,
        infer_extractors: true,
        sanitize_sensitive: true,
      }),
    }))
    expect(container.textContent).toContain('Review Generated Script')
    expect(container.textContent).toContain('POST_v1_session')
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
      config: expect.objectContaining({
        base_url: 'https://api.example.com',
      }),
    }))
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      name: 'POST_v1_session',
      protocol: 'http',
    }))
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}