// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  DESKTOP_NOTIFICATIONS_PREF_KEY,
  buildTestNotification,
  useDesktopNotifications,
} from './useDesktopNotifications'
import type { WailsDesktopBridge } from '../desktop/desktopBridge'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

type BridgeWindow = typeof window & { go?: { main?: { App?: WailsDesktopBridge } } }

let notify: ReturnType<typeof useDesktopNotifications>['notifyTestStatus']

function Harness() {
  const { notifyTestStatus } = useDesktopNotifications()
  notify = notifyTestStatus
  return null
}

describe('buildTestNotification', () => {
  it('maps notifiable statuses and ignores others', () => {
    expect(buildTestNotification('test_complete', 'order-flow')).toEqual({ title: 'Test complete', body: 'order-flow finished.' })
    expect(buildTestNotification('test_failed', 'checkout')?.title).toBe('Test failed')
    expect(buildTestNotification('test_aborted')?.body).toBe('A test was aborted.')
    expect(buildTestNotification('script_shakeout_failed', 'login')?.title).toBe('Shakeout failed')
    expect(buildTestNotification('test_started', 'x')).toBeNull()
    expect(buildTestNotification('metrics_update')).toBeNull()
  })
})

describe('useDesktopNotifications', () => {
  let container: HTMLDivElement
  let root: Root
  let notifySpy: ReturnType<typeof vi.fn>

  beforeEach(async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    window.localStorage.clear()
    notifySpy = vi.fn().mockResolvedValue(undefined)
    ;(window as BridgeWindow).go = { main: { App: { Notify: notifySpy } } }
    // Default: window is not focused so notifications fire.
    document.hasFocus = vi.fn().mockReturnValue(false)
    await act(async () => { root.render(<Harness />) })
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    delete (window as BridgeWindow).go
    vi.restoreAllMocks()
  })

  it('fires the native bridge for notifiable statuses and dedups', () => {
    act(() => { notify('test_complete', 'order-flow', 't1') })
    expect(notifySpy).toHaveBeenCalledTimes(1)
    expect(notifySpy).toHaveBeenCalledWith('Test complete', 'order-flow finished.')

    // Same test+status again → deduped.
    act(() => { notify('test_complete', 'order-flow', 't1') })
    expect(notifySpy).toHaveBeenCalledTimes(1)

    // Different status for same test → fires.
    act(() => { notify('test_failed', 'order-flow', 't1') })
    expect(notifySpy).toHaveBeenCalledTimes(2)

    // Same status for a DIFFERENT test id → fires (dedup key includes test id).
    act(() => { notify('test_complete', 'checkout', 't2') })
    expect(notifySpy).toHaveBeenCalledTimes(3)
  })

  it('ignores non-notifiable statuses', () => {
    act(() => { notify('test_started', 'order-flow', 't1') })
    expect(notifySpy).not.toHaveBeenCalled()
  })

  it('stays quiet while the window is focused', () => {
    document.hasFocus = vi.fn().mockReturnValue(true)
    act(() => { notify('test_complete', 'order-flow', 't1') })
    expect(notifySpy).not.toHaveBeenCalled()
  })

  it('respects the disabled preference', () => {
    window.localStorage.setItem(DESKTOP_NOTIFICATIONS_PREF_KEY, 'false')
    act(() => { notify('test_complete', 'order-flow', 't1') })
    expect(notifySpy).not.toHaveBeenCalled()
  })
})
