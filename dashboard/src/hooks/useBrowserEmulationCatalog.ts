import { useEffect, useState } from 'react'

import { FALLBACK_BROWSER_EMULATION_CATALOG } from '../components/scriptEditorRuntimeSettingsModel'
import type { BrowserEmulationCatalog } from '../types/scripts'

import { useSystemApi } from './api/useSystemApi'

/**
 * Serves the browser-emulation catalog to every editor that renders the
 * Browser Emulation dropdowns — the script author page and both scenario
 * override surfaces.
 *
 * The catalog is the same data the engine uses to resolve a selection to a
 * concrete User-Agent, so any editor rendering from a hardcoded copy drifts the
 * moment the controller's list changes: a browser added server-side would be
 * unofferable, and one removed server-side would still be offered and then
 * silently resolved to the nearest catalog version at replay. Fetching it in a
 * shared hook keeps all three surfaces in lockstep.
 *
 * The bundled fallback is the initial value, so the dropdowns are populated on
 * first paint and stay usable if the request fails or the caller lacks access.
 *
 * @param enabled skip the request when the surface cannot show the section.
 */
export function useBrowserEmulationCatalog(enabled = true): BrowserEmulationCatalog {
  const [catalog, setCatalog] = useState<BrowserEmulationCatalog>(FALLBACK_BROWSER_EMULATION_CATALOG)
  const { getBrowserEmulationCatalog } = useSystemApi()

  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    try {
      getBrowserEmulationCatalog()
        .then((next) => {
          if (!cancelled && next?.browsers?.length) setCatalog(next)
        })
        .catch(() => { /* keep the bundled fallback */ })
    } catch { /* keep the bundled fallback */ }
    return () => { cancelled = true }
  }, [enabled, getBrowserEmulationCatalog])

  return catalog
}
