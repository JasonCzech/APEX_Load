import type { ScenarioFormState, ScenarioStartChoice } from './TestHub'
import type { ScenarioSourceMode, ScenarioWizardStep } from './scenarioWizardUtils'

/** Authoring checkpoints are browser-local and can never be used as run payloads. */
export interface ScenarioAuthoringDraft {
  version: 2
  projectId: string
  savedAt: string
  editingId: string | null
  editingUpdatedAt: string
  form: ScenarioFormState
  source: ScenarioSourceMode
  startChoice: ScenarioStartChoice
  shapeId: string
  templateId: string
  cloneId: string
  step: ScenarioWizardStep
  visited: number
}
const key = (project: string) => `apexload.scenario.authoring.v1.${encodeURIComponent(project)}`
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value)

const WIZARD_STEPS: ScenarioWizardStep[] = ['scripts', 'source', 'run', 'data', 'review']
// Version 1 checkpoints were written while the builder opened on a separate Start step.
const V1_WIZARD_STEPS = ['start', 'scripts', 'source', 'run', 'data', 'review']

/**
 * Upgrade a stored checkpoint to the current step order. Version 1 used six steps
 * (Start, Scripts, Shape / Source, …); the model choice now lives on the Model &
 * Shape step, so Start maps to Scripts and the visited index is re-expressed in the
 * five-step order. Returns null for anything that is not a recognizable checkpoint.
 */
export function migrateScenarioAuthoringDraft(value: Record<string, unknown>): Record<string, unknown> | null {
  if (value.version === 2) {
    if (!WIZARD_STEPS.includes(value.step as ScenarioWizardStep) || !Number.isInteger(value.visited) || Number(value.visited) < 0 || Number(value.visited) >= WIZARD_STEPS.length) return null
    return value
  }
  if (value.version !== 1) return null
  if (!V1_WIZARD_STEPS.includes(String(value.step)) || !Number.isInteger(value.visited) || Number(value.visited) < 0 || Number(value.visited) >= V1_WIZARD_STEPS.length) return null
  const toCurrent = (step: string) => (step === 'start' ? 'scripts' : step) as ScenarioWizardStep
  return {
    ...value,
    version: 2,
    step: toCurrent(String(value.step)),
    visited: WIZARD_STEPS.indexOf(toCurrent(V1_WIZARD_STEPS[Number(value.visited)])),
  }
}

// Refuse to persist inline credentials rather than silently dropping authored
// settings. Project-secret references are safe to retain and resolve at launch.
export function assertDraftHasNoInlineCredentials(value: unknown): void {
  const stack: unknown[] = [value]
  let count = 0
  while (stack.length) {
    if (++count > 100_000) throw new Error('The authoring draft is too large to save in this browser.')
    const item = stack.pop()
    if (Array.isArray(item)) { stack.push(...item); continue }
    if (!record(item)) continue
    for (const [name, child] of Object.entries(item)) {
      if (child == null || child === '') continue
      const reference = typeof child === 'string' && /^\{\{secret\.[\w.-]+\}\}$/.test(child)
      if (/^(env|headers|extra_http_headers|cookies|storage_state|additional_vm_parameters|password|token|authorization|client_secret|private_key)$/i.test(name)
        && !reference && (typeof child !== 'object' || Object.keys(child).length > 0)) {
        // Environment maps containing only named secret references are retained.
        if (name === 'env' && record(child) && Object.values(child).every(v => typeof v === 'string' && /^\{\{secret\.[\w.-]+\}\}$/.test(v))) continue
        throw new Error('This draft contains inline runtime credentials or environment values. Use project-secret references before saving browser progress; your edits remain here.')
      }
      if (typeof child === 'string' && /^https?:\/\//i.test(child)) {
        const url = new URL(child)
        if (url.username || url.password || url.search) throw new Error('Remove credentials and query values from runtime URLs before saving browser progress. Your edits remain here.')
      }
      if (child && typeof child === 'object') stack.push(child)
    }
  }
}

export function saveScenarioAuthoringDraft(draft: ScenarioAuthoringDraft): void {
  if (!draft.projectId) throw new Error('Select a project before saving draft progress.')
  assertDraftHasNoInlineCredentials(draft.form)
  const json = JSON.stringify(draft)
  if (json.length > 2_000_000) throw new Error('The authoring draft exceeds the browser draft limit of 2 MB.')
  // Let storage errors reach the UI. Never announce a checkpoint that was not written.
  window.localStorage.setItem(key(draft.projectId), json)
}

export function readScenarioAuthoringDraft(project: string): ScenarioAuthoringDraft | null {
  if (!project) return null
  try {
    const raw = window.localStorage.getItem(key(project))
    if (!raw || raw.length > 2_000_000) return null
    const parsed: unknown = JSON.parse(raw)
    const d = record(parsed) ? migrateScenarioAuthoringDraft(parsed) : null
    if (!d || d.projectId !== project || !record(d.form)) return null
    if (!['savedAt', 'editingUpdatedAt', 'shapeId', 'templateId', 'cloneId'].every(k => typeof d[k] === 'string')) return null
    if (!Number.isFinite(Date.parse(d.savedAt as string)) || (d.editingId !== null && typeof d.editingId !== 'string')) return null
    if (!['shape', 'existing', 'traffic_profile', 'csv_driven'].includes(String(d.source))) return null
    if (!['', 'goal', 'shape', 'existing', 'traffic_profile', 'csv_driven'].includes(String(d.startChoice))) return null
    const f = d.form
    if (typeof f.name !== 'string' || typeof f.duration !== 'string' || !Array.isArray(f.groups) || f.groups.length > 1000) return null
    if (!['start_delay', 'runtime_pacing_type', 'runtime_log_level', 'runtime_auto_transactions', 'assignment_mode'].every(name => typeof f[name] === 'string')) return null
    if (!['script_shakeout', 'resource_baseline', 'runtime_ignore_think_time'].every(name => typeof f[name] === 'boolean')) return null
    if (!['vuser_driven', 'goal_oriented'].includes(String(f.scenario_mode))) return null
    if (!Array.isArray(f.assignment_targets) || !f.assignment_targets.every(value => typeof value === 'string')) return null
    if (!f.groups.every(g => record(g) && typeof g.script_id === 'string' && typeof g.name === 'string'
      && typeof g.ramp_up === 'string' && typeof g.ramp_down === 'string' && ['at_runtime', 'at_once'].includes(String(g.initialization))
      && Number.isFinite(g.vusers) && Array.isArray(g.stages) && g.stages.every(stage => record(stage) && typeof stage.duration === 'string' && typeof stage.type === 'string' && Number.isFinite(stage.target_vusers))
      && (g.runtimeSettings === undefined || record(g.runtimeSettings)) && (g.assignment === undefined || record(g.assignment)))) return null
    if (f.data_pools !== undefined && (!Array.isArray(f.data_pools) || !f.data_pools.every(binding => record(binding) && typeof binding.pool_id === 'string' && typeof binding.alias === 'string' && ['sequential', 'random', 'unique'].includes(String(binding.selection_strategy))))) return null
    if (['runtime_settings', 'goal_config', 'traffic_profile', 'assignment'].some(name => f[name] !== undefined && !record(f[name]))) return null
    assertDraftHasNoInlineCredentials(f)
    return d as unknown as ScenarioAuthoringDraft
  } catch { return null }
}

export function clearScenarioAuthoringDraft(project: string): void {
  window.localStorage.removeItem(key(project))
}
