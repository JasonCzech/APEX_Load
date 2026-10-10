// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiContext } from '../hooks/apiContext'
import type { ApiServices } from '../hooks/ApiProvider'
import { resetHTTP3CapabilityCache } from '../hooks/useHTTP3Capability'
import { HTTP3ExecutionNote } from './HTTP3ExecutionNote'
import { HTTP3_EXECUTION_NOTE } from './httpVersionModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('HTTP3ExecutionNote', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => { resetHTTP3CapabilityCache(); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
  afterEach(() => { act(() => root.unmount()); container.remove() })
  const render = async (policy: string, http3?: { exact: boolean; upgrade: boolean }) => {
    const listAuthoringCapabilities = vi.fn().mockResolvedValue(http3 ? { http_runtime: { versions: [], http3: { ...http3, scope: 'controller' } } } : {})
    await act(async () => {
      root.render(<ApiContext.Provider value={{ listAuthoringCapabilities } as unknown as ApiServices}><HTTP3ExecutionNote as="p" policy={policy} /></ApiContext.Provider>)
    })
    return listAuthoringCapabilities
  }

  it('says nothing for TCP policies and does not ask the controller', async () => {
    const fetch = await render('2', { exact: true, upgrade: true })
    expect(container.textContent).toBe('')
    expect(fetch).not.toHaveBeenCalled()
  })

  it('asks the controller once per page load', async () => {
    const first = await render('3', { exact: true, upgrade: true })
    const second = await render('auto+h3', { exact: false, upgrade: false })
    expect(first).toHaveBeenCalledTimes(1)
    expect(second).not.toHaveBeenCalled()
    // The cached answer, not the second response, decides the text.
    expect(container.textContent).toContain('enabled on this controller')
  })

  it('reports the controller capability without blocking saves', async () => {
    await render('3', { exact: true, upgrade: true })
    expect(container.textContent).toContain('HTTP/3 execution is enabled on this controller')
    resetHTTP3CapabilityCache()
    await render('3', { exact: false, upgrade: false })
    expect(container.textContent).toContain('not enabled on this installation')
    expect(container.textContent).toContain('The script still saves.')
    resetHTTP3CapabilityCache()
    await render('auto+h3', { exact: true, upgrade: false })
    expect(container.textContent).toContain('not auto+h3 yet')
  })

  it('falls back to the static note when the controller does not say, or without an API', async () => {
    await render('auto+h3')
    expect(container.textContent).toBe(HTTP3_EXECUTION_NOTE)
    await act(async () => { root.render(<HTTP3ExecutionNote as="p" policy="3" />) })
    expect(container.textContent).toBe(HTTP3_EXECUTION_NOTE)
  })
})
