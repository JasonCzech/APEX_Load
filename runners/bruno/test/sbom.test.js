'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');

const {
  collectLockfileComponents,
  generateSBOM,
  parseLockPackageKey
} = require('../scripts/sbom');

test('SBOM generator inventories the immutable pnpm lock snapshot', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'apexload-bruno-sbom-'));
  try {
    fs.writeFileSync(path.join(root, 'package.json'), JSON.stringify({
      name: '@apexload/test-runner',
      version: '1.0.0'
    }));
    fs.writeFileSync(path.join(root, 'pnpm-lock.yaml'), `lockfileVersion: '9.0'

importers:
  .: {}

packages:

  '@scope/library@2.0.0':
    resolution: {integrity: sha512-example}
    peerDependencies:
      peer: '*'

  duplicate@1.0.0:
    resolution: {integrity: sha512-example}

  uuid@11.1.1:
    resolution: {integrity: sha512-example}

snapshots:
  uuid@11.1.1: {}
`);

    const components = collectLockfileComponents(root);
    assert.deepEqual(components.map(({ name, version }) => `${name}@${version}`), [
      '@scope/library@2.0.0',
      'duplicate@1.0.0',
      'uuid@11.1.1'
    ]);
    const document = JSON.parse(generateSBOM(root));
    assert.equal(document.metadata.timestamp, '1970-01-01T00:00:00.000Z');
    assert.equal(document.components.length, 3);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test('pnpm package key parsing handles scoped and unscoped names', () => {
  assert.deepEqual(parseLockPackageKey("'@usebruno/cli@3.5.2'"), {
    name: '@usebruno/cli',
    version: '3.5.2'
  });
  assert.deepEqual(parseLockPackageKey('uuid@11.1.1'), {
    name: 'uuid',
    version: '11.1.1'
  });
  assert.throws(() => parseLockPackageKey('invalid'), /unsupported pnpm package key/);
});
