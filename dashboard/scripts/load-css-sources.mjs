import { readdir, readFile } from 'node:fs/promises'
import { join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

async function collectCss(directory, root, sources) {
  const entries = await readdir(directory, { withFileTypes: true })

  for (const entry of entries.sort((left, right) => left.name.localeCompare(right.name))) {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) {
      await collectCss(path, root, sources)
    } else if (entry.isFile() && entry.name.endsWith('.css')) {
      const sourcePath = relative(root, path).split(sep).join('/')
      sources[sourcePath] = await readFile(path, 'utf8')
    }
  }
}

export async function loadCssSources(directoryUrl) {
  const root = fileURLToPath(directoryUrl)
  const sources = {}
  await collectCss(root, root, sources)
  return sources
}
