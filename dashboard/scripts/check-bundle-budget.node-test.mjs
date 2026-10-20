import assert from 'node:assert/strict'
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import { gzipSync } from 'node:zlib'

import { formatBundleReport, inspectBundle, inspectRouteBudgets } from './check-bundle-budget.mjs'

async function withBundle(files, callback) {
  const directory = await mkdtemp(join(tmpdir(), 'apexload-bundle-budget-test-'))
  try {
    for (const [path, contents] of Object.entries(files)) {
      const absolutePath = join(directory, path)
      await mkdir(join(absolutePath, '..'), { recursive: true })
      await writeFile(absolutePath, contents)
    }
    await callback(directory)
  } finally {
    await rm(directory, { recursive: true, force: true })
  }
}

const generousBudgets = {
  '.js': {
    label: 'JavaScript',
    maxAssetBytes: 1_000,
    maxAssetGzipBytes: 1_000,
    maxTotalBytes: 2_000,
    maxTotalGzipBytes: 2_000,
  },
  '.css': {
    label: 'CSS',
    maxAssetBytes: 1_000,
    maxAssetGzipBytes: 1_000,
    maxTotalBytes: 2_000,
    maxTotalGzipBytes: 2_000,
  },
}

test('measures JavaScript and CSS recursively in deterministic path order', async () => {
  await withBundle(
    {
      'assets/z.js': 'const z = 1;'.repeat(4),
      'assets/a.js': 'const a = 1;',
      'assets/app.css': '.app { color: red; }',
      'assets/app.js.map': '{"version":3}',
      'logo.svg': '<svg />',
    },
    async (directory) => {
      const result = await inspectBundle(directory, generousBudgets)

      assert.deepEqual(
        result.groups['.js'].assets.map((asset) => asset.path),
        ['assets/a.js', 'assets/z.js'],
      )
      assert.equal(result.groups['.css'].assets.length, 1)
      assert.equal(result.groups['.js'].totalBytes, 'const a = 1;'.length + 'const z = 1;'.repeat(4).length)
      assert.deepEqual(result.violations, [])
      assert.match(formatBundleReport(result), /within the configured regression budgets/)
    },
  )
})

test('reports per-asset and aggregate regressions', async () => {
  await withBundle(
    {
      'assets/app.js': 'abcdefghij',
      'assets/app.css': 'x',
    },
    async (directory) => {
      const budgets = {
        ...generousBudgets,
        '.js': {
          label: 'JavaScript',
          maxAssetBytes: 5,
          maxAssetGzipBytes: 1,
          maxTotalBytes: 8,
          maxTotalGzipBytes: 1,
        },
      }
      const result = await inspectBundle(directory, budgets)

      assert.equal(result.violations.length, 4)
      assert.match(result.violations[0], /JavaScript asset assets\/app\.js/)
      assert.match(result.violations[2], /JavaScript total/)
      assert.match(formatBundleReport(result), /Bundle budget violations:/)
    },
  )
})

test('fails a budget group when the expected asset type is absent', async () => {
  await withBundle({ 'assets/app.js': 'const app = true;' }, async (directory) => {
    const result = await inspectBundle(directory, generousBudgets)

    assert.deepEqual(result.violations, ['CSS: no .css assets found'])
  })
})

test('checks the static route graph without charging deferred dynamic chunks', async () => {
  const manifest = {
    'index.html': {
      file: 'assets/index.js',
      isEntry: true,
      imports: ['_vendor.js'],
      dynamicImports: ['src/DeferredEditor.tsx'],
    },
    '_vendor.js': { file: 'assets/vendor.js' },
    'src/DeferredEditor.tsx': {
      file: 'assets/DeferredEditor-codemirror.js',
      isDynamicEntry: true,
    },
  }
  await withBundle(
    {
      '.vite/manifest.json': JSON.stringify(manifest),
      'assets/index.js': 'const app = true;',
      'assets/vendor.js': 'const vendor = true;',
      'assets/DeferredEditor-codemirror.js': 'const editor = true;',
    },
    async (directory) => {
      const result = await inspectRouteBudgets(directory, [{
        id: 'initial',
        label: 'Initial route',
        entryKey: 'index.html',
        maxGzipBytes: 1_000,
        forbiddenAssets: [/codemirror/i],
      }])

      assert.deepEqual(result.violations, [])
      assert.deepEqual(result.routes[0].assets.map(asset => asset.path).sort(), [
        'assets/index.js',
        'assets/vendor.js',
      ])
    },
  )
})

test('reports oversized and forbidden assets in a static route graph', async () => {
  const manifest = {
    'index.html': { file: 'assets/index.js', isEntry: true, imports: ['_editor.js'] },
    '_editor.js': { file: 'assets/vendor-codemirror.js' },
  }
  await withBundle(
    {
      '.vite/manifest.json': JSON.stringify(manifest),
      'assets/index.js': 'const app = true;',
      'assets/vendor-codemirror.js': 'abcdefghij'.repeat(20),
    },
    async (directory) => {
      const result = await inspectRouteBudgets(directory, [{
        id: 'initial',
        label: 'Initial route',
        entryKey: 'index.html',
        maxGzipBytes: 1,
        forbiddenAssets: [/codemirror/i],
      }])

      assert.equal(result.violations.length, 2)
      assert.match(result.violations[0], /Initial route static JavaScript/)
      assert.match(result.violations[1], /vendor-codemirror/)
    },
  )
})


test('rejects a lazy route importing the shell entry even when assets fit their size limits', async () => {
  const manifest = {
    'index.html': {
      file: 'assets/renamed-shell.js',
      isEntry: true,
      imports: ['_vendor.js'],
      dynamicImports: ['src/ScriptsPage.tsx'],
    },
    '_vendor.js': { file: 'assets/vendor.js' },
    'src/ScriptsPage.tsx': {
      file: 'assets/ScriptsPage.js',
      isDynamicEntry: true,
      imports: ['index.html'],
    },
  }
  await withBundle({
    '.vite/manifest.json': JSON.stringify(manifest),
    'assets/renamed-shell.js': 'const shell = true;',
    'assets/vendor.js': 'const vendor = true;',
    'assets/ScriptsPage.js': 'const scripts = true;',
  }, async directory => {
    const budget = {
      id: 'scripts', label: 'Scripts route', entryKey: 'src/ScriptsPage.tsx',
      maxGzipBytes: 1_000, forbiddenEntryKeys: ['index.html'],
    }
    const result = await inspectRouteBudgets(directory, [budget])
    assert.deepEqual(result.violations, ['Scripts route unexpectedly imports the index.html entry'])
    // Removing the reverse static import preserves the normal dynamic route.
    manifest['src/ScriptsPage.tsx'].imports = ['_vendor.js']
    await writeFile(join(directory, '.vite/manifest.json'), JSON.stringify(manifest))
    assert.deepEqual((await inspectRouteBudgets(directory, [budget])).violations, [])
  })
})


test('deferred Help remains charged to the exact aggregate boundary without entering the startup route', async () => {
  const main = 'export const app = true;'
  const help = 'export default "A deferred native analytics operating contract";'
  const mainBytes = gzipSync(main, { level: 9 }).byteLength
  const totalBytes = mainBytes + gzipSync(help, { level: 9 }).byteLength
  await withBundle({
    '.vite/manifest.json': JSON.stringify({
      'index.html': { file: 'assets/index.js', isEntry: true, dynamicImports: ['docs/NATIVE_ANALYTICS.md'] },
      'docs/NATIVE_ANALYTICS.md': { file: 'assets/NATIVE_ANALYTICS-help.js', isDynamicEntry: true },
    }),
    'assets/index.js': main,
    'assets/NATIVE_ANALYTICS-help.js': help,
    'assets/style.css': '.app {}',
  }, async directory => {
    const routes = [{ id: 'initial', label: 'Initial route', entryKey: 'index.html', maxGzipBytes: mainBytes, forbiddenAssets: [/NATIVE_ANALYTICS/] }]
    const budgets = { ...generousBudgets, '.js': { ...generousBudgets['.js'], maxTotalGzipBytes: totalBytes } }
    const exact = await inspectBundle(directory, budgets, routes)
    assert.deepEqual(exact.violations, [])
    assert.equal(exact.groups['.js'].totalGzipBytes, totalBytes)
    assert.equal(exact.routes[0].totalGzipBytes, mainBytes)
    const exceeded = await inspectBundle(directory, { ...budgets, '.js': { ...budgets['.js'], maxTotalGzipBytes: totalBytes - 1 } }, routes)
    assert.equal(exceeded.violations.length, 1)
    assert.match(exceeded.violations[0], /JavaScript total/)
    assert.deepEqual(exceeded.routes[0].assets.map(asset => asset.path), ['assets/index.js'])
  })
})
