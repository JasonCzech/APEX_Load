import { ReactNode, useState } from 'react'

import { AuthStatusState } from '../hooks/useAuthStatus'
import ErrorDiagnostic from './ErrorDiagnostic'
import Logo from './Logo'
import './AuthGate.css'

export default function AuthGate({ children, state }: { children: ReactNode; state: AuthStatusState }) {
  const [logoutPending, setLogoutPending] = useState(false)
  const providerLabel = state.status === 'ready' && state.auth.provider_name?.trim()
    ? state.auth.provider_name.trim()
    : state.status === 'ready' && state.auth.mode === 'oidc'
      ? 'identity provider'
      : 'access gateway'

  const blockedMessage = state.status === 'ready' && state.auth.mode === 'oidc'
    ? state.auth.session_status === 'expired'
      ? `Your APEX Load session has expired. Sign in with ${providerLabel} again to continue.`
      : state.auth.session_status === 'invalid'
        ? `APEX Load could not verify the local session cookie. Sign in with ${providerLabel} again to continue.`
        : state.auth.session_status === 'not_onboarded'
          ? `Your ${providerLabel} sign-in is valid, but this APEX Load workspace has not onboarded your account.`
          : state.auth.session_status === 'disabled'
            ? `Your APEX Load account is disabled. Sign out or contact an administrator to restore access.`
            : state.auth.session_status === 'revoked'
              ? `This APEX Load session was revoked. Sign out and authenticate with ${providerLabel} again.`
              : state.auth.session_status === 'blocked'
                ? `Your ${providerLabel} sign-in is valid, but APEX Load could not grant local access.`
                : `This dashboard is configured for native OIDC authentication. Complete sign-in with ${providerLabel} so APEX Load can establish your session.`
    : `This dashboard is configured for trusted-header authentication. Complete sign-in through the configured upstream ${providerLabel} so APEX Load receives your identity headers.`

  const signOutLocalSession = async () => {
    if (state.status !== 'ready' || !state.auth.logout_url || !state.auth.logout_csrf_token) return
    setLogoutPending(true)
    try {
      const response = await fetch(state.auth.logout_url, {
        method: 'POST',
        headers: { 'X-APEXLoad-CSRF-Token': state.auth.logout_csrf_token },
      })
      const payload = await response.json().catch(() => null) as { redirect_url?: string } | null
      window.location.assign(payload?.redirect_url || state.auth.login_url || '/')
    } catch {
      window.location.assign(state.auth.login_url || '/')
    }
  }

  if (state.status === 'loading') {
    return (
      <div className="auth-gate-shell">
        <section className="auth-gate-card auth-gate-card--loading">
          <div className="auth-gate-brand">
            <Logo size={32} title="APEX Load" />
            <p className="auth-gate-kicker">APEX Load Access</p>
          </div>
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
          <div className="auth-gate-brand">
            <Logo size={32} title="APEX Load" />
            <p className="auth-gate-kicker">APEX Load Access</p>
          </div>
          <h1>Authentication check failed</h1>
          <ErrorDiagnostic diagnostic={state.diagnostic} compact />
        </section>
      </div>
    )
  }

  if (state.auth.enabled && !state.auth.authenticated) {
    return (
      <div className="auth-gate-shell">
        <section className="auth-gate-card auth-gate-card--blocked">
          <div className="auth-gate-brand">
            <Logo size={32} title="APEX Load" />
            <p className="auth-gate-kicker">APEX Load Access</p>
          </div>
          <h1>Sign-in required</h1>
          <p>{blockedMessage}</p>
          {state.auth.login_url && (
            <p>
              <a className="auth-gate-action" href={state.auth.login_url}>Continue to {providerLabel}</a>
            </p>
          )}
          {state.auth.logout_url && state.auth.logout_csrf_token && (
            <p>
              <button className="auth-gate-action" type="button" onClick={signOutLocalSession} disabled={logoutPending}>
                {logoutPending ? 'Signing out...' : 'Sign out local session'}
              </button>
            </p>
          )}
          <p className="auth-gate-note">Current auth mode: {state.auth.mode.replace('_', ' ')}</p>
        </section>
      </div>
    )
  }

  return <>{children}</>
}
