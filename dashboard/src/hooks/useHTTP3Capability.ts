import { useContext, useEffect, useState } from 'react'
import type { AuthoringHTTPRuntime } from '../types/scriptImports'
import { ApiContext } from './apiContext'

export type HTTP3Capability = AuthoringHTTPRuntime['http3'] | null

// One request per page load: the answer changes only when an operator restarts
// the controller with a different APEXLOAD_HTTP3.
let cached: Promise<HTTP3Capability> | null = null

/** Resets the cache; tests only. */
export function resetHTTP3CapabilityCache() {
  cached = null
}

/**
 * Whether this controller can execute the HTTP/3 policies, or null while
 * unknown (loading, an older controller, no API in an isolated render).
 */
export function useHTTP3Capability(enabled = true): HTTP3Capability {
  // The context, not useApi: editors render in isolated tests that mock useApi
  // without a provider, and the note then falls back to its static text.
  const api = useContext(ApiContext)
  const [capability, setCapability] = useState<HTTP3Capability>(null)
  useEffect(() => {
    if (!enabled || !api) return
    let active = true
    if (!cached) {
      cached = api.listAuthoringCapabilities()
        .then(response => response.http_runtime?.http3 ?? null)
        .catch(() => { cached = null; return null })
    }
    void cached.then(value => { if (active) setCapability(value) })
    return () => { active = false }
  }, [api, enabled])
  return capability
}
