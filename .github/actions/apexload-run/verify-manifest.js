#!/usr/bin/env node
'use strict'

const fs = require('fs')

function fail(message) {
  process.stderr.write(`release manifest: ${message}\n`)
  process.exit(1)
}

const [manifestPath, tag, os, architecture, asset, checksum] = process.argv.slice(2)
if (!manifestPath || !tag || !os || !architecture || !asset || !/^[0-9a-f]{64}$/.test(checksum || '')) {
  fail('invalid verifier arguments')
}

let manifest
try {
  manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'))
} catch {
  fail('file is not valid JSON')
}

if (manifest.schema_version !== 1 && manifest.schema_version !== 2) fail('unsupported schema version')
if (manifest.tag !== tag || manifest.canonical_apexctl_version !== tag) fail('release tag mismatch')
if (manifest.version !== tag.replace(/^v/, '')) fail('release version mismatch')
if (!Array.isArray(manifest.binaries)) fail('binaries must be an array')

const matches = manifest.binaries.filter((row) =>
  row && row.name === 'apexctl' && row.os === os && row.architecture === architecture
)
if (matches.length !== 1) fail('expected exactly one apexctl record for this platform')
const binary = matches[0]
if (binary.asset !== asset || binary.sha256 !== checksum) fail('apexctl record does not match the verified asset')

if (!Array.isArray(manifest.assets)) fail('assets must be an array')
const assets = manifest.assets.filter((row) => row && row.asset === asset)
if (assets.length !== 1 || assets[0].sha256 !== checksum || !Number.isSafeInteger(assets[0].size) || assets[0].size <= 0) {
  fail('asset inventory does not match the verified apexctl')
}

// Schema v2 adds installable desktop packages. Validate its discriminating
// contract so accepting v2 cannot silently degrade into treating arbitrary
// future JSON as the legacy manifest.
if (manifest.schema_version === 2) {
  if (!Array.isArray(manifest.desktop_installers)) fail('desktop_installers must be an array for schema v2')
  const expected = new Set(['linux/amd64/deb', 'linux/arm64/deb'])
  const seen = new Set()
  for (const row of manifest.desktop_installers) {
    if (!row || row.name !== 'apexload-desktop' || row.os !== 'linux' ||
        !['amd64', 'arm64'].includes(row.architecture) || row.format !== 'deb' ||
        typeof row.asset !== 'string' || !/^[0-9a-f]{64}$/.test(row.sha256 || '')) {
      fail('invalid schema-v2 desktop installer record')
    }
    const coordinate = `${row.os}/${row.architecture}/${row.format}`
    if (!expected.has(coordinate) || seen.has(coordinate)) fail('duplicate or unexpected schema-v2 desktop installer')
    seen.add(coordinate)
    const expectedAsset = `apexload-desktop-${manifest.version}-linux-${row.architecture}.deb`
    if (row.asset !== expectedAsset) fail('schema-v2 desktop installer filename does not match its platform')
    const inventory = manifest.assets.filter((candidate) => candidate && candidate.asset === row.asset)
    if (inventory.length !== 1 || inventory[0].sha256 !== row.sha256 ||
        !Number.isSafeInteger(inventory[0].size) || inventory[0].size <= 0) {
      fail('schema-v2 desktop installer does not match asset inventory')
    }
  }
  if (seen.size !== expected.size) fail('schema-v2 desktop installer set is incomplete')
}
