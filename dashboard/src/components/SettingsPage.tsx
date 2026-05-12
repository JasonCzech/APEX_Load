import { useEffect, useMemo, useState, type DragEvent } from 'react'
import { useApplicationApi } from '../hooks/api/useApplicationApi'
import { useApplication } from '../hooks/useApplication'
import { useApi } from '../hooks/useApi'
import { DashboardSystemSettings, HealthResponse, SharedDataPool, SharedDataPoolRow } from '../types'

const DEFAULT_SETTINGS: DashboardSystemSettings = {
  general: {
    api_endpoint: ':8080',
    max_concurrent_tests: 10,
    metrics_interval_seconds: 1,
  },
  thresholds: {
    max_error_rate_pct: undefined,
    max_p99_latency_ms: undefined,
  },
  capacity_planning: {
    headroom_pct: 20,
    generator: {
      cpu_cores: 4,
      memory_mb: 8192,
      network_mbps: 1000,
      max_vusers: 10000,
    },
    availability: {
      source: 'configured',
      configured_available_generators: 1,
    },
  },
}

const SETTINGS_SECTIONS = [
  { id: 'general', title: 'General' },
  { id: 'thresholds', title: 'Performance Thresholds' },
  { id: 'capacity', title: 'Capacity Planning' },
  { id: 'applications', title: 'Applications & Releases' },
  { id: 'data-pools', title: 'Shared Data Pools' },
  { id: 'integrations', title: 'Integrations' },
] as const

const GENERATOR_PRESETS = [
  { id: 'local', label: 'Local', cpu_cores: 2, memory_mb: 4096, network_mbps: 250, max_vusers: 1000 },
  { id: 'standard', label: 'Standard Pod', cpu_cores: 4, memory_mb: 8192, network_mbps: 1000, max_vusers: 10000 },
  { id: 'large', label: 'Large Pod', cpu_cores: 8, memory_mb: 16384, network_mbps: 2500, max_vusers: 20000 },
] as const

type SettingsSectionId = typeof SETTINGS_SECTIONS[number]['id']

function parseOptionalNumber(value: string): number | undefined {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  const parsed = Number(trimmed)
  return Number.isFinite(parsed) ? parsed : undefined
}

function validateSettings(settings: DashboardSystemSettings): string[] {
  const errors: string[] = []
  if (!settings.general.api_endpoint.trim()) {
    errors.push('API endpoint is required.')
  }
  if (!Number.isInteger(settings.general.max_concurrent_tests) || settings.general.max_concurrent_tests < 1) {
    errors.push('Max concurrent tests must be an integer >= 1.')
  }
  if (!Number.isInteger(settings.general.metrics_interval_seconds) || settings.general.metrics_interval_seconds < 1) {
    errors.push('Metrics interval must be an integer >= 1 second.')
  }
  if (settings.thresholds.max_error_rate_pct !== undefined) {
    if (settings.thresholds.max_error_rate_pct < 0 || settings.thresholds.max_error_rate_pct > 100) {
      errors.push('Max error rate must be between 0 and 100%.')
    }
  }
  if (settings.thresholds.max_p99_latency_ms !== undefined && settings.thresholds.max_p99_latency_ms <= 0) {
    errors.push('Max P99 latency must be greater than 0 ms.')
  }
  if (settings.capacity_planning.headroom_pct < 0 || settings.capacity_planning.headroom_pct >= 100) {
    errors.push('Capacity planning headroom must be between 0 and 100%.')
  }
  if (settings.capacity_planning.generator.cpu_cores <= 0) {
    errors.push('Generator CPU cores must be greater than 0.')
  }
  if (!Number.isInteger(settings.capacity_planning.generator.memory_mb) || settings.capacity_planning.generator.memory_mb < 1) {
    errors.push('Generator memory must be an integer >= 1 MB.')
  }
  if (settings.capacity_planning.generator.network_mbps <= 0) {
    errors.push('Generator network throughput must be greater than 0 Mbps.')
  }
  if (!Number.isInteger(settings.capacity_planning.generator.max_vusers) || settings.capacity_planning.generator.max_vusers < 1) {
    errors.push('Generator max VUsers must be an integer >= 1.')
  }
  if (!Number.isInteger(settings.capacity_planning.availability.configured_available_generators) || settings.capacity_planning.availability.configured_available_generators < 1) {
    errors.push('Configured available generators must be an integer >= 1.')
  }
  return errors
}

export default function SettingsPage() {
  const api = useApi()
  const applicationApi = useApplicationApi()
  const {
    applications,
    releases,
    activeApplication,
    activeRelease,
    setActiveApplication,
    setActiveRelease,
    refreshApplications,
    refreshReleases,
    createAndSelectApplication,
  } = useApplication()
  const [settings, setSettings] = useState<DashboardSystemSettings>(DEFAULT_SETTINGS)
  const [savedSettings, setSavedSettings] = useState<DashboardSystemSettings>(DEFAULT_SETTINGS)
  const [health, setHealth] = useState<HealthResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [saveMessage, setSaveMessage] = useState('')
  const [requestError, setRequestError] = useState('')
  const [customizingSections, setCustomizingSections] = useState(false)
  const [draggedSectionId, setDraggedSectionId] = useState<SettingsSectionId | null>(null)
  const [sectionOrder, setSectionOrder] = useState<SettingsSectionId[]>(() => readSettingsSectionArray('stormrunner.settings.sectionOrder'))
  const [hiddenSectionIds, setHiddenSectionIds] = useState<SettingsSectionId[]>(() => readSettingsSectionArray('stormrunner.settings.hiddenSections'))
  const [collapsedSectionIds, setCollapsedSectionIds] = useState<SettingsSectionId[]>(() => readSettingsSectionArray('stormrunner.settings.collapsedSections'))
  const [managedReleases, setManagedReleases] = useState(releases)
  const [newApplicationName, setNewApplicationName] = useState('')
  const [applicationActionError, setApplicationActionError] = useState('')
  const [dataPools, setDataPools] = useState<SharedDataPool[]>([])
  const [selectedDataPoolId, setSelectedDataPoolId] = useState('')
  const [dataPoolRows, setDataPoolRows] = useState<SharedDataPoolRow[]>([])
  const [dataPoolName, setDataPoolName] = useState('')
  const [dataPoolDescription, setDataPoolDescription] = useState('')
  const [dataPoolRowJson, setDataPoolRowJson] = useState('{\n  "customer_id": "C-10001"\n}')
  const [editingDataPoolRowId, setEditingDataPoolRowId] = useState<string | null>(null)
  const [dataPoolActionError, setDataPoolActionError] = useState('')

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      setRequestError('')
      try {
        const response = await api.getSystemSettings()
        if (!cancelled) {
          setSettings(response)
          setSavedSettings(response)
        }
      } catch (error: any) {
        if (!cancelled) {
          setRequestError(error.message || 'Unable to load system settings')
          setSettings(DEFAULT_SETTINGS)
        }
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => {
      cancelled = true
    }
  }, [api])

  useEffect(() => {
    let cancelled = false
    const loadHealth = async () => {
      if (typeof document !== 'undefined' && document.visibilityState !== 'visible') {
        return
      }
      try {
        const response = await api.getHealth()
        if (!cancelled) setHealth(response)
      } catch {
        if (!cancelled) setHealth(null)
      }
    }

    loadHealth()
    const interval = setInterval(loadHealth, 5000)

    const handleVisibilityChange = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'visible') {
        loadHealth()
      }
    }

    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', handleVisibilityChange)
    }

    return () => {
      cancelled = true
      clearInterval(interval)
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', handleVisibilityChange)
      }
    }
  }, [api])

  const validationErrors = useMemo(() => validateSettings(settings), [settings])
  const hasUnsavedChanges = useMemo(() => JSON.stringify(settings) !== JSON.stringify(savedSettings), [settings, savedSettings])
  const canSave = validationErrors.length === 0 && !loading && hasUnsavedChanges
  const availabilitySnapshot = health?.capacity_availability
  const availabilitySourceLabel = availabilitySnapshot?.source === 'configured_plus_live'
    ? availabilitySnapshot.live_available_generators !== undefined
      ? 'Live snapshot + configured ceiling'
      : 'Configured ceiling only'
    : 'Configured inventory only'

  useEffect(() => {
    setSectionOrder(prev => mergeSettingsOrder(prev))
  }, [])

  useEffect(() => {
    if (typeof window === 'undefined') return
    const targetSection = window.localStorage.getItem('stormrunner.settings.focusSection') as SettingsSectionId | null
    if (!targetSection) return
    window.localStorage.removeItem('stormrunner.settings.focusSection')
    window.setTimeout(() => {
      document.getElementById(`settings-section-${targetSection}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }, 150)
  }, [])

  useEffect(() => {
    let cancelled = false
    const loadManagedReleases = async () => {
      if (!activeApplication?.id) {
        setManagedReleases([])
        return
      }
      try {
        const response = await applicationApi.listReleases(activeApplication.id, true)
        if (!cancelled) setManagedReleases(response.releases || [])
      } catch {
        if (!cancelled) setManagedReleases(releases)
      }
    }
    loadManagedReleases()
    return () => {
      cancelled = true
    }
  }, [activeApplication?.id, applicationApi, releases])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('stormrunner.settings.sectionOrder', JSON.stringify(sectionOrder))
  }, [sectionOrder])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('stormrunner.settings.hiddenSections', JSON.stringify(hiddenSectionIds))
  }, [hiddenSectionIds])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('stormrunner.settings.collapsedSections', JSON.stringify(collapsedSectionIds))
  }, [collapsedSectionIds])

  const loadDataPools = async (preferredId?: string) => {
    try {
      const response = await api.listDataPools()
      const pools: SharedDataPool[] = response.data_pools || []
      setDataPools(pools)
      const nextSelected = preferredId || selectedDataPoolId || pools[0]?.id || ''
      setSelectedDataPoolId(nextSelected)
      if (nextSelected) {
        const details = await api.getDataPool(nextSelected)
        setDataPoolRows(details.rows || [])
      } else {
        setDataPoolRows([])
      }
      setDataPoolActionError('')
    } catch (error: any) {
      setDataPoolActionError(error.message || 'Unable to load shared data pools')
    }
  }

  useEffect(() => {
    loadDataPools()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    let cancelled = false
    const loadRows = async () => {
      if (!selectedDataPoolId) {
        setDataPoolRows([])
        return
      }
      try {
        const response = await api.getDataPool(selectedDataPoolId)
        if (!cancelled) setDataPoolRows(response.rows || [])
      } catch (error: any) {
        if (!cancelled) setDataPoolActionError(error.message || 'Unable to load data pool rows')
      }
    }
    loadRows()
    return () => {
      cancelled = true
    }
  }, [api, selectedDataPoolId])

  const updateGeneral = (key: keyof DashboardSystemSettings['general'], value: string) => {
    setSettings((prev) => ({
      ...prev,
      general: {
        ...prev.general,
        [key]: key === 'api_endpoint' ? value : Math.max(0, Number(value) || 0),
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateThreshold = (key: keyof DashboardSystemSettings['thresholds'], value: string) => {
    setSettings((prev) => ({
      ...prev,
      thresholds: {
        ...prev.thresholds,
        [key]: parseOptionalNumber(value),
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateCapacityPlanning = (
    key: keyof DashboardSystemSettings['capacity_planning'],
    value: string,
  ) => {
    setSettings((prev) => ({
      ...prev,
      capacity_planning: {
        ...prev.capacity_planning,
        [key]: key === 'availability' || key === 'generator' ? prev.capacity_planning[key] : Number(value),
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateGenerator = (key: keyof DashboardSystemSettings['capacity_planning']['generator'], value: string) => {
    setSettings((prev) => ({
      ...prev,
      capacity_planning: {
        ...prev.capacity_planning,
        generator: {
          ...prev.capacity_planning.generator,
          [key]: Math.max(0, Number(value) || 0),
        },
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateAvailability = (key: keyof DashboardSystemSettings['capacity_planning']['availability'], value: string) => {
    setSettings((prev) => ({
      ...prev,
      capacity_planning: {
        ...prev.capacity_planning,
        availability: {
          ...prev.capacity_planning.availability,
          [key]: key === 'source' ? value : Math.max(0, Number(value) || 0),
        },
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const saveSettings = async () => {
    if (!canSave) return
    try {
      const response = await api.updateSystemSettings(settings)
      setSettings(response)
      setSavedSettings(response)
      setSaveMessage('Settings saved.')
      setRequestError('')
    } catch (error: any) {
      setRequestError(error.message || 'Unable to save settings')
      setSaveMessage('')
    }
  }

  const resetSettings = async () => {
    if (!window.confirm('Reset all system settings to defaults?')) return
    try {
      const response = await api.updateSystemSettings(DEFAULT_SETTINGS)
      setSettings(response)
      setSavedSettings(response)
      setSaveMessage('Settings reset to defaults.')
      setRequestError('')
    } catch (error: any) {
      setRequestError(error.message || 'Unable to reset settings')
      setSaveMessage('')
    }
  }

  const discardChanges = () => {
    setSettings(savedSettings)
    setSaveMessage('')
    setRequestError('')
  }

  const applyGeneratorPreset = (preset: typeof GENERATOR_PRESETS[number]) => {
    setSettings(prev => ({
      ...prev,
      capacity_planning: {
        ...prev.capacity_planning,
        generator: {
          cpu_cores: preset.cpu_cores,
          memory_mb: preset.memory_mb,
          network_mbps: preset.network_mbps,
          max_vusers: preset.max_vusers,
        },
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const visibleSections = SETTINGS_SECTIONS.filter(section => !hiddenSectionIds.includes(section.id))
  const hiddenSections = SETTINGS_SECTIONS.filter(section => hiddenSectionIds.includes(section.id))
  const orderedSectionIds = mergeSettingsOrder(sectionOrder)
  const getSectionOrder = (id: SettingsSectionId) => orderedSectionIds.indexOf(id)
  const sectionClass = (id: SettingsSectionId) =>
    `settings-card ${customizingSections ? 'editable' : ''} ${collapsedSectionIds.includes(id) ? 'collapsed' : ''} ${draggedSectionId === id ? 'dragging' : ''}`
  const scrollToSection = (id: SettingsSectionId) => {
    document.getElementById(`settings-section-${id}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  const toggleHiddenSection = (id: SettingsSectionId) => {
    setHiddenSectionIds(prev => prev.includes(id) ? prev.filter(sectionId => sectionId !== id) : [...prev, id])
  }

  const toggleCollapsedSection = (id: SettingsSectionId) => {
    setCollapsedSectionIds(prev => prev.includes(id) ? prev.filter(sectionId => sectionId !== id) : [...prev, id])
  }

  const resetSectionLayout = () => {
    setSectionOrder(SETTINGS_SECTIONS.map(section => section.id))
    setHiddenSectionIds([])
    setCollapsedSectionIds([])
  }

  const createApplication = async () => {
    const name = newApplicationName.trim()
    if (!name) return
    try {
      await createAndSelectApplication(name)
      setNewApplicationName('')
      setApplicationActionError('')
    } catch (error: any) {
      setApplicationActionError(error.message || 'Unable to create application')
    }
  }

  const renameApplication = async () => {
    if (!activeApplication) return
    const name = window.prompt('Application name', activeApplication.name)
    if (!name?.trim()) return
    try {
      const updated = await applicationApi.updateApplication(activeApplication.id, { name: name.trim() })
      await refreshApplications()
      setActiveApplication(updated)
      setApplicationActionError('')
    } catch (error: any) {
      setApplicationActionError(error.message || 'Unable to rename application')
    }
  }

  const deleteApplication = async () => {
    if (!activeApplication) return
    if (!window.confirm(`Delete application "${activeApplication.name}" and its releases?`)) return
    try {
      await applicationApi.deleteApplication(activeApplication.id)
      await refreshApplications()
      setApplicationActionError('')
    } catch (error: any) {
      setApplicationActionError(error.message || 'Unable to delete application')
    }
  }

  const renameRelease = async (releaseId: string, currentName: string) => {
    const name = window.prompt('Release name', currentName)
    if (!name?.trim()) return
    try {
      const updated = await applicationApi.updateRelease(releaseId, { name: name.trim() })
      await refreshReleases(activeApplication?.id)
      const response = await applicationApi.listReleases(activeApplication?.id || '', true)
      setManagedReleases(response.releases || [])
      if (!updated.archived_at) setActiveRelease(updated)
      setApplicationActionError('')
    } catch (error: any) {
      setApplicationActionError(error.message || 'Unable to rename release')
    }
  }

  const archiveRelease = async (releaseId: string, releaseName: string) => {
    const results = await api.listTests({ application_id: activeApplication?.id, release_id: releaseId })
    if ((results.tests || []).length > 0) {
      setApplicationActionError(`Release "${releaseName}" still has associated results. Archive or delete those results before archiving the release.`)
      return
    }
    if (!window.confirm(`Archive release "${releaseName}"? If this release still had associated results, you would need to archive or delete those results first.`)) return
    try {
      await applicationApi.archiveRelease(releaseId)
      await refreshReleases(activeApplication?.id)
      const response = await applicationApi.listReleases(activeApplication?.id || '', true)
      setManagedReleases(response.releases || [])
      setApplicationActionError('')
    } catch (error: any) {
      setApplicationActionError(error.message || 'Unable to archive release')
    }
  }

  const deleteRelease = async (releaseId: string, releaseName: string) => {
    const results = await api.listTests({ application_id: activeApplication?.id, release_id: releaseId })
    if ((results.tests || []).length > 0) {
      setApplicationActionError(`Release "${releaseName}" still has associated results. Archive or delete those results before deleting the release.`)
      return
    }
    if (!window.confirm(`Delete release "${releaseName}"? If this release still had associated results, you would need to archive or delete those results first.`)) return
    try {
      await applicationApi.deleteRelease(releaseId)
      await refreshReleases(activeApplication?.id)
      const response = await applicationApi.listReleases(activeApplication?.id || '', true)
      setManagedReleases(response.releases || [])
      setApplicationActionError('')
    } catch (error: any) {
      setApplicationActionError(error.message || 'Unable to delete release')
    }
  }

  const createDataPool = async () => {
    const name = dataPoolName.trim()
    if (!name) return
    try {
      const pool = await api.createDataPool({ name, description: dataPoolDescription.trim() })
      setDataPoolName('')
      setDataPoolDescription('')
      await loadDataPools(pool.id)
      setDataPoolActionError('')
    } catch (error: any) {
      setDataPoolActionError(error.message || 'Unable to create data pool')
    }
  }

  const deleteDataPool = async (pool: SharedDataPool) => {
    if (!window.confirm(`Delete data pool "${pool.name}"? Data pools bound to scenarios must be unbound first.`)) return
    try {
      await api.deleteDataPool(pool.id)
      await loadDataPools('')
      setDataPoolActionError('')
    } catch (error: any) {
      setDataPoolActionError(error.message || 'Unable to delete data pool')
    }
  }

  const saveDataPoolRow = async () => {
    if (!selectedDataPoolId) return
    let parsed: Record<string, string>
    try {
      parsed = JSON.parse(dataPoolRowJson)
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        throw new Error('Row data must be a JSON object.')
      }
    } catch (error: any) {
      setDataPoolActionError(error.message || 'Invalid row JSON')
      return
    }
    try {
      if (editingDataPoolRowId) {
        await api.updateDataPoolRow(selectedDataPoolId, editingDataPoolRowId, { data: parsed })
      } else {
        await api.createDataPoolRow(selectedDataPoolId, { data: parsed })
      }
      setEditingDataPoolRowId(null)
      setDataPoolRowJson('{\n  "customer_id": "C-10001"\n}')
      const response = await api.getDataPool(selectedDataPoolId)
      setDataPoolRows(response.rows || [])
      setDataPoolActionError('')
    } catch (error: any) {
      setDataPoolActionError(error.message || 'Unable to save data row')
    }
  }

  const editDataPoolRow = (row: SharedDataPoolRow) => {
    setEditingDataPoolRowId(row.id)
    setDataPoolRowJson(JSON.stringify(row.data || {}, null, 2))
  }

  const deleteDataPoolRow = async (row: SharedDataPoolRow) => {
    if (!selectedDataPoolId) return
    if (!window.confirm('Delete this data row?')) return
    try {
      await api.deleteDataPoolRow(selectedDataPoolId, row.id)
      const response = await api.getDataPool(selectedDataPoolId)
      setDataPoolRows(response.rows || [])
      setDataPoolActionError('')
    } catch (error: any) {
      setDataPoolActionError(error.message || 'Unable to delete data row')
    }
  }

  const handleSectionDragStart = (event: DragEvent<HTMLDivElement>, id: SettingsSectionId) => {
    if (!customizingSections) return
    setDraggedSectionId(id)
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', id)
  }

  const handleSectionDragOver = (event: DragEvent<HTMLDivElement>, id: SettingsSectionId) => {
    if (!customizingSections || !draggedSectionId || draggedSectionId === id) return
    event.preventDefault()
    event.dataTransfer.dropEffect = 'move'
  }

  const handleSectionDrop = (event: DragEvent<HTMLDivElement>, id: SettingsSectionId) => {
    if (!customizingSections) return
    event.preventDefault()
    const sourceId = (event.dataTransfer.getData('text/plain') || draggedSectionId) as SettingsSectionId | null
    if (!sourceId) return
    setSectionOrder(prev => moveSettingsSection(mergeSettingsOrder(prev), sourceId, id))
    setDraggedSectionId(null)
  }

  const renderSettingsHeader = (id: SettingsSectionId, title: string) => (
    <h3 className="settings-card-title">
      <span className="settings-title-main">
        {customizingSections && <span className="settings-drag-handle" title="Drag to reorder">::</span>}
        {title}
      </span>
      <span className="settings-card-controls">
        <button className="btn-icon settings-collapse-btn" type="button" onClick={() => toggleCollapsedSection(id)}>
          {collapsedSectionIds.includes(id) ? 'Show' : 'Hide Body'}
        </button>
        {customizingSections && (
          <button className="btn-icon settings-collapse-btn" type="button" onClick={() => toggleHiddenSection(id)}>
            Hide
          </button>
        )}
      </span>
    </h3>
  )

  return (
    <div className="page settings-page">
      <div className="page-header">
        <div>
          <h2>Settings</h2>
          <p className="page-subtitle">Configuration, thresholds, and integrations</p>
        </div>
        <div className="settings-actions">
          {customizingSections && (
            <button className="btn btn-secondary" onClick={resetSectionLayout}>
              Reset Layout
            </button>
          )}
          <button className={`btn ${customizingSections ? 'btn-primary' : 'btn-secondary'}`} onClick={() => setCustomizingSections(value => !value)}>
            {customizingSections ? 'Done' : 'Customize Layout'}
          </button>
          {hasUnsavedChanges && (
            <button className="btn btn-secondary" onClick={discardChanges}>
              Discard Changes
            </button>
          )}
          <button className="btn btn-secondary" onClick={resetSettings}>
            Reset All Settings
          </button>
          <button className="btn btn-primary" disabled={!canSave} onClick={saveSettings}>
            {hasUnsavedChanges ? 'Save Changes' : 'Saved'}
          </button>
        </div>
      </div>

      <div className="settings-overview">
        <div>
          <span>Save State</span>
          <strong>{hasUnsavedChanges ? 'Unsaved changes' : 'Up to date'}</strong>
        </div>
        <div>
          <span>Concurrency</span>
          <strong>{settings.general.max_concurrent_tests} tests</strong>
        </div>
        <div>
          <span>Generator Profile</span>
          <strong>{settings.capacity_planning.generator.cpu_cores} CPU / {settings.capacity_planning.generator.memory_mb} MB</strong>
        </div>
        <div>
          <span>Capacity Source</span>
          <strong>{availabilitySourceLabel}</strong>
        </div>
      </div>

      <div className="settings-quick-nav">
        {visibleSections.map(section => (
          <button key={section.id} type="button" className="btn btn-secondary" onClick={() => scrollToSection(section.id)}>
            {section.title}
          </button>
        ))}
      </div>

      {loading && <p className="settings-notice">Loading settings...</p>}
      {!loading && saveMessage && <p className="settings-notice">{saveMessage}</p>}
      {requestError && <p className="settings-error-inline">{requestError}</p>}
      {applicationActionError && <p className="settings-error-inline">{applicationActionError}</p>}
      {dataPoolActionError && <p className="settings-error-inline">{dataPoolActionError}</p>}
      {validationErrors.length > 0 && (
        <div className="settings-errors" role="alert">
          {validationErrors.map((error) => (
            <p key={error}>{error}</p>
          ))}
        </div>
      )}

      {customizingSections && hiddenSections.length > 0 && (
        <div className="hidden-chart-list">
          {hiddenSections.map(section => (
            <button key={section.id} className="hidden-chart-pill" type="button" onClick={() => toggleHiddenSection(section.id)}>
              Show {section.title}
            </button>
          ))}
        </div>
      )}

      <div className="settings-grid">
        <div
          id="settings-section-general"
          className={sectionClass('general')}
          hidden={!visibleSections.some(section => section.id === 'general')}
          draggable={customizingSections}
          onDragStart={event => handleSectionDragStart(event, 'general')}
          onDragOver={event => handleSectionDragOver(event, 'general')}
          onDrop={event => handleSectionDrop(event, 'general')}
          onDragEnd={() => setDraggedSectionId(null)}
          style={{ order: getSectionOrder('general') }}
        >
          {renderSettingsHeader('general', 'General')}
          <div className="settings-group">
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">API Endpoint</span>
                <span className="setting-desc">Controller API server address</span>
              </div>
              <input
                className="settings-input cell-mono"
                value={settings.general.api_endpoint}
                onChange={(e) => updateGeneral('api_endpoint', e.target.value)}
                placeholder=":8080"
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Concurrent Tests</span>
                <span className="setting-desc">Maximum tests running simultaneously</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.general.max_concurrent_tests}
                onChange={(e) => updateGeneral('max_concurrent_tests', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Metrics Interval (seconds)</span>
                <span className="setting-desc">How often metrics are broadcast via WebSocket</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.general.metrics_interval_seconds}
                onChange={(e) => updateGeneral('metrics_interval_seconds', e.target.value)}
              />
            </div>
          </div>
        </div>

        <div
          id="settings-section-thresholds"
          className={sectionClass('thresholds')}
          hidden={!visibleSections.some(section => section.id === 'thresholds')}
          draggable={customizingSections}
          onDragStart={event => handleSectionDragStart(event, 'thresholds')}
          onDragOver={event => handleSectionDragOver(event, 'thresholds')}
          onDrop={event => handleSectionDrop(event, 'thresholds')}
          onDragEnd={() => setDraggedSectionId(null)}
          style={{ order: getSectionOrder('thresholds') }}
        >
          {renderSettingsHeader('thresholds', 'Performance Thresholds')}
          <div className="settings-group">
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Error Rate (%)</span>
                <span className="setting-desc">Optional: alert/stop threshold for high error rate</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={0}
                max={100}
                step={0.1}
                value={settings.thresholds.max_error_rate_pct ?? ''}
                onChange={(e) => updateThreshold('max_error_rate_pct', e.target.value)}
                placeholder="Not configured"
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Response Time (P99 ms)</span>
                <span className="setting-desc">Optional: alert threshold for P99 latency</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.thresholds.max_p99_latency_ms ?? ''}
                onChange={(e) => updateThreshold('max_p99_latency_ms', e.target.value)}
                placeholder="Not configured"
              />
            </div>
          </div>
        </div>

        <div
          id="settings-section-capacity"
          className={sectionClass('capacity')}
          hidden={!visibleSections.some(section => section.id === 'capacity')}
          draggable={customizingSections}
          onDragStart={event => handleSectionDragStart(event, 'capacity')}
          onDragOver={event => handleSectionDragOver(event, 'capacity')}
          onDrop={event => handleSectionDrop(event, 'capacity')}
          onDragEnd={() => setDraggedSectionId(null)}
          style={{ order: getSectionOrder('capacity') }}
        >
          {renderSettingsHeader('capacity', 'Capacity Planning')}
          <div className="settings-group">
            <div className="settings-status-block">
              <div className="settings-status-header">
                <span className="settings-status-title">Current Availability Snapshot</span>
                <span className={`settings-status-badge ${availabilitySnapshot ? 'ready' : 'unknown'}`}>
                  {availabilitySnapshot ? 'Live status' : 'Unavailable'}
                </span>
              </div>
              <div className="settings-status-grid">
                <div className="settings-status-item">
                  <span className="settings-status-label">Source</span>
                  <strong>{availabilitySourceLabel}</strong>
                </div>
                <div className="settings-status-item">
                  <span className="settings-status-label">Effective Available</span>
                  <strong>{availabilitySnapshot?.effective_available_generators ?? settings.capacity_planning.availability.configured_available_generators}</strong>
                </div>
                <div className="settings-status-item">
                  <span className="settings-status-label">Configured Ceiling</span>
                  <strong>{availabilitySnapshot?.configured_available_generators ?? settings.capacity_planning.availability.configured_available_generators}</strong>
                </div>
                <div className="settings-status-item">
                  <span className="settings-status-label">Live Snapshot</span>
                  <strong>{availabilitySnapshot?.live_available_generators ?? 'Not provided'}</strong>
                </div>
              </div>
              <p className="settings-status-copy">
                Advisory calculations and pre-start checks use this snapshot when availability source is set to configured plus live.
              </p>
              {availabilitySnapshot?.warnings && availabilitySnapshot.warnings.length > 0 && (
                <div className="settings-status-warnings" role="status">
                  {availabilitySnapshot.warnings.map((warning) => (
                    <p key={warning}>{warning}</p>
                  ))}
                </div>
              )}
            </div>
            <div className="settings-subsection">
              <div>
                <span className="setting-label">Generator Presets</span>
                <span className="setting-desc">Apply common sizing defaults, then tune individual fields below.</span>
              </div>
              <div className="settings-preset-row">
                {GENERATOR_PRESETS.map(preset => (
                  <button key={preset.id} type="button" className="btn btn-secondary" onClick={() => applyGeneratorPreset(preset)}>
                    {preset.label}
                  </button>
                ))}
              </div>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Headroom (%)</span>
                <span className="setting-desc">Reserve spare generator capacity when estimating pod requirements</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={0}
                max={99}
                step={1}
                value={settings.capacity_planning.headroom_pct}
                onChange={(e) => updateCapacityPlanning('headroom_pct', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Generator CPU (cores)</span>
                <span className="setting-desc">Baseline CPU available per load generator pod or machine</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={0.1}
                step={0.1}
                value={settings.capacity_planning.generator.cpu_cores}
                onChange={(e) => updateGenerator('cpu_cores', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Generator Memory (MB)</span>
                <span className="setting-desc">Baseline memory available per load generator pod or machine</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={256}
                value={settings.capacity_planning.generator.memory_mb}
                onChange={(e) => updateGenerator('memory_mb', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Generator Network (Mbps)</span>
                <span className="setting-desc">Estimated network throughput budget per load generator</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={50}
                value={settings.capacity_planning.generator.network_mbps}
                onChange={(e) => updateGenerator('network_mbps', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Generator Max VUsers</span>
                <span className="setting-desc">Safety ceiling for VUsers that one generator should carry before headroom</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={100}
                value={settings.capacity_planning.generator.max_vusers}
                onChange={(e) => updateGenerator('max_vusers', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Availability Source</span>
                <span className="setting-desc">Choose whether capacity advice should stop at configured inventory or also honor the latest live generator snapshot</span>
              </div>
              <select
                className="settings-input"
                value={settings.capacity_planning.availability.source}
                onChange={(e) => updateAvailability('source', e.target.value)}
              >
                <option value="configured">Configured generators only</option>
                <option value="configured_plus_live">Configured plus live availability</option>
              </select>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Configured Available Generators</span>
                <span className="setting-desc">Configured inventory ceiling. When live snapshots are enabled, the effective available count is the lower of this value and the latest runtime snapshot.</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.capacity_planning.availability.configured_available_generators}
                onChange={(e) => updateAvailability('configured_available_generators', e.target.value)}
              />
            </div>
          </div>
        </div>

        <div
          id="settings-section-applications"
          className={sectionClass('applications')}
          hidden={!visibleSections.some(section => section.id === 'applications')}
          draggable={customizingSections}
          onDragStart={event => handleSectionDragStart(event, 'applications')}
          onDragOver={event => handleSectionDragOver(event, 'applications')}
          onDrop={event => handleSectionDrop(event, 'applications')}
          onDragEnd={() => setDraggedSectionId(null)}
          style={{ order: getSectionOrder('applications') }}
        >
          {renderSettingsHeader('applications', 'Applications & Releases')}
          <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Applications</span>
                  <span className="setting-desc">Select one to manage its releases and rename or remove it.</span>
                </div>
                <strong>{applications.length}</strong>
              </div>
              <div className="settings-app-grid">
                {applications.map(application => (
                  <button
                    key={application.id}
                    type="button"
                    className={`settings-app-card ${activeApplication?.id === application.id ? 'active' : ''}`}
                    onClick={() => setActiveApplication(application)}
                  >
                  <span>{application.name}</span>
                  <small>{application.id === activeApplication?.id ? 'Current application' : 'Switch context'}</small>
                </button>
              ))}
              </div>
              <div className="inline-create-row">
                <input
                  className="settings-input"
                  value={newApplicationName}
                  onChange={event => setNewApplicationName(event.target.value)}
                  placeholder="Create application, e.g. ACC"
                />
                <button className="btn btn-secondary" type="button" onClick={createApplication} disabled={!newApplicationName.trim()}>
                  Add Application
                </button>
              </div>
            </div>

            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Current Application</span>
                <span className="setting-desc">Rename or delete the selected application.</span>
              </div>
              <div className="settings-inline-actions">
                <span className="setting-value">{activeApplication?.name || 'No application selected'}</span>
                <button className="btn btn-secondary" type="button" onClick={renameApplication} disabled={!activeApplication}>
                  Rename
                </button>
                <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={deleteApplication} disabled={!activeApplication}>
                  Delete
                </button>
              </div>
            </div>

            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Releases</span>
                  <span className="setting-desc">These belong to {activeApplication?.name || 'the selected application'}. Create new releases from the New Test page.</span>
                </div>
                <strong>{managedReleases.length}</strong>
              </div>
              {managedReleases.length > 0 ? (
                <div className="settings-release-list">
                  {managedReleases.map(release => (
                    <div key={release.id} className={`settings-release-row ${!release.archived_at && activeRelease?.id === release.id ? 'active' : ''}`}>
                      <button
                        type="button"
                        className="settings-release-main"
                        onClick={() => !release.archived_at && setActiveRelease(release)}
                        disabled={Boolean(release.archived_at)}
                      >
                        <span>{release.name}</span>
                        <small>
                          {release.archived_at
                            ? `Archived ${new Date(release.archived_at).toLocaleDateString()}`
                            : activeRelease?.id === release.id
                              ? 'Current release'
                              : 'Set active release'}
                        </small>
                      </button>
                      <div className="settings-inline-actions">
                        <button className="btn btn-secondary" type="button" onClick={() => renameRelease(release.id, release.name)}>
                          Rename
                        </button>
                        {!release.archived_at && (
                          <button className="btn btn-secondary" type="button" onClick={() => archiveRelease(release.id, release.name)}>
                            Archive
                          </button>
                        )}
                        <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={() => deleteRelease(release.id, release.name)}>
                          Delete
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="new-test-inline-note">No releases yet for this application.</p>
              )}
            </div>
          </div>
        </div>

        <div
          id="settings-section-data-pools"
          className={sectionClass('data-pools')}
          hidden={!visibleSections.some(section => section.id === 'data-pools')}
          draggable={customizingSections}
          onDragStart={event => handleSectionDragStart(event, 'data-pools')}
          onDragOver={event => handleSectionDragOver(event, 'data-pools')}
          onDrop={event => handleSectionDrop(event, 'data-pools')}
          onDragEnd={() => setDraggedSectionId(null)}
          style={{ order: getSectionOrder('data-pools') }}
        >
          {renderSettingsHeader('data-pools', 'Shared Data Pools')}
          <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Reusable Data</span>
                  <span className="setting-desc">Create CRUD data pools that scenarios can bind by alias and share across scripts.</span>
                </div>
                <strong>{dataPools.length}</strong>
              </div>
              <div className="inline-create-row">
                <input
                  className="settings-input"
                  value={dataPoolName}
                  onChange={event => setDataPoolName(event.target.value)}
                  placeholder="Pool name, e.g. Customer Accounts"
                />
                <input
                  className="settings-input"
                  value={dataPoolDescription}
                  onChange={event => setDataPoolDescription(event.target.value)}
                  placeholder="Optional description"
                />
                <button className="btn btn-secondary" type="button" onClick={createDataPool} disabled={!dataPoolName.trim()}>
                  Add Pool
                </button>
              </div>
            </div>

            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Data Pools</span>
                  <span className="setting-desc">Select a pool to inspect and edit its rows.</span>
                </div>
                <strong>{selectedDataPoolId ? `${dataPoolRows.length} rows` : 'No pool selected'}</strong>
              </div>
              {dataPools.length > 0 ? (
                <div className="settings-release-list">
                  {dataPools.map(pool => (
                    <div key={pool.id} className={`settings-release-row ${selectedDataPoolId === pool.id ? 'active' : ''}`}>
                      <button type="button" className="settings-release-main" onClick={() => setSelectedDataPoolId(pool.id)}>
                        <span>{pool.name}</span>
                        <small>{pool.description || `Updated ${new Date(pool.updated_at).toLocaleDateString()}`}</small>
                      </button>
                      <div className="settings-inline-actions">
                        <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={() => deleteDataPool(pool)}>
                          Delete
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="new-test-inline-note">No shared data pools have been created yet.</p>
              )}
            </div>

            {selectedDataPoolId && (
              <div className="settings-subsection">
                <div className="settings-manager-summary">
                  <div>
                    <span className="setting-label">{editingDataPoolRowId ? 'Edit Row' : 'Add Row'}</span>
                    <span className="setting-desc">Use JSON object keys as parameter names, for example customer_id, account_id, or zip.</span>
                  </div>
                  <strong>{editingDataPoolRowId ? 'Editing' : 'New'}</strong>
                </div>
                <textarea
                  className="settings-input cell-mono"
                  style={{ minHeight: 120, resize: 'vertical' }}
                  value={dataPoolRowJson}
                  onChange={event => setDataPoolRowJson(event.target.value)}
                />
                <div className="settings-inline-actions">
                  <button className="btn btn-primary" type="button" onClick={saveDataPoolRow}>
                    {editingDataPoolRowId ? 'Update Row' : 'Add Row'}
                  </button>
                  {editingDataPoolRowId && (
                    <button className="btn btn-secondary" type="button" onClick={() => { setEditingDataPoolRowId(null); setDataPoolRowJson('{\n  "customer_id": "C-10001"\n}') }}>
                      Cancel Edit
                    </button>
                  )}
                </div>

                {dataPoolRows.length > 0 ? (
                  <div className="script-parameter-table-wrap" style={{ marginTop: 16 }}>
                    <table className="script-parameter-table">
                      <thead>
                        <tr>
                          <th>Data</th>
                          <th>Status</th>
                          <th>Version</th>
                          <th>Updated</th>
                          <th>Actions</th>
                        </tr>
                      </thead>
                      <tbody>
                        {dataPoolRows.map(row => (
                          <tr key={row.id}>
                            <td className="cell-mono">{JSON.stringify(row.data)}</td>
                            <td>{row.status || 'active'}</td>
                            <td>{row.version}</td>
                            <td>{new Date(row.updated_at).toLocaleString()}</td>
                            <td>
                              <div className="settings-inline-actions">
                                <button className="btn btn-sm btn-secondary" type="button" onClick={() => editDataPoolRow(row)}>
                                  Edit
                                </button>
                                <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={() => deleteDataPoolRow(row)}>
                                  Delete
                                </button>
                              </div>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <p className="new-test-inline-note">No rows in this data pool yet.</p>
                )}
              </div>
            )}
          </div>
        </div>

        <div
          id="settings-section-integrations"
          className={sectionClass('integrations')}
          hidden={!visibleSections.some(section => section.id === 'integrations')}
          draggable={customizingSections}
          onDragStart={event => handleSectionDragStart(event, 'integrations')}
          onDragOver={event => handleSectionDragOver(event, 'integrations')}
          onDrop={event => handleSectionDrop(event, 'integrations')}
          onDragEnd={() => setDraggedSectionId(null)}
          style={{ order: getSectionOrder('integrations') }}
        >
          {renderSettingsHeader('integrations', 'Integrations')}
          <div className="settings-group">
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Prometheus</span>
                <span className="setting-desc">Export metrics to Prometheus endpoint</span>
              </div>
              <span className="setting-value text-muted">Phase 2</span>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Dynatrace</span>
                <span className="setting-desc">Push metrics to Dynatrace via OpenTelemetry</span>
              </div>
              <span className="setting-value text-muted">Phase 2</span>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">ELK Stack</span>
                <span className="setting-desc">Send logs and metrics to Elasticsearch</span>
              </div>
              <span className="setting-value text-muted">Phase 2</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

function readSettingsSectionArray(key: string): SettingsSectionId[] {
  if (typeof window === 'undefined') return []
  try {
    const parsed = JSON.parse(window.localStorage.getItem(key) || '[]')
    if (!Array.isArray(parsed)) return []
    return parsed.filter((value): value is SettingsSectionId =>
      SETTINGS_SECTIONS.some(section => section.id === value),
    )
  } catch {
    return []
  }
}

function mergeSettingsOrder(order: SettingsSectionId[]) {
  const ids = SETTINGS_SECTIONS.map(section => section.id)
  const retained = order.filter(id => ids.includes(id))
  const added = ids.filter(id => !retained.includes(id))
  return [...retained, ...added]
}

function moveSettingsSection(ids: SettingsSectionId[], draggedId: SettingsSectionId, targetId: SettingsSectionId) {
  if (draggedId === targetId) return ids
  const next = ids.filter(id => id !== draggedId)
  const targetIndex = next.indexOf(targetId)
  if (targetIndex === -1) return ids
  next.splice(targetIndex, 0, draggedId)
  return next
}
