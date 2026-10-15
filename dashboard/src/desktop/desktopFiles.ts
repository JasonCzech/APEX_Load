// Native file helpers for the desktop shell: native open/save dialogs, file
// read/write over the base64 bridge, and reveal-in-folder. All degrade to no-op
// in the browser so callers can use them unconditionally and fall back to the
// HTML <input type=file> path when nothing happens.

import {
  API_ORIGIN,
  apiFetch,
  readDevPreviewContext,
} from "../hooks/apiClient";
import { getDesktopBridge } from "./desktopBridge";

// fileNameFromPath returns the basename of a native (possibly Windows) path.
export function fileNameFromPath(path: string): string {
  const normalized = path.replace(/\\/g, "/");
  const idx = normalized.lastIndexOf("/");
  return idx >= 0 ? normalized.slice(idx + 1) : normalized;
}

// acceptToFiltersJson converts an HTML accept attribute (e.g. ".zip,.json") into
// the JSON filter list the Go bridge expects. Extension tokens become a single
// "*.zip;*.json" pattern; MIME tokens are ignored (native dialogs filter by
// extension). Returns "" when there are no usable extensions.
export function acceptToFiltersJson(accept: string): string {
  const exts = (accept || "")
    .split(",")
    .map((token) => token.trim())
    .filter((token) => token.startsWith("."))
    .map((token) => `*${token}`);
  if (exts.length === 0) {
    return "";
  }
  return JSON.stringify([
    { displayName: "Supported files", pattern: exts.join(";") },
  ]);
}

function base64ToArrayBuffer(base64: string): ArrayBuffer {
  const binary = atob(base64);
  const buffer = new ArrayBuffer(binary.length);
  const view = new Uint8Array(buffer);
  for (let i = 0; i < binary.length; i += 1) {
    view[i] = binary.charCodeAt(i);
  }
  return buffer;
}

// fileFromBase64 reconstructs a browser File from a native path + base64 content
// so a natively-picked or -dropped file flows through the existing upload code.
export function fileFromBase64(path: string, base64: string): File {
  return new File([base64ToArrayBuffer(base64)], fileNameFromPath(path));
}

export type NativeFileReadErrorCode =
  | "bridge_unavailable"
  | "not_authorized"
  | "not_found"
  | "too_large"
  | "read_failed";

export class NativeFileReadError extends Error {
  readonly code: NativeFileReadErrorCode;
  readonly path: string;
  readonly cause: unknown;

  constructor(code: NativeFileReadErrorCode, path: string, cause?: unknown) {
    const name = fileNameFromPath(path) || "selected file";
    const message =
      code === "too_large"
        ? `${name} is too large for the native desktop bridge.`
        : code === "not_authorized"
          ? `The desktop shell could not confirm permission to read ${name}.`
          : code === "not_found"
            ? `${name} is no longer available at the selected location.`
            : code === "bridge_unavailable"
              ? "Native file reading is unavailable in this desktop build."
              : `The desktop shell could not read ${name}.`;
    super(message);
    this.name = "NativeFileReadError";
    this.code = code;
    this.path = path;
    this.cause = cause;
  }
}

function classifyNativeReadError(error: unknown): NativeFileReadErrorCode {
  const message = error instanceof Error ? error.message : String(error || "");
  if (
    /too large|payload is too large|use the upload flow|exceeds?.*bytes/i.test(
      message,
    )
  )
    return "too_large";
  if (
    /not selected|not authorized|permission|read grant|refusing to read/i.test(
      message,
    )
  )
    return "not_authorized";
  if (/not found|no such file|does not exist/i.test(message))
    return "not_found";
  return "read_failed";
}

export function nativeFileReadErrorMessage(error: unknown): string {
  if (error instanceof NativeFileReadError) {
    if (error.code === "too_large") {
      return `${error.message} Use the browser file chooser to import it without base64 bridging.`;
    }
    return error.message;
  }
  return error instanceof Error
    ? error.message
    : "The desktop shell could not read the selected file.";
}

export function shouldUseBrowserFileFallback(error: unknown): boolean {
  return (
    error instanceof NativeFileReadError &&
    (error.code === "too_large" || error.code === "bridge_unavailable")
  );
}

async function readFileFromBridge(
  path: string,
  source: "dialog" | "drop",
): Promise<File> {
  const bridge = getDesktopBridge();
  const hasReader =
    source === "drop"
      ? typeof bridge?.ReadDroppedFileBase64 === "function" ||
        typeof bridge?.ReadFileBase64 === "function"
      : typeof bridge?.ReadFileBase64 === "function";
  if (!bridge || !hasReader || !path) {
    throw new NativeFileReadError("bridge_unavailable", path);
  }

  const read =
    source === "drop" && bridge.ReadDroppedFileBase64
      ? bridge.ReadDroppedFileBase64.bind(bridge)
      : bridge.ReadFileBase64!.bind(bridge);
  // New desktop builds make drop authorization deterministic in
  // ReadDroppedFileBase64. The bounded retry is compatibility-only for older
  // shells where the two Wails drop listeners can race.
  const retryDelays =
    source === "drop" && !bridge.ReadDroppedFileBase64 ? [0, 25, 75, 150] : [0];
  let lastError: unknown;
  for (const delay of retryDelays) {
    if (delay > 0)
      await new Promise((resolve) => window.setTimeout(resolve, delay));
    try {
      const base64 = await read(path);
      return fileFromBase64(path, base64);
    } catch (error: unknown) {
      lastError = error;
    }
  }
  throw new NativeFileReadError(
    classifyNativeReadError(lastError),
    path,
    lastError,
  );
}

// pickFilesViaBridge opens a native file dialog and reconstructs File objects.
// Returns [] when the bridge is unavailable (caller should use the <input>
// fallback) or the user cancels.
export async function pickFilesViaBridge(
  accept: string,
  multiple = false,
): Promise<File[]> {
  const bridge = getDesktopBridge();
  if (!bridge?.OpenFileDialogNative) {
    return [];
  }
  const filters = acceptToFiltersJson(accept);
  let paths: string[] = [];
  if (multiple && bridge.OpenFilesDialogNative) {
    paths = (await bridge.OpenFilesDialogNative("Select files", filters)) || [];
  } else {
    const single = await bridge.OpenFileDialogNative("Select file", filters);
    paths = single ? [single] : [];
  }
  const files: File[] = [];
  for (const path of paths) {
    files.push(await readFileFromBridge(path, "dialog"));
  }
  return files;
}

// Importable file types accepted from a native drop. Kept in sync with the Go
// bridge's isImportableFile (instance.go: openableFileExtensions plus
// importReadableFileExtensions), which is the security boundary; this
// client-side gate avoids attempting to read unrelated dropped files.
const IMPORTABLE_DROP_EXTENSIONS = [
  ".json",
  ".zip",
  ".usz",
  ".har",
  ".jmx",
  ".xml",
  ".wsdl",
  ".yaml",
  ".yml",
  ".raml",
  ".js",
  ".devweb",
  ".postman_collection.json",
  ".postman_environment.json",
  ".asyncapi.json",
  ".asyncapi.yaml",
  ".asyncapi.yml",
  ".openapi.json",
  ".openapi.yaml",
  ".openapi.yml",
  ".http",
  ".txt",
  ".curl",
  ".sh",
  ".bru",
  // Bruno archives by name; import routing sends them to the Bruno importer.
  ".bruno.tar",
  ".bruno.tar.gz",
];

// pickImportFilesViaBridge is File > Open Script…: a native Open dialog for the
// import file types. A dialog filter matches a file's last extension only, so
// a compound name is offered through its plain suffix (".postman_collection.json"
// through ".json"). Bruno archives are not offered: their ".tar" or ".gz" would
// admit any tarball, which import routing would take for an API description.
// They open from the Bruno import card, or by dropping them on the window.
// Returns null when the shell has no native dialog, and [] when the person
// cancels.
export async function pickImportFilesViaBridge(): Promise<File[] | null> {
  if (!canUseNativeDialogs()) return null;
  const accept = IMPORTABLE_DROP_EXTENSIONS.filter((ext) => ext.lastIndexOf(".") === 0).join(",");
  return pickFilesViaBridge(accept, true);
}

function isImportableDropPath(path: string): boolean {
  const lower = path.toLowerCase();
  return IMPORTABLE_DROP_EXTENSIONS.some((ext) => lower.endsWith(ext));
}

// filesFromDroppedPaths reconstructs File objects from native drop paths,
// ignoring non-importable types.
export async function filesFromDroppedPaths(paths: string[]): Promise<File[]> {
  const files: File[] = [];
  for (const path of paths) {
    if (!isImportableDropPath(path)) {
      continue;
    }
    files.push(await readFileFromBridge(path, "drop"));
  }
  return files;
}

// Archives the Bruno source card reads. They stay out of the shared list: a
// generic tarball dropped elsewhere is not import content, and import routing
// would hand it to the OpenAPI importer.
export const BRUNO_DROP_ARCHIVE_EXTENSIONS: readonly string[] = [".tar", ".tgz", ".gz"];

// The shell grants only what a drop event reported: a folder, or a file of a
// type it reads. Asking for anything else fails with "not granted".
function isUngrantedDropError(error: unknown): boolean {
  const cause = error instanceof NativeFileReadError ? error.cause : error;
  return /not granted/i.test(cause instanceof Error ? cause.message : String(cause ?? ""));
}

// filesFromDroppedEntries is filesFromDroppedPaths plus dropped folders: a
// folder's files carry webkitRelativePath ("Folder/sub/file"), like a browser
// folder pick, so they upload through the same folder path. A dropped item
// that is neither (a document, an app) is skipped. extraExtensions widens the
// file types one drop zone reads.
export async function filesFromDroppedEntries(
  paths: string[],
  extraExtensions: readonly string[] = [],
): Promise<File[]> {
  const bridge = getDesktopBridge();
  const readFolder = async (path: string): Promise<File[] | null> => {
    if (typeof bridge?.ReadDroppedDirectory !== "function") return null;
    try {
      return filesFromDroppedDirectoryPayload(await bridge.ReadDroppedDirectory(path));
    } catch (error: unknown) {
      if (isUngrantedDropError(error)) return null;
      throw new NativeFileReadError(classifyNativeReadError(error), path, error);
    }
  };
  // Paths are read side by side: each one the shell did not grant waits out
  // its grant timeout, and those waits must not add up.
  const groups = await Promise.all(paths.map(async (path): Promise<File[]> => {
    const lower = path.toLowerCase();
    if (isImportableDropPath(path) || extraExtensions.some((ext) => lower.endsWith(ext))) {
      try {
        return [await readFileFromBridge(path, "drop")];
      } catch (error: unknown) {
        // A folder named like a file type (orders.yml/) has no file grant.
        const folder = isUngrantedDropError(error) ? await readFolder(path) : null;
        if (folder) return folder;
        throw error;
      }
    }
    return (await readFolder(path)) ?? [];
  }));
  return groups.flat();
}

function filesFromDroppedDirectoryPayload(raw: string): File[] {
  const parsed = JSON.parse(raw) as { files?: Array<{ path?: unknown; base64?: unknown }> } | null;
  return (parsed?.files || []).flatMap((entry) => {
    if (typeof entry.path !== "string" || typeof entry.base64 !== "string") return [];
    const file = fileFromBase64(entry.path, entry.base64);
    Object.defineProperty(file, "webkitRelativePath", { value: entry.path });
    return [file];
  });
}

// Native save is capped below the Go bridge's 64 MiB limit; larger exports fall
// back to the streaming browser download (JS base64 of huge blobs is costly).
export const MAX_NATIVE_SAVE_BYTES = 16 * 1024 * 1024;

// Native save helpers use an explicit outcome instead of a boolean so callers
// never confuse a user-cancelled dialog with a file that was actually written.
// Bridge errors still reject: they are operational failures that the owning UI
// should surface, not reasons to attempt an unreliable WebView download.
export type NativeSaveOutcome = "saved" | "cancelled" | "unavailable";

function filtersForFilename(filename: string): string {
  const dot = filename.lastIndexOf(".");
  if (dot < 0 || dot === filename.length - 1) {
    return "";
  }
  const ext = filename.slice(dot + 1).toLowerCase();
  return JSON.stringify([
    { displayName: `${ext.toUpperCase()} file`, pattern: `*.${ext}` },
  ]);
}

async function blobToBase64(blob: Blob): Promise<string> {
  const bytes = new Uint8Array(await blob.arrayBuffer());
  let binary = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(binary);
}

// canSaveBlobViaBridge is a synchronous check so callers (e.g. downloadBlob) can
// keep the browser path fully synchronous within the user gesture.
export function canSaveBlobViaBridge(blob: Blob): boolean {
  const bridge = getDesktopBridge();
  return (
    typeof bridge?.SaveFileDialogNative === "function" &&
    typeof bridge?.WriteFileBase64 === "function" &&
    blob.size <= MAX_NATIVE_SAVE_BYTES
  );
}

// saveBlobViaBridge shows the native Save dialog and writes the blob. A cancel
// is distinct from both a completed write and an unavailable bridge. Native
// dialog/write failures reject so callers can surface the real error.
export async function saveBlobViaBridge(
  blob: Blob,
  filename: string,
): Promise<NativeSaveOutcome> {
  const bridge = getDesktopBridge();
  if (
    !canSaveBlobViaBridge(blob) ||
    !bridge?.SaveFileDialogNative ||
    !bridge?.WriteFileBase64
  ) {
    return "unavailable";
  }
  const path = await bridge.SaveFileDialogNative(
    "Save",
    filename,
    filtersForFilename(filename),
  );
  if (!path) {
    return "cancelled";
  }
  await bridge.WriteFileBase64(path, await blobToBase64(blob));
  return "saved";
}

// saveApiResourceViaBridge asks the native process to fetch an API resource
// from its own embedded controller and stream it directly to a user-selected
// file. Cancellation is distinct from a completed write; errors deliberately
// reject so the owning screen can show them instead of falling back silently.
export function canSaveApiResourceViaBridge(): boolean {
  return typeof getDesktopBridge()?.SaveAPIResourceNative === "function";
}

export async function saveApiResourceViaBridge(
  resourcePath: string,
  filename: string,
): Promise<NativeSaveOutcome> {
  const bridge = getDesktopBridge();
  if (!canSaveApiResourceViaBridge() || !bridge?.SaveAPIResourceNative) {
    return "unavailable";
  }
  const preview = readDevPreviewContext();
  const path = await bridge.SaveAPIResourceNative(
    resourcePath,
    filename,
    filtersForFilename(filename),
    preview.mode,
    preview.projectId,
  );
  return path ? "saved" : "cancelled";
}

export type DesktopBrowserDownloadMode = "download" | "open";

// startDesktopBrowserDownload exchanges the browser tab's desktop capability
// for a short-lived navigation URL, then lets the browser stream the response
// directly to disk (or a new tab). This deliberately avoids Response.blob(),
// which would buffer large run archives in memory.
export async function startDesktopBrowserDownload(
  resourcePath: string,
  _filename: string,
  mode: DesktopBrowserDownloadMode = "download",
): Promise<void> {
  // Reserve the report tab while the click still carries transient user
  // activation. Waiting for the mint request before calling window.open makes
  // real browsers' popup blockers discard an otherwise valid report action.
  let reportWindow: Window | null = null;
  if (mode === "open") {
    try {
      reportWindow = window.open("", "_blank");
      if (!reportWindow) {
        throw new Error("popup blocked");
      }
      // A same-origin about:blank popup receives a copy of the opener's
      // sessionStorage before opener can be nulled. Never carry the desktop
      // API capability into report content: clear the reserved tab's copied
      // storage synchronously while user activation is still live.
      reportWindow.opener = null;
      reportWindow.sessionStorage.clear();
    } catch {
      reportWindow?.close();
      throw new Error(
        "Unable to open the report safely. Allow pop-ups for this app and try again.",
      );
    }
  }

  let response: Response;
  try {
    response = await apiFetch("/desktop/browser-downloads", {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ resourcePath, mode }),
    });
  } catch (error) {
    reportWindow?.close();
    throw error;
  }

  let payload: { error?: string; url?: string } | undefined;
  try {
    payload = (await response.json()) as { error?: string; url?: string };
  } catch {
    // Preserve the status-based error below when the controller does not
    // return JSON (for example, when an older desktop backend is running).
  }
  if (!response.ok) {
    reportWindow?.close();
    throw new Error(
      payload?.error ||
        `Download setup failed (${response.status} ${response.statusText || "request failed"})`,
    );
  }

  const rawURL = payload?.url?.trim() || "";
  if (!rawURL) {
    reportWindow?.close();
    throw new Error("The desktop controller did not return a download URL.");
  }
  const navigationURL = new URL(rawURL, API_ORIGIN);
  if (
    navigationURL.origin !== new URL(API_ORIGIN).origin ||
    navigationURL.pathname !== "/desktop/download" ||
    navigationURL.username !== "" ||
    navigationURL.password !== "" ||
    navigationURL.hash !== ""
  ) {
    reportWindow?.close();
    throw new Error("The desktop controller returned an unsafe download URL.");
  }

  if (mode === "open") {
    // Open-mode never falls back to the current dashboard tab: it owns the
    // desktop API capability in sessionStorage, which same-origin report HTML
    // must not inherit.
    try {
      reportWindow!.location.replace(navigationURL.toString());
      return;
    } catch {
      reportWindow?.close();
      throw new Error(
        "Unable to open the report safely. Allow pop-ups for this app and try again.",
      );
    }
  }

  const anchor = document.createElement("a");
  anchor.href = navigationURL.toString();
  // Do not set the HTML download attribute. The authenticated target must
  // return Content-Disposition: attachment before the backend will stream a
  // download; downstream 4xx/5xx responses then stay visible instead of being
  // saved under a trusted archive/installer suffix.
  anchor.hidden = true;
  document.body.appendChild(anchor);
  try {
    anchor.click();
  } finally {
    anchor.remove();
  }
}

// revealPath shows a file in the OS file manager. No-op in the browser.
export async function revealPath(path: string): Promise<void> {
  const bridge = getDesktopBridge();
  if (!bridge?.RevealInFolder || !path) {
    return;
  }
  try {
    await bridge.RevealInFolder(path);
  } catch {
    // best-effort
  }
}

// canUseNativeDialogs reports whether the native file picker is available.
export function canUseNativeDialogs(): boolean {
  return typeof getDesktopBridge()?.OpenFileDialogNative === "function";
}
