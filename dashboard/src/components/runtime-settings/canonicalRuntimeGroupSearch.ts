import {
  getCanonicalGroupForPath,
  isOverrideProvenance,
  type CanonicalCatalogFieldRef,
  type CanonicalRuntimeGroupDefinition,
  type CanonicalRuntimeGroupId,
  type RuntimeSettingsEditorMode,
  type RuntimeValueProvenance,
} from './canonicalRuntimeTaxonomy'

// Search and override grouping behind the canonical runtime settings rail.

/**
 * The config paths of a group that a rail search hits by path or catalog
 * label. A path counts for the group it resolves to (catalog first), so a
 * pill is never credited with a field the catalog places elsewhere.
 */
export function canonicalGroupSearchFieldMatches(
  group: CanonicalRuntimeGroupDefinition,
  query: string,
  catalogFields?: readonly CanonicalCatalogFieldRef[],
): string[] {
  const needle = query.trim().toLowerCase()
  if (!needle) {
    return []
  }
  const candidates: Array<{ path: string; label: string }> = group.configPaths.map(path => ({ path, label: '' }))
  for (const field of catalogFields ?? []) {
    if (field.group === group.id) {
      candidates.push({ path: field.path, label: field.label ?? '' })
    }
  }
  const matches: string[] = []
  for (const { path, label } of candidates) {
    if (!path.toLowerCase().includes(needle) && !label.toLowerCase().includes(needle)) continue
    if (getCanonicalGroupForPath(path, catalogFields) !== group.id) continue
    if (!matches.includes(path)) matches.push(path)
  }
  return matches
}

/** Whether a canonical group matches a rail search by label, description, or field label or path. */
export function canonicalGroupMatchesSearch(
  group: CanonicalRuntimeGroupDefinition,
  query: string,
  catalogFields?: readonly CanonicalCatalogFieldRef[],
): boolean {
  const needle = query.trim().toLowerCase()
  if (!needle) {
    return true
  }
  if ([group.label, group.description].some(text => text.toLowerCase().includes(needle))) {
    return true
  }
  return canonicalGroupSearchFieldMatches(group, needle, catalogFields).length > 0
}

/** Override-provenance paths per canonical group. */
export function overridePathsByCanonicalGroup(
  provenance?: Readonly<Record<string, RuntimeValueProvenance>>,
  catalogFields?: readonly CanonicalCatalogFieldRef[],
): Partial<Record<CanonicalRuntimeGroupId, string[]>> {
  const paths: Partial<Record<CanonicalRuntimeGroupId, string[]>> = {}
  for (const [path, source] of Object.entries(provenance ?? {})) {
    if (!isOverrideProvenance(source)) continue
    const groupId = getCanonicalGroupForPath(path, catalogFields)
    if (!groupId) continue
    ;(paths[groupId] ??= []).push(path)
  }
  return paths
}

/** Count override-provenance paths per canonical group. */
export function countOverridesByCanonicalGroup(
  provenance?: Readonly<Record<string, RuntimeValueProvenance>>,
  catalogFields?: readonly CanonicalCatalogFieldRef[],
): Partial<Record<CanonicalRuntimeGroupId, number>> {
  const counts: Partial<Record<CanonicalRuntimeGroupId, number>> = {}
  for (const [groupId, paths] of Object.entries(overridePathsByCanonicalGroup(provenance, catalogFields))) {
    counts[groupId as CanonicalRuntimeGroupId] = paths?.length ?? 0
  }
  return counts
}

export const RUNTIME_EDITOR_MODE_LABELS: Readonly<Record<RuntimeSettingsEditorMode, string>> = {
  author: 'Author VUsers runtime settings',
  import: 'Imported VUsers runtime settings',
  debug: 'Debug VUsers session overrides',
  scenario: 'Scenario VUsers runtime overrides',
}

