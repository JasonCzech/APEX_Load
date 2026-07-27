import { useCallback, useEffect, useState } from 'react'
import {
  getImportedRuntimeSettingsDestination,
  HTTP_TRANSPORT_TAB_OPTIONS,
  RUNTIME_SETTINGS_SECTION_IDS,
  runtimeSettingsSectionIsAvailable,
  type HTTPTransportTabId,
  type RuntimeSettingsSectionId,
} from './scriptEditorRuntimeSettingsModel'

interface UseScriptRuntimeSettingsControllerOptions {
  onOpenRuntimeTab: () => void
  protocol: string
  setOpenSections: (updater: (previous: Record<string, boolean>) => Record<string, boolean>) => void
}

export function useScriptRuntimeSettingsController({
  onOpenRuntimeTab,
  protocol,
  setOpenSections,
}: UseScriptRuntimeSettingsControllerOptions) {
  const [activeSection, setActiveSection] = useState<RuntimeSettingsSectionId>('script_config')
  const [activeHTTPTransportTab, setActiveHTTPTransportTab] = useState<HTTPTransportTabId>(
    HTTP_TRANSPORT_TAB_OPTIONS[0].id,
  )

  const sectionIsAvailable = useCallback(
    (section: RuntimeSettingsSectionId) => runtimeSettingsSectionIsAvailable(section, protocol),
    [protocol],
  )

  const selectSection = useCallback((section: RuntimeSettingsSectionId) => {
    setActiveSection(section)
    setOpenSections(previous => {
      const next: Record<string, boolean> = { ...previous, runtime: true }
      for (const sectionId of RUNTIME_SETTINGS_SECTION_IDS) {
        next[sectionId] = sectionId === section
      }
      return next
    })
  }, [setOpenSections])

  useEffect(() => {
    if (!sectionIsAvailable(activeSection)) selectSection('think')
  }, [activeSection, sectionIsAvailable, selectSection])

  const openImportedRuntimeSettings = useCallback((area?: string | null) => {
    onOpenRuntimeTab()
    const destination = getImportedRuntimeSettingsDestination(area)
    if (destination && sectionIsAvailable(destination.section)) {
      if (destination.httpTransportTab) {
        setActiveHTTPTransportTab(destination.httpTransportTab)
      }
      selectSection(destination.section)
      return
    }
    setOpenSections(previous => ({ ...previous, runtime: true }))
  }, [onOpenRuntimeTab, sectionIsAvailable, selectSection, setOpenSections])

  return {
    activeHTTPTransportTab,
    activeSection,
    isActive: (section: RuntimeSettingsSectionId) => activeSection === section,
    openImportedRuntimeSettings,
    sectionIsAvailable,
    selectHTTPTransportTab: setActiveHTTPTransportTab,
    selectSection,
  }
}

export type ScriptRuntimeSettingsController = ReturnType<typeof useScriptRuntimeSettingsController>
