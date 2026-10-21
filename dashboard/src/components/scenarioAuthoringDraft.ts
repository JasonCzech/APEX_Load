import type { ScenarioFormState, ScenarioStartChoice } from '../types/scenarioAuthoring'
import type { ScenarioSourceMode, ScenarioWizardStep } from './scenarioWizardUtils'
import { legacyFlatFormFieldsValid, migrateLegacyFlatForm } from './scenarioAuthoringDraftV2'
import { emptyScenarioForm, emptyScenarioGroup } from './scenarioFormModel'
import { scenarioGroupNameAfterScriptSelection } from './scenarioGroupUtils'
import { isAuthoringCreateIntent, type AuthoringCreateIntent } from './authoringCreateIntent'

/** Authoring checkpoints are browser-local and can never be used as run payloads. */
export interface ScenarioAuthoringDraft {
  version: 3
  projectId: string
  savedAt: string
  editingId: string | null
  editingUpdatedAt: string
  /**
   * The revision tag, form and workload shape the edit was loaded from. A
   * resumed edit adopts a durable baseline only when its tag still matches;
   * otherwise the save reconciles from this original. Older checkpoints
   * without a tag fall back to the listed update time.
   */
  baselineTag?: string
  baselineForm?: ScenarioFormState
  baselineShapeId?: string
  form: ScenarioFormState
  source: ScenarioSourceMode
  startChoice: ScenarioStartChoice
  shapeId: string
  templateId: string
  cloneId: string
  step: ScenarioWizardStep
  visited: number
  /**
   * In a browser checkpoint only: the project draft this work continues, so
   * its next Save updates that draft rather than adding another.
   */
  projectDraft?: { id: string; revision: number }
  /**
   * In a browser checkpoint of a new scenario only: the create intent its
   * saves send (AUTH-1), so a save retried after a reload replays the
   * scenario an earlier attempt created instead of creating another. Never
   * part of a project draft.
   */
  createIntent?: AuthoringCreateIntent
}
const key = (project: string) => `apexload.scenario.authoring.v1.${encodeURIComponent(project)}`
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value)

const WIZARD_STEPS: ScenarioWizardStep[] = ['scripts', 'source', 'run', 'data', 'review']
// Version 1 checkpoints were written while the builder opened on a separate Start step.
const V1_WIZARD_STEPS = ['start', 'scripts', 'source', 'run', 'data', 'review']

// New Scenario Builder work with one group per script, one virtual user each,
// opened at Model & Shape. Saved as a project draft, it can hold scripts that
// cannot run yet, which a saved scenario refuses. Each group is named for its
// script as the builder names one, so scripts sharing a name get distinct groups.
export function scenarioAuthoringDraftFromScripts(projectId: string, name: string, scripts: ReadonlyArray<{ id: string; name: string }>): ScenarioAuthoringDraft {
  const groups: ScenarioFormState['groups'] = []
  scripts.forEach((script, index) => {
    const group = emptyScenarioGroup(script.id, index)
    groups.push({ ...group, name: scenarioGroupNameAfterScriptSelection(group.name, index, script.name, '', groups.map(item => item.name)), vusers: 1 })
  })
  return {
    version: 3,
    projectId,
    savedAt: new Date().toISOString(),
    editingId: null,
    editingUpdatedAt: '',
    form: { ...emptyScenarioForm(), name, groups },
    source: 'shape',
    startChoice: '',
    shapeId: '',
    templateId: '',
    cloneId: '',
    step: 'source',
    visited: WIZARD_STEPS.indexOf('source'),
  }
}

/**
 * Upgrade a stored checkpoint to the current version. Version 1 used six steps
 * (Start, Scripts, Shape / Source, …); the model choice now lives on the Model &
 * Shape step, so Start maps to Scripts and the visited index is re-expressed in the
 * five-step order. Version 2 forms carried flat mirrors beside the canonical
 * runtime override, goal config and assignment; version 3 forms carry only the
 * canonical values, so the mirrors are composed into them
 * (scenarioAuthoringDraftV2). Returns null for
 * anything that is not a recognizable checkpoint.
 */
export function migrateScenarioAuthoringDraft(value: Record<string, unknown>): Record<string, unknown> | null {
  if (value.version === 3) {
    if (!WIZARD_STEPS.includes(value.step as ScenarioWizardStep) || !Number.isInteger(value.visited) || Number(value.visited) < 0 || Number(value.visited) >= WIZARD_STEPS.length) return null
    return value
  }
  if (value.version === 2) {
    if (!record(value.form) || !legacyFlatFormFieldsValid(value.form)) return null
    const baseline = record(value.baselineForm) && legacyFlatFormFieldsValid(value.baselineForm) ? migrateLegacyFlatForm(value.baselineForm) : value.baselineForm
    return migrateScenarioAuthoringDraft({ ...value, version: 3, form: migrateLegacyFlatForm(value.form), ...(value.baselineForm !== undefined ? { baselineForm: baseline } : {}) })
  }
  if (value.version !== 1) return null
  if (!V1_WIZARD_STEPS.includes(String(value.step)) || !Number.isInteger(value.visited) || Number(value.visited) < 0 || Number(value.visited) >= V1_WIZARD_STEPS.length) return null
  const toCurrent = (step: string) => (step === 'start' ? 'scripts' : step) as ScenarioWizardStep
  return migrateScenarioAuthoringDraft({
    ...value,
    version: 2,
    step: toCurrent(String(value.step)),
    visited: WIZARD_STEPS.indexOf(toCurrent(V1_WIZARD_STEPS[Number(value.visited)])),
  })
}

// Refuse to persist inline credentials rather than silently dropping authored
// settings. Project-secret references are safe to retain and resolve at launch.
export function assertDraftHasNoInlineCredentials(value: unknown): void {
  const stack: unknown[] = [value]
  let count = 0
  while (stack.length) {
    if (++count > 100_000) throw new Error('The draft is too large to save.')
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
        throw new Error('This draft contains inline runtime credentials or environment values. Use project-secret references before saving; your edits remain here.')
      }
      if (typeof child === 'string' && /^https?:\/\//i.test(child)) {
        const url = new URL(child)
        if (url.username || url.password || url.search) throw new Error('Remove credentials and query values from runtime URLs before saving. Your edits remain here.')
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
    return parseScenarioAuthoringDraft(JSON.parse(raw), project)
  } catch {
    return null
  }
}

/**
 * A draft document, from this browser or saved to the project, upgraded to the
 * current version, or null when it is not a usable draft for the project.
 */
export function parseScenarioAuthoringDraft(parsed: unknown, project: string): ScenarioAuthoringDraft | null {
  if (!project) return null
  try {
    const d = record(parsed) ? migrateScenarioAuthoringDraft(parsed) : null
    if (!d || d.projectId !== project || !record(d.form)) return null
    if (!['savedAt', 'editingUpdatedAt', 'shapeId', 'templateId', 'cloneId'].every(k => typeof d[k] === 'string')) return null
    if (!Number.isFinite(Date.parse(d.savedAt as string)) || (d.editingId !== null && typeof d.editingId !== 'string')) return null
    if (!['shape', 'existing', 'traffic_profile', 'csv_driven'].includes(String(d.source))) return null
    if (!['', 'goal', 'shape', 'existing', 'traffic_profile', 'csv_driven'].includes(String(d.startChoice))) return null
    const f = d.form
    if (typeof f.name !== 'string' || typeof f.duration !== 'string' || !Array.isArray(f.groups) || f.groups.length > 1000) return null
    if (typeof f.start_delay !== 'string') return null
    if (!['script_shakeout', 'resource_baseline'].every(name => typeof f[name] === 'boolean')) return null
    if (!['vuser_driven', 'goal_oriented'].includes(String(f.scenario_mode))) return null
    if (!f.groups.every(g => record(g) && typeof g.script_id === 'string' && typeof g.name === 'string'
      && typeof g.ramp_up === 'string' && typeof g.ramp_down === 'string' && ['at_runtime', 'at_once'].includes(String(g.initialization))
      && Number.isFinite(g.vusers) && Array.isArray(g.stages) && g.stages.every(stage => record(stage) && typeof stage.duration === 'string' && typeof stage.type === 'string' && Number.isFinite(stage.target_vusers))
      && (g.runtimeSettings === undefined || record(g.runtimeSettings)) && (g.assignment === undefined || record(g.assignment)))) return null
    // Legacy checkpoints used the scalar fields as an overlay on the first
    // retained binding. Field presence distinguishes them from current drafts,
    // whose canonical-only arrays must remain unchanged.
    if (Object.prototype.hasOwnProperty.call(f, 'data_pool_id')) {
      if (typeof f.data_pool_id !== 'string' || (f.data_pools !== undefined && !Array.isArray(f.data_pools))) return null
      const strategy = f.data_pool_selection_strategy ?? 'sequential'
      if (!['sequential', 'random', 'unique'].includes(String(strategy)) || (f.data_pool_alias !== undefined && typeof f.data_pool_alias !== 'string')) return null
      const retained = Array.isArray(f.data_pools) ? f.data_pools : []
      f.data_pools = f.data_pool_id ? [{
        ...(record(retained[0]) ? retained[0] : {}),
        pool_id: f.data_pool_id,
        alias: f.data_pool_alias || '',
        access_mode: 'read_only',
        selection_strategy: strategy,
      }, ...retained.slice(1)] : retained.slice(1)
    }
    for (const name of ['data_pool_id','data_pool_alias','data_pool_access_mode','data_pool_selection_strategy']) delete f[name]
    if (f.data_pools !== undefined && (!Array.isArray(f.data_pools) || !f.data_pools.every(binding => record(binding) && typeof binding.pool_id === 'string' && typeof binding.alias === 'string' && (binding.access_mode === 'live' ? ['manual', 'next', 'pop', 'lease'] : ['sequential', 'random', 'unique']).includes(String(binding.selection_strategy))))) return null
    if (['runtime_settings', 'goal_config', 'traffic_profile', 'assignment'].some(name => f[name] !== undefined && !record(f[name]))) return null
    assertDraftHasNoInlineCredentials(f)
    // An intent belongs to a new scenario in this project; anything else is dropped.
    if (d.createIntent !== undefined && !(d.editingId === null && isAuthoringCreateIntent(d.createIntent) && d.createIntent.purpose === 'scenario_create' && d.createIntent.projectId === project)) delete d.createIntent
    const link = d.projectDraft
    if (link !== undefined && !(record(link) && typeof link.id === 'string' && link.id && Number.isInteger(link.revision) && (link.revision as number) > 0)) delete d.projectDraft
    // A recorded baseline that does not look like one is dropped: the draft
    // then resumes like an older checkpoint.
    const b = d.baselineForm
    if (d.baselineTag !== undefined && (typeof d.baselineTag !== 'string' || !d.baselineTag || !record(b) || typeof b.name !== 'string' || !Array.isArray(b.groups) || typeof d.baselineShapeId !== 'string')) {
      delete d.baselineTag; delete d.baselineForm; delete d.baselineShapeId
    }
    return d as unknown as ScenarioAuthoringDraft
  } catch { return null }
}

export function clearScenarioAuthoringDraft(project: string): void {
  window.localStorage.removeItem(key(project))
}
