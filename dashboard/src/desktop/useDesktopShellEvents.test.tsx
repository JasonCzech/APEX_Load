// @vitest-environment jsdom

import { act } from "react";
import { createRoot, Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  parsePendingDesktopOpenRequests,
  useDesktopShellEvents,
} from "./useDesktopShellEvents";
import type { WailsDesktopBridge } from "./desktopBridge";
import { DESKTOP_QUEUED_MENU_ZOOM_EVENT } from "./rendererEvents";
import { DesktopShellHandlerCoordinator } from "./desktopShellHandlerCoordinator";
import { openDesktopBrowserWithPreview } from "../hooks/apiClient";

(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true;

type DesktopWindow = typeof window & {
  go?: { main?: { App?: WailsDesktopBridge } };
  runtime?: {
    EventsOn?: (event: string, cb: (arg?: unknown) => void) => () => void;
  };
};

function Harness({
  onMenuAbout,
  onMenuOpenBrowser,
  onRecentsChanged,
  onMenuAction,
  onOpenFile,
  onOpenURL,
  onOpenRecent,
}: {
  onMenuAbout?: () => void | Promise<void>;
  onMenuOpenBrowser?: () => void | Promise<void>;
  onRecentsChanged?: () => void | Promise<void>;
  onMenuAction?: (action: string) => void | Promise<void>;
  onOpenFile?: (path: string) => void | Promise<void>;
  onOpenURL?: (url: string) => void | Promise<void>;
  onOpenRecent?: (route: string) => void | Promise<void>;
}) {
  useDesktopShellEvents({
    onMenuAbout,
    onMenuOpenBrowser,
    onRecentsChanged,
    onMenuAction,
    onOpenFile,
    onOpenURL,
    onOpenRecent,
  });
  return null;
}

describe("useDesktopShellEvents", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => {
      root.unmount();
    });
    container.remove();
    delete (window as DesktopWindow).go;
    delete (window as DesktopWindow).runtime;
    window.localStorage.clear();
    vi.restoreAllMocks();
  });

  it("routes external links to the system browser", async () => {
    const openURL = vi.fn().mockResolvedValue(undefined);
    (window as DesktopWindow).go = { main: { App: { OpenURL: openURL } } };

    await act(async () => {
      root.render(<Harness />);
    });

    const anchor = document.createElement("a");
    anchor.href = "https://docs.example.com/guide";
    anchor.textContent = "docs";
    container.appendChild(anchor);

    const event = new MouseEvent("click", {
      bubbles: true,
      cancelable: true,
      button: 0,
    });
    await act(async () => {
      anchor.dispatchEvent(event);
    });

    expect(openURL).toHaveBeenCalledWith("https://docs.example.com/guide");
    expect(event.defaultPrevented).toBe(true);
  });

  it("resolves protocol-relative links before routing them out of the webview", async () => {
    const openURL = vi.fn().mockResolvedValue(undefined);
    (window as DesktopWindow).go = { main: { App: { OpenURL: openURL } } };

    await act(async () => {
      root.render(<Harness />);
    });

    const anchor = document.createElement("a");
    anchor.setAttribute("href", "//attacker.example/replace-webview");
    container.appendChild(anchor);

    const event = new MouseEvent("click", {
      bubbles: true,
      cancelable: true,
      button: 0,
    });
    await act(async () => {
      anchor.dispatchEvent(event);
    });

    expect(openURL).toHaveBeenCalledWith(
      `${window.location.protocol}//attacker.example/replace-webview`,
    );
    expect(event.defaultPrevented).toBe(true);
  });

  it("ignores internal hash links", async () => {
    const openURL = vi.fn().mockResolvedValue(undefined);
    (window as DesktopWindow).go = { main: { App: { OpenURL: openURL } } };
    await act(async () => {
      root.render(<Harness />);
    });

    const anchor = document.createElement("a");
    anchor.setAttribute("href", "#/scripts");
    container.appendChild(anchor);
    await act(async () => {
      anchor.dispatchEvent(
        new MouseEvent("click", { bubbles: true, cancelable: true, button: 0 }),
      );
    });

    expect(openURL).not.toHaveBeenCalled();
  });

  it("leaves explicitly managed resource links for their authenticated React handler", async () => {
    const openURL = vi.fn().mockResolvedValue(undefined);
    (window as DesktopWindow).go = { main: { App: { OpenURL: openURL } } };
    await act(async () => {
      root.render(<Harness />);
    });

    const anchor = document.createElement("a");
    anchor.href =
      "http://127.0.0.1:49152/api/v1/downloads/recording-agent/latest?platform=linux-amd64";
    anchor.dataset.desktopManagedResource = "true";
    container.appendChild(anchor);
    const event = new MouseEvent("click", {
      bubbles: true,
      cancelable: true,
      button: 0,
    });
    await act(async () => {
      anchor.dispatchEvent(event);
    });

    expect(openURL).not.toHaveBeenCalled();
    expect(event.defaultPrevented).toBe(false);
  });

  it("routes ordinary same-controller absolute links out of the webview", async () => {
    const openURL = vi.fn().mockResolvedValue(undefined);
    (window as DesktopWindow).go = { main: { App: { OpenURL: openURL } } };
    await act(async () => {
      root.render(<Harness />);
    });

    const anchor = document.createElement("a");
    anchor.href = window.location.origin + "/api/v1/docs/swagger.yaml";
    container.appendChild(anchor);
    const event = new MouseEvent("click", {
      bubbles: true,
      cancelable: true,
      button: 0,
    });
    await act(async () => {
      anchor.dispatchEvent(event);
    });

    expect(openURL).toHaveBeenCalledWith(anchor.getAttribute("href"));
    expect(event.defaultPrevented).toBe(true);
  });

  it("retires the renderer only after navigation commits", async () => {
    const rendererUnready = vi.fn().mockResolvedValue(undefined);
    (window as DesktopWindow).go = {
      main: { App: { RendererUnready: rendererUnready } },
    };
    await act(async () => {
      root.render(<Harness />);
    });

    await act(async () => {
      window.dispatchEvent(new Event("beforeunload"));
    });

    expect(rendererUnready).not.toHaveBeenCalled();

    await act(async () => {
      window.dispatchEvent(new Event("pagehide"));
    });

    expect(rendererUnready).toHaveBeenCalledTimes(1);
    expect(rendererUnready.mock.calls[0]?.[0]).toMatch(/^renderer-/);
  });

  it("invokes onMenuAbout when the native menu emits menu:about", async () => {
    const listeners: Record<string, (arg?: unknown) => void> = {};
    (window as DesktopWindow).runtime = {
      EventsOn: (event, cb) => {
        listeners[event] = cb;
        return () => {
          delete listeners[event];
        };
      },
    };
    const onMenuAbout = vi.fn();
    await act(async () => {
      root.render(<Harness onMenuAbout={onMenuAbout} />);
    });

    await act(async () => {
      listeners["menu:about"]?.();
    });
    expect(onMenuAbout).toHaveBeenCalledTimes(1);
  });

  it("routes the native browser menu action through the renderer context handler", async () => {
    const listeners: Record<string, (arg?: unknown) => void> = {};
    (window as DesktopWindow).runtime = {
      EventsOn: (event, cb) => {
        listeners[event] = cb;
        return () => {
          delete listeners[event];
        };
      },
    };
    const openInBrowser = vi.fn().mockResolvedValue(undefined);
    (window as DesktopWindow).go = {
      main: { App: { OpenInBrowser: openInBrowser } },
    };
    window.localStorage.setItem("apexload.devViewMode", "user");
    window.localStorage.setItem("apexload.activeProjectId", "project-b");
    const onMenuOpenBrowser = () => {
      void openDesktopBrowserWithPreview(
        (window as DesktopWindow).go?.main?.App,
      );
    };
    await act(async () => {
      root.render(<Harness onMenuOpenBrowser={onMenuOpenBrowser} />);
    });

    await act(async () => {
      listeners["menu:open-browser"]?.();
    });
    expect(openInBrowser).toHaveBeenCalledWith("user", "project-b");
  });

  // View > Zoom menu clicks (and the accelerators wherever the native menu
  // takes the key before the page) reach the dashboard through this event.
  it("zooms the dashboard when the native menu emits menu:zoom", async () => {
    const listeners: Record<string, (arg?: unknown) => void> = {};
    (window as DesktopWindow).runtime = {
      EventsOn: (event, cb) => {
        listeners[event] = cb;
        return () => {
          delete listeners[event];
        };
      },
    };
    const zoomed: unknown[] = [];
    const onZoom = (event: Event) => zoomed.push((event as CustomEvent).detail);
    window.addEventListener(DESKTOP_QUEUED_MENU_ZOOM_EVENT, onZoom);
    try {
      await act(async () => {
        root.render(<Harness />);
      });
      await act(async () => {
        listeners["menu:zoom"]?.("in");
        listeners["menu:zoom"]?.("sideways");
        listeners["menu:zoom"]?.("reset");
      });
      expect(zoomed).toEqual(["in", "reset"]);
    } finally {
      window.removeEventListener(DESKTOP_QUEUED_MENU_ZOOM_EVENT, onZoom);
    }
  });

  it("invokes onRecentsChanged when the native menu mutates recents", async () => {
    const listeners: Record<string, (arg?: unknown) => void> = {};
    (window as DesktopWindow).runtime = {
      EventsOn: (event, cb) => {
        listeners[event] = cb;
        return () => {
          delete listeners[event];
        };
      },
    };
    const onRecentsChanged = vi.fn();
    await act(async () => {
      root.render(<Harness onRecentsChanged={onRecentsChanged} />);
    });

    await act(async () => {
      listeners["desktop:recents-changed"]?.();
    });
    expect(onRecentsChanged).toHaveBeenCalledTimes(1);
  });

  it("subscribes before declaring renderer readiness and drains cold-launch requests", async () => {
    const listeners: Record<string, (arg?: unknown) => void> = {};
    (window as DesktopWindow).runtime = {
      EventsOn: (event, cb) => {
        listeners[event] = cb;
        return () => {
          delete listeners[event];
        };
      },
    };
    const rendererReady = vi.fn(async (session: string) => {
      expect(session).toMatch(/^renderer-/);
      expect(listeners["desktop:open-file"]).toBeTypeOf("function");
      expect(listeners["desktop:open-url"]).toBeTypeOf("function");
      return JSON.stringify({
        delivery_id: "1",
        events: [
          { name: "desktop:open-file", payload: "/tmp/order.json" },
          { name: "desktop:open-url", payload: "apexload://results" },
          { name: "menu:about" },
          { name: "menu:open-browser" },
          { name: "menu:action", payload: "new-test" },
          { name: "menu:zoom", payload: "in" },
          { name: "desktop:open-recent", payload: "#/scripts/s1" },
          { name: "desktop:recents-changed" },
        ],
      });
    });
    const rendererReadyAck = vi.fn(async (_session: string, deliveryId: string) => {
      if (deliveryId === "1") {
        return JSON.stringify({
          delivery_id: "2",
          events: [{ name: "menu:action", payload: "import" }],
        });
      }
      return JSON.stringify({ events: [] });
    });
    (window as DesktopWindow).go = {
      main: {
        App: { RendererReady: rendererReady, RendererReadyAck: rendererReadyAck },
      },
    };
    const deliveryOrder: string[] = [];
    const onOpenFile = vi.fn(() => { deliveryOrder.push("file"); });
    const onOpenURL = vi.fn(() => { deliveryOrder.push("url"); });
    const onMenuAbout = vi.fn(() => { deliveryOrder.push("about"); });
    const onMenuOpenBrowser = vi.fn(() => { deliveryOrder.push("browser"); });
    const onMenuAction = vi.fn((action: string) => {
      deliveryOrder.push(`action:${action}`);
    });
    const onOpenRecent = vi.fn(() => { deliveryOrder.push("recent"); });
    const onRecentsChanged = vi.fn(() => { deliveryOrder.push("recents-changed"); });
    const queuedZoom = vi.fn<(event: Event) => void>(() => { deliveryOrder.push("zoom"); });
    window.addEventListener(DESKTOP_QUEUED_MENU_ZOOM_EVENT, queuedZoom);

    try {
      await act(async () => {
        root.render(
          <Harness
            onMenuAbout={onMenuAbout}
            onMenuOpenBrowser={onMenuOpenBrowser}
            onMenuAction={onMenuAction}
            onOpenFile={onOpenFile}
            onOpenURL={onOpenURL}
            onOpenRecent={onOpenRecent}
            onRecentsChanged={onRecentsChanged}
          />,
        );
      });

      expect(rendererReady).toHaveBeenCalledTimes(1);
      const rendererSession = rendererReady.mock.calls[0]?.[0];
      expect(rendererSession).toMatch(/^renderer-/);
      expect(onOpenFile).toHaveBeenCalledWith("/tmp/order.json");
      expect(onOpenURL).toHaveBeenCalledWith("apexload://results");
      expect(onMenuAbout).toHaveBeenCalledTimes(1);
      expect(onMenuOpenBrowser).toHaveBeenCalledTimes(1);
      expect(onMenuAction.mock.calls).toEqual([["new-test"], ["import"]]);
      expect(queuedZoom).toHaveBeenCalledTimes(1);
      expect(
        (queuedZoom.mock.calls[0]?.[0] as CustomEvent<unknown>).detail,
      ).toBe("in");
      expect(onOpenRecent).toHaveBeenCalledWith("#/scripts/s1");
      expect(onRecentsChanged).toHaveBeenCalledTimes(1);
      expect(rendererReadyAck.mock.calls).toEqual([
        [rendererSession, "1"],
        [rendererSession, "2"],
      ]);
      expect(deliveryOrder).toEqual([
        "file",
        "url",
        "about",
        "browser",
        "action:new-test",
        "zoom",
        "recent",
        "recents-changed",
        "action:import",
      ]);
    } finally {
      window.removeEventListener(DESKTOP_QUEUED_MENU_ZOOM_EVENT, queuedZoom);
    }
  });

  it("does not acknowledge a workspace delivery until AppShell registers its handler", async () => {
    const coordinator = new DesktopShellHandlerCoordinator();
    const rendererReady = vi.fn().mockResolvedValue(
      JSON.stringify({
        delivery_id: "1",
        events: [{ name: "desktop:open-file", payload: "/tmp/gated.har" }],
      }),
    );
    const rendererReadyAck = vi
      .fn()
      .mockResolvedValue(JSON.stringify({ events: [] }));
    (window as DesktopWindow).go = {
      main: {
        App: { RendererReady: rendererReady, RendererReadyAck: rendererReadyAck },
      },
    };

    await act(async () => {
      root.render(<Harness onOpenFile={coordinator.dispatchOpenFile} />);
      await Promise.resolve();
    });

    expect(rendererReady).toHaveBeenCalledTimes(1);
    expect(rendererReadyAck).not.toHaveBeenCalled();

    const onOpenFile = vi.fn().mockResolvedValue(undefined);
    await act(async () => {
      coordinator.setWorkspaceHandlers({ onOpenFile });
      await Promise.resolve();
    });

    expect(onOpenFile).toHaveBeenCalledWith("/tmp/gated.har");
    expect(rendererReadyAck).toHaveBeenCalledWith(
      rendererReady.mock.calls[0]?.[0],
      "1",
    );
  });

  it("keeps gated Help live when a workspace-only menu action precedes it", async () => {
    const coordinator = new DesktopShellHandlerCoordinator();
    const onMenuAction = vi.fn();
    const onMenuOpenBrowser = vi.fn().mockResolvedValue(undefined);
    coordinator.setGatedHandlers({ onMenuAction, onMenuOpenBrowser });
    const rendererReady = vi.fn().mockResolvedValue(
      JSON.stringify({
        delivery_id: "1",
        events: [
          { name: "menu:action", payload: "new-test" },
          { name: "menu:open-browser" },
        ],
      }),
    );
    const rendererReadyAck = vi
      .fn()
      .mockResolvedValue(JSON.stringify({ events: [] }));
    (window as DesktopWindow).go = {
      main: {
        App: { RendererReady: rendererReady, RendererReadyAck: rendererReadyAck },
      },
    };

    await act(async () => {
      root.render(
        <Harness
          onMenuAction={coordinator.dispatchMenuAction}
          onMenuOpenBrowser={coordinator.dispatchMenuOpenBrowser}
        />,
      );
      await Promise.resolve();
    });

    expect(onMenuAction).toHaveBeenCalledWith("new-test");
    expect(onMenuOpenBrowser).toHaveBeenCalledTimes(1);
    expect(rendererReadyAck).toHaveBeenCalledWith(
      rendererReady.mock.calls[0]?.[0],
      "1",
    );
  });

  it("rejects unknown or malformed native events from the readiness payload", () => {
    expect(
      parsePendingDesktopOpenRequests(
        JSON.stringify({
          events: [
            { name: "desktop:open-file", payload: "" },
            { name: "desktop:open-file", payload: "/tmp/valid.har" },
            { name: "desktop:open-url", payload: "https://attacker.example" },
            { name: "desktop:open-url", payload: "apexload://run/1" },
            { name: "menu:open-browser", payload: "unexpected" },
            { name: "menu:action", payload: "delete-everything" },
            { name: "menu:zoom", payload: "sideways" },
            { name: "desktop:open-recent", payload: "" },
            { name: "attacker:event", payload: "value" },
            { name: "menu:action", payload: "import" },
          ],
        }),
      ),
    ).toEqual({
      sessionAccepted: true,
      deliveryId: "",
      events: [
        { name: "desktop:open-file", payload: "/tmp/valid.har" },
        { name: "desktop:open-url", payload: "apexload://run/1" },
        { name: "menu:action", payload: "import" },
      ],
    });
  });

  it("retries the same acknowledgement without replaying an accepted batch", async () => {
    vi.useFakeTimers();
    try {
      const rendererReady = vi.fn<(session: string) => Promise<string>>().mockResolvedValue(
        JSON.stringify({
          delivery_id: "7",
          events: [{ name: "menu:open-browser" }],
        }),
      );
      const rendererReadyAck = vi
        .fn<(session: string, deliveryId: string) => Promise<string>>()
        .mockRejectedValueOnce(new Error("temporary bridge failure"))
        .mockResolvedValueOnce(
          JSON.stringify({ events: [] }),
        );
      (window as DesktopWindow).go = {
        main: {
          App: { RendererReady: rendererReady, RendererReadyAck: rendererReadyAck },
        },
      };
      const onMenuOpenBrowser = vi.fn();

      await act(async () => {
        root.render(<Harness onMenuOpenBrowser={onMenuOpenBrowser} />);
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(onMenuOpenBrowser).toHaveBeenCalledTimes(1);
      expect(rendererReadyAck).toHaveBeenCalledTimes(1);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(50);
      });
      expect(rendererReady).toHaveBeenCalledTimes(1);
      const rendererSession = rendererReady.mock.calls[0]?.[0];
      expect(rendererReadyAck.mock.calls).toEqual([
        [rendererSession, "7"],
        [rendererSession, "7"],
      ]);
      expect(onMenuOpenBrowser).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("delivers a delayed readiness batch to the latest handlers", async () => {
    let resolveReady: ((payload: string) => void) | undefined;
    const rendererReady = vi.fn(
      (session: string) => {
        void session;
        return new Promise<string>((resolve) => {
          resolveReady = resolve;
        });
      },
    );
    const rendererReadyAck = vi.fn().mockResolvedValue(
      JSON.stringify({ events: [] }),
    );
    (window as DesktopWindow).go = {
      main: {
        App: { RendererReady: rendererReady, RendererReadyAck: rendererReadyAck },
      },
    };
    const staleHandler = vi.fn();
    const currentHandler = vi.fn();

    await act(async () => {
      root.render(<Harness onMenuOpenBrowser={staleHandler} />);
    });
    await act(async () => {
      root.render(<Harness onMenuOpenBrowser={currentHandler} />);
    });
    await act(async () => {
      resolveReady?.(
        JSON.stringify({
          delivery_id: "11",
          events: [{ name: "menu:open-browser" }],
        }),
      );
      await Promise.resolve();
    });

    expect(staleHandler).not.toHaveBeenCalled();
    expect(currentHandler).toHaveBeenCalledTimes(1);
    expect(rendererReadyAck).toHaveBeenCalledWith(
      rendererReady.mock.calls[0]?.[0],
      "11",
    );
  });

  it("awaits an older queued file before delivering newer native intents", async () => {
    let releaseFile!: () => void;
    const rendererReady = vi.fn<(session: string) => Promise<string>>().mockResolvedValue(
      JSON.stringify({
        delivery_id: "21",
        events: [
          { name: "desktop:open-file", payload: "/tmp/older.har" },
          { name: "desktop:open-url", payload: "apexload://newer" },
          { name: "menu:about" },
        ],
      }),
    );
    const rendererReadyAck = vi
      .fn<(session: string, deliveryId: string) => Promise<string>>()
      .mockResolvedValue(JSON.stringify({ events: [] }));
    (window as DesktopWindow).go = {
      main: {
        App: { RendererReady: rendererReady, RendererReadyAck: rendererReadyAck },
      },
    };
    const order: string[] = [];
    const onOpenFile = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          releaseFile = () => {
            order.push("file");
            resolve();
          };
        }),
    );
    const onOpenURL = vi.fn(() => {
      order.push("url");
    });
    const onMenuAbout = vi.fn(() => {
      order.push("about");
    });

    await act(async () => {
      root.render(
        <Harness
          onOpenFile={onOpenFile}
          onOpenURL={onOpenURL}
          onMenuAbout={onMenuAbout}
        />,
      );
      await Promise.resolve();
    });
    expect(onOpenFile).toHaveBeenCalledWith("/tmp/older.har");
    expect(onOpenURL).not.toHaveBeenCalled();
    expect(onMenuAbout).not.toHaveBeenCalled();
    expect(rendererReadyAck).not.toHaveBeenCalled();

    await act(async () => {
      releaseFile();
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(order).toEqual(["file", "url", "about"]);
    expect(rendererReadyAck).toHaveBeenCalledWith(
      rendererReady.mock.calls[0]?.[0],
      "21",
    );
  });

  it("serializes live event handlers even when later work could finish first", async () => {
    const listeners: Record<string, (arg?: unknown) => void> = {};
    (window as DesktopWindow).runtime = {
      EventsOn: (event, cb) => {
        listeners[event] = cb;
        return () => {
          delete listeners[event];
        };
      },
    };
    const releases = new Map<string, () => void>();
    const completed: string[] = [];
    const onOpenFile = vi.fn(
      (path: string) =>
        new Promise<void>((resolve) => {
          releases.set(path, () => {
            completed.push(path);
            resolve();
          });
        }),
    );
    await act(async () => {
      root.render(<Harness onOpenFile={onOpenFile} />);
    });

    act(() => {
      listeners["desktop:open-file"]?.("/tmp/first.har");
      listeners["desktop:open-file"]?.("/tmp/second.har");
    });
    await act(async () => {
      await Promise.resolve();
    });
    expect(onOpenFile.mock.calls).toEqual([["/tmp/first.har"]]);
    expect(releases.has("/tmp/second.har")).toBe(false);

    await act(async () => {
      releases.get("/tmp/first.har")?.();
      await Promise.resolve();
    });
    expect(onOpenFile.mock.calls).toEqual([
      ["/tmp/first.har"],
      ["/tmp/second.har"],
    ]);
    await act(async () => {
      releases.get("/tmp/second.har")?.();
      await Promise.resolve();
    });
    expect(completed).toEqual(["/tmp/first.har", "/tmp/second.har"]);
  });
});
