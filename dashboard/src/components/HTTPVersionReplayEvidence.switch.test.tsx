// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ProtocolEvidence } from '../types/replay'
import { HTTPVersionReplayEvidence } from './HTTPVersionReplayEvidence'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('HTTP version evidence: switch to auto+h3', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
  afterEach(() => { act(() => root.unmount()); container.remove() })
  const advertised: ProtocolEvidence = { http_version_policy: '2', http_version_source: 'wire', http_version_negotiated: '2', alt_svc_h3_advertised: true }
  const buttons = () => Array.from(container.querySelectorAll('button')).map(button => button.textContent)

  it('offers the request and script scopes, applies the choice and confirms', () => {
    const onSwitch = vi.fn()
    act(() => root.render(<HTTPVersionReplayEvidence evidence={advertised} upgrade={{ scopes: ['request', 'script'], onSwitch }} />))
    const offer = container.querySelector('[role="group"][aria-label="Switch to auto+h3"]')!
    expect(offer.textContent).toContain('The origin advertises HTTP/3; this request used HTTP/2. Switch to auto+h3?')
    expect(buttons()).toEqual(['Switch this request to auto+h3', 'Switch the script default to auto+h3'])
    act(() => (container.querySelectorAll('button')[1] as HTMLButtonElement).click())
    expect(onSwitch).toHaveBeenCalledWith('script')
    expect(container.querySelector('[role="group"]')).toBeNull()
    expect(container.querySelector('[role="status"]')?.textContent).toBe('Switched the script default to auto+h3. The script is being validated again; replay it to exercise HTTP/3.')
  })

  it('offers nothing without an editable draft, an advertisement, or for HTTP/3 policies', () => {
    act(() => root.render(<HTTPVersionReplayEvidence evidence={advertised} />))
    expect(buttons()).toEqual([])
    expect(container.textContent).toContain('so HTTP/3 was not exercised')
    act(() => root.render(<HTTPVersionReplayEvidence evidence={{ ...advertised, alt_svc_h3_advertised: false }} upgrade={{ scopes: ['request'], onSwitch: vi.fn() }} />))
    expect(buttons()).toEqual([])
    act(() => root.render(<HTTPVersionReplayEvidence evidence={{ ...advertised, http_version_policy: 'auto+h3', http_version_selection_reason: 'not_advertised' }} upgrade={{ scopes: ['request'], onSwitch: vi.fn() }} />))
    expect(buttons()).toEqual([])
    act(() => root.render(<HTTPVersionReplayEvidence evidence={advertised} upgrade={{ scopes: [], onSwitch: vi.fn() }} />))
    expect(buttons()).toEqual([])
  })
})
