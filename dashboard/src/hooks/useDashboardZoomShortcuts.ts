import { useEffect, useState } from 'react'

import { DESKTOP_QUEUED_MENU_ZOOM_EVENT } from '../desktop/rendererEvents'
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

/**
 * Zoom from a command (the command palette). It goes through the same single
 * subscription as the desktop menu, so each request applies exactly once; the
 * Cmd/Ctrl key presses are handled by the capture listener below.
 */
export function requestDashboardZoom(action: DashboardZoomAction) {
  window.dispatchEvent(new CustomEvent(DESKTOP_QUEUED_MENU_ZOOM_EVENT, { detail: action }))
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

  // The desktop shell dispatcher converts both live native zoom clicks and
  // renderer-ready replays into one DOM event. Keeping a single subscription
  // prevents a live Wails menu click from applying the zoom transition twice.
  useEffect(() => {
    if (typeof window === 'undefined') {
      return undefined
    }
    const handleQueuedZoom = (event: Event) => {
      const action = (event as CustomEvent<unknown>).detail
      if (action === 'in' || action === 'out' || action === 'reset') {
        setZoom(current => nextDashboardZoom(current, action))
      }
    }
    window.addEventListener(DESKTOP_QUEUED_MENU_ZOOM_EVENT, handleQueuedZoom)
    return () => {
      window.removeEventListener(DESKTOP_QUEUED_MENU_ZOOM_EVENT, handleQueuedZoom)
    }
  }, [])

  return zoom
}
