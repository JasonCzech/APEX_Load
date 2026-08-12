import { useEffect, useState } from 'react'

import { DashboardAuthStatus } from '../types'
import { diagnoseError, type ErrorDiagnostic } from '../utils/errorUtils'
import { useAuthApi } from './api/useAuthApi'
import { useDevView } from './useDevView'

const AUTH_SESSION_RECHECK_INTERVAL_MS = 60_000
const AUTH_SESSION_EXPIRY_SLOP_MS = 250

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

      try {
        const auth = await getAuthStatus()
        if (!active) {
          return
        }
        setState({ status: 'ready', auth })
        scheduleRecheck(auth)
      } catch (error: unknown) {
        if (!active) {
          return
        }
        const diagnostic = diagnoseError(error, 'Unable to determine authentication status.')
        setState({ status: 'error', message: diagnostic.summary, diagnostic })
        scheduleErrorRetry()
      }
    }

    void loadAuthStatus()

    return () => {
      active = false
      if (recheckTimer !== undefined) {
        window.clearTimeout(recheckTimer)
      }
    }
  }, [getAuthStatus, mode])

  return state
}
