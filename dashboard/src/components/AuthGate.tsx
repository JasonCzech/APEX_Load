import { ReactNode, useEffect, useState } from 'react'

import { DashboardAuthStatus } from '../types'
import { useAuthApi } from '../hooks/api/useAuthApi'
import './AuthGate.css'

type AuthGateState =
  | { status: 'loading' }
  | { status: 'ready'; auth: DashboardAuthStatus }
  | { status: 'error'; message: string }

export default function AuthGate({ children }: { children: ReactNode }) {
  const { getAuthStatus } = useAuthApi()
  const [state, setState] = useState<AuthGateState>({ status: 'loading' })

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
  }, [getAuthStatus])

  if (state.status === 'loading') {
    return (
      <div className="auth-gate-shell">
        <section className="auth-gate-card auth-gate-card--loading">
          <p className="auth-gate-kicker">Stormrunner Access</p>
          <h1>Checking access</h1>
          <p>Confirming your upstream authentication context before loading the dashboard.</p>
        </section>
      </div>
    )
  }

  if (state.status === 'error') {
    return (
      <div className="auth-gate-shell">
        <section className="auth-gate-card auth-gate-card--error">
          <p className="auth-gate-kicker">Stormrunner Access</p>
          <h1>Authentication check failed</h1>
          <p>{state.message}</p>
          <p>Verify the controller is reachable and that the dashboard is pointing at the correct API origin.</p>
        </section>
      </div>
    )
  }

  if (state.auth.enabled && !state.auth.authenticated) {
    return (
      <div className="auth-gate-shell">
        <section className="auth-gate-card auth-gate-card--blocked">
          <p className="auth-gate-kicker">Stormrunner Access</p>
          <h1>Sign-in required</h1>
          <p>
            This dashboard is configured for trusted-header authentication. Open it through the configured access
            proxy so Stormrunner receives your identity headers.
          </p>
          <p className="auth-gate-note">Current auth mode: {state.auth.mode.replace('_', ' ')}</p>
        </section>
      </div>
    )
  }

  return <>{children}</>
}