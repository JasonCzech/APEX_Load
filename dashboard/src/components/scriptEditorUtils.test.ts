import { describe, expect, it } from 'vitest'

import {
  buildReplayStartPayload,
  detectInlineDataDelimiter,
  inferDelimiter,
  parseInlineData,
  parseReplayVariablesText,
} from './scriptEditorUtils'
import { type ScriptDefinition } from '../types/scripts'

describe('scriptEditorUtils replay handoff helpers', () => {
  it('parses replay variables text into string values', () => {
    expect(parseReplayVariablesText('')).toBeUndefined()
    expect(parseReplayVariablesText('{}')).toBeUndefined()
    expect(parseReplayVariablesText('{"token":"abc","count":3,"empty":null}')).toEqual({
      token: 'abc',
      count: '3',
      empty: '',
    })
  })

  it('rejects non-object replay variables', () => {
    expect(() => parseReplayVariablesText('[]')).toThrow('Replay variables must be a JSON object')
    expect(() => parseReplayVariablesText('"token"')).toThrow('Replay variables must be a JSON object')
  })

  it('builds a browser replay payload with normalized browser defaults', () => {
    const script: ScriptDefinition = {
      name: 'Mobile Checkout',
      version: '1.0.0',
      protocol: 'browser',
      config: {
        base_url: 'https://shop.example.com',
        browser: {
          engine: 'webkit',
          headless: false,
          device_profile: 'iphone_15_pro',
          touch_mode: true,
          orientation: 'portrait',
          client_metrics: true,
          capture_screenshots: 'on_failure',
          viewport: { width: 393 },
        },
      },
      actions: [],
    }

    expect(buildReplayStartPayload(script, {
      projectId: 'project-1',
      groupName: '  ',
      replaySessionId: 'replay-1',
      variables: { token: 'abc' },
      ignoreThinkTime: true,
      startPaused: true,
    })).toEqual({
      group_name: 'Mobile Checkout-replay',
      replay_session_id: 'replay-1',
      ignore_think_time: true,
      start_paused: true,
      variables: { token: 'abc' },
      script: expect.objectContaining({
        name: 'Mobile Checkout',
        project_id: 'project-1',
      }),
      browser: {
        engine: 'webkit',
        headless: false,
        slow_mo_ms: undefined,
        user_agent: undefined,
        device_profile: 'iphone_15_pro',
        touch_mode: true,
        orientation: 'portrait',
        client_metrics: true,
        capture_screenshots: 'per_action',
        viewport: { width: 393, height: 900 },
      },
    })
  })

  it('builds a non-browser replay payload without browser-only options', () => {
    const script: ScriptDefinition = {
      name: 'API Smoke',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.com' },
      actions: [],
      project_id: 'existing-project',
    }

    expect(buildReplayStartPayload(script, {
      projectId: 'project-1',
      groupName: 'api-group',
      ignoreThinkTime: false,
      startPaused: true,
    })).toEqual({
      group_name: 'api-group',
      replay_session_id: undefined,
      ignore_think_time: false,
      start_paused: undefined,
      variables: undefined,
      script,
      browser: undefined,
    })
  })

  it('detects comma and tab inline data delimiters', () => {
    expect(detectInlineDataDelimiter('username,password\nalice,secret')).toBe(',')
    expect(detectInlineDataDelimiter('username\tpassword\nalice\tsecret')).toBe('\t')
    expect(detectInlineDataDelimiter('username\nalice')).toBeNull()
  })

  it('uses auto delimiter detection while preserving manual overrides', () => {
    const tabData = 'username\tpassword\nalice\tsecret'

    expect(inferDelimiter({
      name: 'users',
      type: 'csv',
      path: '',
      strategy: 'sequential',
      delimiter: 'auto',
      inline_data: tabData,
    })).toBe('\t')

    expect(parseInlineData({
      name: 'users',
      type: 'csv',
      path: '',
      strategy: 'sequential',
      delimiter: ',',
      first_row_header: true,
      inline_data: tabData,
    }).columns).toEqual(['username', 'password'])

    expect(parseInlineData({
      name: 'users',
      type: 'csv',
      path: '',
      strategy: 'sequential',
      delimiter: 'comma',
      first_row_header: true,
      inline_data: tabData,
    }).columns).toEqual(['username\tpassword'])
  })
})
