import { test, expect, seedScript, json, idOf, login } from "/Users/jasonczech/.codex/worktrees/native-partition-pruning/Project_Stormrunner/dashboard/e2e/support/workflow"
import { createWorkspace, scopeQuery } from "/Users/jasonczech/.codex/worktrees/native-partition-pruning/Project_Stormrunner/dashboard/e2e/support/workspace"

test('settled queued copy dialog retains opaque surfaces and visible controls', async ({ page, target, trackRun }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, 'Settled_Copy_HTTP', target.url)
  const created = await json(await request.post('/api/v1/tests', {data: {project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Settled copy style source', duration: '1m', groups: [{name: 'orders', script_id: script, vusers: 1}]}}))
  const run = idOf(created); trackRun(run)
  const original = await json(await request.get(`/api/v1/tests/${run}`))
  await page.goto(`/#/command-center/${run}?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('button', {name: 'Create revised queued copy', exact: true}).click()
  const dialog = page.getByRole('dialog', {name: 'Create Revised Queued Run', exact: true})
  await expect(dialog).toBeVisible()
  await dialog.evaluate(async element => { await Promise.all(element.getAnimations({subtree: true}).map(animation => animation.finished.catch(() => undefined))) })
  await expect(dialog).toHaveCSS('opacity', '1')
  const evidence = await dialog.evaluate(element => {
    const ancestors = []
    for (let current: Element | null = element; current; current = current.parentElement) {
      const style = getComputedStyle(current)
      ancestors.push({tag: current.tagName, class: current.className, opacity: style.opacity, background: style.backgroundColor, color: style.color, visibility: style.visibility, display: style.display, animationName: style.animationName, animationDuration: style.animationDuration, transform: style.transform})
    }
    const controls = [...element.querySelectorAll('input,select,button')].map(control => {
      const node = control as HTMLInputElement
      const box = node.getBoundingClientRect(); const style = getComputedStyle(node)
      return {tag: node.tagName, type: node.type, ariaLabel: node.getAttribute('aria-label'), label: node.closest('label')?.textContent?.trim(), text: node.tagName === 'BUTTON' ? node.textContent?.trim() : undefined, disabled: node.disabled, value: node.value, opacity: style.opacity, visibility: style.visibility, display: style.display, box: {x: box.x, y: box.y, width: box.width, height: box.height}}
    })
    return {ancestors, controls, effectiveOpacity: ancestors.reduce((value, ancestor) => value * Number(ancestor.opacity), 1)}
  })
  expect(evidence.effectiveOpacity).toBe(1)
  await expect(dialog.getByLabel('Scenario Name', {exact: true})).toBeVisible()
  await expect(dialog.getByRole('button', {name: /^Create Revised Copy \(\d+ groups?\)$/})).toBeEnabled()
  await testInfo.attach('settled-dialog-style-evidence', {body: JSON.stringify(evidence, null, 2), contentType: 'application/json'})
  await page.screenshot({path: testInfo.outputPath('settled-dialog-viewport.png'), animations: 'disabled'})
  await dialog.screenshot({path: testInfo.outputPath('settled-dialog-surface.png'), animations: 'disabled'})
  await dialog.getByRole('button', {name: /^Create Revised Copy \(\d+ groups?\)$/}).scrollIntoViewIfNeeded()
  await page.screenshot({path: testInfo.outputPath('settled-dialog-submit-visible.png'), animations: 'disabled'})
  await dialog.getByRole('button', {name: 'Cancel', exact: true}).click()
  expect(await json(await request.get(`/api/v1/tests/${run}`))).toEqual(original)
  expect(target.count()).toBe(0)
})
