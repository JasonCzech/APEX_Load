// @vitest-environment jsdom

import { describe, expect, it, vi } from 'vitest'

import {
  filesContainNativeBrunoSource,
  nativeImportTargetForFile,
  nativeImportTargetForFileName,
  resolveNativeImportRequest,
} from './nativeImportRouting'

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
    await expect(nativeImportTargetForFile(attachedStyleRequest)).resolves.toEqual({ launcher: 'bruno', mode: 'native_requests' })
  })

  it('keeps explicit OpenAPI documents in the OpenAPI importer', async () => {
    const openapi = new File(['openapi: 3.1.0\npaths:\n  /orders:\n    get: {}\n'], 'orders.yml', { type: 'application/yaml' })
    await expect(nativeImportTargetForFile(openapi)).resolves.toEqual({ launcher: 'openapi' })
  })

  it('opens an importer chooser for genuinely ambiguous YAML', async () => {
    const ambiguous = new File(['title: Example\nversion: 1\n'], 'example.yml', { type: 'application/yaml' })
    await expect(nativeImportTargetForFile(ambiguous)).resolves.toEqual({ launcher: 'yaml_chooser' })
  })

  it('classifies every dropped file before choosing a folder importer', async () => {
    const incidental = new File(['metadata'], '.DS_Store', { type: 'application/octet-stream' })
    const folder = new File(['meta:\n  name: Services\n'], 'folder.yml', { type: 'application/yaml' })
    const request = new File(['info:\n  name: Get orders\n  type: http\nhttp:\n  method: GET\n  url: /orders\n'], 'get-orders.yml', { type: 'application/yaml' })

    await expect(resolveNativeImportRequest(
      [incidental, folder, request],
      7,
      requestID => requestID === 7,
    )).resolves.toEqual({
      id: 7,
      files: [incidental, folder, request],
      launcher: 'bruno',
      mode: 'native_requests',
    })
  })

  it('recognizes OpenCollection metadata anywhere in a selected file set', async () => {
    const ordinaryYAML = new File(['title: unrelated\n'], 'first.yml', { type: 'application/yaml' })
    const openCollection = new File(['meta:\n  name: Collection\n'], 'OpenCollection.yaml', { type: 'application/yaml' })

    await expect(filesContainNativeBrunoSource([ordinaryYAML, openCollection])).resolves.toBe(true)
    await expect(resolveNativeImportRequest(
      [ordinaryYAML, openCollection],
      9,
      requestID => requestID === 9,
    )).resolves.toMatchObject({
      launcher: 'bruno',
      mode: 'native_requests',
    })
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

  it('sniffs JavaScript-family files before falling back to the k6 importer', async () => {
    const devWeb = new File(['load.initialize(() => {}); load.WebRequest({ url: "/orders" });'], 'orders.js')
    const playwright = new File(["import { test } from '@playwright/test'; test('orders', async ({ page }) => page.goto('/orders'));"], 'orders.ts')
    const k6 = new File(["import http from 'k6/http'; export default () => http.get('https://example.com');"], 'load.js')
    const ambiguous = new File(['console.log("plain JavaScript")'], 'plain.js')

    await expect(nativeImportTargetForFile(devWeb)).resolves.toEqual({ launcher: 'devweb' })
    await expect(nativeImportTargetForFile(playwright)).resolves.toEqual({ launcher: 'recorder_export' })
    await expect(nativeImportTargetForFile(k6)).resolves.toEqual({ launcher: 'k6' })
    await expect(nativeImportTargetForFile(ambiguous)).resolves.toEqual({ launcher: 'k6' })
  })

  it('routes Chrome DevTools Recorder JSON to the recorder importer instead of OpenAPI', async () => {
    const recording = new File([JSON.stringify({
      title: 'Checkout',
      steps: [
        { type: 'setViewport', width: 1200, height: 800 },
        { type: 'navigate', url: 'https://example.com/checkout' },
        { type: 'click', selectors: [['#submit'], ['xpath///*[@id="submit"]']], offsetX: 4, offsetY: 6 },
      ],
    })], 'checkout-recording.json', { type: 'application/json' })
    const openapi = new File([JSON.stringify({ openapi: '3.1.0', paths: { '/orders': { get: {} } } })], 'orders.json', { type: 'application/json' })

    await expect(nativeImportTargetForFile(recording)).resolves.toEqual({ launcher: 'recorder_export' })
    await expect(nativeImportTargetForFile(openapi)).resolves.toEqual({ launcher: 'openapi' })
  })
})
