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

if (manifest.schema_version !== 1) fail('unsupported schema version')
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
