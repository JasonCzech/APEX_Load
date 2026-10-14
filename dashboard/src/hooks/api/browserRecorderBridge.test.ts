// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest'

import { bridgeRequest, bridgeFetch, clearBrowserRecorderBridgeSession, pairBrowserRecorderBridge, readBrowserRecorderBridge, verifyBrowserRecorderBridgeAgent, writeBrowserRecorderBridge, type BrowserRecorderBridgeConnection } from './browserRecorderBridge'

describe('browser recorder bridge requests', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('sends both the bridge token and session id headers', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{"ok":true}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    const connection: BrowserRecorderBridgeConnection = {
      origin: 'http://127.0.0.1:49152',
      token: 'tok-1',
      session_id: 'session-1',
      paired_at: new Date().toISOString(),
    }

    await bridgeRequest<{ ok: boolean }>(connection, '/browser-recorder/devices', {
      headers: { 'X-Custom': 'keep-me' },
    })

    expect(fetchMock.mock.calls[0][0]).toBe('http://127.0.0.1:49152/api/v1/browser-recorder/devices')
    const headers = fetchMock.mock.calls[0][1]?.headers as Headers
    expect(headers.get('X-APEXLoad-Bridge-Token')).toBe('tok-1')
    expect(headers.get('X-APEXLoad-Bridge-Session')).toBe('session-1')
    expect(headers.get('X-Custom')).toBe('keep-me')
    expect(fetchMock.mock.calls[0][1]?.credentials).toBe('omit')
  })

  it('omits controller cookies for health, pairing and artifact downloads', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify({ bridge: { session_id: 'paired', availability: { available: true } } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await verifyBrowserRecorderBridgeAgent('http://127.0.0.1:49152')
    const connection = await pairBrowserRecorderBridge('http://127.0.0.1:49152', 'test-token')
    await bridgeFetch(connection, '/browser-recorder/sessions/test/export', { credentials: 'include' })
    expect(fetchMock).toHaveBeenCalledTimes(3)
    for (const call of fetchMock.mock.calls) expect(call[1]?.credentials).toBe('omit')
  })
})

describe('clearBrowserRecorderBridgeSession', () => {
  it('clears the stored connection only while it is the named session', () => {
    const connection: BrowserRecorderBridgeConnection = {
      origin: 'http://127.0.0.1:27777',
      token: 'tok',
      session_id: 'session-new',
      paired_at: new Date().toISOString(),
    }
    writeBrowserRecorderBridge(connection)
    // A refused older session must not clear the pairing that replaced it.
    expect(clearBrowserRecorderBridgeSession('session-old')).toBe(false)
    expect(readBrowserRecorderBridge()?.session_id).toBe('session-new')
    expect(clearBrowserRecorderBridgeSession('session-new')).toBe(true)
    expect(readBrowserRecorderBridge()).toBeNull()
    // Nothing stored (cleared without an event here): the caller drops its copy.
    expect(clearBrowserRecorderBridgeSession('session-new')).toBe(true)
  })
})
