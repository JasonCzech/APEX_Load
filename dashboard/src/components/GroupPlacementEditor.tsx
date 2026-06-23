import { GeneratorAssignmentSettings, SystemInventoryGenerator } from '../types'
import './GroupPlacementEditor.css'

type PlacementMode = 'inherit' | 'auto' | 'specific_hosts' | 'required_labels'

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
}

function normalizeLabels(labels?: Record<string, string>) {
  if (!labels) return undefined
  const next = Object.fromEntries(Object.entries(labels)
    .map(([key, value]) => [key.trim(), value.trim()])
    .filter(([key, value]) => key && value))
  return Object.keys(next).length > 0 ? next : undefined
}

function assignmentMode(assignment?: GeneratorAssignmentSettings): PlacementMode {
  if (assignment?.mode === 'specific_hosts') return 'specific_hosts'
  if (normalizeLabels(assignment?.required_labels)) return 'required_labels'
  if (assignment?.mode === 'auto') return 'auto'
  return 'inherit'
}

function labelSummary(labels?: Record<string, string>) {
  const entries = Object.entries(normalizeLabels(labels) || {})
  if (entries.length === 0) return 'No capabilities selected'
  return entries.map(([key, value]) => `${key}=${value}`).join(', ')
}

function generatorCanRun(generator: SystemInventoryGenerator, protocol?: string) {
  const drainActive = !generator.drain_state || generator.drain_state === 'active'
  const connected = ['connected', 'healthy', 'ready'].includes(generator.status)
  const compatible = !generator.compatibility_state || generator.compatibility_state === 'compatible' || generator.compatibility_state === 'unknown'
  const protocols = generator.supported_protocols || []
  const protocolSupported = !protocol || protocols.length === 0 || protocols.includes(protocol)
  return drainActive && connected && compatible && protocolSupported && (generator.available_capacity ?? 0) > 0
}

function generatorDisabledReason(generator: SystemInventoryGenerator, protocol?: string) {
  if (generator.drain_state && generator.drain_state !== 'active') return `Host is ${generator.drain_state}`
  if (!['connected', 'healthy', 'ready'].includes(generator.status)) return `Host is ${generator.status}`
  if (generator.compatibility_state && generator.compatibility_state !== 'compatible' && generator.compatibility_state !== 'unknown') return generator.compatibility_issues?.[0] || `Compatibility is ${generator.compatibility_state}`
  const protocols = generator.supported_protocols || []
  if (protocol && protocols.length > 0 && !protocols.includes(protocol)) return `Does not support ${protocol}`
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
  const labels = normalizeLabels(required)
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
}: Props) {
  const rawMode = assignmentMode(assignment)
  const mode = !allowInherit && rawMode === 'inherit' ? 'auto' : rawMode
  const selectedTargets = assignment?.selected_targets || []
  const requiredLabels = normalizeLabels(assignment?.required_labels)
  const rows = targetRows(availableTargets, generatorInventory, selectedTargets)
  const labelKeys = Object.keys(availableLabels || {}).sort()
  const matchingLabelHosts = generatorInventory?.filter(generator => generatorCanRun(generator, protocol) && labelsMatch(generator, requiredLabels)).length ?? 0
  const selectedTargetSet = new Set(selectedTargets)
  const selectedCapacity = rows.reduce((total, generator) => selectedTargetSet.has(generator.target) ? total + (generator.available_capacity || 0) : total, 0)

  const setMode = (nextMode: PlacementMode) => {
    if (nextMode === 'inherit') {
      onChange(undefined)
      return
    }
    if (nextMode === 'auto') {
      onChange({ mode: 'auto' })
      return
    }
    if (nextMode === 'specific_hosts') {
      onChange({ mode: 'specific_hosts', selected_targets: selectedTargets })
      return
    }
    onChange({ mode: 'auto', required_labels: requiredLabels || {} })
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
    })
  }

  const setRequiredLabel = (key: string, value: string) => {
    const next = { ...(requiredLabels || {}) }
    if (value) next[key] = value
    else delete next[key]
    onChange({ mode: 'auto', required_labels: next })
  }

  return (
    <div className={`group-placement-editor ${compact ? 'compact' : ''}`}>
      <div className="new-test-placement-toggle" role="radiogroup" aria-label="Group generator placement">
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
        <label className={mode === 'required_labels' ? 'selected' : ''}>
          <input type="radio" checked={mode === 'required_labels'} onChange={() => setMode('required_labels')} />
          <span>Require capability</span>
        </label>
      </div>

      {mode === 'inherit' && <p className="scenario-assignment-copy secondary">Inherits the scenario Controller / Generator assignment.</p>}
      {mode === 'auto' && <p className="scenario-assignment-copy secondary">Uses automatic placement for this group.</p>}

      {mode === 'specific_hosts' && (
        <div className="new-test-generator-picker">
          {rows.length === 0 ? (
            <div className="new-test-empty compact">No load generators are available.</div>
          ) : rows.map(generator => {
            const selected = selectedTargets.includes(generator.target)
            const eligible = generatorCanRun(generator, protocol)
            const disabledReason = generatorDisabledReason(generator, protocol)
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
                  <em>{generator.region || generator.pool_name || 'default'} · {generator.available_capacity || 0} capacity · {generator.status}{recommended ? ' · recommended' : ''}</em>
                  {disabledReason && <em className="warning">{disabledReason}</em>}
                </span>
              </label>
            )
          })}
          {selectedTargets.some(target => !rows.some(row => row.target === target && generatorCanRun(row, protocol))) && (
            <p className="scenario-assignment-copy secondary">One or more selected hosts are absent or ineligible and will block preflight or launch.</p>
          )}
          <div className="new-test-placement-summary">
            <strong>{formatHostSelectionSummary(selectedTargets)}</strong>
            <span>{selectedCapacity} selected VUser capacity</span>
          </div>
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
    </div>
  )
}
