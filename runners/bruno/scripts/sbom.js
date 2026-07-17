'use strict';

const fs = require('node:fs');
const path = require('node:path');

const root = path.resolve(__dirname, '..');

function unquoteYAMLKey(value) {
  if (value.startsWith("'") && value.endsWith("'")) {
    return value.slice(1, -1).replaceAll("''", "'");
  }
  if (value.startsWith('"') && value.endsWith('"')) {
    return JSON.parse(value);
  }
  return value;
}

function parseLockPackageKey(raw) {
  const value = unquoteYAMLKey(String(raw || '').trim());
  const separator = value.startsWith('@')
    ? value.indexOf('@', value.indexOf('/') + 1)
    : value.indexOf('@');
  if (separator <= 0 || separator >= value.length - 1) {
    throw new Error(`unsupported pnpm package key ${JSON.stringify(value)}`);
  }
  return {
    name: value.slice(0, separator),
    version: value.slice(separator + 1)
  };
}

function collectLockfileComponents(projectRoot) {
  const filename = path.join(projectRoot, 'pnpm-lock.yaml');
  const lines = fs.readFileSync(filename, 'utf8').split(/\r?\n/);
  const components = new Map();
  let inPackages = false;
  for (const line of lines) {
    if (line === 'packages:') {
      inPackages = true;
      continue;
    }
    if (inPackages && /^[^\s]/.test(line)) break;
    if (!inPackages) continue;
    const match = line.match(/^  (\S.*):$/);
    if (!match) continue;
    const { name, version } = parseLockPackageKey(match[1]);
    const key = `${name}\0${version}`;
    components.set(key, {
      type: 'library',
      name,
      version,
      purl: `pkg:npm/${encodeURIComponent(name)}@${encodeURIComponent(version)}`
    });
  }
  if (components.size === 0) {
    throw new Error('pnpm-lock.yaml contains no package snapshot');
  }
  return [...components.values()].sort((left, right) => (
    left.name.localeCompare(right.name) || left.version.localeCompare(right.version)
  ));
}

function generateSBOM(projectRoot = root) {
  const manifest = JSON.parse(fs.readFileSync(path.join(projectRoot, 'package.json'), 'utf8'));
  return `${JSON.stringify({
    bomFormat: 'CycloneDX',
    specVersion: '1.5',
    version: 1,
    metadata: {
      timestamp: new Date(0).toISOString(),
      component: { type: 'application', name: manifest.name, version: manifest.version }
    },
    components: collectLockfileComponents(projectRoot)
  }, null, 2)}\n`;
}

if (require.main === module) {
  const document = generateSBOM();
  const outputIndex = process.argv.indexOf('--output');
  if (outputIndex >= 0) {
    const filename = process.argv[outputIndex + 1];
    if (!filename) throw new Error('--output requires a filename');
    fs.writeFileSync(path.resolve(root, filename), document, { mode: 0o644 });
  } else {
    process.stdout.write(document);
  }
}

module.exports = {
  collectLockfileComponents,
  generateSBOM,
  parseLockPackageKey,
  unquoteYAMLKey
};
