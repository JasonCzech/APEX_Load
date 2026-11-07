import { describe, expect, it } from 'vitest'

import type { ScriptConfig, ScriptDefinition } from '../types/scripts'
import {
  browserProxyCredentialsActive,
  executionBlockerCodeBlocks,
  httpProxyCredentialsActive,
  inactiveProxyCredentialNotes,
  proxyCredentialBlockerText,
  proxyCredentialSecretRefs,
  withoutProxyCredentials,
} from './proxyCredentialSecrets'
import { activeProxyOnlySecretRefs, inactiveProxyOnlySecretNames, scriptInactiveProxyCredentialNotes, withoutInactiveProxySecretNames } from './scriptSecretReferenceModel'

const httpProxy = (mode: string | undefined, url = 'http://proxy.example.test:8080'): ScriptConfig => ({
  base_url: 'https://example.test',
  http: { ...(mode === undefined ? {} : { proxy_mode: mode }), proxy: { url, username: 'svc-user', password: '{{secret.ENTERPRISE_PROXY_PASSWORD}}' } },
} as ScriptConfig)

const scriptWith = (config: ScriptConfig, actions: ScriptDefinition['actions'] = []): ScriptDefinition => ({
  name: 'Proxy', version: '1', protocol: 'http', config, actions,
})

describe('proxy credential rule', () => {
  it('sends HTTP proxy credentials only for Custom, or for no mode with a proxy URL', () => {
    expect(httpProxyCredentialsActive(httpProxy('custom'))).toBe(true)
    expect(httpProxyCredentialsActive(httpProxy(' custom '))).toBe(true)
    expect(httpProxyCredentialsActive(httpProxy(undefined))).toBe(true)
    expect(httpProxyCredentialsActive(httpProxy(undefined, ' '))).toBe(false)
    for (const mode of ['none', 'system', 'pac', 'direct']) expect(httpProxyCredentialsActive(httpProxy(mode))).toBe(false)
    expect(httpProxyCredentialsActive(undefined)).toBe(false)
  })

  it('uses browser proxy credentials only with a server', () => {
    const browser = (server?: string) => ({ base_url: '', browser: { proxy: { ...(server === undefined ? {} : { server }), username: 'u', password: '{{secret.BROWSER_PROXY}}' } } } as ScriptConfig)
    expect(browserProxyCredentialsActive(browser('http://proxy.example.test:3128'))).toBe(true)
    expect(browserProxyCredentialsActive(browser('  '))).toBe(false)
    expect(browserProxyCredentialsActive(browser())).toBe(false)
  })

  it('lists every proxy credential secret reference with whether it is in use', () => {
    const config = {
      ...httpProxy('system'),
      browser: { proxy: { server: 'http://proxy.example.test:3128', username: '{{ secret.browser-proxy-user }}', password: 'plain' } },
    } as ScriptConfig
    expect(proxyCredentialSecretRefs(config)).toEqual([
      { name: 'ENTERPRISE_PROXY_PASSWORD', kind: 'http', field: 'password', active: false, path: '/config/http/proxy/password' },
      { name: 'BROWSER_PROXY_USER', kind: 'browser', field: 'username', active: true, path: '/config/browser/proxy/username' },
    ])
  })

  it('leaves out only the proxy credentials not in use, and returns the script itself when none is', () => {
    const inactive = scriptWith(httpProxy('pac'))
    const stripped = withoutProxyCredentials(inactive)
    expect(stripped.config.http?.proxy).toEqual({ url: 'http://proxy.example.test:8080' })
    expect(inactive.config.http?.proxy?.password).toBe('{{secret.ENTERPRISE_PROXY_PASSWORD}}')
    const active = scriptWith(httpProxy('custom'))
    expect(withoutProxyCredentials(active)).toBe(active)
    expect(withoutProxyCredentials(active, 'all').config.http?.proxy).toEqual({ url: 'http://proxy.example.test:8080' })
  })

  it('words an unbound in-use proxy credential with both ways out', () => {
    const refs = proxyCredentialSecretRefs(httpProxy('custom'))
    expect(proxyCredentialBlockerText('ENTERPRISE_PROXY_PASSWORD', refs)).toEqual({
      title: 'Custom proxy credential not bound',
      description: 'The proxy password uses the secret ENTERPRISE_PROXY_PASSWORD, which is not bound to a project secret. Bind a project secret to it, or change Proxy mode to No proxy, System or PAC.',
    })
    const username = proxyCredentialSecretRefs({ base_url: '', http: { proxy_mode: 'custom', proxy: { url: 'http://p:1', username: '{{secret.PROXY_USER}}' } } } as ScriptConfig)
    expect(proxyCredentialBlockerText('PROXY_USER', username).description).toBe('The proxy username uses the secret PROXY_USER, which is not bound to a project secret. Bind a project secret to it, or change Proxy mode to No proxy, System or PAC.')
    const browser = proxyCredentialSecretRefs({ base_url: '', browser: { proxy: { server: 'http://p:1', password: '{{secret.BROWSER_PROXY}}' } } } as ScriptConfig)
    expect(proxyCredentialBlockerText('BROWSER_PROXY', browser)).toEqual({
      title: 'Browser proxy credential not bound',
      description: 'The browser proxy password uses the secret BROWSER_PROXY, which is not bound to a project secret. Bind a project secret to it, or clear the browser proxy server.',
    })
  })

  it('notes a secret a not-in-use proxy credential references without requiring it', () => {
    const notes = (config: ScriptConfig, kind?: 'http' | 'browser') => scriptInactiveProxyCredentialNotes(scriptWith(config), kind)
    expect(notes(httpProxy('none'))).toEqual(['The proxy password uses the secret ENTERPRISE_PROXY_PASSWORD, but Proxy mode is No proxy, so it is not needed. It becomes required if you switch Proxy mode to Custom.'])
    expect(notes(httpProxy('system'))[0]).toContain('Proxy mode is System,')
    expect(notes(httpProxy('pac'))[0]).toContain('Proxy mode is PAC,')
    expect(notes(httpProxy('custom'))).toEqual([])
    const browser = { base_url: '', browser: { proxy: { username: '{{secret.BROWSER_PROXY}}', password: '{{secret.BROWSER_PROXY}}' } } } as ScriptConfig
    expect(notes(browser, 'http')).toEqual([])
    expect(notes(browser)).toEqual(['The browser proxy username and password use the secret BROWSER_PROXY, but no browser proxy server is set, so it is not needed. It becomes required if you set a browser proxy server.'])
    expect(scriptInactiveProxyCredentialNotes(undefined)).toEqual([])
  })

  it('says nothing is not needed for a proxy secret the script also uses elsewhere', () => {
    // A request header still sends the secret, so it stays required.
    const shared = scriptWith(httpProxy('system'), [{ name: 'Call', type: 'GET', target: '/', headers: { Authorization: 'Bearer {{secret.enterprise-proxy-password}}' } }])
    expect(scriptInactiveProxyCredentialNotes(shared)).toEqual([])
    const browserShared = scriptWith({ base_url: '', browser: { proxy: { password: '{{secret.BP}}' } } } as ScriptConfig, [{ name: 'Call', type: 'GET', target: '/?t={{secret.BP}}' }])
    expect(scriptInactiveProxyCredentialNotes(browserShared, 'browser')).toEqual([])
    // The low-level form notes only the names it is given.
    expect(inactiveProxyCredentialNotes(httpProxy('system'), new Set())).toEqual([])
    expect(inactiveProxyCredentialNotes(httpProxy('system'), new Set(['ENTERPRISE_PROXY_PASSWORD']))).toHaveLength(1)
  })

  it('matches the blocker\'s references canonically and quotes the name as given', () => {
    const username = proxyCredentialSecretRefs({ base_url: '', http: { proxy_mode: 'custom', proxy: { url: 'http://p:1', username: '{{secret.proxy_user}}' } } } as ScriptConfig)
    expect(proxyCredentialBlockerText('proxy_user', username).description).toBe('The proxy username uses the secret proxy_user, which is not bound to a project secret. Bind a project secret to it, or change Proxy mode to No proxy, System or PAC.')
    const browser = proxyCredentialSecretRefs({ base_url: '', browser: { proxy: { server: 'http://p:1', password: '{{secret.bp.pw}}' } } } as ScriptConfig)
    expect(proxyCredentialBlockerText('bp.pw', browser)).toEqual({
      title: 'Browser proxy credential not bound',
      description: 'The browser proxy password uses the secret bp.pw, which is not bound to a project secret. Bind a project secret to it, or clear the browser proxy server.',
    })
  })

  it('names both ways out when an in-use HTTP proxy and browser proxy share the secret', () => {
    const refs = proxyCredentialSecretRefs({
      base_url: '',
      http: { proxy_mode: 'custom', proxy: { url: 'http://p:1', password: '{{secret.P}}' } },
      browser: { proxy: { server: 'http://p:2', username: '{{secret.P}}', password: '{{secret.P}}' } },
    } as ScriptConfig)
    expect(proxyCredentialBlockerText('P', refs)).toEqual({
      title: 'Custom proxy credential not bound',
      description: 'The proxy password uses the secret P, which is not bound to a project secret. Bind a project secret to it, or change Proxy mode to No proxy, System or PAC.'
        + ' The browser proxy username and password use the secret P, which is not bound to a project secret. Bind a project secret to it, or clear the browser proxy server.',
    })
  })

  it('never blocks on an encrypted proxy password code by itself', () => {
    expect(executionBlockerCodeBlocks('encrypted_proxy_password')).toBe(false)
    expect(executionBlockerCodeBlocks('legacy_tls_replacement')).toBe(true)
  })
})

describe('proxy-only secret names', () => {
  it('drops a secret only a not-in-use proxy credential references, keeping one used elsewhere', () => {
    for (const mode of ['none', 'system', 'pac']) {
      const script = scriptWith(httpProxy(mode))
      expect([...inactiveProxyOnlySecretNames(script)]).toEqual(['ENTERPRISE_PROXY_PASSWORD'])
      expect(withoutInactiveProxySecretNames(['enterprise_proxy_password', 'OTHER'], script)).toEqual(['OTHER'])
    }
    const shared = scriptWith(httpProxy('system'), [{ name: 'Login', type: 'POST', target: '/login', body: '{"p":"{{secret.ENTERPRISE_PROXY_PASSWORD}}"}' }])
    expect(inactiveProxyOnlySecretNames(shared).size).toBe(0)
    expect(withoutInactiveProxySecretNames(['ENTERPRISE_PROXY_PASSWORD'], shared)).toEqual(['ENTERPRISE_PROXY_PASSWORD'])
    expect(inactiveProxyOnlySecretNames(scriptWith(httpProxy('custom'))).size).toBe(0)
  })

  it('maps a secret only an in-use proxy credential references to its references', () => {
    const custom = scriptWith(httpProxy('custom'))
    expect([...activeProxyOnlySecretRefs(custom).keys()]).toEqual(['ENTERPRISE_PROXY_PASSWORD'])
    expect(activeProxyOnlySecretRefs(scriptWith(httpProxy('system'))).size).toBe(0)
    const shared = scriptWith(httpProxy('custom'), [{ name: 'Login', type: 'GET', target: '/?p={{secret.ENTERPRISE_PROXY_PASSWORD}}' }])
    expect(activeProxyOnlySecretRefs(shared).size).toBe(0)
  })
})
