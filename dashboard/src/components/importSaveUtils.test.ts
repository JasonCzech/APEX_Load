import { describe, expect, it } from 'vitest'

import { type ScriptDefinition } from '../types/scripts'
import { withFallbackPersistedScript, withFallbackPersistedScripts } from './importSaveUtils'

describe('importSaveUtils', () => {
  it('hydrates a thin persisted script with the reviewed draft payload', () => {
    const fallbackScript: ScriptDefinition = {
      name: 'Imported Checkout',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://example.com' },
      actions: [{ name: '01 - Login', type: 'http', target: '/' }],
    }

    expect(withFallbackPersistedScript({
      id: 'script-1',
      name: 'Imported Checkout',
      protocol: 'http',
      version: '1.0.0',
      created_at: '2026-05-17T00:00:00Z',
    }, fallbackScript)).toEqual(expect.objectContaining({
      script: fallbackScript,
    }))
  })

  it('preserves embedded script payloads when the API already returned them', () => {
    const embeddedScript: ScriptDefinition = {
      name: 'Persisted Checkout',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://persisted.example.com' },
      actions: [{ name: '01 - Login', type: 'http', target: '/' }],
    }

    expect(withFallbackPersistedScript({
      id: 'script-1',
      name: 'Persisted Checkout',
      protocol: 'http',
      version: '1.0.0',
      created_at: '2026-05-17T00:00:00Z',
      script: embeddedScript,
    }, {
      name: 'Fallback Checkout',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://fallback.example.com' },
      actions: [{ name: '01 - Fallback', type: 'http', target: '/' }],
    })).toEqual(expect.objectContaining({
      script: embeddedScript,
    }))
  })

  it('hydrates each saved script with the matching reviewed draft by index', () => {
    const hydratedScripts = withFallbackPersistedScripts([
      {
        id: 'script-1',
        name: 'Auth Users',
        protocol: 'http',
        version: '1.0.0',
        created_at: '2026-05-17T00:00:00Z',
      },
      {
        id: 'script-2',
        name: 'Checkout Users',
        protocol: 'http',
        version: '1.0.0',
        created_at: '2026-05-17T00:00:00Z',
      },
    ], [
      {
        name: 'Auth Users',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://auth.example.com' },
        actions: [{ name: '01 - Login', type: 'http', target: '/' }],
      },
      {
        name: 'Checkout Users',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://checkout.example.com' },
        actions: [{ name: '01 - Checkout', type: 'http', target: '/checkout' }],
      },
    ])

    expect(hydratedScripts).toHaveLength(2)
    expect(hydratedScripts[0]).toEqual(expect.objectContaining({
      id: 'script-1',
      script: expect.objectContaining({
        name: 'Auth Users',
        actions: [{ name: '01 - Login', type: 'http', target: '/' }],
      }),
    }))
    expect(hydratedScripts[1]).toEqual(expect.objectContaining({
      id: 'script-2',
      script: expect.objectContaining({
        name: 'Checkout Users',
        actions: [{ name: '01 - Checkout', type: 'http', target: '/checkout' }],
      }),
    }))
  })
})
