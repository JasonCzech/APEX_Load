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

  it('renders governed Java VM controls without a Base URL and preserves exact argument lines', () => {
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

    expect(container.textContent).toContain('Java VM Settings')
    expect(container.textContent).toContain('ordinary classpath')
    expect(container.textContent).toContain('one JVM, Actions instance, and child loader for its full lifecycle')
    expect(container.textContent).toContain('saved LoadRunner parameters persist')
    expect(container.textContent).not.toContain('Base URL')
    expect(container.textContent).not.toContain('Runner Command')

    const jdkHome = container.querySelector<HTMLInputElement>('input[aria-label="JDK Home"]')
    expect(jdkHome?.disabled).toBe(true)

    const specified = container.querySelectorAll<HTMLInputElement>('input[name="java-vuser-jdk-selection"]')[1]
    act(() => specified.click())
    expect(onUpdateField).toHaveBeenCalledWith('config.java_vuser.jdk_selection', 'specified')

    const vmParams = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Additional VM Parameters"]')
    expect(vmParams).not.toBeNull()
    act(() => vmParams!.dispatchEvent(new FocusEvent('focusin', { bubbles: true })))
    act(() => {
      const setValue = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      setValue?.call(vmParams, '-Xmx1g\n-Dlabel=hello world\n')
      vmParams!.dispatchEvent(new Event('input', { bubbles: true }))
    })
    act(() => vmParams!.dispatchEvent(new FocusEvent('focusout', { bubbles: true })))
    expect(onUpdateField).toHaveBeenCalledWith(
      'config.java_vuser.additional_vm_parameters',
      ['-Xmx1g', '-Dlabel=hello world'],
    )
  })

  it('commits JDK home, class loading, xboot, and timeout writes to config.java_vuser paths', () => {
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
    expect(onUpdateField).toHaveBeenCalledWith('config.java_vuser.jdk_home', '/opt/jdks/temurin-17')

    const checkboxes = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
    const xboot = checkboxes.find(input => input.closest('label')?.textContent?.includes('-Xbootclasspath'))
    expect(xboot?.checked).toBe(true)
    act(() => xboot!.click())
    expect(onUpdateField).toHaveBeenCalledWith('config.java_vuser.use_xbootclasspath', false)

    const dedicated = checkboxes.find(input => input.closest('label')?.textContent?.includes('dedicated class loader'))
    expect(dedicated?.checked).toBe(false)
    act(() => dedicated!.click())
    expect(onUpdateField).toHaveBeenCalledWith('config.java_vuser.dedicated_class_loader', true)

    const startup = container.querySelector<HTMLInputElement>('input[aria-label="Java Vuser startup timeout"]')
    act(() => setInputValue(startup!, '5'))
    expect(onUpdateField).toHaveBeenCalledWith('config.java_vuser.startup_timeout_ms', 5000)

    const action = container.querySelector<HTMLInputElement>('input[aria-label="Java Vuser action timeout"]')
    act(() => setInputValue(action!, '45'))
    expect(onUpdateField).toHaveBeenCalledWith('config.java_vuser.action_timeout_ms', 45000)
  })

  it('does not offer the Java VM section for non-Java protocols', () => {
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

    expect(container.textContent).not.toContain('Java VM Settings')
    expect(container.querySelector('input[aria-label="JDK Home"]')).toBeNull()
    expect(container.textContent).toContain('Base URL')
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
