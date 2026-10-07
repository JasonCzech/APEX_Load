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

function parseProductionLockGraph(lines) {
  const roots = [];
  const snapshots = new Map();
  let topLevel = '';
  let inRootImporter = false;
  let importerDependencySection = '';
  let importerDependency = '';
  let snapshot = '';
  let snapshotDependencySection = '';

  for (const line of lines) {
    if (/^[^\s]/.test(line)) {
      topLevel = line.endsWith(':') ? line.slice(0, -1) : '';
      inRootImporter = false;
      importerDependencySection = '';
      importerDependency = '';
      snapshot = '';
      snapshotDependencySection = '';
      continue;
    }

    if (topLevel === 'importers') {
      const importerMatch = line.match(/^ {2}(\S.*):$/);
      if (importerMatch) {
        inRootImporter = unquoteYAMLKey(importerMatch[1]) === '.';
        importerDependencySection = '';
        importerDependency = '';
        continue;
      }
      if (!inRootImporter) continue;

      const sectionMatch = line.match(/^ {4}(dependencies|optionalDependencies|devDependencies):$/);
      if (sectionMatch) {
        importerDependencySection = sectionMatch[1];
        importerDependency = '';
        continue;
      }
      const dependencyMatch = line.match(/^ {6}(\S.*):$/);
      if (dependencyMatch) {
        importerDependency = unquoteYAMLKey(dependencyMatch[1]);
        continue;
      }
      const versionMatch = line.match(/^ {8}version: (.+)$/);
      if (versionMatch && importerDependency && importerDependencySection !== 'devDependencies') {
        roots.push(`${importerDependency}@${unquoteYAMLKey(versionMatch[1])}`);
      }
      continue;
    }

    if (topLevel !== 'snapshots') continue;
    const snapshotMatch = line.match(/^ {2}(\S.*):(?: \{\})?$/);
    if (snapshotMatch) {
      snapshot = unquoteYAMLKey(snapshotMatch[1]);
      snapshots.set(snapshot, []);
      snapshotDependencySection = '';
      continue;
    }
    if (!snapshot) continue;

    const sectionMatch = line.match(/^ {4}(\S+):$/);
    if (sectionMatch) {
      snapshotDependencySection = ['dependencies', 'optionalDependencies'].includes(sectionMatch[1])
        ? sectionMatch[1]
        : '';
      continue;
    }
    if (!snapshotDependencySection) continue;
    const dependencyMatch = line.match(/^ {6}(\S.*?): (.+)$/);
    if (dependencyMatch) {
      const name = unquoteYAMLKey(dependencyMatch[1]);
      const version = unquoteYAMLKey(dependencyMatch[2]);
      snapshots.get(snapshot).push(`${name}@${version}`);
    }
  }

  if (roots.length === 0) {
    throw new Error('pnpm-lock.yaml root importer contains no production dependencies');
  }
  if (snapshots.size === 0) {
    throw new Error('pnpm-lock.yaml contains no package snapshots');
  }
  return { roots, snapshots };
}

function collectLockfileComponents(projectRoot) {
  const filename = path.join(projectRoot, 'pnpm-lock.yaml');
  const lines = fs.readFileSync(filename, 'utf8').split(/\r?\n/);
  const { roots, snapshots } = parseProductionLockGraph(lines);
  const components = new Map();
  const visited = new Set();
  const pending = [...roots];
  while (pending.length > 0) {
    const snapshot = pending.pop();
    if (visited.has(snapshot)) continue;
    visited.add(snapshot);
    const dependencies = snapshots.get(snapshot);
    if (!dependencies) {
      throw new Error(`production dependency ${JSON.stringify(snapshot)} has no pnpm snapshot`);
    }
    pending.push(...dependencies);

    const parsed = parseLockPackageKey(snapshot);
    const name = parsed.name;
    const version = parsed.version.replace(/\(.*/, '');
    const key = `${name}\0${version}`;
    components.set(key, {
      type: 'library',
      name,
      version,
      purl: `pkg:npm/${encodeURIComponent(name)}@${encodeURIComponent(version)}`
    });
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
  parseProductionLockGraph,
  parseLockPackageKey,
  unquoteYAMLKey
};
