import { expect, test } from '@playwright/test'
import { login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('application settings show the shared secret vault in both auth profiles', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/settings?setting=applications&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const section = page.locator('#settings-section-applications')
  await expect(section).toBeVisible()
  const vault = section.locator('.project-secrets-panel')
  await expect(vault.getByText('Application Secret Vault')).toBeVisible()
  await expect(vault.getByText('Scripts in this application share its secret names and fall back to project values.', { exact: false })).toBeVisible()
  await expect(vault.getByText('Loading secret metadata...')).toBeHidden()
  await expect(vault.locator('input[type="password"]')).toHaveCount(1)
  const create = vault.getByRole('button', { name: 'Create Secret' })
  if (process.env.APEXLOAD_WRAPPING_MASTER_KEY) {
    await expect(vault.getByLabel('Secret name')).toBeVisible()
  }
  if (await vault.getByLabel('Secret name').isVisible()) {
    await vault.getByLabel('Secret name').fill('E2E_LOGIN_PASSWORD')
    await vault.getByLabel('Secret value').fill('first-password')
    await expect(create).toBeEnabled()
    await create.click()
    const row = vault.locator('.project-secret-row').filter({ hasText: 'E2E_LOGIN_PASSWORD' })
    await expect(row).toContainText('application scope')
    await row.getByRole('button', { name: 'Rotate' }).click()
    await row.getByLabel('New value').fill('rotated-password')
    await row.getByRole('button', { name: 'Rotate' }).click()
    await expect(vault.locator('.settings-notice')).toContainText('E2E_LOGIN_PASSWORD was rotated')
    await expect(vault).not.toContainText('rotated-password')
  }
})
