// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ScriptConfig, ScriptDefinition } from '../../types/scripts'
import { RuntimeSettingsEditor } from './RuntimeSettingsEditor'
import type { RuntimeSettingsEditorMode } from './canonicalRuntimeTaxonomy'

describe('RuntimeSettingsEditor', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => {
      root.unmount()
    })
    container.remove()
  })

  const baseScript: ScriptDefinition = {
    name: 'Checkout API',
    version: '1.0.0',
    protocol: 'http',
    config: {
      base_url: 'https://api.example.com',
      http: {
        version: 'auto+h3',
        tls: { min_version: '1.2' },
      },
    },
    actions: [],
  }

  it.each([
    ['author', 'Author VUsers runtime settings'],
    ['import', 'Imported VUsers runtime settings'],
    ['debug', 'Debug VUsers session overrides'],
    ['scenario', 'Scenario VUsers runtime overrides'],
  ] as const)('renders canonical 7-group navigation in %s mode with VUsers terminology', (mode: RuntimeSettingsEditorMode, expectedBadge: string) => {
    act(() => {
      root.render(
        <RuntimeSettingsEditor
          mode={mode}
          script={baseScript}
        />,
      )
    })

    const editor = container.querySelector('[data-testid="runtime-settings-editor"]')
    expect(editor?.getAttribute('data-runtime-editor-mode')).toBe(mode)
    expect(
      container.querySelector('[data-testid="runtime-editor-mode-badge"]')?.textContent,
    ).toBe(expectedBadge)

    const rail = container.querySelector('[data-testid="runtime-settings-canonical-groups"]')
    expect(rail).toBeTruthy()
    const pills = Array.from(rail?.querySelectorAll('button') || [])
    expect(pills).toHaveLength(7)
    expect(pills.map(pill => pill.getAttribute('data-canonical-group'))).toEqual([
      'target_identity',
      'run_logic_pacing',
      'think_time',
      'transport_connections',
      'security_proxy_auth',
      'protocol_behavior_session',
      'errors_logging_diagnostics',
    ])
  })

  it('navigates between canonical groups and edits ScriptConfig via onChangeConfig', () => {
    const onGroupSelect = vi.fn()
    function Harness() {
      const [config, setConfig] = useState<ScriptConfig>({
        base_url: 'https://api.example.com',
        http: { version: '3', tls: { min_version: '1.2' } },
      })
      return (
        <RuntimeSettingsEditor
          mode="scenario"
          protocol="http"
          config={config}
          onChangeConfig={updater => setConfig(prev => updater(prev))}
          onSelectGroup={onGroupSelect}
        />
      )
    }

    act(() => {
      root.render(<Harness />)
    })

    const securityPill = container.querySelector<HTMLButtonElement>(
      '[data-testid="runtime-canonical-group-security_proxy_auth"]',
    )
    expect(securityPill).toBeTruthy()
    act(() => {
      securityPill?.click()
    })
    expect(onGroupSelect).toHaveBeenCalledWith('security_proxy_auth')
    expect(container.querySelector('#http-transport-panel-tls')).toBeTruthy()

    const maxTlsSelect = container.querySelector<HTMLSelectElement>(
      'select[aria-label="Max TLS version"]',
    )
    expect(maxTlsSelect).toBeTruthy()
    act(() => {
      if (!maxTlsSelect) return
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
      setter?.call(maxTlsSelect, '1.2')
      maxTlsSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const warning = container.querySelector('[data-testid="http-tls-max-version-h3-warning"]')
    expect(warning?.textContent).toContain('HTTP/3 (QUIC) requires TLS 1.3')
  })

  it('filters out HTTP/SOAP-only groups for native database protocol scripts', () => {
    act(() => {
      root.render(
        <RuntimeSettingsEditor
          mode="author"
          script={{
            ...baseScript,
            protocol: 'database',
            config: {
              base_url: '',
              database: { driver: 'sqlite', dsn: ':memory:' },
            },
          }}
        />,
      )
    })

    const rail = container.querySelector('[data-testid="runtime-settings-canonical-groups"]')
    const groupIds = Array.from(rail?.querySelectorAll('button') || []).map(pill =>
      pill.getAttribute('data-canonical-group'),
    )
    expect(groupIds).toEqual([
      'target_identity',
      'run_logic_pacing',
      'think_time',
      'transport_connections',
      'errors_logging_diagnostics',
    ])

    const transportPill = container.querySelector<HTMLButtonElement>(
      '[data-testid="runtime-canonical-group-transport_connections"]',
    )
    act(() => {
      transportPill?.click()
    })
    expect(container.querySelector('#runtime-settings-active-heading')?.textContent).toBe(
      'Database Runtime',
    )
  })
})
