import { describe, expect, it } from 'vitest'
import { DroppedFolderTooLargeError, MAX_DROPPED_FOLDER_ENTRIES, readDroppedImportSources } from './droppedImportSources'

type FakeEntry = { isFile: boolean; isDirectory: boolean; name: string; [key: string]: unknown }

function fileEntry(name: string, content = name): FakeEntry {
  return { isFile: true, isDirectory: false, name, file: (resolve: (file: File) => void) => resolve(new File([content], name)) }
}

// Hands its children out in batches, like Chromium's 100-entry pages.
function folderEntry(name: string, children: FakeEntry[], batchSize = 2): FakeEntry {
  return {
    isFile: false,
    isDirectory: true,
    name,
    createReader: () => {
      let offset = 0
      return {
        readEntries: (resolve: (entries: FakeEntry[]) => void) => {
          const batch = children.slice(offset, offset + batchSize)
          offset += batch.length
          resolve(batch)
        },
      }
    },
  }
}

function transfer(items: Array<{ entry?: FakeEntry; file?: File }>): DataTransfer {
  return {
    items: items.map(({ entry, file }) => ({ kind: 'file', webkitGetAsEntry: () => entry || null, getAsFile: () => file || null })),
    files: [],
  } as unknown as DataTransfer
}

describe('readDroppedImportSources', () => {
  it('reads a dropped folder, including every batch and subfolder, with relative paths', async () => {
    const folder = folderEntry('Checkout', [
      fileEntry('Checkout.usr'),
      fileEntry('Action.c'),
      fileEntry('default.cfg'),
      folderEntry('data', [fileEntry('users.dat')]),
    ])
    const { files, folders } = await readDroppedImportSources(transfer([{ entry: folder }]))
    expect(files).toEqual([])
    expect(folders).toHaveLength(1)
    expect(folders[0].map(file => file.webkitRelativePath).sort()).toEqual([
      'Checkout/Action.c',
      'Checkout/Checkout.usr',
      'Checkout/data/users.dat',
      'Checkout/default.cfg',
    ])
  })

  it('does not walk a working copy\'s .git folder or a Mac\'s __MACOSX folder', async () => {
    const objects = Array.from({ length: MAX_DROPPED_FOLDER_ENTRIES + 1 }, (_, index) => fileEntry(`object-${index}`))
    const folder = folderEntry('Checkout', [
      fileEntry('Checkout.usr'),
      fileEntry('.gitignore'),
      folderEntry('.git', objects, 500),
      folderEntry('__MACOSX', [fileEntry('._Checkout.usr')]),
    ])
    const { folders } = await readDroppedImportSources(transfer([{ entry: folder }]))
    // A .gitignore stays: a Bruno collection reads it to decide what to skip.
    expect(folders[0].map(file => file.webkitRelativePath).sort()).toEqual(['Checkout/.gitignore', 'Checkout/Checkout.usr'])
  })

  it('keeps loose files apart from dropped folders', async () => {
    const archive = new File(['zip'], 'Checkout.zip')
    const { files, folders } = await readDroppedImportSources(transfer([
      { entry: fileEntry('Checkout.zip'), file: archive },
      { entry: folderEntry('Search', [fileEntry('Search.usr')]) },
    ]))
    expect(files).toEqual([archive])
    expect(folders.map(folder => folder.map(file => file.webkitRelativePath))).toEqual([['Search/Search.usr']])
  })

  it('falls back to the plain file list when the drop has no items', async () => {
    const archive = new File(['zip'], 'Checkout.zip')
    const { files, folders } = await readDroppedImportSources({ files: [archive] } as unknown as DataTransfer)
    expect(files).toEqual([archive])
    expect(folders).toEqual([])
  })

  it('stops walking a tree of folders with no files in them', async () => {
    let nested = folderEntry('leaf', [])
    for (let depth = 0; depth <= MAX_DROPPED_FOLDER_ENTRIES; depth += 1) nested = folderEntry(`level-${depth}`, [nested])
    await expect(readDroppedImportSources(transfer([{ entry: nested }]))).rejects.toBeInstanceOf(DroppedFolderTooLargeError)
  })

  it('stops walking a folder that is far larger than a script folder', async () => {
    const children = Array.from({ length: MAX_DROPPED_FOLDER_ENTRIES + 1 }, (_, index) => fileEntry(`file-${index}.c`))
    await expect(readDroppedImportSources(transfer([{ entry: folderEntry('Home', children, 500) }])))
      .rejects.toBeInstanceOf(DroppedFolderTooLargeError)
  })
})
