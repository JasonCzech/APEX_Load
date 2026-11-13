import type { ScenarioRuntimeSettingsDraft } from '../types'
import type { PartialRuntimeSettingsOverride } from '../types/runtimeSettings'
import type { ScriptConfig, ScriptDefinition } from '../types/scripts'
import { captureScript, type DebugCapture } from './debuggerExperience'
import type { RuntimeValueProvenance } from './runtime-settings/canonicalRuntimeTaxonomy'
import {
  effectiveRuntimeValue,
  normalizeRuntimeOverrideFieldPath,
  overrideFieldValue,
  partialRuntimeOverrideFromDraft,
  resetOverrideField,
  runtimeOverrideFieldPaths,
  runtimeOverridePathWireSegments,
  sameRuntimeValue,
  setOverrideField,
} from './runtimeSettingsModel'

// Debugger session runtime overrides: a partial RuntimeSettingsOverride
// (override_fields) the debugger sends as runtime_overrides with
// POST /api/v1/scripts/{id}/debug/sessions, /scripts/debug/sessions,
// /scripts/{id}/replay and /scripts/replay. The server applies it to a clone of
// the script after the trusted-import and Java VUser approval checks
// (pkg/api/handlers_replay.go applyReplayOrDebugRuntimeOverrides), so it never
// changes script.config and never dirties the script.
//
// Precedence, highest first (pkg/api/handlers_script_debug.go
// applyScriptDebugOverrides):
//   1. Debug options: log level, Ignore think time, After a failure and the
//      Run N iterations mode. They are explicit request fields, which the
//      server applies after runtime_overrides, so they always win.
//   2. Session runtime overrides (this module).
//   3. The script's runtime settings (the unsaved draft when it has edits).
//   4. Engine defaults (GET /api/v1/dsl/config-defaults).
// The paths Debug options own are never offered or sent as session overrides,
// so one setting has one control.

/** A session override draft: the shared partial draft (always with override_fields). */
export type DebugSessionOverrideDraft = ScenarioRuntimeSettingsDraft

/** Paths a Debug option owns, with the option that sets them. */
export const DEBUG_OPTION_OWNED_PATHS: Readonly<Record<string, string>> = {
  'log.level': 'Log level',
  'think_time.ignore': 'Ignore think time',
  'error_handling.continue_on_error': 'After a failure',
  'run_logic.iterations': 'Run mode (iterations)',
}

/** Sections a single-VUser debug replay does not schedule. */
const DEBUG_SKIPPED_SECTIONS = ['pacing'] as const

export const DEBUG_SESSION_PRECEDENCE = 'Debug options win over session runtime overrides, which win over the script’s runtime settings, which win over engine defaults.'

export const DEBUG_PACING_SKIPPED_LABEL = 'Skipped in single-VUser debug replay'

function canonical(path: string): string {
  return normalizeRuntimeOverrideFieldPath(path) ?? path
}

/** Whether a session override may carry `path` (Debug options and skipped sections never). */
export function isDebugSessionOverridablePath(path: string): boolean {
  const normalized = canonical(path)
  if (Object.prototype.hasOwnProperty.call(DEBUG_OPTION_OWNED_PATHS, normalized)) return false
  // A listed ancestor of an owned path (a whole `log` section) would set it too.
  if (Object.keys(DEBUG_OPTION_OWNED_PATHS).some(owned => owned.startsWith(`${normalized}.`))) return false
  return !DEBUG_SKIPPED_SECTIONS.some(section => normalized === section || normalized.startsWith(`${section}.`))
}

/** The paths the session overrides, in edit order. */
export function sessionOverridePaths(draft?: DebugSessionOverrideDraft): string[] {
  return runtimeOverrideFieldPaths(draft).filter(isDebugSessionOverridablePath)
}

export function sessionOverrideCount(draft?: DebugSessionOverrideDraft): number {
  return sessionOverridePaths(draft).length
}

function normalizeDraft(draft: DebugSessionOverrideDraft): DebugSessionOverrideDraft | undefined {
  return runtimeOverrideFieldPaths(draft).length > 0 ? draft : undefined
}

/** Overrides one path for this session; a path a Debug option owns is refused. */
export function setSessionOverride(draft: DebugSessionOverrideDraft | undefined, path: string, value: unknown): DebugSessionOverrideDraft | undefined {
  if (!isDebugSessionOverridablePath(path)) return draft
  return normalizeDraft(setOverrideField(draft, path, value))
}

/** Returns one path to the script's value; an empty draft becomes undefined. */
export function resetSessionOverride(draft: DebugSessionOverrideDraft | undefined, path: string): DebugSessionOverrideDraft | undefined {
  if (!draft) return undefined
  return normalizeDraft(resetOverrideField(draft, path))
}

/** The value the session sets at `path`, or undefined when it inherits. */
export function sessionOverrideValue(draft: DebugSessionOverrideDraft | undefined, path: string): unknown {
  return isDebugSessionOverridablePath(path) ? overrideFieldValue(draft, path) : undefined
}

export function isSessionOverridden(draft: DebugSessionOverrideDraft | undefined, path: string): boolean {
  const normalized = canonical(path)
  return sessionOverridePaths(draft).some(field => field === normalized || normalized.startsWith(`${field}.`))
}

/** Each overridden path keyed to 'session_override', for the canonical group rail. */
export function sessionOverrideProvenance(draft?: DebugSessionOverrideDraft): Record<string, RuntimeValueProvenance> {
  return Object.fromEntries(sessionOverridePaths(draft).map(path => [path, 'session_override' as const]))
}

export interface DebugValueLayers {
  session?: DebugSessionOverrideDraft
  script?: ScriptConfig
  engine?: ScriptConfig
}

/** One path resolved session override → script → engine default, with where it came from. */
export function effectiveDebugValue(path: string, layers: DebugValueLayers): { value: unknown; provenance: RuntimeValueProvenance } {
  const session = layers.session && sessionOverridePaths(layers.session).length > 0 ? layers.session : undefined
  const resolved = effectiveRuntimeValue(path, { group: isDebugSessionOverridablePath(path) ? session : undefined, script: layers.script, engine: layers.engine })
  return { value: resolved.value, provenance: resolved.provenance === 'group_override' ? 'session_override' : resolved.provenance }
}

/** What the script (then the engine) supplies at `path`, ignoring the session. */
export function inheritedDebugValue(path: string, layers: DebugValueLayers): { value: unknown; provenance: RuntimeValueProvenance } {
  return effectiveDebugValue(path, { script: layers.script, engine: layers.engine })
}

/**
 * The wire row (override_fields listed) the session sends as runtime_overrides,
 * or undefined when it overrides nothing. Owned and skipped paths are dropped.
 */
export function sessionRuntimeOverrides(draft?: DebugSessionOverrideDraft): PartialRuntimeSettingsOverride | undefined {
  const paths = sessionOverridePaths(draft)
  if (paths.length === 0) return undefined
  const clean = paths.reduce<DebugSessionOverrideDraft | undefined>((next, path) => setOverrideField(next, path, overrideFieldValue(draft, path)), undefined)
  return partialRuntimeOverrideFromDraft(clean)
}

/**
 * The debugger's evidence capture, as the override leaves captureScript would
 * write into a draft body: successful message detail and the advanced network
 * trace. A stored script runs without a body, so these travel as overrides.
 */
export function captureOverrideEntries(script: ScriptDefinition, capture: DebugCapture | undefined, networkTrace: boolean): Array<[string, unknown]> {
  const captured = captureScript(script, capture || 'diagnostic', networkTrace)
  if (captured === script) return []
  const entries: Array<[string, unknown]> = []
  if (!sameRuntimeValue(captured.config.log?.extended_options, script.config.log?.extended_options)) {
    entries.push(['log.extended_options', captured.config.log?.extended_options ?? []])
  }
  if (!sameRuntimeValue(captured.config.results?.message_detail, script.config.results?.message_detail)) {
    entries.push(['results.message_detail', captured.config.results?.message_detail])
  }
  return entries
}

export interface StoredSessionRequestContext {
  /** The editor's current script (the stored projection when it has no edits). */
  script: ScriptDefinition
  /** The editor has unsaved edits, so the request sends the draft body. */
  unsavedEdits: boolean
  /**
   * The stored content revision the editor shows. A body-less start names it so
   * the server refuses (409) rather than run a newer stored revision than the
   * one the editor's outline, breakpoints and evidence describe. Without it the
   * request keeps the body.
   */
  baselineRevision?: string | null
  capture?: DebugCapture
  networkTrace: boolean
  overrides?: DebugSessionOverrideDraft
}

type SessionRequestBody<T> = Omit<T, 'script' | 'runtime_overrides'> & {
  script?: ScriptDefinition
  runtime_overrides?: PartialRuntimeSettingsOverride
  expected_content_revision?: string
}

/**
 * Whether a stored script's debug or replay start sends the script body: it has
 * unsaved edits, or no baseline revision to pin a body-less start to. The
 * session overrides panel names the run's source from this, so it never says
 * the body is not sent when storedSessionRequestBody sends it.
 */
export function storedSessionSendsScriptBody(context: { unsavedEdits: boolean; baselineRevision?: string | null }): boolean {
  return context.unsavedEdits || !context.baselineRevision?.trim()
}

/** Native browser replay has no runtime_overrides on its start request, so session overrides never reach it. */
export const NATIVE_REPLAY_SESSION_OVERRIDES_NOTE = 'Session runtime overrides apply to debug runs only. Native browser replay uses the script’s runtime settings.'

/** The native browser replay control's note while the debugger holds session overrides it will not apply. */
export function nativeReplaySessionOverridesNote(draft?: DebugSessionOverrideDraft): string | undefined {
  const count = sessionOverrideCount(draft)
  if (count === 0) return undefined
  return `${count} session runtime ${count === 1 ? 'override is' : 'overrides are'} not applied here. ${NATIVE_REPLAY_SESSION_OVERRIDES_NOTE}`
}

/**
 * The body for a stored script's debug or replay start. With no unsaved edits
 * and a known baseline revision it sends no script: the server runs its own
 * stored script at that revision (keeping a credential-preserving import's
 * trust and the Java VUser approval), with the debugger capture and the
 * session overrides as runtime_overrides, and refuses a newer revision. With
 * unsaved edits, or no baseline revision to pin, it keeps the body (already
 * captured) and adds the session overrides, which the server applies on top.
 */
export function storedSessionRequestBody<T extends { script?: ScriptDefinition }>(payload: T, context: StoredSessionRequestContext): SessionRequestBody<T> {
  const { script: body, ...copy } = { ...payload } as T & { runtime_overrides?: unknown }
  delete copy.runtime_overrides
  const rest = copy as Omit<T, 'script' | 'runtime_overrides'>
  const baselineRevision = context.baselineRevision?.trim()
  if (storedSessionSendsScriptBody(context) || !baselineRevision) {
    const runtimeOverrides = sessionRuntimeOverrides(context.overrides)
    return { ...rest, ...(body ? { script: body } : {}), ...(runtimeOverrides ? { runtime_overrides: runtimeOverrides } : {}) }
  }
  let merged: DebugSessionOverrideDraft | undefined
  for (const [path, value] of captureOverrideEntries(context.script, context.capture, context.networkTrace)) {
    merged = setOverrideField(merged, path, value)
  }
  for (const path of sessionOverridePaths(context.overrides)) {
    merged = setOverrideField(merged, path, overrideFieldValue(context.overrides, path))
  }
  const runtimeOverrides = merged && runtimeOverrideFieldPaths(merged).length > 0 ? partialRuntimeOverrideFromDraft(merged) : undefined
  return { ...rest, expected_content_revision: baselineRevision, ...(runtimeOverrides ? { runtime_overrides: runtimeOverrides } : {}) }
}

/** The body for a draft (unsaved, no script id) debug or replay start: the draft plus the session overrides. */
export function draftSessionRequestBody<T extends { script: ScriptDefinition }>(payload: T, overrides?: DebugSessionOverrideDraft): T & { runtime_overrides?: PartialRuntimeSettingsOverride } {
  const runtimeOverrides = sessionRuntimeOverrides(overrides)
  const next = { ...payload } as T & { runtime_overrides?: PartialRuntimeSettingsOverride }
  delete next.runtime_overrides
  if (runtimeOverrides) next.runtime_overrides = runtimeOverrides
  return next
}

function writeConfigPath(config: Record<string, unknown>, segments: readonly string[], value: unknown) {
  let current = config
  for (let index = 0; index < segments.length - 1; index++) {
    const next = current[segments[index]]
    if (next === null || typeof next !== 'object' || Array.isArray(next)) current[segments[index]] = {}
    current = current[segments[index]] as Record<string, unknown>
  }
  const leaf = segments[segments.length - 1]
  if (value === undefined) delete current[leaf]
  else current[leaf] = structuredClone(value)
}

/**
 * Writes the session overrides into a script config, as the Author runtime
 * settings editor would: each overridden path takes the session's value (an
 * explicit empty override removes the script's value). Paths outside
 * ScriptConfig (env, Java Env, redaction rules) cannot be promoted and are
 * returned in `skipped`.
 */
export function promoteSessionOverrides(config: ScriptConfig, draft?: DebugSessionOverrideDraft): { config: ScriptConfig; promoted: string[]; skipped: string[] } {
  const next = structuredClone(config) as unknown as Record<string, unknown>
  const promoted: string[] = []
  const skipped: string[] = []
  for (const path of sessionOverridePaths(draft)) {
    const wire = runtimeOverridePathWireSegments(path)
    if (!wire || wire[0] === 'env' || wire[0] === 'java_vuser' || wire[0] === 'redaction_rules') {
      skipped.push(path)
      continue
    }
    const segments = wire[0] === 'browser_config' ? ['browser', ...wire.slice(1)] : wire
    writeConfigPath(next, segments, overrideFieldValue(draft, path))
    promoted.push(path)
  }
  return { config: next as unknown as ScriptConfig, promoted, skipped }
}
