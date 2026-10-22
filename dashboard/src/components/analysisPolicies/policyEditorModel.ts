// Browser and journey policy editors (BI-4): form drafts and the definitions
// the controller stores (pkg/api/analysis_policies.go). The controller
// validates every save; the checks here only explain a draft that cannot be
// saved before it is sent.

export const BROWSER_POLICY_METRICS = ['lcp', 'fcp', 'inp', 'cls'] as const
export const BROWSER_TIMING_METRICS = ['lcp', 'fcp', 'inp'] as const
export type BrowserTimingMetricName = typeof BROWSER_TIMING_METRICS[number]
export type BrowserPolicyMetricName = typeof BROWSER_POLICY_METRICS[number]

export interface BrowserMetricDraft { include: boolean; good: string; poor: string; weight: string }
export interface BrowserPolicyDraft {
  name: string
  description: string
  applyByDefault: boolean
  metrics: Record<BrowserPolicyMetricName, BrowserMetricDraft>
  engines: { chromium: boolean; webkit: boolean }
  deviceProfiles: string
  floorTimingByMetricMs: Record<BrowserTimingMetricName, string>
  floorTimingMs: string
  floorTimingRelative: string
  floorCls: string
  baselineMode: 'none' | 'latest_compatible' | 'pinned_run'
  baselineRunId: string
}

export interface JourneyDraft {
  id: string
  name: string
  startKind: 'transaction' | 'action'
  start: string
  successKind: 'transaction' | 'action'
  success: string
  weight: string
}
export interface JourneyPolicyDraft {
  name: string
  description: string
  applyByDefault: boolean
  stepRiskEnabled: boolean
  journeys: JourneyDraft[]
}

/** The published Core Web Vitals good and poor bounds, weighted toward loading and responsiveness. */
export function defaultBrowserPolicyDraft(): BrowserPolicyDraft {
  return {
    name: '',
    description: '',
    applyByDefault: false,
    metrics: {
      lcp: { include: true, good: '2500', poor: '4000', weight: '35' },
      fcp: { include: true, good: '1800', poor: '3000', weight: '15' },
      inp: { include: true, good: '200', poor: '500', weight: '30' },
      cls: { include: true, good: '0.1', poor: '0.25', weight: '20' },
    },
    engines: { chromium: false, webkit: false },
    deviceProfiles: '',
    floorTimingByMetricMs: { lcp: '', fcp: '', inp: '' },
    floorTimingMs: '',
    floorTimingRelative: '',
    floorCls: '',
    baselineMode: 'latest_compatible',
    baselineRunId: '',
  }
}

export function emptyJourney(index: number): JourneyDraft {
  return { id: `journey-${index + 1}`, name: '', startKind: 'transaction', start: '', successKind: 'transaction', success: '', weight: '1' }
}

export function defaultJourneyPolicyDraft(): JourneyPolicyDraft {
  return { name: '', description: '', applyByDefault: false, stepRiskEnabled: false, journeys: [emptyJourney(0)] }
}

type Record_ = Record<string, unknown>
const record = (value: unknown): value is Record_ => Boolean(value) && typeof value === 'object' && !Array.isArray(value)
const numberText = (value: unknown) => typeof value === 'number' && Number.isFinite(value) ? String(value) : ''

export function browserDraftFromDefinition(name: string, definition: unknown): BrowserPolicyDraft {
  const draft = defaultBrowserPolicyDraft()
  draft.name = name
  if (!record(definition)) return draft
  draft.description = typeof definition.description === 'string' ? definition.description : ''
  draft.applyByDefault = definition.apply_by_default === true
  for (const metric of BROWSER_POLICY_METRICS) draft.metrics[metric] = { ...draft.metrics[metric], include: false }
  for (const entry of Array.isArray(definition.metrics) ? definition.metrics : []) {
    if (!record(entry) || !BROWSER_POLICY_METRICS.includes(entry.metric as BrowserPolicyMetricName)) continue
    draft.metrics[entry.metric as BrowserPolicyMetricName] = { include: true, good: numberText(entry.good), poor: numberText(entry.poor), weight: numberText(entry.weight) }
  }
  const applicability = record(definition.applicability) ? definition.applicability : {}
  const engines = Array.isArray(applicability.engines) ? applicability.engines : []
  draft.engines = { chromium: engines.includes('chromium'), webkit: engines.includes('webkit') }
  draft.deviceProfiles = Array.isArray(applicability.device_profiles) ? applicability.device_profiles.filter(value => typeof value === 'string').join(', ') : ''
  const floors = record(definition.effect_floors) ? definition.effect_floors : {}
  const metricFloors = record(floors.timing_by_metric_ms) ? floors.timing_by_metric_ms : {}
  for (const metric of BROWSER_TIMING_METRICS) draft.floorTimingByMetricMs[metric] = numberText(metricFloors[metric])
  draft.floorTimingMs = numberText(floors.timing_ms)
  draft.floorTimingRelative = numberText(floors.timing_relative)
  draft.floorCls = numberText(floors.cls)
  const baseline = record(definition.baseline) ? definition.baseline : {}
  draft.baselineMode = baseline.mode === 'none' || baseline.mode === 'pinned_run' ? baseline.mode : 'latest_compatible'
  draft.baselineRunId = typeof baseline.run_id === 'string' ? baseline.run_id : ''
  return draft
}

const parsed = (text: string) => text.trim() === '' ? undefined : Number(text)

/** The definition a browser draft saves, or the first reason it cannot be saved. */
export function browserDefinitionFromDraft(draft: BrowserPolicyDraft): { definition?: Record_; problem?: string } {
  if (!draft.name.trim()) return { problem: 'Name the policy.' }
  const metrics: Record_[] = []
  let total = 0
  for (const metric of BROWSER_POLICY_METRICS) {
    const entry = draft.metrics[metric]
    if (!entry.include) continue
    const good = parsed(entry.good), poor = parsed(entry.poor), weight = parsed(entry.weight)
    if (good === undefined || poor === undefined || weight === undefined || ![good, poor, weight].every(Number.isFinite)) return { problem: `Give ${metric.toUpperCase()} a good bound, a poor bound and a weight.` }
    if (!(good >= 0 && good < poor)) return { problem: `${metric.toUpperCase()}'s good bound must be at least 0 and below its poor bound.` }
    if (weight < 0) return { problem: 'Weights cannot be negative.' }
    total += weight
    metrics.push({ metric, good, poor, weight })
  }
  if (metrics.length === 0) return { problem: 'Score at least one metric.' }
  if (total <= 0) return { problem: 'At least one weight must be above zero.' }
  const engines = (['chromium', 'webkit'] as const).filter(engine => draft.engines[engine])
  const deviceProfiles = draft.deviceProfiles.split(',').map(value => value.trim()).filter(Boolean)
  const floors: Record_ = {}
  for (const [key, text] of [['timing_ms', draft.floorTimingMs], ['timing_relative', draft.floorTimingRelative], ['cls', draft.floorCls]] as const) {
    const value = parsed(text)
    if (value === undefined) continue
    if (!Number.isFinite(value)) return { problem: 'Effect floors must be numbers.' }
    floors[key] = value
  }
  const metricFloors: Record_ = {}
  for (const metric of BROWSER_TIMING_METRICS) {
    const value = parsed(draft.floorTimingByMetricMs[metric])
    if (value === undefined) continue
    if (!Number.isFinite(value) || value < 50 || value > 600_000) return { problem: `${metric.toUpperCase()} floor must be between 50 and 600000 ms.` }
    metricFloors[metric] = value
  }
  if (Object.keys(metricFloors).length) floors.timing_by_metric_ms = metricFloors
  if (draft.baselineMode === 'pinned_run' && !draft.baselineRunId.trim()) return { problem: 'A pinned baseline needs the run to compare with.' }
  return {
    definition: {
      version: 1,
      ...(draft.applyByDefault ? { apply_by_default: true } : {}),
      ...(draft.description.trim() ? { description: draft.description.trim() } : {}),
      metrics,
      ...(engines.length || deviceProfiles.length ? { applicability: { ...(engines.length ? { engines } : {}), ...(deviceProfiles.length ? { device_profiles: deviceProfiles } : {}) } } : {}),
      ...(Object.keys(floors).length ? { effect_floors: floors } : {}),
      baseline: { mode: draft.baselineMode, ...(draft.baselineMode === 'pinned_run' ? { run_id: draft.baselineRunId.trim() } : {}) },
    },
  }
}

export function journeyDraftFromDefinition(name: string, definition: unknown): JourneyPolicyDraft {
  const draft = defaultJourneyPolicyDraft()
  draft.name = name
  if (!record(definition)) return draft
  draft.description = typeof definition.description === 'string' ? definition.description : ''
  draft.applyByDefault = definition.apply_by_default === true
  draft.stepRiskEnabled = definition.step_risk_enabled === true
  const boundary = (value: unknown): [JourneyDraft['startKind'], string] => {
    if (record(value) && typeof value.action === 'string' && value.action) return ['action', value.action]
    return ['transaction', record(value) && typeof value.transaction === 'string' ? value.transaction : '']
  }
  const journeys = (Array.isArray(definition.journeys) ? definition.journeys : []).filter(record).map(journey => {
    const [startKind, start] = boundary(journey.start)
    const [successKind, success] = boundary(journey.success)
    return { id: typeof journey.id === 'string' ? journey.id : '', name: typeof journey.name === 'string' ? journey.name : '', startKind, start, successKind, success, weight: numberText(journey.weight) }
  })
  draft.journeys = journeys.length ? journeys : [emptyJourney(0)]
  return draft
}

const JOURNEY_ID = /^[a-z0-9][a-z0-9_-]{0,63}$/

/** The definition a journey draft saves, or the first reason it cannot be saved. */
export function journeyDefinitionFromDraft(draft: JourneyPolicyDraft): { definition?: Record_; problem?: string } {
  if (!draft.name.trim()) return { problem: 'Name the policy.' }
  if (draft.journeys.length === 0) return { problem: 'Define at least one journey.' }
  const seen = new Set<string>()
  const journeys: Record_[] = []
  for (const journey of draft.journeys) {
    const id = journey.id.trim()
    if (!JOURNEY_ID.test(id) || seen.has(id)) return { problem: `Journey ID "${id}" must be a unique lowercase slug (letters, digits, - and _).` }
    seen.add(id)
    if (!journey.name.trim()) return { problem: `Name journey ${id}.` }
    if (!journey.start.trim() || !journey.success.trim()) return { problem: `Journey ${id} needs a start and a success boundary.` }
    const weight = parsed(journey.weight)
    if (weight === undefined || !Number.isFinite(weight) || weight < 0) return { problem: `Journey ${id} needs a weight of zero or more.` }
    journeys.push({
      id,
      name: journey.name.trim(),
      start: { [journey.startKind]: journey.start.trim() },
      success: { [journey.successKind]: journey.success.trim() },
      weight,
    })
  }
  return {
    definition: {
      version: 1,
      ...(draft.applyByDefault ? { apply_by_default: true } : {}),
      ...(draft.description.trim() ? { description: draft.description.trim() } : {}),
      journeys,
      ...(draft.stepRiskEnabled ? { step_risk_enabled: true } : {}),
    },
  }
}
