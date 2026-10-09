import { useEffect, useState } from 'react'

import { DashboardAuthStatus } from '../types'
import { diagnoseError, type ErrorDiagnostic } from '../utils/errorUtils'
import { useAuthApi } from './api/useAuthApi'
import { useDevView } from './useDevView'

const AUTH_SESSION_RECHECK_INTERVAL_MS = 60_000
const AUTH_SESSION_EXPIRY_SLOP_MS = 250

const AUTH_STATUS_REFRESH_EVENT = 'apexload:auth-status-refresh'

/** Re-check authentication in place (no page reload), e.g. after signing in again. */
export function requestAuthStatusRefresh() {
  if (typeof window !== 'undefined') window.dispatchEvent(new Event(AUTH_STATUS_REFRESH_EVENT))
}

export type AuthStatusState =
  | { status: 'loading' }
  | { status: 'ready'; auth: DashboardAuthStatus }
  | { status: 'error'; message: string; diagnostic: ErrorDiagnostic }

export function useAuthStatus(): AuthStatusState {
  const { getAuthStatus } = useAuthApi()
  const { mode } = useDevView()
  const [state, setState] = useState<AuthStatusState>({ status: 'loading' })

  useEffect(() => {
    let active = true
    let recheckTimer: number | undefined
    // Checks can overlap (a scheduled recheck and a refresh after signing in
    // again); only the latest may apply, or a stale "signed out" answer wins.
    let latestLoad = 0

    const scheduleRecheck = (auth: DashboardAuthStatus) => {
      if (
        !auth.enabled
        || !auth.authenticated
        || (auth.mode !== 'local' && auth.mode !== 'oidc')
      ) {
        return
      }

      let delay = AUTH_SESSION_RECHECK_INTERVAL_MS
      const expiresAt = Date.parse(auth.session_expires_at || '')
      if (Number.isFinite(expiresAt)) {
        delay = Math.min(
          delay,
          Math.max(0, expiresAt - Date.now()) + AUTH_SESSION_EXPIRY_SLOP_MS,
        )
      } else if (
        typeof auth.session_ttl_seconds === 'number'
        && Number.isFinite(auth.session_ttl_seconds)
      ) {
        delay = Math.min(
          delay,
          Math.max(0, auth.session_ttl_seconds * 1000) + AUTH_SESSION_EXPIRY_SLOP_MS,
        )
      }

      recheckTimer = window.setTimeout(() => {
        void loadAuthStatus()
      }, delay)
    }

    const scheduleErrorRetry = () => {
      recheckTimer = window.setTimeout(() => {
        void loadAuthStatus()
      }, AUTH_SESSION_RECHECK_INTERVAL_MS)
    }

    async function loadAuthStatus() {
      if (recheckTimer !== undefined) {
        window.clearTimeout(recheckTimer)
        recheckTimer = undefined
      }

      const load = ++latestLoad
      try {
        const auth = await getAuthStatus()
        if (!active || load !== latestLoad) {
          return
        }
        setState({ status: 'ready', auth })
        scheduleRecheck(auth)
      } catch (error: unknown) {
        if (!active || load !== latestLoad) {
          return
        }
        const diagnostic = diagnoseError(error, 'Unable to determine authentication status.')
        setState({ status: 'error', message: diagnostic.summary, diagnostic })
        scheduleErrorRetry()
      }
    }

    void loadAuthStatus()
    const refresh = () => { void loadAuthStatus() }
    window.addEventListener(AUTH_STATUS_REFRESH_EVENT, refresh)

    return () => {
      active = false
      window.removeEventListener(AUTH_STATUS_REFRESH_EVENT, refresh)
      if (recheckTimer !== undefined) {
        window.clearTimeout(recheckTimer)
      }
    }
  }, [getAuthStatus, mode])

  return state
}
