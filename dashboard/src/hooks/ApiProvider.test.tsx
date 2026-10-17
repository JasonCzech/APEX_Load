// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { API_ORIGIN, request, requestFromOrigin, setApiOrigin } from './apiClient'
import { writeBrowserRecorderBridge, clearBrowserRecorderBridge } from './api/browserRecorderBridge'
import { useExecutionTestPolling } from '../components/useExecutionTestPolling'

vi.mock('./apiClient', async importOriginal => {
  const actual = await importOriginal<typeof import('./apiClient')>()
  return {
    ...actual,
    get API_ORIGIN() { return actual.API_ORIGIN },
    request: vi.fn().mockResolvedValue({}),
    requestFromOrigin: vi.fn().mockResolvedValue({}),
  }
})

import { ApiProvider } from './ApiProvider'
import { useApi, useOptionalApi, useRecordingApi } from './useApi'

it('shares one context identity across required and optional consumers and scopes nested providers', async () => {
  const node = document.createElement('div')
  const root = createRoot(node)
  const observed = new Map<string, { required: ReturnType<typeof useApi>; optional: ReturnType<typeof useOptionalApi> }>()
  function Consumer({ name }: { name: string }) {
    observed.set(name, { required: useApi(), optional: useOptionalApi() })
    return null
  }
  const tree = (nested: boolean) => <ApiProvider>
    <Consumer name="outer" />
    {nested ? <ApiProvider><Consumer name="inner" /></ApiProvider> : <Consumer name="inner" />}
    <Consumer name="sibling" />
  </ApiProvider>
  try {
    await act(async () => root.render(tree(true)))
    const outer = observed.get('outer')!.required
    const inner = observed.get('inner')!.required
    expect(outer).not.toBe(inner)
    expect(observed.get('sibling')!.required).toBe(outer)
    for (const value of observed.values()) expect(value.optional).toBe(value.required)
    expect(typeof inner.listTests).toBe('function')

    await act(async () => root.render(tree(false)))
    expect(observed.get('outer')!.required).toBe(outer)
    expect(observed.get('inner')!.required).toBe(outer)
    expect(observed.get('inner')!.optional).toBe(outer)
    expect(observed.get('sibling')!.required).toBe(outer)
  } finally {
    await act(async () => root.unmount())
  }
})

it('keeps controller transports and pending execution polling stable while recorder endpoints change', async () => {
  const node = document.createElement('div')
  const root = createRoot(node)
  const initialOrigin = API_ORIGIN
  let finish!: (value: unknown) => void
  vi.mocked(request).mockImplementation(() => new Promise(resolve => { finish = resolve }))
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  let transport!: ReturnType<typeof useApi>
  let recording!: ReturnType<typeof useRecordingApi>
  let transportRenders = 0
  function ControllerConsumer() {
    transport = useApi()
    transportRenders += 1
    const { test } = useExecutionTestPolling('pending-test')
    return <span>{test?.status || 'loading'}</span>
  }
  function RecorderConsumer() {
    recording = useRecordingApi()
    return null
  }
  try {
    await act(async () => root.render(<ApiProvider><ControllerConsumer /><RecorderConsumer /></ApiProvider>))
    const originalTransport = transport
    const originalRenderCount = transportRenders
    const controllerRecorder = recording
    const connection = { origin: 'http://127.0.0.1:49152', token: 'test-token', session_id: 'paired-1', paired_at: '2026-10-17T00:00:00Z' }
    await act(async () => writeBrowserRecorderBridge(connection))
    expect(transport).toBe(originalTransport)
    expect(transportRenders).toBe(originalRenderCount)
    expect(request).toHaveBeenCalledTimes(1)
    expect(recording.browserRecorderRequestScope).toBe('bridge:http://127.0.0.1:49152:paired-1')
    expect(recording.getBrowserRecording).not.toBe(controllerRecorder.getBrowserRecording)
    const pairedRecorder = recording

    await act(async () => { finish({ id: 'pending-test', status: 'RUNNING' }) })
    expect(node.textContent).toBe('RUNNING')
    await act(async () => writeBrowserRecorderBridge({ ...connection, session_id: 'paired-2' }))
    await recording.getBrowserRecording('session')
    const headers = vi.mocked(requestFromOrigin).mock.calls.slice(-1)[0]?.[2]?.headers
    expect(new Headers(headers).get('X-APEXLoad-Bridge-Session')).toBe('paired-2')
    // A callback captured by an earlier asynchronous workflow keeps its endpoint.
    await pairedRecorder.getBrowserRecording('session')
    expect(new Headers(vi.mocked(requestFromOrigin).mock.calls.slice(-1)[0]?.[2]?.headers).get('X-APEXLoad-Bridge-Session')).toBe('paired-1')

    await act(async () => setApiOrigin('http://other-controller.test'))
    expect(recording.browserRecorderBridge).toBeNull()
    expect(recording.browserRecorderRequestScope).toBe('api:http://other-controller.test')
    expect(transport).toBe(originalTransport)
    expect(request).toHaveBeenCalledTimes(1)
    await act(async () => setApiOrigin(initialOrigin))
    expect(recording.browserRecorderBridge?.session_id).toBe('paired-2')
  } finally {
    await act(async () => root.unmount())
    setApiOrigin(initialOrigin)
    clearBrowserRecorderBridge()
    vi.clearAllMocks()
  }
})
