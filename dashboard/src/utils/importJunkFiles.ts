// Operating-system and version-control files are never import content. This
// mirrors the server's archiveingest.JunkReason, so a picked or dropped folder
// is filtered the same way before upload as an archive is after it.

export type ImportJunkReason = 'operating_system_file' | 'version_control_file' | ''

const systemDirectories = new Set(['__macosx'])
const versionControlDirectories = new Set(['.git', '.svn', '.hg'])
const systemFiles = new Set(['.ds_store', 'thumbs.db', 'ehthumbs.db', 'desktop.ini', 'icon\r'])
const versionControlFiles = new Set(['.gitignore', '.gitattributes', '.gitkeep', '.gitmodules'])

// Why a directory name is bookkeeping (its contents are never content), or ''.
export function importJunkDirectoryReason(name: string): ImportJunkReason {
  const lower = name.toLowerCase()
  if (systemDirectories.has(lower)) return 'operating_system_file'
  if (versionControlDirectories.has(lower)) return 'version_control_file'
  return ''
}

// Why a file's logical path ("Folder/sub/file") is bookkeeping, or ''.
export function importJunkReason(path: string): ImportJunkReason {
  const segments = path.replace(/\\/g, '/').split('/').filter(Boolean)
  const base = segments.pop() || ''
  for (const directory of segments) {
    const reason = importJunkDirectoryReason(directory)
    if (reason) return reason
  }
  const lower = base.toLowerCase()
  if (systemFiles.has(lower) || base.startsWith('._')) return 'operating_system_file'
  if (versionControlFiles.has(lower)) return 'version_control_file'
  return ''
}
