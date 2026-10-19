// @vitest-environment jsdom

import { act } from "react";
import { createRoot, Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import ClientDownloadsMenu from "./ClientDownloadsMenu";

const getVersionMock = vi.fn();
const apiFetchMock = vi.fn();
const downloadBlobMock = vi.fn();
const canSaveApiResourceViaBridgeMock = vi.fn();
const saveApiResourceViaBridgeMock = vi.fn();
const startDesktopBrowserDownloadMock = vi.fn();
const hasDesktopApiCapabilityMock = vi.fn();

vi.mock("../hooks/api/useSystemApi", () => ({
  useSystemApi: () => ({ getVersion: getVersionMock }),
}));

vi.mock("../hooks/apiClient", () => ({
  apiFetch: (...args: unknown[]) => apiFetchMock(...args),
  hasDesktopApiCapability: () => hasDesktopApiCapabilityMock(),
}));

vi.mock("../desktop/desktopFiles", () => ({
  canSaveApiResourceViaBridge: () => canSaveApiResourceViaBridgeMock(),
  saveApiResourceViaBridge: (...args: unknown[]) => saveApiResourceViaBridgeMock(...args),
  startDesktopBrowserDownload: (...args: unknown[]) => startDesktopBrowserDownloadMock(...args),
}));

vi.mock("../utils/fileUtils", () => ({
  downloadBlob: (...args: unknown[]) => downloadBlobMock(...args),
  downloadFilenameFromDisposition: (
    _disposition: string | null,
    fallback: string,
  ) => fallback,
}));

(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true;

function deploymentVersion(clientDownloadsEnabled: boolean) {
  return {
    version: "dev",
    environment: "docker",
    deployment: {
      kind: "docker",
      label: "Docker",
      bind_scope: "loopback",
      auth_enforced: false,
      desktop_available: false,
      client_downloads_enabled: clientDownloadsEnabled,
    },
  };
}

function okPackageResponse() {
  return {
    ok: true,
    headers: {
      get: (key: string) =>
        key.toLowerCase() === "content-type"
          ? "application/gzip"
          : 'attachment; filename="apexload-recording-agent-0.1.0-darwin-arm64.tar.gz"',
    },
    blob: async () => new Blob(["package"]),
  };
}

const flush = () =>
  act(async () => {
    await Promise.resolve();
  });

describe("ClientDownloadsMenu", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    getVersionMock.mockReset();
    apiFetchMock.mockReset();
    downloadBlobMock.mockReset();
    canSaveApiResourceViaBridgeMock.mockReset();
    canSaveApiResourceViaBridgeMock.mockReturnValue(false);
    saveApiResourceViaBridgeMock.mockReset();
    startDesktopBrowserDownloadMock.mockReset();
    startDesktopBrowserDownloadMock.mockResolvedValue(undefined);
    hasDesktopApiCapabilityMock.mockReset();
    hasDesktopApiCapabilityMock.mockReturnValue(false);
    // Force a deterministic macOS platform detection.
    Object.defineProperty(window.navigator, "userAgent", {
      value: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)",
      configurable: true,
    });
    Object.defineProperty(window.navigator, "platform", {
      value: "MacIntel",
      configurable: true,
    });
    Object.defineProperty(window.navigator, "userAgentData", {
      value: {
        platform: "macOS",
        getHighEntropyValues: vi
          .fn()
          .mockResolvedValue({
            platform: "macOS",
            architecture: "arm",
            bitness: "64",
          }),
      },
      configurable: true,
    });
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
  });

  const render = async () => {
    await act(async () => {
      root.render(<ClientDownloadsMenu />);
    });
    await flush();
  };

  const openPanel = async () => {
    const trigger = container.querySelector(
      ".client-downloads-trigger",
    ) as HTMLButtonElement;
    await act(async () => {
      trigger.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
  };

  it("renders nothing when client downloads are disabled", async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(false));
    await render();
    expect(container.querySelector(".client-downloads-trigger")).toBeNull();
    expect(container.textContent).not.toContain("Download client");
  });

  it("shows the trigger and lists both artifacts when enabled", async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(true));
    await render();

    expect(container.querySelector(".client-downloads-trigger")).not.toBeNull();
    expect(container.querySelector(".client-downloads-panel")).toBeNull();

    await openPanel();

    expect(container.querySelector(".client-downloads-panel")).not.toBeNull();
    expect(container.textContent).toContain("Recording Agent");
    expect(container.textContent).toContain("Desktop App");
    expect(container.textContent).toContain("Download for macOS (Apple Silicon)");
  });

  it("raises the topbar above the sticky sidebar while the menu is open", async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import("node:fs");
    const css = readFileSync(
      "src/components/ClientDownloadsMenu.css",
      "utf8",
    ) as string;

    const rule =
      css.match(
        /\.main-topbar:has\(\.client-downloads-panel\) \{([\s\S]*?)\n\}/,
      )?.[1] || "";
    const panelRule =
      css.match(/\.client-downloads-panel \{([\s\S]*?)\n\}/)?.[1] || "";
    expect(rule).toContain("z-index: 140;");
    expect(panelRule).toContain("left: 0;");
    expect(panelRule).toContain("right: auto;");
  });

  it("downloads the recording agent for the detected platform", async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(true));
    apiFetchMock.mockResolvedValue(okPackageResponse());
    await render();
    await openPanel();

    const primary = container
      .querySelectorAll(".client-downloads-item")[0]
      .querySelector(".btn-primary") as HTMLButtonElement;
    await act(async () => {
      primary.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();

    expect(apiFetchMock).toHaveBeenCalledWith(
      "/downloads/recording-agent/latest?platform=darwin-arm64",
    );
    expect(downloadBlobMock).toHaveBeenCalledTimes(1);
    expect(container.textContent).toContain("Recording Agent download started");
  });

  it("does not report a browser download as started when its native dialog is cancelled", async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(true));
    apiFetchMock.mockResolvedValue(okPackageResponse());
    downloadBlobMock.mockResolvedValue('cancelled');
    await render();
    await openPanel();

    const primary = container
      .querySelectorAll(".client-downloads-item")[0]
      .querySelector(".btn-primary") as HTMLButtonElement;
    await act(async () => {
      primary.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();

    expect(downloadBlobMock).toHaveBeenCalledTimes(1);
    expect(container.textContent).not.toContain("Recording Agent download started");
  });

  it("streams packages without Blob buffering in the desktop bootstrap browser", async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(true));
    hasDesktopApiCapabilityMock.mockReturnValue(true);
    await render();
    await openPanel();

    const primary = container
      .querySelectorAll(".client-downloads-item")[0]
      .querySelector(".btn-primary") as HTMLButtonElement;
    await act(async () => {
      primary.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();

    expect(startDesktopBrowserDownloadMock).toHaveBeenCalledWith(
      "/api/v1/downloads/recording-agent/latest?platform=darwin-arm64",
      "apexload-recording-agent.tar.gz",
    );
    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(downloadBlobMock).not.toHaveBeenCalled();
    expect(container.textContent).toContain("Recording Agent download request opened");
  });

  it("streams a desktop package through the native save bridge", async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(true));
    canSaveApiResourceViaBridgeMock.mockReturnValue(true);
    saveApiResourceViaBridgeMock.mockResolvedValue('saved');
    apiFetchMock.mockResolvedValue({
      ok: true,
      json: async () => ({ filename: "apexload-desktop-0.1.0-darwin-arm64.tar.gz" }),
    });
    await render();
    await openPanel();

    const desktopPrimary = container
      .querySelectorAll(".client-downloads-item")[1]
      .querySelector(".btn-primary") as HTMLButtonElement;
    await act(async () => {
      desktopPrimary.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();

    expect(apiFetchMock).toHaveBeenCalledWith(
      "/downloads/desktop/latest/checksum?platform=darwin-arm64",
    );
    expect(saveApiResourceViaBridgeMock).toHaveBeenCalledWith(
      "/api/v1/downloads/desktop/latest?platform=darwin-arm64",
      "apexload-desktop-0.1.0-darwin-arm64.tar.gz",
    );
    expect(downloadBlobMock).not.toHaveBeenCalled();
    expect(container.textContent).toContain("Desktop App saved");
  });

  it("does not report success or fetch a browser blob when the native save is cancelled", async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(true));
    canSaveApiResourceViaBridgeMock.mockReturnValue(true);
    saveApiResourceViaBridgeMock.mockResolvedValue('cancelled');
    apiFetchMock.mockResolvedValue({
      ok: true,
      json: async () => ({ filename: "apexload-desktop-0.1.0-darwin-arm64.tar.gz" }),
    });
    await render();
    await openPanel();

    const desktopPrimary = container
      .querySelectorAll(".client-downloads-item")[1]
      .querySelector(".btn-primary") as HTMLButtonElement;
    await act(async () => {
      desktopPrimary.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();

    expect(saveApiResourceViaBridgeMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledTimes(1);
    expect(downloadBlobMock).not.toHaveBeenCalled();
    expect(container.textContent).not.toContain("Desktop App saved");
  });

  it("surfaces a native save failure without falling back to a browser blob", async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(true));
    canSaveApiResourceViaBridgeMock.mockReturnValue(true);
    saveApiResourceViaBridgeMock.mockRejectedValue(new Error('disk is full'));
    apiFetchMock.mockResolvedValue({
      ok: true,
      json: async () => ({ filename: "apexload-desktop-0.1.0-darwin-arm64.tar.gz" }),
    });
    await render();
    await openPanel();

    const desktopPrimary = container
      .querySelectorAll(".client-downloads-item")[1]
      .querySelector(".btn-primary") as HTMLButtonElement;
    await act(async () => {
      desktopPrimary.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();

    expect(downloadBlobMock).not.toHaveBeenCalled();
    expect(container.querySelector(".client-downloads-status.error")?.textContent).toContain('disk is full');
  });

  it("surfaces a server error when a package is unavailable", async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(true));
    apiFetchMock.mockResolvedValue({
      ok: false,
      headers: { get: () => null },
      json: async () => ({
        error: "APEX Load Desktop package for darwin-arm64 is not staged.",
      }),
    });
    await render();
    await openPanel();

    const desktopPrimary = container
      .querySelectorAll(".client-downloads-item")[1]
      .querySelector(".btn-primary") as HTMLButtonElement;
    await act(async () => {
      desktopPrimary.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();

    expect(downloadBlobMock).not.toHaveBeenCalled();
    expect(
      container.querySelector(".client-downloads-status.error")?.textContent,
    ).toContain("is not staged");
  });

  it("falls back to the platform picker when macOS architecture is unknown", async () => {
    Object.defineProperty(window.navigator, "userAgentData", {
      value: undefined,
      configurable: true,
    });
    getVersionMock.mockResolvedValue(deploymentVersion(true));
    await render();
    await openPanel();

    expect(container.textContent).not.toContain(
      "Download for macOS (Apple Silicon)",
    );
    expect(container.textContent).toContain("Choose a platform:");
    expect(container.textContent).toContain("macOS (Apple Silicon)");
    expect(container.textContent).not.toContain("macOS (Intel)");
    expect(container.querySelector(".client-downloads-unsupported")).toBeNull();
  });

  it("tells an Intel Mac that it has no package", async () => {
    Object.defineProperty(window.navigator, "userAgentData", {
      value: {
        platform: "macOS",
        getHighEntropyValues: vi.fn().mockResolvedValue({
          platform: "macOS",
          architecture: "x86",
          bitness: "64",
        }),
      },
      configurable: true,
    });
    getVersionMock.mockResolvedValue(deploymentVersion(true));
    await render();
    await openPanel();

    expect(
      container.querySelector(".client-downloads-unsupported")?.textContent,
    ).toBe(
      "Intel Macs are not supported. The macOS packages run on Apple Silicon only.",
    );
    expect(container.textContent).not.toContain("Download for macOS");
    expect(container.textContent).toContain("Choose a platform:");
  });

  it("closes the panel on an outside click", async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(true));
    await render();
    await openPanel();
    expect(container.querySelector(".client-downloads-panel")).not.toBeNull();

    await act(async () => {
      document.body.dispatchEvent(
        new MouseEvent("mousedown", { bubbles: true }),
      );
    });
    expect(container.querySelector(".client-downloads-panel")).toBeNull();
  });
});
