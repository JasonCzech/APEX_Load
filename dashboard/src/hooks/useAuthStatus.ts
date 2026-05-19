import { useEffect, useState } from 'react'

import { DashboardAuthStatus } from '../types'
import { useAuthApi } from './api/useAuthApi'
import { useDevView } from './DevViewProvider'

export type AuthStatusState =
  | { status: 'loading' }
  | { status: 'ready'; auth: DashboardAuthStatus }
  | { status: 'error'; message: string }

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
        const message = error instanceof Error ? error.message : 'Unable to determine authentication status.'
        setState({ status: 'error', message })
      })

    return () => {
      active = false
    }
  }, [getAuthStatus, mode])

  return state
}
