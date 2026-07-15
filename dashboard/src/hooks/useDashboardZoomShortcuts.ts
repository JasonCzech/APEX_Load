import { useEffect, useState } from 'react'

import { getWailsEventRuntime } from '../desktop/wailsRuntime'
import { readStoredString, writeStoredString } from '../utils/browserStorage'

export const DASHBOARD_ZOOM_STORAGE_KEY = 'apexload.dashboardZoom'

const DEFAULT_DASHBOARD_ZOOM = 1
const DASHBOARD_ZOOM_STEP = 0.1
const MIN_DASHBOARD_ZOOM = 0.5
const MAX_DASHBOARD_ZOOM = 1.8

type DashboardZoomAction = 'in' | 'out' | 'reset'

function normalizeDashboardZoom(value: number) {
  if (!Number.isFinite(value)) {
    return DEFAULT_DASHBOARD_ZOOM
  }

  const clamped = Math.min(MAX_DASHBOARD_ZOOM, Math.max(MIN_DASHBOARD_ZOOM, value))
  return Math.round(clamped * 100) / 100
}

function readStoredDashboardZoom() {
  if (typeof window === 'undefined') {
    return DEFAULT_DASHBOARD_ZOOM
  }

  const stored = readStoredString(DASHBOARD_ZOOM_STORAGE_KEY)
  if (!stored) {
    return DEFAULT_DASHBOARD_ZOOM
  }

  return normalizeDashboardZoom(Number(stored))
}

function applyDashboardZoom(zoom: number) {
  if (typeof document === 'undefined') {
    return
  }

  const normalized = normalizeDashboardZoom(zoom)
  document.documentElement.style.setProperty('--dashboard-zoom', String(normalized))
}

function shortcutActionForEvent(event: KeyboardEvent): DashboardZoomAction | null {
  if (event.defaultPrevented || !(event.metaKey || event.ctrlKey) || event.altKey) {
    return null
  }

  switch (event.key) {
    case '+':
    case '=':
      return 'in'
    case '-':
    case '_':
      return 'out'
    case '0':
      return 'reset'
    default:
      return null
  }
}

function nextDashboardZoom(current: number, action: DashboardZoomAction) {
  if (action === 'reset') {
    return DEFAULT_DASHBOARD_ZOOM
  }

  const delta = action === 'in' ? DASHBOARD_ZOOM_STEP : -DASHBOARD_ZOOM_STEP
  return normalizeDashboardZoom(current + delta)
}

export function useDashboardZoomShortcuts() {
  const [zoom, setZoom] = useState(() => readStoredDashboardZoom())

  useEffect(() => {
    applyDashboardZoom(zoom)
    if (typeof window !== 'undefined') {
      writeStoredString(DASHBOARD_ZOOM_STORAGE_KEY, String(zoom))
    }
  }, [zoom])

  useEffect(() => {
    if (typeof window === 'undefined') {
      return undefined
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      const action = shortcutActionForEvent(event)
      if (!action) {
        return
      }

      event.preventDefault()
      setZoom(current => nextDashboardZoom(current, action))
    }

    window.addEventListener('keydown', handleKeyDown, { capture: true })
    return () => {
      window.removeEventListener('keydown', handleKeyDown, { capture: true })
    }
  }, [])

  // In the Wails desktop shell the View > Zoom menu items emit a `menu:zoom`
  // event (the OS webview has no stable zoom API, so zoom is the in-app
  // --dashboard-zoom variable). Bridge those native menu actions to the same
  // zoom state. No-op in the browser, where `window.runtime` is undefined.
  useEffect(() => {
    const runtime = getWailsEventRuntime()
    if (!runtime?.EventsOn) {
      return undefined
    }
    const cancel = runtime.EventsOn('menu:zoom', (action?: unknown) => {
      if (action === 'in' || action === 'out' || action === 'reset') {
        setZoom(current => nextDashboardZoom(current, action))
      }
    })
    return () => {
      if (typeof cancel === 'function') {
        cancel()
      }
    }
  }, [])

  return zoom
}
