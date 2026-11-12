import { useMemo, useState, type ReactNode } from 'react'
import type { ScriptConfig, ScriptDefinition } from '../../types/scripts'
import type { DebugRuntimeContext } from '../RuntimeContextSummary'
import type {
  HTTPTransportTabId,
  RuntimeSettingsSectionId,
} from '../scriptEditorRuntimeSettingsModel'
import ScriptRuntimeSettingsView from '../ScriptRuntimeSettingsView'
import {
  CANONICAL_RUNTIME_GROUPS,
  getCanonicalGroupDestination,
  getCanonicalGroupForSection,
  getCanonicalRuntimeGroup,
  getCanonicalRuntimeGroupsForProtocol,
  normalizeRuntimeProtocol,
  type CanonicalRuntimeGroupId,
  type RuntimeSettingsEditorMode,
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
  children?: ReactNode
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
  children,
}: RuntimeSettingsEditorProps) {
  const effectiveProtocol = normalizeRuntimeProtocol(protocol || script?.protocol)
  const [internalSection, setInternalSection] = useState<RuntimeSettingsSectionId>('script_config')
  const [internalHttpTab, setInternalHttpTab] = useState<HTTPTransportTabId>('request_defaults')

  const currentSection = activeSection ?? internalSection
  const currentHttpTab = activeHTTPTransportTab ?? internalHttpTab

  const supportedGroups = useMemo(
    () => getCanonicalRuntimeGroupsForProtocol(effectiveProtocol, mode),
    [effectiveProtocol, mode],
  )

  const derivedGroupId = useMemo(
    () => getCanonicalGroupForSection(currentSection, currentHttpTab),
    [currentSection, currentHttpTab],
  )
  const currentGroupId = activeGroupId ?? derivedGroupId
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

  const handleSelectCanonicalGroup = (groupId: CanonicalRuntimeGroupId) => {
    onSelectGroup?.(groupId)
    const destination = getCanonicalGroupDestination(groupId, effectiveProtocol)
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
        <nav
          className="runtime-canonical-groups-rail"
          aria-label="Canonical runtime settings groups"
          data-testid="runtime-settings-canonical-groups"
        >
          {CANONICAL_RUNTIME_GROUPS.map((group, index) => {
            const isSupported = supportedGroups.some(item => item.id === group.id)
            if (!isSupported) {
              return null
            }
            const isActive = group.id === currentGroupId
            return (
              <button
                key={group.id}
                type="button"
                className={`runtime-canonical-group-pill ${isActive ? 'active' : ''}`}
                data-testid={`runtime-canonical-group-${group.id}`}
                data-canonical-group={group.id}
                aria-pressed={isActive}
                onClick={() => handleSelectCanonicalGroup(group.id)}
              >
                <span className="runtime-canonical-group-index">{index + 1}.</span>
                <span>{group.label}</span>
              </button>
            )
          })}
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
