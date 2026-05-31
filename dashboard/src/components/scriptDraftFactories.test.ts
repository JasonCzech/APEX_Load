import { describe, expect, it } from 'vitest'

import { type AuthoringComponent } from '../types'
import { composeComponentDraft, createApiDraft, createJavaHttpDraft, createJmsDraft, createSoapDraft, createWebDraft } from './scriptDraftFactories'

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
          client_metrics: true,
          capture_screenshots: 'per_action',
          download_resources: 'recorded',
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

  describe('composeComponentDraft', () => {
    it('seeds an HTTP draft from a request-group component and carries name, actions, data sources, and parameters', () => {
      const component: AuthoringComponent = {
        id: 'http-core-api-request-group',
        kind: 'request_group',
        name: 'Core API Request Group',
        protocols: ['http', 'devweb', 'har', 'jmeter'],
        summary: 'Reusable transaction group.',
        actions: [
          { name: 'Health Check', type: 'request', method: 'GET', target: '/health' },
          { name: 'Lookup Entity', type: 'request', method: 'GET', target: '/api/entities/1' },
        ],
        data_sources: [{ name: 'users', type: 'csv', path: 'users.csv', strategy: 'unique' }],
        parameters: [{ name: 'username', type: 'file', data_source: 'users', column: 'username' }],
      }

      const draft = composeComponentDraft(component)

      expect(draft.protocol).toBe('http')
      expect(draft.name).toBe('Core API Request Group')
      expect(draft.actions.map(action => action.name)).toEqual(['Health Check', 'Lookup Entity'])
      expect(draft.data_sources).toEqual(component.data_sources)
      expect(draft.parameters).toEqual(component.parameters)
    })

    it('folds init, actions, and teardown into a single actions array in order', () => {
      const component: AuthoringComponent = {
        id: 'mixed',
        kind: 'request_group',
        name: 'Mixed Phases',
        protocols: ['http'],
        summary: 'Has every phase.',
        init: [{ name: 'Acquire Token', type: 'request', method: 'POST', target: '/oauth/token' }],
        actions: [{ name: 'Main Call', type: 'request', method: 'GET', target: '/api' }],
        teardown: [{ name: 'Logout', type: 'request', method: 'POST', target: '/logout' }],
      }

      const draft = composeComponentDraft(component)

      expect(draft.actions.map(action => action.name)).toEqual(['Acquire Token', 'Main Call', 'Logout'])
    })

    it('merges component config onto the base draft config', () => {
      const component: AuthoringComponent = {
        id: 'http-oauth-bearer-auth-flow',
        kind: 'auth_flow',
        name: 'OAuth Bearer Token Flow',
        protocols: ['http', 'soap'],
        summary: 'Token acquisition.',
        config: { http: { default_headers: { Authorization: 'Bearer {{var.access_token}}' } } },
        init: [{ name: 'Acquire Access Token', type: 'request', method: 'POST', target: '/oauth/token' }],
      }

      const draft = composeComponentDraft(component)

      expect(draft.config).toEqual(expect.objectContaining({
        base_url: '',
        http: { default_headers: { Authorization: 'Bearer {{var.access_token}}' } },
      }))
    })

    it('selects the browser base draft for browser-family components', () => {
      const component: AuthoringComponent = {
        id: 'browser-login-transaction',
        kind: 'login_transaction',
        name: 'Browser Login Transaction',
        protocols: ['browser', 'trueclient2_web'],
        summary: 'Login sequence.',
        actions: [{ name: 'Open Login', type: 'browser_navigate', target: '/login' }],
      }

      const draft = composeComponentDraft(component)

      expect(draft.protocol).toBe('browser')
      expect(draft.config).toHaveProperty('browser')
    })

    it('selects the SOAP base draft when soap is the leading protocol', () => {
      const component: AuthoringComponent = {
        id: 'soap-only',
        kind: 'request_group',
        name: 'SOAP Group',
        protocols: ['soap'],
        summary: 'SOAP flow.',
        actions: [{ name: 'Call', type: 'POST', target: '/svc' }],
      }

      expect(composeComponentDraft(component).protocol).toBe('soap')
    })

    it('falls back to the HTTP draft for all-protocol (*) components and omits empty data sources', () => {
      const component: AuthoringComponent = {
        id: 'shared-user-data-pool',
        kind: 'data_pool',
        name: 'Shared User Data Pool',
        protocols: ['*'],
        summary: 'CSV-backed data.',
        data_sources: [{ name: 'users', type: 'csv', path: 'users.csv', strategy: 'unique' }],
      }

      const draft = composeComponentDraft(component)

      expect(draft.protocol).toBe('http')
      expect(draft.actions).toEqual([])
      expect(draft.data_sources).toEqual(component.data_sources)
    })
  })
})