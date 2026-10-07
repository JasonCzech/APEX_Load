import { ApiError } from '../hooks/apiClient'

// Shared revision-safety primitives for authored documents (scripts and
// scenarios). Each kind keeps its own adapter for baseline admission; these
// helpers own tag construction, the outcome-unknown error, the reconciliation
// predicate, the bounded three-way reapply and the field-path diff.

export type AuthoringKind = 'script' | 'scenario'

const revisionPatterns: Record<AuthoringKind, RegExp> = {
  script: /^scr-v1-[a-f0-9]{64}$/,
  scenario: /^scn-v1-[a-f0-9]{64}$/,
}

const tagPrefixes: Record<AuthoringKind, string> = {
  script: 'script-authoring-v1:',
  scenario: 'scenario-authoring-v1:',
}

export function isAuthoringRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

export function isAuthoringRevision(kind: AuthoringKind, revision: unknown): revision is string {
  return typeof revision === 'string' && revisionPatterns[kind].test(revision)
}

// authoringTagFor is the strong ETag the controller serves for a revision.
export function authoringTagFor(kind: AuthoringKind, revision: string, unsupported: string): string {
  if (!isAuthoringRevision(kind, revision)) throw new Error(unsupported)
  return `"${tagPrefixes[kind]}${revision}"`
}

// AuthoringSaveOutcomeUnknown means a save may or may not have committed: the
// editor keeps the draft and reloads the baseline; it never retries itself.
export class AuthoringSaveOutcomeUnknown extends Error {
  constructor(kind: AuthoringKind, name: string) {
    super(`The save outcome could not be verified. Your draft is retained. Reload the current ${kind} baseline before reviewing and reapplying it; do not retry this save automatically.`)
    this.name = name
  }
}

// A conditioned save needs reconciliation when the baseline is stale (412),
// missing (428), the outcome is unknown (5xx or a named outcome code) or the
// failure is not a controller answer at all.
export function authoringSaveNeedsReconciliation(error: unknown, outcomeCodes: readonly string[]): boolean {
  return !(error instanceof ApiError) || error.status === 412 || error.status === 428 || error.status >= 500
    || (typeof error.code === 'string' && outcomeCodes.includes(error.code))
}

// reapplyAuthoringDraft carries the local changes (original → draft) onto the
// reviewed current document. A locally changed field or array replaces the
// current one; untouched fields take the current value.
export function reapplyAuthoringDraft<T>(original: T, draft: T, current: T): T {
  function equal(a: unknown, b: unknown): boolean { return JSON.stringify(a) === JSON.stringify(b) }
  let visited = 0
  function merge(old: unknown, local: unknown, latest: unknown, depth = 0): unknown {
    if (++visited > 50_000 || depth > 64) throw new Error('Draft reapply exceeds the bounded review limit. Simplify the draft before reapplying.')
    if (equal(old, local)) return latest
    if (isAuthoringRecord(old) && isAuthoringRecord(local) && isAuthoringRecord(latest)) {
      const result: Record<string, unknown> = { ...latest }
      const hasOwn = (value: Record<string, unknown>, key: string) => Object.prototype.hasOwnProperty.call(value, key)
      const ownValue = (value: Record<string, unknown>, key: string) => hasOwn(value, key) ? value[key] : undefined
      for (const key of new Set([...Object.keys(old), ...Object.keys(local)])) {
        if (equal(ownValue(old, key), ownValue(local, key))) continue
        if (!hasOwn(local, key)) delete result[key]
        // JSON keys are data, including __proto__ and inherited property names.
        else Object.defineProperty(result, key, {
          value: merge(ownValue(old, key), ownValue(local, key), ownValue(latest, key), depth + 1),
          enumerable: true, configurable: true, writable: true,
        })
      }
      return result
    }
    return local
  }
  return merge(original, draft, current) as T
}

// Diff only bounded field paths, never credential or draft values.
export function authoringChangedPaths(before: unknown, after: unknown, rootLabel = 'Script'): string[] {
  const paths: string[] = []
  let visited = 0
  function visit(a: unknown, b: unknown, path: string, depth: number) {
    if (Object.is(a, b)) return
    if (++visited > 10_000 || paths.length >= 50 || depth > 20) {
      if (!paths.includes('Additional changes (bounded preview)')) paths.push('Additional changes (bounded preview)')
      return
    }
    if (Array.isArray(a) && Array.isArray(b)) {
      for (let i = 0; i < Math.max(a.length, b.length); i++) visit(a[i], b[i], `${path}[${i}]`, depth + 1)
    } else if (isAuthoringRecord(a) && isAuthoringRecord(b)) {
      for (const key of [...new Set([...Object.keys(a), ...Object.keys(b)])].sort()) visit(a[key], b[key], path ? `${path}.${key.slice(0, 240)}` : key.slice(0, 240), depth + 1)
    } else {
      paths.push((path || rootLabel).slice(0, 240))
    }
  }
  visit(before, after, '', 0)
  return paths
}
