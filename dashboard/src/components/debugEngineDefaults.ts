import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { ScriptConfig } from '../types/scripts'

// The engine's own defaults for the debugger's read-only Run configuration and
// its session override editor: the values a run applies when the script leaves
// a field unset, served by GET /api/v1/dsl/config-defaults (dsl.DefaultScriptConfig).

/**
 * Shown only until (or if) the controller answers: the same values as
 * pkg/protocol/httpcontract/limits.go, so an unreachable endpoint never shows
 * a wrong default.
 */
export const ENGINE_DEFAULTS_FALLBACK: ScriptConfig = {
  base_url: '',
  think_time: { type: 'none' },
  pacing: { type: 'none' },
  http: {
    max_conns_per_host: 100,
    keep_alive: true,
    connect_timeout_ms: 30000,
    request_timeout_ms: 60000,
    tls_timeout_ms: 10000,
    idle_timeout_ms: 90000,
    tls: { min_version: '1.2' },
    max_redirects: 10,
    cookies_enabled: true,
  },
}

let cached: ScriptConfig | null = null
let pending: Promise<ScriptConfig> | null = null

export function loadEngineConfigDefaults(): Promise<ScriptConfig> {
  if (cached) return Promise.resolve(cached)
  if (!pending) {
    pending = request<{ config: ScriptConfig }>('/dsl/config-defaults')
      .then(response => {
        if (!response?.config || typeof response.config !== 'object') throw new Error('config defaults unavailable')
        cached = response.config
        return cached
      })
      .finally(() => { pending = null })
  }
  return pending
}

/** Test hook: forget the cached defaults. */
export function resetEngineConfigDefaultsCache() {
  cached = null
  pending = null
}

/** The engine defaults, or the fallback until they load (and when they cannot). */
export function useEngineConfigDefaults(): ScriptConfig {
  const [defaults, setDefaults] = useState<ScriptConfig>(() => cached ?? ENGINE_DEFAULTS_FALLBACK)
  useEffect(() => {
    if (cached) return // already loaded
    let active = true
    loadEngineConfigDefaults().then(config => { if (active) setDefaults(config) }).catch(() => {})
    return () => { active = false }
  }, [])
  return defaults
}
