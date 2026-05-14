// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import ScriptEditor from './ScriptEditor'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getScript: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useWebSocket', () => ({
  useWebSocket: vi.fn(),
}))

describe('ScriptEditor', () => {
  let container: HTMLDivElement
  let root: Root

  const initialScript: ScriptDefinition = {
    name: 'Checkout Flow',
    version: '1.0.0',
    protocol: 'http',
    config: {
      base_url: 'https://api.example.com',
      think_time: { type: 'fixed', mean_ms: 1000 },
      pacing: { type: 'none' },
      run_logic: { iterations: 1, auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
    },
    actions: [
      {
        name: 'Login',
        type: 'request',
        method: 'POST',
        target: '/login',
        headers: {
          'Content-Type': 'application/json',
        },
        body: '{"username":"demo"}',
        extractors: [],
        assertions: [{ type: 'status_code', expected: 200 }],
      },
    ],
    parameters: [
      {
        name: 'session_id',
        type: 'counter',
        update: 'each_iteration',
        start: 100,
        step: 1,
      },
    ],
    data_sources: [],
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.getScript.mockResolvedValue({ script: initialScript })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('renders the provided script model without fetching from the API', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    expect(apiMock.getScript).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Action List')
    expect(container.textContent).toContain('Login')
    expect(container.textContent).toContain('session_id')
    expect(container.textContent).toContain('Named Parameters')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'My_Test_Script')
    expect(nameInput?.getAttribute('value')).toBe('Checkout Flow')

    const baseUrlInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'https://api.example.com')
    expect(baseUrlInput?.getAttribute('value')).toBe('https://api.example.com')
    expect(container.textContent).not.toContain('Protocol')
  })

  it('keeps the protocol selector when editing an existing script', async () => {
    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} />)
    })

    expect(container.textContent).toContain('Protocol')
    const protocolSelect = container.querySelector('select')
    expect(protocolSelect?.value).toBe('http')
  })

  it('renders the action workspace with adjacent list and detail regions', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const contentGrid = container.querySelector('.actions-content-grid')
    const sidebar = container.querySelector('.actions-sidebar')
    const detail = container.querySelector('.action-detail')

    expect(contentGrid).not.toBeNull()
    expect(sidebar?.parentElement).toBe(contentGrid)
    expect(detail?.parentElement).toBe(contentGrid)
  })

  it('renders SOAP protocol settings and action fields for SOAP drafts', async () => {
    const soapScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'soap',
      config: {
        ...initialScript.config,
        soap: {
          version: '1.2',
          namespaces: {
            soapenv: 'http://www.w3.org/2003/05/soap-envelope',
          },
          ws_security: {
            username_token: {
              username: 'svc-user',
              password: 'svc-pass',
              password_type: 'text',
            },
            timestamp: {
              ttl_seconds: 60,
            },
          },
          fault_handling: {
            fail_on_fault: true,
          },
        },
      },
      actions: [
        {
          name: 'CreateOrder',
          type: 'POST',
          method: 'POST',
          target: '/soap/orders',
          headers: {},
          body: '<soapenv:Envelope />',
          extractors: [],
          assertions: [{ type: 'status_code', expected: 200 }],
          soap: {
            action: 'urn:CreateOrder',
            operation: 'CreateOrder',
            body_mode: 'raw_xml',
          },
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={soapScript} />)
    })

    expect(container.textContent).toContain('SOAP scripts reuse the HTTP transport model')
    expect(container.textContent).toContain('CreateOrder')

    const runtimeToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Runtime Settings'))
    expect(runtimeToggle).toBeDefined()
    if (!runtimeToggle) {
      return
    }

    await act(async () => {
      runtimeToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const soapToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('SOAP Settings'))
    expect(soapToggle).toBeDefined()
    if (!soapToggle) {
      return
    }

    await act(async () => {
      soapToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('WS-Security UsernameToken')
    expect(container.textContent).toContain('Fail on SOAP Fault')
    expect(container.textContent).toContain('SOAPAction')
  })

  it('renders Java-over-HTTP settings for java_http drafts', async () => {
    const javaScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'java_http',
      config: {
        ...initialScript.config,
        java_http: {
          codec: 'java_serialized_object_base64',
          helper_mode: 'embedded',
          content_type: 'application/x-java-serialized-object',
          accept: 'application/x-java-serialized-object',
          body_encoding: 'base64',
          object_schema: 'com.example.AuthRequest',
          request_mapping: {
            username: 'principal.name',
          },
          response_mapping: {
            'session.token': 'session_token',
          },
          extractor_type: 'opaque',
          preserve_binary_result: true,
        },
      },
      actions: [
        {
          name: 'JavaCall',
          type: 'POST',
          method: 'POST',
          target: '/java/auth',
          headers: {},
          body: 'rO0ABXQABGRlbW8=',
          extractors: [],
          assertions: [{ type: 'status_code', expected: 200 }],
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={javaScript} />)
    })

    expect(container.textContent).toContain('java_serialized_object_base64')
    expect(container.textContent).toContain('Java-over-HTTP scripts reuse the HTTP transport client')

    const runtimeToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Runtime Settings'))
    expect(runtimeToggle).toBeDefined()
    if (!runtimeToggle) {
      return
    }

    await act(async () => {
      runtimeToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const javaToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Java HTTP Settings'))
    expect(javaToggle).toBeDefined()
    if (!javaToggle) {
      return
    }

    await act(async () => {
      javaToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Wire Format')
    expect(container.textContent).toContain('Request Mapping')
    expect(container.textContent).toContain('Response Mapping')
    const principalInput = Array.from(container.querySelectorAll('input')).find(input => input.value === 'principal.name')
    const tokenInput = Array.from(container.querySelectorAll('input')).find(input => input.value === 'session_token')
    expect(principalInput).toBeDefined()
    expect(tokenInput).toBeDefined()
    expect(container.textContent).toContain('Preserve binary responses as base64 text')
  })
})