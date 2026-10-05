import { useEffect, useId, useState } from 'react'
import { GeneratorAssignmentSettings, SystemInventoryGenerator } from '../types'
import { normalizeRequiredLabels } from './assignmentLabels'
import { isPositiveSafeInteger } from './generatorAssignmentValidation'
import './GroupPlacementEditor.css'
import PodCapacityMeter from './PodCapacityMeter'
import JavaHostPlacementEditor from './JavaHostPlacementEditor'

type PlacementMode = 'inherit' | 'auto' | 'specific_hosts' | 'required_labels' | 'region_weighted' | 'load_zone_weighted'

interface Props {
  assignment?: GeneratorAssignmentSettings
  onChange: (assignment: GeneratorAssignmentSettings | undefined) => void
  availableTargets?: string[]
  recommendedTargets?: string[]
  generatorInventory?: SystemInventoryGenerator[]
  availableLabels?: Record<string, string[]>
  protocol?: string
  compact?: boolean
  allowInherit?: boolean
  scope?: 'group' | 'scenario'
}

function assignmentMode(assignment?: GeneratorAssignmentSettings): PlacementMode {
  if (assignment?.mode === 'specific_hosts') return 'specific_hosts'
  if (assignment?.mode === 'region_weighted') return 'region_weighted'
  if (assignment?.mode === 'load_zone_weighted') return 'load_zone_weighted'
  if (normalizeRequiredLabels(assignment?.required_labels)) return 'required_labels'
  if (assignment?.mode === 'auto') return 'auto'
  if (assignment?.java_host && !assignment.mode) return 'auto' // Legacy profile-only drafts used automatic placement.
  return 'inherit'
}

function weightSummary(weights?: Record<string, number>) {
  const entries = Object.entries(weights || {})
  if (entries.length === 0) return 'No weights configured'
  return entries.map(([key, value]) => `${key}=${value}`).join(', ')
}

function labelSummary(labels?: Record<string, string>) {
  const entries = Object.entries(normalizeRequiredLabels(labels) || {})
  if (entries.length === 0) return 'No capabilities selected'
  return entries.map(([key, value]) => `${key}=${value}`).join(', ')
}

function protocolRuntimeCapacities(generator: SystemInventoryGenerator, protocol?: string) {
  const wanted = protocol?.trim().toLowerCase()
  if (!wanted) return []
  return (generator.runtime_capacities || []).filter(capacity => capacity.protocol.trim().toLowerCase() === wanted)
}

function protocolRuntimeAvailableVUsers(generator: SystemInventoryGenerator, protocol?: string): number | undefined {
  const capacities = protocolRuntimeCapacities(generator, protocol).filter(capacity => !capacity.available || capacity.max_vusers > 0 || !capacity.resources)
  if (capacities.length === 0) return undefined
  return capacities.reduce((available, capacity) => {
    const candidate = capacity.available && capacity.max_vusers > 0 && capacity.available_vusers > 0
      ? Math.min(capacity.available_vusers, capacity.max_vusers)
      : 0
    return Math.min(available, candidate)
  }, Number.POSITIVE_INFINITY)
}

function javaHostCeiling(generator: SystemInventoryGenerator, profile?: string): number {
  const caps = generator.java_host
  const matches = caps?.profiles.filter(item => item.profile_id === profile) || []
  if (!caps || caps.mode !== 'dedicated' || caps.containment !== 'execution_pod' || !matches.length) return 0
  const limits = [caps.max_hosts, caps.max_vusers, generator.available_capacity, ...matches.map(item => item.memory_bytes > 0 ? Math.floor(caps.max_memory_bytes / item.memory_bytes) : 0)]
  const pod = generator.pod_capacity
  if (pod) {
    if (pod.state !== 'ready' || pod.dedicated) return 0
    for (const item of matches) {
      if (!item.facade_resources_sha256 || !(item.facade_cpu_millicores! > 0) || !(item.facade_memory_bytes! > 0) || !(item.facade_network_kbps! > 0)) return 0
      limits.push(pod.remaining.cpu_cores * 1000 / item.facade_cpu_millicores!, pod.remaining.memory_mb * 1024 * 1024 / item.facade_memory_bytes!, pod.remaining.network_mbps * 1000 / item.facade_network_kbps!, pod.remaining.vusers, pod.remaining.protocols?.java_host_facade ?? 0)
    }
  }
  return Math.max(0, Math.floor(Math.min(...limits) + 1e-9))
}

function generatorCapacityForProtocol(generator: SystemInventoryGenerator, protocol?: string, javaProfile?: string): number | null {
  if (javaProfile) return javaHostCeiling(generator, javaProfile)
  // Configured fallback rows are target names without a live inventory sample.
  // Zero would falsely claim exhaustion, so keep their capacity explicitly unknown.
  if (generator.status === 'configured') return null
  const overall = Math.max(0, generator.available_capacity ?? 0)
  const runtimeAvailable = protocolRuntimeAvailableVUsers(generator, protocol)
  if (runtimeAvailable === undefined) return overall
  return Math.min(overall, runtimeAvailable)
}

function generatorCapacityLabel(generator: SystemInventoryGenerator, protocol?: string, javaProfile?: string): string {
  if (javaProfile) return `${javaHostCeiling(generator, javaProfile)} host ceiling · reservation required`
  const capacity = generatorCapacityForProtocol(generator, protocol)
  const base = capacity === null ? 'capacity unknown' : `${capacity} capacity`
  const resources = protocolRuntimeCapacities(generator, protocol).find(item => item.resources)?.resources
  if (!resources) return base
  if (!resources.available) return `${base} · processor capacity unavailable`
  return `${base} · ${resources.available_processors}/${resources.max_processors} processors, ${resources.available_workers}/${resources.max_workers} workers available · ${resources.draining_processors} processors/${resources.draining_workers} workers draining`
}

function generatorCanRun(generator: SystemInventoryGenerator, protocol?: string, javaProfile?: string) {
  if (javaProfile) return (!generator.drain_state || generator.drain_state === 'active') && (!generator.compatibility_state || ['compatible', 'unknown'].includes(generator.compatibility_state)) && ['connected', 'healthy', 'ready'].includes(generator.status) && javaHostCeiling(generator, javaProfile) > 0
  if (generator.pod_capacity && (generator.pod_capacity.state !== 'ready' || generator.pod_capacity.remaining_pct <= 0 || generator.pod_capacity.dedicated)) return false
  const drainActive = !generator.drain_state || generator.drain_state === 'active'
  const compatible = !generator.compatibility_state || generator.compatibility_state === 'compatible' || generator.compatibility_state === 'unknown'
  const protocols = generator.supported_protocols || []
  const runtimeAvailable = protocolRuntimeAvailableVUsers(generator, protocol)
  const protocolSupported = runtimeAvailable !== undefined
    ? runtimeAvailable > 0
    : !protocol || protocols.length === 0 || protocols.includes(protocol)
  // Synthetic fallback rows (no live inventory) carry status 'configured' with no
  // capacity/health data — keep them selectable so recommendation hostnames remain
  // pinnable when /system/inventory is empty or unavailable (M3).
  if (generator.status === 'configured') {
    return drainActive && compatible && protocolSupported
  }
  const connected = ['connected', 'healthy', 'ready'].includes(generator.status)
  const capacityAvailable = (generator.available_capacity ?? 0) > 0
    && (runtimeAvailable === undefined || runtimeAvailable > 0)
  return drainActive && connected && compatible && protocolSupported && capacityAvailable
}

function generatorDisabledReason(generator: SystemInventoryGenerator, protocol?: string, javaProfile?: string) {
  if (javaProfile) return generatorCanRun(generator, protocol, javaProfile) ? '' : 'Dedicated Java profile unavailable or incompatible with generator placement settings'
  if (generator.drain_state && generator.drain_state !== 'active') return `Host is ${generator.drain_state}`
  if (generator.compatibility_state && generator.compatibility_state !== 'compatible' && generator.compatibility_state !== 'unknown') return generator.compatibility_issues?.[0] || `Compatibility is ${generator.compatibility_state}`
  const protocols = generator.supported_protocols || []
  const runtimeCapacities = protocolRuntimeCapacities(generator, protocol)
  const runtimeAvailable = protocolRuntimeAvailableVUsers(generator, protocol)
  if (runtimeCapacities.some(capacity => !capacity.available)) return `${protocol} runtime is unavailable`
  if (runtimeAvailable !== undefined && runtimeAvailable <= 0) return `No available ${protocol} runtime capacity`
  if (protocol && protocols.length > 0 && !protocols.includes(protocol)) return `Does not support ${protocol}`
  // Fallback rows have no live status/capacity to report on once the above
  // configuration checks pass.
  if (generator.status === 'configured') return ''
  if (!['connected', 'healthy', 'ready'].includes(generator.status)) return `Host is ${generator.status}`
  if ((generator.available_capacity ?? 0) <= 0) return 'No available capacity'
  return ''
}

function targetRows(availableTargets: string[], generatorInventory: SystemInventoryGenerator[] | undefined, selectedTargets: string[]): SystemInventoryGenerator[] {
  if (generatorInventory?.length) {
    const selected = new Set(selectedTargets)
    const rows: SystemInventoryGenerator[] = [...generatorInventory]
    selectedTargets.forEach(target => {
      if (!rows.some(generator => generator.target === target)) {
        rows.push({
          target,
          generator_id: target,
          status: 'unreachable',
          available_capacity: 0,
          active_vusers: 0,
          cpu_percent: 0,
          memory_bytes: 0,
          goroutine_count: 0,
          cpu_limit_cores: 0,
          memory_limit_bytes: 0,
          max_vusers: 0,
        } satisfies SystemInventoryGenerator)
      }
    })
    return rows.sort((left, right) => Number(selected.has(right.target)) - Number(selected.has(left.target)) || left.target.localeCompare(right.target))
  }
  return Array.from(new Set([...selectedTargets, ...availableTargets])).map(target => ({
    target,
    generator_id: target,
    status: 'configured',
    available_capacity: 0,
    active_vusers: 0,
    cpu_percent: 0,
    memory_bytes: 0,
    goroutine_count: 0,
    cpu_limit_cores: 0,
    memory_limit_bytes: 0,
    max_vusers: 0,
  } satisfies SystemInventoryGenerator))
}

function labelsMatch(generator: SystemInventoryGenerator, required?: Record<string, string>) {
  const labels = normalizeRequiredLabels(required)
  if (!labels) return true
  return Object.entries(labels).every(([key, value]) => generator.labels?.[key] === value)
}

function formatHostSelectionSummary(selectedTargets: string[]) {
  if (selectedTargets.length === 0) return 'No hosts selected'
  if (selectedTargets.length === 1) return `Pinned: ${selectedTargets[0]}`
  return `Sharded: ${selectedTargets.length} hosts, capacity weighted`
}

export default function GroupPlacementEditor({
  assignment,
  onChange,
  availableTargets = [],
  recommendedTargets = [],
  generatorInventory,
  availableLabels,
  protocol,
  compact = false,
  allowInherit = true,
  scope = 'group',
}: Props) {
  const placementId = useId()
  const rawMode = assignmentMode(assignment)
  const derivedMode: PlacementMode = !allowInherit && rawMode === 'inherit' ? 'auto' : rawMode
  const unsupportedGroupWeighted = scope === 'group' && (rawMode === 'region_weighted' || rawMode === 'load_zone_weighted')
  const unsupportedGroupLossTolerance = scope === 'group' && Boolean(assignment?.tolerate_generator_loss)
  const selectedTargets = assignment?.selected_targets || []
  const requiredLabels = normalizeRequiredLabels(assignment?.required_labels)

  // The "Require capability" mode serializes as { mode:'auto', required_labels }, so
  // until the user picks a label it is indistinguishable from plain "auto" in the
  // serialized assignment. Track the selected mode locally so the capability panel
  // stays open while labels are still empty, instead of snapping back to auto (H2).
  const [mode, setLocalMode] = useState<PlacementMode>(derivedMode)
  const [newWeightKey, setNewWeightKey] = useState('')
  const [newWeightValue, setNewWeightValue] = useState('1')
  const [weightError, setWeightError] = useState('')
  useEffect(() => {
    setLocalMode(prev => {
      if (prev === 'required_labels' && derivedMode === 'auto' && !normalizeRequiredLabels(assignment?.required_labels)) {
        return prev
      }
      return derivedMode
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [derivedMode])

  const rows = targetRows(availableTargets, generatorInventory, selectedTargets)
  const labelKeys = Object.keys(availableLabels || {}).sort()
  const matchingLabelHosts = generatorInventory?.filter(generator => generatorCanRun(generator, protocol, assignment?.java_host?.profile_id) && labelsMatch(generator, requiredLabels)).length ?? 0
  const selectedTargetSet = new Set(selectedTargets)
  const selectedRows = rows.filter(generator => selectedTargetSet.has(generator.target))
  const selectedCapacity = selectedRows.reduce((total, generator) => total + (generatorCapacityForProtocol(generator, protocol, assignment?.java_host?.profile_id) ?? 0), 0)
  const selectedUnknownCapacityCount = selectedRows.filter(generator => generatorCapacityForProtocol(generator, protocol, assignment?.java_host?.profile_id) === null).length
  const selectedCapacitySummary = assignment?.java_host ? 'Eligible generators share the group; the broker confirms each reservation.' : selectedUnknownCapacityCount === 0
    ? `${selectedCapacity} selected VUser capacity`
    : selectedCapacity > 0
      ? `${selectedCapacity} known selected VUser capacity · ${selectedUnknownCapacityCount} host${selectedUnknownCapacityCount === 1 ? '' : 's'} unknown`
      : 'Selected VUser capacity unknown'
  const lossTolerance = {
    ...(scope === 'group' && assignment?.java_host ? { java_host: assignment.java_host } : {}),
    ...(scope === 'scenario' && assignment?.tolerate_generator_loss ? { tolerate_generator_loss: true as const } : {}),
    ...(scope === 'scenario' && assignment?.sharing_mode ? { sharing_mode: assignment.sharing_mode } : {}),
    ...(scope === 'scenario' && assignment?.failover_policy === 'continue' ? { failover_policy: 'continue' as const } : {}),
    ...(scope === 'scenario' && assignment?.recovery_spare_vusers !== undefined ? { recovery_spare_vusers: assignment?.recovery_spare_vusers } : {}),
  }
  const regionKeys = Array.from(new Set((generatorInventory || []).map(generator => generator.region?.trim()).filter((value): value is string => Boolean(value)))).sort()
  // The controller resolves both weighting modes against target-spec regions
  // (newRegionPlacementState). Inventory labels are capability metadata and do
  // not define independently schedulable load zones.
  const loadZoneKeys = regionKeys
  // Topology is a configuration requirement; temporary utilization or health
  // changes should not remove placement choices from an authored scenario.
  const regionWeightingAvailable = regionKeys.length >= 2
  const loadZoneWeightingAvailable = loadZoneKeys.length >= 2

  const setMode = (nextMode: PlacementMode) => {
    setLocalMode(nextMode)
    if (nextMode === 'inherit') {
      onChange(assignment?.java_host ? { mode: 'inherit', java_host: assignment.java_host } : undefined)
      return
    }
    if (nextMode === 'auto') {
      onChange({ mode: 'auto', ...lossTolerance })
      return
    }
    if (nextMode === 'specific_hosts') {
      onChange({
        mode: 'specific_hosts',
        selected_targets: selectedTargets,
        ...(requiredLabels ? { required_labels: requiredLabels } : {}),
        ...lossTolerance,
      })
      return
    }
    if (nextMode === 'region_weighted') {
      const existing = assignment?.region_weights || {}
      const weights = Object.keys(existing).length ? existing : Object.fromEntries(regionKeys.map(key => [key, 1]))
      onChange({ mode: 'region_weighted', region_weights: { ...weights }, ...(requiredLabels ? { required_labels: requiredLabels } : {}), ...lossTolerance })
      return
    }
    if (nextMode === 'load_zone_weighted') {
      const existing = assignment?.load_zone_weights || {}
      const weights = Object.keys(existing).length ? existing : Object.fromEntries(loadZoneKeys.map(key => [key, 1]))
      onChange({ mode: 'load_zone_weighted', load_zone_weights: { ...weights }, ...(requiredLabels ? { required_labels: requiredLabels } : {}), ...lossTolerance })
      return
    }
    onChange({ mode: 'auto', required_labels: requiredLabels || {}, ...lossTolerance })
  }

  const toggleTarget = (target: string, checked: boolean) => {
    const nextTargets = checked
      ? Array.from(new Set([...selectedTargets, target]))
      : selectedTargets.filter(value => value !== target)
    onChange({
      mode: 'specific_hosts',
      selected_targets: nextTargets,
      ...(nextTargets.length > 1 ? { shard_strategy: 'capacity_weighted' as const } : {}),
      ...(requiredLabels ? { required_labels: requiredLabels } : {}),
      ...lossTolerance,
    })
  }

  const setRequiredLabel = (key: string, value: string) => {
    const next = { ...(requiredLabels || {}) }
    if (value) next[key] = value
    else delete next[key]
    onChange({ mode: 'auto', required_labels: next, ...lossTolerance })
  }

  const setWeight = (key: string, rawValue: string) => {
    const field = mode === 'region_weighted' ? 'region_weights' : 'load_zone_weights'
    const current = { ...(assignment?.[field] || {}) }
    const value = Number(rawValue)
    if (rawValue.trim() === '') {
      delete current[key]
      setWeightError(Object.keys(current).length === 0 ? 'Add at least one positive whole-number weight.' : '')
    } else if (!isPositiveSafeInteger(value)) {
      setWeightError('Weights must be positive whole numbers.')
      return
    } else {
      current[key] = value
      setWeightError('')
    }
    onChange({
      mode,
      [field]: current,
      ...(requiredLabels ? { required_labels: requiredLabels } : {}),
      ...lossTolerance,
    } as GeneratorAssignmentSettings)
  }

  const addWeight = () => {
    const key = newWeightKey.trim()
    const value = Number(newWeightValue)
    if (!key) {
      setWeightError('Enter a region or load-zone name.')
      return
    }
    if (!isPositiveSafeInteger(value)) {
      setWeightError('Weights must be positive whole numbers.')
      return
    }
    setWeight(key, String(value))
    setNewWeightKey('')
    setNewWeightValue('1')
  }

  const setLossTolerance = (enabled: boolean) => {
    const next: GeneratorAssignmentSettings = assignment ? { ...assignment } : { mode: 'auto' }
    if (enabled) next.tolerate_generator_loss = true
    else {
      delete next.tolerate_generator_loss
      delete next.recovery_spare_vusers
    }
    onChange(next)
  }

  const setFailoverContinuity = (enabled: boolean) => {
    const next: GeneratorAssignmentSettings = assignment ? { ...assignment } : { mode: 'auto' }
    if (enabled) next.failover_policy = 'continue'
    else delete next.failover_policy
    onChange(next)
  }

  const removeUnsupportedGroupLossTolerance = () => {
    if (!assignment) return
    const next = { ...assignment }
    delete next.tolerate_generator_loss
    onChange(next)
  }

  const useAutomaticGroupPlacement = () => {
    setLocalMode('auto')
    onChange({ mode: 'auto', ...lossTolerance })
  }

  const activeWeights = mode === 'region_weighted' ? assignment?.region_weights : assignment?.load_zone_weights
  const activeWeightEntries = Object.entries(activeWeights || {})
  const storedWeightError = activeWeightEntries.length === 0
    ? 'Add at least one positive whole-number weight.'
    : activeWeightEntries.some(([key, value]) => !key.trim() || !isPositiveSafeInteger(value))
      ? 'Weight names must be non-empty and weights must be positive whole numbers.'
      : ''
  const newWeightIsValid = isPositiveSafeInteger(Number(newWeightValue))
  const newWeightDraftError = newWeightValue.trim() && !newWeightIsValid ? 'Weights must be positive whole numbers.' : ''

  return (
    <div className={`group-placement-editor ${compact ? 'compact' : ''}`}>
      {scope === 'group' && protocol === 'java_vuser' && <JavaHostPlacementEditor assignment={assignment} inventory={generatorInventory} onChange={onChange} />}
      {scope === 'scenario' && <label className="scenario-field">
        <span>Generator sharing</span>
        <select aria-label="Generator sharing" value={assignment?.sharing_mode || 'shared'} onChange={event => onChange({ ...assignment, mode: assignment?.mode || 'auto', sharing_mode: event.target.value as 'shared' | 'dedicated' })}>
          <option value="shared">Shared capacity</option>
          <option value="dedicated">Dedicated generators</option>
        </select>
      </label>}
      <div className="new-test-placement-toggle" role="radiogroup" aria-label={`${scope === 'scenario' ? 'Scenario' : 'Group'} generator placement`}>
        {allowInherit && (
          <label className={mode === 'inherit' ? 'selected' : ''}>
            <input type="radio" checked={mode === 'inherit'} onChange={() => setMode('inherit')} />
            <span>Inherit scenario default</span>
          </label>
        )}
        <label className={mode === 'auto' ? 'selected' : ''}>
          <input type="radio" checked={mode === 'auto'} onChange={() => setMode('auto')} />
          <span>Automatic placement</span>
        </label>
        <label className={mode === 'specific_hosts' ? 'selected' : ''}>
          <input type="radio" checked={mode === 'specific_hosts'} onChange={() => setMode('specific_hosts')} />
          <span>Pin to host(s)</span>
        </label>
        {scope === 'scenario' && (
          <>
            <label className={`${mode === 'region_weighted' ? 'selected' : ''} ${!regionWeightingAvailable ? 'disabled' : ''}`}>
              <input type="radio" aria-label="Region-weighted placement" aria-describedby={!regionWeightingAvailable ? `${placementId}-region-unavailable` : undefined} disabled={!regionWeightingAvailable} checked={mode === 'region_weighted'} onChange={() => setMode('region_weighted')} />
              <span>Region-weighted placement{!regionWeightingAvailable && <small id={`${placementId}-region-unavailable`}>Requires generators in at least two configured regions.</small>}</span>
            </label>
            <label className={`${mode === 'load_zone_weighted' ? 'selected' : ''} ${!loadZoneWeightingAvailable ? 'disabled' : ''}`}>
              <input type="radio" aria-label="Load-zone-weighted placement" aria-describedby={!loadZoneWeightingAvailable ? `${placementId}-zone-unavailable` : undefined} disabled={!loadZoneWeightingAvailable} checked={mode === 'load_zone_weighted'} onChange={() => setMode('load_zone_weighted')} />
              <span>Load-zone-weighted placement{!loadZoneWeightingAvailable && <small id={`${placementId}-zone-unavailable`}>Requires generators in at least two configured load zones.</small>}</span>
            </label>
          </>
        )}
        <label className={mode === 'required_labels' ? 'selected' : ''}>
          <input type="radio" checked={mode === 'required_labels'} onChange={() => setMode('required_labels')} />
          <span>Require capability</span>
        </label>
      </div>

      {mode === 'inherit' && <p className="scenario-assignment-copy secondary">Inherits the scenario Controller / Generator assignment.</p>}
      {mode === 'auto' && <p className="scenario-assignment-copy secondary">Uses automatic placement for this {scope}.</p>}
      {unsupportedGroupWeighted && (
        <div className="settings-error-inline" role="alert">
          Weighted placement is not supported for individual groups. Choose inherit, automatic placement, host pinning, or a required capability.
          <button type="button" className="secondary-button" onClick={useAutomaticGroupPlacement}>Use automatic placement</button>
        </div>
      )}
      {unsupportedGroupLossTolerance && (
        <div className="settings-error-inline" role="alert">
          Generator-loss tolerance is executed only at scenario scope.
          <button type="button" className="secondary-button" onClick={removeUnsupportedGroupLossTolerance}>Remove unsupported setting</button>
        </div>
      )}
      {scope === 'scenario' && (mode === 'region_weighted' || mode === 'load_zone_weighted') && (() => {
        const weights = mode === 'region_weighted' ? assignment?.region_weights : assignment?.load_zone_weights
        return (
          <div className="new-test-generator-picker weighted-placement-editor">
            <p className="scenario-assignment-copy secondary">
              {mode === 'region_weighted' ? 'Region' : 'Load-zone'} weights: {weightSummary(weights)}. Each weight must be greater than zero.
            </p>
            {Object.entries(weights || {}).map(([key, value]) => (
              <label key={key} className="scenario-field">
                <span>{key}</span>
                <input
                  aria-label={`${mode === 'region_weighted' ? 'Region' : 'Load zone'} ${key} weight`}
                  className="settings-input"
                  type="number"
                  min={1}
                  step={1}
                  value={value}
                  aria-invalid={!isPositiveSafeInteger(value)}
                  onChange={event => setWeight(key, event.target.value)}
                />
              </label>
            ))}
            <div className="scenario-browser-grid">
              <label><span>{mode === 'region_weighted' ? 'Region' : 'Load zone'} name</span><input aria-label="New placement weight name" className="settings-input" value={newWeightKey} onChange={event => setNewWeightKey(event.target.value)} /></label>
              <label><span>Weight</span><input aria-label="New placement weight value" className="settings-input" type="number" min={1} step={1} value={newWeightValue} aria-invalid={!newWeightIsValid} onChange={event => setNewWeightValue(event.target.value)} /></label>
              <button type="button" className="secondary-button" disabled={!newWeightKey.trim() || !newWeightIsValid} onClick={addWeight}>Add weight</button>
            </div>
            {(weightError || storedWeightError || newWeightDraftError) && <p className="settings-error-inline" role="alert">{weightError || storedWeightError || newWeightDraftError}</p>}
          </div>
        )
      })()}

      {mode === 'specific_hosts' && (
        <div className="new-test-generator-picker">
          {rows.length === 0 ? (
            <div className="new-test-empty compact">No load generators are available.</div>
          ) : rows.map(generator => {
            const selected = selectedTargets.includes(generator.target)
            const eligible = generatorCanRun(generator, protocol, assignment?.java_host?.profile_id)
            const disabledReason = generatorDisabledReason(generator, protocol, assignment?.java_host?.profile_id)
            const recommended = recommendedTargets.includes(generator.target)
            return (
              <label key={generator.target} className={`new-test-generator-option ${selected ? 'selected' : ''} ${!eligible ? 'disabled' : ''}`}>
                <input
                  type="checkbox"
                  checked={selected}
                  disabled={!eligible && !selected}
                  onChange={event => toggleTarget(generator.target, event.target.checked)}
                />
                <span>
                  <strong>{generator.target}</strong>
                  <PodCapacityMeter capacity={generator.pod_capacity} />
                  <em>{generator.region || generator.pool_name || 'default'} · {generatorCapacityLabel(generator, protocol, assignment?.java_host?.profile_id)} · {generator.status}{recommended ? ' · recommended' : ''}</em>
                  {disabledReason && <em className="warning">{disabledReason}</em>}
                </span>
              </label>
            )
          })}
          {selectedTargets.some(target => !rows.some(row => row.target === target && generatorCanRun(row, protocol, assignment?.java_host?.profile_id))) && (
            <p className="scenario-assignment-copy secondary">One or more selected hosts are absent or ineligible and will block preflight or launch.</p>
          )}
          <div className="new-test-placement-summary">
            <strong>{formatHostSelectionSummary(selectedTargets)}</strong>
            <span>{selectedCapacitySummary}</span>
          </div>
          {selectedTargets.every(target => !target.trim()) && <p className="settings-error-inline" role="alert">Select at least one load generator host.</p>}
        </div>
      )}

      {mode === 'required_labels' && (
        <div className="new-test-generator-picker">
          {labelKeys.length === 0 ? (
            <div className="new-test-empty compact">No generator capabilities are available.</div>
          ) : labelKeys.map(key => (
            <label key={key} className="scenario-field">
              <span>{key}</span>
              <select className="settings-input" value={requiredLabels?.[key] || ''} onChange={event => setRequiredLabel(key, event.target.value)}>
                <option value="">Any</option>
                {(availableLabels?.[key] || []).map(value => <option key={value} value={value}>{value}</option>)}
              </select>
            </label>
          ))}
          <div className="new-test-placement-summary">
            <strong>{labelSummary(requiredLabels)}</strong>
            <span>{matchingLabelHosts} matching generator{matchingLabelHosts === 1 ? '' : 's'}</span>
          </div>
        </div>
      )}

      {scope === 'scenario' && mode !== 'inherit' && (
        <label className={`scenario-option-card ${assignment?.tolerate_generator_loss ? 'selected' : ''}`}>
          <input type="checkbox" checked={Boolean(assignment?.tolerate_generator_loss)} onChange={event => setLossTolerance(event.target.checked)} />
          <div><strong>Tolerate generator loss</strong><span>Continue on surviving assigned generators when policy permits.</span></div>
        </label>
      )}
      {scope === 'scenario' && mode !== 'inherit' && assignment?.tolerate_generator_loss && (
        <label className="recovery-spare-budget">
          <span>Recovery spare VUser budget</span>
          <input type="number" aria-label="Recovery spare VUser budget" min={0} max={1000000} step={1}
            value={assignment.recovery_spare_vusers ?? 0}
            onChange={event => onChange({ ...assignment, recovery_spare_vusers: Number(event.target.value) })} />
          <small>Zero disables spare reservations. A positive budget reserves idle generators on other workers and counts toward project and pool quotas. Admission requires stateless HTTP and observed Kubernetes capacity. A generator lost during a controller failover still fails the run.</small>
        </label>
      )}
      {scope === 'scenario' && mode !== 'inherit' && (
        <label className={`scenario-option-card ${assignment?.failover_policy === 'continue' ? 'selected' : ''}`}>
          <input
            type="checkbox"
            aria-label="Continue through controller failover"
            checked={assignment?.failover_policy === 'continue'}
            onChange={event => setFailoverContinuity(event.target.checked)}
          />
          <div>
            <strong>Continue through controller failover</strong>
            <span>{assignment?.tolerate_generator_loss
              ? 'On an HA controller, a standby adopts this run if the leader is lost. A generator lost while no controller is leading fails the run instead of being evacuated.'
              : 'On an HA controller, a standby adopts this run if the leader is lost instead of failing it. Launch checks warn when this deployment cannot honour it.'}</span>
          </div>
        </label>
      )}
    </div>
  )
}
