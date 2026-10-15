import fs from 'node:fs'
import path from 'node:path'
import type { Page } from '@playwright/test'
import { test, expect, mutation, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

const folder = path.join(process.cwd(), '../pkg/convert/enterprisearchive/testdata/golden_web_http_html_vugen24')

async function openLoadRunnerImport(page: Page) {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('tab', { name: 'Import', exact: true }).click()
  await page.getByRole('button', { name: /LoadRunner Script Archive/ }).click()
}

async function validateQueuedFolder(page: Page) {
  const validated = mutation(page, 'POST', '/api/v1/scripts/import/enterprise-script-archive/validate')
  await page.getByRole('button', { name: 'Validate Script', exact: true }).click()
  const response = await validated
  expect(response.ok()).toBeTruthy()
  const result = await response.json()
  expect(result.script?.name).toBe('VuGen24Real')
  await expect(page.getByRole('tab', { name: /^Review(,|$)/ })).toBeVisible()
}

test('imports an unpacked VuGen project folder chosen with the drop zone picker', async ({ page }) => {
  await openLoadRunnerImport(page)
  // The keyboard and desktop-app path: a real directory picker, no dragging.
  const chooser = page.waitForEvent('filechooser')
  await page.locator('.lr-dropzone').getByRole('button', { name: 'choose a VuGen folder', exact: true }).click()
  await (await chooser).setFiles(folder)
  await expect(page.getByText('1 source queued').first()).toBeVisible()
  await validateQueuedFolder(page)
})

test('imports an unpacked VuGen project folder dropped on the drop zone', async ({ page }) => {
  await openLoadRunnerImport(page)
  const folderFiles = fs.readdirSync(folder).map(name => ({ name, data: fs.readFileSync(path.join(folder, name)).toString('base64') }))
  // Browsers expose a dropped folder only through FileSystem entries, which a
  // synthetic drop has to stand in for.
  await page.locator('.lr-dropzone').evaluate((zone, { name, files }) => {
    const fileEntry = (file: { name: string; data: string }) => ({
      isFile: true,
      isDirectory: false,
      name: file.name,
      file: (resolve: (value: File) => void) => resolve(new File([Uint8Array.from(atob(file.data), char => char.charCodeAt(0))], file.name)),
    })
    const folderEntry = {
      isFile: false,
      isDirectory: true,
      name,
      createReader: () => {
        let read = false
        return { readEntries: (resolve: (entries: unknown[]) => void) => { resolve(read ? [] : files.map(fileEntry)); read = true } }
      },
    }
    const drop = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperty(drop, 'dataTransfer', {
      value: { items: [{ kind: 'file', webkitGetAsEntry: () => folderEntry, getAsFile: () => null }], files: [] },
    })
    zone.dispatchEvent(drop)
  }, { name: path.basename(folder), files: folderFiles })
  await expect(page.getByText('1 source queued').first()).toBeVisible()
  // Folders are added from the drop zone itself, by drop or by its picker.
  await expect(page.locator('.lr-dropzone').getByRole('button', { name: 'Add folder', exact: true })).toBeVisible()

  await validateQueuedFolder(page)
})
