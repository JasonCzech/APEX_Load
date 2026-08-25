import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'

/**
 * scriptRequiresJavaVuserAuthoringApproval mirrors the backend predicate of the
 * same name in pkg/api/external_runner_gating.go. POST /scripts answers 409 for
 * a matching script, so the editor must route those saves through an authoring
 * session instead.
 *
 * The condition is narrower than "protocol is java_vuser": only a script that
 * actually executes java_vuser_run actions needs governed approval. A
 * review-only Java draft still saves directly, and widening this here would put
 * those drafts into a review queue the backend never asked for.
 *
 * Keep this in sync with the Go predicate — including the recursion into
 * control-flow bodies, where a single java_vuser_run step is enough.
 */
export function scriptRequiresJavaVuserAuthoringApproval(script: ScriptDefinition | null | undefined): boolean {
  if (!script || script.protocol?.trim().toLowerCase() !== 'java_vuser') return false
  return javaVuserActionsRequireRunner(script.init)
    || javaVuserActionsRequireRunner(script.actions)
    || javaVuserActionsRequireRunner(script.teardown)
}

function javaVuserActionsRequireRunner(actions: ScriptActionDefinition[] | undefined): boolean {
  for (const action of actions ?? []) {
    if (action?.type?.trim().toLowerCase() === 'java_vuser_run') return true
	if (action?.control) {
		if (javaVuserActionsRequireRunner(action.control.steps) || javaVuserActionsRequireRunner(action.control.else)) return true
		if ((action.control.cases || []).some(switchCase => javaVuserActionsRequireRunner(switchCase.steps))) return true
		if (javaVuserActionsRequireRunner(action.control.default)) return true
	}
  }
  return false
}

/**
 * authoringSessionIdempotencyKey derives a key from the draft's own content.
 *
 * The backend fingerprints the request behind the key: retrying with the same
 * key and the same draft replays the original session (which is what protects a
 * lost network reply), while the same key with a different draft is rejected
 * with a 409. Deriving the key from the content therefore gives both halves —
 * re-saving unchanged content converges on one review session, and saving after
 * an edit opens a new one instead of colliding.
 */
export function authoringSessionIdempotencyKey(prefix: string, draft: ScriptDefinition): string {
  // FNV-1a over the serialized draft. This is a dedupe key, not a security
  // boundary — the backend re-verifies the full request fingerprint itself.
  const serialized = JSON.stringify(draft)
  let hash = 0x811c9dc5
  for (let index = 0; index < serialized.length; index++) {
    hash ^= serialized.charCodeAt(index)
    hash = Math.imul(hash, 0x01000193) >>> 0
  }
  return `${prefix}:${serialized.length.toString(36)}:${hash.toString(36)}`
}
