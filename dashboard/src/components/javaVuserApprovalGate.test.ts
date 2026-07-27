import { describe, expect, it } from 'vitest'

import { authoringSessionIdempotencyKey, scriptRequiresJavaVuserAuthoringApproval } from './javaVuserApprovalGate'
import type { ScriptDefinition } from '../types/scripts'

const baseScript: ScriptDefinition = {
  name: 'Draft',
  version: '1',
  protocol: 'java_vuser',
  config: { base_url: 'https://example.com' },
  actions: [],
}

describe('scriptRequiresJavaVuserAuthoringApproval', () => {
  it('is false for a java_vuser script with no runner actions', () => {
    // Review-only Java drafts still save through POST /scripts; widening the
    // condition to the protocol alone would queue them for review needlessly.
    expect(scriptRequiresJavaVuserAuthoringApproval({
      ...baseScript,
      actions: [{ name: 'Note', type: 'think_time', target: '' }],
    })).toBe(false)
  })

  it('is false for a non-Java script that happens to hold a java_vuser_run action', () => {
    expect(scriptRequiresJavaVuserAuthoringApproval({
      ...baseScript,
      protocol: 'http',
      actions: [{ name: 'Java action', type: 'java_vuser_run', target: '' }],
    })).toBe(false)
  })

  it('matches a runner action in any lifecycle phase', () => {
    for (const phase of ['init', 'actions', 'teardown'] as const) {
      expect(scriptRequiresJavaVuserAuthoringApproval({
        ...baseScript,
        [phase]: [{ name: 'Java action', type: 'java_vuser_run', target: '' }],
      })).toBe(true)
    }
  })

  it('recurses into control-flow bodies like the backend predicate', () => {
    expect(scriptRequiresJavaVuserAuthoringApproval({
      ...baseScript,
      actions: [{
        name: 'Loop',
        type: 'control_loop',
        target: '',
        control: { steps: [{ name: 'Java action', type: 'java_vuser_run', target: '' }] },
      }],
    })).toBe(true)

    expect(scriptRequiresJavaVuserAuthoringApproval({
      ...baseScript,
      actions: [{
        name: 'Branch',
        type: 'control_if',
        target: '',
        control: { steps: [], else: [{ name: 'Java action', type: 'JAVA_VUSER_RUN', target: '' }] },
      }],
    })).toBe(true)
  })

  it('tolerates missing and malformed input', () => {
    expect(scriptRequiresJavaVuserAuthoringApproval(null)).toBe(false)
    expect(scriptRequiresJavaVuserAuthoringApproval(undefined)).toBe(false)
    expect(scriptRequiresJavaVuserAuthoringApproval({ ...baseScript, protocol: '  Java_VUser  ', actions: [{ name: 'a', type: ' java_vuser_run ', target: '' }] })).toBe(true)
  })
})

describe('authoringSessionIdempotencyKey', () => {
  it('is stable for identical drafts so a lost reply replays one session', () => {
    const draft = { ...baseScript, actions: [{ name: 'Java action', type: 'java_vuser_run', target: '' }] }
    expect(authoringSessionIdempotencyKey('p', draft)).toBe(authoringSessionIdempotencyKey('p', { ...draft }))
  })

  it('changes with the draft so an edited script opens a new session', () => {
    // The backend 409s when one key is reused for a different request, so the
    // key has to move when the content does.
    const first = authoringSessionIdempotencyKey('p', baseScript)
    const second = authoringSessionIdempotencyKey('p', { ...baseScript, name: 'Draft 2' })
    expect(first).not.toBe(second)
  })

  it('carries the caller prefix', () => {
    expect(authoringSessionIdempotencyKey('script-editor-java-vuser', baseScript)).toMatch(/^script-editor-java-vuser:/)
  })
})
