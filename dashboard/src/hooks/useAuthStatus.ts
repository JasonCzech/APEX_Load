import { useEffect, useState } from 'react'

import { DashboardAuthStatus } from '../types'
import { diagnoseError, type ErrorDiagnostic } from '../utils/errorUtils'
import { useAuthApi } from './api/useAuthApi'
import { useDevView } from './useDevView'

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

    getAuthStatus()
      .then((auth) => {
        if (!active) {
          return
        }
        setState({ status: 'ready', auth })
      })
      .catch((error: unknown) => {
        if (!active) {
          return
        }
        const diagnostic = diagnoseError(error, 'Unable to determine authentication status.')
        setState({ status: 'error', message: diagnostic.summary, diagnostic })
      })

    return () => {
      active = false
    }
  }, [getAuthStatus, mode])

  return state
}
