// Client for the DSL action-type catalog served by GET /api/v1/dsl/actions.
//
// The catalog is the controller's declarative source of truth for which
// action types exist per protocol and which fields each type understands
// (generated from pkg/dsl). Serving it from the API keeps the editor in
// lockstep with the running validator. Helpers fall back to conservative
// built-in lists when the catalog has not loaded (offline, tests), matching
// the editor's pre-catalog behavior.

import { useEffect, useState } from 'react'

import { request } from '../hooks/apiClient'

export interface DslCatalogField {
  name: string
  required?: boolean
  kind: string
  enum?: string[]
  doc?: string
  maximum?: number
  /** Presentation for catalog-driven forms; all optional. */
  label?: string
  widget?: string
  advanced?: boolean
  placeholder?: string
  enum_labels?: Record<string, string>
  output_kind?: string
}

/** One operation of an action whose fields depend on a discriminator field. */
export interface DslCatalogVariant {
  value: string
  label: string
  group: string
  summary: string
  mutates?: boolean
  required?: string[]
  /** Alternative field sets: exactly one is given, whole. */
  choices?: string[][]
  allowed: string[]
  counts?: Record<string, number>
  defaults?: Record<string, unknown>
  table_kind?: string
}

export interface DslCatalogAction {
  type: string
  aliases?: string[]
  protocols: string[]
  summary: string
  fields?: DslCatalogField[]
  require_one_of?: string[][]
  discriminator?: string
  variants?: DslCatalogVariant[]
}

export interface DslActionCatalog {
  protocols: string[]
  common_fields: DslCatalogField[]
  actions: DslCatalogAction[]
}

let cachedCatalog: DslActionCatalog | null = null
let pendingLoad: Promise<DslActionCatalog | null> | null = null

export function getCachedDslActionCatalog(): DslActionCatalog | null {
  return cachedCatalog
}

/** Test hook / manual injection. Pass null to reset. */
export function setCachedDslActionCatalog(catalog: DslActionCatalog | null) {
  cachedCatalog = catalog
  pendingLoad = null
}

/**
 * Load the catalog once per session. Failures resolve to null (the editor
 * keeps its built-in fallbacks) and allow a later retry.
 */
export function ensureDslActionCatalog(): Promise<DslActionCatalog | null> {
  if (cachedCatalog) return Promise.resolve(cachedCatalog)
  if (pendingLoad) return pendingLoad
  pendingLoad = request<DslActionCatalog>('/dsl/actions')
    .then(catalog => {
      cachedCatalog = catalog
      return catalog
    })
    .catch(() => {
      pendingLoad = null
      return null
    })
  return pendingLoad
}

export type DslCatalogStatus = 'loading' | 'ready' | 'failed'

/**
 * The catalog for a component that cannot work without it: loading until it
 * arrives, failed (with a retry) when it cannot be fetched.
 */
export function useDslActionCatalog(): { catalog: DslActionCatalog | null; status: DslCatalogStatus; retry: () => void } {
  const [state, setState] = useState<{ catalog: DslActionCatalog | null; status: DslCatalogStatus }>(() => cachedCatalog ? { catalog: cachedCatalog, status: 'ready' } : { catalog: null, status: 'loading' })
  const [attempt, setAttempt] = useState(0)
  const ready = state.status === 'ready'
  useEffect(() => {
    if (ready) return undefined
    let cancelled = false
    void ensureDslActionCatalog().then(catalog => {
      if (!cancelled) setState(catalog ? { catalog, status: 'ready' } : { catalog: null, status: 'failed' })
    })
    return () => { cancelled = true }
  }, [attempt, ready])
  return { ...state, retry: () => { setState({ catalog: null, status: 'loading' }); setAttempt(value => value + 1) } }
}

/** Map a script protocol to its catalog family (jms-solace → jms). */
export function dslProtocolFamily(protocol: string): string {
  const trimmed = protocol.trim()
  if (trimmed === 'http/1.1' || trimmed === 'http/2' || trimmed === 'http/3') return 'http'
  return trimmed.startsWith('jms-') ? 'jms' : trimmed
}

/** Resolve an action type (alias or legacy loadrunner_* spelling) to its spec. */
export function dslSpecForActionType(
  actionType: string,
  catalog: DslActionCatalog | null = cachedCatalog,
): DslCatalogAction | null {
  if (!catalog) return null
  const canonical = actionType.startsWith('loadrunner_')
    ? `source_${actionType.slice('loadrunner_'.length)}`
    : actionType
  for (const action of catalog.actions) {
    if (action.type === canonical) return action
    if (action.aliases?.includes(canonical)) return action
  }
  return null
}

/** Action specs usable for a script protocol, engine actions included where valid. */
export function dslActionTypesForProtocol(
  protocol: string,
  catalog: DslActionCatalog | null = cachedCatalog,
): DslCatalogAction[] {
  if (!catalog) return []
  const family = dslProtocolFamily(protocol)
  return catalog.actions.filter(action => {
    if (action.protocols.includes('*')) return true
    return action.protocols.includes(family)
  })
}

function specHasField(spec: DslCatalogAction, name: string): boolean {
  return Boolean(spec.fields?.some(field => field.name === name))
}

// Pre-catalog hardcoded lists, kept as fallback so the editor behaves
// identically before the catalog request resolves (and in offline tests).
const FALLBACK_SELECTOR_TYPES = new Set([
  'browser_click', 'browser_fill', 'browser_select', 'browser_set_files', 'browser_press_key',
  'browser_wait', 'browser_screenshot', 'browser_tap', 'browser_long_press', 'browser_swipe',
  'browser_dblclick', 'browser_hover', 'browser_drag_and_drop', 'browser_switch_frame',
])
const FALLBACK_VALUE_TYPES = new Set(['browser_fill', 'browser_select', 'browser_set_files', 'browser_press_key'])

/** Whether the action-form UI should offer a selector input for this type. */
export function dslActionUsesSelector(
  actionType: string,
  catalog: DslActionCatalog | null = cachedCatalog,
): boolean {
  const spec = dslSpecForActionType(actionType, catalog)
  if (!spec) return FALLBACK_SELECTOR_TYPES.has(actionType)
  return specHasField(spec, 'selector')
}

/** Whether the action-form UI should offer a value input for this type. */
export function dslActionUsesValue(
  actionType: string,
  catalog: DslActionCatalog | null = cachedCatalog,
): boolean {
  const spec = dslSpecForActionType(actionType, catalog)
  if (!spec) return FALLBACK_VALUE_TYPES.has(actionType)
  return specHasField(spec, 'value')
}

/**
 * Whether authoring assist should flag a missing target for this type:
 * true when the spec requires target outright or lists it in a one-of
 * group (target/selector style). Unknown types fall back to the legacy
 * heuristic so behavior is unchanged before the catalog loads.
 */
export function dslActionNeedsExplicitTarget(
  actionType: string,
  fallback: (actionType: string) => boolean,
  catalog: DslActionCatalog | null = cachedCatalog,
): boolean {
  const spec = dslSpecForActionType(actionType, catalog)
  if (!spec) return fallback(actionType)
  const target = spec.fields?.find(field => field.name === 'target')
  if (target?.required) return true
  return Boolean(spec.require_one_of?.some(group => group.includes('target') || group.includes('selector')))
}
