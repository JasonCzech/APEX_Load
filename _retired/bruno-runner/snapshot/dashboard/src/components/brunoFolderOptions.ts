import { type Folder } from '../types'

function folderPath(folder: Folder, foldersByID: Map<string, Folder>) {
  const parts: string[] = []
  const visited = new Set<string>()
  let current: Folder | undefined = folder
  while (current) {
    if (visited.has(current.id)) {
      parts.unshift(`[cycle: ${current.id}]`)
      break
    }
    visited.add(current.id)
    parts.unshift(current.name)
    const parentID = current.parent_id?.trim()
    if (!parentID) break
    current = foldersByID.get(parentID)
    if (!current) {
      parts.unshift(`[missing parent: ${parentID}]`)
      break
    }
  }
  return parts.join(' / ')
}

export function brunoDestinationFolderOptions(folders: Folder[]) {
  const foldersByID = new Map(folders.map(folder => [folder.id, folder]))
  const paths = folders.map(folder => ({ folder, path: folderPath(folder, foldersByID) }))
  const duplicatePaths = new Map<string, number>()
  paths.forEach(({ path }) => {
    const normalized = path.toLocaleLowerCase()
    duplicatePaths.set(normalized, (duplicatePaths.get(normalized) || 0) + 1)
  })
  return paths
    .map(({ folder, path }) => ({
      folder,
      label: (duplicatePaths.get(path.toLocaleLowerCase()) || 0) > 1
        ? `${path} · ${folder.id}`
        : path,
    }))
    .sort((left, right) => left.label.localeCompare(right.label) || left.folder.id.localeCompare(right.folder.id))
}
