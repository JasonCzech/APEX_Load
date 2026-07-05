// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest'

import { bridgeRequest, type BrowserRecorderBridgeConnection } from './browserRecorderBridge'

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
  })
})
