/** Normalize an importer-owned logical path without applying host-OS rules. */
export function normalizeImportLogicalPath(value: string): string {
  const normalized = value.replace(/\\/g, '/').normalize('NFC')
  if (!normalized) throw new Error('Import source path is empty.')
  if (normalized.startsWith('/') || /^[A-Za-z]:($|\/)/.test(normalized)) {
    throw new Error('Import source paths must be relative and must not contain a drive or UNC path.')
  }
  const parts: string[] = []
  for (const part of normalized.split('/')) {
    if (!part) throw new Error('Import source paths must not contain empty components.')
    if (part === '.' || part === '..') throw new Error('Import source paths must not contain dot or parent traversal components.')
    if (part.trim() !== part) throw new Error('Import source path components must not have leading or trailing whitespace.')
    parts.push(part)
  }
  if (parts.length === 0) throw new Error('Import source path is empty after normalization.')
  return parts.join('/')
}

export function importFileLogicalPath(file: File): string {
  if (file.webkitRelativePath) return normalizeImportLogicalPath(file.webkitRelativePath)
  const normalized = file.name.replace(/\\/g, '/').normalize('NFC')
  const base = normalized.split('/').filter(Boolean).pop() || ''
  return normalizeImportLogicalPath(base)
}

export function importLogicalBase(value: string): string {
  const normalized = value.replace(/\\/g, '/')
  return normalized.split('/').filter(Boolean).pop() || ''
}
