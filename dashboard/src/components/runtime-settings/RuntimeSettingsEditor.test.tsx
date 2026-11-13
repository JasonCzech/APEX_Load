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
      '[data-testid="runtime-settings-canonical-group-security_proxy_auth"]',
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
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
      setter?.call(maxTlsSelect!, '1.2')
      maxTlsSelect!.dispatchEvent(new Event('change', { bubbles: true }))
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
      '[data-testid="runtime-settings-canonical-group-transport_connections"]',
    )
    act(() => {
      transportPill?.click()
    })
    expect(container.querySelector('#runtime-settings-active-heading')?.textContent).toBe(
      'Database Runtime',
    )
  })

  function pill(groupId: string) {
    return container.querySelector<HTMLButtonElement>(
      `[data-testid="runtime-settings-canonical-group-${groupId}"]`,
    )
  }

  it('opens WS-Security from the Security pill on SOAP scripts and keeps that pill active', () => {
    act(() => {
      root.render(
        <RuntimeSettingsEditor
          mode="author"
          script={{
            ...baseScript,
            protocol: 'soap',
            config: { base_url: 'https://soap.example.com', soap: { version: '1.1' } },
          }}
        />,
      )
    })

    act(() => {
      pill('security_proxy_auth')?.click()
    })
    const headings = Array.from(container.querySelectorAll('h5')).map(heading => heading.textContent)
    expect(headings).toContain('WS-Security UsernameToken')
    expect(pill('security_proxy_auth')?.getAttribute('aria-pressed')).toBe('true')
    expect(pill('protocol_behavior_session')?.getAttribute('aria-pressed')).toBe('false')

    // Reaching the shared SOAP section another way shows its default group.
    act(() => {
      pill('errors_logging_diagnostics')?.click()
    })
    expect(pill('errors_logging_diagnostics')?.getAttribute('aria-pressed')).toBe('true')
  })

  it('routes the Transport & connections pill to the JMS section for hyphenated JMS protocols', () => {
    const onSelectSection = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsEditor
          mode="author"
          protocol="jms-solace"
          activeSection="script_config"
          onSelectSection={onSelectSection}
        >
          <div />
        </RuntimeSettingsEditor>,
      )
    })
    act(() => {
      pill('transport_connections')?.click()
    })
    expect(onSelectSection).toHaveBeenCalledWith('jms')
  })

  it('filters canonical group pills by search over labels, descriptions and field paths', () => {
    act(() => {
      root.render(
        <RuntimeSettingsEditor
          mode="author"
          script={baseScript}
          catalogFields={[{ path: 'config.http.auth.domain', group: 'security_proxy_auth', label: 'NTLM domain' }]}
        >
          <div />
        </RuntimeSettingsEditor>,
      )
    })
    const search = container.querySelector<HTMLInputElement>('[data-testid="runtime-settings-canonical-group-search"]')
    expect(search).toBeTruthy()
    const typeSearch = (value: string) => {
      act(() => {
        const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
        setter?.call(search!, value)
        search!.dispatchEvent(new Event('input', { bubbles: true }))
      })
    }
    const visibleIds = () =>
      Array.from(container.querySelectorAll('[data-testid="runtime-settings-canonical-groups"] button')).map(button =>
        button.getAttribute('data-canonical-group'),
      )

    typeSearch('think')
    expect(visibleIds()).toEqual(['think_time'])
    typeSearch('tls_timeout')
    expect(visibleIds()).toEqual(['transport_connections'])
    typeSearch('ntlm domain')
    expect(visibleIds()).toEqual(['security_proxy_auth'])
    typeSearch('no such setting')
    expect(visibleIds()).toEqual([])
    expect(container.querySelector('[data-testid="runtime-settings-canonical-group-search-empty"]')).toBeTruthy()
    typeSearch('')
    expect(visibleIds()).toHaveLength(7)
  })

  it('shows the provenance legend and per-group override badges only in debug and scenario modes', () => {
    const provenance = {
      'config.http.tls.max_version': 'group_override',
      'config.http.tls_timeout_ms': 'scenario_default',
      'config.http.connect_timeout_ms': 'session_override',
      'config.think_time.type': 'script',
      'config.log.level': 'engine_default',
    } as const
    const renderMode = (mode: RuntimeSettingsEditorMode) => {
      act(() => {
        root.render(
          <RuntimeSettingsEditor mode={mode} script={baseScript} provenance={provenance}>
            <div />
          </RuntimeSettingsEditor>,
        )
      })
    }

    renderMode('scenario')
    const legend = container.querySelector('[data-testid="runtime-settings-provenance-legend"]')
    expect(Array.from(legend?.querySelectorAll('li') || []).map(item => item.textContent)).toEqual([
      'Session override',
      'Group override',
      'Scenario default',
      'Script value',
      'Engine default',
    ])
    expect(
      container.querySelector('[data-testid="runtime-settings-canonical-group-overrides-transport_connections"]')?.textContent,
    ).toBe('2 overrides')
    expect(
      container.querySelector('[data-testid="runtime-settings-canonical-group-overrides-security_proxy_auth"]')?.textContent,
    ).toBe('1 override')
    expect(container.querySelector('[data-testid="runtime-settings-canonical-group-overrides-think_time"]')).toBeNull()

    renderMode('debug')
    expect(container.querySelector('[data-testid="runtime-settings-provenance-legend"]')).toBeTruthy()

    renderMode('author')
    expect(container.querySelector('[data-testid="runtime-settings-provenance-legend"]')).toBeNull()
    expect(container.querySelector('[data-testid^="runtime-settings-canonical-group-overrides-"]')).toBeNull()
  })

  function typeRailSearch(value: string) {
    const search = container.querySelector<HTMLInputElement>('[data-testid="runtime-settings-canonical-group-search"]')
    expect(search).toBeTruthy()
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(search!, value)
      search!.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }

  function visibleGroupIds() {
    return Array.from(container.querySelectorAll('[data-testid="runtime-settings-canonical-groups"] button')).map(button =>
      button.getAttribute('data-canonical-group'),
    )
  }

  function fieldLabels() {
    return Array.from(container.querySelectorAll('label > span')).map(span => span.textContent?.trim())
  }

  function activeHeading() {
    return container.querySelector('#runtime-settings-active-heading')?.textContent
  }

  // Each field's Go catalog group differs from the group whose default panel
  // its section belongs to, so the pill must open the field's own panel.
  it.each([
    ['max_redirects', 'protocol_behavior_session', 'Max redirects', 'HTTP Transport'],
    ['custom_user_agent', 'target_identity', 'Browser', 'Browser Emulation'],
    ['browser_type', 'target_identity', 'Browser', 'Browser Emulation'],
    ['network_simulation', 'transport_connections', 'Preset', 'Web HTTP/HTML Compatibility'],
  ] as const)('opens the panel that renders the %s search hit', (query, groupId, expectedLabel, expectedHeading) => {
    act(() => {
      root.render(<RuntimeSettingsEditor mode="author" script={baseScript} />)
    })
    typeRailSearch(query)
    expect(visibleGroupIds()).toEqual([groupId])
    act(() => {
      pill(groupId)?.click()
    })
    expect(activeHeading()).toContain(expectedHeading)
    expect(fieldLabels()).toContain(expectedLabel)
    expect(pill(groupId)?.getAttribute('aria-pressed')).toBe('true')
  })

  it('clears a pinned pill once the user moves away, so coming back shows the derived group', () => {
    act(() => {
      root.render(<RuntimeSettingsEditor mode="author" script={baseScript} />)
    })
    typeRailSearch('max_redirects')
    act(() => {
      pill('protocol_behavior_session')?.click()
    })
    expect(container.querySelector('#http-transport-tab-connection')?.getAttribute('aria-selected')).toBe('true')
    expect(pill('protocol_behavior_session')?.getAttribute('aria-pressed')).toBe('true')
    typeRailSearch('')
    act(() => {
      container.querySelector<HTMLButtonElement>('#http-transport-tab-timeouts')?.click()
    })
    expect(pill('transport_connections')?.getAttribute('aria-pressed')).toBe('true')
    act(() => {
      container.querySelector<HTMLButtonElement>('#http-transport-tab-connection')?.click()
    })
    expect(pill('transport_connections')?.getAttribute('aria-pressed')).toBe('true')
    expect(pill('protocol_behavior_session')?.getAttribute('aria-pressed')).toBe('false')
  })

  it('keeps the group default panel when a search hit already lives there', () => {
    act(() => {
      root.render(<RuntimeSettingsEditor mode="author" script={baseScript} />)
    })
    typeRailSearch('keep_alive')
    expect(visibleGroupIds()).toEqual(['transport_connections'])
    act(() => {
      pill('transport_connections')?.click()
    })
    expect(container.querySelector('#http-transport-panel-connection')).toBeTruthy()
    expect(fieldLabels()).toContain('Keep-alive')
  })

  it('routes an override badge pill to the panel that renders the overridden field', () => {
    const renderWith = (provenance: Record<string, 'group_override' | 'scenario_default'>) => {
      act(() => {
        root.render(<RuntimeSettingsEditor mode="scenario" script={baseScript} provenance={provenance} />)
      })
    }

    renderWith({ 'config.http.max_redirects': 'group_override' })
    const badge = container.querySelector('[data-testid="runtime-settings-canonical-group-overrides-protocol_behavior_session"]')
    expect(badge?.textContent).toBe('1 override')
    expect(badge?.getAttribute('title')).toBe('config.http.max_redirects')
    act(() => {
      pill('protocol_behavior_session')?.click()
    })
    expect(container.querySelector('#http-transport-panel-connection')).toBeTruthy()
    expect(fieldLabels()).toContain('Max redirects')
    expect(pill('protocol_behavior_session')?.getAttribute('aria-pressed')).toBe('true')

    act(() => {
      root.unmount()
    })
    root = createRoot(container)
    renderWith({ 'config.browser_emulation.mode': 'scenario_default' })
    expect(
      container.querySelector('[data-testid="runtime-settings-canonical-group-overrides-target_identity"]')?.textContent,
    ).toBe('1 override')
    act(() => {
      pill('target_identity')?.click()
    })
    expect(activeHeading()).toContain('Browser Emulation')
    expect(fieldLabels()).toContain('Browser')
  })
})
