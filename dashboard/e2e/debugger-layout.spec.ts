import { expect, test } from '@playwright/test'

test('keeps the debug outline and its parameters inside the rail when the bottom dock is maximized', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 })
  await page.goto('/#/scripts')

  await page.getByRole('button', { name: 'Script builder' }).click()
  await page.getByRole('tab', {name:'Starter templates'}).click()
  await page.getByRole('button', { name: 'Use HTTP API Smoke' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Create script' })).toBeVisible()

  await page.locator('.author-main-action').getByRole('button', { name: '+ Add Action', exact: true }).click()
  await page.getByText('Add action from a block', { exact: true }).click()
  await page.getByRole('button', { name: /Shared User Data Pool/ }).click()
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug' }).click()

  await page.getByRole('button', { name: 'Expand logs and problems' }).click()
  const bottomSeparator = page.getByRole('separator', { name: 'Resize bottom panel' })
  await bottomSeparator.focus()
  await bottomSeparator.press('End')
  await expect(bottomSeparator).toHaveAttribute('aria-valuenow', '640')

  const rail = page.getByRole('complementary', { name: 'Actions', exact: true })
  const tree = rail.getByRole('tree', { name: 'Script steps' })
  const parameters = rail.locator('.debug-outline-parameters')
  await expect(tree).toBeVisible()
  await expect(parameters).toBeVisible()

  // The outline scrolls inside the rail, and the parameters sit below it, still inside the rail.
  const [railBounds, treeBounds, parameterBounds, treeOverflow] = await Promise.all([
    rail.boundingBox(),
    tree.boundingBox(),
    parameters.boundingBox(),
    tree.evaluate(element => getComputedStyle(element).overflowY),
  ])
  expect(railBounds).not.toBeNull()
  expect(treeBounds).not.toBeNull()
  expect(parameterBounds).not.toBeNull()
  expect(treeOverflow).toBe('auto')
  expect(treeBounds!.y + treeBounds!.height).toBeLessThanOrEqual(parameterBounds!.y + 1)
  expect(parameterBounds!.y + parameterBounds!.height).toBeLessThanOrEqual(railBounds!.y + railBounds!.height + 1)
})
