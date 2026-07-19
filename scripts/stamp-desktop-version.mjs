#!/usr/bin/env node

import fs from 'node:fs'
import path from 'node:path'

const [configPathArg, versionArg, outputPathArg] = process.argv.slice(2)
if (!configPathArg || !versionArg) {
  console.error('usage: stamp-desktop-version.mjs <wails.json> <version> [output]')
  process.exit(2)
}

const fullVersion = versionArg.trim().replace(/^v(?=\d)/i, '')
const number = '(0|[1-9][0-9]*)'
const identifier = '(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)'
const buildIdentifier = '[0-9A-Za-z-]+'
const semver = new RegExp(`^${number}\\.${number}\\.${number}(?:-${identifier}(?:\\.${identifier})*)?(?:\\+${buildIdentifier}(?:\\.${buildIdentifier})*)?$`)
const match = semver.exec(fullVersion)
if (!match) {
  console.error(`desktop version must be canonical SemVer: ${versionArg}`)
  process.exit(2)
}

// Apple and Windows bundle metadata require a numeric version even when the Go
// build and dashboard expose a SemVer prerelease/build suffix.
const productVersion = `${match[1]}.${match[2]}.${match[3]}`
const configPath = path.resolve(configPathArg)
const outputPath = outputPathArg ? path.resolve(outputPathArg) : configPath
const configuration = JSON.parse(fs.readFileSync(configPath, 'utf8'))
configuration.info ??= {}
configuration.info.productVersion = productVersion
fs.writeFileSync(outputPath, `${JSON.stringify(configuration, null, 2)}\n`)
