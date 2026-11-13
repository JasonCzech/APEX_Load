import { useMemo } from 'react'
import './CanonicalRuntimeGroupRail.css'
import {
  CANONICAL_RUNTIME_GROUPS,
  getCanonicalRuntimeGroup,
  getCanonicalRuntimeGroupsForProtocol,
  RUNTIME_VALUE_PROVENANCE_LABELS,
  type CanonicalCatalogFieldRef,
  type CanonicalRuntimeGroupDefinition,
  type CanonicalRuntimeGroupId,
  type RuntimeSettingsEditorMode,
  type RuntimeValueProvenance,
} from './canonicalRuntimeTaxonomy'
import {
  canonicalGroupMatchesSearch,
  canonicalGroupSearchFieldMatches,
  overridePathsByCanonicalGroup,
  RUNTIME_EDITOR_MODE_LABELS,
} from './canonicalRuntimeGroupSearch'

// The canonical 7-group header (mode and protocol badges, search, provenance
// legend and group rail) shared by every runtime settings surface. It lives
// apart from RuntimeSettingsEditor so the scenario and group override editors
// can mount it without loading the Author view.

const PROVENANCE_MODES: readonly RuntimeSettingsEditorMode[] = ['debug', 'scenario']

export interface CanonicalRuntimeGroupRailProps {
  mode: RuntimeSettingsEditorMode
  /** Normalized protocol; decides which groups the rail offers. */
  protocol: string
  currentGroupId: CanonicalRuntimeGroupId
  search: string
  onSearchChange: (search: string) => void
  /** Called with the pill clicked and the paths it stands for (search hits, else overridden paths). */
  onSelectGroup: (groupId: CanonicalRuntimeGroupId, focusPaths: string[]) => void
  catalogFields?: readonly CanonicalCatalogFieldRef[]
  provenance?: Readonly<Record<string, RuntimeValueProvenance>>
  /** Restricts the rail to these groups (after protocol support), e.g. to the panels a surface has. */
  availableGroupIds?: readonly CanonicalRuntimeGroupId[]
}

export function CanonicalRuntimeGroupRail({
  mode,
  protocol,
  currentGroupId,
  search,
  onSearchChange,
  onSelectGroup,
  catalogFields,
  provenance,
  availableGroupIds,
}: CanonicalRuntimeGroupRailProps) {
  const supportedGroups = useMemo(
    () => getCanonicalRuntimeGroupsForProtocol(protocol, mode)
      .filter(group => !availableGroupIds || availableGroupIds.includes(group.id)),
    [protocol, mode, availableGroupIds],
  )
  const showProvenance = PROVENANCE_MODES.includes(mode)
  const overridePaths = useMemo(
    () => (showProvenance ? overridePathsByCanonicalGroup(provenance, catalogFields) : {}),
    [showProvenance, provenance, catalogFields],
  )
  const visibleGroups = supportedGroups.filter(group =>
    canonicalGroupMatchesSearch(group, search, catalogFields),
  )
  const currentGroup = getCanonicalRuntimeGroup(currentGroupId)
  // A pill standing for specific fields (search hits, else overridden paths)
  // opens the panel that renders them, not only the group's default panel.
  const focusPathsForGroup = (group: CanonicalRuntimeGroupDefinition): string[] =>
    search.trim()
      ? canonicalGroupSearchFieldMatches(group, search, catalogFields)
      : overridePaths[group.id] ?? []

  return (
    <div className="runtime-canonical-header">
      <div className="runtime-canonical-header-meta">
        <span
          className="repository-status-pill"
          data-testid="runtime-editor-mode-badge"
        >
          {RUNTIME_EDITOR_MODE_LABELS[mode]}
        </span>
        <span
          className="repository-status-pill"
          data-testid="runtime-editor-protocol-badge"
        >
          {protocol}
        </span>
        <span
          className="text-muted"
          data-testid="runtime-editor-active-group-summary"
        >
          {currentGroup.label} — {currentGroup.description}
        </span>
      </div>
      <input
        type="search"
        autoComplete="off"
        className="runtime-canonical-group-search"
        aria-label="Search runtime settings groups"
        placeholder="Search groups and settings"
        data-testid="runtime-settings-canonical-group-search"
        value={search}
        onChange={event => onSearchChange(event.target.value)}
      />
      {showProvenance && (
        <ul
          className="runtime-canonical-provenance-legend"
          aria-label="Value provenance"
          data-testid="runtime-settings-provenance-legend"
        >
          {(Object.keys(RUNTIME_VALUE_PROVENANCE_LABELS) as RuntimeValueProvenance[]).map(source => (
            <li key={source} data-provenance={source}>
              <span className={`repository-status-pill provenance-${source}`}>
                {RUNTIME_VALUE_PROVENANCE_LABELS[source]}
              </span>
            </li>
          ))}
        </ul>
      )}
      <nav
        className="runtime-canonical-groups-rail"
        aria-label="Canonical runtime settings groups"
        data-testid="runtime-settings-canonical-groups"
      >
        {CANONICAL_RUNTIME_GROUPS.map((group, index) => {
          if (!visibleGroups.some(item => item.id === group.id)) {
            return null
          }
          const isActive = group.id === currentGroupId
          const groupOverridePaths = overridePaths[group.id] ?? []
          const overrides = groupOverridePaths.length
          return (
            <button
              key={group.id}
              type="button"
              className={`runtime-canonical-group-pill ${isActive ? 'active' : ''}`}
              data-testid={`runtime-settings-canonical-group-${group.id}`}
              data-canonical-group={group.id}
              aria-pressed={isActive}
              onClick={() => onSelectGroup(group.id, focusPathsForGroup(group))}
            >
              <span className="runtime-canonical-group-index">{index + 1}.</span>
              <span>{group.label}</span>
              {overrides > 0 && (
                <span
                  className="repository-status-pill"
                  data-testid={`runtime-settings-canonical-group-overrides-${group.id}`}
                  title={groupOverridePaths.join(', ')}
                >
                  {overrides === 1 ? '1 override' : `${overrides} overrides`}
                </span>
              )}
            </button>
          )
        })}
        {visibleGroups.length === 0 && (
          <span className="text-muted" role="status" data-testid="runtime-settings-canonical-group-search-empty">
            No runtime settings groups match this search.
          </span>
        )}
      </nav>
    </div>
  )
}

export default CanonicalRuntimeGroupRail
