// @vitest-environment jsdom

import { act } from "react";
import { createRoot, Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import AuthGate from "./AuthGate";
import { API_ORIGIN, resolveApiResourceUrl } from "../hooks/apiClient";
import { AuthStatusState } from "../hooks/useAuthStatus";
import { diagnoseError } from "../utils/errorUtils";
import type { WailsDesktopBridge } from "../desktop/desktopBridge";
import type { DesktopShellEventHandlers } from "../desktop/useDesktopShellEvents";

type ReadyAuthState = Extract<AuthStatusState, { status: "ready" }>["auth"];
type BridgeWindow = typeof window & {
  go?: { main?: { App?: WailsDesktopBridge } };
};

function setInputValue(input: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")?.set?.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
}
(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true;

describe("AuthGate", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    window.history.replaceState({}, "", "/");
    window.localStorage.clear();
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => {
      root.unmount();
    });
    container.remove();
    delete (window as BridgeWindow).go;
    window.localStorage.clear();
    vi.restoreAllMocks();
  });

  it("renders children when authentication is disabled", async () => {
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: false,
            mode: "disabled",
            authenticated: false,
          })}
        >
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      );
    });

    expect(container.textContent).toContain("Protected content");
    expect(container.textContent).not.toContain("Sign-in required");
  });

  it("blocks the dashboard when trusted-header auth is enabled without a user", async () => {
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "trusted_headers",
            provider_name: "Microsoft Entra ID",
            login_url: "https://login.example.test/start",
            authenticated: false,
          })}
        >
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      );
    });

    expect(container.textContent).toContain("Sign-in required");
    expect(container.textContent).toContain("Continue to Microsoft Entra ID");
    const link = container.querySelector("a") as HTMLAnchorElement | null;
    expect(link?.href).toBe("https://login.example.test/start");
    expect(container.textContent).not.toContain("Protected content");
  });

  it("renders children when a trusted-header identity is present", async () => {
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "trusted_headers",
            authenticated: true,
            user: {
              subject: "alice",
              display_name: "Alice Admin",
              global_roles: ["system_admin"],
            },
          })}
        >
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      );
    });

    expect(container.textContent).toContain("Protected content");
    expect(container.textContent).not.toContain("Checking access");
  });

  it("signs in with a visible local test persona form", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ authenticated: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const changed = vi.fn();
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({ enabled: true, mode: "local", authenticated: false, login_url: "/api/v1/auth/login" })}
          onAuthenticationChanged={changed}
        >
          <div>Protected content</div>
        </AuthGate>,
      );
    });

    expect(container.textContent).toContain("admin/admin");
    expect(container.textContent).toContain("domainadmin/domainadmin");
    expect(container.textContent).toContain("Read-only default workspace");
    const inputs = container.querySelectorAll<HTMLInputElement>("input");
    await act(async () => {
      setInputValue(inputs[0], "dev");
      setInputValue(inputs[1], "dev");
    });
    await act(async () => {
      container.querySelector("form")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });

    expect(fetchMock).toHaveBeenCalledWith(
      `${API_ORIGIN}/api/v1/auth/login`,
      expect.objectContaining({ method: "POST", body: JSON.stringify({ username: "dev", password: "dev" }) }),
    );
    expect(changed).toHaveBeenCalledOnce();
  });

  it("returns a startup-invalidated local session to the persona login", async () => {
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "local",
            authenticated: false,
            session_status: "invalid",
            logout_url: "/api/v1/auth/logout",
            logout_csrf_token: "stale-session-csrf",
          })}
        >
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      );
    });

    expect(container.textContent).toContain("Choose a test persona");
    expect(container.textContent).toContain("dev/dev");
    expect(container.textContent).toContain("Clear current local session");
    expect(container.textContent).not.toContain("Protected content");
  });

  it("keeps the local login form available and reports generic failures", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "invalid username or password" }), {
        status: 401,
        headers: { "Content-Type": "application/json" },
      }),
    );
    await act(async () => {
      root.render(
        <AuthGate state={readyState({ enabled: true, mode: "local", authenticated: false })}>
          <div>Protected content</div>
        </AuthGate>,
      );
    });
    const inputs = container.querySelectorAll<HTMLInputElement>("input");
    await act(async () => {
      setInputValue(inputs[0], "unknown");
      setInputValue(inputs[1], "wrong");
    });
    await act(async () => {
      container.querySelector("form")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });
    expect(container.querySelector('[role="alert"]')?.textContent).toContain("invalid username or password");
    expect(container.querySelector("form")).not.toBeNull();
  });

  it("keeps a blocked local session visible when clearing it fails", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "session store is unavailable" }), {
        status: 503,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const changed = vi.fn();
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "local",
            authenticated: false,
            session_status: "disabled",
            logout_url: "/api/v1/auth/logout",
            logout_csrf_token: "local-csrf-token",
          })}
          onAuthenticationChanged={changed}
        >
          <div>Protected content</div>
        </AuthGate>,
      );
    });

    const clearSession = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent === "Clear current local session",
    );
    await act(async () => {
      clearSession?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });

    expect(fetchMock).toHaveBeenCalledWith(
      resolveApiResourceUrl("/api/v1/auth/logout", API_ORIGIN),
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        headers: expect.any(Headers),
      }),
    );
    expect(changed).not.toHaveBeenCalled();
    expect(container.querySelector('[role="alert"]')?.textContent).toContain(
      "Unable to clear the current session: session store is unavailable",
    );
    expect(clearSession?.hasAttribute("disabled")).toBe(false);
  });

  it("reloads authentication providers after clearing a blocked local session", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ redirect_url: "/" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const changed = vi.fn();
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "local",
            authenticated: false,
            session_status: "revoked",
            logout_url: "/api/v1/auth/logout",
            logout_csrf_token: "local-csrf-token",
          })}
          onAuthenticationChanged={changed}
        >
          <div>Protected content</div>
        </AuthGate>,
      );
    });

    const clearSession = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent === "Clear current local session",
    );
    await act(async () => {
      clearSession?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });

    expect(changed).toHaveBeenCalledOnce();
    expect(container.querySelector('[role="alert"]')).toBeNull();
  });

  it("blocks the dashboard with OIDC-specific copy when native OIDC is enabled without a session", async () => {
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "oidc",
            provider_name: "Microsoft Entra ID",
            login_url: "/api/v1/auth/login",
            authenticated: false,
          })}
        >
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      );
    });

    expect(container.textContent).toContain("native OIDC authentication");
    expect(container.textContent).toContain("Continue to Microsoft Entra ID");
    expect(container.querySelector("a")).toBeNull();
    expect(
      Array.from(container.querySelectorAll("button")).some(
        (button) => button.textContent === "Continue to Microsoft Entra ID",
      ),
    ).toBe(true);
    expect(container.textContent).not.toContain("Protected content");
  });

  it("keeps unsupported desktop OIDC out of the embedded webview and opens the system browser", async () => {
    const openInBrowser = vi.fn().mockResolvedValue(undefined);
    const openURL = vi.fn().mockResolvedValue(undefined);
    (window as BridgeWindow).go = {
      main: { App: { OpenInBrowser: openInBrowser, OpenURL: openURL } },
    };
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "oidc",
            provider_name: "Microsoft Entra ID",
            login_url: "/api/v1/auth/login",
            authenticated: false,
          })}
        >
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      );
    });

    expect(container.textContent).toContain("Continue in your browser");
    expect(container.textContent).toContain(
      "not shared with the embedded desktop webview",
    );
    expect(container.querySelector("a")).toBeNull();

    const button = Array.from(container.querySelectorAll("button")).find(
      (candidate) => candidate.textContent === "Open authenticated dashboard",
    );
    await act(async () =>
      button?.dispatchEvent(new MouseEvent("click", { bubbles: true })),
    );
    expect(openInBrowser).toHaveBeenCalledWith("admin", "");
    expect(openURL).not.toHaveBeenCalled();
  });

  it("honors a native Help menu browser request while desktop auth blocks AppShell", async () => {
    const openInBrowser = vi.fn().mockResolvedValue(undefined);
    let desktopHandlers: DesktopShellEventHandlers | null = null;
    (window as BridgeWindow).go = {
      main: { App: { OpenInBrowser: openInBrowser } },
    };
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "oidc",
            login_url: "/api/v1/auth/login",
            authenticated: false,
          })}
          onDesktopHandlersChange={(handlers) => {
            desktopHandlers = handlers;
          }}
        >
          <div>Protected content</div>
        </AuthGate>,
      );
    });

    const registeredHandlers = desktopHandlers as DesktopShellEventHandlers | null;
    expect(registeredHandlers?.onMenuOpenBrowser).toBeTypeOf("function");
    await act(async () => {
      await registeredHandlers?.onMenuOpenBrowser?.();
    });
    expect(openInBrowser).toHaveBeenCalledWith("admin", "");
  });

  it("visibly consumes workspace-only native actions while desktop auth is blocked", async () => {
    let desktopHandlers: DesktopShellEventHandlers | null = null;
    (window as BridgeWindow).go = { main: { App: {} } };
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "oidc",
            authenticated: false,
          })}
          onDesktopHandlersChange={(handlers) => {
            desktopHandlers = handlers;
          }}
        >
          <div>Protected content</div>
        </AuthGate>,
      );
    });

    const registeredHandlers = desktopHandlers as DesktopShellEventHandlers | null;
    expect(registeredHandlers?.onMenuAction).toBeTypeOf("function");
    await act(async () => {
      await registeredHandlers?.onMenuAction?.("new-test");
    });

    expect(container.querySelector('[role="alert"]')?.textContent).toContain(
      "needs the embedded workspace",
    );
    expect(container.querySelector('[role="alert"]')?.textContent).toContain(
      "Local Test Login",
    );
  });

  it("directs blocked native actions back to the embedded local login", async () => {
    let desktopHandlers: DesktopShellEventHandlers | null = null;
    (window as BridgeWindow).go = { main: { App: {} } };
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "local",
            authenticated: false,
          })}
          onDesktopHandlersChange={(handlers) => {
            desktopHandlers = handlers;
          }}
        >
          <div>Protected content</div>
        </AuthGate>,
      );
    });

    const registeredHandlers = desktopHandlers as DesktopShellEventHandlers | null;
    expect(registeredHandlers?.onMenuAction).toBeTypeOf("function");
    await act(async () => {
      await registeredHandlers?.onMenuAction?.("new-test");
    });

    expect(container.querySelector('[role="alert"]')?.textContent).toContain(
      "Sign in with Local Test Login here",
    );
  });

  it("opens the validated upstream login URL for desktop trusted-header auth", async () => {
    const openURL = vi.fn().mockResolvedValue(undefined);
    const openInBrowser = vi.fn().mockResolvedValue(undefined);
    (window as BridgeWindow).go = {
      main: { App: { OpenURL: openURL, OpenInBrowser: openInBrowser } },
    };
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "trusted_headers",
            provider_name: "Microsoft Entra ID",
            login_url: "https://login.example.test/start",
            authenticated: false,
          })}
        >
          <div>Protected content</div>
        </AuthGate>,
      );
    });

    const button = Array.from(container.querySelectorAll("button")).find(
      (candidate) => candidate.textContent === "Open authenticated dashboard",
    );
    await act(async () => button?.click());

    expect(openURL).toHaveBeenCalledWith("https://login.example.test/start");
    expect(openInBrowser).not.toHaveBeenCalled();
  });

  it("rejects an invalid desktop trusted-header login URL", async () => {
    const openURL = vi.fn().mockResolvedValue(undefined);
    (window as BridgeWindow).go = { main: { App: { OpenURL: openURL } } };
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "trusted_headers",
            login_url: "javascript:alert(1)",
            authenticated: false,
          })}
        >
          <div>Protected content</div>
        </AuthGate>,
      );
    });

    const button = Array.from(container.querySelectorAll("button")).find(
      (candidate) => candidate.textContent === "Open authenticated dashboard",
    );
    await act(async () => button?.click());

    expect(openURL).not.toHaveBeenCalled();
    expect(container.textContent).toContain(
      "Could not open the authenticated dashboard",
    );
  });

  it("does not resolve a relative desktop trusted-header login URL against the loopback controller", async () => {
    const openURL = vi.fn().mockResolvedValue(undefined);
    (window as BridgeWindow).go = { main: { App: { OpenURL: openURL } } };
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "trusted_headers",
            login_url: "/api/v1/auth/login",
            authenticated: false,
          })}
        >
          <div>Protected content</div>
        </AuthGate>,
      );
    });

    const button = Array.from(container.querySelectorAll("button")).find(
      (candidate) => candidate.textContent === "Open authenticated dashboard",
    );
    await act(async () => button?.click());

    expect(openURL).not.toHaveBeenCalled();
    expect(container.querySelector('[role="alert"]')?.textContent).toContain(
      "a relative path would reopen the unauthenticated desktop loopback",
    );
    expect(container.textContent).toContain(
      "Configure the public gateway URL in the authentication settings",
    );
  });

  it("starts OIDC through a capability-bearing managed fetch", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockReturnValue(new Promise(() => {}));
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "oidc",
            provider_name: "Microsoft Entra ID",
            login_url: "/api/v1/auth/login",
            authenticated: false,
          })}
        >
          <div>Protected content</div>
        </AuthGate>,
      );
    });
    const login = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent === "Continue to Microsoft Entra ID",
    );
    await act(async () => login?.click());
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const loginURL = new URL(String(fetchMock.mock.calls[0][0]));
    expect(`${loginURL.origin}${loginURL.pathname}`).toBe(
      resolveApiResourceUrl("/api/v1/auth/login", API_ORIGIN),
    );
    expect(loginURL.searchParams.get("return_to")).toBe("/");
    expect(fetchMock.mock.calls[0][1]).toEqual(
      expect.objectContaining({
        credentials: "include",
        headers: expect.any(Headers),
      }),
    );
    expect((fetchMock.mock.calls[0][1]?.headers as Headers).get("Accept")).toBe(
      "application/json",
    );
    expect(login?.hasAttribute("disabled")).toBe(true);
  });

  it("explains when the native oidc session expired", async () => {
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "oidc",
            provider_name: "Microsoft Entra ID",
            login_url: "/api/v1/auth/login",
            authenticated: false,
            session_status: "expired",
          })}
        >
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      );
    });

    expect(container.textContent).toContain("session has expired");
    expect(container.textContent).toContain("Continue to Microsoft Entra ID");
    expect(container.textContent).not.toContain("Protected content");
  });

  it("posts logout to the configured API origin with credentials", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockReturnValue(new Promise(() => {}));
    await act(async () => {
      root.render(
        <AuthGate
          state={readyState({
            enabled: true,
            mode: "oidc",
            authenticated: false,
            login_url: "/api/v1/auth/login",
            logout_url: "/api/v1/auth/logout",
            logout_csrf_token: "csrf-token",
          })}
        >
          <div>Protected content</div>
        </AuthGate>,
      );
    });

    const logout = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent === "Sign out local session",
    );
    await act(async () => {
      logout?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    expect(fetchMock).toHaveBeenCalledWith(
      resolveApiResourceUrl("/api/v1/auth/logout", API_ORIGIN),
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        headers: expect.any(Headers),
      }),
    );
    expect(
      (fetchMock.mock.calls[0][1]?.headers as Headers).get(
        "X-APEXLoad-CSRF-Token",
      ),
    ).toBe("csrf-token");
    expect(logout?.hasAttribute("disabled")).toBe(true);
  });

  it("shows diagnostic guidance when authentication status cannot be checked", async () => {
    await act(async () => {
      root.render(
        <AuthGate
          state={{
            status: "error",
            message: "API controller is unreachable",
            diagnostic: diagnoseError(
              new TypeError("Failed to fetch"),
              "Unable to determine authentication status.",
            ),
          }}
        >
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      );
    });

    expect(container.textContent).toContain("Authentication check failed");
    expect(container.textContent).toContain("API controller is unreachable");
    expect(container.textContent).toContain(
      "Docker Compose stack or controller process",
    );
    expect(container.textContent).not.toContain("Protected content");
  });
});

function readyState(auth: ReadyAuthState): AuthStatusState {
  return { status: "ready", auth };
}
