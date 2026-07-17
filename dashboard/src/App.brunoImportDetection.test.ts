// @vitest-environment jsdom

import { describe, expect, it, vi } from 'vitest'

import { nativeImportTargetForFile, nativeImportTargetForFileName, resolveNativeImportRequest } from './nativeImportRouting'

describe('Bruno source auto-detection', () => {
  it('routes classic and explicitly named Bruno artifacts to the Bruno importer', () => {
    expect(nativeImportTargetForFileName('collection.bru')).toEqual({ launcher: 'bruno' })
    expect(nativeImportTargetForFileName('checkout.bruno.zip')).toEqual({ launcher: 'bruno' })
  })

  it('checks Bruno YAML markers before generic OpenAPI routing', async () => {
    const attachedStyleRequest = new File([`info:
  name: ExecuteUnifiedCreditPolicyCheck_v246
  type: http
http:
  method: POST
  url: "{{env}}/Services/ExecuteUnifiedCreditPolicyCheck.jws"
runtime:
  scripts:
    - type: after-response
      code: expect(res.getStatus()).to.equal(500);
`], 'ExecuteUnifiedCreditPolicyCheck_v246.yml', { type: 'application/yaml' })
    await expect(nativeImportTargetForFile(attachedStyleRequest)).resolves.toEqual({ launcher: 'bruno' })
  })

  it('keeps explicit OpenAPI documents in the OpenAPI importer', async () => {
    const openapi = new File(['openapi: 3.1.0\npaths:\n  /orders:\n    get: {}\n'], 'orders.yml', { type: 'application/yaml' })
    await expect(nativeImportTargetForFile(openapi)).resolves.toEqual({ launcher: 'openapi' })
  })

  it('opens an importer chooser for genuinely ambiguous YAML', async () => {
    const ambiguous = new File(['title: Example\nversion: 1\n'], 'example.yml', { type: 'application/yaml' })
    await expect(nativeImportTargetForFile(ambiguous)).resolves.toEqual({ launcher: 'yaml_chooser' })
  })

  it('drops an older file classification that resolves after a newer request', async () => {
    let resolveText: ((value: string) => void) | undefined
    const slowText = new Promise<string>(resolve => { resolveText = resolve })
    const slowYAML = new File(['pending'], 'slow.yml', { type: 'application/yaml' })
    vi.spyOn(slowYAML, 'slice').mockReturnValue({ text: () => slowText } as Blob)
    const jmeter = new File(['<jmeterTestPlan />'], 'latest.jmx', { type: 'application/xml' })
    let currentRequest = 1

    const older = resolveNativeImportRequest([slowYAML], 1, requestID => requestID === currentRequest)
    currentRequest = 2
    await expect(resolveNativeImportRequest([jmeter], 2, requestID => requestID === currentRequest)).resolves.toEqual({
      id: 2,
      files: [jmeter],
      launcher: 'jmeter',
    })
    resolveText?.('info:\n  type: http\n')
    await expect(older).resolves.toBeNull()
  })
})
