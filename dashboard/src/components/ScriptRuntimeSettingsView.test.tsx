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
    expect(container.textContent).toContain('one JVM, Actions instance, and child loader for its full lifecycle')
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
    const select = Array.from(container.querySelectorAll<HTMLSelectElement>('select')).find(item => item.parentElement?.textContent?.includes('HTTP Version'))!
    expect(select.value).toBe(selection)
    expect(select.getAttribute('aria-invalid')).toBe('false')
    expect(container.textContent).toContain(`Effective script policy: ${expected}`)
    if (protocol === 'http/2') expect(select.options[0].textContent).toBe('Protocol default (HTTP/2 required (no fallback))')
    expect(onUpdateField).not.toHaveBeenCalled()
    expect(script.config.http?.version).toBe(version)
    expect(Array.from(select.options).filter(option => !option.disabled).map(option => option.value)).toEqual(['', '1.1', '2'])
  })

  it.each([null, 'auto', '1.0', 2, {}, []].map(value => ({ value })))('retains malformed global $value visibly until an intentional edit', ({ value }) => {
    const onUpdateField = vi.fn()
    const script = { name: 'Malformed version', version: '1', protocol: 'http/2', config: { base_url: '', http: { version: value, max_conns_per_host: 7, vendor_extension: { keep: true } } }, actions: [] } as unknown as ScriptDefinition
    act(() => root.render(<ScriptRuntimeSettingsView activeHTTPTransportTab="connection" activeSection="http" canEditProtocol={false}
      onSave={vi.fn()} onSelectHTTPTransportTab={vi.fn()} onSelectSection={vi.fn()} onUpdateField={onUpdateField}
      onUpdateProtocol={vi.fn()} saveDisabled saving={false} script={script} />))
    const select = Array.from(container.querySelectorAll<HTMLSelectElement>('select')).find(item => item.parentElement?.textContent?.includes('HTTP Version'))!
    expect(select.value).toBe('invalid')
    expect(select.getAttribute('aria-invalid')).toBe('true')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('original value is preserved')
    expect(onUpdateField).not.toHaveBeenCalled()
    expect(script.config.http?.version).toBe(value)
    act(() => { select.value = ''; select.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(onUpdateField).toHaveBeenCalledWith('config.http', { ...script.config.http, version: '' })
    expect(script.config.http?.version).toBe(value)
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
      .find(select => select.parentElement?.textContent?.includes('HTTP Version'))
    expect(version?.value).toBe('2')
    expect(Array.from(version?.options || []).map(option => option.textContent)).toEqual([
      'Auto-negotiate (HTTP/2 or HTTP/1.1)',
      'Require HTTP/1.1',
      'Require HTTP/2 (no fallback)',
    ])
    expect(version?.querySelector('option[value="1.0"]')).toBeNull()
    expect(container.textContent).toContain('required HTTP/2 never falls back to HTTP/1.1')
    act(() => {
      if (!version) return
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
      setter?.call(version, '')
      version.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(onUpdateField).toHaveBeenCalledWith('config.http', { version: '' })
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
      .find(input => input.closest('label')?.textContent?.includes('Consumer Group'))
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

  it('edits a Java HTTP request mapping without dropping the rest of the java_http block', () => {
    const script: ScriptDefinition = {
      name: 'Java over HTTP',
      version: '1.0.0',
      protocol: 'java_http',
      config: {
        base_url: 'https://java.example.test',
        java_http: {
          codec: 'java_serialized_object_base64',
          helper_mode: 'embedded',
          request_mapping: { userId: 'payload.user' },
        },
      },
      actions: [],
    }
    const onUpdateField = renderView(script, 'java_http')

    expect(sectionHeader('Java HTTP Settings').querySelector('[data-runtime-section-icon="java_http"]')).not.toBeNull()
    const mappingInputs = Array.from(container.querySelectorAll<HTMLInputElement>('.pair-row input'))
    const valueInput = mappingInputs.find(input => input.value === 'payload.user')
    expect(valueInput).toBeTruthy()
    typeInto(valueInput!, 'payload.account.user')

    expect(onUpdateField).toHaveBeenCalledWith('config.java_http', {
      codec: 'java_serialized_object_base64',
      helper_mode: 'embedded',
      request_mapping: { userId: 'payload.account.user' },
    })
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

    const sourceIPs = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Source IP pool"]')
    expect(sourceIPs?.value).toBe('10.0.0.5')
    typeInto(sourceIPs!, '10.0.0.5\n10.0.0.6')

    expect(onUpdateField).toHaveBeenCalledWith('config.web_runtime', {
      source_ips: ['10.0.0.5', '10.0.0.6'],
    })
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
