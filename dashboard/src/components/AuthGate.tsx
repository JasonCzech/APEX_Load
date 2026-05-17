import { ReactNode } from 'react'

import { AuthStatusState } from '../hooks/useAuthStatus'
import './AuthGate.css'

export default function AuthGate({ children, state }: { children: ReactNode; state: AuthStatusState }) {
  const providerLabel = state.status === 'ready' && state.auth.provider_name?.trim()
    ? state.auth.provider_name.trim()
    : state.status === 'ready' && state.auth.mode === 'oidc'
      ? 'identity provider'
      : 'access gateway'

  const blockedMessage = state.status === 'ready' && state.auth.mode === 'oidc'
    ? state.auth.session_status === 'expired'
      ? `Your Stormrunner session has expired. Sign in with ${providerLabel} again to continue.`
      : state.auth.session_status === 'invalid'
        ? `Stormrunner could not verify the local session cookie. Sign in with ${providerLabel} again to continue.`
        : `This dashboard is configured for native OIDC authentication. Complete sign-in with ${providerLabel} so Stormrunner can establish your session.`
    : `This dashboard is configured for trusted-header authentication. Complete sign-in through the configured upstream ${providerLabel} so Stormrunner receives your identity headers.`

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
          <p>{blockedMessage}</p>
          {state.auth.login_url && (
            <p>
              <a className="auth-gate-action" href={state.auth.login_url}>Continue to {providerLabel}</a>
            </p>
          )}
          <p className="auth-gate-note">Current auth mode: {state.auth.mode.replace('_', ' ')}</p>
        </section>
      </div>
    )
  }

  return <>{children}</>
}
