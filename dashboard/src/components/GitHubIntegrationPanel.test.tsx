// @vitest-environment jsdom

import { act } from "react";
import { createRoot, Root } from "react-dom/client";
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
  type MockInstance,
} from "vitest";

import GitHubIntegrationPanel from "./GitHubIntegrationPanel";
import type { WailsDesktopBridge } from "../desktop/desktopBridge";
import { DashboardGitHubSettings } from "../types";

type BridgeWindow = typeof window & {
  go?: { main?: { App?: WailsDesktopBridge } };
};

(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true;

function settings(
  overrides: Partial<DashboardGitHubSettings> = {},
): DashboardGitHubSettings {
  return {
    enabled: true,
    client_id: "client-1",
    client_secret_env_var: "APEXLOAD_GITHUB_CLIENT_SECRET",
    app_id: 123,
    app_slug: "apex-load",
    app_private_key_env_var: "APEXLOAD_GITHUB_APP_PRIVATE_KEY",
    app_private_key_file: "",
    webhook_secret_env_var: "APEXLOAD_GITHUB_WEBHOOK_SECRET",
    pat_env_var: "",
    automation_mode: "app_oauth_pat",
    checks_enabled: true,
    api_base_url: "https://ghe.example.com/api/v3",
    web_base_url: "https://ghe.example.com",
    ca_cert_file: "",
    tls_insecure_skip_verify: false,
    scopes: ["repo"],
    default_repo: "org/scripts",
    default_branch: "main",
    path_template: "apexload/scripts/{project}/{ref_key}.json",
    ...overrides,
  };
}

function jsonResponse(body: unknown) {
  return Promise.resolve(
    new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }),
  );
}

function findButton(label: string) {
  return Array.from(document.querySelectorAll("button")).find(
    (button) => button.textContent?.trim() === label,
  ) as HTMLButtonElement | undefined;
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("GitHubIntegrationPanel", () => {
  let container: HTMLDivElement;
  let root: Root;
  let fetchMock: MockInstance<
    (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>
  >;

  beforeEach(() => {
    window.history.replaceState({}, "", "/");
    window.localStorage.clear();
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async (input) => {
        const url = String(input);
        if (url.includes("/api/v1/integrations/github/connection")) {
          return jsonResponse({
            connected: true,
            login: "octocat",
            scopes: "repo",
          });
        }
        return jsonResponse({});
      });
  });

  afterEach(async () => {
    await act(async () => {
      root.unmount();
    });
    container.remove();
    delete (window as BridgeWindow).go;
    window.history.replaceState({}, "", "/");
    window.localStorage.clear();
    fetchMock.mockRestore();
  });

  it("shows the connected GitHub identity and renders the admin form", async () => {
    await act(async () => {
      root.render(
        <GitHubIntegrationPanel value={settings()} onChange={() => {}} />,
      );
    });
    await flushPromises();
    await flushPromises();

    expect(container.textContent).toContain("Connected as octocat");
    expect(findButton("Disconnect")).toBeTruthy();
    const apiBase = container.querySelector(
      'input[value="https://ghe.example.com/api/v3"]',
    );
    expect(apiBase).toBeTruthy();
  });

  it("emits config changes via onChange when toggling enabled", async () => {
    const onChange = vi.fn();
    await act(async () => {
      root.render(
        <GitHubIntegrationPanel
          value={settings({ enabled: false })}
          onChange={onChange}
        />,
      );
    });
    await flushPromises();

    const checkbox = container.querySelector(
      'input[type="checkbox"]',
    ) as HTMLInputElement;
    await act(async () => {
      checkbox.click();
    });
    expect(onChange).toHaveBeenCalled();
    const next = onChange.mock.calls[0][0] as DashboardGitHubSettings;
    expect(next.enabled).toBe(true);
  });

  it("disables Connect when the integration is not enabled", async () => {
    fetchMock.mockImplementation(async () =>
      jsonResponse({ connected: false }),
    );
    await act(async () => {
      root.render(
        <GitHubIntegrationPanel
          value={settings({ enabled: false })}
          onChange={() => {}}
        />,
      );
    });
    await flushPromises();
    await flushPromises();

    const connect = findButton("Connect GitHub");
    expect(connect).toBeTruthy();
    expect(connect?.disabled).toBe(true);
  });

  it("starts GitHub OAuth through a managed controller fetch", async () => {
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url.includes("/api/v1/integrations/github/login")) {
        return new Promise<Response>(() => {});
      }
      if (url.includes("/api/v1/integrations/github/connection")) {
        return jsonResponse({ connected: false });
      }
      return jsonResponse({});
    });
    await act(async () => {
      root.render(
        <GitHubIntegrationPanel value={settings()} onChange={() => {}} />,
      );
    });
    await flushPromises();
    await flushPromises();

    const connect = findButton("Connect GitHub");
    await act(async () => connect?.click());
    const loginCall = fetchMock.mock.calls.find(([input]) =>
      String(input).includes("/api/v1/integrations/github/login"),
    );
    expect(loginCall).toBeTruthy();
    const loginURL = new URL(String(loginCall?.[0]));
    expect(loginURL.searchParams.get("return_to")).toBe("/");
    expect(loginURL.searchParams.get("return_to")).not.toMatch(/^https?:/);
    const init = loginCall?.[1] as RequestInit;
    expect((init.headers as Headers).get("Accept")).toBe("application/json");
    expect(init.credentials).toBe("include");
    expect(connect?.disabled).toBe(true);
  });

  it("hands desktop GitHub authorization to an authenticated system browser", async () => {
    const openInBrowser = vi.fn().mockResolvedValue(undefined);
    const setLastRoute = vi.fn().mockResolvedValue(undefined);
    (window as BridgeWindow).go = {
      main: {
        App: {
          OpenInBrowser: openInBrowser,
          SetLastRoute: setLastRoute,
        },
      },
    };
    window.history.replaceState({}, "", "/#/settings");
    fetchMock.mockImplementation(async (input) => {
      if (String(input).includes("/api/v1/integrations/github/connection")) {
        return jsonResponse({ connected: false });
      }
      return jsonResponse({});
    });
    await act(async () => {
      root.render(
        <GitHubIntegrationPanel value={settings()} onChange={() => {}} />,
      );
    });
    await flushPromises();
    await flushPromises();

    await act(async () => findButton("Connect GitHub")?.click());

    expect(setLastRoute).toHaveBeenCalledWith("#/settings");
    expect(openInBrowser).toHaveBeenCalledWith("admin", "");
    expect(
      fetchMock.mock.calls.some(([input]) =>
        String(input).includes("/api/v1/integrations/github/login"),
      ),
    ).toBe(false);
    expect(container.textContent).toContain(
      "authenticated dashboard is open in your system browser",
    );
  });
});
