import { readFileSync } from 'node:fs'
import { fileURLToPath, pathToFileURL } from 'node:url'

// The repository pins exactly one Node.js version, and `.nvmrc` is the copy a
// contributor's `nvm use` actually reads. Derive the preinstall gate from that
// file instead of repeating the literal here, so the two cannot drift apart.
// The remaining copies (dashboard engines.node, every `FROM node:` builder
// image and distroless Node runtime image across the root and runners/*
// Dockerfiles, and the setup-node pins under .github) are asserted against it by
// check-node-version.node-test.mjs, which CI runs via `npm run test:node-version`.
export const nodeVersionFileUrl = new URL('../../.nvmrc', import.meta.url)

export function readPinnedNodeVersion(fileUrl = nodeVersionFileUrl) {
  let contents
  try {
    contents = readFileSync(fileUrl, 'utf8')
  } catch (cause) {
    throw new Error(`Unable to read the pinned Node.js version from ${fileURLToPath(fileUrl)}`, { cause })
  }
  const version = contents.trim().replace(/^v/, '')
  if (!/^\d+\.\d+\.\d+$/.test(version)) {
    throw new Error(`${fileURLToPath(fileUrl)} must contain an exact Node.js version such as 22.22.0; found ${JSON.stringify(contents.trim())}.`)
  }
  return version
}

export const requiredNodeVersion = readPinnedNodeVersion()

export function assertNodeVersion(actual = process.versions.node) {
  if (actual !== requiredNodeVersion) {
    throw new Error(`APEX Load dashboard requires Node.js ${requiredNodeVersion}; found ${actual}. Install the pinned version before running npm install.`)
  }
}

const executedDirectly = process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href
if (executedDirectly) {
  assertNodeVersion()
}
