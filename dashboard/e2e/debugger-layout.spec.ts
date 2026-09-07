import { expect, test } from '@playwright/test'

test('keeps the debug parameter list below the action list when the bottom dock is maximized', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 })
  await page.goto('/#/scripts')

  await page.getByRole('button', { name: 'Script Builder' }).click()
  await page.getByRole('tab', {name:'Starter Templates'}).click()
  await page.getByRole('button', { name: 'Use HTTP API Smoke' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Create Script' })).toBeVisible()

  await page.getByRole('button', { name: /Shared User Data Pool/ }).click()
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug' }).click()

  const bottomSeparator = page.getByRole('separator', { name: 'Resize bottom panel' })
  await bottomSeparator.focus()
  await bottomSeparator.press('End')
  await expect(bottomSeparator).toHaveAttribute('aria-valuenow', '640')

  const actionList = page.getByRole('region', { name: 'Debugger actions' })
  const actionSection = page.locator('.debugger-rail > .debugger-rail-section').first()
  const parameterSection = page.locator('.debugger-parameter-section')
  await expect(actionList).toBeVisible()
  await expect(actionSection).toBeVisible()
  await expect(parameterSection).toBeVisible()

  const [actionBounds, actionSectionBounds, parameterBounds, actionOverflow, parameterBackground] = await Promise.all([
    actionList.boundingBox(),
    actionSection.boundingBox(),
    parameterSection.boundingBox(),
    actionSection.evaluate(element => getComputedStyle(element).overflow),
    parameterSection.evaluate(element => getComputedStyle(element).backgroundColor),
  ])
  expect(actionBounds).not.toBeNull()
  expect(actionSectionBounds).not.toBeNull()
  expect(parameterBounds).not.toBeNull()
  const visibleActionBottom = Math.min(
    actionBounds!.y + actionBounds!.height,
    actionSectionBounds!.y + actionSectionBounds!.height,
  )
  expect(actionOverflow).toBe('hidden')
  expect(visibleActionBottom).toBeLessThanOrEqual(parameterBounds!.y)
  expect(parameterBackground).not.toBe('rgba(0, 0, 0, 0)')
})
