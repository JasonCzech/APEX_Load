import { useEffect, useRef, useState, type FormEvent } from 'react'

import DialogLayer from './ux/DialogLayer'
import { useModalDialog } from '../hooks/useModalDialog'

/**
 * Shown over the still-mounted app when a previously valid session ends, so
 * signing in again keeps the current page and any unsaved work.
 */
export function SessionExpiredDialog({
  mode,
  providerLabel,
  onLocalSignIn,
  onContinueToProvider,
  onCheckAgain,
  providerPending = false,
  providerError = '',
}: {
  mode: string
  providerLabel: string
  onLocalSignIn: (username: string, password: string) => Promise<void>
  /** Redirects to the identity provider; this leaves the page. */
  onContinueToProvider?: () => void
  /** Re-checks the session after signing in elsewhere (another tab); rejects while still signed out. */
  onCheckAgain: () => Promise<void>
  providerPending?: boolean
  providerError?: string
}) {
  const dialogRef = useRef<HTMLDivElement>(null)
  const usernameRef = useRef<HTMLInputElement>(null)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  useModalDialog({ dialogRef, initialFocusRef: mode === 'local' ? usernameRef : undefined })

  const checkAgain = async () => {
    if (pending) return
    setPending(true)
    setError('')
    try {
      await onCheckAgain()
    } catch (cause: unknown) {
      setError(cause instanceof Error ? cause.message : 'Unable to check the session.')
    } finally {
      setPending(false)
    }
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (pending) return
    setPending(true)
    setError('')
    try {
      await onLocalSignIn(username, password)
    } catch (cause: unknown) {
      setError(cause instanceof Error ? cause.message : 'Unable to sign in.')
    } finally {
      setPending(false)
    }
  }

  return <DialogLayer><div className="modal-overlay session-expired-overlay" role="presentation">
    <div ref={dialogRef} className="modal session-expired-dialog" role="alertdialog" aria-modal="true" aria-labelledby="session-expired-title" aria-describedby="session-expired-description">
      <header className="modal-header"><h3 id="session-expired-title">Your session expired</h3></header>
      <p id="session-expired-description">{mode === 'local'
        ? 'Sign in again to continue. This page and anything you have not saved stay open behind this dialog.'
        : onContinueToProvider
          ? `Continuing to ${providerLabel} leaves this page and brings you back to it afterwards; changes you have not saved are lost. To keep them, sign in to ${providerLabel} in another tab, then check again here.`
          : `Sign in through ${providerLabel} in another tab, then check again here. This page and anything you have not saved stay open behind this dialog.`}</p>
      {mode === 'local' ? <form className="auth-gate-form" onSubmit={event => { void submit(event) }}>
        <label><span>Username</span><input ref={usernameRef} autoComplete="username" value={username} onChange={event => setUsername(event.target.value)} disabled={pending} required /></label>
        <label><span>Password</span><input type="password" autoComplete="current-password" value={password} onChange={event => setPassword(event.target.value)} disabled={pending} required /></label>
        <div className="modal-actions"><button className="btn btn-primary" type="submit" disabled={pending}>{pending ? 'Signing in…' : 'Sign in'}</button></div>
      </form> : <div className="modal-actions">
        <button className={onContinueToProvider ? 'btn btn-secondary' : 'btn btn-primary'} type="button" onClick={() => { void checkAgain() }} disabled={pending || providerPending}>{pending ? 'Checking…' : 'I have signed in — check again'}</button>
        {onContinueToProvider && <button className="btn btn-primary" type="button" onClick={onContinueToProvider} disabled={pending || providerPending}>{providerPending ? 'Opening…' : `Continue to ${providerLabel}`}</button>}
      </div>}
      {(error || providerError) && <p className="auth-gate-error" role="alert">{error || providerError}</p>}
    </div>
  </div></DialogLayer>
}

/** Warns before a session ends; re-renders itself when the warning window opens. */
export function SessionExpiryNotice({ expiresAt, warnBeforeMs = 5 * 60_000 }: { expiresAt?: string; warnBeforeMs?: number }) {
  const expiry = Date.parse(expiresAt || '')
  const [, setTick] = useState(0)
  useEffect(() => {
    if (!Number.isFinite(expiry)) return undefined
    const untilWarning = expiry - warnBeforeMs - Date.now()
    if (untilWarning <= 0) return undefined
    const timer = window.setTimeout(() => setTick(value => value + 1), untilWarning + 50)
    return () => window.clearTimeout(timer)
  }, [expiry, warnBeforeMs])
  if (!Number.isFinite(expiry) || expiry - Date.now() > warnBeforeMs || expiry <= Date.now()) return null
  const time = new Date(expiry).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })
  return <div className="session-expiry-notice" role="status">Your session ends at {time}. You will be asked to sign in again, and this page stays open — save your work now if you can.</div>
}

/** An auth re-check failed after the app loaded: keep the app, offer a retry. */
export function SessionCheckFailedNotice({ message, onRetry }: { message: string; onRetry: () => void }) {
  return <div className="session-expiry-notice session-check-failed" role="alert">
    Could not confirm your session: {message} Retrying automatically every minute.
    <button type="button" className="btn btn-secondary btn-sm" onClick={onRetry}>Retry now</button>
  </div>
}
