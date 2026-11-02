// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ParameterizationReview from './ParameterizationReview'

const applyImportParameterizationMock = vi.fn()

vi.mock('../hooks/useApi', () => ({
  useApi: () => ({
    applyImportParameterization: applyImportParameterizationMock,
  }),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const baseScript = {
  name: 'Salesforce Login',
  version: '1.0',
  protocol: 'http',
  config: { base_url: 'https://org.my.salesforce.com' },
  actions: [{ name: 'Home', type: 'request', method: 'GET', target: '/one/one.app' }],
}

const suggestionDefaults = {
  rule_id: 'rule.test',
  auto_apply: false,
  high_risk: false,
}

const emptySummary = {
  total: 3,
  by_kind: { data_param: 3 },
  auto_apply_count: 0,
  review_count: 3,
  high_risk_count: 0,
  pre_applied_count: 0,
}

describe('ParameterizationReview', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    applyImportParameterizationMock.mockReset()
  })

  afterEach(() => {
    act(() => {
      root.unmount()
    })
    container.remove()
  })

  it('collapses low-signal browser capability headers when primary data parameter suggestions exist', () => {
    act(() => {
      root.render(
        <ParameterizationReview
          script={baseScript}
          onApplied={vi.fn()}
          parameterization={{
            summary: emptySummary,
            suggestions: [
              {
                ...suggestionDefaults,
                id: 'customer-account',
                kind: 'data_param',
                var_name: 'account_number',
                sample_value: 'ACCT-90210',
                replacement: '{{data.accounts.account_number}}',
                confidence: 0.85,
                rationale: 'Account identifier varies across requests.',
              },
              {
                ...suggestionDefaults,
                id: 'hdr-sec-ch-ua',
                kind: 'data_param',
                var_name: 'sec_ch_ua_platform',
                sample_value: '"macOS"',
                replacement: '{{param.sec_ch_ua_platform}}',
                confidence: 0.4,
                rationale: 'Client hint header.',
              },
              {
                ...suggestionDefaults,
                id: 'hdr-accept-lang',
                kind: 'data_param',
                var_name: 'accept_language',
                sample_value: 'en-US,en;q=0.9',
                replacement: '{{param.accept_language}}',
                confidence: 0.4,
                rationale: 'Locale header.',
              },
            ],
          }}
        />,
      )
    })

    const browserDetails = container.querySelector<HTMLDetailsElement>('.paramize-browser-headers')
    expect(browserDetails).not.toBeNull()
    expect(browserDetails?.open).toBe(false)
    expect(browserDetails?.querySelector('summary')?.textContent).toContain('Browser capability headers (2)')

    const primaryList = container.querySelector<HTMLUListElement>('.paramize-group-data_param > .paramize-list')
    expect(primaryList?.textContent).toContain('account_number')
    expect(primaryList?.textContent).not.toContain('sec_ch_ua_platform')
    expect(browserDetails?.textContent).toContain('sec_ch_ua_platform')
    expect(browserDetails?.textContent).toContain('accept_language')

    const checkbox = browserDetails?.querySelector<HTMLInputElement>('input[type="checkbox"]')
    expect(checkbox).not.toBeNull()
    act(() => {
      checkbox!.click()
    })
    expect(browserDetails?.querySelector('summary')?.textContent).toContain('1 selected')
  })

  it('keeps auto-applied or sole browser capability suggestions directly visible', () => {
    act(() => {
      root.render(
        <ParameterizationReview
          script={baseScript}
          onApplied={vi.fn()}
          parameterization={{
            summary: emptySummary,
            suggestions: [
              {
                ...suggestionDefaults,
                id: 'hdr-only',
                kind: 'data_param',
                var_name: 'user_agent',
                sample_value: 'Mozilla/5.0',
                replacement: '{{param.user_agent}}',
                confidence: 0.5,
                rationale: 'User-Agent header.',
              },
            ],
          }}
        />,
      )
    })

    expect(container.querySelector('.paramize-browser-headers')).toBeNull()
    expect(container.querySelector('.paramize-group-data_param > .paramize-list')?.textContent).toContain('user_agent')
  })
})
