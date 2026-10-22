import type { JointEvidenceProjection } from '../types/jointEvidence'
import type { ScriptResultsConfig } from '../types/scripts'
import type { StoredScript } from '../types/storedContent'

// The group settings that decide what a group promotes (a TestHub form or a
// run draft).
type PromotionSource = {
  groups: ReadonlyArray<{ script_id: string; runtimeSettings?: { results?: ScriptResultsConfig } }>
  runtime_settings?: { results?: ScriptResultsConfig }
}

// The controller's projectable dimensions (engine.JointDimensions), in its
// order, besides one promoted property category ("property:<name>").
export const JOINT_EVIDENCE_DIMENSIONS: ReadonlyArray<{ id: string; label: string }> = [
  { id: 'name', label: 'Name' },
  { id: 'protocol', label: 'Protocol' },
  { id: 'group', label: 'Group' },
  { id: 'region', label: 'Region' },
  { id: 'dependency', label: 'Dependency' },
  { id: 'page', label: 'Page' },
  { id: 'hierarchy', label: 'Hierarchy' },
  { id: 'phase', label: 'Phase' },
  { id: 'network_profile', label: 'Network profile' },
  { id: 'browser_profile', label: 'Browser profile' },
  { id: 'browser_engine', label: 'Browser engine' },
  { id: 'browser_platform', label: 'Browser platform' },
]

export const JOINT_EVIDENCE_MAX_DIMENSIONS = 3
const PROPERTY_PREFIX = 'property:'
// The engine's own bounded category: whether a browser iteration started
// cold or warm. Scripts cannot promote it; the controller always admits it.
export const BROWSER_ITERATION_CATEGORY = 'browser_iteration'

export function jointPropertyCategory(dimension: string): string | null {
  return dimension.startsWith(PROPERTY_PREFIX) ? dimension.slice(PROPERTY_PREFIX.length) : null
}

/** How a property category is named to the user. */
export function jointPropertyCategoryLabel(category: string): string {
  return category === BROWSER_ITERATION_CATEGORY ? 'Browser iteration (cold/warm)' : `Property: ${category}`
}

/**
 * The property categories the scenario's groups promote, as the controller
 * resolves them: a group's own runtime results settings, else the
 * scenario's, else its script's; plus the browser iteration category when a
 * group runs a browser script.
 */
export function promotedPropertyCategories(form: PromotionSource, scripts: ReadonlyArray<Pick<StoredScript, 'id' | 'protocol' | 'script'>>): string[] {
  const names = new Set<string>()
  for (const group of form.groups) {
    const script = scripts.find(item => item.id === group.script_id)
    const results = group.runtimeSettings?.results ?? form.runtime_settings?.results ?? script?.script?.config?.results
    for (const rule of results?.property_analytics ?? []) {
      if (rule.name) names.add(rule.name)
    }
    if (script?.protocol === 'browser') names.add(BROWSER_ITERATION_CATEGORY)
  }
  return [...names].sort()
}

/**
 * Selects or clears one dimension. At most three are kept, and a property
 * category replaces any other: a projection groups by one category.
 */
export function toggleJointEvidenceDimension(projection: JointEvidenceProjection, dimension: string, selected: boolean): JointEvidenceProjection {
  const others = projection.dimensions.filter(item => item !== dimension)
  if (!selected) return { ...projection, dimensions: others }
  const kept = jointPropertyCategory(dimension) === null ? others : others.filter(item => jointPropertyCategory(item) === null)
  if (kept.length >= JOINT_EVIDENCE_MAX_DIMENSIONS) return projection
  return { ...projection, dimensions: [...kept, dimension] }
}

/** A projection in words, e.g. "Actions by Name, Group and Property: tier". */
export function describeJointEvidence(projection: JointEvidenceProjection): string {
  const labels = projection.dimensions.map(dimension => {
    const category = jointPropertyCategory(dimension)
    return category !== null ? jointPropertyCategoryLabel(category) : JOINT_EVIDENCE_DIMENSIONS.find(item => item.id === dimension)?.label ?? dimension
  })
  const list = labels.length > 1 ? `${labels.slice(0, -1).join(', ')} and ${labels[labels.length - 1]}` : labels[0] ?? ''
  return `${projection.population === 'transaction' ? 'Business transactions' : 'Actions'} by ${list}`
}

/** Why the scenario cannot be saved with this projection, or ''. */
export function jointEvidenceIssue(projection: JointEvidenceProjection | undefined, promoted: ReadonlyArray<string>): string {
  if (!projection) return ''
  if (projection.dimensions.length === 0) return 'Choose at least one joint evidence dimension, or turn joint evidence off.'
  const missing = projection.dimensions.map(jointPropertyCategory).find(name => name !== null && name !== BROWSER_ITERATION_CATEGORY && !promoted.includes(name))
  return missing ? `The property category "${missing}" is not promoted by any of the scenario's scripts; choose a promoted category or remove it.` : ''
}
