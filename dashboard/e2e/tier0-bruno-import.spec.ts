import { expect, test, type Response } from '@playwright/test'

import { createWorkspace, scopeQuery } from './support/workspace'

type ZipMember = { name: string; content: string }

function crc32(input: Buffer): number {
  let crc = 0xffffffff
  for (const byte of input) {
    crc ^= byte
    for (let bit = 0; bit < 8; bit += 1) {
      crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0)
    }
  }
  return (crc ^ 0xffffffff) >>> 0
}

function storedZip(members: ZipMember[]): Buffer {
  const locals: Buffer[] = []
  const centrals: Buffer[] = []
  let offset = 0
  for (const member of members) {
    const name = Buffer.from(member.name, 'utf8')
    const content = Buffer.from(member.content, 'utf8')
    const checksum = crc32(content)
    const local = Buffer.alloc(30 + name.length + content.length)
    local.writeUInt32LE(0x04034b50, 0)
    local.writeUInt16LE(20, 4)
    local.writeUInt16LE(0x0800, 6)
    local.writeUInt32LE(checksum, 14)
    local.writeUInt32LE(content.length, 18)
    local.writeUInt32LE(content.length, 22)
    local.writeUInt16LE(name.length, 26)
    name.copy(local, 30)
    content.copy(local, 30 + name.length)
    locals.push(local)

    const central = Buffer.alloc(46 + name.length)
    central.writeUInt32LE(0x02014b50, 0)
    central.writeUInt16LE(20, 4)
    central.writeUInt16LE(20, 6)
    central.writeUInt16LE(0x0800, 8)
    central.writeUInt32LE(checksum, 16)
    central.writeUInt32LE(content.length, 20)
    central.writeUInt32LE(content.length, 24)
    central.writeUInt16LE(name.length, 28)
    central.writeUInt32LE(offset, 42)
    name.copy(central, 46)
    centrals.push(central)
    offset += local.length
  }
  const centralDirectory = Buffer.concat(centrals)
  const end = Buffer.alloc(22)
  end.writeUInt32LE(0x06054b50, 0)
  end.writeUInt16LE(members.length, 8)
  end.writeUInt16LE(members.length, 10)
  end.writeUInt32LE(centralDirectory.length, 12)
  end.writeUInt32LE(offset, 16)
  return Buffer.concat([...locals, centralDirectory, end])
}

function isImportResponse(response: Response, endpoint: string): boolean {
  const url = new URL(response.url())
  return response.request().method() === 'POST' && url.pathname === endpoint
}

test('[U23] Bruno native collection inspection, acknowledgements, and atomic save work on the host platform', async ({ page, request }) => {
  test.setTimeout(120_000)
  const workspace = await createWorkspace(request)
  const archive = storedZip([
    {
      name: 'CTA_Collection/opencollection.yml',
      content: 'opencollection: 1.0.0\ninfo: {name: Cross-platform CTA, uid: cross-platform-cta}\n',
    },
    {
      name: 'CTA_Collection/CTA/UnsupportedHook.yml',
      content: `info: {name: UnsupportedHook, uid: unsupported-hook, type: http, seq: 1}
http:
  method: GET
  url: https://example.test/hook
runtime:
  scripts:
    - type: after-response
      code: |
        console.log("requires review");
`,
    },
    {
      name: 'CTA_Collection/CTA/UnresolvedHeader.yml',
      content: `info: {name: UnresolvedHeader, uid: unresolved-header, type: http, seq: 2}
http:
  method: GET
  url: https://example.test/header
  headers:
    - {name: X-CDF-AUTH-TOKEN, value: "Bearer {{authToken}}"}
`,
    },
  ])

  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  await page.getByRole('button', { name: 'Import', exact: true }).click()
  await page.getByRole('button', { name: /Bruno Collection/ }).click()
  const controls = page.getByRole('group', { name: 'Script import controls' })
  await controls.locator('input[type="file"]').first().setInputFiles({
    name: 'CTA_Collection.zip',
    mimeType: 'application/zip',
    buffer: archive,
  })
  await expect(controls.getByRole('heading', { level: 2, name: 'Import Bruno Collection' })).toBeVisible()

  await controls.getByRole('textbox', { name: 'New script folder name' }).fill('Cross-platform CTA')
  await controls.getByRole('button', { name: 'Create and select folder' }).click()
  await expect(controls.getByText(/Created and selected script folder/)).toBeVisible()

  const inspectionPromise = page.waitForResponse(response => isImportResponse(response, '/api/v1/scripts/import/bruno/inspect'))
  await controls.getByRole('button', { name: 'Inspect source' }).click()
  const inspection = await inspectionPromise
  expect(inspection.ok(), await inspection.text()).toBeTruthy()
  await expect(controls.getByText(/2 API requests found.*2 native scripts/).first()).toBeVisible()

  await controls.getByRole('tab', { name: 'Import APIs', exact: true }).click()
  const acknowledgements = controls.getByRole('checkbox', { name: /^Acknowledge / })
  await expect(acknowledgements).toHaveCount(2)
  for (let index = 0; index < 2; index += 1) await acknowledgements.nth(index).check()

  const savePromise = page.waitForResponse(response => isImportResponse(response, '/api/v1/scripts/import/bruno/commit'))
  await controls.getByRole('button', { name: /Save 2 native scripts/ }).click()
  const save = await savePromise
  expect(save.ok(), await save.text()).toBeTruthy()

  const storedResponse = await request.get(`/api/v1/scripts?project_id=${workspace.projectId}`)
  expect(storedResponse.ok(), await storedResponse.text()).toBeTruthy()
  const stored = await storedResponse.json() as { scripts: Array<{ name: string; folder_id?: string }> }
  expect(stored.scripts.map(script => script.name).sort()).toEqual(['UnresolvedHeader', 'UnsupportedHook'])
  expect(new Set(stored.scripts.map(script => script.folder_id)).size).toBe(1)
  expect(stored.scripts[0]?.folder_id).toBeTruthy()
})
