import { describe, expect, it } from 'vitest'

import { createApiDraft, createJavaHttpDraft, createJmsDraft, createSoapDraft, createWebDraft } from './scriptDraftFactories'

describe('scriptDraftFactories', () => {
  it('creates the default API script draft', () => {
    expect(createApiDraft()).toEqual(expect.objectContaining({
      name: '',
      version: '1.0.0',
      protocol: 'http',
      actions: [],
      config: expect.objectContaining({
        base_url: '',
        think_time: { type: 'fixed', mean_ms: 1000 },
        pacing: { type: 'none' },
      }),
    }))
  })

  it('creates the default browser script draft', () => {
    expect(createWebDraft()).toEqual(expect.objectContaining({
      protocol: 'browser',
      config: expect.objectContaining({
        browser: expect.objectContaining({
          engine: 'chromium',
          headless: true,
          capture_screenshots: 'per_action',
          viewport: { width: 1440, height: 900 },
        }),
      }),
    }))
  })

  it('creates the default JMS script draft', () => {
    expect(createJmsDraft()).toEqual(expect.objectContaining({
      protocol: 'jms-solace',
      config: expect.objectContaining({
        jms: expect.objectContaining({
          provider: 'solace',
          destination_type: 'queue',
          acknowledgement: 'auto',
          delivery_mode: 'persistent',
          timeout_ms: 5000,
          mock: true,
        }),
      }),
    }))
  })

  it('creates the default SOAP script draft', () => {
    expect(createSoapDraft()).toEqual(expect.objectContaining({
      protocol: 'soap',
      config: expect.objectContaining({
        soap: expect.objectContaining({
          version: '1.1',
          namespaces: expect.objectContaining({
            soapenv: 'http://schemas.xmlsoap.org/soap/envelope/',
          }),
          fault_handling: expect.objectContaining({
            fail_on_fault: true,
          }),
        }),
      }),
      actions: [expect.objectContaining({
        type: 'POST',
        method: 'POST',
        soap: expect.objectContaining({
          body_mode: 'raw_xml',
        }),
      })],
    }))
  })

  it('creates the Java-over-HTTP draft on top of the HTTP protocol surface', () => {
    expect(createJavaHttpDraft()).toEqual(expect.objectContaining({
      protocol: 'java_http',
      actions: [expect.objectContaining({
        method: 'POST',
      })],
      config: expect.objectContaining({
        base_url: '',
        java_http: expect.objectContaining({
          codec: 'java_serialized_object_base64',
          helper_mode: 'embedded',
          body_encoding: 'base64',
        }),
      }),
    }))
  })
})