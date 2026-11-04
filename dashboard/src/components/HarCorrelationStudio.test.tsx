// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import HarCorrelationStudio from './HarCorrelationStudio'
import { findBlockedUnexportedBodyActions, truncateFieldName } from './harCorrelationStudioModel'
import type { ScriptDefinition } from '../types/scripts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('truncateFieldName', () => {
  it('cuts at 200 UTF-8 bytes and drops a split character, as the pair diff does', () => {
    const ascii = 'a'.repeat(250)
    expect(truncateFieldName(ascii)).toBe('a'.repeat(200))
    expect(truncateFieldName('short')).toBe('short')
    // 120 two-byte characters are 240 bytes: the server keeps 100 of them.
    expect(truncateFieldName('é'.repeat(120))).toBe('é'.repeat(100))
    // A three-byte character the 200th byte splits is dropped whole.
    expect(truncateFieldName('a'.repeat(199) + '漢字')).toBe('a'.repeat(199))
    expect(truncateFieldName('a'.repeat(198) + '😀b')).toBe('a'.repeat(198))
    expect(truncateFieldName('a'.repeat(196) + '😀b')).toBe('a'.repeat(196) + '😀')
  })
})

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

  // The import API serializes an empty list as null; the review must render
  // rather than fail as "Authoring tools could not be loaded".
  it('renders when the import result sends null review lists', () => {
    const script: ScriptDefinition = {
      name: 'Null lists', version: '1', protocol: 'http', config: { base_url: 'https://app.example.test' },
      actions: [{ name: 'GET /', type: 'request', method: 'GET', target: '/' }],
    }
    expect(() => act(() => {
      root.render(<HarCorrelationStudio script={script} warnings={null} diagnostics={null} unsupportedElements={null} reviewSections={null} />)
    })).not.toThrow()
    expect(container.querySelector('.har-funnel-step')).not.toBeNull()
  })

  it('does not count the recording profile drops as static assets', () => {
    const script: ScriptDefinition = {
      name: 'Profiled', version: '1', protocol: 'http', config: { base_url: 'https://org.my.salesforce.com' },
      actions: [{ name: 'GET /', type: 'request', method: 'GET', target: '/' }],
    }
    const warnings = [
      'recording profile "Salesforce \\"lean\\"" left 12 Aura request(s) out because every action in them matches its excluded Aura descriptors (matched as substrings: "ComponentLogger")',
      'recording profile "Salesforce" left 3 request(s) out by host or content type',
      'unrelated warning that left 9 request(s) out',
    ]
    act(() => {
      root.render(<HarCorrelationStudio script={script} warnings={warnings} stats={{ total_requests: 120, filtered_assets: 40, filtered_telemetry: 8, converted_actions: 80 }} />)
    })
    expect(funnelSteps()['Filtered static & telemetry']).toBe('40')
    expect(container.textContent).toContain('17 static · 15 by recording profile · 8 telemetry')
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

  it('counts requests, not recorded transaction markers', () => {
    const marker = (name: string, start: boolean) => ({
      name: `${start ? 'Start' : 'End'} ${name} transaction`, type: 'source_transaction', target: '',
      params: { function: start ? 'lr_start_transaction' : 'lr_end_transaction', args: start ? [name] : [name, 'LR_AUTO'] },
    })
    const script: ScriptDefinition = {
      name: 'Recorded', version: '1', protocol: 'http', config: { base_url: 'https://app.example.test' },
      init: [marker('01_login', true), { name: '1 - POST login', type: 'request', method: 'POST', target: '/login', extractors: [{ name: 'token', type: 'json', source: 'body', expression: '$.t' }] }, marker('01_login', false)],
      actions: [
        marker('02_home', true),
        { name: '2 - GET home', type: 'request', method: 'GET', target: '/home?t={{var.token}}' },
        { name: '3 - GET feed', type: 'request', method: 'GET', target: '/feed', headers: { Authorization: 'Bearer {{var.token}}' } },
        marker('02_home', false),
      ],
    }
    act(() => { root.render(<HarCorrelationStudio script={script} />) })
    expect(funnelSteps()['Generated actions']).toBe('3')
    expect(container.textContent).toContain('Init 1 · Main 2 · Teardown 0 · 2 transactions')
    const tokenRow = Array.from(container.querySelectorAll('.har-studio-table tbody tr')).find(row => row.querySelector('th[scope="row"]')?.textContent === 'token')!
    expect(tokenRow.textContent).toContain('Init #1 · 1 - POST login')
    expect(tokenRow.textContent).toContain('2 steps (first: Actions #2)')
  })

  it('lists changed request inputs the script still sends as recorded under client inputs', () => {
    const script: ScriptDefinition = {
      name: 'Salesforce', version: '1', protocol: 'http', config: { base_url: 'https://org.my.salesforce.com' },
      actions: [{ name: '12 - POST aura', type: 'request', method: 'POST', target: '/aura?r=4' }],
    }
    act(() => {
      root.render(
        <HarCorrelationStudio
          script={script}
          salesforceReview={{ detected: true, har_match_coverage: { har_provided: true, imported_requests: 1, matched_requests: 1, coverage_pct: 100 } }}
          harDiff={{
            artifact_mode: 'dual_har', compared: true, baseline_correlations: 0, comparison_correlations: 0, dynamic_candidates: 0, static_candidates: 0, unmatched_candidates: 0,
            unhandled_inputs: [
              { field: 'form:message$.actions[].params.clientRequestId', kind: 'client_generated_id', requests: 3, distinct_values: 3, baseline_entries: [12, 14, 20, 31], reason: 'A new value in every request.' },
              { field: 'header:X-Sfdc-Page-Cache', kind: 'server_value', requests: 1, distinct_values: 1, reason: 'Issued by an earlier response.' },
            ],
            unhandled_inputs_truncated: true,
          }}
        />,
      )
    })
    expect(stageOf('form:message$.actions[].params.clientRequestId')).toBe('Lifecycle stage 4: Client counters & per-request IDs')
    expect(stageOf('header:X-Sfdc-Page-Cache')).toBe('Lifecycle stage 3: Aura & Apex actions (Controller$method)')
    expect(container.textContent).toContain('Sent as recorded · Client-generated ID')
    expect(container.textContent).toContain('Use {{fn.uuid()}}')
    expect(container.textContent).toContain('3 requests (HAR entries 12, 14, 20, …)')
    expect(container.textContent).toContain('Only the first 2 changed request fields are listed')
    expect(container.textContent).toContain('Needs review (2)')
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

  it('re-checks unhandled request inputs against the current draft', () => {
    const harDiff = {
      artifact_mode: 'dual_har', compared: true, baseline_correlations: 0, comparison_correlations: 0, dynamic_candidates: 0, static_candidates: 0, unmatched_candidates: 0,
      unhandled_inputs: [
        { field: 'query:_', kind: 'client_timestamp', requests: 2, distinct_values: 2, reason: 'Epoch milliseconds.' },
        { field: 'body.request.clientId', kind: 'client_generated_id', requests: 1, distinct_values: 1, reason: 'A new value in every request.' },
      ],
    }
    const draft = (timestamp: string, clientId: string): ScriptDefinition => ({
      name: 'Shop', version: '1', protocol: 'http', config: { base_url: 'https://shop.example.test' },
      actions: [
        { name: 'GET /feed', type: 'request', method: 'GET', target: `/feed?_=${timestamp}` },
        { name: 'GET /more', type: 'request', method: 'GET', target: `/more?page=2&_=${timestamp}` },
        { name: 'POST /order', type: 'request', method: 'POST', target: '/order', body: `{"request":{"clientId":${clientId}}}` },
      ],
    })
    const row = (field: string) => Array.from(container.querySelectorAll('.har-studio-table tbody tr')).find(candidate => candidate.querySelector('th[scope="row"]')?.textContent === field)!
    act(() => { root.render(<HarCorrelationStudio script={draft('1730000000000', '"c-1"')} harDiff={harDiff} />) })
    expect(row('query:_').textContent).toContain('Sent as recorded · Client timestamp')
    expect(row('body.request.clientId').textContent).toContain('Not parameterized')
    expect(container.textContent).toContain('Needs review (2)')

    // The user parameterized both in the review; an unquoted template in a
    // JSON body counts as one too.
    act(() => { root.render(<HarCorrelationStudio script={draft('{{param.now_ms}}', '{{fn.uuid()}}')} harDiff={harDiff} />) })
    expect(row('query:_').textContent).toContain('Parameterized in the draft · Client timestamp')
    expect(row('query:_').textContent).not.toContain('Not parameterized')
    expect(row('body.request.clientId').textContent).toContain('Parameterized in the draft')
    expect(container.textContent).toContain('Needs review (0)')

    // One request still sending the recorded value keeps the row open.
    const partial = draft('{{param.now_ms}}', '{{fn.uuid()}}')
    partial.actions![1] = { ...partial.actions![1], target: '/more?page=2&_=1730000000000' }
    act(() => { root.render(<HarCorrelationStudio script={partial} harDiff={harDiff} />) })
    expect(row('query:_').textContent).toContain('Sent as recorded · Client timestamp')
  })

  it('stops asking for a decision on an input only excluded requests sent', () => {
    const harDiff = {
      artifact_mode: 'dual_har', compared: true, baseline_correlations: 0, comparison_correlations: 0, dynamic_candidates: 0, static_candidates: 0, unmatched_candidates: 0,
      unhandled_inputs: [
        { field: 'query:_', kind: 'client_timestamp', requests: 1, distinct_values: 2, reason: 'Epoch milliseconds.' },
        { field: 'form:aura.pageURI', kind: 'client_counter', requests: 1, distinct_values: 2, reason: 'Page URI.' },
      ],
    }
    const base: ScriptDefinition = {
      name: 'Shop', version: '1', protocol: 'http', config: { base_url: 'https://shop.example.test' },
      actions: [{ name: 'GET /feed', type: 'request', method: 'GET', target: '/feed?_=1730000000000' }],
    }
    const row = (field: string) => Array.from(container.querySelectorAll('.har-studio-table tbody tr')).find(candidate => candidate.querySelector('th[scope="row"]')?.textContent === field)!

    // The blocked request that sent form:aura.pageURI was excluded.
    act(() => { root.render(<HarCorrelationStudio script={base} harDiff={harDiff} stepPositionsStale />) })
    expect(row('form:aura.pageURI').textContent).toContain('No longer sent · Client counter')
    expect(row('form:aura.pageURI').textContent).not.toContain('Sent as recorded')
    expect(row('query:_').textContent).toContain('Sent as recorded · Client timestamp')
    expect(container.textContent).toContain('Needs review (1)')

    // Without an exclusion the absence means the field went unread: keep it open.
    act(() => { root.render(<HarCorrelationStudio script={base} harDiff={harDiff} />) })
    expect(row('form:aura.pageURI').textContent).toContain('Sent as recorded · Client counter')
    expect(container.textContent).toContain('Needs review (2)')

    // A request body the studio cannot read could still send it.
    const unreadable: ScriptDefinition = { ...base, actions: [...base.actions!, { name: 'POST /aura', type: 'request', method: 'POST', target: '/aura', body: 'message=1\naura.pageURI=%2Fone' }] }
    act(() => { root.render(<HarCorrelationStudio script={unreadable} harDiff={harDiff} stepPositionsStale />) })
    expect(row('form:aura.pageURI').textContent).toContain('Sent as recorded · Client counter')
    expect(container.textContent).toContain('Needs review (2)')
  })

  it('stops naming diff producers by step number once an exclusion moved the steps', () => {
    const script: ScriptDefinition = { name: 'Shop', version: '1', protocol: 'http', config: { base_url: 'https://shop.example.test' }, actions: [{ name: 'GET /', type: 'request', method: 'GET', target: '/' }] }
    const harDiff = {
      artifact_mode: 'dual_har', compared: true, baseline_correlations: 1, comparison_correlations: 1, dynamic_candidates: 1, static_candidates: 0, unmatched_candidates: 0,
      classifications: [{ name: 'corr_cart', classification: 'dynamic', diff_confidence: 0.9, producer_found: true, found_in_step: 12, used_in_steps: [13, 15], reason: 'Differed across captures.' }],
    }
    const row = () => Array.from(container.querySelectorAll('.har-studio-table tbody tr')).find(candidate => candidate.querySelector('th[scope="row"]')?.textContent === 'corr_cart')!
    act(() => { root.render(<HarCorrelationStudio script={script} harDiff={harDiff} />) })
    expect(row().textContent).toContain('Step #12')
    expect(row().textContent).toContain('2 steps')
    act(() => { root.render(<HarCorrelationStudio script={script} harDiff={harDiff} stepPositionsStale />) })
    expect(row().textContent).not.toContain('Step #12')
    expect(row().textContent).toContain('its step number predates the exclusion')
    expect(row().textContent).toContain('2 recorded steps at import')
  })

  it('says the eikoocnekot cookie name is confirmed only when the analyzer confirmed it', () => {
    const script: ScriptDefinition = {
      name: 'Salesforce', version: '1', protocol: 'http', config: { base_url: 'https://org.my.salesforce.com' },
      actions: [{ name: 'GET /one/one.app', type: 'request', method: 'GET', target: '/one/one.app', extractors: [{ name: 'corr_salesforce_aura_token', type: 'regex', source: 'header', expression: 'x' }] }],
    }
    const review = (reasons: string[]) => ({
      detected: true,
      har_match_coverage: { har_provided: true, imported_requests: 1, matched_requests: 1, coverage_pct: 100 },
      native_preview: { correlations: [{ name: 'corr_salesforce_aura_token', source: 'header', expression: 'x', found_in_seq: 1, rule_id: 'salesforce.aura_token_uncaptured_cookie', reasons }] },
    })
    const base = ['aura.token is sent by later Aura requests but no captured response issues it', 'Lightning sets it in the __Host-ERIC_* Set-Cookie of /one/one.app, which the capture does not include']
    act(() => { root.render(<HarCorrelationStudio script={script} salesforceReview={review(base)} />) })
    expect(container.textContent).not.toContain('Confirmed via auraConfig.eikoocnekot')
    expect(container.textContent).toContain('Not confirmed')
    act(() => { root.render(<HarCorrelationStudio script={script} salesforceReview={review([...base, 'confirmed auraConfig.eikoocnekot (__Host-ERIC_PROD) in /one/one.app bootstrap HTML'])} />) })
    expect(container.textContent).toContain('Confirmed via auraConfig.eikoocnekot (__Host-ERIC_PROD)')
  })

  it('does not announce a later drop in blockers after a refused or failed exclusion', () => {
    const blocked = (id: number) => ({ name: `POST aura ${id}`, type: 'request', method: 'POST', target: `/aura?r=${id}`, metadata: { import_action_uid: `har-entry-${id}`, execution_blockers: ['har_request_body_not_exported'] } })
    const script = (ids: number[]): ScriptDefinition => ({ name: 'Blocked', version: '1', protocol: 'http', config: { base_url: 'https://app.example.test' }, actions: ids.map(blocked) })
    const status = () => container.querySelector('.har-correlation-studio [role="status"]')?.textContent
    const buttons = () => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).filter(button => button.textContent === 'Exclude action from draft')
    // Refused (an expired draft): the host starts nothing.
    const refuse = vi.fn(() => false)
    act(() => { root.render(<HarCorrelationStudio script={script([1, 2, 3, 4])} onExcludeBlockedActions={refuse} />) })
    act(() => { buttons()[0].click() })
    expect(refuse).toHaveBeenCalled()
    // A re-inspection then finds fewer blockers.
    act(() => { root.render(<HarCorrelationStudio script={script([1])} onExcludeBlockedActions={refuse} />) })
    expect(status()).toBe('')

    // Failed: busy while it ran, then the same blockers.
    const accept = vi.fn(() => true)
    act(() => { root.render(<HarCorrelationStudio script={script([1, 2])} onExcludeBlockedActions={accept} />) })
    act(() => { buttons()[1].click() })
    act(() => { root.render(<HarCorrelationStudio script={script([1, 2])} onExcludeBlockedActions={accept} busy />) })
    act(() => { root.render(<HarCorrelationStudio script={script([1, 2])} onExcludeBlockedActions={accept} />) })
    act(() => { root.render(<HarCorrelationStudio script={script([1])} onExcludeBlockedActions={accept} />) })
    expect(status()).toBe('')
  })

  it('consolidates HAR preflight, governance, Aura outcomes, provenance, domains, and conversion log into a 3-zone workbench with zero accordions', () => {
    const script: ScriptDefinition = {
      name: 'Consolidated Salesforce HAR',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://org.my.salesforce.com' },
      actions: [
        {
          name: '1 - POST aura',
          type: 'request',
          method: 'POST',
          target: '/aura',
          extractors: [{ name: 'corr_salesforce_sid', type: 'regex', source: 'header', expression: 'sid=([^;]+)' }],
        },
      ],
    }
    const onViewSource = vi.fn()
    act(() => {
      root.render(
        <HarCorrelationStudio
          script={script}
          stats={{ format: 'har', total_requests: 10, filtered_assets: 4, converted_actions: 1, extractors: 1 }}
          preflight={{
            valid: true,
            action_count: 1,
            expected_action_count: 1,
            data_source_count: 0,
            parameter_count: 0,
            browser_required: false,
            checks: [{ id: 'har_accounting', label: 'HAR replay accounting', status: 'pass', detail: 'Matched.' }],
          }}
          harSummary={{
            artifact_mode: 'design_har_pair',
            artifact_sensitivity: 'restricted_har',
            retention_days: 7,
          }}
          sourceReview={{
            source_files: [{ path: 'sample.har', language: 'json', content: '{}' }],
            mappings: [{ id: 'map-1', source_file: 'sample.har', start_line: 1, target_path: 'actions[0]', target_kind: 'action', status: 'converted' }],
          }}
          salesforceReview={{
            detected: true,
            domains: [{ host: 'org.my.salesforce.com', kind: 'lightning', count: 1 }],
            har_match_coverage: { har_provided: true, imported_requests: 1, matched_requests: 1, coverage_pct: 100 },
            aura_outcomes: {
              complete: true,
              total_batches: 1,
              inspected_batches: 1,
              complete_batches: 1,
              incomplete_batches: 0,
              omitted_batches: 0,
              missing_responses: 0,
              invalid_requests: 0,
              request_actions: 1,
              response_actions: 1,
              matched_actions: 1,
              batch_limit: 512,
              analysis_byte_limit: 67108864,
              state_counts: { ERROR: 1 },
              issue_counts: { non_success_state: 1 },
              batches: [{
                action_sequence: 1,
                action_path: 'actions[0]',
                har_entry_id: 1,
                request_valid: true,
                analysis: {
                  success: false,
                  complete: true,
                  has_response: true,
                  request_actions: 1,
                  response_actions: 1,
                  matched_actions: 1,
                  outcomes: [{ ordinal: 1, state: 'ERROR' }],
                  issues: [{ code: 'non_success_state', ordinal: 1 }],
                },
              }],
            },
            native_preview: {
              correlations: [{
                name: 'corr_salesforce_sid',
                source: 'header',
                expression: 'sid=([^;]+)',
                found_in_seq: 1,
                rule_id: 'salesforce.frontdoor_sid',
                producer_reference: { har_entry_id: 1, original_action_path: 'actions[0]', source_mapping_id: 'map-1', source_file: 'sample.har' },
              }],
            },
          }}
          diagnostics={[
            { code: 'sf_aura_folded', element: 'aura', detail: 'Folded Aura action at step #1', severity: 'info' },
            { code: 'sf_aura_folded', element: 'aura', detail: 'Folded Aura action at step #2', severity: 'info' },
          ]}
          onViewSource={onViewSource}
        />,
      )
    })

    // Zero <details> accordions anywhere in the consolidated studio
    expect(container.querySelectorAll('details')).toHaveLength(0)

    // Zone 1 metadata strip combines preflight, artifact governance, and Salesforce match coverage
    const metaStrip = container.querySelector('.har-capture-meta-strip')!
    expect(metaStrip.textContent).toContain('Ready (HAR replay accounting)')
    expect(metaStrip.textContent).toContain('design_har_pair')
    expect(metaStrip.textContent).toContain('restricted_har')
    expect(metaStrip.textContent).toContain('100%')

    // Inline producer source link in Correlations table
    const inlineSourceBtn = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(b => b.textContent === 'View producer source')
    expect(inlineSourceBtn).toBeTruthy()
    act(() => { inlineSourceBtn!.click() })
    expect(onViewSource).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'map-1' }),
      'Captured correlation request',
      expect.any(String),
    )

    // Zone 2 Aura failure readiness callout switches to the Aura outcomes workbench tab
    const inspectAuraBtn = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(b => b.textContent?.includes('Inspect Aura outcomes (1)'))
    expect(inspectAuraBtn).toBeTruthy()
    act(() => { inspectAuraBtn!.click() })
    const auraTab = Array.from(container.querySelectorAll<HTMLButtonElement>('[role="tab"]'))
      .find(t => t.textContent?.startsWith('Aura outcomes'))!
    expect(auraTab.getAttribute('aria-selected')).toBe('true')

    // Deduplicated conversion log groups repeated step diagnostics into 1 row with count 2
    const logTab = Array.from(container.querySelectorAll<HTMLButtonElement>('[role="tab"]'))
      .find(t => t.textContent?.startsWith('Conversion log'))!
    expect(logTab.textContent).toBe('Conversion log (1)')
    act(() => { logTab.click() })
    const logRegion = container.querySelector('[aria-label="Deduplicated conversion log"]')!
    expect(logRegion.textContent).toContain('sf_aura_folded')
    expect(logRegion.textContent).toContain('2')
  })
})
