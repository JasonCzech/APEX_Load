import fs from 'node:fs'
import path from 'node:path'
import type { Page } from '@playwright/test'
import { canonicalImportDraft } from './importResponse'
import { expect, mutation, login } from './workflow'
import { createWorkspace, scopeQuery } from './workspace'

// Shared by the LoadRunner archive/folder upload specs.
export async function openLoadRunnerImport(page: Page, browserOrigin?: string) {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  if (browserOrigin && process.env.APEXLOAD_E2E_PROFILE === 'local-auth') {
    // Login and API setup use the configured loopback URL. Give the same
    // session to the machine-name origin without making it a secure context.
    const cookies = await page.context().cookies(page.url())
    await page.context().addCookies(cookies.map(cookie => ({
      ...cookie,
      domain: new URL(browserOrigin).hostname,
    })))
  }
  await page.goto(`${browserOrigin || ''}/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('tab', { name: 'Import', exact: true }).click()
  await page.getByRole('button', { name: /LoadRunner Script Archive/ }).click()
}

export async function validateQueuedFolder(page: Page, expectedName = 'VuGen24Real') {
  const validated = mutation(page, 'POST', '/api/v1/scripts/import/enterprise-script-archive/validate')
  await page.getByRole('button', { name: 'Validate Script', exact: true }).click()
  const response = await validated
  expect(response.ok()).toBeTruthy()
  const result = await response.json()
  expect(canonicalImportDraft(result).name).toBe(expectedName)
  await expect(page.getByRole('tab', { name: /^Review(,|$)/ })).toBeVisible()
}

async function chooseUSZArchive(page: Page, name: string) {
  const chooser = page.waitForEvent('filechooser')
  await page.locator('.lr-dropzone').getByRole('button', { name: 'browse for archives', exact: true }).click()
  await (await chooser).setFiles({
    name,
    // Windows does not necessarily associate VuGen's extension with a MIME type.
    mimeType: '',
    buffer: fs.readFileSync(path.join(process.cwd(), 'e2e/fixtures/transaction-labels.zip')),
  })
  await expect(page.getByText('1 archive queued', { exact: true }).first()).toBeVisible()
  await expect(page.locator('.lr-file-row strong')).toHaveText(name)
  await expect(page.locator('input[type="file"][accept=".zip,.usz"]')).toHaveValue('')
}

export async function checkUSZPickerAndReselection(page: Page) {
  for (const name of ['windows-vugen.usz', 'WINDOWS-VUGEN.USZ']) {
    await chooseUSZArchive(page, name)
    await page.locator('.lr-dropzone').getByRole('button', { name: 'Clear All', exact: true }).click()
    await expect(page.locator('.lr-file-row')).toHaveCount(0)
    await chooseUSZArchive(page, name)
    if (name.endsWith('.usz')) {
      await page.locator('.lr-dropzone').getByRole('button', { name: 'Clear All', exact: true }).click()
    }
  }
  await validateQueuedFolder(page, 'Labels')
}
