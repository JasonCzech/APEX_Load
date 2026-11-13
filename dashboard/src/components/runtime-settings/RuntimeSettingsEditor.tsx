import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { ScriptConfig, ScriptDefinition } from '../../types/scripts'
import type { DebugRuntimeContext } from '../RuntimeContextSummary'
import type {
  HTTPTransportTabId,
  RuntimeSettingsSectionId,
} from '../scriptEditorRuntimeSettingsModel'
import ScriptRuntimeSettingsView from '../ScriptRuntimeSettingsView'
import {
  CANONICAL_RUNTIME_GROUPS,
  getCanonicalGroupDestinationForPaths,
  getCanonicalGroupForPath,
  getCanonicalGroupForSection,
  getCanonicalRuntimeGroup,
  getCanonicalRuntimeGroupsForProtocol,
  isLocationInCanonicalGroup,
  isOverrideProvenance,
  normalizeRuntimeProtocol,
  RUNTIME_VALUE_PROVENANCE_LABELS,
  type CanonicalCatalogFieldRef,
  type CanonicalRuntimeGroupDefinition,
  type CanonicalRuntimeGroupId,
  type RuntimeFieldLocation,
  type RuntimeSettingsEditorMode,
  type RuntimeValueProvenance,
} from './canonicalRuntimeTaxonomy'

export interface RuntimeSettingsEditorProps {
  mode?: RuntimeSettingsEditorMode
  protocol?: string
  script?: ScriptDefinition
  config?: ScriptConfig
  baselineConfig?: ScriptConfig
  debugContext?: DebugRuntimeContext
  activeGroupId?: CanonicalRuntimeGroupId
  onSelectGroup?: (groupId: CanonicalRuntimeGroupId) => void
  activeSection?: RuntimeSettingsSectionId
  onSelectSection?: (section: RuntimeSettingsSectionId) => void
  activeHTTPTransportTab?: HTTPTransportTabId
  onSelectHTTPTransportTab?: (tab: HTTPTransportTabId) => void
  onUpdateScript?: (updater: (prev: ScriptDefinition) => ScriptDefinition) => void
  onChangeConfig?: (updater: (prev: ScriptConfig) => ScriptConfig) => void
  hideSave?: boolean
  onSave?: () => void
  saveDisabled?: boolean
  saveTitle?: string
  saving?: boolean
  /** Backend catalog fields (GET /api/v1/dsl/config-catalog); refine search and provenance grouping. */
  catalogFields?: readonly CanonicalCatalogFieldRef[]
  /** Effective-value provenance by config path, shown as legend and per-group badges in debug and scenario modes. */
  provenance?: Readonly<Record<string, RuntimeValueProvenance>>
  children?: ReactNode
}

const PROVENANCE_MODES: readonly RuntimeSettingsEditorMode[] = ['debug', 'scenario']

/**
 * The config paths of a group that a rail search hits by path or catalog
 * label. A path counts for the group it resolves to (catalog first), so a
 * pill is never credited with a field the catalog places elsewhere.
 */
function canonicalGroupSearchFieldMatches(
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
function canonicalGroupMatchesSearch(
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
function overridePathsByCanonicalGroup(
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

const MODE_LABELS: Record<RuntimeSettingsEditorMode, string> = {
  author: 'Author VUsers runtime settings',
  import: 'Imported VUsers runtime settings',
  debug: 'Debug VUsers session overrides',
  scenario: 'Scenario VUsers runtime overrides',
}

export function RuntimeSettingsEditor({
  mode = 'author',
  protocol,
  script,
  config,
  baselineConfig,
  debugContext,
  activeGroupId,
  onSelectGroup,
  activeSection,
  onSelectSection,
  activeHTTPTransportTab,
  onSelectHTTPTransportTab,
  onUpdateScript,
  onChangeConfig,
  hideSave = true,
  onSave,
  saveDisabled = false,
  saveTitle,
  saving = false,
  catalogFields,
  provenance,
  children,
}: RuntimeSettingsEditorProps) {
  const effectiveProtocol = normalizeRuntimeProtocol(protocol || script?.protocol)
  const [internalSection, setInternalSection] = useState<RuntimeSettingsSectionId>('script_config')
  const [internalHttpTab, setInternalHttpTab] = useState<HTTPTransportTabId>('request_defaults')

  const currentSection = activeSection ?? internalSection
  const currentHttpTab = activeHTTPTransportTab ?? internalHttpTab
  // The pill the user last clicked, with the panel it opened. A section
  // shared by two groups (soap: envelope and WS-Security), or a panel reached
  // through a search hit or override in another group's tab (Max redirects on
  // the Connection tab), keeps the clicked pill active until the user moves.
  const [pinnedGroup, setPinnedGroup] = useState<({ groupId: CanonicalRuntimeGroupId } & RuntimeFieldLocation) | null>(null)
  const [search, setSearch] = useState('')

  const supportedGroups = useMemo(
    () => getCanonicalRuntimeGroupsForProtocol(effectiveProtocol, mode),
    [effectiveProtocol, mode],
  )

  const derivedGroupId = useMemo(
    () => getCanonicalGroupForSection(currentSection, currentHttpTab),
    [currentSection, currentHttpTab],
  )
  const pinnedGroupId =
    pinnedGroup &&
    pinnedGroup.section === currentSection &&
    (isLocationInCanonicalGroup(pinnedGroup.groupId, currentSection, currentHttpTab) ||
      currentSection !== 'http' ||
      pinnedGroup.httpTransportTab === currentHttpTab)
      ? pinnedGroup.groupId
      : null
  // The pin lasts only until the user moves: once its panel has been shown,
  // moving to any other section or tab clears it, so coming back later shows
  // the derived group rather than the old pin.
  const pinReachedRef = useRef(false)
  useEffect(() => {
    if (!pinnedGroup) return
    if (pinnedGroupId !== null) {
      pinReachedRef.current = true
    } else if (pinReachedRef.current) {
      pinReachedRef.current = false
      setPinnedGroup(null)
    }
  }, [pinnedGroup, pinnedGroupId])
  const currentGroupId = activeGroupId ?? pinnedGroupId ?? derivedGroupId
  const showProvenance = PROVENANCE_MODES.includes(mode)
  const overridePaths = useMemo(
    () => (showProvenance ? overridePathsByCanonicalGroup(provenance, catalogFields) : {}),
    [showProvenance, provenance, catalogFields],
  )
  const visibleGroups = supportedGroups.filter(group =>
    canonicalGroupMatchesSearch(group, search, catalogFields),
  )
  const currentGroup = getCanonicalRuntimeGroup(currentGroupId)

  const handleSelectSection = (nextSection: RuntimeSettingsSectionId) => {
    if (onSelectSection) {
      onSelectSection(nextSection)
    } else {
      setInternalSection(nextSection)
    }
  }

  const handleSelectHttpTab = (nextTab: HTTPTransportTabId) => {
    if (onSelectHTTPTransportTab) {
      onSelectHTTPTransportTab(nextTab)
    } else {
      setInternalHttpTab(nextTab)
    }
  }

  // A pill standing for specific fields (search hits, else overridden paths)
  // opens the panel that renders them, not only the group's default panel.
  const focusPathsForGroup = (group: CanonicalRuntimeGroupDefinition): string[] =>
    search.trim()
      ? canonicalGroupSearchFieldMatches(group, search, catalogFields)
      : overridePaths[group.id] ?? []

  const handleSelectCanonicalGroup = (groupId: CanonicalRuntimeGroupId) => {
    onSelectGroup?.(groupId)
    const destination = getCanonicalGroupDestinationForPaths(
      groupId,
      effectiveProtocol,
      focusPathsForGroup(getCanonicalRuntimeGroup(groupId)),
    )
    pinReachedRef.current = false
    setPinnedGroup({ groupId, ...destination })
    handleSelectSection(destination.section)
    if (destination.httpTransportTab) {
      handleSelectHttpTab(destination.httpTransportTab)
    }
  }

  const resolvedScript = useMemo<ScriptDefinition>(() => {
    if (script) {
      return script
    }
    return {
      name: 'Runtime configuration',
      version: '1.0.0',
      protocol: effectiveProtocol,
      config: config ?? { base_url: '' },
      actions: [],
    }
  }, [script, effectiveProtocol, config])

  const handleUpdateScript = (updater: (prev: ScriptDefinition) => ScriptDefinition) => {
    if (onUpdateScript) {
      onUpdateScript(updater)
      return
    }
    if (onChangeConfig) {
      onChangeConfig(prevConfig => {
        const nextScript = updater({
          ...resolvedScript,
          config: prevConfig,
        })
        return nextScript.config
      })
    }
  }

  return (
    <div
      className="runtime-settings-editor"
      data-testid="runtime-settings-editor"
      data-runtime-editor-mode={mode}
      data-runtime-protocol={effectiveProtocol}
    >
      <div className="runtime-canonical-header">
        <div className="runtime-canonical-header-meta">
          <span
            className="repository-status-pill"
            data-testid="runtime-editor-mode-badge"
          >
            {MODE_LABELS[mode]}
          </span>
          <span
            className="repository-status-pill"
            data-testid="runtime-editor-protocol-badge"
          >
            {effectiveProtocol}
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
          onChange={event => setSearch(event.target.value)}
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
                onClick={() => handleSelectCanonicalGroup(group.id)}
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
      {children ? (
        children
      ) : (
        <ScriptRuntimeSettingsView
          embeddedInCanonicalEditor
          editorMode={mode}
          activeSection={currentSection}
          activeHTTPTransportTab={currentHttpTab}
          baselineConfig={baselineConfig}
          debugContext={debugContext}
          hideSave={hideSave}
          onSave={onSave ?? (() => {})}
          onSelectHTTPTransportTab={handleSelectHttpTab}
          onSelectSection={handleSelectSection}
          onUpdateScript={handleUpdateScript}
          saveDisabled={saveDisabled}
          saveTitle={saveTitle}
          saving={saving}
          script={resolvedScript}
        />
      )}
    </div>
  )
}

export default RuntimeSettingsEditor
