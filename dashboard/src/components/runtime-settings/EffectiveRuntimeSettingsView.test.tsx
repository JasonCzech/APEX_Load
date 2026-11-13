// @vitest-environment jsdom

import { act, type ReactElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { ScriptConfig } from '../../types/scripts'
import { EffectiveRuntimeSettingsView } from './EffectiveRuntimeSettingsView'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('EffectiveRuntimeSettingsView', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  const render = async (element: ReactElement) => {
    await act(async () => root.render(element))
  }

  const entry = (group: string, path: string) =>
    container.querySelector(`[data-group-name="${group}"] [data-runtime-path="${path}"]`)

  it('groups each group by the canonical labels with a provenance badge per value', async () => {
    await render(
      <EffectiveRuntimeSettingsView
        groups={[
          { name: 'Orders', protocol: 'soap', row: { soap: { version: '1.2', ws_security: { username_token: { password: '[REDACTED]' } } }, override_fields: ['soap.version', 'soap.ws_security.username_token.password'] } },
          { name: 'Browse', protocol: 'http', scriptConfig: { http: { tls: { max_version: '1.3' } } } as ScriptConfig },
        ]}
        scenarioRow={{ base_url: 'https://shop.example.test', env: { HOST: '{{secret.SHOP_HOST}}' } }}
      />,
    )

    const orders = container.querySelector('[data-group-name="Orders"]')
    expect(orders).toBeTruthy()
    if (!orders) return
    expect(orders.textContent).toContain('Orders · soap')
    expect(Array.from(orders.querySelectorAll('[data-canonical-group] > h6')).map(heading => heading.textContent)).toEqual([
      'Target & identity',
      'Security, proxy & auth',
      'Protocol behavior & session',
      'Other runtime settings',
    ])
    expect(entry('Orders', 'soap.version')?.textContent).toBe('SOAP version1.2Group override')
    expect(entry('Orders', 'base_url')?.getAttribute('data-provenance')).toBe('scenario_default')
    expect(entry('Orders', 'base_url')?.textContent).toContain('Scenario default')
    expect(entry('Browse', 'http.tls.max_version')?.textContent).toBe('TLS max version1.3Script value')
    expect(container.textContent).toContain("Settings not listed here run with the script's own values.")
  })

  it('never renders the "[REDACTED]" mask as a value and shows secret references as references', async () => {
    await render(
      <EffectiveRuntimeSettingsView
        groups={[{ name: 'Orders', row: { soap: { ws_security: { username_token: { username: '[REDACTED]', password: '[REDACTED]' } } } } }]}
        scenarioRow={{ env: { HOST: '{{secret.SHOP_HOST}}' }, http: { proxy: { url: 'http://[REDACTED]:[REDACTED]@proxy.example.test:8080' } } }}
      />,
    )

    expect(container.textContent).not.toContain('[REDACTED]')
    expect(entry('Orders', 'soap.ws_security.username_token.password')?.textContent).toContain('Stored secret (hidden)')
    const reference = entry('Orders', 'env.HOST')
    expect(reference?.querySelector('.secret-reference code')?.textContent).toBe('{{secret.SHOP_HOST}}')
    expect(entry('Orders', 'http.proxy.url')?.textContent).toContain('http://***@proxy.example.test:8080')
  })

  it('says a group without overrides runs with its script settings', async () => {
    await render(<EffectiveRuntimeSettingsView groups={[{ name: 'Plain' }]} showGroupNames={false} />)
    expect(container.querySelector('.effective-runtime-group-name')).toBeNull()
    expect(container.textContent).toContain("No group or scenario runtime overrides. This group runs with its script's runtime settings.")
  })
})
