import { describe, expect, it } from 'vitest'

// Runtime override rows may be PARTIAL: override_fields lists the only paths the row applies
// (pkg/api/runtime_overrides.go), while a v1 row applies every section it carries whole. A
// display or baseline read of `row.pacing` (or any other section or compact alias) ignores that
// and shows a partial row's few leaves as the whole section. These files therefore read override
// rows only through the shared resolvers in runtimeSettingsModel.ts (resolveRuntimeRowsValue,
// effectiveRuntimeSection, runtimeSectionSource, runtimeRowCoversPath, runtimeBrowserSettings)
// or the thin wrappers over them (runtimeLayerValue / runtimeLayerSection in newTestRunModel.ts).
// A v1-only write path that must touch a section directly is marked `runtime-row-v1-write`.

async function files() {
  // @ts-expect-error Source contracts run in Node; application sources omit Node types.
  return await import('node:fs')
}

const GUARDED_FILES = [
  'newTestRunModel.ts',
  'NewTestPage.tsx',
  'NewTestReadiness.tsx',
  'TestExecutionScenarioPreview.tsx',
  'TestHub.tsx',
  'GroupRuntimeSettingsDialog.tsx',
  'ExecutionPlansPanel.tsx',
  'ScenarioRowSummary.tsx',
  'TestExecutionArchive.tsx',
  'ResultsPage.tsx',
  'browserCaptureSettings.ts',
  'jointEvidenceModel.ts',
]

// Not guarded, by design: groupRuntimeSettingsModel.ts reads sections by presence, but only of
// (a) the server's fully resolved effective_settings projection (GroupRuntimeSettingsDialog's
// "Effective value" column), which is a ScriptConfig view and never an override row, and (b) the
// dialog's save diff, which compares two payloads the dialog itself built field by field. Neither
// reads a stored partial row as a whole section; layer sources go through runtimeRowCoversPath.

// A call argument list with at most one level of nested parentheses.
const CALL_ARGS = String.raw`\((?:[^()]|\([^()]*\))*\)`
// Identifiers that hold an override row in these files, the generic row parameter names the
// display helpers use (settings, row, layer), and the helpers that build a row from a form.
const ROW = String.raw`(?:\b(?:runtime_settings|runtimeSettings|runtime_overrides|rootRuntime|groupRuntime|scenarioRuntime|overrideRuntime|effectiveRootRuntime|effective|test_runtime_settings|settings|row|layer)\b|\b(?:runtimeSettingsFromForm|overrideFromRuntimeDraft)${CALL_ARGS})`
const SECTION = String.raw`(?:pacing|think_time|error_handling|log|log_level|run_logic|iterations|auto_transactions|http|soap|browser_config|browser|browser_emulation|results|web_runtime|websocket|java_vuser|base_url|env|redaction_rules)`
const DIRECT_SECTION_READ = new RegExp([
  // row.pacing / row?.pacing
  String.raw`${ROW}\??\.${SECTION}\b`,
  // row['pacing'] / row?.['pacing']
  String.raw`${ROW}(?:\?\.)?\[\s*['"\x60]${SECTION}['"\x60]\s*\]`,
  // const { pacing } = row
  String.raw`\{[^}]*\b${SECTION}\b[^}]*\}\s*=\s*(?:[\w$]+\??\.)*${ROW}`,
].join('|'))
const MARKER = 'runtime-row-v1-write'
// The masked script-settings projection (a ScriptConfig view, never an override row).
const PROJECTION_MARKER = 'runtime-settings-projection'
const MARKERS = [MARKER, PROJECTION_MARKER]

/** Lines that read a section directly. A line carrying a marker is exempt, as is every line
 * between `// <marker>-begin` and `// <marker>-end` (a whole v1 write helper). */
function directSectionReads(source: string): string[] {
  let exemptBlock = false
  const hits: string[] = []
  source.split('\n').forEach((line, index) => {
    if (MARKERS.some(marker => line.includes(`${marker}-begin`))) { exemptBlock = true; return }
    if (MARKERS.some(marker => line.includes(`${marker}-end`))) { exemptBlock = false; return }
    if (exemptBlock) return
    const trimmed = line.trim()
    if (trimmed.startsWith('//') || trimmed.startsWith('*') || trimmed.startsWith('/*')) return
    if (MARKERS.some(marker => line.includes(marker))) return
    if (DIRECT_SECTION_READ.test(line)) hits.push(`${index + 1}: ${trimmed}`)
  })
  return hits
}

describe('runtime override display reads', () => {
  it('flags a direct section read and spares resolver calls', () => {
    expect(directSectionReads('const p = group.runtime_settings?.pacing')).toHaveLength(1)
    expect(directSectionReads('const t = effective?.think_time ?? x')).toHaveLength(1)
    expect(directSectionReads('formatPacing(runtimeSettingsFromForm(form).pacing)')).toHaveLength(1)
    expect(directSectionReads("effectiveRuntimeSection('pacing', [group.runtime_settings, test.runtime_settings])")).toHaveLength(0)
    expect(directSectionReads('const p = effectiveRuntime.pacing')).toHaveLength(0)
    expect(directSectionReads(`settings.pacing = next // ${MARKER}`)).toHaveLength(0)
  })

  it('flags destructuring, bracket access, generic row names and row-building calls', () => {
    expect(directSectionReads('const { pacing } = group.runtime_settings')).toHaveLength(1)
    expect(directSectionReads('const { think_time: t, log } = row')).toHaveLength(1)
    expect(directSectionReads("const p = row['pacing']")).toHaveLength(1)
    expect(directSectionReads('const p = layer?.["error_handling"]')).toHaveLength(1)
    expect(directSectionReads('const p = settings.run_logic')).toHaveLength(1)
    expect(directSectionReads('overrideFromRuntimeDraft(runtimeSettingsFromForm(form))?.error_handling')).toHaveLength(1)
    expect(directSectionReads('const { label } = settings')).toHaveLength(0)
    expect(directSectionReads('const p = rowCount.pacing')).toHaveLength(0)
  })

  it('exempts a marked block and nothing after it', () => {
    const block = [`// ${MARKER}-begin`, 'settings.run_logic = { ...settings.run_logic }', `// ${MARKER}-end`, 'const p = settings.pacing'].join('\n')
    expect(directSectionReads(block)).toEqual(['4: const p = settings.pacing'])
  })

  for (const file of GUARDED_FILES) {
    it(`${file} reads override sections only through the shared resolvers`, async () => {
      const source = (await files()).readFileSync(new URL(file, import.meta.url), 'utf8') as string
      expect(directSectionReads(source)).toEqual([])
    })
  }
})
