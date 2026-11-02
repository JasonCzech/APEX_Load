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

  const funnelSteps = () => Object.fromEntries(Array.from(container.querySelectorAll('.har-funnel-step')).map(step => [
    step.querySelector('.har-funnel-label')?.textContent,
    step.querySelector('.har-funnel-value')?.textContent,
  ]))
  // The lifecycle group heading above a variable's row.
  const stageOf = (variable: string) => {
    let current = ''
    for (const row of Array.from(container.querySelectorAll('.har-studio-table tbody tr'))) {
      if (row.classList.contains('har-studio-stage-row')) current = row.textContent || ''
      else if (row.querySelector('th[scope="row"]')?.textContent === variable) return current
    }
    return undefined
  }

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
          stats={{ format: 'har', total_requests: 246, filtered_assets: 179, filtered_telemetry: 2, converted_actions: 4, assertions: 0, extractors: 1, correlations_detected: 1 }}
          salesforceReview={{
            detected: true,
            har_match_coverage: { har_provided: true, imported_requests: 55, matched_requests: 55, coverage_pct: 100 },
            native_preview: {
              correlations: [
                { name: 'corr_salesforce_sid', source: 'header', expression: 'Location: sid=([^&]+)', found_in_seq: 1, rule_id: 'salesforce.frontdoor_sid', confidence: 0.95 },
                { name: 'corr_aura_fwuid', source: 'body', expression: '"fwuid":"([^"]+)"', found_in_seq: 1, rule_id: 'salesforce.aura_context_fwuid', confidence: 0.99, reasons: ['same_day_release_coupled: constant across paired same-day captures'] },
              ],
            },
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

    // The server's filtered_assets already counts the telemetry beacons.
    const funnel = funnelSteps()
    expect(funnel['Captured HAR entries']).toBe('246')
    expect(funnel['Filtered static & telemetry']).toBe('179')
    expect(container.textContent).toContain('177 static · 2 telemetry')
    expect(container.textContent).toContain('Init 1 · Main 2 · Teardown 1 · 0 transactions')
    expect(funnel['Wired extractors']).toBe('1')
    expect(funnel['Engineer decisions']).toBe('1')
    expect(stageOf('corr_salesforce_sid')).toBe('Lifecycle stage 1: Identity & Frontdoor SSO')
    expect(stageOf('corr_aura_fwuid')).toBe('Lifecycle stage 2: Lightning bootstrap (/one/one.app)')
    expect(container.textContent).toContain('Static same-day (release-coupled)')

    const excludeBtn = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Exclude action from draft')
    expect(excludeBtn?.getAttribute('aria-label')).toBe('Exclude action from draft: 94 - POST aura')
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

  it('derives the funnel from server stats without a total and never double-counts telemetry', () => {
    const script: ScriptDefinition = {
      name: 'Fallback', version: '1', protocol: 'http', config: { base_url: 'https://app.example.test' },
      actions: [{ name: 'GET /', type: 'request', method: 'GET', target: '/' }],
    }
    act(() => {
      root.render(<HarCorrelationStudio script={script} stats={{ filtered_assets: 179, filtered_telemetry: 2, converted_actions: 65, skipped_requests: 2 }} />)
    })
    const funnel = funnelSteps()
    expect(funnel['Captured HAR entries']).toBe('246')
    expect(funnel['Filtered static & telemetry']).toBe('179')
    expect(container.textContent).toContain('177 static · 2 telemetry')
  })

  it('groups by server rules and producers, not by variable-name substrings', () => {
    const script: ScriptDefinition = {
      name: 'Salesforce', version: '1', protocol: 'http', config: { base_url: 'https://org.my.salesforce.com' },
      actions: [{
        name: '12 - POST aura', type: 'request', method: 'POST', target: '/aura?r=4',
        extractors: [{ name: 'corr_consideration_id', type: 'json', source: 'body', expression: '$.actions[0].returnValue.id' }],
      }],
    }
    act(() => {
      root.render(
        <HarCorrelationStudio
          script={script}
          salesforceReview={{ detected: true, har_match_coverage: { har_provided: true, imported_requests: 1, matched_requests: 1, coverage_pct: 100 } }}
          harDiff={{
            artifact_mode: 'dual_har', compared: true, baseline_correlations: 1, comparison_correlations: 1, dynamic_candidates: 1, static_candidates: 1, unmatched_candidates: 0,
            classifications: [
              { name: 'corr_uploaded_file_id', classification: 'static', diff_confidence: 0.9, producer_found: true, found_in_step: 1, reason: 'Same in both captures.' },
              { name: 'client_counter', classification: 'dynamic', diff_confidence: 0.8, reason: 'No server producer.' },
            ],
          }}
        />,
      )
    })
    // 'sid' inside consideration and 'loaded' inside uploaded no longer
    // decide the stage or the release-coupled status.
    expect(stageOf('corr_consideration_id')).toBe('Lifecycle stage 3: Aura & Apex actions (Controller$method)')
    expect(stageOf('corr_uploaded_file_id')).toBe('Lifecycle stage 3: Aura & Apex actions (Controller$method)')
    expect(container.textContent).toContain('Static across pair')
    expect(container.textContent).not.toContain('release-coupled)')
    expect(stageOf('client_counter')).toBe('Lifecycle stage 4: Client counters & per-request IDs')
  })

  it('groups a non-Salesforce HAR by authentication, application and client inputs', () => {
    const script: ScriptDefinition = {
      name: 'Shop', version: '1', protocol: 'http', config: { base_url: 'https://shop.example.test' },
      init: [{ name: 'POST /login', type: 'request', method: 'POST', target: '/login', extractors: [{ name: 'session_token', type: 'json', source: 'body', expression: '$.token' }] }],
      actions: [{ name: 'GET /cart', type: 'request', method: 'GET', target: '/cart', extractors: [{ name: 'cart_id', type: 'json', source: 'body', expression: '$.id' }] }],
    }
    act(() => {
      root.render(
        <HarCorrelationStudio
          script={script}
          harDiff={{
            artifact_mode: 'dual_har', compared: true, baseline_correlations: 1, comparison_correlations: 1, dynamic_candidates: 1, static_candidates: 0, unmatched_candidates: 0,
            classifications: [{ name: 'nonce', classification: 'dynamic', diff_confidence: 0.7, reason: 'Client nonce.' }],
          }}
        />,
      )
    })
    expect(stageOf('session_token')).toBe('Authentication & session')
    expect(stageOf('cart_id')).toBe('Application endpoints')
    expect(stageOf('nonce')).toBe('Client-generated inputs')
  })

  it('counts blockers and requests inside control_parallel branches', () => {
    const script: ScriptDefinition = {
      name: 'Parallel', version: '1', protocol: 'http', config: { base_url: 'https://app.example.test' },
      actions: [{
        name: 'Parallel', type: 'control_parallel', target: '',
        control: { branches: [{ name: 'Branch 1', steps: [{ name: 'Beacon', type: 'request', method: 'POST', target: '/beacon', metadata: { import_action_uid: 'har-entry-9', execution_blocker: 'har_request_body_not_exported' } }] }] },
      } as ScriptDefinition['actions'][number]],
    }
    expect(findBlockedUnexportedBodyActions(script)).toMatchObject([{ uid: 'har-entry-9', path: 'actions[0].control.branches[0].steps[0]' }])
  })

  it('links Recording Studio and explains cookies the HAR export stripped', () => {
    const script: ScriptDefinition = {
      name: 'Salesforce', version: '1', protocol: 'http', config: { base_url: 'https://org.my.salesforce.com' },
      actions: [
        { name: 'GET /one/one.app', type: 'request', method: 'GET', target: '/one/one.app', extractors: [{ name: 'corr_salesforce_aura_token', type: 'regex', source: 'header', expression: 'Set-Cookie: __Host-ERIC_PROD=([^;]+)' }] },
        { name: 'POST aura', type: 'request', method: 'POST', target: '/aura', metadata: { import_action_uid: 'har-entry-4', execution_blockers: ['har_request_body_not_exported'] } },
      ],
    }
    act(() => {
      root.render(
        <HarCorrelationStudio
          script={script}
          recordingStudioHref="#/scripts?mode=record&projectId=p1"
          onExcludeBlockedActions={vi.fn()}
          salesforceReview={{
            detected: true,
            har_match_coverage: { har_provided: true, imported_requests: 2, matched_requests: 2, coverage_pct: 100 },
            native_preview: { correlations: [{ name: 'corr_salesforce_aura_token', source: 'header', expression: 'x', found_in_seq: 1, rule_id: 'salesforce.aura_token_uncaptured_cookie' }] },
          }}
        />,
      )
    })
    expect(container.querySelector('[aria-label="Cookies missing from the HAR export"]')?.textContent).toContain('Set-Cookie headers')
    expect(container.textContent).toContain('Expected at live replay')
    expect(stageOf('corr_salesforce_aura_token')).toBe('Lifecycle stage 2: Lightning bootstrap (/one/one.app)')
    const links = Array.from(container.querySelectorAll<HTMLAnchorElement>('a')).filter(link => link.textContent === 'Record in Recording Studio')
    // One in the cookie callout, one next to the blocker's exclude actions.
    expect(links).toHaveLength(2)
    expect(links.every(link => link.getAttribute('href') === '#/scripts?mode=record&projectId=p1')).toBe(true)
  })

  it('moves focus to the next blocker and announces an exclusion', () => {
    const blocked = (id: number) => ({ name: `POST aura ${id}`, type: 'request', method: 'POST', target: `/aura?r=${id}`, metadata: { import_action_uid: `har-entry-${id}`, execution_blockers: ['har_request_body_not_exported'] } })
    const script = (ids: number[]): ScriptDefinition => ({ name: 'Blocked', version: '1', protocol: 'http', config: { base_url: 'https://app.example.test' }, actions: ids.map(blocked) })
    const onExclude = vi.fn()
    act(() => { root.render(<HarCorrelationStudio script={script([1, 2, 3])} onExcludeBlockedActions={onExclude} />) })
    const buttons = () => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).filter(button => button.textContent === 'Exclude action from draft')
    act(() => { buttons()[1].focus(); buttons()[1].click() })
    expect(onExclude).toHaveBeenCalledWith({ actionUids: ['har-entry-2'] })
    act(() => { root.render(<HarCorrelationStudio script={script([1, 3])} onExcludeBlockedActions={onExclude} />) })
    expect(document.activeElement?.getAttribute('aria-label')).toBe('Exclude action from draft: POST aura 3')
    expect(container.querySelector('.har-correlation-studio [role="status"]')?.textContent).toBe('Excluded 1 blocked request from the draft. 2 remain.')
    act(() => { buttons()[1].click() })
    act(() => { root.render(<HarCorrelationStudio script={script([1])} onExcludeBlockedActions={onExclude} />) })
    expect(document.activeElement?.getAttribute('aria-label')).toBe('Exclude action from draft: POST aura 1')
    act(() => { buttons()[0].click() })
    act(() => { root.render(<HarCorrelationStudio script={script([])} onExcludeBlockedActions={onExclude} />) })
    expect(document.activeElement?.textContent).toBe('Correlation studio & capture funnel')
  })
})
