// Desktop-shell event wiring for the dashboard: native menu events and
// external-link routing. Inert in the browser (no Wails runtime / bridge).

import { useCallback, useEffect, useRef } from "react";

import { getDesktopBridge } from "./desktopBridge";
import { DESKTOP_QUEUED_MENU_ZOOM_EVENT } from "./rendererEvents";
import { getWailsEventRuntime } from "./wailsRuntime";

const EXTERNAL_LINK_RE = /^(https?:|mailto:)/i;
export const DESKTOP_OPEN_ACTIVATION_EVENT = "apexload:desktop-open-activation";

export type DesktopRouteActivationKind =
  | "file"
  | "url"
  | "menu-action"
  | "open-recent";

export function announceDesktopOpenActivation(kind: DesktopRouteActivationKind): void {
  if (typeof window !== "undefined") {
    window.dispatchEvent(
      new CustomEvent(DESKTOP_OPEN_ACTIVATION_EVENT, { detail: kind }),
    );
  }
}

export interface DesktopShellEventHandlers {
  onMenuAbout?: () => void | Promise<void>;
  onMenuOpenBrowser?: () => void | Promise<void>;
  onMenuAction?: (action: string) => void | Promise<void>;
  onOpenFile?: (path: string) => void | Promise<void>;
  onOpenURL?: (url: string) => void | Promise<void>;
  onOpenRecent?: (route: string) => void | Promise<void>;
  onRecentsChanged?: () => void | Promise<void>;
}

export type PendingDesktopRendererEvent = {
  name:
    | "desktop:open-file"
    | "desktop:open-url"
    | "menu:about"
    | "menu:open-browser"
    | "menu:action"
    | "menu:zoom"
    | "desktop:open-recent"
    | "desktop:recents-changed";
  payload: string;
};

export type PendingDesktopOpenRequests = {
  sessionAccepted: boolean;
  deliveryId: string;
  events: PendingDesktopRendererEvent[];
};

function createRendererSessionID(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return `renderer-${crypto.randomUUID()}`;
  }
  return `renderer-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

const MENU_ACTIONS = new Set(["new-test", "open-script", "import"]);
const MENU_ZOOM_ACTIONS = new Set(["in", "out", "reset"]);

function parsePendingDesktopRendererEvent(
  value: unknown,
): PendingDesktopRendererEvent | null {
  if (typeof value !== "object" || value === null) return null;
  const candidate = value as { name?: unknown; payload?: unknown };
  if (typeof candidate.name !== "string") return null;
  const payload =
    typeof candidate.payload === "string" ? candidate.payload : "";
  switch (candidate.name) {
    case "desktop:open-file":
      return payload.trim() !== "" && payload.length <= 32768
        ? { name: candidate.name, payload }
        : null;
    case "desktop:open-url":
      return /^apexload:/i.test(payload) && payload.length <= 4096
        ? { name: candidate.name, payload }
        : null;
    case "menu:about":
    case "menu:open-browser":
    case "desktop:recents-changed":
      return payload === "" ? { name: candidate.name, payload } : null;
    case "menu:action":
      return MENU_ACTIONS.has(payload)
        ? { name: candidate.name, payload }
        : null;
    case "menu:zoom":
      return MENU_ZOOM_ACTIONS.has(payload)
        ? { name: candidate.name, payload }
        : null;
    case "desktop:open-recent":
      return payload.trim() !== "" && payload.length <= 4096
        ? { name: candidate.name, payload }
        : null;
    default:
      return null;
  }
}

export function parsePendingDesktopOpenRequests(
  payload: string,
): PendingDesktopOpenRequests {
  const parsed = JSON.parse(payload) as {
    session_accepted?: unknown;
    delivery_id?: unknown;
    events?: unknown;
  } | null;
  if (typeof parsed !== "object" || parsed === null || !Array.isArray(parsed.events)) {
    throw new Error("Invalid desktop renderer delivery");
  }
  if (
    parsed.session_accepted !== undefined &&
    typeof parsed.session_accepted !== "boolean"
  ) {
    throw new Error("Invalid desktop renderer session status");
  }
  if (
    parsed.delivery_id !== undefined &&
    (typeof parsed.delivery_id !== "string" ||
      !/^[1-9][0-9]{0,19}$/.test(parsed.delivery_id))
  ) {
    throw new Error("Invalid desktop renderer delivery id");
  }
  const events = parsed.events
    .map(parsePendingDesktopRendererEvent)
    .filter(
      (value): value is PendingDesktopRendererEvent => value !== null,
    );
  const deliveryId = parsed.delivery_id ?? "";
  return {
    // Older desktop builds did not return this field. Treat absence as the
    // legacy accepted response, but honor an explicit false from the current
    // session-fenced protocol.
    sessionAccepted: parsed.session_accepted !== false,
    deliveryId,
    events,
  };
}

async function deliverRendererEvent(
  event: PendingDesktopRendererEvent,
  handlers: DesktopShellEventHandlers,
): Promise<void> {
  switch (event.name) {
    case "desktop:open-file":
      await handlers.onOpenFile?.(event.payload);
      break;
    case "desktop:open-url":
      await handlers.onOpenURL?.(event.payload);
      break;
    case "menu:about":
      await handlers.onMenuAbout?.();
      break;
    case "menu:open-browser":
      await handlers.onMenuOpenBrowser?.();
      break;
    case "menu:action":
      await handlers.onMenuAction?.(event.payload);
      break;
    case "menu:zoom":
      if (typeof window !== "undefined") {
        window.dispatchEvent(
          new CustomEvent(DESKTOP_QUEUED_MENU_ZOOM_EVENT, {
            detail: event.payload,
          }),
        );
      }
      break;
    case "desktop:open-recent":
      await handlers.onOpenRecent?.(event.payload);
      break;
    case "desktop:recents-changed":
      await handlers.onRecentsChanged?.();
      break;
  }
}

// useDesktopShellEvents subscribes to the native menu/open events and routes
// external links to the system browser. Each subscription is opt-in via the
// matching handler so the host page only wires what it can act on.
export function useDesktopShellEvents(
  handlers: DesktopShellEventHandlers,
  enabled = true,
) {
  const handlersRef = useRef(handlers);
  handlersRef.current = handlers;
  const rendererSessionRef = useRef("");
  if (!rendererSessionRef.current) {
    rendererSessionRef.current = createRendererSessionID();
  }
  const deliveryChainRef = useRef<Promise<void>>(Promise.resolve());

  const enqueueRendererEvent = useCallback(
    (event: PendingDesktopRendererEvent): Promise<void> => {
      const delivery = deliveryChainRef.current.then(
        () => deliverRendererEvent(event, handlersRef.current),
        () => deliverRendererEvent(event, handlersRef.current),
      );
      // A handler failure must not permanently stall later native intents. App
      // handlers surface their own actionable errors; this terminal catch keeps
      // the shared serial queue usable and prevents unhandled rejections.
      deliveryChainRef.current = delivery.catch(() => undefined);
      return deliveryChainRef.current;
    },
    [],
  );

  // Native menu + OS open events.
  useEffect(() => {
    if (!enabled) return undefined;
    const runtime = getWailsEventRuntime();
    if (!runtime?.EventsOn) return undefined;

    const cancels: Array<() => void> = [];
    const on = (event: string, cb: (arg?: unknown) => void) => {
      const cancel = runtime.EventsOn?.(event, cb);
      if (typeof cancel === "function") cancels.push(cancel);
    };

    on("menu:about", () => {
      void enqueueRendererEvent({ name: "menu:about", payload: "" });
    });
    on("menu:open-browser", () => {
      void enqueueRendererEvent({ name: "menu:open-browser", payload: "" });
    });
    on("menu:action", (action) => {
      if (typeof action === "string")
        void enqueueRendererEvent({ name: "menu:action", payload: action });
    });
    on("desktop:open-file", (path) => {
      if (typeof path === "string") {
        void enqueueRendererEvent({ name: "desktop:open-file", payload: path });
      }
    });
    on("desktop:open-url", (url) => {
      if (typeof url === "string") {
        void enqueueRendererEvent({ name: "desktop:open-url", payload: url });
      }
    });
    on("desktop:open-recent", (route) => {
      if (typeof route === "string")
        void enqueueRendererEvent({
          name: "desktop:open-recent",
          payload: route,
        });
    });
    on("desktop:recents-changed", () => {
      void enqueueRendererEvent({
        name: "desktop:recents-changed",
        payload: "",
      });
    });

    return () => cancels.forEach((cancel) => cancel());
  }, [enabled, enqueueRendererEvent]);

  // Subscribe above before beginning the acknowledged readiness handshake. The
  // backend keeps live delivery closed while each retained batch is replayed;
  // acknowledging it atomically returns the next batch. This preserves intent
  // order even when a new native event arrives while a bridge response is in
  // flight. A rejected Ready/Ack operation is retried verbatim, so the backend's
  // retained batch cannot be silently lost.
  useEffect(() => {
    if (!enabled) return undefined;
    const bridge = getDesktopBridge();
    if (!bridge?.RendererReady) return undefined;

    let cancelled = false;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let retryDelayMs = 50;
    const rendererSession = rendererSessionRef.current;
    let request: () => Promise<string> = () =>
      bridge.RendererReady!(rendererSession);

    const replay = async () => {
      try {
        while (!cancelled) {
          const payload = await request();
          if (cancelled) return;
          const pending = parsePendingDesktopOpenRequests(payload);
          if (!pending.sessionAccepted) return;
          if (pending.events.length > 0 && !pending.deliveryId) {
            throw new Error(
              "Desktop renderer events are missing an acknowledgement id",
            );
          }
          if (pending.deliveryId && !bridge.RendererReadyAck) {
            throw new Error("Desktop renderer acknowledgement is unavailable");
          }

          for (const event of pending.events) {
            await enqueueRendererEvent(event);
          }

          if (!pending.deliveryId) return;
          const deliveryId = pending.deliveryId;
          request = () =>
            bridge.RendererReadyAck!(rendererSession, deliveryId);
          retryDelayMs = 50;
        }
      } catch {
        if (cancelled) return;
        retryTimer = setTimeout(() => {
          retryTimer = undefined;
          void replay();
        }, retryDelayMs);
        retryDelayMs = Math.min(retryDelayMs * 2, 2000);
      }
    };

    void replay();
    return () => {
      cancelled = true;
      if (retryTimer !== undefined) clearTimeout(retryTimer);
    };
  }, [enabled, enqueueRendererEvent]);

  // Tell the backend when this renderer has committed to going away so OS
  // activations are queued until the replacement installs listeners. Do not
  // use cancelable beforeunload: a dirty-edit guard can keep this same renderer
  // alive after the backend has already retired its session.
  useEffect(() => {
    if (!enabled) return undefined;
    const bridge = getDesktopBridge();
    if (
      typeof window === "undefined" ||
      typeof bridge?.RendererUnready !== "function"
    )
      return undefined;
    const markUnready = () => {
      void bridge.RendererUnready?.(rendererSessionRef.current);
    };
    window.addEventListener("pagehide", markUnready);
    return () => window.removeEventListener("pagehide", markUnready);
  }, [enabled]);

  // Route external http(s)/mailto links to the system browser so they never
  // replace the SPA inside the webview.
  useEffect(() => {
    if (!enabled) return undefined;
    const bridge = getDesktopBridge();
    if (
      typeof document === "undefined" ||
      typeof bridge?.OpenURL !== "function"
    )
      return undefined;

    const handler = (event: MouseEvent) => {
      if (event.defaultPrevented || event.button !== 0) return;
      const target = event.target as HTMLElement | null;
      const anchor = target?.closest?.("a") as HTMLAnchorElement | null;
      const authoredHref = anchor?.getAttribute("href")?.trim() || "";
      if (!anchor || !authoredHref || authoredHref.startsWith("#")) return;

      // Classify the URL after browser resolution. Reading the authored
      // attribute alone misses protocol-relative links such as //attacker.
      // macOS uses the custom wails: scheme, so resolve authority-relative
      // links against HTTPS there instead of producing wails://attacker.
      let href = anchor.href;
      if (/^[/\\]{2}/.test(authoredHref)) {
        try {
          const base = /^https?:$/i.test(window.location.protocol)
            ? window.location.href
            : "https://wails.localhost/";
          href = new URL(authoredHref, base).href;
        } catch {
          return;
        }
      }
      if (!EXTERNAL_LINK_RE.test(href)) return;
      // Explicitly managed links have React handlers that perform a
      // capability-bearing fetch. Every other absolute link must leave the
      // webview, including same-controller links that would replace the SPA.
      if (anchor?.dataset.desktopManagedResource === "true") return;
      event.preventDefault();
      void bridge.OpenURL?.(href);
    };
    document.addEventListener("click", handler, { capture: true });
    return () =>
      document.removeEventListener("click", handler, { capture: true });
  }, [enabled]);
}
