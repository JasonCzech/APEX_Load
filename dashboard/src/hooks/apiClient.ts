import { isWailsLocation } from "../desktop/desktopBridge";
import { readStoredString, writeStoredString } from "../utils/browserStorage";

const DEFAULT_LOCAL_API_ORIGIN = "http://127.0.0.1:8080";
export const DEV_VIEW_STORAGE_KEY = "apexload.devViewMode";
export const DEV_VIEW_MODE_CHANGED_EVENT = "apexload:dev-view-mode-changed";
export const API_ORIGIN_CHANGED_EVENT = "apexload:api-origin-changed";
const ACTIVE_PROJECT_STORAGE_KEY = "apexload.activeProjectId";
const API_ORIGIN_STORAGE_KEY = "apexload.apiOrigin";
const DEV_VIEW_HEADER = "X-APEXLoad-Dev-View";
const DEV_PROJECT_HEADER = "X-APEXLoad-Dev-Project-ID";
const DEV_PROJECT_ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$/;
export const DESKTOP_CAPABILITY_SESSION_KEY = "apexload.desktopCapability";

export type DevViewMode = "admin" | "user";
export type DevPreviewContext = { mode: DevViewMode; projectId: string };

type ApiEnvironment = {
  VITE_API_ORIGIN?: string;
  DEV?: boolean;
};

type ApiLocation = Pick<Location, "protocol" | "hostname" | "port" | "origin">;
export type WailsApiBootstrapBridge = {
  GetAPIOrigin?: () => Promise<string>;
  GetAPICapability?: () => Promise<string>;
};

type ApiErrorPayload = {
  error?: string;
  code?: string;
  [key: string]: unknown;
};

export function trimTrailingSlash(value: string): string {
  return value.replace(/\/+$/, "");
}

function getApiEnvironment(): ApiEnvironment {
  return (import.meta as ImportMeta & { env?: ApiEnvironment }).env ?? {};
}

function getWindowLocation(): ApiLocation | undefined {
  return typeof window !== "undefined" ? window.location : undefined;
}

function isApiErrorPayload(value: unknown): value is ApiErrorPayload {
  return typeof value === "object" && value !== null;
}

function getStoredApiOrigin(): string {
  return readStoredString(API_ORIGIN_STORAGE_KEY).trim();
}

function getWailsAppBridge(): WailsApiBootstrapBridge | undefined {
  if (typeof window === "undefined") return undefined;
  const candidate = (
    window as unknown as { go?: { main?: { App?: WailsApiBootstrapBridge } } }
  ).go?.main?.App;
  return candidate;
}

export async function readDesktopApiBootstrap(
  bridge: WailsApiBootstrapBridge,
): Promise<{ origin: string; capability: string }> {
  if (!bridge.GetAPIOrigin)
    throw new Error("The desktop API bridge is unavailable.");
  const origin = await bridge.GetAPIOrigin();
  const capability = await (bridge.GetAPICapability?.() ?? Promise.resolve(""));
  return { origin, capability };
}

export function resolveApiOrigin(
  env: ApiEnvironment = getApiEnvironment(),
  location: ApiLocation | undefined = getWindowLocation(),
): string {
  const envOrigin = env.VITE_API_ORIGIN;
  if (envOrigin && envOrigin.trim()) {
    return trimTrailingSlash(envOrigin.trim());
  }
  // Detect Wails desktop environment
  if (isWailsLocation(location)) {
    return trimTrailingSlash(getStoredApiOrigin() || DEFAULT_LOCAL_API_ORIGIN);
  }
  if (location?.protocol === "file:") {
    return DEFAULT_LOCAL_API_ORIGIN;
  }
  const isLocalHost =
    location?.hostname === "localhost" || location?.hostname === "127.0.0.1";
  const hasUsableOrigin = !!location?.origin && location.origin !== "null";
  if (isLocalHost && hasUsableOrigin && (env.DEV || location.port === "3000")) {
    return trimTrailingSlash(location.origin);
  }
  if (hasUsableOrigin) {
    return trimTrailingSlash(location.origin);
  }
  return DEFAULT_LOCAL_API_ORIGIN;
}

export let API_ORIGIN = resolveApiOrigin();
export let BASE = `${API_ORIGIN}/api/v1`;
// The Wails capability is intentionally process-memory only. A system-browser
// bootstrap stores a separate browser-only grant in origin-scoped
// sessionStorage so it remains limited to the controller port and survives
// dashboard reloads in that tab.
// Never persist it to localStorage, logs, diagnostics, or URL history.
let desktopApiCapability = "";

function readSessionDesktopApiCapability(): string {
  if (typeof window === "undefined") return "";
  try {
    return (
      window.sessionStorage.getItem(DESKTOP_CAPABILITY_SESSION_KEY)?.trim() ??
      ""
    );
  } catch {
    return "";
  }
}

export function setDesktopApiCapability(capability: string): void {
  desktopApiCapability = capability.trim();
}

export function hasDesktopApiCapability(): boolean {
  return desktopApiCapability !== "";
}

export function setApiOrigin(origin: string): void {
  const normalized = trimTrailingSlash(origin.trim());
  if (!normalized) return;
  API_ORIGIN = normalized;
  BASE = `${API_ORIGIN}/api/v1`;
  writeStoredString(API_ORIGIN_STORAGE_KEY, normalized);
  if (typeof window !== "undefined") {
    window.dispatchEvent(
      new CustomEvent(API_ORIGIN_CHANGED_EVENT, { detail: normalized }),
    );
  }
}

export async function initializeDesktopApiOrigin(
  bridge: WailsApiBootstrapBridge | undefined = getWailsAppBridge(),
): Promise<void> {
  const location = getWindowLocation();
  // Production webviews use a Wails origin, while `wails dev` loads the Vite
  // HTTP origin and injects the same native bridge. Either signal means the
  // renderer must discover the random embedded-controller port and capability.
  if (!isWailsLocation(location) && !bridge?.GetAPIOrigin) {
    setDesktopApiCapability(readSessionDesktopApiCapability());
    return;
  }
  if (!bridge?.GetAPIOrigin) {
    throw new Error("The desktop API bridge is unavailable.");
  }
  try {
    // GetAPIOrigin is the readiness barrier: OnStartup initializes the server
    // and per-launch capability before it resolves. Reading both in parallel
    // can observe an empty capability during that initialization window.
    const { origin, capability } = await readDesktopApiBootstrap(bridge);
    if (origin?.trim()) {
      setApiOrigin(origin);
    }
    if (bridge.GetAPICapability && !capability?.trim()) {
      throw new Error("The desktop API capability is unavailable.");
    }
    setDesktopApiCapability(capability || "");
  } catch (error: unknown) {
    setDesktopApiCapability("");
    // A Wails app must never guess port 8080: the embedded controller binds a
    // random loopback port. Let bootstrap render the native failure surface.
    throw error;
  }
}

export function normalizeDevViewMode(value: unknown): DevViewMode {
  return value === "user" ? "user" : "admin";
}

export function readStoredDevViewMode(): DevViewMode {
  return normalizeDevViewMode(readStoredString(DEV_VIEW_STORAGE_KEY));
}

export function writeStoredDevViewMode(mode: DevViewMode): void {
  const normalized = normalizeDevViewMode(mode);
  writeStoredString(DEV_VIEW_STORAGE_KEY, normalized);
  if (typeof window !== "undefined") {
    window.dispatchEvent(
      new CustomEvent(DEV_VIEW_MODE_CHANGED_EVENT, { detail: normalized }),
    );
  }
}

// readDevPreviewContext is the single renderer-side source for requests that
// cross the Wails bridge. Corrupt/stale project storage is treated as no active
// project (user mode then remains global-viewer-only) and the native boundary
// independently validates the same compact identifier allow-list.
export function readDevPreviewContext(): DevPreviewContext {
  const mode = readStoredDevViewMode();
  const rawProjectId = readStoredString(ACTIVE_PROJECT_STORAGE_KEY).trim();
  const projectId = DEV_PROJECT_ID_PATTERN.test(rawProjectId)
    ? rawProjectId
    : "";
  return { mode, projectId };
}

export function buildDevPreviewHeaders(): Record<string, string> {
  const preview = readDevPreviewContext();
  const headers: Record<string, string> = {
    [DEV_VIEW_HEADER]: preview.mode,
  };
  if (preview.projectId) {
    headers[DEV_PROJECT_HEADER] = preview.projectId;
  }
  return headers;
}

export type DesktopBrowserPreviewBridge = {
  OpenInBrowser?: (
    devViewMode: DevViewMode,
    projectId: string,
  ) => Promise<void>;
};

export async function openDesktopBrowserWithPreview(
  bridge: DesktopBrowserPreviewBridge | undefined,
): Promise<void> {
  if (!bridge?.OpenInBrowser) {
    throw new Error("Desktop browser launch is unavailable");
  }
  const preview = readDevPreviewContext();
  await bridge.OpenInBrowser(preview.mode, preview.projectId);
}

function mergeHeaders(
  headers?: HeadersInit,
  previewHeaders = buildDevPreviewHeaders(),
  includeDesktopCapability = false,
): Headers {
  const merged = new Headers(headers);
  Object.entries(previewHeaders).forEach(([key, value]) => {
    if (value) {
      merged.set(key, value);
    }
  });
  if (includeDesktopCapability && desktopApiCapability) {
    merged.set("X-APEXLoad-Desktop-Token", desktopApiCapability);
  }
  return merged;
}

function buildApiUrl(apiOrigin: string, pathOrURL: string): string {
  if (/^https?:\/\//i.test(pathOrURL)) {
    return pathOrURL;
  }
  const path = pathOrURL.startsWith("/") ? pathOrURL : `/${pathOrURL}`;
  return `${trimTrailingSlash(apiOrigin)}/api/v1${path}`;
}

function apiUrl(pathOrURL: string): string {
  return buildApiUrl(API_ORIGIN, pathOrURL);
}

export function resolveApiResourceUrl(
  pathOrURL: string,
  apiOrigin = API_ORIGIN,
): string {
  if (/^https?:\/\//i.test(pathOrURL)) {
    return pathOrURL;
  }
  const path = pathOrURL.startsWith("/") ? pathOrURL : `/${pathOrURL}`;
  return `${trimTrailingSlash(apiOrigin)}${path}`;
}

export function wsUrl(
  path: string,
  apiOrigin = API_ORIGIN,
  includeDesktopCapability = true,
): string {
  const wsOrigin = trimTrailingSlash(apiOrigin)
    .replace(/^http:/, "ws:")
    .replace(/^https:/, "wss:");
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  const url = new URL(`${wsOrigin}${normalizedPath}`);
  if (
    includeDesktopCapability &&
    desktopApiCapability &&
    trimTrailingSlash(apiOrigin) === trimTrailingSlash(API_ORIGIN)
  ) {
    url.searchParams.set("desktop_token", desktopApiCapability);
  }
  return url.toString();
}

async function parseJsonResponse(res: Response): Promise<unknown> {
  const contentType = (res.headers.get("content-type") || "").toLowerCase();
  if (res.status === 204 || res.status === 205) {
    return undefined;
  }
  const text = await res.text();
  if (!text.trim()) {
    return undefined;
  }
  if (contentType.includes("application/json")) {
    return JSON.parse(text) as unknown;
  }
  const snippet = text.trim().slice(0, 180);
  const isHTML =
    contentType.includes("text/html") ||
    snippet.toLowerCase().startsWith("<!doctype html") ||
    snippet.toLowerCase().startsWith("<html");
  const message = isHTML
    ? "API endpoint returned the dashboard HTML instead of JSON. The backend route may be unavailable or the local dev proxy may be pointed at an older controller."
    : `Expected JSON response from API but received ${contentType || "non-JSON content"}`;
  throw new ApiError(message, res.status, {
    content_type: contentType,
    body: snippet,
  });
}

export class ApiError extends Error {
  status: number;
  code?: string;
  payload?: unknown;

  constructor(message: string, status: number, payload?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code =
      isApiErrorPayload(payload) && typeof payload.code === "string"
        ? payload.code
        : undefined;
    this.payload = payload;
  }
}

export function apiFetch(
  pathOrURL: string,
  options?: RequestInit,
): Promise<Response> {
  const target = apiUrl(pathOrURL);
  const isDesktopController =
    new URL(target).origin === new URL(API_ORIGIN).origin;
  return fetch(target, {
    ...options,
    credentials: options?.credentials ?? "include",
    headers: mergeHeaders(
      options?.headers,
      isDesktopController ? buildDevPreviewHeaders() : {},
      isDesktopController,
    ),
  });
}

// requestApiNavigation starts an OAuth-style browser navigation through an
// authenticated fetch. Desktop capabilities live in sessionStorage/in memory
// and cannot be attached to a top-level navigation, so the controller returns
// the external provider URL after setting its signed state cookie.
export async function requestApiNavigation(pathOrURL: string): Promise<string> {
  const endpoint = resolveApiResourceUrl(pathOrURL);
  if (new URL(endpoint).origin !== new URL(API_ORIGIN).origin) {
    throw new Error(
      "Authentication navigation must start at the active controller.",
    );
  }
  const response = await apiFetch(endpoint, {
    credentials: "include",
    headers: { Accept: "application/json" },
  });
  const payload = (await parseJsonResponse(response).catch(() => undefined)) as
    { error?: string; redirect_url?: string } | undefined;
  if (!response.ok) {
    throw new ApiError(
      payload?.error ||
        response.statusText ||
        "Authentication navigation failed",
      response.status,
      payload,
    );
  }
  const rawRedirect = payload?.redirect_url?.trim() || "";
  if (!rawRedirect) {
    throw new Error(
      "The controller did not return an authentication destination.",
    );
  }
  const redirect = new URL(rawRedirect, API_ORIGIN);
  if (redirect.protocol !== "http:" && redirect.protocol !== "https:") {
    throw new Error(
      "The controller returned an unsupported authentication destination.",
    );
  }
  return redirect.toString();
}

export function apiFetchFromOrigin(
  apiOrigin: string,
  pathOrURL: string,
  options?: RequestInit,
): Promise<Response> {
  const target = buildApiUrl(apiOrigin, pathOrURL);
  const isDesktopController =
    new URL(target).origin === new URL(API_ORIGIN).origin;
  return fetch(target, {
    ...options,
    credentials: options?.credentials ?? "include",
    headers: mergeHeaders(
      options?.headers,
      isDesktopController ? buildDevPreviewHeaders() : {},
      isDesktopController,
    ),
  });
}

export async function request<T>(
  path: string,
  options?: RequestInit,
): Promise<T> {
  return requestFromOrigin<T>(API_ORIGIN, path, options);
}

export async function requestFromOrigin<T>(
  apiOrigin: string,
  path: string,
  options?: RequestInit,
): Promise<T> {
  const headers = new Headers(options?.headers);
  if (!headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const res = await apiFetchFromOrigin(apiOrigin, path, {
    ...options,
    headers,
  });
  if (!res.ok) {
    const body = await parseJsonResponse(res).catch((): ApiErrorPayload => ({
      error: res.statusText,
    }));
    const message =
      isApiErrorPayload(body) && typeof body.error === "string" && body.error
        ? body.error
        : res.statusText;
    throw new ApiError(message, res.status, body);
  }
  return (await parseJsonResponse(res)) as T;
}
