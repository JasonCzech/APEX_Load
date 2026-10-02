import { test, expect, seedScript, json, idOf, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
test('browser resource evidence filters retained failures and inspects action attribution', async ({ page, trackRun }) => {
 if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
 const request=page.context().request;const workspace=await createWorkspace(request);const script=await seedScript(request,workspace)
 const id=idOf(await json(await request.post('/api/v1/tests',{data:{project_id:workspace.projectId,application_id:workspace.applicationId,release_id:workspace.releaseId,name:'Resource inspection run',duration:'1s',groups:[{name:'browser',script_id:script,vusers:1}]}})));trackRun(id)
 await page.route(`**/api/v1/tests/${id}`,async route=>{
 if(route.request().method()!=='GET')return route.continue()
 const response=await route.fetch();const body=await response.json()
 return route.fulfill({response,json:{...body,status:'COMPLETE',archive:{transaction_count:1},started_at:'2026-10-01T00:00:00Z',ended_at:'2026-10-01T00:00:01Z'}})
 })
 await page.route(`**/api/v1/tests/${id}/archive/report*`,route=>route.fulfill({json:{manifest:{test_id:id,test_name:'Resource inspection run',status:'COMPLETE',provenance:{started_at:'2026-10-01T00:00:00Z',duration:'1s',total_vusers:1}},overview:{transactions:1},by_action:{},browser_resources:[{timestamp:'2026-10-01T00:00:01Z',action_name:'Navigate',url:'https://example.test/ok.js',duration_ms:0,bytes_received:0,status_code:200},{timestamp:'2026-10-01T00:00:01Z',action_name:'Click',page_url:'https://example.test/page',url:'https://example.test/fail.js',failure_kind:'transport',method:'GET'},{timestamp:'2026-10-01T00:00:01Z',action_name:'Click',url:'https://example.test/service.js',status_code:503,failure_kind:'http',method:'GET'}]}}))
 await page.goto(`/#/report/${id}?section=diagnostics&${scopeQuery(workspace)}`);await page.reload()
 await page.getByRole('tab',{name:'Diagnostics',exact:true}).click()
 await page.getByText('Browser evidence',{exact:true}).click()
 const resources=page.getByRole('region',{name:'Browser resource evidence',exact:true})
 await expect(resources).toContainText('0.0 ms');await expect(resources).toContainText('0 B')
 await resources.getByLabel('Failed resources only').check();await expect(resources).toContainText('2 of 3 retained resources');await expect(resources).not.toContainText('ok.js');await expect(resources).toContainText('503 · http');await expect(resources).toContainText('transport')
 await resources.getByText('Inspect resource 1',{exact:true}).click();await expect(resources).toContainText('https://example.test/page');await expect(resources).toContainText('GET')
 await resources.getByLabel('Resource action',{exact:true}).selectOption('Navigate');await expect(resources).toContainText('No retained resources match')
 await resources.screenshot({path:test.info().outputPath('resource-evidence.png')})
})
