import type { ScriptDefinition, SourceReview, SourceReviewMapping } from '../types/scripts'
import { fingerprintPortableContent as fingerprintContent } from '../utils/contentFingerprint'
import { readSourceReview } from './scriptEditorImportModel'
import { scriptHasSourceOnlyReview, sourceReviewIsSourceOnly } from './sourceReviewDomain'

export interface MappingBinding {
  path: string
  linkedBy?: string
  linkedAt?: string
  indices: { pathIndex: number; digest: string }[]
  targetDigest: string
}
export interface MappingDecision {
  outcome: 'acknowledged' | 'needs_changes'
  reviewer: string
  reviewedAt: string
  importRevision: string
  targetDigest: string
  note: string
}
export interface MappingReviewState {
  version: 1
  sourceOnly?: boolean
  importRevision: string
  capturedAt: string
  snapshot?: ScriptDefinition
  bindings: Record<string, MappingBinding>
  decisions: Record<string, MappingDecision>
}
export interface ResolvedMapping {
  path?: string
  status: 'matched' | 'moved' | 'changed' | 'missing' | 'ambiguous' | 'unmapped'
  targetDigest?: string
}
const KEY = 'source_mapping_review'
const tokensFor = (path: string): (string | number)[] => (path.replace(/^script\./, '').replace(/\.(\d+)(?=\.|$)/g, '[$1]').match(/[^.[\]]+|\[\d+\]/g) || []).map(token => /^\[\d+\]$/.test(token) ? Number(token.slice(1, -1)) : token)
const at = (value: unknown, key: string | number): unknown => value && typeof value === 'object' && Object.prototype.hasOwnProperty.call(value, key) ? (value as Record<string | number, unknown>)[key] : undefined
const pathFor = (tokens: (string | number)[]) => tokens.map((token, index) => typeof token === 'number' ? `[${token}]` : `${index ? '.' : ''}${token}`).join('')

export function readMappingReviewState(script?: ScriptDefinition | null): MappingReviewState | null {
  if (scriptHasSourceOnlyReview(script)) return null
  const value = script?.metadata?.import?.preserved?.[KEY] as MappingReviewState | undefined
  if (value?.sourceOnly || value?.version !== 1 || typeof value.importRevision !== 'string' || typeof value.capturedAt !== 'string' || !value.bindings || !value.decisions) return null
  if (Object.values(value.bindings).some(binding => !binding || typeof binding.path !== 'string' || typeof binding.targetDigest !== 'string' || !Array.isArray(binding.indices) || binding.indices.some(item => !item || !Number.isInteger(item.pathIndex) || item.pathIndex < 0 || typeof item.digest !== 'string'))) return null
  if (Array.isArray(value.bindings) || Array.isArray(value.decisions) || typeof value.bindings !== 'object' || typeof value.decisions !== 'object') return null
  if (Object.values(value.decisions).some(decision => !decision || !['acknowledged', 'needs_changes'].includes(decision.outcome) || ['reviewer', 'reviewedAt', 'importRevision', 'targetDigest', 'note'].some(key => typeof (decision as unknown as Record<string, unknown>)[key] !== 'string'))) return null
  if (value.snapshot && (typeof value.snapshot !== 'object' || Array.isArray(value.snapshot))) return null
  return { ...value, bindings: Object.assign(Object.create(null), value.bindings), decisions: Object.assign(Object.create(null), value.decisions) }
}

function withoutMappingReviewState(script: ScriptDefinition): ScriptDefinition {
  const preserved = script.metadata?.import?.preserved
  if (!preserved || !Object.prototype.hasOwnProperty.call(preserved, KEY)) return script
  const cleaned = { ...preserved }
  delete cleaned[KEY]
  return { ...script, metadata: { ...script.metadata, import: { ...script.metadata?.import, preserved: cleaned } } }
}

export function withMappingReviewState(script: ScriptDefinition, state: MappingReviewState): ScriptDefinition {
  if (scriptHasSourceOnlyReview(script) || state.sourceOnly) return withoutMappingReviewState(script)
  return { ...script, metadata: { ...script.metadata, import: { ...script.metadata?.import, preserved: { ...script.metadata?.import?.preserved, [KEY]: state } } } }
}

export async function createMappingReviewState(script: ScriptDefinition, review: SourceReview): Promise<MappingReviewState> {
  const importRevision = await fingerprintContent(review)
  if (sourceReviewIsSourceOnly(review) || scriptHasSourceOnlyReview(script)) {
    return { version: 1, sourceOnly: true, importRevision, capturedAt: new Date().toISOString(), bindings: Object.create(null), decisions: Object.create(null) }
  }
  const existing = readMappingReviewState(script)
  if (existing?.importRevision === importRevision) return existing
  if (script.metadata?.import?.preserved?.[KEY] && !existing) throw new Error('Saved mapping review data is invalid. Restore a valid script revision before recording review decisions.')
  const cache = new Map<unknown, Promise<string>>()
  const digest = (value: unknown) => { if (!cache.has(value)) cache.set(value, fingerprintContent(value)); return cache.get(value)! }
  const bindings: Record<string, MappingBinding> = Object.create(null)
  for (const mapping of review.mappings || []) {
    if (!mapping.target_path || mapping.target_path.includes(KEY)) continue
    let value: unknown = script
    const indices: MappingBinding['indices'] = []
    const tokens = tokensFor(mapping.target_path)
    for (let index = 0; index < tokens.length; index++) {
      value = at(value, tokens[index])
      if (typeof tokens[index] === 'number' && value !== undefined) indices.push({ pathIndex: index, digest: await digest(value) })
    }
    if (value !== undefined) bindings[mapping.id] = { path: mapping.target_path, indices, targetDigest: await digest(value) }
  }
  const { name, version, protocol, config, actions, init, teardown, parameters, data_sources } = script
  const snapshot = JSON.parse(JSON.stringify({ name, version, protocol, config, actions, init, teardown, parameters, data_sources })) as ScriptDefinition
  return { version: 1, importRevision, capturedAt: new Date().toISOString(), snapshot, bindings, decisions: Object.create(null) }
}

export async function preserveMappingBindings(script: ScriptDefinition, baseline: ScriptDefinition | null): Promise<ScriptDefinition> {
  if (scriptHasSourceOnlyReview(script)) return withoutMappingReviewState(script)
  const review = readSourceReview(script.metadata?.import)
  if (!review?.mappings?.length) return script
  const revision = await fingerprintContent(review)
  const stored = readMappingReviewState(script)
  if (stored?.importRevision === revision) return script
  const baselineReview = readSourceReview(baseline?.metadata?.import)
  // Never establish an old import's index bindings from an already edited draft.
  if (!baseline || !baselineReview || await fingerprintContent(baselineReview) !== revision) return script
  return withMappingReviewState(script, await createMappingReviewState(baseline, review))
}

export async function resolveMappingBindings(script: ScriptDefinition, mappings: SourceReviewMapping[], state: MappingReviewState): Promise<Record<string, ResolvedMapping>> {
  const result: Record<string, ResolvedMapping> = Object.create(null)
  if (scriptHasSourceOnlyReview(script) || state.sourceOnly) {
    for (const mapping of mappings) result[mapping.id] = { status: 'unmapped' }
    return result
  }
  const cache = new Map<unknown, Promise<string>>()
  const digest = (value: unknown) => { if (!cache.has(value)) cache.set(value, fingerprintContent(value)); return cache.get(value)! }
  for (const mapping of mappings) {
    const binding = Object.prototype.hasOwnProperty.call(state.bindings, mapping.id) ? state.bindings[mapping.id] : undefined
    if (!mapping.target_path && !binding) { result[mapping.id] = { status: 'unmapped', targetDigest: await digest(mapping) }; continue }
    if (!binding) { result[mapping.id] = { status: 'missing' }; continue }
    const tokens = tokensFor(binding.path)
    let value: unknown = script
    let failure: ResolvedMapping['status'] | undefined
    for (let index = 0; index < tokens.length; index++) {
      const anchor = binding.indices.find(item => item.pathIndex === index)
      if (anchor) {
        if (!Array.isArray(value)) { failure = 'missing'; break }
        const matches = (await Promise.all(value.map(async (item, itemIndex) => await digest(item) === anchor.digest ? itemIndex : -1))).filter(itemIndex => itemIndex >= 0)
        if (matches.length !== 1) { failure = matches.length > 1 ? 'ambiguous' : 'changed'; break }
        tokens[index] = matches[0]
      }
      value = at(value, tokens[index])
      if (value === undefined) { failure = 'missing'; break }
    }
    if (failure) { result[mapping.id] = { status: failure }; continue }
    const targetDigest = await digest(value)
    const path = pathFor(tokens)
    result[mapping.id] = { path, targetDigest, status: targetDigest !== binding.targetDigest ? 'changed' : path !== binding.path ? 'moved' : 'matched' }
  }
  return result
}

export function decisionMatches(decision: MappingDecision | undefined, state: MappingReviewState, resolution: ResolvedMapping | undefined): boolean {
  return Boolean(!state.sourceOnly && decision && resolution?.targetDigest && decision.importRevision === state.importRevision && decision.targetDigest === resolution.targetDigest)
}

export async function rebindMapping(script: ScriptDefinition, mapping: SourceReviewMapping, state: MappingReviewState, path: string): Promise<MappingReviewState> {
  if (scriptHasSourceOnlyReview(script) || state.sourceOnly) throw new Error('Original source paths cannot be linked to the current draft.')
  if (!/^(?:[a-zA-Z_][\w]*)(?:(?:\.[a-zA-Z_][\w]*)|(?:\[\d+\]))*$/.test(path) || path.includes(KEY)) throw new Error('Enter an existing DSL path, such as actions[0].target.')
  const captured = await createMappingReviewState(script, { mappings: [{ ...mapping, target_path: path }] })
  const binding = captured.bindings[mapping.id]
  if (!binding) throw new Error('Choose an existing destination path in the current draft.')
  const decisions = { ...state.decisions }
  delete decisions[mapping.id]
  return { ...state, bindings: { ...state.bindings, [mapping.id]: binding }, decisions }
}
