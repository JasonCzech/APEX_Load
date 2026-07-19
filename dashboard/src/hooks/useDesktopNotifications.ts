import { useCallback, useRef } from 'react'

import { getDesktopBridge } from '../desktop/desktopBridge'
import { readStoredString, writeStoredString } from '../utils/browserStorage'

export const DESKTOP_NOTIFICATIONS_PREF_KEY = 'apexload.desktopNotifications'

// Upper bound on the dedup set so a long session observing many test ids does
// not leak memory.
const MAX_SEEN_NOTIFICATIONS = 500

// Long-running test outcomes worth a native notification. Live metric updates
// and intermediate lifecycle events (started/stopping) are intentionally
// excluded to avoid noise.
export const NOTIFIABLE_TEST_STATUSES = new Set<string>([
  'test_complete',
  'test_failed',
  'test_aborted',
  'script_shakeout_failed',
])

export type DesktopNotification = { title: string; body: string }

// buildTestNotification maps a test status to a notification, or null when the
// status is not notification-worthy. Pure + exported for testing.
export function buildTestNotification(status: string, testName?: string): DesktopNotification | null {
  const name = (testName || '').trim() || 'A test'
  switch (status) {
    case 'test_complete':
      return { title: 'Test complete', body: `${name} finished.` }
    case 'test_failed':
      return { title: 'Test failed', body: `${name} failed.` }
    case 'test_aborted':
      return { title: 'Test aborted', body: `${name} was aborted.` }
    case 'script_shakeout_failed':
      return { title: 'Shakeout failed', body: `${name} failed pre-flight shakeout.` }
    default:
      return null
  }
}

function notificationsEnabled(): boolean {
  if (typeof window === 'undefined') return false
  const stored = readStoredString(DESKTOP_NOTIFICATIONS_PREF_KEY)
  // Default on in the desktop shell.
  return stored === '' ? true : stored === 'true'
}

// isDesktopNotificationsEnabled / setDesktopNotificationsEnabled back the
// Settings toggle (preference persisted in localStorage; default on).
export function isDesktopNotificationsEnabled(): boolean {
  return notificationsEnabled()
}

export function setDesktopNotificationsEnabled(enabled: boolean): void {
  if (typeof window === 'undefined') return
  writeStoredString(DESKTOP_NOTIFICATIONS_PREF_KEY, enabled ? 'true' : 'false')
}

function windowIsFocused(): boolean {
  return typeof document !== 'undefined' && typeof document.hasFocus === 'function' && document.hasFocus()
}

function sendWebNotification(notification: DesktopNotification): boolean {
  if (typeof window === 'undefined' || typeof window.Notification !== 'function') return false
  // Only use the web fallback when permission was already granted; never prompt.
  if (window.Notification.permission !== 'granted') return false
  try {
    new window.Notification(notification.title, { body: notification.body })
    return true
  } catch {
    return false
  }
}

// useDesktopNotifications returns a stable notifyTestStatus(status, name, id)
// that fires a native (or, in the browser, web) notification for notification-
// worthy test outcomes. It dedups by test+status, respects the user preference,
// and stays quiet while the window is focused.
export function useDesktopNotifications() {
  const seen = useRef<Set<string>>(new Set())
  const inFlight = useRef<Set<string>>(new Set())
  const nativeAvailability = useRef<Promise<boolean> | null>(null)

  const notifyTestStatus = useCallback((status?: string, testName?: string, testId?: string) => {
    if (!status) return
    const notification = buildTestNotification(status, testName)
    if (!notification) return
    if (!notificationsEnabled()) return
    if (windowIsFocused()) return

    const key = `${testId || ''}:${status}`
    if (seen.current.has(key) || inFlight.current.has(key)) return
    inFlight.current.add(key)

    const bridge = getDesktopBridge()
    void (async () => {
      let delivered = false
      if (typeof bridge?.Notify === 'function') {
        let available = true
        if (typeof bridge.NotificationsAvailable === 'function') {
          nativeAvailability.current ??= Promise.resolve().then(() => bridge.NotificationsAvailable?.() ?? false).catch(() => false)
          available = await nativeAvailability.current
        }
        if (available) {
          try {
            await bridge.Notify(notification.title, notification.body)
            delivered = true
          } catch {
            delivered = false
          }
        }
      }
      if (!delivered) delivered = sendWebNotification(notification)

      if (delivered) {
        // Bound the dedup set over a long-running session: evict the oldest key
        // (Set preserves insertion order) once we exceed the cap.
        if (seen.current.size >= MAX_SEEN_NOTIFICATIONS) {
          const oldest = seen.current.values().next().value
          if (oldest !== undefined) seen.current.delete(oldest)
        }
        seen.current.add(key)
      }
      inFlight.current.delete(key)
    })().catch(() => {
      // An unexpected bridge/browser failure remains retryable on the next
      // status replay and never becomes an unhandled promise rejection.
      inFlight.current.delete(key)
    })
  }, [])

  return { notifyTestStatus }
}
