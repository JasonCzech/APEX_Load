// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import RecordingStudio from './RecordingStudio'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listRecordings: vi.fn(),
  getRecorderStatus: vi.fn(),
  getRecording: vi.fn(),
  previewRecordingConversion: vi.fn(),
  convertRecording: vi.fn(),
  createScript: vi.fn(),
  startRecording: vi.fn(),
  stopRecording: vi.fn(),
  pauseRecording: vi.fn(),
  resumeRecording: vi.fn(),
  deleteRecording: vi.fn(),
  updateRecordedRequest: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

describe('RecordingStudio', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.listRecordings.mockResolvedValue({
      sessions: [
        {
          id: 'rec-1',
          name: 'Checkout Capture',
          status: 'stopped',
          started_at: '2026-05-12T20:00:00Z',
          base_url: 'https://shop.example.com',
          requests: [
            {
              seq: 1,
              timestamp: '2026-05-12T20:00:01Z',
              method: 'GET',
              url: 'https://shop.example.com/',
              path: '/',
              host: 'shop.example.com',
              status_code: 200,
              response_time_ms: 123,
              content_type: 'text/html',
              body: '',
              think_time_ms: 0,
              is_static: false,
            },
          ],
          filters: { exclude_static: true },
        },
      ],
    })
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
    })
    apiMock.getRecording.mockResolvedValue(null)
    apiMock.previewRecordingConversion.mockResolvedValue({
      script: {
        name: 'Checkout Preview',
        protocol: 'http',
        actions: [{ name: '01 - GET /' }],
      },
      correlations: [
        {
          name: 'session_id',
          source: 'response',
          expression: '$.sessionId',
          extract_type: 'jsonpath',
          found_in_seq: 1,
          used_in_seqs: [2],
          sample_value: 'abc123',
        },
      ],
      warnings: ['review dynamic headers'],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      stats: {},
      summary: {},
    })
    apiMock.convertRecording.mockResolvedValue({
      script: {
        id: 'script-1',
        name: 'Checkout Converted',
        protocol: 'http',
        actions: [{ name: '01 - GET /' }],
      },
      correlations: [
        {
          name: 'session_id',
          source: 'response',
          expression: '$.sessionId',
          extract_type: 'jsonpath',
          found_in_seq: 1,
          used_in_seqs: [2],
          sample_value: 'abc123',
        },
      ],
      warnings: ['review dynamic headers'],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      stats: {},
      summary: {},
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

  it('reviews a recording conversion and saves it through the shared script flow', async () => {
    const onImported = vi.fn()
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<RecordingStudio onImported={onImported} onEdit={onEdit} />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Recorded Sessions')
    expect(container.textContent).toContain('Checkout Capture')

    const reviewButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Review Script')
    expect(reviewButton).toBeDefined()
    if (!reviewButton) {
      return
    }

    await act(async () => {
      reviewButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.previewRecordingConversion).toHaveBeenCalledWith('rec-1', 'Checkout Capture')
    expect(container.textContent).toContain('Preview generated with 1 actions and 1 correlations')
    expect(container.textContent).toContain('Checkout Preview')
    expect(container.textContent).toContain('review dynamic headers')
    expect(container.textContent).toContain('{{var.session_id}}')
    expect(container.textContent).toContain('Save To Script Library')
    expect(container.textContent).toContain('Open In Visual Editor')

    const editButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Open In Visual Editor')
    expect(editButton).toBeDefined()
    if (!editButton) {
      return
    }

    await act(async () => {
      editButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Checkout Preview',
      protocol: 'http',
      metadata: expect.objectContaining({
        import: expect.objectContaining({
          source: 'recorder',
          preserved: expect.objectContaining({
            correlation_suggestions: [expect.objectContaining({
              name: 'session_id',
              found_at: 'Request 1',
              used_at: ['Request 2'],
            })],
          }),
        }),
      }),
    }))

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Save To Script Library')
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.convertRecording).not.toHaveBeenCalled()
    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Checkout Preview',
      protocol: 'http',
      metadata: expect.objectContaining({
        import: expect.objectContaining({
          source: 'recorder',
        }),
      }),
    }), undefined)
    expect(container.textContent).toContain('Script "Checkout Preview" created with 1 correlations detected')
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Checkout Preview',
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
