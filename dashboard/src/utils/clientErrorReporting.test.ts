// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  type ClientErrorReport,
  installClientErrorReporting,
  reportClientError,
  withoutQuery,
} from './clientErrorReporting'

const flush = () => new Promise(resolve => setTimeout(resolve, 0))

// A stand-in for window: dispatching error events on the real jsdom window
// also reaches Vitest's own uncaught-error handler.
function fakeWindow(): Window {
  const target = new EventTarget() as EventTarget & { location: { href: string } }
  target.location = { href: 'https://apexload.test/tests/t1?desktop_token=secret' }
  return target as unknown as Window
}

// 'undelivered' stands for a send that never got an HTTP response.
function install(status: number | 'undelivered' = 204, clock = { now: 1_000_000 }) {
  const sent: ClientErrorReport[] = []
  const send = vi.fn(async (report: ClientErrorReport) => {
    sent.push(report)
    return status === 'undelivered' ? undefined : status
  })
  const target = fakeWindow()
  const uninstall = installClientErrorReporting(send, { appVersion: 'test-build', now: () => clock.now, target })
  return { sent, send, uninstall, clock, target }
}

let uninstallCurrent: (() => void) | undefined

afterEach(() => {
  uninstallCurrent?.()
  uninstallCurrent = undefined
})

describe('client error reporting', () => {
  it('is inert until installed', () => {
    const send = vi.fn()
    reportClientError('error', new Error('before install'))
    expect(send).not.toHaveBeenCalled()
  })

  it('forwards uncaught errors and unhandled rejections from the window', async () => {
    const { sent, uninstall, target } = install()
    uninstallCurrent = uninstall
    const thrown = new TypeError("Cannot read properties of undefined (reading 'actions')")
    target.dispatchEvent(new ErrorEvent('error', { error: thrown, message: thrown.message, filename: 'https://apexload.test/assets/index.js?v=1', lineno: 120, colno: 7 }))
    const rejection = new Event('unhandledrejection') as PromiseRejectionEvent
    Object.defineProperty(rejection, 'reason', { value: new Error('save failed') })
    target.dispatchEvent(rejection)
    await flush()

    expect(sent.map(report => report.kind)).toEqual(['error', 'unhandledrejection'])
    expect(sent[0]).toMatchObject({ message: thrown.message, name: 'TypeError', source: 'https://apexload.test/assets/index.js:120:7', page: 'https://apexload.test/tests/t1', app_version: 'test-build' })
    expect(sent[0].stack).toContain('TypeError')
    expect(sent[1].message).toBe('save failed')
  })

  it('stops listening after uninstall', async () => {
    const { sent, uninstall, target } = install()
    uninstall()
    target.dispatchEvent(new ErrorEvent('error', { error: new Error('after uninstall') }))
    reportClientError('render', new Error('after uninstall'))
    await flush()
    expect(sent).toEqual([])
  })

  it('suppresses duplicates for a minute and caps reports per minute', async () => {
    const { sent, uninstall, clock } = install()
    uninstallCurrent = uninstall
    for (let i = 0; i < 3; i++) reportClientError('network', new TypeError('Failed to fetch'), { request_method: 'get', request_path: '/api/v1/tests' })
    expect(sent).toHaveLength(1)
    expect(sent[0]).toMatchObject({ request_method: 'GET', request_path: '/api/v1/tests' })

    for (let i = 0; i < 20; i++) reportClientError('error', new Error(`distinct ${i}`))
    expect(sent).toHaveLength(10)

    clock.now += 61_000
    reportClientError('network', new TypeError('Failed to fetch'), { request_method: 'get', request_path: '/api/v1/tests' })
    expect(sent).toHaveLength(11)
  })

  it('backs off after the controller refuses a report', async () => {
    const { sent, uninstall, clock } = install(429)
    uninstallCurrent = uninstall
    reportClientError('error', new Error('first'))
    await flush()
    reportClientError('error', new Error('second'))
    expect(sent).toHaveLength(1)
    clock.now += 61_000
    reportClientError('error', new Error('third'))
    expect(sent).toHaveLength(2)
  })

  it('ignores browser noise and cancelled work', () => {
    const { sent, uninstall } = install()
    uninstallCurrent = uninstall
    reportClientError('error', 'ResizeObserver loop completed with undelivered notifications.')
    reportClientError('error', 'Script error.')
    reportClientError('unhandledrejection', new DOMException('The user aborted a request.', 'AbortError'))
    expect(sent).toEqual([])
  })

  it('joins an unhandled API failure to the controller request', () => {
    const { sent, uninstall } = install()
    uninstallCurrent = uninstall
    const apiFailure = Object.assign(new Error('script name already exists'), { name: 'ApiError', status: 409, requestId: 'req-409' })
    reportClientError('unhandledrejection', apiFailure)
    expect(sent[0]).toMatchObject({ kind: 'unhandledrejection', request_id: 'req-409', request_status: 409 })
  })

  it('returns undelivered reports to the budget and pauses', async () => {
    const { sent, uninstall, clock } = install('undelivered')
    uninstallCurrent = uninstall
    // A long offline stretch: more undelivered failures than the page budget.
    for (let i = 0; i < 60; i++) {
      reportClientError('network', new TypeError('Failed to fetch'), { request_path: '/api/v1/tests' })
      await flush()
      reportClientError('network', new TypeError('Failed to fetch'), { request_path: '/api/v1/tests' })
      expect(sent).toHaveLength(i + 1) // paused after each undelivered attempt
      clock.now += 61_000
    }
  })

  it('bounds fields and never throws from a hostile error value', () => {
    const { sent, uninstall } = install()
    uninstallCurrent = uninstall
    const huge = new Error('x'.repeat(5_000))
    huge.stack = 'y'.repeat(20_000)
    reportClientError('render', huge, { component_stack: 'z'.repeat(10_000) })
    expect(sent[0].message).toHaveLength(1_000)
    expect(sent[0].stack).toHaveLength(8_000)
    expect(sent[0].component_stack).toHaveLength(4_000)

    const circular: Record<string, unknown> = {}
    circular.self = circular
    expect(() => reportClientError('unhandledrejection', circular)).not.toThrow()
    expect(sent).toHaveLength(2)
  })

  it('keeps tokens in query strings off the wire', () => {
    expect(withoutQuery('https://apexload.test/scripts/s1?desktop_token=secret#/editor?tab=json')).toBe('https://apexload.test/scripts/s1#/editor')
    expect(withoutQuery('/api/v1/tests?state=abc')).toBe('/api/v1/tests')
    expect(withoutQuery('https://apexload.test/#section')).toBe('https://apexload.test/')
  })
})
