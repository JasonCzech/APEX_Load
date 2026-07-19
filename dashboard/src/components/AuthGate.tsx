import {
  ReactNode,
  useCallback,
  useLayoutEffect,
  useMemo,
  useState,
} from "react";

import {
  apiFetch,
  openDesktopBrowserWithPreview,
  requestApiNavigation,
  resolveApiResourceUrl,
} from "../hooks/apiClient";
import { AuthStatusState } from "../hooks/useAuthStatus";
import { getDesktopBridge, isDesktopSurface } from "../desktop/desktopBridge";
import type { DesktopShellEventHandlers } from "../desktop/useDesktopShellEvents";
import ErrorDiagnostic from "./ErrorDiagnostic";
import Logo from "./Logo";
import "./AuthGate.css";

function validatedTrustedHeaderLoginURL(loginURL: string | undefined): string {
  const trimmed = loginURL?.trim() || "";
  if (!trimmed) {
    throw new Error(
      "Desktop trusted-header sign-in requires an absolute upstream login URL. Configure the public gateway URL in the authentication settings.",
    );
  }
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    throw new Error(
      "Desktop trusted-header sign-in requires an absolute upstream login URL; a relative path would reopen the unauthenticated desktop loopback. Configure the public gateway URL in the authentication settings.",
    );
  }
  if (
    (parsed.protocol !== "http:" && parsed.protocol !== "https:") ||
    parsed.username ||
    parsed.password
  ) {
    throw new Error(
      "Desktop trusted-header sign-in requires an absolute http(s) upstream login URL without embedded credentials.",
    );
  }
  return parsed.toString();
}

export default function AuthGate({
  children,
  state,
  onDesktopHandlersChange,
}: {
  children: ReactNode;
  state: AuthStatusState;
  onDesktopHandlersChange?: (
    handlers: DesktopShellEventHandlers | null,
  ) => void;
}) {
  const [logoutPending, setLogoutPending] = useState(false);
  const [loginPending, setLoginPending] = useState(false);
  const [loginError, setLoginError] = useState("");
  const [desktopOpenPending, setDesktopOpenPending] = useState(false);
  const [desktopOpenError, setDesktopOpenError] = useState("");
  const [desktopIntentError, setDesktopIntentError] = useState("");
  const desktopSurface = isDesktopSurface();
  const providerLabel =
    state.status === "ready" && state.auth.provider_name?.trim()
      ? state.auth.provider_name.trim()
      : state.status === "ready" && state.auth.mode === "oidc"
        ? "identity provider"
        : "access gateway";

  const blockedMessage =
    state.status === "ready" && state.auth.mode === "oidc"
      ? state.auth.session_status === "expired"
        ? `Your APEX Load session has expired. Sign in with ${providerLabel} again to continue.`
        : state.auth.session_status === "invalid"
          ? `APEX Load could not verify the local session cookie. Sign in with ${providerLabel} again to continue.`
          : state.auth.session_status === "not_onboarded"
            ? `Your ${providerLabel} sign-in is valid, but this APEX Load workspace has not onboarded your account.`
            : state.auth.session_status === "disabled"
              ? `Your APEX Load account is disabled. Sign out or contact an administrator to restore access.`
              : state.auth.session_status === "revoked"
                ? `This APEX Load session was revoked. Sign out and authenticate with ${providerLabel} again.`
                : state.auth.session_status === "blocked"
                  ? `Your ${providerLabel} sign-in is valid, but APEX Load could not grant local access.`
                  : `This dashboard is configured for native OIDC authentication. Complete sign-in with ${providerLabel} so APEX Load can establish your session.`
      : `This dashboard is configured for trusted-header authentication. Complete sign-in through the configured upstream ${providerLabel} so APEX Load receives your identity headers.`;

  const signOutLocalSession = async () => {
    if (
      state.status !== "ready" ||
      !state.auth.logout_url ||
      !state.auth.logout_csrf_token
    )
      return;
    const loginUrl = state.auth.login_url
      ? resolveApiResourceUrl(state.auth.login_url)
      : "/";
    setLogoutPending(true);
    try {
      const response = await apiFetch(
        resolveApiResourceUrl(state.auth.logout_url),
        {
          method: "POST",
          credentials: "include",
          headers: { "X-APEXLoad-CSRF-Token": state.auth.logout_csrf_token },
        },
      );
      const payload = (await response.json().catch(() => null)) as {
        redirect_url?: string;
      } | null;
      window.location.assign(
        payload?.redirect_url
          ? resolveApiResourceUrl(payload.redirect_url)
          : loginUrl,
      );
    } catch {
      window.location.assign(loginUrl);
    }
  };

  const openAuthenticatedBrowser = useCallback(async () => {
    const bridge = getDesktopBridge();
    setDesktopOpenPending(true);
    setDesktopOpenError("");
    try {
      if (state.status === "ready" && state.auth.mode === "trusted_headers") {
        if (!bridge?.OpenURL) {
          throw new Error("Desktop browser launch is unavailable");
        }
        await bridge.OpenURL(
          validatedTrustedHeaderLoginURL(state.auth.login_url),
        );
      } else if (bridge?.OpenInBrowser) {
        // OIDC and auth-diagnostic launches must use the one-time bootstrap so
        // the system-browser tab gets its port-scoped desktop capability.
        await openDesktopBrowserWithPreview(bridge);
      } else {
        throw new Error("Desktop browser launch is unavailable");
      }
    } catch (cause: unknown) {
      const detail =
        cause instanceof Error && cause.message ? ` ${cause.message}` : "";
      setDesktopOpenError(
        `Could not open the authenticated dashboard.${detail} Open the public controller gateway in your browser, or restart the desktop controller in Dev Bypass mode.`,
      );
    } finally {
      setDesktopOpenPending(false);
    }
  }, [state]);
  const desktopWorkspaceBlocked =
    desktopSurface &&
    (state.status === "error" ||
      (state.status === "ready" &&
        state.auth.enabled &&
        !state.auth.authenticated));
  const reportBlockedDesktopIntent = useCallback(() => {
    setDesktopIntentError(
      "This native command needs the embedded workspace, which is unavailable with the current desktop authentication. Open the authenticated dashboard in your browser, or restart the desktop controller in Dev Bypass mode.",
    );
  }, []);
  const gatedDesktopHandlers = useMemo<DesktopShellEventHandlers>(
    () => ({
      onMenuOpenBrowser: openAuthenticatedBrowser,
      onMenuAction: reportBlockedDesktopIntent,
      onOpenFile: reportBlockedDesktopIntent,
      onOpenURL: reportBlockedDesktopIntent,
      onOpenRecent: reportBlockedDesktopIntent,
      onRecentsChanged: () => undefined,
    }),
    [openAuthenticatedBrowser, reportBlockedDesktopIntent],
  );
  useLayoutEffect(() => {
    if (!onDesktopHandlersChange) return undefined;
    onDesktopHandlersChange(
      desktopWorkspaceBlocked ? gatedDesktopHandlers : null,
    );
    return () => onDesktopHandlersChange(null);
  }, [
    desktopWorkspaceBlocked,
    gatedDesktopHandlers,
    onDesktopHandlersChange,
  ]);

  const beginOIDCLogin = async () => {
    if (state.status !== "ready" || !state.auth.login_url) return;
    setLoginPending(true);
    setLoginError("");
    try {
      const loginURL = new URL(resolveApiResourceUrl(state.auth.login_url));
      if (typeof window !== "undefined") {
        const returnTo = `${window.location.pathname}${window.location.search}${window.location.hash}`;
        loginURL.searchParams.set("return_to", returnTo || "/");
      }
      const redirectURL = await requestApiNavigation(loginURL.toString());
      window.location.assign(redirectURL);
    } catch (cause: unknown) {
      setLoginError(
        cause instanceof Error
          ? cause.message
          : "Could not start authentication.",
      );
    } finally {
      setLoginPending(false);
    }
  };

  if (state.status === "loading") {
    return (
      <div className="auth-gate-shell">
        <section className="auth-gate-card auth-gate-card--loading">
          <div className="auth-gate-brand">
            <Logo size={32} title="APEX Load" />
            <p className="auth-gate-kicker">APEX Load Access</p>
          </div>
          <h1>Checking access</h1>
          <p>
            Confirming your upstream authentication context before loading the
            dashboard.
          </p>
        </section>
      </div>
    );
  }

  if (state.status === "error") {
    return (
      <div className="auth-gate-shell">
        <section className="auth-gate-card auth-gate-card--error">
          <div className="auth-gate-brand">
            <Logo size={32} title="APEX Load" />
            <p className="auth-gate-kicker">APEX Load Access</p>
          </div>
          <h1>Authentication check failed</h1>
          <ErrorDiagnostic diagnostic={state.diagnostic} compact />
          {desktopSurface && (
            <p>
              <button
                className="auth-gate-action"
                type="button"
                onClick={openAuthenticatedBrowser}
                disabled={desktopOpenPending}
              >
                {desktopOpenPending
                  ? "Opening browser..."
                  : "Open dashboard in system browser"}
              </button>
            </p>
          )}
          {desktopOpenError && <p role="alert">{desktopOpenError}</p>}
          {desktopIntentError && <p role="alert">{desktopIntentError}</p>}
        </section>
      </div>
    );
  }

  if (state.auth.enabled && !state.auth.authenticated) {
    if (
      desktopSurface &&
      (state.auth.mode === "oidc" || state.auth.mode === "trusted_headers")
    ) {
      return (
        <div className="auth-gate-shell">
          <section className="auth-gate-card auth-gate-card--blocked">
            <div className="auth-gate-brand">
              <Logo size={32} title="APEX Load" />
              <p className="auth-gate-kicker">APEX Load Desktop Access</p>
            </div>
            <h1>Continue in your browser</h1>
            <p>
              {state.auth.mode === "oidc"
                ? "Native OIDC sessions are established in the system browser and are not shared with the embedded desktop webview."
                : "Trusted-header authentication requires an upstream gateway, which the desktop loopback connection does not traverse."}
            </p>
            <p>
              <button
                className="auth-gate-action"
                type="button"
                onClick={openAuthenticatedBrowser}
                disabled={desktopOpenPending}
              >
                {desktopOpenPending
                  ? "Opening browser..."
                  : "Open authenticated dashboard"}
              </button>
            </p>
            <p className="auth-gate-note">
              To use the embedded desktop workspace, configure Dev Bypass and
              restart the desktop controller.
            </p>
            {desktopOpenError && <p role="alert">{desktopOpenError}</p>}
            {desktopIntentError && <p role="alert">{desktopIntentError}</p>}
          </section>
        </div>
      );
    }
    return (
      <div className="auth-gate-shell">
        <section className="auth-gate-card auth-gate-card--blocked">
          <div className="auth-gate-brand">
            <Logo size={32} title="APEX Load" />
            <p className="auth-gate-kicker">APEX Load Access</p>
          </div>
          <h1>Sign-in required</h1>
          <p>{blockedMessage}</p>
          {state.auth.login_url && state.auth.mode === "oidc" && (
            <p>
              <button
                className="auth-gate-action"
                type="button"
                onClick={() => {
                  void beginOIDCLogin();
                }}
                disabled={loginPending}
              >
                {loginPending
                  ? "Opening sign-in…"
                  : `Continue to ${providerLabel}`}
              </button>
            </p>
          )}
          {state.auth.login_url && state.auth.mode !== "oidc" && (
            <p>
              <a
                className="auth-gate-action"
                href={resolveApiResourceUrl(state.auth.login_url)}
              >
                Continue to {providerLabel}
              </a>
            </p>
          )}
          {loginError && <p role="alert">{loginError}</p>}
          {desktopOpenError && <p role="alert">{desktopOpenError}</p>}
          {desktopIntentError && <p role="alert">{desktopIntentError}</p>}
          {state.auth.logout_url && state.auth.logout_csrf_token && (
            <p>
              <button
                className="auth-gate-action"
                type="button"
                onClick={signOutLocalSession}
                disabled={logoutPending}
              >
                {logoutPending ? "Signing out..." : "Sign out local session"}
              </button>
            </p>
          )}
          <p className="auth-gate-note">
            Current auth mode: {state.auth.mode.replace("_", " ")}
          </p>
        </section>
      </div>
    );
  }

  return <>{children}</>;
}
