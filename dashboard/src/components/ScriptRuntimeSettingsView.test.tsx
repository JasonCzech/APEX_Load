// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { ScriptRuntimeSettingsView } from './ScriptRuntimeSettingsView'

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

    expect(container.textContent).toContain('Target transactions per second')
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
