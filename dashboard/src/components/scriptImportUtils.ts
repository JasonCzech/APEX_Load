import { type ImportedScript, type ScriptImportResult } from '../types/scriptImports'

function isObjectRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

export function normalizeScriptImportResult<
  TStats = Record<string, unknown>,
  TCorrelation = unknown,
  TScript extends ImportedScript = ImportedScript,
  TScenarioDraft = unknown,
  TSummary = Record<string, unknown>,
>(response: unknown): ScriptImportResult<TStats, TCorrelation, TScript, TScenarioDraft, TSummary> {
  const payload = isObjectRecord(response) ? response : {}
  const script = payload.script as TScript | undefined
  const scripts = Array.isArray(payload.scripts)
    ? payload.scripts as TScript[]
    : (script ? [script] : [])

  return {
    scripts,
    script,
    scenario_draft: payload.scenario_draft as TScenarioDraft | undefined,
    warnings: Array.isArray(payload.warnings) ? payload.warnings as string[] : [],
    unsupported_elements: Array.isArray(payload.unsupported_elements) ? payload.unsupported_elements as string[] : [],
    assumptions: Array.isArray(payload.assumptions) ? payload.assumptions as string[] : [],
    correlations: Array.isArray(payload.correlations) ? payload.correlations as TCorrelation[] : [],
    stats: (payload.stats || {}) as TStats,
    summary: (payload.summary || {}) as TSummary,
  }
}