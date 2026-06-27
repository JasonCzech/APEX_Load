// Native file helpers for the desktop shell: native open/save dialogs, file
// read/write over the base64 bridge, and reveal-in-folder. All degrade to no-op
// in the browser so callers can use them unconditionally and fall back to the
// HTML <input type=file> path when nothing happens.

import { getDesktopBridge } from './desktopBridge'

// fileNameFromPath returns the basename of a native (possibly Windows) path.
export function fileNameFromPath(path: string): string {
  const normalized = path.replace(/\\/g, '/')
  const idx = normalized.lastIndexOf('/')
  return idx >= 0 ? normalized.slice(idx + 1) : normalized
}

// acceptToFiltersJson converts an HTML accept attribute (e.g. ".zip,.json") into
// the JSON filter list the Go bridge expects. Extension tokens become a single
// "*.zip;*.json" pattern; MIME tokens are ignored (native dialogs filter by
// extension). Returns "" when there are no usable extensions.
export function acceptToFiltersJson(accept: string): string {
  const exts = (accept || '')
    .split(',')
    .map(token => token.trim())
    .filter(token => token.startsWith('.'))
    .map(token => `*${token}`)
  if (exts.length === 0) {
    return ''
  }
  return JSON.stringify([{ displayName: 'Supported files', pattern: exts.join(';') }])
}

function base64ToArrayBuffer(base64: string): ArrayBuffer {
  const binary = atob(base64)
  const buffer = new ArrayBuffer(binary.length)
  const view = new Uint8Array(buffer)
  for (let i = 0; i < binary.length; i += 1) {
    view[i] = binary.charCodeAt(i)
  }
  return buffer
}

// fileFromBase64 reconstructs a browser File from a native path + base64 content
// so a natively-picked or -dropped file flows through the existing upload code.
export function fileFromBase64(path: string, base64: string): File {
  return new File([base64ToArrayBuffer(base64)], fileNameFromPath(path))
}

async function readFileFromBridge(path: string): Promise<File | null> {
  const bridge = getDesktopBridge()
  if (!bridge?.ReadFileBase64 || !path) {
    return null
  }
  try {
    const base64 = await bridge.ReadFileBase64(path)
    return fileFromBase64(path, base64)
  } catch {
    return null
  }
}

// pickFilesViaBridge opens a native file dialog and reconstructs File objects.
// Returns [] when the bridge is unavailable (caller should use the <input>
// fallback) or the user cancels.
export async function pickFilesViaBridge(accept: string, multiple = false): Promise<File[]> {
  const bridge = getDesktopBridge()
  if (!bridge?.OpenFileDialogNative) {
    return []
  }
  const filters = acceptToFiltersJson(accept)
  let paths: string[] = []
  if (multiple && bridge.OpenFilesDialogNative) {
    paths = (await bridge.OpenFilesDialogNative('Select files', filters)) || []
  } else {
    const single = await bridge.OpenFileDialogNative('Select file', filters)
    paths = single ? [single] : []
  }
  const files: File[] = []
  for (const path of paths) {
    const file = await readFileFromBridge(path)
    if (file) {
      files.push(file)
    }
  }
  return files
}

// Importable file types accepted from a native drop. Kept in sync with the Go
// bridge's openableFileExtensions (instance.go), which is the security boundary;
// this client-side gate avoids attempting to read unrelated dropped files.
const IMPORTABLE_DROP_EXTENSIONS = ['.json', '.zip', '.har']

function isImportableDropPath(path: string): boolean {
  const lower = path.toLowerCase()
  return IMPORTABLE_DROP_EXTENSIONS.some(ext => lower.endsWith(ext))
}

// filesFromDroppedPaths reconstructs File objects from native drop paths,
// ignoring non-importable types.
export async function filesFromDroppedPaths(paths: string[]): Promise<File[]> {
  const files: File[] = []
  for (const path of paths) {
    if (!isImportableDropPath(path)) {
      continue
    }
    const file = await readFileFromBridge(path)
    if (file) {
      files.push(file)
    }
  }
  return files
}

// Native save is capped below the Go bridge's 64 MiB limit; larger exports fall
// back to the streaming browser download (JS base64 of huge blobs is costly).
export const MAX_NATIVE_SAVE_BYTES = 16 * 1024 * 1024

function filtersForFilename(filename: string): string {
  const dot = filename.lastIndexOf('.')
  if (dot < 0 || dot === filename.length - 1) {
    return ''
  }
  const ext = filename.slice(dot + 1).toLowerCase()
  return JSON.stringify([{ displayName: `${ext.toUpperCase()} file`, pattern: `*.${ext}` }])
}

async function blobToBase64(blob: Blob): Promise<string> {
  const bytes = new Uint8Array(await blob.arrayBuffer())
  let binary = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(binary)
}

// canSaveBlobViaBridge is a synchronous check so callers (e.g. downloadBlob) can
// keep the browser path fully synchronous within the user gesture.
export function canSaveBlobViaBridge(blob: Blob): boolean {
  const bridge = getDesktopBridge()
  return (
    typeof bridge?.SaveFileDialogNative === 'function' &&
    typeof bridge?.WriteFileBase64 === 'function' &&
    blob.size <= MAX_NATIVE_SAVE_BYTES
  )
}

// saveBlobViaBridge shows the native Save dialog and writes the blob. Returns
// true when the desktop path handled it (including a user cancel) so the caller
// should NOT also trigger a browser download; false when unavailable.
export async function saveBlobViaBridge(blob: Blob, filename: string): Promise<boolean> {
  const bridge = getDesktopBridge()
  if (!canSaveBlobViaBridge(blob) || !bridge?.SaveFileDialogNative || !bridge?.WriteFileBase64) {
    return false
  }
  const path = await bridge.SaveFileDialogNative('Save', filename, filtersForFilename(filename))
  if (!path) {
    return true // user cancelled — handled, don't fall back to a browser download
  }
  await bridge.WriteFileBase64(path, await blobToBase64(blob))
  return true
}

// revealPath shows a file in the OS file manager. No-op in the browser.
export async function revealPath(path: string): Promise<void> {
  const bridge = getDesktopBridge()
  if (!bridge?.RevealInFolder || !path) {
    return
  }
  try {
    await bridge.RevealInFolder(path)
  } catch {
    // best-effort
  }
}

// canUseNativeDialogs reports whether the native file picker is available.
export function canUseNativeDialogs(): boolean {
  return typeof getDesktopBridge()?.OpenFileDialogNative === 'function'
}
