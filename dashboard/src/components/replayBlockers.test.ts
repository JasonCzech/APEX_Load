import { describe, expect, it } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import { replayBlockedIssues, summarizeReplayBlockers } from './replayBlockers'
import { blockerTitle } from './replayReadinessIssues'

const script = {
  name: 'HAR',
  protocol: 'http',
  config: { base_url: 'https://shop.example' },
  init: [{ type: 'http', name: 'Token', target: '/token' }],
  actions: [
    { type: 'http', name: 'POST ctp', target: 'https://beacon.example.test/ctp' },
    { type: 'http', name: 'POST ctp', target: 'https://beacon.example.test/ctp' },
    { type: 'http', name: 'Async', target: 'https://cdn.example.test/storage.html' },
  ],
} as unknown as ScriptDefinition

describe('summarizeReplayBlockers', () => {
  it('groups gates by reason with the hosts they call, most common first', () => {
    const summary = summarizeReplayBlockers([
      'actions[0] "POST ctp": execution blocker import_review_required, credential_json_invalid_utf8 blocks replay until the imported behavior is resolved',
      'actions[1] "POST ctp": execution blocker import_review_required blocks replay until the imported behavior is resolved',
      // A nested request on the same step counts that step once.
      'actions[2] "Async": execution blocker import_review_required blocks replay until the imported behavior is resolved',
      'actions[2].params.request "Async": execution blocker import_review_required blocks replay until the imported behavior is resolved',
      'init[0] "Token": data source rows is not configured',
      'the script has no actions',
    ], script)
    expect(summary.steps).toBe(5)
    expect(summary.groups).toEqual([
      {
        title: blockerTitle('import_review_required'),
        count: 3,
        hosts: [{ host: 'beacon.example.test', count: 2 }, { host: 'cdn.example.test', count: 1 }],
      },
      { title: blockerTitle('credential_json_invalid_utf8'), count: 1, hosts: [{ host: 'beacon.example.test', count: 1 }] },
      // A relative target resolves against the base URL.
      { title: 'data source rows is not configured', count: 1, hosts: [{ host: 'shop.example', count: 1 }] },
      { title: 'the script has no actions', count: 1, hosts: [] },
    ])
  })
})

describe('replayBlockedIssues', () => {
  it('reads the gates only from a replay refusal that lists them', () => {
    const issues = ['actions[0] "POST ctp": execution blocker import_review_required blocks replay until the imported behavior is resolved']
    expect(replayBlockedIssues({ code: 'script_replay_blocked', payload: { issues } })).toEqual(issues)
    expect(replayBlockedIssues({ code: 'script_replay_blocked', payload: { error: 'older server' } })).toBeNull()
    expect(replayBlockedIssues({ code: 'other', payload: { issues } })).toBeNull()
    expect(replayBlockedIssues(new Error('network'))).toBeNull()
  })
})
