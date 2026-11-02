// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import HarCorrelationStudio from './HarCorrelationStudio'
import { findBlockedUnexportedBodyActions } from './harCorrelationStudioModel'
import type { ScriptDefinition } from '../types/scripts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('HarCorrelationStudio', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('finds unexported-body blockers across top-level actions and http_sequence params.requests', () => {
    const script: ScriptDefinition = {
      name: 'Salesforce Flow',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://org.my.salesforce.com' },
      init: [
        {
          name: '1 - Login POST',
          type: 'request',
          method: 'POST',
          target: '/',
          extractors: [{ name: 'corr_salesforce_sid', type: 'regex', source: 'header', expression: 'Location: sid=([^&]+)' }],
        },
      ],
      actions: [
        {
          name: 'Parallel Aura calls',
          type: 'http_sequence',
          target: '/aura',
          params: {
            concurrent: true,
            requests: [
              {
                name: '17 - Aura Context.getCurrentApp',
                method: 'POST',
                url: '/aura?r=1',
                body: 'aura.token={{var.corr_salesforce_aura_token}}',
                metadata: { import_action_uid: 'har_uid_00017' },
              },
              {
                name: '94 - POST aura',
                method: 'POST',
                url: '/aura?r=28',
                metadata: {
                  import_action_uid: 'har_uid_00094',
                  execution_blockers: ['har_request_body_not_exported'],
                  'har.request.recorded_content_length': 512,
                },
              },
            ],
          },
        },
      ],
      teardown: [
        {
          name: '240 - GET /secur/logout.jsp',
          type: 'request',
          method: 'GET',
          target: '/secur/logout.jsp',
        },
      ],
    }

    const blocked = findBlockedUnexportedBodyActions(script)
    expect(blocked).toHaveLength(1)
    expect(blocked[0]).toMatchObject({
      uid: 'har_uid_00094',
      section: 'Actions',
      path: 'actions[0].params.requests[1]',
      name: '94 - POST aura',
      recordedBytes: 512,
    })

    const onExclude = vi.fn()
    act(() => {
      root.render(
        <HarCorrelationStudio
          script={script}
          stats={{ total_entries: 246, filtered_assets: 179, filtered_telemetry: 2, converted_actions: 4 }}
          salesforceReview={{
            detected: true,
            har_match_coverage: { har_provided: true, imported_requests: 55, matched_requests: 55, coverage_pct: 100 },
          }}
          harDiff={{
            artifact_mode: 'dual_har',
            compared: true,
            baseline_correlations: 2,
            comparison_correlations: 2,
            dynamic_candidates: 2,
            static_candidates: 1,
            unmatched_candidates: 0,
            classifications: [
              { name: 'corr_salesforce_sid', classification: 'dynamic', diff_confidence: 0.96, producer_found: true, found_in_step: 1, used_in_steps: [2], reason: 'Differed across captures with upstream Location producer.' },
              { name: 'corr_aura_fwuid', classification: 'static', diff_confidence: 0.9, producer_found: true, found_in_step: 1, used_in_steps: [2], reason: 'Identical release framework identifier across captures.' },
            ],
          }}
          onExcludeBlockedActions={onExclude}
        />,
      )
    })

    expect(container.textContent).toContain('246')
    expect(container.textContent).toContain('179 static · 2 telemetry')
    expect(container.textContent).toContain('Init 1 · Actions 2 · Teardown 1')
    expect(container.textContent).toContain('Stage 1 · Desktop sign-in & session bootstrap (Init)')
    expect(container.textContent).toContain('Stage 2 · Release-coupled Aura framework context')

    const excludeBtn = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Exclude action from draft')
    expect(excludeBtn).toBeTruthy()
    act(() => {
      excludeBtn?.click()
    })
    expect(onExclude).toHaveBeenCalledWith({ actionUids: ['har_uid_00094'] })
  })

  it('disables per-row exclude when a blocked action lacks import_action_uid and exposes explicit bulk exclude', () => {
    const script: ScriptDefinition = {
      name: 'UID-less Blocker Flow',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://org.my.salesforce.com' },
      actions: [
        {
          name: 'Legacy POST without UID',
          type: 'request',
          method: 'POST',
          target: '/aura?r=9',
          metadata: {
            execution_blockers: ['har_request_body_not_exported'],
          },
        },
      ],
    }

    const onExclude = vi.fn()
    act(() => {
      root.render(
        <HarCorrelationStudio
          script={script}
          onExcludeBlockedActions={onExclude}
        />,
      )
    })

    const perRowBtn = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Exclude action from draft')
    expect(perRowBtn).toBeTruthy()
    expect(perRowBtn?.disabled).toBe(true)
    act(() => {
      perRowBtn?.click()
    })
    expect(onExclude).not.toHaveBeenCalled()

    const bulkBtn = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes('Exclude all 1 blocked request from draft'))
    expect(bulkBtn).toBeTruthy()
    act(() => {
      bulkBtn?.click()
    })
    expect(onExclude).toHaveBeenCalledWith({ excludeUnexportedBodies: true })
  })
})
