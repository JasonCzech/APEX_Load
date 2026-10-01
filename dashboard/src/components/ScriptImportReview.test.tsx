// @vitest-environment jsdom

import { act, useContext, useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { type Parameterization } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import ScriptImportReview from './ScriptImportReview'
import { CategoryActions } from './ImportSettingsContext'
import { ImportSourceIssueFocusContext } from './ImportSourceIssueFocus'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = vi.hoisted(() => ({
  applyImportParameterization: vi.fn(),
  getProjectSecretStatuses: vi.fn(),
  createProjectSecrets: vi.fn(),
  migrateImportSecrets: vi.fn(),
  discardImportSecretSession: vi.fn(),
}))

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

// CodeMirror requires layout APIs unavailable in jsdom. The production editor
// and its semantic DSL highlighting are covered in the browser workflow.
vi.mock('./LinkedCodePane', () => ({
  default: ({ value, ariaLabel, language, semanticMode }: {
    value: string
    ariaLabel?: string
    language?: string
    semanticMode?: string
  }) => <pre aria-label={ariaLabel} data-language={language} data-semantic-mode={semanticMode}>{value}</pre>,
}))

const script = {
  name: 'Imported API Script',
  version: '1.0.0',
  protocol: 'http',
  config: { base_url: 'https://api.example.test' },
  actions: [{ name: 'Create Order', type: 'request', method: 'POST', target: '/orders' }],
}

describe('ScriptImportReview', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.clearAllMocks()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.getProjectSecretStatuses.mockResolvedValue({
      project_id: 'project-1',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
      },
      secrets: [],
    })
    apiMock.createProjectSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 0 })
    apiMock.migrateImportSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 0 })
  })

  afterEach(() => {
    act(() => {
      root.unmount()
    })
    container.remove()
  })

  it('retains LoadRunner section navigation when a compact review supplies preview data', () => {
    act(() => root.render(<ScriptImportReview compactReview title="LoadRunner review" script={script} previewData={script} />))
    const section = Array.from(container.querySelectorAll('select')).find(select => select.parentElement?.textContent?.startsWith('Section'))!
    expect(section).toBeDefined()
    act(() => { section.value = 'config'; section.dispatchEvent(new Event('change', { bubbles: true })) })
    const preview = container.querySelector('.script-import-review-preview')!.textContent
    expect(preview).toContain('"config"')
    expect(preview).toContain('https://api.example.test')
    expect(container.querySelector('pre')!.textContent).not.toContain('Create Order')
  })

  it('uses normalized behavior counts and renders each diagnostic once with source navigation', () => {
    const diagnostics = [
      { code: 'jmeter_timestamp_timezone_required', source_ref: 'plan.jmx:77', element: 'Request', detail: 'Choose a timestamp policy', severity: 'blocker' as const, location: 'actions[0]' },
      { code: 'import_missing_variables', source_ref: 'plan.jmx:77', element: 'Request', detail: 'Bind client_id, client_secret', severity: 'blocker' as const, location: 'actions[0]' },
      { code: 'csv_export', source_ref: 'plan.jmx:161', element: 'CSV export', detail: 'Translated CSV download', severity: 'info' as const },
    ]
    act(() => root.render(<ScriptImportReview tabbed compactReview title="JMeter review" script={script}
      diagnostics={diagnostics} reviewSummary={{ blocking_behaviors: 2, review_behaviors: 0, unsupported_behaviors: 0, warnings: 0, informational: 1 }}
      warnings={['Duplicate legacy warning']} unsupportedElements={['Duplicate unsupported text']}
      reviewSections={[{ key: 'old', title: 'Old summary', severity: 'blocker', summary: 'Duplicate', items: ['Choose a timestamp policy'] }]} />))
    expect(container.querySelector('[aria-label="JMeter review summary"]')?.textContent).toContain('2 blocking behaviors')
    expect(container.querySelector('.script-import-review-header [role="status"]')?.textContent).toContain('Import review has blockers')
    expect(container.querySelector('[aria-label="JMeter review summary"]')?.textContent).toContain('0 unsupported behaviors')
    expect(container.querySelectorAll('.shared-import-issue-list > button')).toHaveLength(3)
    expect(container.querySelector('.shared-import-issue-workspace')?.textContent).not.toContain('Duplicate')
    expect(container.querySelector('.shared-import-issue-detail')?.textContent).not.toContain('Mark reviewed with limitation')
    act(() => Array.from(container.querySelectorAll<HTMLButtonElement>('.shared-import-issue-row')).find(button => button.textContent?.includes('CSV export'))!.click())
    const reviewed = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Mark reviewed')!
    act(() => reviewed.click())
    expect(container.querySelector('[aria-label="JMeter review summary"]')?.textContent).toContain('2 blocking behaviors')
    const source = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Open related source')!
    expect(source).toBeDefined()
    act(() => source.click())
    expect(container.querySelector('[aria-label="Source Mapping"]')?.getAttribute('aria-selected')).toBe('true')
  })

  it('separates preserved credentials and optional data suggestions from required setup', () => {
    act(() => root.render(<ScriptImportReview tabbed compactReview title="Sample review" script={script}
      secretBindingRequirements={[{ id: 'body', path: '/actions/0/body', label: 'Request body', context: 'Request', suggested_name: 'BODY', destination_value_preserved: true }]}
      sharedValues={{ candidates: [{ id: 'origin', kind: 'repeated_literal', name: 'origin', occurrence_count: 1 }], summary: { total: 1, by_kind: { repeated_literal: 1 }, credential_count: 0, multi_script_count: 0 } }}
      onSharedValuesApplied={vi.fn()} />))
    const summary = container.querySelector('.import-review-readiness')?.textContent || ''
    expect(summary).toContain('1 preserved credential to review')
    expect(summary).toContain('1 optional data suggestion')
    expect(summary).not.toContain('credential requirement')
    expect(summary).not.toContain('suggested data change')
    act(() => Array.from(container.querySelectorAll<HTMLButtonElement>('.import-review-readiness button')).find(button => button.textContent?.includes('optional data suggestion'))!.click())
    expect(container.querySelector<HTMLDetailsElement>('.import-review-suggestions')?.open).toBe(true)
    expect(container.querySelector<HTMLDetailsElement>('.shared-values details')?.open).toBe(false)
    expect(Array.from(container.querySelectorAll<HTMLDetailsElement>('.shared-values details')).find(item => item.textContent?.startsWith('Optional substitutions'))?.open).toBe(true)
  })

  it('keeps a dry-run validation outcome visible in the compact review header', () => {
    act(() => root.render(<ScriptImportReview tabbed compactReview title="Validation Review" script={script}
      subtitle="This preview came from a dry-run validation; nothing has been saved." />))
    expect(container.querySelector('.script-import-review-header')?.textContent).toContain('nothing has been saved')
    expect(container.querySelector('.script-import-review-header details')).toBeNull()
  })

  it('passes a selected source finding to the source review surface', () => {
    function SourceFocusProbe() {
      const focus = useContext(ImportSourceIssueFocusContext)
      return <span data-testid="source-focus">{focus ? `${focus.title}: ${focus.description}` : 'no issue focus'}</span>
    }
    act(() => root.render(<ScriptImportReview tabbed compactReview title="Sample review" script={script}
      reviewGeneration="source-focus" previewAside={<SourceFocusProbe />}
      reviewSections={[{ key: 'source_order', title: 'Import Assumptions', severity: 'info', summary: '1 diagnostic', items: ['headers at line:6 [info]: the source exposed headers as a map; deterministic order was synthesized'] }]} />))
    const source = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Inspect source evidence')!
    act(() => source.click())
    expect(container.querySelector('[data-testid="source-focus"]')?.textContent).toContain('line:6')
    expect(container.querySelector('[aria-label="Source Mapping"]')?.getAttribute('aria-selected')).toBe('true')
  })

  it('falls back to legacy findings when a summary has no structured evidence', () => {
    act(() => root.render(<ScriptImportReview tabbed compactReview title="JMeter review" script={script}
      reviewSummary={{ blocking_behaviors: 0, review_behaviors: 0, unsupported_behaviors: 0, warnings: 0, informational: 0 }}
      warnings={['Legacy warning still matters']} />))
    expect(container.querySelector('.shared-import-issue-workspace')?.textContent).toContain('Legacy warning still matters')
    expect(container.querySelector('[aria-label="JMeter review summary"]')).toBeNull()
  })

  it('omits original diagnostics when the importer supplies its own issue workspace', () => {
    const render = (show: boolean) => act(() => root.render(<ScriptImportReview compactReview title="Archive review" script={script}
      showCredentials={false} showDataReview={false} showPreview={false} showPreviewAside={false} showOriginalDiagnostics={show}
      warnings={['Original runtime warning']} />))
    render(false)
    expect(container.querySelector('.shared-import-issue-workspace')).toBeNull()
    render(true)
    const workspace = container.querySelector<HTMLElement>('.shared-import-issue-workspace')!
    expect(workspace).not.toBeNull()
    expect(workspace.textContent).toContain('Original runtime warning')
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
  })

  it('keeps original diagnostics on Review in the shared tabbed importer', () => {
    act(() => root.render(<ScriptImportReview tabbed compactReview title="Import review" script={script} warnings={['Original runtime warning']} />))
    expect(container.querySelector<HTMLElement>('.shared-import-issue-workspace')?.parentElement?.hidden).toBe(false)
    act(() => container.querySelector<HTMLButtonElement>('[aria-label="Runtime settings"]')!.click())
    expect(container.querySelector<HTMLElement>('.shared-import-issue-workspace')?.parentElement?.hidden).toBe(true)
    act(() => container.querySelector<HTMLButtonElement>('[aria-label="Review"]')!.click())
    expect(container.querySelector<HTMLElement>('.shared-import-issue-workspace')?.parentElement?.hidden).toBe(false)
  })

  it('uses a compact tabbed heading and puts the importer runtime action before the settings inventory', () => {
    act(() => root.render(<ScriptImportReview tabbed title="Conversion Review" script={script}
      onSettingsApplied={vi.fn()} runtimeContent={<section aria-label="Importer runtime action">Set termination rule</section>} />))
    expect(container.querySelector('.script-import-review-compact')).not.toBeNull()
    expect(container.querySelector('.script-import-review-header h3')?.textContent).toBe('Conversion Review')
    act(() => container.querySelector<HTMLButtonElement>('[aria-label="Runtime settings"]')!.click())
    const runtimeAction = container.querySelector('[aria-label="Importer runtime action"]')!
    const inventory = container.querySelector('.import-runtime-workspace')
    expect(inventory).not.toBeNull()
    expect(runtimeAction.compareDocumentPosition(inventory!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('redacts arbitrary credential paths in the correct multi-script preview', () => {
    act(() => root.render(<ScriptImportReview tabbed title="Batch review" script={script}
      previewData={{ scripts: [script, { ...script, config: { base_url: '', custom: 'batch-secret-canary' } }] }}
      secretBindingRequirements={[{ id: 'custom', path: '/config/custom', context: 'Batch credential', label: 'Custom credential', suggested_name: 'CUSTOM', script_index: 1 }]} />))
    expect(container.querySelector('.script-import-review-preview')).toBeNull()
    act(() => container.querySelector<HTMLButtonElement>('[aria-label="DSL Preview"]')!.click())
    const preview = container.querySelector('.script-import-review-preview')!.textContent
    expect(preview).not.toContain('batch-secret-canary')
    expect(preview).toContain('[protected imported value]')
    expect(preview).toContain('Imported API Script')
  })

  it.each([false, true])('exports every generated action with credentials protected before saving (compact %s)', async (compact) => {
    const actions = Array.from({ length: 1500 }, (_, index) => ({ name: `Request ${index}`, type: 'request', target: '/orders', headers: { Authorization: 'Bearer private-token' }, params: { args: ['opaque-private'], call_args: [{ value: 'opaque-private' }] }, metadata: { raw_source: 'web_add_header("Cookie","opaque-private")' } }))
    actions[0].target = 'https://user:embedded-private@example.test/orders'
    actions[1].target = 'https://other:unclassified-private@example.test/orders'
    let exported: Blob | undefined
    const originalCreate = URL.createObjectURL
    const originalRevoke = URL.revokeObjectURL
    URL.createObjectURL = vi.fn((blob: Blob) => { exported = blob; return 'blob:protected-review' })
    URL.revokeObjectURL = vi.fn()
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    try {
      act(() => root.render(<ScriptImportReview compactReview={compact} previewData={compact ? script : undefined} title="Review" script={{ ...script, init: [actions[1]], actions }} secretBindingRequirements={[{ id: 'target-secret', path: '/actions/0/target', label: 'Request target', context: 'Request', suggested_name: 'AUTH', destination_value_preserved: true }]} />))
      if (!compact) expect(container.textContent).toContain('1501 total actions (1 initialization, 1500 main, 0 teardown)')
      expect(container.textContent).toContain('Preview truncated')
      expect(container.textContent).not.toContain('embedded-private')
      expect(container.textContent).not.toContain('unclassified-private')
      act(() => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Download complete protected JSON')!.click())
      const text = await new Promise<string>((resolve, reject) => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result)); reader.onerror = reject; reader.readAsText(exported!) })
      const decoded = JSON.parse(text)
      expect(decoded.script.actions).toHaveLength(1500)
      expect(decoded.script.actions[1499].name).toBe('Request 1499')
      expect(text).not.toContain('private-token')
      expect(text).not.toContain('opaque-private')
      expect(text).not.toContain('embedded-private')
      expect(text).not.toContain('unclassified-private')
      expect(decoded.script.actions[1].target).toBe('https://[protected imported value]@example.test/orders')
      expect(text).toContain('[protected imported value]')
    } finally { click.mockRestore(); URL.createObjectURL = originalCreate; URL.revokeObjectURL = originalRevoke }
  })

  it('keeps credential preparation mounted while other review tabs are visible', () => {
    act(() => root.render(<ScriptImportReview title="Review" script={script} showCredentials={false} showDataReview={false} readinessMessage="Repair required before execution" secretBindingRequirements={[{ id:'cookie', path:'/actions/0/headers/Cookie', label:'Cookie', context:'request', suggested_name:'COOKIE', destination_value_preserved:true, value_group_id:'group' }]} onSecretBindingsApply={vi.fn()}><section>Source settings</section></ScriptImportReview>))
    const credentials = container.querySelector('.credentials-card')!
    expect(credentials.closest('[hidden]')).not.toBeNull()
    expect(container.textContent).toContain('Repair required before execution')
    expect(container.querySelector('section')?.textContent).toBe('Source settings')
  })

  it.each([
    ['https://user:private@example.test/path', 'https://[protected imported value]@example.test/path'],
    ['//user:private@example.test/path', '//[protected imported value]@example.test/path'],
    ['ftp://user:p%40ss@example.test/path', 'ftp://[protected imported value]@example.test/path'],
    ['https://user:private@part@example.test/path', 'https://[protected imported value]@example.test/path'],
    ['https://example.test/users/contact@example.test', 'https://example.test/users/contact@example.test'],
  ])('protects URL userinfo without binding metadata: %s', (target, expected) => {
    act(() => root.render(<ScriptImportReview title="Review" script={{ ...script, actions: [{ name: 'Call', type: 'request', target }] }} />))
    const preview = JSON.parse(container.querySelector('pre')!.textContent!)
    expect(preview.script.actions[0].target).toBe(expected)
  })

  it('keeps conversion notes informational and blockers distinct from unsupported features', () => {
    act(() => root.render(<ScriptImportReview title="Review" script={script} diagnostics={[
      {element:'headers',detail:'source_order_unavailable',severity:'info'},
      {element:'unsupported adapter',detail:'No execution adapter',severity:'warning'},
      {element:'invalid source',detail:'Cannot convert',severity:'blocker'},
    ]} />))
    expect(container.textContent).toContain('Conversion reference (1)')
    expect(container.textContent).toContain('Unsupported element')
    expect(container.textContent).toContain('Blocking conversion failure')
    expect(container.querySelector('[aria-label="View 2 unsupported import elements"]')).toBeNull()
    expect(document.activeElement).toBe(container.querySelector('.script-import-review-header'))
  })

  it('does not serialize preview data until the preview is visible and memoizes the result', () => {
    const toJSON = vi.fn(() => ({ generated: true }))
    const previewData = { toJSON }

    act(() => {
      root.render(
        <ScriptImportReview
          title="Review hidden preview"
          script={script}
          previewData={previewData}
          showPreview={false}
        />,
      )
    })
    expect(toJSON).not.toHaveBeenCalled()

    act(() => {
      root.render(
        <ScriptImportReview
          title="Review visible preview"
          script={script}
          previewData={previewData}
          showPreview
        />,
      )
    })
    expect(toJSON).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('"generated": true')

    act(() => {
      root.render(
        <ScriptImportReview
          title="Unrelated title change"
          script={script}
          previewData={previewData}
          showPreview
        />,
      )
    })
    expect(toJSON).toHaveBeenCalledTimes(1)
  })

  it('never places imported credential values in the generated preview DOM', () => {
    const credentialScript = {
      ...script,
      config: {
        base_url: 'https://api.example.test',
        http: {
          auth: {
            password: 'raw-password-should-not-render',
            token: '{{secret.API_TOKEN}}',
            session_id: 'raw-session-object',
          },
          headers: {
            Authorization: 'Bearer raw-authorization-token',
            'X-API-Key': 'raw-api-key-value',
            'idpctx-session-id': 'raw-idpctx-session',
            'X-CSRF-Token': 'raw-csrf-header',
          },
        },
      },
      actions: [{
        ...script.actions[0],
        body: 'password=body-password&safe=value&credentials[session_id]=raw-session-body {"csrf":"raw-csrf-body"} <mh:token>xml-token</mh:token> <auth:xsrfToken>raw-xsrf-xml</auth:xsrfToken>',
      }],
    } as ScriptDefinition

    act(() => {
      root.render(
        <ScriptImportReview
          title="Credential-safe preview"
          script={credentialScript}
        />,
      )
    })

    const preview = container.querySelector('.script-import-review-preview')?.textContent || ''
    expect(preview).not.toContain('raw-password-should-not-render')
    expect(preview).not.toContain('raw-authorization-token')
    expect(preview).not.toContain('raw-api-key-value')
    expect(preview).not.toContain('raw-session-object')
    expect(preview).not.toContain('raw-idpctx-session')
    expect(preview).not.toContain('raw-csrf-header')
    expect(preview).not.toContain('raw-session-body')
    expect(preview).not.toContain('raw-csrf-body')
    expect(preview).not.toContain('raw-xsrf-xml')
    expect(preview).not.toContain('body-password')
    expect(preview).not.toContain('xml-token')
    expect(preview).toContain('[protected imported value]')
    expect(preview).toContain('{{secret.API_TOKEN}}')
  })

  it('protects scalar descendants of credential wrappers while preserving logical secret references', () => {
    act(() => {
      root.render(
        <ScriptImportReview
          title="Credential wrapper preview"
          script={script}
          previewData={{
            credentials: {
              value: 'wrapper-value-canary',
              nested: {
                arbitraryCamel: 'nested-value-canary',
                reference: '{{secret.team-api.session.key}}',
                authorization: 'Bearer {{secret.team-api.token}}',
                count: 42,
              },
              entries: ['array-value-canary', '{{secret.team-api.array-key}}'],
            },
            sessionId: 'camel-session-canary',
            'idpctx-session-id': 'separator-session-canary',
            csrfToken: 'camel-csrf-canary',
            xsrf_token: 'separator-xsrf-canary',
            harmless: 'visible-non-credential-value',
          }}
        />,
      )
    })

    const preview = container.querySelector('.script-import-review-preview')?.textContent || ''
    expect(preview).not.toContain('wrapper-value-canary')
    expect(preview).not.toContain('nested-value-canary')
    expect(preview).not.toContain('array-value-canary')
    expect(preview).not.toContain('camel-session-canary')
    expect(preview).not.toContain('separator-session-canary')
    expect(preview).not.toContain('camel-csrf-canary')
    expect(preview).not.toContain('separator-xsrf-canary')
    expect(preview).toContain('{{secret.team-api.session.key}}')
    expect(preview).toContain('Bearer {{secret.team-api.token}}')
    expect(preview).toContain('{{secret.team-api.array-key}}')
    expect(preview).toContain('visible-non-credential-value')
  })

  it('protects adapter-specific descendants of semantic credential records', () => {
    act(() => {
      root.render(
        <ScriptImportReview
          title="Semantic credential preview"
          script={script}
          previewData={{
            fields: [
              { name: 'client_secret', defaultValue: 'semantic-default-canary' },
              { key: 'password', values: ['semantic-values-canary'] },
              { name: 'api_key', schema: { default: 'semantic-nested-canary' } },
              { name: 'display_name', defaultValue: 'visible-display-name' },
            ],
          }}
        />,
      )
    })

    const preview = container.querySelector('.script-import-review-preview')?.textContent || ''
    expect(preview).not.toContain('semantic-default-canary')
    expect(preview).not.toContain('semantic-values-canary')
    expect(preview).not.toContain('semantic-nested-canary')
    expect(preview).toContain('visible-display-name')
  })

  it('mirrors broad backend header governance without hiding non-sensitive header values', () => {
    act(() => {
      root.render(
        <ScriptImportReview
          title="Broad header policy preview"
          script={script}
          previewData={{
            headers: {
              'Subscription-Key': 'subscription-key-canary',
              'X-Tenant-Key': 'tenant-key-canary',
              'X-CustomToken': 'custom-token-canary',
              'X-Auth': 'auth-canary',
              'Content-Type': 'application/json; charset=utf-8',
              'X-Monkey': 'visible-monkey-value',
              'X-Request-ID': 'visible-request-id',
            },
            raw_transport: 'Authorization: Bearer x\nProxy-Authorization: Basic y\nAuthorization: Digest raw-digest-canary\nProxy-Authorization: Negotiate raw-negotiate-canary\nCookie: sid=abc; harmless=raw-cookie-canary\nX-Request-ID: visible-raw-request-id\n<credentials>xml-credential-canary</credentials>\n<authorization>xml-auth-canary</authorization>\n<session>xml-session-canary</session>\n<client:credentials>xml-qualified-canary</client:credentials>\n<Envelope><Body><credentials>xml-nested-canary</credentials></Body></Envelope>\n<password><!-- </password> -->xml-comment-canary</password>\n<password><![CDATA[ </password> ]]>xml-cdata-canary</password>\n<root><entry name="password" value="xml-semantic-value-canary"/><entry name="pass&#x77;ord" value="xml-entity-canary"/><property key="client_secret">xml-semantic-text-canary</property><password fixed="xml-sensitive-attribute-canary"/><entry name="display_name" value="visible-xml-attribute"/></root>\n<monkey>visible-xml-monkey</monkey>',
            truncated_xml: '<password>xml-truncated-canary',
            truncated_semantic_xml: '<entry name="pass&#x77;ord" value="xml-truncated-semantic-canary"',
            mismatched_xml: '<root><password>xml-mismatched-canary</root>',
            header_records: [
              { name: 'Ocp-Apim-Subscription-Key', value: 'semantic-key-canary' },
              { key: 'X-BuildToken', value: 'semantic-token-canary' },
              { name: 'Content-Type', value: 'visible-semantic-content-type' },
            ],
          }}
        />,
      )
    })

    const preview = container.querySelector('.script-import-review-preview')?.textContent || ''
    expect(preview).not.toContain('subscription-key-canary')
    expect(preview).not.toContain('tenant-key-canary')
    expect(preview).not.toContain('custom-token-canary')
    expect(preview).not.toContain('auth-canary')
    expect(preview).not.toContain('semantic-key-canary')
    expect(preview).not.toContain('semantic-token-canary')
    expect(preview).not.toContain('xml-credential-canary')
    expect(preview).not.toContain('xml-auth-canary')
    expect(preview).not.toContain('xml-session-canary')
    expect(preview).not.toContain('xml-qualified-canary')
    expect(preview).not.toContain('xml-nested-canary')
    expect(preview).not.toContain('xml-semantic-value-canary')
    expect(preview).not.toContain('xml-semantic-text-canary')
    expect(preview).not.toContain('xml-sensitive-attribute-canary')
    expect(preview).not.toContain('xml-entity-canary')
    expect(preview).not.toContain('xml-comment-canary')
    expect(preview).not.toContain('xml-cdata-canary')
    expect(preview).not.toContain('xml-truncated-canary')
    expect(preview).not.toContain('xml-truncated-semantic-canary')
    expect(preview).not.toContain('xml-mismatched-canary')
    expect(preview).not.toMatch(/Bearer x|Basic y/)
    expect(preview).not.toContain('raw-digest-canary')
    expect(preview).not.toContain('raw-negotiate-canary')
    expect(preview).not.toContain('raw-cookie-canary')
    expect(preview).toContain('Ocp-Apim-Subscription-Key')
    expect(preview).toContain('X-BuildToken')
    expect(preview).toContain('application/json; charset=utf-8')
    expect(preview).toContain('visible-monkey-value')
    expect(preview).toContain('visible-request-id')
    expect(preview).toContain('visible-semantic-content-type')
    expect(preview).toContain('visible-xml-monkey')
    expect(preview).toContain('visible-xml-attribute')
    expect(preview).toContain('visible-raw-request-id')
  })

  it('renders unsupported elements and diagnostic review sections', () => {
    act(() => {
      root.render(
        <ScriptImportReview
          reviewGeneration="render-unsupported"
          title="Review Generated Script"
          script={script}
          unsupportedElements={[
            'OpenAPI schema $ref at POST /orders [warning]: schema reference external.yaml could not be resolved',
          ]}
          reviewSections={[
            {
              key: 'import_diagnostics_warning',
              title: 'Import Diagnostics',
              severity: 'warning',
              summary: '1 import diagnostic requires review.',
              items: ['Multipart file upload needs a governed file value.'],
            },
          ]}
        />,
      )
    })

    expect(container.textContent).toContain('2 findings')
    expect(container.textContent).toContain('Import Diagnostics')
    expect(container.querySelector('.script-import-review-section')).toBeNull()
    const diagnosticButton = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent?.includes('Import Diagnostics')) as HTMLButtonElement
    act(() => diagnosticButton.click())
    const evidenceButton = Array.from(container.querySelectorAll('summary'))
      .find(summary => summary.textContent?.includes('Technical evidence and affected locations')) as HTMLElement
    act(() => evidenceButton.click())
    expect(container.textContent).toContain('Multipart file upload needs a governed file value.')
    const unsupportedButton = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent?.includes('Unsupported element')) as HTMLButtonElement
    act(() => unsupportedButton.click())
    expect(container.querySelector('.shared-import-issue-detail')?.textContent).toContain('schema reference external.yaml')
  })

  it('opens blocker and conversion-warning summaries independently', () => {
    act(() => {
      root.render(
        <ScriptImportReview
          reviewGeneration="issue-pills"
          title="Review Generated Script"
          script={script}
          warnings={['A generated placeholder needs review.']}
          reviewSections={[{
            key: 'blocked-runtime',
            title: 'Runtime prerequisite',
            severity: 'blocker',
            summary: 'A runner is required.',
            items: ['Configure an approved external runner.'],
          }]}
        />,
      )
    })

    expect(container.querySelector('.shared-import-issue-detail')?.textContent).toContain('Configure an approved external runner.')
    const warning = Array.from(container.querySelectorAll<HTMLButtonElement>('.shared-import-issue-row'))
      .find(candidate => candidate.textContent?.includes('Warnings'))!
    act(() => warning.click())
    expect(container.querySelector('.shared-import-issue-detail')?.textContent).toContain('A generated placeholder needs review.')
  })

  it('pages ten thousand modal findings and resets the stale modal for a new review generation', () => {
    const warnings = Array.from({ length: 10_000 }, (_, index) => `Finding ${String(index).padStart(5, '0')}`)
    act(() => {
      root.render(
        <ScriptImportReview
          reviewGeneration="large-finding-set"
          title="Review Generated Script"
          script={script}
          warnings={warnings}
        />,
      )
    })

    let workspace = container.querySelector<HTMLElement>('.shared-import-issue-workspace')!
    expect(workspace.querySelectorAll('.shared-import-issue-evidence section')).toHaveLength(100)
    expect(workspace.textContent).toContain('Finding 00000')
    expect(workspace.textContent).not.toContain('Finding 00100')
    expect(workspace.querySelector('.shared-import-issue-pagination')?.textContent)
      .toContain('Showing 1–100 of 10000')

    const next = Array.from(workspace.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Next 100'))!
    act(() => next.click())
    workspace = container.querySelector<HTMLElement>('.shared-import-issue-workspace')!
    expect(workspace.querySelectorAll('.shared-import-issue-evidence section')).toHaveLength(100)
    expect(workspace.textContent).toContain('Finding 00100')
    expect(workspace.querySelector('.shared-import-issue-evidence')?.textContent).not.toContain('Finding 00000')

    act(() => {
      root.render(
        <ScriptImportReview
          reviewGeneration="replacement-review"
          title="Replacement Review"
          script={script}
          warnings={['New finding']}
        />,
      )
    })
    expect(container.textContent).toContain('New finding')
    expect(container.textContent).not.toContain('Finding 00100')
  // This deliberate 10,000-finding scale fixture verifies bounded DOM output,
  // not a five-second host-performance SLA. Leave headroom for parallel CI.
  }, 15_000)

  it('renders an explicit accessible reason whenever the owner disables saving', () => {
    const onSave = vi.fn()
    act(() => {
      root.render(
        <ScriptImportReview
          reviewGeneration="disabled-reason"
          title="Review Generated Script"
          script={script}
          saveDisabled
          saveDisabledReason="Select an existing script to replace before saving."
          onSave={onSave}
        />,
      )
    })

    const save = container.querySelector<HTMLButtonElement>('.script-import-review-actions .btn-primary')!
    const describedBy = save.getAttribute('aria-describedby')
    expect(save.disabled).toBe(true)
    expect(save.title).toBe('Select an existing script to replace before saving.')
    expect(describedBy).toBeTruthy()
    expect(document.getElementById(describedBy!)?.textContent)
      .toContain('Why saving is unavailable: Select an existing script to replace before saving.')
    act(() => save.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(onSave).not.toHaveBeenCalled()
  })

  it('uses theme-aware contrast for credential-preserving session notices', async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const css = readFileSync(`${cwd}/src/components/ScriptImportReview.css`, 'utf8')
    const sessionRule = css.match(/\.script-import-review-session\s*\{([^}]*)\}/)?.[1] || ''

    expect(sessionRule).toContain('border: 1px solid var(--border);')
    expect(sessionRule).toContain('background: var(--bg-secondary);')
    expect(sessionRule).toContain('color: var(--text-primary);')
    expect(sessionRule).not.toContain('--surface-muted')
  })

  it('renders generated preview as a collapsible section before source comparison content', () => {
    act(() => {
      root.render(
        <ScriptImportReview
          reviewGeneration="render-preview"
          title="Review Generated Script"
          script={script}
          previewAside={<div className="test-source-comparison">Source Comparison</div>}
        />,
      )
    })

    const review = container.querySelector('.script-import-review')
    expect(review).not.toBeNull()
    const children = Array.from(review?.children || [])
    const preview = children.find(element => element.classList.contains('script-import-review-preview')) as HTMLDetailsElement | undefined
    const comparison = children.find(element => element.classList.contains('script-import-review-preview-aside'))
    expect(preview?.tagName).toBe('DETAILS')
    expect(preview?.open).toBe(false)
    expect(preview?.querySelector('summary h4')?.textContent).toBe('Generated Script Preview')
    expect(comparison?.textContent).toContain('Source Comparison')
    expect(preview && comparison ? children.indexOf(preview) < children.indexOf(comparison) : false).toBe(true)
    expect(container.querySelector('.script-import-review-preview-row')).toBeNull()
  })

  it('renders the generated DSL preview with the semantic JSON highlighter', () => {
    act(() => {
      root.render(<ScriptImportReview title="Review" previewTitle="DSL Preview" script={script} />)
    })

    const preview = container.querySelector<HTMLPreElement>('.script-import-review-preview pre')
    expect(preview?.dataset.language).toBe('json')
    expect(preview?.dataset.semanticMode).toBe('apex-dsl')
    expect(preview?.getAttribute('aria-label')).toBe('DSL Preview JSON')
  })

  it('caps very large generated previews', () => {
    act(() => {
      root.render(
        <ScriptImportReview
          reviewGeneration="large-preview"
          title="Review Generated Script"
          script={{
            ...script,
            actions: Array.from({ length: 5000 }, (_, index) => ({
              name: `Action ${index}`,
              type: 'request',
              method: 'GET',
              target: `/orders/${index}`,
            })),
          }}
        />,
      )
    })

    expect(container.textContent).toContain('Preview truncated for browser performance')
    const preview = container.querySelector('.script-import-review-preview pre')
    expect(preview?.textContent?.length).toBeLessThan(121000)
  })

  it('bounds text scanning and fails closed when truncation splits a credential field', () => {
    const prefix = 'x'.repeat(19_985)
    act(() => {
      root.render(
        <ScriptImportReview
          reviewGeneration="large-sensitive-string"
          title="Review Generated Script"
          script={script}
          previewData={{ raw: `${prefix}\nAuthorization: raw-boundary-canary${'y'.repeat(1_000_000)}` }}
        />,
      )
    })

    const preview = container.querySelector('.script-import-review-preview pre')?.textContent || ''
    expect(preview).not.toContain('raw-boundary-canary')
    expect(preview.length).toBeLessThan(21_000)
    expect(container.textContent).toContain('Preview truncated for browser performance')
  })

  it('redacts quoted credential fields beside identifier text after bounding scans', () => {
    act(() => root.render(<ScriptImportReview
      title="Review Generated Script"
      script={script}
      previewData={{ raw: `prefix"password":"quoted-boundary-canary"\nother'authorization'='second-boundary-canary'\n?headers%5Btoken%5D=encoded-boundary-canary\n123password=numeric-prefix-canary\ncombinedpassword=combined-prefix-canary\n%61pi_key=encoded-leading-canary\n%2570assword=double-encoded-leading-canary` }}
    />))
    const preview = container.querySelector('.script-import-review-preview pre')?.textContent || ''
    expect(preview).not.toContain('quoted-boundary-canary')
    expect(preview).not.toContain('second-boundary-canary')
    expect(preview).not.toContain('encoded-boundary-canary')
    expect(preview).not.toContain('numeric-prefix-canary')
    expect(preview).not.toContain('combined-prefix-canary')
    expect(preview).not.toContain('encoded-leading-canary')
    expect(preview).not.toContain('double-encoded-leading-canary')
  })

  it('auto-applies safe references and allows values to be configured later', async () => {
    const onSave = vi.fn()
    const onEdit = vi.fn()
    const requirement = {
      id: 'auth-header',
      path: '/actions/0/headers/Authorization',
      label: 'Create Order — Authorization header',
      context: 'Bearer credential',
      suggested_name: 'ORDER_API_TOKEN',
      replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
    }
    function Harness() {
      const [requirements, setRequirements] = useState([requirement])
      return (
        <ScriptImportReview
          reviewGeneration="safe-references"
          title="Review Generated Script"
          script={script}
          onSave={onSave}
          onEdit={onEdit}
          secretBindingRequirements={requirements}
          onSecretBindingsApply={async () => setRequirements([])}
        />
      )
    }
    await act(async () => {
      root.render(<Harness />)
    })

    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Save with 1 secret to configure')
    expect(saveButton?.disabled).toBe(false)
    const editButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Open In Visual Editor with 1 secret to configure')
    expect(editButton?.disabled).toBe(false)
    expect(container.textContent).toContain('Credential groups (1)')
    await act(async () => {
      saveButton?.click()
      await Promise.resolve()
    })
    expect(onSave).toHaveBeenCalledTimes(1)
  })

  it('keeps the durable project-secret recovery contract visible after a later script save failure', async () => {
    const requirement = {
      id: 'auth-header-with-value',
      path: '/actions/0/headers/Authorization',
      label: 'Create Order — Authorization header',
      context: 'Bearer credential',
      suggested_name: 'ORDER_API_TOKEN',
      replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
    }
    const onSave = vi.fn()

    function Harness() {
      const [requirements, setRequirements] = useState([requirement])
      const [saveError, setSaveError] = useState('')
      return (
        <>
          <ScriptImportReview
            reviewGeneration="durable-secret-retry"
            title="Review Generated Script"
            script={script}
            projectId="project-1"
            onSave={() => {
              onSave()
              setSaveError('The script save failed after credential preparation.')
            }}
            secretBindingRequirements={requirements}
            onSecretBindingsApply={async () => setRequirements([])}
          />
          {saveError && <div role="alert">{saveError}</div>}
        </>
      )
    }

    await act(async () => {
      root.render(<Harness />)
      await Promise.resolve()
      await Promise.resolve()
    })
    const valueInput = container.querySelector<HTMLInputElement>('.secret-binding-review input[type="password"]')!
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(valueInput, 'write-only-order-token')
      valueInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const firstSave = container.querySelector<HTMLButtonElement>('.script-import-review-actions .btn-primary')!
    await act(async () => {
      firstSave.click()
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1)
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('script save failed')
    const recovery = container.querySelector('.script-import-review-credential-recovery')
    expect(recovery?.textContent).toContain('remain in Project Secrets independently of script saving')
    expect(recovery?.textContent).toContain('retrying this save reuses those configured values')
    expect(recovery?.textContent).toContain('atomic-save promise on this page applies to scripts')
    expect(container.textContent).not.toContain('write-only-order-token')

    const retrySave = container.querySelector<HTMLButtonElement>('.script-import-review-actions .btn-primary')!
    await act(async () => {
      retrySave.click()
      await Promise.resolve()
    })
    expect(onSave).toHaveBeenCalledTimes(2)
    expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1)
  })

  it('keeps server-marked imported values runnable when Save or Edit selects no bindings', async () => {
    const onSave = vi.fn()
    const onEdit = vi.fn()
    const onSecretBindingsApply = vi.fn()
    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="preserve-imported-values"
          title="Review Generated Script"
          script={script}
          onSave={onSave}
          onEdit={onEdit}
          secretBindingRequirements={[{
            id: 'auth-header',
            path: '/actions/0/headers/Authorization',
            label: 'Create Order — Authorization header',
            context: 'Bearer credential',
            suggested_name: 'ORDER_API_TOKEN',
            replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
            destination_value_preserved: true,
          }]}
          onSecretBindingsApply={onSecretBindingsApply}
        />,
      )
      await Promise.resolve()
    })

    expect(container.textContent).toContain('server-confirmed imported value can remain')
    expect(container.textContent).toContain('0 bindings need setup')
    const saveButton = container.querySelector<HTMLButtonElement>('.script-import-review-actions .btn-primary')!
    const editButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Open In Visual Editor')!

    await act(async () => {
      saveButton.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(onSave).toHaveBeenCalledTimes(1)
    expect(onSecretBindingsApply).not.toHaveBeenCalled()

    await act(async () => {
      editButton.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(onEdit).toHaveBeenCalledTimes(1)
    expect(onSecretBindingsApply).not.toHaveBeenCalled()
  })

  it('blocks Save and Edit after a credential-preserving import draft expires', async () => {
    const onSave = vi.fn()
    const onEdit = vi.fn()
    const onParameterizationApplied = vi.fn()
    const parameterization: Parameterization = {
      suggestions: [{
        id: 'expired-order-id',
        kind: 'data_param',
        var_name: 'ORDER_ID',
        replacement: '{{param.ORDER_ID}}',
        confidence: 0.95,
        rule_id: 'test',
        rationale: 'Repeated order id',
        auto_apply: true,
        high_risk: false,
      }],
      summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
    }
    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="expired-import-draft"
          title="Review Generated Script"
          script={script}
          importDraftExpiresAt="2000-01-01T00:00:00Z"
          onSave={onSave}
          onEdit={onEdit}
          parameterization={parameterization}
          onParameterizationApplied={onParameterizationApplied}
          secretBindingRequirements={[{
            id: 'auth-header',
            path: '/actions/0/headers/Authorization',
            label: 'Create Order — Authorization header',
            context: 'Bearer credential',
            suggested_name: 'ORDER_API_TOKEN',
            replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
            destination_value_preserved: true,
          }]}
          onSecretBindingsApply={vi.fn()}
        />,
      )
      await Promise.resolve()
    })

    expect(container.querySelector('[role="alert"]')?.textContent).toContain('credential-preserving import draft has expired')
    const saveButton = container.querySelector<HTMLButtonElement>('.script-import-review-actions .btn-primary')!
    const editButton = container.querySelector<HTMLButtonElement>('.script-import-review-actions .btn-secondary')!
    expect(saveButton.disabled).toBe(true)
    expect(editButton.disabled).toBe(true)
    expect(saveButton.title).toContain('Re-run the import')
    expect(editButton.title).toContain('Re-run the import')
    const applyParameterization = container.querySelector<HTMLButtonElement>('.paramize-review button.btn-primary')!
    expect(applyParameterization.disabled).toBe(true)
    await act(async () => {
      applyParameterization.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(apiMock.applyImportParameterization).not.toHaveBeenCalled()
    expect(onParameterizationApplied).not.toHaveBeenCalled()
  })

  it('applies an explicit project-secret selection for a server-marked import before saving', async () => {
    const onSave = vi.fn()
    const onSecretBindingsApply = vi.fn()
    const requirement = {
      id: 'auth-header',
      path: '/actions/0/headers/Authorization',
      label: 'Create Order — Authorization header',
      context: 'Bearer credential',
      suggested_name: 'ORDER_API_TOKEN',
      replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
      destination_value_preserved: true,
    }
    function Harness() {
      const [requirements, setRequirements] = useState([requirement])
      return (
        <ScriptImportReview
          reviewGeneration="replace-imported-value"
          title="Review Generated Script"
          script={script}
          onSave={onSave}
          secretBindingRequirements={requirements}
          onSecretBindingsApply={async bindings => {
            onSecretBindingsApply(bindings)
            setRequirements([])
          }}
        />
      )
    }
    await act(async () => {
      root.render(<Harness />)
      await Promise.resolve()
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit credentials')!.click()
      await Promise.resolve()
    })
    await act(async () => {
      const destination = container.querySelector<HTMLSelectElement>('.secret-binding-review select[id$="-destination"]')!
      destination.value = 'project-secret'
      destination.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const nameInput = container.querySelector<HTMLInputElement>('.secret-binding-review input[type="text"]')!
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(nameInput, 'EXISTING_ORDER_TOKEN')
      nameInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Apply credentials')!.click()
      await Promise.resolve()
    })
    const saveButton = container.querySelector<HTMLButtonElement>('.script-import-review-actions .btn-primary')!
    await act(async () => {
      saveButton.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(onSecretBindingsApply).toHaveBeenCalledWith([{
      requirement_id: 'auth-header',
      secret_name: 'EXISTING_ORDER_TOKEN',
    }])
    expect(onSave).toHaveBeenCalledTimes(1)
  })

  it('disables save, edit, and credential controls while parameterization is applying', async () => {
    apiMock.applyImportParameterization.mockImplementationOnce(() => new Promise(() => {}))
    const parameterization: Parameterization = {
      suggestions: [{
        id: 'parameterize-order-id',
        kind: 'data_param',
        var_name: 'ORDER_ID',
        replacement: '{{var.ORDER_ID}}',
        confidence: 0.95,
        rule_id: 'test',
        rationale: 'Repeated order id',
        auto_apply: true,
        high_risk: false,
      }],
      summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
    }
    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="parameterization-pending"
          onSettingsApplied={() => {}}
          runtimeContent={<CategoryActions category="HTTP" />}
          title="Review Generated Script"
          script={script}
          parameterization={parameterization}
          onParameterizationApplied={() => {}}
          onSave={() => {}}
          onEdit={() => {}}
          projectId="project-1"
          secretBindingRequirements={[{
            id: 'auth',
            path: '/actions/0/headers/Authorization',
            label: 'Authorization',
            context: 'Bearer credential',
            suggested_name: 'ORDER_API_TOKEN',
            replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
          }]}
          onSecretBindingsApply={async () => {}}
        />,
      )
    })
    const apply = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Apply Selected'))
    await act(async () => {
      apply?.click()
      await Promise.resolve()
    })

    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save with'))
    const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Open In Visual Editor'))
    expect(save?.disabled).toBe(true)
    expect(edit?.disabled).toBe(true)
    expect(container.querySelector<HTMLInputElement>('.secret-binding-review input[type="text"]')?.disabled).toBe(true)
    expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit HTTP')?.disabled).toBe(true)
  })

  it('disables parameterization while a credential-backed Save is in progress', async () => {
    const parameterization: Parameterization = {
      suggestions: [{
        id: 'parameterize-order-id',
        kind: 'data_param',
        var_name: 'ORDER_ID',
        replacement: '{{var.ORDER_ID}}',
        confidence: 0.95,
        rule_id: 'test',
        rationale: 'Repeated order id',
        auto_apply: true,
        high_risk: false,
      }],
      summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
    }
    const onSecretBindingsApply = vi.fn(() => new Promise<void>(() => {}))
    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="save-pending"
          title="Review Generated Script"
          script={script}
          parameterization={parameterization}
          onParameterizationApplied={() => {}}
          onSave={vi.fn()}
          projectId="project-1"
          secretBindingRequirements={[{
            id: 'auth',
            path: '/actions/0/headers/Authorization',
            label: 'Authorization',
            context: 'Bearer credential',
            suggested_name: 'ORDER_API_TOKEN',
            replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
          }]}
          onSecretBindingsApply={onSecretBindingsApply}
        />,
      )
      await Promise.resolve()
    })

    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save with'))!
    await act(async () => {
      save.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(onSecretBindingsApply).toHaveBeenCalledTimes(1)
    expect(container.querySelector<HTMLButtonElement>('.paramize-review button.btn-primary')?.disabled).toBe(true)
    expect(container.querySelector<HTMLInputElement>('.paramize-review input[type="checkbox"]')?.disabled).toBe(true)
  })

  it('preserves edited credential names and write-only values after applying parameterization', async () => {
    const onParameterizationApplied = vi.fn()
    const parameterization: Parameterization = {
      suggestions: [{
        id: 'parameterize-order-id',
        kind: 'data_param',
        var_name: 'ORDER_ID',
        replacement: '{{var.ORDER_ID}}',
        confidence: 0.95,
        rule_id: 'test',
        rationale: 'Repeated order id',
        auto_apply: true,
        high_risk: false,
      }],
      summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
    }
    apiMock.applyImportParameterization.mockResolvedValue({
      script: { ...script, config: { ...script.config, base_url: '{{param.BASE_URL}}' } },
      diffs: [{ kind: 'value_replaced', path: '/config/base_url', suggestion_id: 'parameterize-order-id' }],
      applied_count: 1,
      import_draft_expires_at: '2099-07-26T13:00:00Z',
    })

    function Harness() {
      const [generation, setGeneration] = useState(1)
      const [reviewedScript, setReviewedScript] = useState<ScriptDefinition>(script)
      return (
        <ScriptImportReview
          reviewGeneration={generation}
          title="Review Generated Script"
          script={reviewedScript}
          parameterization={parameterization}
          onParameterizationApplied={(nextScript, diffs, selectedIds, importDraftExpiresAt) => {
            onParameterizationApplied(nextScript, diffs, selectedIds, importDraftExpiresAt)
            setReviewedScript(nextScript)
            setGeneration(current => current + 1)
          }}
          projectId="project-1"
          secretBindingRequirements={[{
            id: 'auth',
            path: '/actions/0/headers/Authorization',
            label: 'Authorization',
            context: 'Bearer credential',
            suggested_name: 'ORDER_API_TOKEN',
            replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
          }]}
          onSecretBindingsApply={vi.fn()}
        />
      )
    }

    await act(async () => {
      root.render(<Harness />)
      await Promise.resolve()
    })
    const nameInput = container.querySelector<HTMLInputElement>('.secret-binding-review input[type="text"]')!
    const valueInput = container.querySelector<HTMLInputElement>('.secret-binding-review input[type="password"]')!
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(nameInput, 'CUSTOM_ORDER_TOKEN')
      nameInput.dispatchEvent(new Event('input', { bubbles: true }))
      setter?.call(valueInput, 'write-only-value')
      valueInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    await act(async () => {
      container.querySelector<HTMLButtonElement>('.paramize-review button.btn-primary')?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.querySelector<HTMLInputElement>('.secret-binding-review input[type="text"]')?.value).toBe('CUSTOM_ORDER_TOKEN')
    expect(container.querySelector<HTMLInputElement>('.secret-binding-review input[type="password"]')?.value).toBe('write-only-value')
    expect(onParameterizationApplied).toHaveBeenCalledWith(
      expect.any(Object),
      expect.any(Array),
      ['parameterize-order-id'],
      '2099-07-26T13:00:00Z',
    )
  })

  it('invalidates a pending credential action when a newer import result is rendered', async () => {
    const onSave = vi.fn()
    const oldApply = vi.fn().mockResolvedValue(undefined)
    let resolveCommitProbe: ((value: unknown) => void) | undefined
    const oldRequirement = {
      id: 'old-auth',
      path: '/actions/0/headers/Authorization',
      label: 'Old authorization',
      context: 'Old import credential',
      suggested_name: 'OLD_TOKEN',
      replacement: '{{secret.OLD_TOKEN}}',
    }
    const newRequirement = {
      ...oldRequirement,
      id: 'new-auth',
      label: 'New authorization',
      context: 'New import credential',
      suggested_name: 'NEW_TOKEN',
      replacement: '{{secret.NEW_TOKEN}}',
    }

    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="old-import"
          title="Old import"
          script={{ ...script, name: 'Old draft' }}
          projectId="project-1"
          onSave={onSave}
          secretBindingRequirements={[oldRequirement]}
          onSecretBindingsApply={oldApply}
        />,
      )
      await Promise.resolve()
    })
    apiMock.getProjectSecretStatuses.mockReturnValueOnce(new Promise(resolve => {
      resolveCommitProbe = resolve
    }))
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Save with 1 secret to configure')!
    act(() => saveButton.click())
    await act(async () => Promise.resolve())
    expect(oldApply).not.toHaveBeenCalled()

    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="new-import"
          title="New import"
          script={{ ...script, name: 'New draft', config: { base_url: 'https://new.example.test' } }}
          projectId="project-1"
          onSave={onSave}
          secretBindingRequirements={[newRequirement]}
          onSecretBindingsApply={vi.fn()}
        />,
      )
    })
    await act(async () => {
      resolveCommitProbe?.({
        project_id: 'project-1',
        capabilities: { provider: 'database', writable: true, create_supported: true },
        secrets: [],
      })
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(oldApply).not.toHaveBeenCalled()
    expect(onSave).not.toHaveBeenCalled()
    expect(container.textContent).toContain('New import')
    expect(container.textContent).toContain('{{secret.NEW_TOKEN}}')
    const currentSaveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Save with 1 secret to configure')
    expect(currentSaveButton?.disabled).toBe(false)
  })

  it('does not carry an applying Save request into a newer requirements-free generation', async () => {
    const onSave = vi.fn()
    let resolveApply: (() => void) | undefined
    const oldApply = vi.fn(() => new Promise<void>(resolve => {
      resolveApply = resolve
    }))
    const requirement = {
      id: 'old-auth',
      path: '/actions/0/headers/Authorization',
      label: 'Old authorization',
      context: 'Old import credential',
      suggested_name: 'OLD_TOKEN',
      replacement: '{{secret.OLD_TOKEN}}',
    }

    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="generation-a"
          title="Old import"
          script={{ ...script, name: 'Old draft' }}
          projectId="project-1"
          onSave={onSave}
          secretBindingRequirements={[requirement]}
          onSecretBindingsApply={oldApply}
        />,
      )
      await Promise.resolve()
    })
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Save with 1 secret to configure')!
    act(() => saveButton.click())
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(oldApply).toHaveBeenCalledTimes(1)

    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="generation-b"
          title="New import"
          script={{ ...script, name: 'New safe draft' }}
          projectId="project-1"
          onSave={onSave}
          secretBindingRequirements={[]}
          onSecretBindingsApply={vi.fn()}
        />,
      )
      resolveApply?.()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(onSave).not.toHaveBeenCalled()
    expect(container.textContent).toContain('New safe draft')
  })

  it('preserves scoped action wording while reporting deferred credentials', async () => {
    const onCredentialsPreparedChange = vi.fn()
    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="scoped-wording"
          title="Review Generated Scripts"
          script={script}
          saveLabel="Save All 3 Scripts"
          editLabel="Edit Imported Scripts"
          onSave={vi.fn()}
          onEdit={vi.fn()}
          secretBindingRequirements={[{
            id: 'auth-header',
            path: '/actions/0/headers/Authorization',
            label: 'Create Order — Authorization header',
            context: 'Bearer credential',
            suggested_name: 'ORDER_API_TOKEN',
            replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
          }]}
          onSecretBindingsApply={vi.fn()}
          onCredentialsPreparedChange={onCredentialsPreparedChange}
        />,
      )
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Save All 3 Scripts with 1 secret to configure')
    expect(container.textContent).toContain('Edit Imported Scripts with 1 secret to configure')
    expect(onCredentialsPreparedChange).toHaveBeenLastCalledWith({
      configureLaterCount: 1,
      selectedBindingCount: 1,
      valid: true,
    })
  })

  it('filters scoped parameterization suggestions to the active script', () => {
    const parameterization: Parameterization = {
      suggestions: [
        {
          id: 'pz_kafka',
          kind: 'data_param',
          script_name: 'Orders Kafka',
          var_name: 'kafka_token',
          replacement: '{{param.kafka_token}}',
          literal_value: 'kafka-123',
          sample_value: 'kafka-123',
          confidence: 0.8,
          rule_id: 'test.kafka',
          rationale: 'Kafka script value',
          auto_apply: false,
          high_risk: false,
        },
        {
          id: 'pz_jms',
          kind: 'data_param',
          script_name: 'Orders JMS',
          var_name: 'jms_token',
          replacement: '{{param.jms_token}}',
          literal_value: 'jms-123',
          sample_value: 'jms-123',
          confidence: 0.8,
          rule_id: 'test.jms',
          rationale: 'JMS script value',
          auto_apply: false,
          high_risk: false,
        },
      ],
      summary: {
        total: 2,
        by_kind: { data_param: 2 },
        auto_apply_count: 0,
        review_count: 2,
        high_risk_count: 0,
        pre_applied_count: 0,
      },
    }

    act(() => {
      root.render(
        <ScriptImportReview
          reviewGeneration="parameterization"
          title="Review Generated Script"
          script={{ ...script, name: 'Orders JMS' }}
          parameterization={parameterization}
          onParameterizationApplied={vi.fn()}
        />,
      )
    })

    expect(container.textContent).toContain('jms_token')
    expect(container.textContent).not.toContain('kafka_token')
  })
})

it('navigates compact DSL by section and action while protecting explicit credential paths', async () => {
  const container = document.createElement('div'); const root = createRoot(container)
  const draft = { ...script, actions: [{ name: 'Opaque request', type: 'request', target: '/sample', body: 'private-body-value' }] }
  try {
    await act(async () => root.render(<ScriptImportReview title="Review" compactReview script={draft} secretBindingRequirements={[{ path: '/actions/0/body', label: 'Body', suggested_name: 'BODY' } as never]} />))
    const jump = Array.from(container.querySelectorAll('select')).find(select => select.parentElement?.textContent?.startsWith('Jump to action'))!
    await act(async () => { jump.value = 'actions[0]'; jump.dispatchEvent(new Event('change', { bubbles: true })) })
    const code = container.querySelector('pre')!
    expect(code.textContent).toContain('Opaque request')
    expect(code.textContent).not.toContain('private-body-value')
    expect(code.textContent).toContain('[protected imported value]')
    const section = Array.from(container.querySelectorAll('select')).find(select => select.parentElement?.textContent?.startsWith('Section'))!
    await act(async () => { section.value = 'config'; section.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(container.querySelector('pre')?.textContent).toContain('base_url')
    expect(container.textContent).toContain('not an executable export')
  } finally { await act(async () => root.unmount()) }
})
