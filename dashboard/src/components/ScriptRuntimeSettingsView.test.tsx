// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { ScriptRuntimeSettingsSidebar, ScriptRuntimeSettingsView } from './ScriptRuntimeSettingsView'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function javaVuserScript(legacy = false): ScriptDefinition {
  return {
    name: 'Imported Java Vuser',
    version: '1.0.0',
    protocol: 'java_vuser',
    config: {
      base_url: '',
      java_vuser: {
        jdk_selection: 'internal',
        jdk_home: '/opt/jdks/temurin-21',
        classpath: ['/opt/apexload/java-vuser/classes'],
        additional_vm_parameters: ['-Xmx512m'],
        use_xbootclasspath: true,
        dedicated_class_loader: false,
      },
    },
    ...(legacy ? {} : {
      init: [{ name: 'Run init', type: 'java_vuser_run', target: 'Actions', params: { method: 'init' } }],
      teardown: [{ name: 'Run end', type: 'java_vuser_run', target: 'Actions', params: { method: 'end' } }],
    }),
    actions: [{
      name: 'Run lifecycle',
      type: 'java_vuser_run',
      target: 'Actions',
      params: { method: legacy ? 'full_lifecycle' : 'action' },
    }],
  }
}

describe('ScriptRuntimeSettingsView Java Vuser settings', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('clears distribution fields when recorded think time is selected', () => {
    const onUpdateField = vi.fn()
    const script: ScriptDefinition = {
      name: 'Imported web script',
      version: '1.0.0',
      protocol: 'http',
      config: {
        base_url: '',
        think_time: {
          type: 'gaussian',
          mean_ms: 1000,
          std_ms: 200,
          min_ms: 100,
          max_ms: 2500,
          multiplier: 1.5,
          limit_ms: 5000,
        },
      },
      actions: [],
    }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="think"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })
    const distribution = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Distribution'))
    act(() => {
      expect(distribution).toBeTruthy()
      if (!distribution) return
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
      setter?.call(distribution, 'recorded')
      distribution.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(onUpdateField).toHaveBeenCalledWith('config.think_time', {
      type: 'recorded',
      multiplier: 1.5,
      limit_ms: 5000,
    })
  })

  it('parses CRLF source IP entries as separate lines', () => {
    const onUpdateField = vi.fn()
    const script: ScriptDefinition = {
      name: 'Imported web script',
      version: '1.0.0',
      protocol: 'http',
      config: {
        base_url: '',
        web_runtime: {},
      },
      actions: [],
    }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="web_runtime"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })

    const sourceIpTextarea = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Source IP pool"]')
    expect(sourceIpTextarea).toBeTruthy()
    if (!sourceIpTextarea) return

    const setValue = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
    setValue?.call(sourceIpTextarea, '10.0.0.5\r\n10.0.0.6\r\n')
    sourceIpTextarea.dispatchEvent(new Event('input', { bubbles: true }))

    expect(onUpdateField).toHaveBeenCalledWith(
      'config.web_runtime',
      expect.objectContaining({
        source_ips: ['10.0.0.5', '10.0.0.6'],
      }),
    )
  })

  it('renders the governed Java Env Settings subsections without a Base URL', () => {
    const onUpdateField = vi.fn()
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="java_vuser"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={javaVuserScript()}
        />,
      )
    })

    expect(container.textContent).toContain('Java Env Settings')
    expect(container.textContent).toContain('Virtual Machine')
    expect(container.textContent).toContain('Class Loading')
    expect(container.textContent).toContain('Performance')
    expect(container.textContent).toContain('Skip automatic parameter evaluation')
    expect(container.textContent).toContain('ordinary classpath')
    expect(container.textContent).toContain('own Actions instance and child loader for its full lifecycle, and its own JVM unless shared JVMs are on')
    expect(container.textContent).toContain('JVM sharing')
    expect(container.textContent).toContain('saved LoadRunner parameters persist')
    expect(container.textContent).not.toContain('Base URL')
    expect(container.textContent).not.toContain('Runner Command')

    const jdkHome = container.querySelector<HTMLInputElement>('input[aria-label="JDK Home"]')
    expect(jdkHome?.disabled).toBe(true)

    const specified = container.querySelectorAll<HTMLInputElement>('input[name="script-java-vuser-jdk-selection"]')[1]
    act(() => specified.click())
    expect(onUpdateField).toHaveBeenCalledWith(
      'config.java_vuser',
      expect.objectContaining({ jdk_selection: 'specified' }),
    )
  })

  it('adds a new VM parameter row and commits the full argument list to config.java_vuser', () => {
    const onUpdateField = vi.fn()
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="java_vuser"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={javaVuserScript()}
        />,
      )
    })

    const setInputValue = (input: HTMLInputElement, value: string) => {
      const setValue = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setValue?.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    }

    const addButtons = Array.from(container.querySelectorAll<HTMLButtonElement>('button.row-add'))
    const addVmParam = addButtons.find(button => button.textContent?.includes('Add VM parameter'))
    expect(addVmParam).not.toBeNull()
    act(() => addVmParam!.click())

    const vmRows = container.querySelectorAll<HTMLInputElement>('input[aria-label^="Additional VM Parameters entry"]')
    const newRow = vmRows[vmRows.length - 1]
    act(() => setInputValue(newRow, '-Dlabel=hello world'))
    act(() => newRow.dispatchEvent(new FocusEvent('focusout', { bubbles: true })))

    expect(onUpdateField).toHaveBeenCalledWith(
      'config.java_vuser',
      expect.objectContaining({ additional_vm_parameters: ['-Xmx512m', '-Dlabel=hello world'] }),
    )
  })

  it('removes a classpath row and commits the shortened list', () => {
    const onUpdateField = vi.fn()
    const script = javaVuserScript()
    script.config.java_vuser!.classpath = ['/one.jar', '/two.jar']
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="java_vuser"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })

    const removeFirst = container.querySelector<HTMLButtonElement>('button[aria-label="Remove Java Vuser classpath entry 1"]')
    expect(removeFirst).not.toBeNull()
    act(() => removeFirst!.click())
    expect(onUpdateField).toHaveBeenCalledWith(
      'config.java_vuser',
      expect.objectContaining({ classpath: ['/two.jar'] }),
    )
  })

  it('commits JDK home, class loading, xboot, skip flag, and timeout writes to config.java_vuser', () => {
    const onUpdateField = vi.fn()
    const script = javaVuserScript()
    script.config.java_vuser!.jdk_selection = 'specified'
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="java_vuser"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })

    const setInputValue = (input: HTMLInputElement, value: string) => {
      const setValue = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setValue?.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    }

    const jdkHome = container.querySelector<HTMLInputElement>('input[aria-label="JDK Home"]')
    expect(jdkHome?.disabled).toBe(false)
    act(() => setInputValue(jdkHome!, '/opt/jdks/temurin-17'))
    act(() => jdkHome!.dispatchEvent(new FocusEvent('focusout', { bubbles: true })))
    expect(onUpdateField).toHaveBeenCalledWith(
      'config.java_vuser',
      expect.objectContaining({ jdk_home: '/opt/jdks/temurin-17' }),
    )

    const checkboxes = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
    const xboot = checkboxes.find(input => input.closest('label')?.textContent?.includes('-Xbootclasspath'))
    expect(xboot?.checked).toBe(true)
    act(() => xboot!.click())
    expect(onUpdateField).toHaveBeenCalledWith(
      'config.java_vuser',
      expect.objectContaining({ use_xbootclasspath: false }),
    )

    const dedicated = checkboxes.find(input => input.closest('label')?.textContent?.includes('dedicated class loader'))
    expect(dedicated?.checked).toBe(false)
    act(() => dedicated!.click())
    expect(onUpdateField).toHaveBeenCalledWith(
      'config.java_vuser',
      expect.objectContaining({ dedicated_class_loader: true }),
    )

    const skip = checkboxes.find(input => input.closest('label')?.textContent?.includes('Skip automatic parameter evaluation'))
    expect(skip?.checked).toBe(false)
    act(() => skip!.click())
    expect(onUpdateField).toHaveBeenCalledWith(
      'config.java_vuser',
      expect.objectContaining({ skip_parameter_evaluation: true }),
    )

    const startup = container.querySelector<HTMLInputElement>('input[aria-label="Java Vuser startup timeout"]')
    act(() => setInputValue(startup!, '5'))
    expect(onUpdateField).toHaveBeenCalledWith('config.java_vuser.startup_timeout_ms', 5000)

    const action = container.querySelector<HTMLInputElement>('input[aria-label="Java Vuser action timeout"]')
    act(() => setInputValue(action!, '45'))
    expect(onUpdateField).toHaveBeenCalledWith('config.java_vuser.action_timeout_ms', 45000)
  })

  it('does not offer the Java Env section for non-Java protocols', () => {
    const httpScript: ScriptDefinition = {
      name: 'Plain HTTP',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.com' },
      actions: [{ name: 'Get root', type: 'http_request', target: '/', params: { method: 'GET' } }],
    }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="script_config"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={httpScript}
        />,
      )
    })

    expect(container.textContent).not.toContain('Java Env Settings')
    expect(container.querySelector('input[aria-label="JDK Home"]')).toBeNull()
    expect(container.textContent).toContain('Base URL')
  })

  it.each([
    { protocol: 'http', version: undefined, expected: 'Auto-negotiate (HTTP/2 or HTTP/1.1)', selection: '' },
    { protocol: 'http/1.1', version: '', expected: 'HTTP/1.1 required', selection: '' },
    { protocol: 'http/2', version: undefined, expected: 'HTTP/2 required (no fallback)', selection: '' },
    { protocol: 'http/2', version: '1.1', expected: 'HTTP/1.1 required', selection: '1.1' },
    { protocol: 'http/1.1', version: 'HTTP/2.0', expected: 'HTTP/2 required (no fallback)', selection: '2' },
    { protocol: 'http/2', version: '  ', expected: 'Auto-negotiate (HTTP/2 or HTTP/1.1)', selection: 'existing-auto' },
  ])('aligns the summary and global select for $protocol setting $version', ({ protocol, version, expected, selection }) => {
    const onUpdateField = vi.fn()
    const script: ScriptDefinition = { name: 'Version settings', version: '1', protocol, config: { base_url: '', http: { version } }, actions: [] }
    act(() => root.render(<>
      <ScriptRuntimeSettingsSidebar script={script} parityEntries={[]} readiness={null} />
      <ScriptRuntimeSettingsView activeHTTPTransportTab="connection" activeSection="http" canEditProtocol={false}
        onSave={vi.fn()} onSelectHTTPTransportTab={vi.fn()} onSelectSection={vi.fn()} onUpdateField={onUpdateField}
        onUpdateProtocol={vi.fn()} saveDisabled saving={false} script={script} />
    </>))
    expect(container.querySelector('.utility-list')?.textContent).toContain(`HTTP version: ${expected}`)
    const select = Array.from(container.querySelectorAll<HTMLSelectElement>('select')).find(item => item.parentElement?.textContent?.includes('HTTP version'))!
    expect(select.value).toBe(selection)
    expect(select.getAttribute('aria-invalid')).toBe('false')
    expect(container.textContent).toContain(`Effective script policy: ${expected}`)
    if (protocol === 'http/2') expect(select.options[0].textContent).toBe('Protocol default (HTTP/2 required (no fallback))')
    expect(onUpdateField).not.toHaveBeenCalled()
    expect(script.config.http?.version).toBe(version)
    expect(Array.from(select.options).filter(option => !option.disabled).map(option => option.value)).toEqual(['', '1.1', '2', 'auto+h3', '3'])
  })

  it.each([null, 'auto', '1.0', 2, {}, []].map(value => ({ value })))('retains malformed global $value visibly until an intentional edit', ({ value }) => {
    const onUpdateField = vi.fn()
    const script = { name: 'Malformed version', version: '1', protocol: 'http/2', config: { base_url: '', http: { version: value, max_conns_per_host: 7, vendor_extension: { keep: true } } }, actions: [] } as unknown as ScriptDefinition
    act(() => root.render(<ScriptRuntimeSettingsView activeHTTPTransportTab="connection" activeSection="http" canEditProtocol={false}
      onSave={vi.fn()} onSelectHTTPTransportTab={vi.fn()} onSelectSection={vi.fn()} onUpdateField={onUpdateField}
      onUpdateProtocol={vi.fn()} saveDisabled saving={false} script={script} />))
    const select = Array.from(container.querySelectorAll<HTMLSelectElement>('select')).find(item => item.parentElement?.textContent?.includes('HTTP version'))!
    expect(select.value).toBe('invalid')
    expect(select.getAttribute('aria-invalid')).toBe('true')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('original value is preserved')
    expect(onUpdateField).not.toHaveBeenCalled()
    expect(script.config.http?.version).toBe(value)
    act(() => { select.value = ''; select.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(onUpdateField).toHaveBeenCalledWith('config.http', { ...script.config.http, version: '' })
    expect(script.config.http?.version).toBe(value)
  })

  it('edits config.http.quic in the Connection tab and reports HTTP/3 conflicts at the version field', () => {
    const onUpdateField = vi.fn()
    const view = (script: ScriptDefinition) => <ScriptRuntimeSettingsView activeHTTPTransportTab="connection" activeSection="http" canEditProtocol={false}
      onSave={vi.fn()} onSelectHTTPTransportTab={vi.fn()} onSelectSection={vi.fn()} onUpdateField={onUpdateField}
      onUpdateProtocol={vi.fn()} saveDisabled saving={false} script={script} />
    const http = { version: '3', max_conns_per_host: 4, quic: { alt_svc_cache: 'per_iteration' as const } }
    act(() => root.render(view({ name: 'QUIC', version: '1', protocol: 'http', config: { base_url: 'http://shop.example', http }, actions: [] })))
    expect(container.querySelector('.http3-policy-issue')?.textContent).toContain('only https origins')
    expect(container.querySelector('.http3-execution-note')?.textContent).toContain('runs need HTTP/3 execution enabled')
    const handshake = Array.from(container.querySelectorAll('label')).find(label => label.querySelector('span')?.textContent === 'Handshake timeout (ms)')!.querySelector('input')!
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(handshake, '4000')
      handshake.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenLastCalledWith('config.http', { ...http, quic: { alt_svc_cache: 'per_iteration', handshake_timeout_ms: 4000 } })
    // Shaping applies to TCP only: the panel is disabled and both policies are refused.
    act(() => root.render(view({ name: 'QUIC', version: '1', protocol: 'http', config: { base_url: 'https://shop.example', http: { version: 'auto+h3' }, web_runtime: { network_simulation: { mode: 'preset', preset: 'dsl' } } }, actions: [] })))
    expect(container.querySelector<HTMLFieldSetElement>('fieldset.http-quic-settings')?.disabled).toBe(true)
    expect(container.querySelector('.http3-policy-issue')?.textContent).toContain('Network shaping')
  })

  it('edits the NTLM domain only for the NTLM auth types', () => {
    const onUpdateField = vi.fn()
    const script = (type: string): ScriptDefinition => ({ name: 'Auth', version: '1', protocol: 'http', config: { base_url: '', http: { auth: { type, username: 'ada', password: 'pw' } } }, actions: [] })
    const view = (type: string) => <ScriptRuntimeSettingsView activeHTTPTransportTab="authentication" activeSection="http" canEditProtocol={false}
      onSave={vi.fn()} onSelectHTTPTransportTab={vi.fn()} onSelectSection={vi.fn()} onUpdateField={onUpdateField}
      onUpdateProtocol={vi.fn()} saveDisabled saving={false} script={script(type)} />
    act(() => root.render(view('ntlm')))
    const domain = container.querySelector('input[aria-label="Auth domain"]') as HTMLInputElement
    expect(domain).not.toBeNull()
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(domain, 'CORP')
      domain.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenLastCalledWith('config.http', { auth: { type: 'ntlm', username: 'ada', password: 'pw', domain: 'CORP' } })
    act(() => root.render(view('negotiate_any')))
    expect(container.querySelector('input[aria-label="Auth domain"]')).toBeNull()
  })

  it('stores auto-negotiated HTTP version as the DSL empty value', () => {
    const onUpdateField = vi.fn()
    const script: ScriptDefinition = {
      name: 'HTTP version settings',
      version: '1.0.0',
      protocol: 'http',
      config: {
        base_url: 'https://api.example.com',
        http: { version: '2' },
      },
      actions: [],
    }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="connection"
          activeSection="http"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })

    const version = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('HTTP version'))
    expect(version?.value).toBe('2')
    expect(Array.from(version?.options || []).map(option => option.textContent)).toEqual([
      'Auto-negotiate (HTTP/2 or HTTP/1.1)',
      'Require HTTP/1.1',
      'Require HTTP/2 (no fallback)',
      'Upgrade to HTTP/3 when advertised',
      'Require HTTP/3 (no fallback)',
    ])
    expect(version?.querySelector('option[value="1.0"]')).toBeNull()
    expect(container.textContent).toContain('required HTTP/2 never falls back to HTTP/1.1')
    act(() => {
      expect(version).toBeTruthy()
      if (!version) return
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
      setter?.call(version, '')
      version.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(onUpdateField).toHaveBeenCalledWith('config.http', { version: '' })
    expect(container.textContent).not.toContain('runs need HTTP/3 execution enabled')
    act(() => {
      expect(version).toBeTruthy()
      if (!version) return
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
      setter?.call(version, '3')
      version.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenLastCalledWith('config.http', { version: '3' })
  })

  it('keeps an HTTP/3 script policy selected and names its execution requirement', () => {
    const script: ScriptDefinition = { name: 'H3', version: '1', protocol: 'http', config: { base_url: 'https://api.example.com', http: { version: 'auto+h3' } }, actions: [] }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView activeHTTPTransportTab="connection" activeSection="http" canEditProtocol={false}
          onSave={vi.fn()} onSelectHTTPTransportTab={vi.fn()} onSelectSection={vi.fn()} onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()} saveDisabled={false} saving={false} script={script} />,
      )
    })
    const version = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('HTTP version'))
    expect(version?.value).toBe('auto+h3')
    expect(version?.getAttribute('aria-invalid')).toBe('false')
    expect(container.textContent).toContain('Effective script policy: Upgrade to HTTP/3 when advertised')
    expect(container.textContent).toContain('runs need HTTP/3 execution enabled')
  })

  it('offers target TPS pacing in script runtime settings', () => {
    const script: ScriptDefinition = {
      name: 'TPS HTTP',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.com', pacing: { type: 'tps', tps: 12.5 } },
      actions: [{ name: 'Get root', type: 'http_request', target: '/', params: { method: 'GET' } }],
    }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="pacing"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })

    expect(container.textContent).toContain('Target iteration rate (TPS)')
    expect(container.textContent).toContain('Target TPS')
    expect(container.querySelector<HTMLInputElement>('input[type="number"]')?.value).toBe('12.5')
  })

  it('shows imported fixed-interval pacing in seconds in the summary and editor', () => {
    const script: ScriptDefinition = {
      name: 'Imported HTTP',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.com', pacing: { type: 'fixed_interval', interval_ms: 5000 } },
      actions: [{ name: 'Get root', type: 'http_request', target: '/', params: { method: 'GET' } }],
    }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="pacing"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })

    expect(container.textContent).toContain('Fixed interval · 5 seconds')
    const interval = Array.from(container.querySelectorAll('label'))
      .find(label => label.textContent?.includes('Interval (s)'))
      ?.querySelector('input')
    expect(interval?.value).toBe('5')
  })

  it('labels legacy full_lifecycle scripts as one-shot instead of promising persistent Java state', () => {
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="java_vuser"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={javaVuserScript(true)}
        />,
      )
    })

    expect(container.textContent).toContain('legacy full_lifecycle script uses one-shot JVM execution')
    expect(container.textContent).toContain('do not persist between action invocations')
    expect(container.textContent).not.toContain('keeps one JVM, Actions instance, and child loader for its full lifecycle')
  })
})

describe('ScriptRuntimeSettingsView run-evidence redaction', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  function renderLogSection(log: ScriptDefinition['config']['log'], onUpdateField = vi.fn()) {
    const script: ScriptDefinition = {
      name: 'Imported web script',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: '', log },
      actions: [],
    }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="log"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })
    return onUpdateField
  }

  function redactionCheckbox(): HTMLInputElement {
    const checkbox = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(input => input.closest('label')?.textContent?.includes('Redact credentials in run evidence'))
    if (!checkbox) throw new Error('run-evidence redaction checkbox not rendered')
    return checkbox
  }

  it('renders unchecked by default when the script carries no explicit value', () => {
    renderLogSection({ level: 'standard' })
    expect(redactionCheckbox().checked).toBe(false)
    expect(container.textContent).toContain('run logs, snapshots, reports, archives and debugger evidence')
    expect(container.textContent).toContain('project secrets and DSL credentials are always redacted')
    expect(container.textContent).toContain('Does not change the script itself')
  })

  it('emits an explicit true when enabled, keeping the rest of the log block', () => {
    const onUpdateField = renderLogSection({ level: 'extended', on_error_only: true, extended_options: ['advanced_trace'] })
    act(() => redactionCheckbox().click())
    expect(onUpdateField).toHaveBeenCalledWith('config.log', {
      level: 'extended',
      on_error_only: true,
      extended_options: ['advanced_trace'],
      redact_run_evidence: true,
    })
    const written = onUpdateField.mock.calls[onUpdateField.mock.calls.length - 1][1]
    expect(Object.prototype.hasOwnProperty.call(written, 'redact_run_evidence')).toBe(true)
    expect(written.redact_run_evidence).toBe(true)
  })

  it('renders unchecked for an explicit false and re-enables redaction on click', () => {
    const onUpdateField = renderLogSection({ level: 'standard', redact_run_evidence: false })
    expect(redactionCheckbox().checked).toBe(false)
    act(() => redactionCheckbox().click())
    expect(onUpdateField).toHaveBeenCalledWith('config.log', { level: 'standard', redact_run_evidence: true })
  })
})

describe('ScriptRuntimeSettingsView JMS TLS settings', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('edits config.jms.tls without dropping the rest of the JMS block', () => {
    const onUpdateField = vi.fn()
    const script: ScriptDefinition = {
      name: 'Solace order feed',
      version: '1.0.0',
      protocol: 'jms-solace',
      config: {
        base_url: '',
        jms: { provider: 'solace', broker_url: 'tcps://broker.example.com:55443', mock: false },
      },
      actions: [],
    }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="jms"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })
    const caCert = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
      .find(input => input.closest('label')?.textContent?.includes('TLS CA Certificate Secret'))
    expect(caCert).toBeTruthy()
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(caCert!, 'jms-broker-ca')
      caCert!.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.jms', {
      provider: 'solace',
      broker_url: 'tcps://broker.example.com:55443',
      mock: false,
      tls: { ca_cert_secret: 'jms-broker-ca' },
    })
  })
})

describe('ScriptRuntimeSettingsView Event Hubs settings', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  // Mirrors what pkg/convert/eventhubscapture emits for an Event Hubs Capture
  // import: protocol "eventhubs" plus a populated config.eventhubs block.
  function eventHubsScript(): ScriptDefinition {
    return {
      name: 'eventhubs-capture-import',
      version: '1.0.0',
      protocol: 'eventhubs',
      config: {
        base_url: '',
        eventhubs: { mock: true, event_hub: 'orders' },
      },
      actions: [{ name: 'Produce', type: 'eventhubs_produce', target: 'orders', body: '{}' }],
    }
  }

  function renderEventHubs(onUpdateField = vi.fn()) {
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="eventhubs"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={eventHubsScript()}
        />,
      )
    })
    return onUpdateField
  }

  it('renders the Event Hubs section for an imported capture script', () => {
    renderEventHubs()
    expect(container.textContent).toContain('Event Hubs Runtime')
    expect(container.textContent).toContain('Observer Start Position')
    const eventHub = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
      .find(input => input.closest('label')?.textContent?.includes('Event Hub'))
    expect(eventHub?.value).toBe('orders')
  })

  it('edits config.eventhubs without dropping the fields it does not touch', () => {
    const onUpdateField = renderEventHubs()
    const consumerGroup = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
      .find(input => input.closest('label')?.textContent?.includes('Consumer group'))
    expect(consumerGroup).toBeTruthy()
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(consumerGroup!, 'replay-probe')
      consumerGroup!.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.eventhubs', {
      mock: true,
      event_hub: 'orders',
      consumer_group: 'replay-probe',
    })
  })

  it('hides the Event Hubs section for other protocols', () => {
    const script = eventHubsScript()
    script.protocol = 'http'
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="think"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })
    expect(container.textContent).not.toContain('Event Hubs Runtime')
  })
})

describe('ScriptRuntimeSettingsView imported source runtime settings', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  function importedScript(): ScriptDefinition {
    return {
      name: 'Imported LR script',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://svc.example.test' },
      actions: [],
      metadata: {
        import: {
          source: 'enterprise_script_archive',
          preserved: {
            runtime_files: {
              'default.cfg': {
                WEB: { HTTPVer: '1.1', HttpErrorsAsWarnings: '1', WebRecorderVersion: '8' },
                ModemSpeed: { EnableModemSpeed: '0' },
              },
              'other.cfg': { WEB: { HTTPVer: '1.0' } },
            },
            runtime_setting_dispositions: {
              'default.cfg': {
                WEB: {
                  HTTPVer: { status: 'mapped', parity_id: 'http_transport', fields: ['config.http.version'] },
                  HttpErrorsAsWarnings: { status: 'compatibility_only', note: 'Downgrades HTTP errors to warnings.' },
                  WebRecorderVersion: { status: 'non_applicable', note: 'VuGen recorder version stamp.' },
                },
                ModemSpeed: {
                  EnableModemSpeed: { status: 'mapped', parity_id: 'network_simulation', fields: ['config.web_runtime.network_simulation'] },
                },
              },
            },
          },
        },
      },
    }
  }

  it('renders every preserved key with its disposition badge', () => {
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="imported_source"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={importedScript()}
        />,
      )
    })

    expect(container.textContent).toContain('Imported Source Runtime Settings')
    expect(container.textContent).toContain('4 settings dispositioned')
    // Selected profile rows show their dispositions.
    expect(container.textContent).toContain('Mapped → config.http.version')
    expect(container.textContent).toContain('Compatibility-only')
    expect(container.textContent).toContain('Not applicable')
    expect(container.textContent).toContain('[ModemSpeed]')
    // Non-selected profiles stay visible as preserved-only evidence.
    expect(container.textContent).toContain('Not selected at import — preserved only')
    expect(container.textContent).toContain('Preserved (profile not selected)')
    expect(container.querySelector('.parameter-sidebar-list > .imported-runtime-file summary')).toBeNull()
    expect(container.querySelectorAll('.imported-runtime-file-header')).toHaveLength(2)
    expect(container.querySelectorAll('.imported-runtime-table[data-table-pagination="off"]')).toHaveLength(3)
    expect(container.querySelector('.runtime-settings-tab[aria-selected="true"]')?.textContent).toContain('Imported Source Runtime Settings')
    expect(Array.from(container.querySelectorAll('.runtime-settings-tab')).some(tab => tab.textContent?.includes('Preserved run order'))).toBe(false)
  })

  it('filters source setting groups with pills and keeps preserved run order in the same view', () => {
    const script = importedScript()
    script.config.run_logic = { block_scheduling: 'random', action_sequence: ['teardown', 'action', 'init'] }
    act(() => root.render(
      <ScriptRuntimeSettingsView activeHTTPTransportTab="timeouts" activeSection="imported_source" canEditProtocol={false}
        onSave={vi.fn()} onSelectHTTPTransportTab={vi.fn()} onSelectSection={vi.fn()} onUpdateField={vi.fn()}
        onUpdateProtocol={vi.fn()} saveDisabled={false} saving={false} script={script} />,
    ))
    const groups = container.querySelector('[aria-label="Imported runtime setting groups"]')!
    const pills = Array.from(groups.querySelectorAll<HTMLButtonElement>('.imported-runtime-group-pill'))
    const web = pills.find(pill => pill.textContent?.includes('[WEB]'))!
    const modem = pills.find(pill => pill.textContent?.includes('[ModemSpeed]'))!
    expect(pills).toHaveLength(2)
    expect(web.getAttribute('aria-pressed')).toBe('true')
    expect(modem.getAttribute('aria-pressed')).toBe('true')
    expect(container.querySelector('[aria-label="Preserved run order"]')?.textContent).toContain('teardown, action, init')
    act(() => web.click())
    expect(container.querySelectorAll('.imported-runtime-table')).toHaveLength(1)
    expect(container.textContent).not.toContain('HttpErrorsAsWarnings')
    act(() => (Array.from(groups.querySelectorAll('button')).find(button => button.textContent === 'None') as HTMLButtonElement).click())
    expect(container.querySelectorAll('.imported-runtime-table')).toHaveLength(0)
    expect(container.textContent).toContain('No groups selected')
    act(() => (Array.from(groups.querySelectorAll('button')).find(button => button.textContent === 'All') as HTMLButtonElement).click())
    expect(container.querySelectorAll('.imported-runtime-table')).toHaveLength(3)
    expect(web.getAttribute('aria-pressed')).toBe('true')
  })

  it('shows imported run-logic blocks as driving execution', () => {
    const script = importedScript()
    script.config.run_logic = {
      block_scheduling: 'sequential',
      blocks: [{ name: 'Run', mode: 'percentage', children: [{ action: 'browse', weight: 70 }, { action: 'buy', weight: 30 }, { block: { name: 'Inner', mode: 'random', children: [{ action: 'search' }] } }] }],
    }
    act(() => root.render(
      <ScriptRuntimeSettingsView activeHTTPTransportTab="timeouts" activeSection="imported_source" canEditProtocol={false}
        onSave={vi.fn()} onSelectHTTPTransportTab={vi.fn()} onSelectSection={vi.fn()} onUpdateField={vi.fn()}
        onUpdateProtocol={vi.fn()} saveDisabled={false} saving={false} script={script} />,
    ))
    const section = container.querySelector('[aria-label="Imported run logic"]')
    expect(section?.textContent).toContain('Drives execution.')
    expect(section?.textContent).toContain('browse — 70%')
    expect(section?.textContent).toContain('Inner (random)')
    expect(container.querySelector('[aria-label="Preserved run order"]')).toBeNull()
  })

  it('shows relative run-logic weights as shares', () => {
    const script = importedScript()
    script.config.run_logic = { blocks: [{ name: 'Run', mode: 'percentage', children: [{ action: 'browse', weight: 2 }, { action: 'buy', weight: 1 }] }] }
    act(() => root.render(
      <ScriptRuntimeSettingsView activeHTTPTransportTab="timeouts" activeSection="imported_source" canEditProtocol={false}
        onSave={vi.fn()} onSelectHTTPTransportTab={vi.fn()} onSelectSection={vi.fn()} onUpdateField={vi.fn()}
        onUpdateProtocol={vi.fn()} saveDisabled={false} saving={false} script={script} />,
    ))
    const section = container.querySelector('[aria-label="Imported run logic"]')
    expect(section?.textContent).toContain('browse — 66.7%')
    expect(section?.textContent).toContain('buy — 33.3%')
  })

  it('hides the section entirely when the script was not imported', () => {
    const script = importedScript()
    delete script.metadata
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="think"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })
    expect(container.textContent).not.toContain('Imported Source Runtime Settings')
  })
})

// The SOAP, Java HTTP and Web HTTP/HTML sections were lifted out of the
// ScriptRuntimeSettingsView render function into ScriptRuntimeSettingsProtocolSections.
// These cases drive them through the view so the extracted props (script,
// section activation, field writes, proxy summary) stay wired to the same
// config paths the single-function version wrote to.
describe('ScriptRuntimeSettingsView extracted protocol sections', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  function renderView(script: ScriptDefinition, activeSection: 'soap' | 'java_http' | 'web_runtime', onUpdateField = vi.fn()) {
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection={activeSection}
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })
    return onUpdateField
  }

  // The runtime tab strip (rendered by ScriptRuntimeSettingsView itself) emits the same
  // RuntimeSectionIcon glyph and category text as every section header, so a
  // whole-container query is satisfied by the tabs alone and stays
  // green even when the extracted section renders no icon or ignores its props.
  // Scope header assertions to the detail pane and to the named section's header.
  function sectionHeader(label: string) {
    const detail = container.querySelector('.runtime-settings-detail')
    expect(detail).not.toBeNull()
    const header = Array.from(detail!.querySelectorAll<HTMLElement>('.settings-section > .section-toggle'))
      .find(toggle => toggle.querySelector('.section-label')?.textContent === label)
    expect(header, `no "${label}" section header in the detail pane`).toBeTruthy()
    return header!
  }

  function typeInto(input: HTMLInputElement | HTMLTextAreaElement, value: string) {
    act(() => {
      const prototype = input instanceof HTMLTextAreaElement ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype
      Object.getOwnPropertyDescriptor(prototype, 'value')?.set?.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }

  it('edits a SOAP namespace URI without dropping the rest of the SOAP block', () => {
    const script: ScriptDefinition = {
      name: 'Order service',
      version: '1.0.0',
      protocol: 'soap',
      config: {
        base_url: 'https://soap.example.test',
        soap: {
          version: '1.1',
          namespaces: { ord: 'https://example.test/orders' },
          ws_security: { username_token: { username: 'svc' } },
        },
      },
      actions: [],
    }
    const onUpdateField = renderView(script, 'soap')

    expect(sectionHeader('SOAP Settings').querySelector('[data-runtime-section-icon="soap"]')).not.toBeNull()
    const uriInput = container.querySelector<HTMLInputElement>('input.kv-val')
    expect(uriInput?.value).toBe('https://example.test/orders')
    typeInto(uriInput!, 'https://example.test/orders/v2')

    expect(onUpdateField).toHaveBeenCalledWith('config.soap', {
      version: '1.1',
      namespaces: { ord: 'https://example.test/orders/v2' },
      ws_security: { username_token: { username: 'svc' } },
    })
  })

  it('edits a Java HTTP response mapping without dropping the rest of the java_http block', () => {
    const script: ScriptDefinition = {
      name: 'Java over HTTP',
      version: '1.0.0',
      protocol: 'java_http',
      config: {
        base_url: 'https://java.example.test',
        java_http: {
          codec: 'java_serialized_object_base64',
          helper_mode: 'embedded',
          response_mapping: { 'payload.user': 'userId' },
        },
      },
      actions: [],
    }
    const onUpdateField = renderView(script, 'java_http')

    expect(sectionHeader('Java HTTP Settings').querySelector('[data-runtime-section-icon="java_http"]')).not.toBeNull()
    const valueInput = container.querySelector<HTMLInputElement>('[data-catalog-path="config.java_http.response_mapping"] input[aria-label="Response mapping value 1"]')
    expect(valueInput?.value).toBe('userId')
    typeInto(valueInput!, 'accountUserId')

    expect(onUpdateField).toHaveBeenCalledWith('config.java_http', {
      codec: 'java_serialized_object_base64',
      helper_mode: 'embedded',
      response_mapping: { 'payload.user': 'accountUserId' },
    })
  })

  it('refuses a Java HTTP response mapping rename onto an existing key and keeps focus on the row', () => {
    let current: ScriptDefinition = {
      name: 'Java over HTTP',
      version: '1.0.0',
      protocol: 'java_http',
      config: {
        base_url: 'https://java.example.test',
        java_http: { codec: 'java_serialized_object_base64', response_mapping: { ab: 'x', a: 'y' } },
      },
      actions: [],
    }
    const onUpdateField = vi.fn((path: string, value: unknown) => {
      if (path === 'config.java_http') {
        current = { ...current, config: { ...current.config, java_http: value as ScriptDefinition['config']['java_http'] } }
        renderView(current, 'java_http', onUpdateField)
      }
    })
    renderView(current, 'java_http', onUpdateField)
    const group = () => container.querySelector('[data-catalog-path="config.java_http.response_mapping"]')!
    const key = (row: number) => group().querySelector<HTMLInputElement>(`input[aria-label="Response mapping key ${row}"]`)!
    expect(key(1).value).toBe('ab')
    expect(key(2).value).toBe('a')
    act(() => key(2).focus())
    typeInto(key(2), 'ab')
    expect(onUpdateField).not.toHaveBeenCalled()
    expect(current.config.java_http?.response_mapping).toEqual({ ab: 'x', a: 'y' })
    expect(group().querySelector('[role="alert"]')?.textContent).toContain('ab is already defined')
    expect(document.activeElement).toBe(key(2))
    // A valid rename keeps the same row focused and leaves the other entry alone.
    typeInto(key(2), 'abc')
    expect(current.config.java_http).toEqual({ codec: 'java_serialized_object_base64', response_mapping: { ab: 'x', abc: 'y' } })
    expect(document.activeElement).toBe(key(2))
    expect(key(2).value).toBe('abc')
    // Removing the last entries drops the key instead of storing an empty map.
    act(() => group().querySelector<HTMLButtonElement>('button[aria-label="Remove Response mapping entry ab"]')!.click())
    act(() => group().querySelector<HTMLButtonElement>('button[aria-label="Remove Response mapping entry abc"]')!.click())
    expect(current.config.java_http).toEqual({ codec: 'java_serialized_object_base64' })
  })

  it('shows only the Java HTTP fields the embedded validator accepts and lets a rejected stored value be removed', () => {
    const script: ScriptDefinition = {
      name: 'Java over HTTP',
      version: '1.0.0',
      protocol: 'java_http',
      config: {
        base_url: 'https://java.example.test',
        java_http: {
          codec: 'java_serialized_object_base64',
          helper_mode: 'sidecar',
          object_schema: 'com.example.AuthRequest',
          request_mapping: { userId: 'payload.user' },
          extractor_type: 'regex',
        },
      },
      actions: [],
    }
    const onUpdateField = renderView(script, 'java_http')

    const helper = container.querySelector<HTMLSelectElement>('[data-catalog-path="config.java_http.helper_mode"] select')!
    // sidecar is shown as the stored value it is, never offered as a choice.
    expect(helper.value).toBe('sidecar')
    expect(Array.from(helper.options).map(option => option.value)).toEqual(['embedded', 'sidecar'])
    expect(helper.options[1].textContent).toContain('not supported')
    // No editors for the rejected fields: only removable notices.
    expect(Array.from(container.querySelectorAll('label')).some(label => label.textContent?.includes('Object Schema'))).toBe(false)
    expect(Array.from(container.querySelectorAll('[data-unsupported-stored-value]')).map(node => node.getAttribute('data-unsupported-stored-value'))).toEqual([
      'config.java_http.object_schema',
      'config.java_http.extractor_type',
      'config.java_http.request_mapping',
    ])
    const removeRequestMapping = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Remove request mapping')!
    act(() => removeRequestMapping.click())
    expect(onUpdateField).toHaveBeenLastCalledWith('config.java_http', {
      codec: 'java_serialized_object_base64',
      helper_mode: 'sidecar',
      object_schema: 'com.example.AuthRequest',
      extractor_type: 'regex',
    })
  })

  it('shows an unset Java HTTP codec as unset and preserve binary as off', () => {
    const script: ScriptDefinition = {
      name: 'Java over HTTP', version: '1.0.0', protocol: 'java_http', actions: [],
      config: { base_url: 'https://java.example.test', java_http: {} },
    }
    renderView(script, 'java_http')
    const codec = container.querySelector<HTMLSelectElement>('[data-catalog-path="config.java_http.codec"] select')!
    expect(codec.value).toBe('')
    expect(codec.options[0].textContent).toContain('required')
    expect(container.querySelector<HTMLInputElement>('[data-catalog-path="config.java_http.preserve_binary_result"] input')!.checked).toBe(false)
  })

  it('edits the Web HTTP/HTML source IP pool and shows the proxy summary in the header', () => {
    const script: ScriptDefinition = {
      name: 'Web script',
      version: '1.0.0',
      protocol: 'http',
      config: {
        base_url: 'https://shop.example.test',
        http: { proxy_mode: 'custom', proxy: { url: 'http://proxy.example.test:8080' } },
        web_runtime: { source_ips: ['10.0.0.5'] },
      },
      actions: [],
    }
    const onUpdateField = renderView(script, 'web_runtime')

    const webHeader = sectionHeader('Web HTTP/HTML Compatibility')
    expect(webHeader.querySelector('[data-runtime-section-icon="web_runtime"]')).not.toBeNull()
    // httpProxySummary is derived by the view and passed into the extracted section.
    expect(webHeader.querySelector('.section-summary')?.textContent).toBe('Proxy configured')
    const proxyURL = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
      .find(input => input.closest('label')?.textContent?.includes('Proxy URL'))
    expect(proxyURL?.value).toBe('http://proxy.example.test:8080')
    expect(container.querySelector('[data-testid="web-runtime-canonical-proxy-note"]')?.textContent)
      .toContain('Security, proxy & auth')

    const sourceIPs = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Source IP pool"]')
    expect(sourceIPs?.value).toBe('10.0.0.5')
    typeInto(sourceIPs!, '10.0.0.5\n10.0.0.6')

    expect(onUpdateField).toHaveBeenCalledWith('config.web_runtime', {
      source_ips: ['10.0.0.5', '10.0.0.6'],
    })
  })

  it('preserves content-check left_boundary, right_boundary, and case_insensitive when editing in Web HTTP/HTML Compatibility', () => {
    const script: ScriptDefinition = {
      name: 'LR imported content checks',
      version: '1.0.0',
      protocol: 'http',
      config: {
        base_url: 'https://shop.example.test',
        web_runtime: {
          content_checks: [
            {
              text: 'FatalError',
              scope: 'body_only',
              fail_if_found: true,
              left_boundary: '<msg>',
              right_boundary: '</msg>',
              case_insensitive: true,
            },
          ],
        },
      },
      actions: [],
    }
    const onUpdateField = renderView(script, 'web_runtime')

    const contentCheckTextarea = Array.from(container.querySelectorAll<HTMLTextAreaElement>('textarea'))
      .find(textarea => textarea.placeholder === 'Access Denied|any_response|true')
    expect(contentCheckTextarea?.value).toBe('FatalError|body_only|true|<msg>|</msg>|true')

    typeInto(contentCheckTextarea!, 'FatalError|body_only|false|<msg>|</msg>|true')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.web_runtime', {
      content_checks: [
        {
          text: 'FatalError',
          scope: 'body_only',
          fail_if_found: false,
          left_boundary: '<msg>',
          right_boundary: '</msg>',
          case_insensitive: true,
        },
      ],
    })
  })

  it('renders Fail on SOAP Fault unchecked when fault_handling is omitted or false, and checked when true', () => {
    const baseSoap: ScriptDefinition = {
      name: 'SOAP fault test',
      version: '1.0.0',
      protocol: 'soap',
      config: {
        base_url: 'https://soap.example.test',
        soap: { version: '1.1' },
      },
      actions: [],
    }
    renderView(baseSoap, 'soap')
    const checkbox = () => Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(input => input.closest('label')?.textContent?.includes('Fail on SOAP Fault'))!
    expect(checkbox().checked).toBe(false)

    renderView({
      ...baseSoap,
      config: { ...baseSoap.config, soap: { version: '1.1', fault_handling: { fail_on_fault: false } } },
    }, 'soap')
    expect(checkbox().checked).toBe(false)

    renderView({
      ...baseSoap,
      config: { ...baseSoap.config, soap: { version: '1.1', fault_handling: { fail_on_fault: true } } },
    }, 'soap')
    expect(checkbox().checked).toBe(true)
  })

  it('shows engine default 4 for Max Idle Connections and valid SQLite / logical secret placeholders', () => {
    const httpScript: ScriptDefinition = {
      name: 'HTTP connection defaults',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [],
    }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="connection"
          activeSection="http"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={httpScript}
        />,
      )
    })
    const maxIdleInput = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="number"]'))
      .find(input => input.closest('label')?.textContent?.includes('Max idle connections'))
    expect(maxIdleInput?.value).toBe('4')

    const dbScript: ScriptDefinition = {
      name: 'Database placeholders',
      version: '1.0.0',
      protocol: 'database',
      config: { base_url: '', database: { driver: 'sqlite' } },
      actions: [],
    }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="script_config"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={dbScript}
        />,
      )
    })
    const secretPlaceholderInput = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
      .find(input => input.closest('label')?.textContent?.includes('DSN Secret'))
    const dsnInput = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
      .find(input => input.closest('label')?.textContent?.includes('DSN / Fixture'))
    expect(secretPlaceholderInput?.placeholder).toBe('DB_DSN')
    expect(dsnInput?.placeholder).toBe(':memory: or file:testdb?mode=memory&cache=shared')
  })
})

describe('ScriptRuntimeSettingsView runtime tabs', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('keeps the selected settings section stable while filters hide its tab', () => {
    const onSelectSection = vi.fn()
    const script: ScriptDefinition = {
      name: 'Filtered runtime script',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://example.test' },
      actions: [],
    }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="think"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={onSelectSection}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })

    expect(container.querySelector('[role="tablist"]')?.getAttribute('aria-orientation')).toBe('vertical')
    const search = container.querySelector<HTMLInputElement>('.runtime-settings-search input')
    expect(search).toBeTruthy()
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(search, 'logging')
      search?.dispatchEvent(new Event('input', { bubbles: true }))
    })

    expect(onSelectSection).not.toHaveBeenCalled()
    expect(container.querySelector('#runtime-settings-active-heading')?.textContent).toBe('Think Time')
    expect(container.querySelector('.runtime-settings-detail')?.getAttribute('aria-labelledby')).toBe('runtime-settings-active-heading')
  })
})

describe('ScriptRuntimeSettingsView proxy credential note', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  const renderScript = (script: ScriptDefinition, tab: 'proxy' | 'browser' = 'proxy') => {
    act(() => root.render(<ScriptRuntimeSettingsView activeHTTPTransportTab={tab} activeSection="http" canEditProtocol={false}
      onSave={vi.fn()} onSelectHTTPTransportTab={vi.fn()} onSelectSection={vi.fn()} onUpdateField={vi.fn()}
      onUpdateProtocol={vi.fn()} saveDisabled saving={false} script={script} />))
    return Array.from(container.querySelectorAll('[data-inactive-proxy-credential-note]')).map(note => note.textContent)
  }
  const proxied = (mode: string, actions: ScriptDefinition['actions'] = []): ScriptDefinition => ({
    name: 'Proxy', version: '1', protocol: 'http', actions,
    config: { base_url: '', http: { proxy_mode: mode, proxy: { url: 'http://proxy.example.test:8080', username: 'svc', password: '{{secret.ENTERPRISE_PROXY_PASSWORD}}' } } },
  })
  const render = (mode: string) => renderScript(proxied(mode))
  const browserNote = 'The browser proxy password uses the secret BP, but no browser proxy server is set, so it is not needed. It becomes required if you set a browser proxy server.'

  it('notes a proxy credential secret the hidden fields keep outside Custom, without requiring it', () => {
    expect(render('system')).toEqual(['The proxy password uses the secret ENTERPRISE_PROXY_PASSWORD, but Proxy mode is System, so it is not needed. It becomes required if you switch Proxy mode to Custom.'])
    expect(render('none')[0]).toContain('Proxy mode is No proxy,')
    expect(render('pac')[0]).toContain('Proxy mode is PAC,')
    // Custom shows the fields themselves, and the secret is required there.
    expect(render('custom')).toEqual([])
    expect(container.querySelector<HTMLInputElement>('input[type="password"]')?.value).toBe('{{secret.ENTERPRISE_PROXY_PASSWORD}}')
  })

  it('does not call a proxy secret not needed when a request also uses it', () => {
    const shared = proxied('system', [{ name: 'Call', type: 'GET', target: '/', headers: { Authorization: 'Bearer {{secret.ENTERPRISE_PROXY_PASSWORD}}' } }])
    expect(renderScript(shared)).toEqual([])
  })

  it('notes a browser proxy secret with no browser proxy server in the Browser sub-tab', () => {
    const browserScript = (server: string): ScriptDefinition => ({
      name: 'Browser', version: '1', protocol: 'browser', actions: [],
      config: { base_url: '', browser: { proxy: { server, password: '{{secret.BP}}' } } } as ScriptDefinition['config'],
    })
    expect(renderScript(browserScript(''), 'browser')).toEqual([browserNote])
    expect(container.querySelector('#http-transport-panel-browser [data-inactive-proxy-credential-note]')).not.toBeNull()
    // The Proxy sub-tab leaves it to the Browser sub-tab.
    expect(renderScript(browserScript(''), 'proxy')).toEqual([])
    // With a server the credential is in use: required, so no note.
    expect(renderScript(browserScript('http://proxy.example.test:3128'), 'browser')).toEqual([])
  })

  it('notes a browser proxy secret in the Proxy sub-tab for a script without a Browser sub-tab', () => {
    const script: ScriptDefinition = {
      name: 'HTTP', version: '1', protocol: 'http', actions: [],
      config: { base_url: '', browser: { proxy: { password: '{{secret.BP}}' } } } as ScriptDefinition['config'],
    }
    expect(renderScript(script, 'proxy')).toEqual([browserNote])
  })
})

describe('ScriptRuntimeSettingsView transaction and phase SLA rules', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  const renderErrorsSection = (script: ScriptDefinition, onUpdateField = vi.fn()) => {
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="errors"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })
    return onUpdateField
  }

  it('collects transaction names across init, actions, and teardown into the datalist and seeds new rules', () => {
    const script: ScriptDefinition = {
      name: 'Order flow',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://example.test' },
      init: [
        {
          name: 'Start Login transaction',
          type: 'source_transaction',
          target: '',
          params: { function: 'lr_start_transaction', args: ['Login'] },
        },
      ],
      actions: [
        {
          name: 'Search catalog',
          type: 'GET',
          target: '/search',
          metadata: { source_transaction: 'Browse_Catalog' },
        },
        {
          name: 'Submit order',
          type: 'POST',
          target: '/orders',
          metadata: { transaction: 'Place_Order' },
        },
      ],
      teardown: [
        {
          name: 'Logout request',
          type: 'POST',
          target: '/logout',
          metadata: { source_transaction: 'Logout' },
        },
      ],
    }

    const onUpdateField = renderErrorsSection(script)
    const group = container.querySelector<HTMLElement>('[data-testid="script-transaction-sla-rules"]')
    expect(group).not.toBeNull()
    expect(group?.textContent).toContain('Transaction & phase SLA rules')

    const options = Array.from(group!.querySelectorAll<HTMLOptionElement>('datalist option')).map(option => option.value)
    expect(options).toEqual(['Login', 'Browse_Catalog', 'Place_Order', 'Logout'])

    const addButton = Array.from(group!.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Add transaction SLA rule')
    expect(addButton).toBeTruthy()
    act(() => addButton!.click())

    expect(onUpdateField).toHaveBeenCalledWith('goal_config', {
      transaction_rules: [
        {
          transaction: 'Login',
          metric: 'p95_duration_ms',
          comparator: 'lte',
          threshold: 500,
          severity: 'fail',
        },
      ],
    })
  })

  it('edits rule fields, coerces non-duration metrics when a phase is chosen, and removes the last rule cleanly', () => {
    const onUpdateField = vi.fn()
    const baseScript: ScriptDefinition = {
      name: 'Checkout SLA script',
      version: '1.0.0',
      protocol: 'http',
      goal_config: {
        p95_latency_ms: 800,
        transaction_rules: [
          {
            transaction: 'Checkout',
            metric: 'error_rate_pct',
            comparator: 'lte',
            threshold: 2,
            severity: 'fail',
          },
        ],
      },
      config: { base_url: 'https://example.test' },
      actions: [],
    }

    renderErrorsSection(baseScript, onUpdateField)

    const txInput = container.querySelector<HTMLInputElement>('input[aria-label="Transaction SLA rule 1 transaction"]')!
    const phaseSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Transaction SLA rule 1 phase"]')!
    const metricSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Transaction SLA rule 1 metric"]')!
    const comparatorSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Transaction SLA rule 1 comparator"]')!
    const thresholdInput = container.querySelector<HTMLInputElement>('input[aria-label="Transaction SLA rule 1 threshold"]')!
    const severitySelect = container.querySelector<HTMLSelectElement>('select[aria-label="Transaction SLA rule 1 severity"]')!

    expect(txInput.getAttribute('list')).toBeTruthy()
    expect(metricSelect.value).toBe('error_rate_pct')

    // Selecting a network phase when metric is error_rate_pct auto-switches metric to p95_duration_ms.
    act(() => {
      phaseSelect.value = 'tls'
      phaseSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenLastCalledWith('goal_config', {
      p95_latency_ms: 800,
      transaction_rules: [
        {
          transaction: 'Checkout',
          phase: 'tls',
          metric: 'p95_duration_ms',
          comparator: 'lte',
          threshold: 2,
          severity: 'fail',
        },
      ],
    })

    // Re-render with phase='tls' to verify non-duration metrics are omitted from the Metric dropdown.
    renderErrorsSection({
      ...baseScript,
      goal_config: {
        p95_latency_ms: 800,
        transaction_rules: [
          {
            transaction: 'Checkout',
            phase: 'tls',
            metric: 'p95_duration_ms',
            comparator: 'lte',
            threshold: 150,
            severity: 'fail',
          },
        ],
      },
    }, onUpdateField)

    const phaseMetricValues = Array.from(
      container.querySelectorAll<HTMLOptionElement>('select[aria-label="Transaction SLA rule 1 metric"] option'),
    ).map(option => option.value)
    expect(phaseMetricValues).toEqual([
      'p95_duration_ms',
      'p99_duration_ms',
      'p90_duration_ms',
      'average_duration_ms',
      'min_duration_ms',
      'max_duration_ms',
    ])

    // Clearing phase back to "" omits phase from the rule.
    const updatedPhaseSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Transaction SLA rule 1 phase"]')!
    act(() => {
      updatedPhaseSelect.value = ''
      updatedPhaseSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const clearedPhaseCall = onUpdateField.mock.calls[onUpdateField.mock.calls.length - 1][1] as ScriptDefinition['goal_config']
    expect(clearedPhaseCall?.transaction_rules?.[0]).toEqual({
      transaction: 'Checkout',
      metric: 'p95_duration_ms',
      comparator: 'lte',
      threshold: 150,
      severity: 'fail',
    })
    expect('phase' in (clearedPhaseCall?.transaction_rules?.[0] || {})).toBe(false)

    // Edit comparator, threshold, and severity.
    act(() => {
      comparatorSelect.value = 'lt'
      comparatorSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenLastCalledWith('goal_config', expect.objectContaining({
      transaction_rules: [expect.objectContaining({ comparator: 'lt' })],
    }))

    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(thresholdInput, '275')
      thresholdInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenLastCalledWith('goal_config', expect.objectContaining({
      transaction_rules: [expect.objectContaining({ threshold: 275 })],
    }))

    act(() => {
      severitySelect.value = 'warning'
      severitySelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenLastCalledWith('goal_config', expect.objectContaining({
      transaction_rules: [expect.objectContaining({ severity: 'warning' })],
    }))

    // Removing the last rule sets transaction_rules: undefined while preserving p95_latency_ms.
    const removeButton = container.querySelector<HTMLButtonElement>('button[aria-label="Remove transaction SLA rule 1"]')!
    act(() => removeButton.click())
    expect(onUpdateField).toHaveBeenLastCalledWith('goal_config', {
      p95_latency_ms: 800,
      transaction_rules: undefined,
    })
  })

  it('surfaces validateGoalConfig feedback for invalid rules and disables adding past 128 rules', () => {
    const invalidScript: ScriptDefinition = {
      name: 'Invalid SLA script',
      version: '1.0.0',
      protocol: 'http',
      goal_config: {
        transaction_rules: [
          {
            transaction: ' Checkout ',
            metric: 'p95_duration_ms',
            comparator: 'lte',
            threshold: 500,
            severity: 'fail',
          },
        ],
      },
      config: { base_url: 'https://example.test' },
      actions: [],
    }

    renderErrorsSection(invalidScript)
    const group = container.querySelector<HTMLElement>('[data-testid="script-transaction-sla-rules"]')!
    const alert = group.querySelector('[role="alert"]')
    expect(alert?.textContent).toContain('Transaction SLA rule 1 requires an exact name of at most 256 bytes without surrounding whitespace')

    // Render with 128 rules to verify the Add button is disabled.
    const maxRulesScript: ScriptDefinition = {
      ...invalidScript,
      goal_config: {
        transaction_rules: Array.from({ length: 128 }, (_, index) => ({
          transaction: `Tx_${index + 1}`,
          metric: 'p95_duration_ms' as const,
          comparator: 'lte' as const,
          threshold: 500,
          severity: 'fail' as const,
        })),
      },
    }
    renderErrorsSection(maxRulesScript)
    const addButton = Array.from(group.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Add transaction SLA rule')
    expect(addButton?.disabled).toBe(true)
  })
})

describe('ScriptRuntimeSettingsView canonical HTTP & SOAP gaps', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('edits config.http.tls.max_version and warns when HTTP/3 is paired with TLS max_version < 1.3', () => {
    const onUpdateField = vi.fn()
    const renderTls = (script: ScriptDefinition) => {
      act(() => {
        root.render(
          <ScriptRuntimeSettingsView
            activeHTTPTransportTab="tls"
            activeSection="http"
            canEditProtocol={false}
            onSave={vi.fn()}
            onSelectHTTPTransportTab={vi.fn()}
            onSelectSection={vi.fn()}
            onUpdateField={onUpdateField}
            onUpdateProtocol={vi.fn()}
            saveDisabled={false}
            saving={false}
            script={script}
          />,
        )
      })
    }

    renderTls({
      name: 'TLS max version',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://example.test', http: { version: '2', tls: { min_version: '1.2' } } },
      actions: [],
    })

    const maxTlsSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Max TLS version"]')!
    expect(maxTlsSelect).not.toBeNull()
    expect(Array.from(maxTlsSelect.options).map(opt => opt.value)).toEqual(['', '1.0', '1.1', '1.2', '1.3'])
    expect(container.querySelector('[data-testid="http-tls-max-version-h3-warning"]')).toBeNull()

    act(() => {
      maxTlsSelect.value = '1.2'
      maxTlsSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.http', {
      version: '2',
      tls: { min_version: '1.2', max_version: '1.2' },
    })

    // Pair HTTP/3 with max_version 1.2 -> warning appears
    renderTls({
      name: 'TLS max version H3 conflict',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://example.test', http: { version: '3', tls: { max_version: '1.2' } } },
      actions: [],
    })
    const warning = container.querySelector('[data-testid="http-tls-max-version-h3-warning"]')
    expect(warning).not.toBeNull()
    expect(warning?.textContent).toContain('HTTP/3 (QUIC) requires TLS 1.3')
  })

  it('includes digest in config.http.auth.type options and reveals username/password inputs', () => {
    const onUpdateField = vi.fn()
    const renderAuth = (type: string) => {
      act(() => {
        root.render(
          <ScriptRuntimeSettingsView
            activeHTTPTransportTab="authentication"
            activeSection="http"
            canEditProtocol={false}
            onSave={vi.fn()}
            onSelectHTTPTransportTab={vi.fn()}
            onSelectSection={vi.fn()}
            onUpdateField={onUpdateField}
            onUpdateProtocol={vi.fn()}
            saveDisabled={false}
            saving={false}
            script={{
              name: 'Digest auth',
              version: '1.0.0',
              protocol: 'http',
              config: { base_url: 'https://example.test', http: { auth: { type } } },
              actions: [],
            }}
          />,
        )
      })
    }

    renderAuth('basic')
    const authTypeSelect = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(sel => sel.closest('label')?.textContent?.includes('Auth type'))!
    expect(Array.from(authTypeSelect.options).map(opt => opt.value)).toContain('digest')

    act(() => {
      authTypeSelect.value = 'digest'
      authTypeSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.http', { auth: { type: 'digest' } })

    renderAuth('digest')
    const usernameInput = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
      .find(inp => inp.closest('label')?.textContent?.includes('Username'))!
    expect(usernameInput).toBeTruthy()
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(usernameInput, 'digest-user')
      usernameInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenLastCalledWith('config.http', {
      auth: { type: 'digest', username: 'digest-user' },
    })
  })

  it('adds SOAP namespaces via inline prefix and URI inputs without window.prompt and badges generated body_mode', () => {
    const onUpdateField = vi.fn()
    const promptSpy = vi.spyOn(window, 'prompt')
    const script: ScriptDefinition = {
      name: 'SOAP inline namespaces',
      version: '1.0.0',
      protocol: 'soap',
      config: {
        base_url: 'https://soap.example.test',
        soap: { version: '1.1', namespaces: { soapenv: 'http://schemas.xmlsoap.org/soap/envelope/' } },
      },
      actions: [],
    }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="soap"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })

    expect(container.querySelector('[data-testid="soap-namespaces-body-mode-badge"]')?.textContent)
      .toContain('action.soap.body_mode is generated')

    const prefixInput = container.querySelector<HTMLInputElement>('input[aria-label="Namespace prefix"]')!
    const uriInput = container.querySelector<HTMLInputElement>('input[aria-label="Namespace URI"]')!
    const addButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(btn => btn.textContent === 'Add namespace')!

    expect(prefixInput).not.toBeNull()
    expect(uriInput).not.toBeNull()
    expect(addButton.disabled).toBe(true)

    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(prefixInput, 'ord')
      prefixInput.dispatchEvent(new Event('input', { bubbles: true }))
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(uriInput, 'https://example.test/orders')
      uriInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(addButton.disabled).toBe(false)

    act(() => addButton.click())
    expect(promptSpy).not.toHaveBeenCalled()
    expect(onUpdateField).toHaveBeenCalledWith('config.soap', {
      version: '1.1',
      namespaces: {
        soapenv: 'http://schemas.xmlsoap.org/soap/envelope/',
        ord: 'https://example.test/orders',
      },
    })
    promptSpy.mockRestore()
  })

  it('renders a diagnostic notice when http scripts contain soap config, and badges GET/HTML cache sections as inactive on soap scripts', () => {
    const httpWithSoap: ScriptDefinition = {
      name: 'HTTP with leftover SOAP',
      version: '1.0.0',
      protocol: 'http',
      config: {
        base_url: 'https://api.example.test',
        soap: { version: '1.2', namespaces: { ord: 'urn:Order' } },
      },
      actions: [],
    }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="script_config"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={httpWithSoap}
        />,
      )
    })
    const ignoredNotice = container.querySelector('[data-testid="http-ignored-soap-config-notice"]')
    expect(ignoredNotice).not.toBeNull()
    expect(ignoredNotice?.textContent).toContain('ignored when script.protocol is http')

    const soapScript: ScriptDefinition = {
      name: 'SOAP script',
      version: '1.0.0',
      protocol: 'soap',
      config: { base_url: 'https://soap.example.test', soap: { version: '1.1' } },
      actions: [],
    }
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="session_cache"
          activeSection="http"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={soapScript}
        />,
      )
    })
    expect(container.querySelector('[data-testid="soap-inactive-cache-resource-badge"]')?.textContent)
      .toContain('Inactive for POST XML SOAP requests')

    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="web_runtime"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={soapScript}
        />,
      )
    })
    expect(container.querySelector('[data-testid="soap-inactive-web-resource-badge"]')?.textContent)
      .toContain('Inactive for POST XML SOAP requests')

    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="browser_emulation"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={soapScript}
        />,
      )
    })
    expect(container.querySelector('[data-testid="soap-inactive-browser-emulation-cache-badge"]')?.textContent)
      .toContain('Inactive for POST XML SOAP requests')
  })

  it('supports 3-state continue_on_error, retry_on / honor_retry_after / retry_after_max_ms, fail_transaction_on_error_message, and log.max_response_bytes', () => {
    const onUpdateField = vi.fn()
    const renderErrors = (script: ScriptDefinition) => {
      act(() => {
        root.render(
          <ScriptRuntimeSettingsView
            activeHTTPTransportTab="timeouts"
            activeSection="errors"
            canEditProtocol={false}
            onSave={vi.fn()}
            onSelectHTTPTransportTab={vi.fn()}
            onSelectSection={vi.fn()}
            onUpdateField={onUpdateField}
            onUpdateProtocol={vi.fn()}
            saveDisabled={false}
            saving={false}
            script={script}
          />,
        )
      })
    }

    renderErrors({
      name: 'Errors 3-state',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://example.test', error_handling: { max_retries: 2 } },
      actions: [],
    })

    const continueSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Continue on error"]')!
    expect(continueSelect.value).toBe('')
    act(() => {
      continueSelect.value = 'true'
      continueSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.error_handling', {
      max_retries: 2,
      continue_on_error: true,
    })

    act(() => {
      continueSelect.value = 'false'
      continueSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.error_handling', {
      max_retries: 2,
      continue_on_error: false,
    })

    act(() => {
      continueSelect.value = ''
      continueSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.error_handling', {
      max_retries: 2,
      continue_on_error: undefined,
    })

    const failTxCheckbox = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(inp => inp.closest('label')?.textContent?.includes('Fail transaction on error message'))!
    act(() => failTxCheckbox.click())
    expect(onUpdateField).toHaveBeenCalledWith('config.error_handling', {
      max_retries: 2,
      fail_transaction_on_error_message: true,
    })

    const retryOnInput = container.querySelector<HTMLInputElement>('input[aria-label="Retry on conditions"]')!
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(retryOnInput, '429, 5xx')
      retryOnInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.error_handling', {
      max_retries: 2,
      retry_on: ['429', '5xx'],
    })

    const honorRetryAfter = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(inp => inp.closest('label')?.textContent?.includes('Honor Retry-After header'))!
    act(() => honorRetryAfter.click())
    expect(onUpdateField).toHaveBeenCalledWith('config.error_handling', {
      max_retries: 2,
      honor_retry_after: true,
    })

    const retryAfterMaxInput = container.querySelector<HTMLInputElement>('input[aria-label="Max Retry-After delay (ms)"]')!
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(retryAfterMaxInput, '15000')
      retryAfterMaxInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.error_handling', {
      max_retries: 2,
      retry_after_max_ms: 15000,
    })

    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="log"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={{
            name: 'Log bytes',
            version: '1.0.0',
            protocol: 'http',
            config: { base_url: 'https://example.test' },
            actions: [],
          }}
        />,
      )
    })
    const maxRespBytesInput = container.querySelector<HTMLInputElement>('input[aria-label="Max response bytes"]')!
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(maxRespBytesInput, '8192')
      maxRespBytesInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.log', {
      max_response_bytes: 8192,
    })
  })

  it('renders a read-only compatibility notice in Target & identity when jmeter or sampler_schedules are present', () => {
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="script_config"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={vi.fn()}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={{
            name: 'JMeter schedule script',
            version: '1.0.0',
            protocol: 'http',
            config: {
              base_url: 'https://example.test',
              jmeter: { profile: 'default', source_plan_id: 'plan-1', source_group_id: 'tg-1' },
            },
            actions: [],
          }}
        />,
      )
    })
    const notice = container.querySelector('[data-testid="readonly-schedule-compatibility-notice"]')
    expect(notice).not.toBeNull()
    expect(notice?.textContent).toContain('Read-only compatibility notice:')
  })

  it('authors quota pacing, arrival process, rate stages, run_logic.blocks, and exponential think-time bounds', () => {
    const onUpdateField = vi.fn()
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="pacing"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={{
            name: 'Quota & TPS script',
            version: '1.0.0',
            protocol: 'http',
            config: {
              base_url: 'https://example.test',
              pacing: { type: 'quota' },
            },
            actions: [],
          }}
        />,
      )
    })

    const tpmInput = container.querySelector<HTMLInputElement>('input[aria-label="Quota TPM"]')!
    expect(tpmInput).not.toBeNull()
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(tpmInput, '120000')
      tpmInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.pacing', {
      type: 'quota',
      quota: { tpm: 120000 },
    })

    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="pacing"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={{
            name: 'TPS arrival process script',
            version: '1.0.0',
            protocol: 'http',
            config: {
              base_url: 'https://example.test',
              run_logic: { workload_model: 'open' },
              pacing: { type: 'tps', tps: 25 },
            },
            actions: [],
          }}
        />,
      )
    })

    const arrivalSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Arrival process"]')!
    act(() => {
      arrivalSelect.value = 'poisson'
      arrivalSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.pacing', {
      type: 'tps',
      tps: 25,
      arrival_process: 'poisson',
    })

    const addStageBtn = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(btn => btn.textContent === 'Add rate stage')!
    act(() => addStageBtn.click())
    expect(onUpdateField).toHaveBeenCalledWith('config.pacing', {
      type: 'tps',
      tps: 25,
      rate_stages: [{ duration_seconds: 30, target_tps: 25 }],
    })

    const seedInput = container.querySelector<HTMLInputElement>('input[aria-label="Arrival seed"]')!
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(seedInput, '42')
      seedInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.pacing', {
      type: 'tps',
      tps: 25,
      seed: 42,
    })

    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="runlogic"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={{
            name: 'Run logic blocks script',
            version: '1.0.0',
            protocol: 'http',
            config: {
              base_url: 'https://example.test',
              run_logic: {
                iterations: 2,
                blocks: [
                  {
                    name: 'Weighted Block',
                    mode: 'percentage',
                    iterations: 1,
                    children: [
                      { action: 'Browse', weight: 70 },
                      { action: 'Buy', weight: 30 },
                    ],
                  },
                ],
              },
            },
            actions: [],
          }}
        />,
      )
    })

    const termSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Termination mode"]')!
    const termOptions = Array.from(termSelect.options).map(o => o.value)
    expect(termOptions).toEqual(['', 'duration', 'iterations'])

    // block_scheduling does not drive HTTP/SOAP execution, so it is not an editable control.
    expect(container.querySelector('select[aria-label="Block scheduling"]')).toBeNull()

    const blockActionsInput = container.querySelector<HTMLInputElement>('input[aria-label="Run block 1 actions"]')!
    expect(blockActionsInput.value).toBe('Browse:70, Buy:30')
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(blockActionsInput, 'Browse:60, Buy:40')
      blockActionsInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.run_logic', {
      iterations: 2,
      blocks: [
        {
          name: 'Weighted Block',
          mode: 'percentage',
          iterations: 1,
          children: [
            { action: 'Browse', weight: 60 },
            { action: 'Buy', weight: 40 },
          ],
        },
      ],
    })

    const addBlockBtn = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(btn => btn.textContent === 'Add run block')!
    act(() => addBlockBtn.click())
    expect(onUpdateField).toHaveBeenCalledWith('config.run_logic', {
      iterations: 2,
      blocks: [
        {
          name: 'Weighted Block',
          mode: 'percentage',
          iterations: 1,
          children: [
            { action: 'Browse', weight: 70 },
            { action: 'Buy', weight: 30 },
          ],
        },
        {
          name: 'Block 2',
          mode: 'sequential',
          iterations: 1,
          children: [{ action: 'Action' }],
        },
      ],
    })

    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab="timeouts"
          activeSection="think"
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={{
            name: 'Exponential think script',
            version: '1.0.0',
            protocol: 'http',
            config: {
              base_url: 'https://example.test',
              think_time: {
                type: 'exponential',
                distribution: 'exponential',
                mean_ms: 2000,
                min_ms: 500,
                max_ms: 5000,
                multiplier: 1,
              } as ScriptDefinition['config']['think_time'],
            },
            actions: [],
          }}
        />,
      )
    })
    const minLabel = Array.from(container.querySelectorAll('label')).find(l => l.textContent?.includes('Min (s)'))
    const maxLabel = Array.from(container.querySelectorAll('label')).find(l => l.textContent?.includes('Cap base (s)'))
    expect(minLabel).toBeTruthy()
    expect(maxLabel).toBeTruthy()

    const distSelect = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(s => s.closest('label')?.textContent?.includes('Distribution'))!
    act(() => {
      distSelect.value = 'fixed'
      distSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const lastThinkCall = onUpdateField.mock.calls.filter(c => c[0] === 'config.think_time').pop()
    expect(lastThinkCall?.[1]).toMatchObject({ type: 'fixed', mean_ms: 2000 })
    expect((lastThinkCall?.[1] as { distribution?: string }).distribution).toBeUndefined()

    // Script-level force is ignored at runtime, so no control authors it.
    expect(container.querySelector('input[aria-label="Force exact think time"]')).toBeNull()
  })
})
