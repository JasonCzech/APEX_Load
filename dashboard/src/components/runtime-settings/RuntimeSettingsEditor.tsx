import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { ScriptConfig, ScriptDefinition } from '../../types/scripts'
import type { DebugRuntimeContext } from '../RuntimeContextSummary'
import type {
  HTTPTransportTabId,
  RuntimeSettingsSectionId,
} from '../scriptEditorRuntimeSettingsModel'
import ScriptRuntimeSettingsView from '../ScriptRuntimeSettingsView'
import {
  getCanonicalGroupDestinationForPaths,
  getCanonicalGroupForSection,
  isLocationInCanonicalGroup,
  normalizeRuntimeProtocol,
  type CanonicalCatalogFieldRef,
  type CanonicalRuntimeGroupId,
  type RuntimeFieldLocation,
  type RuntimeSettingsEditorMode,
  type RuntimeValueProvenance,
} from './canonicalRuntimeTaxonomy'
import { CanonicalRuntimeGroupRail } from './CanonicalRuntimeGroupRail'

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

  const handleSelectCanonicalGroup = (groupId: CanonicalRuntimeGroupId, focusPaths: string[]) => {
    onSelectGroup?.(groupId)
    const destination = getCanonicalGroupDestinationForPaths(groupId, effectiveProtocol, focusPaths)
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
      <CanonicalRuntimeGroupRail
        mode={mode}
        protocol={effectiveProtocol}
        currentGroupId={currentGroupId}
        search={search}
        onSearchChange={setSearch}
        onSelectGroup={handleSelectCanonicalGroup}
        catalogFields={catalogFields}
        provenance={provenance}
      />
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
