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

// filesFromDroppedPaths reconstructs File objects from native drop paths.
export async function filesFromDroppedPaths(paths: string[]): Promise<File[]> {
  const files: File[] = []
  for (const path of paths) {
    const file = await readFileFromBridge(path)
    if (file) {
      files.push(file)
    }
  }
  return files
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
