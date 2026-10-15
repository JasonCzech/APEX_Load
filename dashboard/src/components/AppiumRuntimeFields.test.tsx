// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { AppiumConfig } from '../types/scripts'
import AppiumRuntimeFields from './AppiumRuntimeFields'
import { appiumDeviceSummary } from './appiumRuntimeModel'
import { requiresScriptBaseURL } from './scriptAuthoringAssist'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function setValue(element: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const prototype = element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype
  Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(element, value)
  element.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('AppiumRuntimeFields', () => {
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

  it('edits the server and commits capabilities only when they parse to an object', async () => {
    let config: AppiumConfig = { server_url: 'http://127.0.0.1:4723', capabilities: { platformName: 'iOS' } }
    const onChange = vi.fn((next: AppiumConfig) => { config = next })
    const render = () => root.render(<AppiumRuntimeFields config={config} onChange={onChange} />)
    await act(async () => render())

    const server = container.querySelector('input') as HTMLInputElement
    await act(async () => setValue(server, 'https://{{secret.CLOUD_USER}}:{{secret.CLOUD_KEY}}@hub.example.com/wd/hub'))
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ server_url: 'https://{{secret.CLOUD_USER}}:{{secret.CLOUD_KEY}}@hub.example.com/wd/hub' }))
    await act(async () => render())

    const capabilities = container.querySelector('textarea') as HTMLTextAreaElement
    const calls = onChange.mock.calls.length
    await act(async () => setValue(capabilities, '{"platformName": "iOS",'))
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('Capabilities must be valid JSON.')
    await act(async () => setValue(capabilities, '["iOS"]'))
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('Capabilities must be a JSON object.')
    expect(onChange).toHaveBeenCalledTimes(calls)

    await act(async () => setValue(capabilities, '{"platformName": "iOS", "appium:deviceName": "iPad Air"}'))
    expect(container.querySelector('[role="alert"]')).toBeNull()
    expect(config.capabilities).toEqual({ platformName: 'iOS', 'appium:deviceName': 'iPad Air' })
    expect(config.server_url).toContain('hub.example.com')
  })

  it('takes capabilities changed elsewhere, replacing a draft that does not parse', async () => {
    const onChange = vi.fn()
    await act(async () => root.render(<AppiumRuntimeFields config={{ capabilities: { platformName: 'iOS' } }} onChange={onChange} />))
    const capabilities = container.querySelector('textarea') as HTMLTextAreaElement
    await act(async () => setValue(capabilities, '{"platformName":'))
    expect(container.querySelector('[role="alert"]')).not.toBeNull()
    await act(async () => root.render(<AppiumRuntimeFields config={{ capabilities: { platformName: 'Android' } }} onChange={onChange} />))
    expect(JSON.parse((container.querySelector('textarea') as HTMLTextAreaElement).value)).toEqual({ platformName: 'Android' })
    expect(container.querySelector('[role="alert"]')).toBeNull()
  })

  it('does not need a base URL', () => {
    expect(requiresScriptBaseURL('appium')).toBe(false)
  })

  it('names the device and the server', () => {
    expect(appiumDeviceSummary({
      server_url: 'https://user:key@hub.example.com/wd/hub',
      capabilities: { platformName: 'iOS', 'appium:deviceName': 'iPad Air' },
    })).toBe('iOS · iPad Air · hub.example.com')
    expect(appiumDeviceSummary(undefined)).toBe('server pending')
  })
})
