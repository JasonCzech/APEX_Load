// Single source of truth for talking to the Wails desktop shell. The dashboard
// is also served as a hosted web app, so every desktop capability is
// feature-detected here and the UI degrades gracefully when it is absent.
//
// The bridge is the set of Go methods bound on `window.go.main.App`. New bound
// methods must also be regenerated into dashboard/wailsjs/go/main/App.{js,d.ts}
// by `wails build` — but the dashboard reads the injected global directly so it
// never imports the generated module (which would pull Wails code into the
// hosted bundle).

import type { DesktopStatusResponse } from "../types/library";

export type WailsDesktopBridge = {
  // API origin discovery (loopback server binds a random port).
  GetAPIOrigin?: () => Promise<string>;
  // Per-launch loopback capability. This value is deliberately retained only
  // in renderer memory and is attached to desktop REST/WebSocket requests.
  GetAPICapability?: () => Promise<string>;
  // Folder / browser helpers.
  OpenInBrowser?: (
    devViewMode: "admin" | "user",
    projectId: string,
  ) => Promise<void>;
  OpenLogsFolder?: () => Promise<void>;
  OpenAppDataFolder?: () => Promise<void>;
  OpenURL?: (url: string) => Promise<void>;
  // Opens a narrowly allow-listed operating-system settings surface.
  OpenSystemSettings?: (target: "network" | "proxy") => Promise<void>;
  RevealInFolder?: (path: string) => Promise<void>;
  // Window / session state.
  GetLastRoute?: () => Promise<string>;
  SetLastRoute?: (route: string) => Promise<void>;
  GetStartupError?: () => Promise<string>;
  // Native file dialogs + file IO (filters/recents passed as JSON strings to
  // keep the Wails binding free of generated model types).
  OpenFileDialogNative?: (
    title: string,
    filtersJson: string,
  ) => Promise<string>;
  OpenFilesDialogNative?: (
    title: string,
    filtersJson: string,
  ) => Promise<string[]>;
  SaveFileDialogNative?: (
    title: string,
    defaultName: string,
    filtersJson: string,
  ) => Promise<string>;
  ReadFileBase64?: (path: string) => Promise<string>;
  // Native-drop reads wait for the backend's OS-issued path grant, avoiding a
  // race between the frontend and backend Wails drop handlers.
  ReadDroppedFileBase64?: (path: string) => Promise<string>;
  WriteFileBase64?: (path: string, base64: string) => Promise<void>;
  // Streams a same-controller GET directly to a native save destination. The
  // native process supplies the desktop capability and never materializes the
  // response as a renderer Blob, so this is suitable for large archives.
  SaveAPIResourceNative?: (
    resourcePath: string,
    defaultName: string,
    filtersJson: string,
    devViewMode: "admin" | "user",
    projectId: string,
  ) => Promise<string>;
  // Notifications.
  Notify?: (title: string, body: string) => Promise<void>;
  NotificationsAvailable?: () => Promise<boolean>;
  // Keep the machine awake while load tests run.
  SetKeepAwake?: (active: boolean) => Promise<void>;
  // Dock/taskbar badge (e.g. active-run count); "" clears it.
  SetDockBadge?: (label: string) => Promise<void>;
  // Recent items.
  GetRecentItems?: () => Promise<string>;
  AddRecentItem?: (itemJson: string) => Promise<void>;
  ClearRecentItems?: () => Promise<void>;
  ToggleRecentPin?: (kind: string, id: string) => Promise<void>;
  // Diagnostics.
  ExportDiagnostics?: () => Promise<string>;
  // Signals that Wails event listeners are installed and returns any cold-
  // launch activations that arrived before the renderer was ready.
  RendererReady?: (rendererSession: string) => Promise<string>;
  // Acknowledges one replayed readiness batch and atomically returns the next.
  RendererReadyAck?: (
    rendererSession: string,
    deliveryId: string,
  ) => Promise<string>;
  // Closes the activation delivery window before a renderer reload/unload.
  RendererUnready?: (rendererSession: string) => Promise<void>;
};

type LocationLike = { protocol?: string; hostname?: string };

// isWailsLocation reports whether a location object is the Wails webview origin.
// macOS WKWebView serves from `wails://wails` (host "wails"); Windows/Linux use
// `*.localhost`. Accepts any {protocol,hostname} so it works with both
// window.location and the apiClient's injectable ApiLocation.
export function isWailsLocation(
  location: LocationLike | undefined | null,
): boolean {
  if (!location) return false;
  return (
    location.protocol === "wails:" || location.hostname === "wails.localhost"
  );
}

// getDesktopBridge returns the bound Go App object, or undefined in the browser.
export function getDesktopBridge(): WailsDesktopBridge | undefined {
  if (typeof window === "undefined") return undefined;
  return (window as unknown as { go?: { main?: { App?: WailsDesktopBridge } } })
    .go?.main?.App;
}

// isDesktopBridgeAvailable reports whether the Go bridge is reachable.
export function isDesktopBridgeAvailable(): boolean {
  return getDesktopBridge() !== undefined;
}

// isDesktopSurface reports whether the dashboard is running inside the desktop
// shell, by webview origin or by the presence of the bound bridge.
export function isDesktopSurface(): boolean {
  if (typeof window === "undefined") return false;
  if (isWailsLocation(window.location)) return true;
  return isDesktopBridgeAvailable();
}

// isMacDesktop reports whether the desktop shell is running on macOS (used for
// platform-specific affordance labels such as "Reveal in Finder").
export function isMacDesktop(): boolean {
  if (typeof navigator === "undefined") return false;
  return navigator.userAgent.toLowerCase().includes("mac");
}

export type DesktopCapabilities = {
  isDesktop: boolean;
  nativeDialogs: boolean;
  fileDrop: boolean;
  notifications: boolean;
  reveal: boolean;
  recents: boolean;
  diagnostics: boolean;
  externalLinks: boolean;
};

// desktopCapabilities derives which native features are usable, combining the
// bound-method surface with the desktop-status payload. Hosted web returns an
// all-false map so native-only UI hides cleanly.
export function desktopCapabilities(
  bridge: WailsDesktopBridge | undefined = getDesktopBridge(),
  status?: DesktopStatusResponse | null,
): DesktopCapabilities {
  void status;
  const isDesktop = bridge !== undefined || isDesktopSurface();
  if (!isDesktop) {
    return {
      isDesktop: false,
      nativeDialogs: false,
      fileDrop: false,
      notifications: false,
      reveal: false,
      recents: false,
      diagnostics: false,
      externalLinks: false,
    };
  }
  return {
    isDesktop: true,
    // File drop is enabled at the shell level (options.DragAndDrop), so it is
    // available whenever we are in the desktop surface.
    fileDrop: true,
    nativeDialogs: typeof bridge?.OpenFileDialogNative === "function",
    notifications: typeof bridge?.Notify === "function",
    reveal: typeof bridge?.RevealInFolder === "function",
    recents: typeof bridge?.GetRecentItems === "function",
    diagnostics: typeof bridge?.ExportDiagnostics === "function",
    externalLinks: typeof bridge?.OpenURL === "function",
  };
}
