// Reads an HTML5 drop into loose files and whole dropped folders, so one drop
// zone accepts both archives and unpacked project folders. Files inside a
// dropped folder carry webkitRelativePath ("Folder/sub/file"), exactly like a
// webkitdirectory pick, so they upload through the same folder path.

export interface DroppedImportSources {
  files: File[]
  folders: File[][]
}

// A project folder holds dozens of files; this only stops a mistaken drop of
// something like a home directory from walking the whole disk. Directories
// count too, so a deep or looping tree with few files is also stopped.
export const MAX_DROPPED_FOLDER_ENTRIES = 5000

export class DroppedFolderTooLargeError extends Error {
  constructor(folderName: string) {
    super(`${folderName} has more than ${MAX_DROPPED_FOLDER_ENTRIES} files and folders. Drop the script's own folder.`)
  }
}

function readEntries(reader: FileSystemDirectoryReader): Promise<FileSystemEntry[]> {
  return new Promise((resolve, reject) => reader.readEntries(resolve, reject))
}

function readFile(entry: FileSystemFileEntry): Promise<File> {
  return new Promise((resolve, reject) => entry.file(resolve, reject))
}

async function collectFolderFiles(root: FileSystemDirectoryEntry): Promise<File[]> {
  const files: File[] = []
  let entries = 0
  const pending: Array<{ entry: FileSystemDirectoryEntry; path: string }> = [{ entry: root, path: root.name }]
  while (pending.length > 0) {
    const { entry, path } = pending.shift()!
    const reader = entry.createReader()
    // readEntries returns the directory in batches until it returns none.
    for (let batch = await readEntries(reader); batch.length > 0; batch = await readEntries(reader)) {
      for (const child of batch) {
        if (++entries > MAX_DROPPED_FOLDER_ENTRIES) throw new DroppedFolderTooLargeError(root.name)
        const childPath = `${path}/${child.name}`
        if (child.isDirectory) {
          pending.push({ entry: child as FileSystemDirectoryEntry, path: childPath })
          continue
        }
        if (!child.isFile) continue
        const file = await readFile(child as FileSystemFileEntry)
        Object.defineProperty(file, 'webkitRelativePath', { value: childPath })
        files.push(file)
      }
    }
  }
  return files
}

// The drop's items are only readable while the drop event is dispatching, so
// this takes its snapshot synchronously and only walks folders afterwards.
export function readDroppedImportSources(dataTransfer: DataTransfer): Promise<DroppedImportSources> {
  const items = Array.from(dataTransfer.items || []).filter(item => item.kind === 'file')
  if (items.length === 0) return Promise.resolve({ files: Array.from(dataTransfer.files || []), folders: [] })
  const files: File[] = []
  const directories: FileSystemDirectoryEntry[] = []
  for (const item of items) {
    const entry = typeof item.webkitGetAsEntry === 'function' ? item.webkitGetAsEntry() : null
    if (entry?.isDirectory) {
      directories.push(entry as FileSystemDirectoryEntry)
      continue
    }
    const file = item.getAsFile()
    if (file) files.push(file)
  }
  return Promise.all(directories.map(collectFolderFiles)).then(folders => ({ files, folders }))
}
