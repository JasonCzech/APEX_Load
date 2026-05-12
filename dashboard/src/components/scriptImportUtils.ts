import { type ImportedScript, type ScriptImportResult } from '../types/scriptImports'

export function normalizeScriptImportResult<
  TStats = Record<string, unknown>,
  TCorrelation = unknown,
  TScript extends ImportedScript = ImportedScript,
  TScenarioDraft = unknown,
  TSummary = Record<string, unknown>,
>(response: any): ScriptImportResult<TStats, TCorrelation, TScript, TScenarioDraft, TSummary> {
  const script = response?.script as TScript | undefined
  const scripts = Array.isArray(response?.scripts)
    ? response.scripts as TScript[]
    : (script ? [script] : [])

  return {
    scripts,
    script,
    scenario_draft: response?.scenario_draft,
    warnings: Array.isArray(response?.warnings) ? response.warnings : [],
    unsupported_elements: Array.isArray(response?.unsupported_elements) ? response.unsupported_elements : [],
    assumptions: Array.isArray(response?.assumptions) ? response.assumptions : [],
    correlations: Array.isArray(response?.correlations) ? response.correlations as TCorrelation[] : [],
    stats: (response?.stats || {}) as TStats,
    summary: (response?.summary || {}) as TSummary,
  }
}