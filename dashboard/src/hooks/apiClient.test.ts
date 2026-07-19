// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  DEV_VIEW_STORAGE_KEY,
  DESKTOP_CAPABILITY_SESSION_KEY,
  API_ORIGIN,
  apiFetch,
  apiFetchFromOrigin,
  buildDevPreviewHeaders,
  initializeDesktopApiOrigin,
  hasDesktopApiCapability,
  openDesktopBrowserWithPreview,
  readDevPreviewContext,
  readStoredDevViewMode,
  readDesktopApiBootstrap,
  request,
  requestApiNavigation,
  requestFromOrigin,
  resolveApiResourceUrl,
  resolveApiOrigin,
  setApiOrigin,
  setDesktopApiCapability,
  wsUrl,
  writeStoredDevViewMode,
} from "./apiClient";

describe("resolveApiOrigin", () => {
  it("uses an explicit API origin when configured", () => {
    expect(
      resolveApiOrigin(
        { VITE_API_ORIGIN: "http://api.example.test:8080/" },
        {
          protocol: "http:",
          hostname: "localhost",
          port: "3000",
          origin: "http://localhost:3000",
        },
      ),
    ).toBe("http://api.example.test:8080");
  });

  it("uses the same-origin Vite proxy for local dev dashboards", () => {
    expect(
      resolveApiOrigin(
        { DEV: true },
        {
          protocol: "http:",
          hostname: "localhost",
          port: "3031",
          origin: "http://localhost:3031",
        },
      ),
    ).toBe("http://localhost:3031");
  });

  it("uses the same-origin proxy for localhost port 3000 even when DEV metadata is absent", () => {
    expect(
      resolveApiOrigin(
        {},
        {
          protocol: "http:",
          hostname: "127.0.0.1",
          port: "3000",
          origin: "http://127.0.0.1:3000",
        },
      ),
    ).toBe("http://127.0.0.1:3000");
  });

  it("falls back to the local controller for file URLs", () => {
    expect(
      resolveApiOrigin(
        { DEV: true },
        { protocol: "file:", hostname: "", port: "", origin: "null" },
      ),
    ).toBe("http://127.0.0.1:8080");
  });

  it("uses the serving origin for embedded controller dashboard assets", () => {
    expect(
      resolveApiOrigin(
        {},
        {
          protocol: "http:",
          hostname: "apexload.example.test",
          port: "",
          origin: "https://apexload.example.test",
        },
      ),
    ).toBe("https://apexload.example.test");
  });
});

describe("resolveApiResourceUrl", () => {
  it("resolves API-provided relative auth URLs against the configured controller origin", () => {
    expect(
      resolveApiResourceUrl(
        "/api/v1/auth/login",
        "https://controller.example.test",
      ),
    ).toBe("https://controller.example.test/api/v1/auth/login");
    expect(
      resolveApiResourceUrl(
        "api/v1/auth/logout",
        "https://controller.example.test/",
      ),
    ).toBe("https://controller.example.test/api/v1/auth/logout");
  });

  it("preserves absolute identity-provider URLs", () => {
    expect(
      resolveApiResourceUrl(
        "https://login.example.test/start",
        "https://controller.example.test",
      ),
    ).toBe("https://login.example.test/start");
  });
});

describe("desktop API bootstrap", () => {
  it("waits for API readiness before reading the per-launch capability", async () => {
    const calls: string[] = [];
    const result = await readDesktopApiBootstrap({
      GetAPIOrigin: async () => {
        calls.push("origin:start");
        await Promise.resolve();
        calls.push("origin:ready");
        return "http://127.0.0.1:49152";
      },
      GetAPICapability: async () => {
        calls.push("capability");
        return "launch-secret";
      },
    });

    expect(calls).toEqual(["origin:start", "origin:ready", "capability"]);
    expect(result).toEqual({
      origin: "http://127.0.0.1:49152",
      capability: "launch-secret",
    });
  });

  it("uses the injected bridge from the HTTP Vite origin in wails dev", async () => {
    await initializeDesktopApiOrigin({
      GetAPIOrigin: async () => "http://127.0.0.1:49154",
      GetAPICapability: async () => "dev-launch-secret",
    });

    expect(API_ORIGIN).toBe("http://127.0.0.1:49154");
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("{}", {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    await apiFetch("/system/health");
    expect(fetchMock.mock.calls[0][0]).toBe(
      "http://127.0.0.1:49154/api/v1/system/health",
    );
    expect(
      (fetchMock.mock.calls[0][1]?.headers as Headers).get(
        "X-APEXLoad-Desktop-Token",
      ),
    ).toBe("dev-launch-secret");

    setApiOrigin(resolveApiOrigin());
    setDesktopApiCapability("");
    vi.restoreAllMocks();
  });
});

describe("dev preview API headers", () => {
  beforeEach(() => {
    window.localStorage.clear();
    window.sessionStorage.clear();
  });

  afterEach(() => {
    setDesktopApiCapability("");
    vi.restoreAllMocks();
    window.localStorage.clear();
    window.sessionStorage.clear();
  });

  it("defaults to admin mode and includes the active project id when present", () => {
    window.localStorage.setItem("apexload.activeProjectId", "project-1");

    expect(readStoredDevViewMode()).toBe("admin");
    expect(buildDevPreviewHeaders()).toEqual({
      "X-APEXLoad-Dev-View": "admin",
      "X-APEXLoad-Dev-Project-ID": "project-1",
    });
  });

  it("persists user mode for subsequent API calls", () => {
    writeStoredDevViewMode("user");

    expect(window.localStorage.getItem(DEV_VIEW_STORAGE_KEY)).toBe("user");
    expect(buildDevPreviewHeaders()["X-APEXLoad-Dev-View"]).toBe("user");
  });

  it("passes the current validated preview context to the native browser bootstrap", async () => {
    writeStoredDevViewMode("user");
    window.localStorage.setItem("apexload.activeProjectId", "project-2");
    const openInBrowser = vi.fn().mockResolvedValue(undefined);

    expect(readDevPreviewContext()).toEqual({
      mode: "user",
      projectId: "project-2",
    });
    await openDesktopBrowserWithPreview({ OpenInBrowser: openInBrowser });

    expect(openInBrowser).toHaveBeenCalledWith("user", "project-2");
  });

  it("does not transfer an invalid stored project identifier across native boundaries", async () => {
    writeStoredDevViewMode("user");
    window.localStorage.setItem(
      "apexload.activeProjectId",
      "project-a/../../secret",
    );
    const openInBrowser = vi.fn().mockResolvedValue(undefined);

    expect(buildDevPreviewHeaders()).toEqual({ "X-APEXLoad-Dev-View": "user" });
    await openDesktopBrowserWithPreview({ OpenInBrowser: openInBrowser });

    expect(openInBrowser).toHaveBeenCalledWith("user", "");
  });

  it("keeps API header construction usable when browser storage is blocked", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("Storage disabled", "SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("Storage disabled", "SecurityError");
    });

    expect(() => writeStoredDevViewMode("user")).not.toThrow();
    expect(readStoredDevViewMode()).toBe("admin");
    expect(buildDevPreviewHeaders()).toEqual({
      "X-APEXLoad-Dev-View": "admin",
    });
  });

  it("attaches preview headers to FormData and blob-style fetches without forcing JSON content type", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("{}", {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    writeStoredDevViewMode("user");
    window.localStorage.setItem("apexload.activeProjectId", "project-2");

    await apiFetch("/scripts/import/enterprise-script-archive", {
      method: "POST",
      body: new FormData(),
    });

    const init = fetchMock.mock.calls[0][1] as RequestInit;
    const headers = init.headers as Headers;
    expect(fetchMock.mock.calls[0][0]).toContain(
      "/api/v1/scripts/import/enterprise-script-archive",
    );
    expect(headers.get("X-APEXLoad-Dev-View")).toBe("user");
    expect(headers.get("X-APEXLoad-Dev-Project-ID")).toBe("project-2");
    expect(headers.has("Content-Type")).toBe(false);
    expect(init.credentials).toBe("include");
  });

  it("attaches preview headers to JSON requests", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response('{"ok":true}', {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await request<{ ok: boolean }>("/auth/me");

    const init = fetchMock.mock.calls[0][1] as RequestInit;
    const headers = init.headers as Headers;
    expect(headers.get("X-APEXLoad-Dev-View")).toBe("admin");
    expect(headers.get("Content-Type")).toBe("application/json");
    expect(init.credentials).toBe("include");
  });

  it("keeps the desktop launch capability in memory and scopes it to the controller origin", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("{}", {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    setDesktopApiCapability("launch-secret");
    expect(hasDesktopApiCapability()).toBe(true);

    await apiFetch("/system/health");
    await apiFetchFromOrigin("http://127.0.0.1:49153", "/bridge/status");
    await apiFetch("https://external.example.test/status");

    expect(
      (fetchMock.mock.calls[0][1]?.headers as Headers).get(
        "X-APEXLoad-Desktop-Token",
      ),
    ).toBe("launch-secret");
    expect(
      (fetchMock.mock.calls[1][1]?.headers as Headers).has(
        "X-APEXLoad-Desktop-Token",
      ),
    ).toBe(false);
    expect(
      (fetchMock.mock.calls[2][1]?.headers as Headers).has(
        "X-APEXLoad-Desktop-Token",
      ),
    ).toBe(false);
    expect(
      (fetchMock.mock.calls[1][1]?.headers as Headers).has(
        "X-APEXLoad-Dev-View",
      ),
    ).toBe(false);
    expect(
      (fetchMock.mock.calls[2][1]?.headers as Headers).has(
        "X-APEXLoad-Dev-View",
      ),
    ).toBe(false);
    expect(
      window.localStorage.getItem("apexload.desktopApiCapability"),
    ).toBeNull();
    const socket = new URL(
      wsUrl("/api/v1/ws?project_id=project-1", API_ORIGIN),
    );
    expect(socket.searchParams.get("desktop_token")).toBe("launch-secret");
    expect(socket.searchParams.get("project_id")).toBe("project-1");
  });

  it("starts browser authentication with a capability-bearing JSON request", async () => {
    setDesktopApiCapability("browser-launch-secret");
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        '{"redirect_url":"https://identity.example.test/authorize?state=abc"}',
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      ),
    );

    await expect(requestApiNavigation("/api/v1/auth/login")).resolves.toBe(
      "https://identity.example.test/authorize?state=abc",
    );
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    const headers = init.headers as Headers;
    expect(fetchMock.mock.calls[0][0]).toBe(`${API_ORIGIN}/api/v1/auth/login`);
    expect(headers.get("Accept")).toBe("application/json");
    expect(headers.get("X-APEXLoad-Desktop-Token")).toBe(
      "browser-launch-secret",
    );
    expect(init.credentials).toBe("include");
  });

  it("rejects off-controller auth start URLs and unsafe redirect schemes", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch");
    await expect(
      requestApiNavigation("https://other.example.test/login"),
    ).rejects.toThrow("active controller");
    expect(fetchMock).not.toHaveBeenCalled();

    fetchMock.mockResolvedValue(
      new Response('{"redirect_url":"javascript:alert(1)"}', {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    await expect(requestApiNavigation("/api/v1/auth/login")).rejects.toThrow(
      "unsupported",
    );
  });

  it("loads the system-browser bootstrap capability from origin-scoped session storage", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("{}", {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    window.sessionStorage.setItem(
      DESKTOP_CAPABILITY_SESSION_KEY,
      "browser-launch-secret",
    );

    await initializeDesktopApiOrigin();
    await apiFetch("/system/health");

    const headers = fetchMock.mock.calls[0][1]?.headers as Headers;
    expect(headers.get("X-APEXLoad-Desktop-Token")).toBe(
      "browser-launch-secret",
    );
    expect(window.sessionStorage.getItem(DESKTOP_CAPABILITY_SESSION_KEY)).toBe(
      "browser-launch-secret",
    );
    expect(new URL(wsUrl("/api/v1/ws")).searchParams.get("desktop_token")).toBe(
      "browser-launch-secret",
    );
  });

  it("can send JSON requests to an alternate API origin", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response('{"ok":true}', {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await requestFromOrigin<{ ok: boolean }>(
      "http://127.0.0.1:49152",
      "/bridge/status",
    );

    expect(fetchMock.mock.calls[0][0]).toBe(
      "http://127.0.0.1:49152/api/v1/bridge/status",
    );
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    const headers = init.headers as Headers;
    expect(headers.get("Content-Type")).toBe("application/json");
    expect(headers.has("X-APEXLoad-Dev-View")).toBe(false);
    expect(headers.has("X-APEXLoad-Dev-Project-ID")).toBe(false);
    expect(init.credentials).toBe("include");
  });

  it.each([204, 205])(
    "accepts a successful %i response without a JSON body",
    async (status) => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(
        new Response(null, { status }),
      );

      await expect(
        request<void>("/library/remotes/remote-1", { method: "DELETE" }),
      ).resolves.toBeUndefined();
    },
  );

  it("accepts an empty successful response even when the endpoint declares JSON", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("", {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await expect(
      request<void>("/library/remotes/remote-1", { method: "DELETE" }),
    ).resolves.toBeUndefined();
  });

  it("reports dashboard HTML fallbacks as unavailable API routes without echoing markup", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        "<!DOCTYPE html><html><head><title>APEX Load</title></head></html>",
        {
          status: 200,
          headers: { "Content-Type": "text/html; charset=utf-8" },
        },
      ),
    );

    let message = "";
    try {
      await request("/library/remotes");
    } catch (error) {
      message = error instanceof Error ? error.message : String(error);
    }

    expect(message).toContain("backend route may be unavailable");
    expect(message).not.toContain("<!DOCTYPE html>");
  });
});
