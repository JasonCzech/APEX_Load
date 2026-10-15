// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  acceptToFiltersJson,
  canSaveApiResourceViaBridge,
  canUseNativeDialogs,
  fileFromBase64,
  fileNameFromPath,
  filesFromDroppedEntries,
  filesFromDroppedPaths,
  pickImportFilesViaBridge,
  NativeFileReadError,
  nativeFileReadErrorMessage,
  pickFilesViaBridge,
  saveApiResourceViaBridge,
  startDesktopBrowserDownload,
  shouldUseBrowserFileFallback,
} from "./desktopFiles";
import type { WailsDesktopBridge } from "./desktopBridge";
import { setDesktopApiCapability } from "../hooks/apiClient";

type BridgeWindow = typeof window & {
  go?: { main?: { App?: WailsDesktopBridge } };
};

function setBridge(bridge: WailsDesktopBridge | undefined) {
  const w = window as BridgeWindow;
  if (bridge) {
    w.go = { main: { App: bridge } };
  } else {
    delete w.go;
  }
}

afterEach(() => {
  setBridge(undefined);
  setDesktopApiCapability("");
  window.localStorage.clear();
  vi.restoreAllMocks();
});

describe("fileNameFromPath", () => {
  it("handles POSIX and Windows separators", () => {
    expect(fileNameFromPath("/Users/x/order-flow.json")).toBe(
      "order-flow.json",
    );
    expect(fileNameFromPath("C:\\Users\\x\\archive.zip")).toBe("archive.zip");
    expect(fileNameFromPath("bare.json")).toBe("bare.json");
  });
});

describe("acceptToFiltersJson", () => {
  it("builds a semicolon pattern from extension tokens", () => {
    expect(acceptToFiltersJson(".zip")).toBe(
      JSON.stringify([{ displayName: "Supported files", pattern: "*.zip" }]),
    );
    expect(acceptToFiltersJson(".har,.json,application/json")).toBe(
      JSON.stringify([
        { displayName: "Supported files", pattern: "*.har;*.json" },
      ]),
    );
  });

  it("returns empty when no extension tokens are present", () => {
    expect(acceptToFiltersJson("")).toBe("");
    expect(acceptToFiltersJson("application/json")).toBe("");
  });
});

describe("fileFromBase64", () => {
  it("reconstructs a File with the path basename and decoded content", async () => {
    const base64 = btoa('{"name":"x"}');
    const file = fileFromBase64("/tmp/order.json", base64);
    expect(file.name).toBe("order.json");
    expect(await file.text()).toBe('{"name":"x"}');
  });
});

describe("canUseNativeDialogs", () => {
  it("reflects the bound dialog method", () => {
    expect(canUseNativeDialogs()).toBe(false);
    setBridge({ OpenFileDialogNative: vi.fn() });
    expect(canUseNativeDialogs()).toBe(true);
  });
});

describe("pickFilesViaBridge", () => {
  it("returns [] when the bridge is unavailable", async () => {
    expect(await pickFilesViaBridge(".zip")).toEqual([]);
  });

  it("reads a single picked file through the bridge", async () => {
    setBridge({
      OpenFileDialogNative: vi.fn().mockResolvedValue("/tmp/order.json"),
      ReadFileBase64: vi.fn().mockResolvedValue(btoa("hello")),
    });
    const files = await pickFilesViaBridge(".json");
    expect(files).toHaveLength(1);
    expect(files[0].name).toBe("order.json");
    expect(await files[0].text()).toBe("hello");
  });

  it("returns [] when the user cancels (empty path)", async () => {
    setBridge({
      OpenFileDialogNative: vi.fn().mockResolvedValue(""),
      ReadFileBase64: vi.fn(),
    });
    expect(await pickFilesViaBridge(".json")).toEqual([]);
  });

  it("returns a structured large-file error with an explicit browser fallback", async () => {
    setBridge({
      OpenFileDialogNative: vi.fn().mockResolvedValue("/tmp/large.zip"),
      ReadFileBase64: vi
        .fn()
        .mockRejectedValue(
          new Error("file is too large for the bridge; use the upload flow"),
        ),
    });

    const error = await pickFilesViaBridge(".zip").catch((value) => value);
    expect(error).toBeInstanceOf(NativeFileReadError);
    expect(error.code).toBe("too_large");
    expect(shouldUseBrowserFileFallback(error)).toBe(true);
    expect(nativeFileReadErrorMessage(error)).toContain(
      "Use the browser file chooser",
    );
  });
});

describe("filesFromDroppedPaths", () => {
  it("reads supported import extensions, ignoring unrelated files", async () => {
    const read = vi.fn().mockResolvedValue(btoa("x"));
    setBridge({ ReadFileBase64: read });
    const files = await filesFromDroppedPaths([
      "/tmp/order.json",
      "/tmp/plan.jmx",
      "/tmp/service.wsdl",
      "/tmp/openapi.yaml",
      "/tmp/flow.js",
      "/etc/passwd",
      "/tmp/secrets.env",
      "/tmp/archive.ZIP",
      "/tmp/orders.bruno.tar.gz",
      "/tmp/backup.tar.gz",
    ]);
    expect(files.map((f) => f.name).sort()).toEqual([
      "archive.ZIP",
      "flow.js",
      "openapi.yaml",
      "order.json",
      "orders.bruno.tar.gz",
      "plan.jmx",
      "service.wsdl",
    ]);
    expect(read).toHaveBeenCalledTimes(7);
    expect(read).not.toHaveBeenCalledWith("/etc/passwd");
  });

  it("uses the grant-waiting drop bridge when available", async () => {
    const legacyRead = vi.fn().mockRejectedValue(new Error("grant race"));
    const droppedRead = vi.fn().mockResolvedValue(btoa("drop"));
    setBridge({
      ReadFileBase64: legacyRead,
      ReadDroppedFileBase64: droppedRead,
    });

    const files = await filesFromDroppedPaths(["/tmp/order.json"]);

    expect(files).toHaveLength(1);
    expect(await files[0].text()).toBe("drop");
    expect(droppedRead).toHaveBeenCalledWith("/tmp/order.json");
    expect(legacyRead).not.toHaveBeenCalled();
  });
});

describe("pickImportFilesViaBridge", () => {
  // File > Open Script…: a native multi-select Open dialog over the import
  // types, by their last extension (what a dialog filter can match).
  it("opens the import file types and reads what the person picks", async () => {
    const open = vi.fn(async () => ["/Users/me/orders.har", "/Users/me/flow.jmx"]);
    const read = vi.fn(async () => btoa("x"));
    setBridge({ OpenFileDialogNative: vi.fn(), OpenFilesDialogNative: open, ReadFileBase64: read });

    const files = await pickImportFilesViaBridge();

    expect(files?.map((file) => file.name)).toEqual(["orders.har", "flow.jmx"]);
    const filters = JSON.parse((open.mock.calls[0] as unknown as [string, string])[1]) as Array<{ pattern: string }>;
    const patterns = filters[0].pattern.split(";");
    expect(patterns).toEqual(expect.arrayContaining(["*.har", "*.zip", "*.usz", "*.json", "*.bru"]));
    expect(patterns.every((pattern) => pattern.lastIndexOf(".") === 1)).toBe(true);
  });

  it("returns [] on cancel and null without a native dialog", async () => {
    setBridge({ OpenFileDialogNative: vi.fn(), OpenFilesDialogNative: vi.fn(async () => []) });
    expect(await pickImportFilesViaBridge()).toEqual([]);
    setBridge({});
    expect(await pickImportFilesViaBridge()).toBeNull();
  });
});

describe("filesFromDroppedEntries", () => {
  it("reads a dropped folder like a folder pick and skips items the shell did not grant", async () => {
    const readDirectory = vi.fn(async (path: string) => {
      if (path === "/Users/me/Orders Collection") {
        return JSON.stringify({
          files: [
            { path: "Orders Collection/opencollection.yml", base64: btoa("opencollection: 1.0.0") },
            { path: "Orders Collection/orders/get-order.yml", base64: btoa("info: {}") },
          ],
        });
      }
      throw "native drop access was not granted for " + path;
    });
    const readFile = vi.fn(async () => btoa("meta {}"));
    setBridge({ ReadDroppedDirectory: readDirectory, ReadDroppedFileBase64: readFile });

    const files = await filesFromDroppedEntries([
      "/Users/me/Orders Collection",
      "/Users/me/request.bru",
      "/Users/me/notes.docx",
    ]);

    expect(files.map((file) => file.webkitRelativePath || file.name)).toEqual([
      "Orders Collection/opencollection.yml",
      "Orders Collection/orders/get-order.yml",
      "request.bru",
    ]);
    expect(await files[1].text()).toBe("info: {}");
    expect(readFile).toHaveBeenCalledWith("/Users/me/request.bru");
  });

  it("reads archives only where a drop zone asks for them, and a folder named like a file type", async () => {
    const readFile = vi.fn(async (path: string) => {
      if (path.endsWith("orders.yml")) throw "native drop access was not granted for " + path;
      return btoa("archive");
    });
    const readDirectory = vi.fn(async (path: string) => {
      if (path.endsWith("orders.yml")) {
        return JSON.stringify({ files: [{ path: "orders.yml/opencollection.yml", base64: btoa("opencollection: 1.0.0") }] });
      }
      throw "native drop access was not granted for " + path;
    });
    setBridge({ ReadDroppedFileBase64: readFile, ReadDroppedDirectory: readDirectory });

    expect(await filesFromDroppedEntries(["/Users/me/backup.tgz"])).toEqual([]);
    const withArchives = await filesFromDroppedEntries(["/Users/me/backup.tgz", "/Users/me/orders.yml"], [".tgz"]);
    expect(withArchives.map((file) => file.webkitRelativePath || file.name)).toEqual([
      "backup.tgz",
      "orders.yml/opencollection.yml",
    ]);
  });

  it("reports a folder the shell refused to read", async () => {
    setBridge({
      ReadDroppedDirectory: vi.fn().mockRejectedValue(new Error("dropped folder is too large: home has more than 5000 files")),
    });
    await expect(filesFromDroppedEntries(["/Users/me"])).rejects.toMatchObject({ code: "too_large" });
  });
});

describe("saveApiResourceViaBridge", () => {
  it("reports unavailable in the browser", async () => {
    expect(canSaveApiResourceViaBridge()).toBe(false);
    await expect(
      saveApiResourceViaBridge("/api/v1/tests/run-1/archive", "run-1.zip"),
    ).resolves.toBe("unavailable");
  });

  it("passes a same-controller path and native file filter to the streaming bridge", async () => {
    const save = vi.fn().mockResolvedValue("/tmp/run-1.zip");
    setBridge({ SaveAPIResourceNative: save });
    window.localStorage.setItem("apexload.devViewMode", "user");
    window.localStorage.setItem("apexload.activeProjectId", "project-b");

    expect(canSaveApiResourceViaBridge()).toBe(true);
    await expect(
      saveApiResourceViaBridge("/api/v1/tests/run-1/archive", "run-1.zip"),
    ).resolves.toBe("saved");
    expect(save).toHaveBeenCalledWith(
      "/api/v1/tests/run-1/archive",
      "run-1.zip",
      JSON.stringify([{ displayName: "ZIP file", pattern: "*.zip" }]),
      "user",
      "project-b",
    );
  });

  it("drops an invalid stored project rather than changing the native request scope", async () => {
    const save = vi.fn().mockResolvedValue("/tmp/run-1.zip");
    setBridge({ SaveAPIResourceNative: save });
    window.localStorage.setItem("apexload.devViewMode", "user");
    window.localStorage.setItem(
      "apexload.activeProjectId",
      "project-b/../project-a",
    );

    await expect(
      saveApiResourceViaBridge("/api/v1/tests/run-1/archive", "run-1.zip"),
    ).resolves.toBe("saved");

    expect(save).toHaveBeenCalledWith(
      "/api/v1/tests/run-1/archive",
      "run-1.zip",
      JSON.stringify([{ displayName: "ZIP file", pattern: "*.zip" }]),
      "user",
      "",
    );
  });

  it("preserves an unscoped user preview without inventing a default project", async () => {
    const save = vi.fn().mockResolvedValue("/tmp/run-1.zip");
    setBridge({ SaveAPIResourceNative: save });
    window.localStorage.setItem("apexload.devViewMode", "user");

    await expect(
      saveApiResourceViaBridge("/api/v1/tests/run-1/archive", "run-1.zip"),
    ).resolves.toBe("saved");

    expect(save).toHaveBeenCalledWith(
      "/api/v1/tests/run-1/archive",
      "run-1.zip",
      JSON.stringify([{ displayName: "ZIP file", pattern: "*.zip" }]),
      "user",
      "",
    );
  });

  it("reports a cancelled native save without treating it as success", async () => {
    setBridge({ SaveAPIResourceNative: vi.fn().mockResolvedValue("") });

    await expect(
      saveApiResourceViaBridge("/api/v1/tests/run-1/archive", "run-1.zip"),
    ).resolves.toBe("cancelled");
  });

  it("propagates native download failures instead of falling back", async () => {
    const error = new Error("disk is full");
    setBridge({ SaveAPIResourceNative: vi.fn().mockRejectedValue(error) });

    await expect(
      saveApiResourceViaBridge("/api/v1/tests/run-1/archive", "run-1.zip"),
    ).rejects.toBe(error);
  });
});

describe("startDesktopBrowserDownload", () => {
  it("mints a capability-bearing URL and starts a browser download without buffering the resource", async () => {
    setDesktopApiCapability("browser-session-secret");
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(
        new Response(
          JSON.stringify({ url: "/desktop/download?token=one-time" }),
          { status: 201, headers: { "Content-Type": "application/json" } },
        ),
      );
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => undefined);
    let clickedHref = "";
    let clickedDownload = "";
    let clickedWhileConnected = false;
    click.mockImplementation(function (this: HTMLAnchorElement) {
      clickedHref = this.href;
      clickedDownload = this.download;
      clickedWhileConnected = this.isConnected;
    });

    await startDesktopBrowserDownload(
      "/api/v1/tests/run-1/archive",
      "run-1.zip",
    );

    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("http://localhost:3000/api/v1/desktop/browser-downloads");
    expect(init?.method).toBe("POST");
    expect((init?.headers as Headers).get("X-APEXLoad-Desktop-Token")).toBe(
      "browser-session-secret",
    );
    expect(init?.body).toBe(
      JSON.stringify({
        resourcePath: "/api/v1/tests/run-1/archive",
        mode: "download",
      }),
    );
    expect(clickedHref).toBe(
      "http://localhost:3000/desktop/download?token=one-time",
    );
    expect(clickedDownload).toBe("");
    expect(clickedWhileConnected).toBe(true);
    expect(document.querySelector('a[href*="token=one-time"]')).toBeNull();
  });

  it("reserves a report tab before minting and rejects cross-origin navigation URLs", async () => {
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => undefined);
    const replace = vi.fn();
    const close = vi.fn();
    const clear = vi.fn();
    const reportWindow = {
      opener: window,
      location: { replace },
      close,
      sessionStorage: { clear },
    } as unknown as Window;
    const open = vi.spyOn(window, "open").mockReturnValue(reportWindow);
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ url: "/desktop/download?token=report" }),
          { status: 200 },
        ),
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ url: "https://attacker.example/download" }),
          { status: 200 },
        ),
      );

    const pending = startDesktopBrowserDownload(
      "/api/v1/tests/run-1/report",
      "run-1.html",
      "open",
    );
    expect(open).toHaveBeenCalledWith("", "_blank");
    expect(
      (reportWindow as unknown as { opener: Window | null }).opener,
    ).toBeNull();
    expect(clear).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await pending;

    expect(replace).toHaveBeenCalledWith(
      "http://localhost:3000/desktop/download?token=report",
    );
    expect(close).not.toHaveBeenCalled();
    await expect(
      startDesktopBrowserDownload("/api/v1/tests/run-1/archive", "run-1.zip"),
    ).rejects.toThrow("unsafe download URL");
    expect(click).not.toHaveBeenCalled();
  });

  it("fails closed before minting a report URL when the popup is blocked", async () => {
    vi.spyOn(window, "open").mockReturnValue(null);
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(
        new Response(
          JSON.stringify({ url: "/desktop/download?token=report" }),
          { status: 200 },
        ),
      );
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => undefined);

    await expect(
      startDesktopBrowserDownload(
        "/api/v1/tests/run-1/report",
        "run-1.html",
        "open",
      ),
    ).rejects.toThrow("Allow pop-ups");

    expect(fetchMock).not.toHaveBeenCalled();
    expect(click).not.toHaveBeenCalled();
  });

  it("closes a reserved report tab and fails before minting when inherited session storage cannot be cleared", async () => {
    const close = vi.fn();
    const reportWindow = {
      opener: window,
      close,
      location: { replace: vi.fn() },
    };
    Object.defineProperty(reportWindow, "sessionStorage", {
      get: () => {
        throw new DOMException("storage blocked", "SecurityError");
      },
    });
    vi.spyOn(window, "open").mockReturnValue(reportWindow as unknown as Window);
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(
        new Response(
          JSON.stringify({ url: "/desktop/download?token=report" }),
          { status: 200 },
        ),
      );

    await expect(
      startDesktopBrowserDownload(
        "/api/v1/tests/run-1/report",
        "run-1.html",
        "open",
      ),
    ).rejects.toThrow("Allow pop-ups");

    expect(close).toHaveBeenCalledTimes(1);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("closes the isolated report tab without same-tab fallback when navigation fails", async () => {
    const close = vi.fn();
    const reportWindow = {
      opener: window,
      close,
      sessionStorage: { clear: vi.fn() },
      location: {
        replace: vi.fn(() => {
          throw new DOMException("navigation blocked", "SecurityError");
        }),
      },
    };
    vi.spyOn(window, "open").mockReturnValue(reportWindow as unknown as Window);
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ url: "/desktop/download?token=report" }), {
        status: 200,
      }),
    );
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => undefined);

    await expect(
      startDesktopBrowserDownload(
        "/api/v1/tests/run-1/report",
        "run-1.html",
        "open",
      ),
    ).rejects.toThrow("Allow pop-ups");

    expect(close).toHaveBeenCalledTimes(1);
    expect(click).not.toHaveBeenCalled();
  });
});
