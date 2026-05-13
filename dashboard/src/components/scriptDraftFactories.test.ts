import { describe, expect, it } from 'vitest'

import { createApiDraft, createJmsDraft, createWebDraft } from './scriptDraftFactories'

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
})