import { isWailsLocation } from "../desktop/desktopBridge";
import { readStoredString, writeStoredString } from "../utils/browserStorage";
import { reportClientError } from "../utils/clientErrorReporting";

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

export type ApiRequestOptions = RequestInit & {
  timeoutMs?: number;
  maxResponseBytes?: number;
  // Background telemetry (client error reports): no activity notification and
  // no failure reporting, so a report can never trigger another report.
  silent?: boolean;
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
  if (isLocalHost && hasUsableOrigin && (env.DEV || location.port === "4000")) {
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

async function parseJsonResponse(res: Response, maxBytes?: number): Promise<unknown> {
  const contentType = (res.headers.get("content-type") || "").toLowerCase();
  if (res.status === 204 || res.status === 205) {
    return undefined;
  }
  let text: string;
  if (maxBytes !== undefined && res.body) {
    const reader = res.body.getReader();
    const chunks: Uint8Array[] = [];
    let bytes = 0;
    try {
      while (true) {
        const chunk = await reader.read();
        if (chunk.done) break;
        bytes += chunk.value.byteLength;
        if (bytes > maxBytes) {
          void reader.cancel().catch(() => undefined);
          throw new Error("API response exceeds the approved size limit.");
        }
        chunks.push(chunk.value);
      }
    } finally {
      reader.releaseLock();
    }
    const joined = new Uint8Array(bytes);
    let offset = 0;
    for (const chunk of chunks) { joined.set(chunk, offset); offset += chunk.byteLength; }
    text = new TextDecoder().decode(joined);
  } else {
    text = await res.text();
    if (maxBytes !== undefined && new TextEncoder().encode(text).byteLength > maxBytes) {
      throw new Error("API response exceeds the approved size limit.");
    }
  }
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
  }, res.headers.get(REQUEST_ID_HEADER));
}

// The controller stamps every API response with this header and uses the same
// value as the "ref" of 5xx error bodies; both locate the request's log lines.
export const REQUEST_ID_HEADER = "X-Request-ID";
const ERROR_REFERENCE_PATTERN = /^[A-Za-z0-9._:-]{1,128}$/;

function errorReference(value: unknown): string | undefined {
  return typeof value === "string" && ERROR_REFERENCE_PATTERN.test(value)
    ? value
    : undefined;
}

export class ApiError extends Error {
  status: number;
  code?: string;
  // ref is the server log reference shown to the user; requestId is this
  // response's X-Request-ID, kept for every status.
  ref?: string;
  requestId?: string;
  payload?: unknown;

  constructor(
    message: string,
    status: number,
    payload?: unknown,
    requestId?: string | null,
  ) {
    const validRequestId = errorReference(requestId);
    // Server failures always get a reference so the user has something to
    // report; 4xx messages already state the reason the server logged.
    const ref =
      (isApiErrorPayload(payload) ? errorReference(payload.ref) : undefined) ??
      (status >= 500 ? validRequestId : undefined);
    super(ref && !message.includes(ref) ? `${message} (ref ${ref})` : message);
    this.name = "ApiError";
    this.status = status;
    this.code =
      isApiErrorPayload(payload) && typeof payload.code === "string"
        ? payload.code
        : undefined;
    this.ref = ref;
    this.requestId = validRequestId;
    this.payload = payload;
  }
}

function apiRequestPath(target: string): string {
  try {
    return new URL(target).pathname;
  } catch {
    return "";
  }
}

// A 5xx without the controller's request ID was produced before the request
// reached the API (an ingress or proxy timeout, a gateway error page), so the
// controller has no record of it; report it from the browser instead.
function observeApiResponse(target: string, method: string | undefined, response: Response): void {
  if (response.status >= 500 && !response.headers.get(REQUEST_ID_HEADER)) {
    reportClientError(
      "gateway",
      `HTTP ${response.status}${response.statusText ? ` ${response.statusText}` : ""} without a controller request ID`,
      { request_method: method || "GET", request_path: apiRequestPath(target), request_status: response.status },
    );
  }
}

// Network failures (fetch rejects with TypeError) and client-side timeouts
// never reach the controller. Caller-initiated aborts are not failures.
function observeApiTransportFailure(target: string, method: string | undefined, error: unknown, timedOut: boolean): void {
  if (!timedOut && !(error instanceof TypeError)) return;
  reportClientError("network", error, { request_method: method || "GET", request_path: apiRequestPath(target) });
}

function mergeAbortSignals(
  primary?: AbortSignal | null,
  secondary?: AbortSignal | null,
): AbortSignal | undefined {
  if (!primary && !secondary) return undefined;
  if (!primary) return secondary ?? undefined;
  if (!secondary) return primary ?? undefined;
  const controller = new AbortController();
  const abort = () => {
    if (!controller.signal.aborted) controller.abort();
  };
  if (primary.aborted || secondary.aborted) {
    abort();
    return controller.signal;
  }
  primary.addEventListener("abort", abort, { once: true });
  secondary.addEventListener("abort", abort, { once: true });
  return controller.signal;
}

function createTimeoutSignal(timeoutMs?: number): {
  signal?: AbortSignal;
  deadline?: Promise<never>;
  clear: () => void;
  timedOut: () => boolean;
} {
  if (!timeoutMs || timeoutMs <= 0) {
    return {
      signal: undefined,
      deadline: undefined,
      clear: () => {},
      timedOut: () => false,
    };
  }
  const controller = new AbortController();
  let firedByTimeout = false;
  let timer = 0;
  const deadline = new Promise<never>((_resolve, reject) => {
    timer = window.setTimeout(() => {
      firedByTimeout = true;
      controller.abort();
      // Some embedded/native fetch transports do not settle when their signal
      // is aborted. Reject independently so a configured timeout is always a
      // hard deadline for callers and UI busy states.
      reject(new Error(`Request timed out after ${timeoutMs}ms`));
    }, timeoutMs);
  });
  return {
    signal: controller.signal,
    deadline,
    clear: () => window.clearTimeout(timer),
    timedOut: () => firedByTimeout,
  };
}

// Only the timeout timer firing makes an AbortError a timeout. A caller- or
// unmount-initiated abort travels through the same merged signal, and reporting
// that as "Request timed out" both misleads the caller and replaces the
// DOMException with a plain Error, breaking standard `err.name === "AbortError"`
// cancellation checks.
function isTimeoutAbort(error: unknown, timedOut: boolean): boolean {
  if (!timedOut) return false;
  return error instanceof DOMException && error.name === "AbortError";
}

function notifyActivityResponse(response: Response, method?: string): Response {
  if (response.ok && typeof window !== 'undefined') {
    if (response.headers?.get('X-APEXLOAD-Audit-Status') === 'unavailable') window.dispatchEvent(new Event('apexload:activity-warning'));
    if (method && !['GET', 'HEAD', 'OPTIONS'].includes(method.toUpperCase())) window.dispatchEvent(new Event('apexload:activity-changed'));
  }
  return response;
}

export function apiFetch(
  pathOrURL: string,
  options?: ApiRequestOptions,
): Promise<Response> {
  return apiFetchFromOrigin(API_ORIGIN, pathOrURL, options);
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
      response.headers.get(REQUEST_ID_HEADER),
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
  options?: ApiRequestOptions,
): Promise<Response> {
  const target = buildApiUrl(apiOrigin, pathOrURL);
  const isDesktopController =
    new URL(target).origin === new URL(API_ORIGIN).origin;
  const { timeoutMs, signal, silent, ...init } = options ?? {};
  delete init.maxResponseBytes;
  const timeout = createTimeoutSignal(timeoutMs);
  const mergedSignal = mergeAbortSignals(signal, timeout.signal);
  const pendingFetch = fetch(target, {
    ...init,
    signal: mergedSignal,
    credentials: init?.credentials ?? "include",
    headers: mergeHeaders(
      init?.headers,
      isDesktopController ? buildDevPreviewHeaders() : {},
      isDesktopController,
    ),
  });
  return (timeout.deadline
    ? Promise.race([pendingFetch, timeout.deadline])
    : pendingFetch).then(response => {
    if (silent) return response;
    observeApiResponse(target, init.method, response);
    return isDesktopController ? notifyActivityResponse(response, init.method) : response;
  }).catch((error: unknown) => {
    const timedOut = isTimeoutAbort(error, timeout.timedOut());
    const failure = timedOut ? new Error(`Request timed out after ${timeoutMs}ms`) : error;
    if (!silent) observeApiTransportFailure(target, init.method, failure, timedOut);
    throw failure;
  }).finally(() => {
    timeout.clear();
  });
}

export async function request<T>(
  path: string,
  options?: ApiRequestOptions,
): Promise<T> {
  return requestFromOrigin<T>(API_ORIGIN, path, options);
}

export interface ApiResponseEnvelope<T> {
  body: T;
  headers: Headers;
  status: number;
}

export async function requestWithResponse<T>(path: string, options?: ApiRequestOptions): Promise<ApiResponseEnvelope<T>> {
  return requestEnvelopeFromOrigin<T>(API_ORIGIN, path, options);
}

export async function requestFromOrigin<T>(
  apiOrigin: string,
  path: string,
  options?: ApiRequestOptions,
): Promise<T> {
  return (await requestEnvelopeFromOrigin<T>(apiOrigin, path, options)).body;
}

async function requestEnvelopeFromOrigin<T>(
  apiOrigin: string,
  path: string,
  options?: ApiRequestOptions,
): Promise<ApiResponseEnvelope<T>> {
  const headers = new Headers(options?.headers);
  const formBody = typeof FormData !== "undefined" && options?.body instanceof FormData;
  if (!headers.has("Content-Type") && !formBody) {
    headers.set("Content-Type", "application/json");
  }
  const { timeoutMs, signal, maxResponseBytes, ...init } = options ?? {};
  const timeout = createTimeoutSignal(timeoutMs);
  const mergedSignal = mergeAbortSignals(signal, timeout.signal);
  const pendingRequest = (async () => {
    // apiFetchFromOrigin's timeout naturally ends when response headers arrive.
    // JSON requests need a stronger contract: keep this outer deadline active
    // until the response body has also been consumed and decoded.
    const res = await apiFetchFromOrigin(apiOrigin, path, {
      ...init,
      headers,
      signal: mergedSignal,
    });
    if (!res.ok) {
      const body = await parseJsonResponse(res, maxResponseBytes).catch((error: unknown): ApiErrorPayload => {
        // Preserve cancellation so the full-request timeout can classify it;
        // only malformed/non-JSON error bodies use the status-text fallback.
        if (error instanceof DOMException && error.name === "AbortError") {
          throw error;
        }
        return { error: res.statusText };
      });
      const message =
        isApiErrorPayload(body) && typeof body.error === "string" && body.error
          ? body.error
          : res.statusText;
      throw new ApiError(message, res.status, body, res.headers.get(REQUEST_ID_HEADER));
    }
    return { body: (await parseJsonResponse(res, maxResponseBytes)) as T, headers: new Headers(res.headers), status: res.status };
  })();

  try {
    return await (timeout.deadline
      ? Promise.race([pendingRequest, timeout.deadline])
      : pendingRequest);
  } catch (error: unknown) {
    if (isTimeoutAbort(error, timeout.timedOut())) {
      throw new Error(`Request timed out after ${timeoutMs}ms`);
    }
    throw error;
  } finally {
    timeout.clear();
  }
}
