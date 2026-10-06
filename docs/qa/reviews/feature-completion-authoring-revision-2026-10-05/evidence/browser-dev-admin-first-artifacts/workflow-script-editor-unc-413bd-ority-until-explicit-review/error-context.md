# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-script-editor.spec.ts >> uncertain successful writes retain drafts and revoke retry authority until explicit review
- Location: e2e/workflow-script-editor.spec.ts:214:1

# Error details

```
Error: {"error":"script must have at least one action; config.base_url is empty"}


expect(received).toBeTruthy()

Received: false
```

# Test source

```ts
  43  |     response.request().method() === 'POST'
  44  |     && new URL(response.url()).pathname === '/api/v1/domains'
  45  |   ))
  46  |   await page.getByRole('button', { name: 'Add Domain' }).click()
  47  |   const domainPayload = await (await domainResponsePromise).json()
  48  |   const domainId = responseID(domainPayload, 'create domain', 'domain')
  49  |   responseID(domainPayload, 'provision application repository', 'application_repository')
  50  |   const categoryId = responseID(domainPayload, 'default category', 'default_category')
  51  | 
  52  |   await expect(page.getByText(`Domain "${domainName}" created.`)).toBeVisible({ timeout: 15_000 })
  53  |   await expect(page.locator('button.settings-app-card').filter({ hasText: domainName })).toBeVisible()
  54  | 
  55  |   const settingsSections = page.getByRole('navigation', { name: 'Settings sections' })
  56  |   await settingsSections.getByRole('button', { name: 'Projects', exact: true }).click()
  57  |   const projectNameInput = page.getByPlaceholder('Create project, e.g. Customer Portal')
  58  | 
  59  |   const createProject = async (name: string) => {
  60  |     await projectNameInput.fill(name)
  61  |     const responsePromise = page.waitForResponse(response => (
  62  |       response.request().method() === 'POST'
  63  |       && new URL(response.url()).pathname === '/api/v1/projects'
  64  |     ))
  65  |     await page.getByRole('button', { name: 'Add Project' }).click()
  66  |     const payload = await (await responsePromise).json()
  67  |     const project = record(payload, 'project')
  68  |     expect(project.domain_id, `${name} must be created in the UI-selected domain`).toBe(domainId)
  69  |     await expect(page.locator('button.settings-app-card').filter({ hasText: name })).toBeVisible({ timeout: 15_000 })
  70  |     return responseID(payload, `create project ${name}`, 'project')
  71  |   }
  72  | 
  73  |   const ownerProjectId = await createProject(ownerProjectName)
  74  |   const decoyProjectId = await createProject(decoyProjectName)
  75  |   expect(routeScope(page.url()).get('projectId')).toBe(decoyProjectId)
  76  | 
  77  |   // Keep the decoy as the active workspace project, then explicitly target the
  78  |   // owner in the application picker. This is the wrong-project regression:
  79  |   // the picker must move real workspace scope, not only its local form cursor.
  80  |   await settingsSections.getByRole('button', { name: 'Applications & releases' }).click()
  81  |   await page.getByPlaceholder('Create application, e.g. ACC').fill(applicationName)
  82  |   const applicationProject = page.getByRole('combobox', { name: 'Project for the new application' })
  83  |   await applicationProject.selectOption(ownerProjectId)
  84  |   await expect(applicationProject).toHaveValue(ownerProjectId)
  85  | 
  86  |   const applicationResponsePromise = page.waitForResponse(response => (
  87  |     response.request().method() === 'POST'
  88  |     && new URL(response.url()).pathname === '/api/v1/applications'
  89  |   ))
  90  |   await page.getByRole('button', { name: 'Add Application' }).click()
  91  |   const applicationPayload = await (await applicationResponsePromise).json()
  92  |   const application = record(applicationPayload, 'application')
  93  |   const applicationId = responseID(applicationPayload, 'create application', 'application')
  94  |   expect(application.project_id, 'application must belong to the project chosen in the UI').toBe(ownerProjectId)
  95  | 
  96  |   await expect(page.locator('button.settings-app-card').filter({ hasText: applicationName })).toBeVisible({ timeout: 15_000 })
  97  |   await expect(page.getByRole('button', { name: new RegExp(`Workspace context\\..*${ownerProjectName}`) })).toBeVisible()
  98  |   await expect.poll(() => routeScope(page.url()).get('projectId')).toBe(ownerProjectId)
  99  |   await expect.poll(() => routeScope(page.url()).get('applicationId')).toBe(applicationId)
  100 | 
  101 |   const storedApplication = await request.get(`/api/v1/applications/${applicationId}`)
  102 |   expect(storedApplication.ok()).toBeTruthy()
  103 |   expect(record(await storedApplication.json(), 'application').project_id).toBe(ownerProjectId)
  104 | 
  105 |   // Release creation in the sidebar changes active scope. The repository tabs
  106 |   // below then prove that the same hierarchy is visible on the S2 route.
  107 |   await page.getByRole('button', { name: /^Workspace context\./ }).click()
  108 |   await page.getByRole('button', { name: /Select release\. Current release:/ }).click()
  109 |   await page.getByRole('button', { name: 'New Release' }).click()
  110 |   const releaseDialog = page.getByRole('dialog', { name: 'New Release' })
  111 |   await releaseDialog.getByRole('textbox', { name: 'Name' }).fill(releaseName)
  112 |   const releaseResponsePromise = page.waitForResponse(response => (
  113 |     response.request().method() === 'POST'
  114 |     && new URL(response.url()).pathname === `/api/v1/projects/${ownerProjectId}/releases`
  115 |   ))
  116 |   await releaseDialog.getByRole('button', { name: 'Create release' }).click()
  117 |   const releasePayload = await (await releaseResponsePromise).json()
  118 |   const release = record(releasePayload, 'release')
  119 |   const releaseId = typeof release.application_release_id === 'string'
  120 |     ? release.application_release_id
  121 |     : responseID(releasePayload, 'create release', 'release')
  122 | 
  123 |   await expect(page.getByRole('button', { name: new RegExp(`Workspace context\\..*${releaseName}`) })).toBeVisible({ timeout: 15_000 })
  124 |   await expect.poll(() => routeScope(page.url()).get('releaseId')).toBe(releaseId)
  125 | 
  126 |   await page.getByRole('button', { name: 'Repository', exact: true }).click()
  127 |   await expect(page.getByRole('heading', { level: 1, name: 'Application Repository' })).toBeVisible({ timeout: 15_000 })
  128 | 
  129 |   const repositoryTabs = page.getByRole('tablist', { name: 'Repository asset types' })
  130 |   await repositoryTabs.getByRole('tab', { name: /Projects/ }).click()
  131 |   await expect(page.getByRole('main').getByRole('button', { name: ownerProjectName, exact: true }).first()).toBeVisible()
  132 |   await repositoryTabs.getByRole('tab', { name: /Releases/ }).click()
  133 |   await expect(page.getByRole('main').getByRole('button', { name: releaseName, exact: true }).first()).toBeVisible()
  134 | 
  135 |   const finalScope = routeScope(page.url())
  136 |   expect(finalScope.get('projectId')).toBe(ownerProjectId)
  137 |   expect(finalScope.get('applicationId')).toBe(applicationId)
  138 |   expect(finalScope.get('releaseId')).toBe(releaseId)
  139 |   return { label, domainId, categoryId, projectId: ownerProjectId, applicationId, releaseId }
  140 | }
  141 | 
  142 | export async function json(response: APIResponse | Response): Promise<Record<string, unknown>> {
> 143 |   expect(response.ok(), await response.text()).toBeTruthy()
      |                                                ^ Error: {"error":"script must have at least one action; config.base_url is empty"}
  144 |   return response.json()
  145 | }
  146 | export function idOf(payload: Record<string, unknown>, key?: string): string {
  147 |   return responseID(payload, 'resource', key)
  148 | }
  149 | export function mutation(page: Page, method: string, path: string) {
  150 |   return page.waitForResponse(r => r.request().method() === method && new URL(r.url()).pathname === path)
  151 | }
  152 | export async function login(page: Page, username = 'admin') {
  153 |   // Workflow tests share a controller and source IP. Pace real UI logins below
  154 |   // its ten-per-minute limit rather than retrying failures or weakening auth.
  155 |   await new Promise(resolve => setTimeout(resolve, 6500))
  156 |   await page.goto('/#/dashboard')
  157 |   await page.getByLabel('Username').fill(username)
  158 |   await page.getByLabel('Password').fill(username)
  159 |   await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  160 |   await expect(page.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
  161 | }
  162 | export async function seedScript(request: APIRequestContext, workspace: Workspace, name = 'Workflow_HTTP', baseURL = 'http://127.0.0.1:1', path = '/orders') {
  163 |   return idOf(await json(await request.post('/api/v1/scripts', { data: {
  164 |     project_id: workspace.projectId,
  165 |     script: { name, version: '1.0.0', protocol: 'http', config: { base_url: baseURL, pacing: { type: 'fixed_interval', interval_ms: 500 } }, actions: [{ name: 'orders', type: 'request', method: 'GET', target: path }] },
  166 |   } })))
  167 | }
  168 | export async function seedScenario(request: APIRequestContext, workspace: Workspace, scriptId: string, name = 'Workflow scenario') {
  169 |   return idOf(await json(await request.post('/api/v1/scenarios', { data: {
  170 |     project_id: workspace.projectId, application_id: workspace.applicationId, name, duration: '2m', scenario_mode: 'vuser_driven', groups: [{ name: 'orders', script_id: scriptId, vusers: 1, ramp_up: '1s' }],
  171 |   } })))
  172 | }
  173 | export async function importOpenAPI(page: Page, name: string, target: string) {
  174 |   await page.getByRole('button', { name: 'Scripts', exact: true }).click()
  175 |   // Navigate through the actual creation entry rather than seeding the script.
  176 |   await page.getByRole('button', { name: 'Script Builder', exact: true }).first().click()
  177 |   await page.getByRole('button', { name: 'Import', exact: true }).click()
  178 |   await page.getByRole('button', { name: /OpenAPI \/ Swagger/ }).click()
  179 |   const controls = page.getByRole('group', { name: 'Script import controls' })
  180 |   const spec = { openapi: '3.0.3', info: { title: name, version: '1.0' }, servers: [{ url: target }], paths: { '/orders': { get: { operationId: 'listOrders', responses: { '200': { description: 'Orders' } } } } } }
  181 |   await controls.locator('input[type=file]').setInputFiles({ name: 'orders.openapi.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(spec)) })
  182 |   await controls.getByRole('textbox', { name: 'Script Name' }).fill(name)
  183 |   await controls.getByText('Advanced validation only', { exact: true }).click()
  184 |   await controls.getByRole('button', { name: 'Validate Only' }).click()
  185 |   await expect(controls.getByText(/dry-run validation; nothing has been saved/i)).toBeVisible()
  186 |   await controls.locator('.import-source-setup > summary').click()
  187 |   await controls.getByRole('button', { name: 'Inspect & review' }).click()
  188 |   await expect(controls.getByRole('heading', { name: 'Review Generated Script' })).toBeVisible()
  189 |   const saved = mutation(page, 'POST', '/api/v1/scripts/import/openapi/commit')
  190 |   await controls.getByRole('button', { name: 'Save draft' }).click()
  191 |   const scriptId = idOf(await json(await saved), 'stored_script')
  192 |   await controls.getByRole('button', { name: `Open ${name} in Visual Editor`, exact: true }).click()
  193 |   return scriptId
  194 | }
  195 | export async function buildScenarioUI(page: Page, scriptName: string, name: string) {
  196 |   await page.getByRole('button', { name: 'New Test', exact: true }).first().click()
  197 |   await page.getByRole('button', { name: 'Open Scenario Builder' }).click()
  198 |   const steps = page.locator('[aria-label="Scenario builder steps"]')
  199 |   await page.getByPlaceholder('Scenario name').fill(name)
  200 |   await page.getByRole('button', { name: 'Choose script for Group 1' }).click()
  201 |   await page.getByRole('row').filter({ hasText: scriptName }).getByRole('button', { name: 'Use for Group 1' }).click()
  202 |   await steps.getByRole('button', { name: /^Model/ }).click()
  203 |   await page.getByRole('button', { name: /^Fixed VUsers/ }).click()
  204 |   await page.getByRole('button', { name: 'Use Closed VUser', exact: true }).click()
  205 |   await steps.getByRole('button', { name: /^Run Settings/ }).click()
  206 |   const scenarioTabs = page.getByRole('tablist', { name: 'Scenario settings sections' })
  207 |   await scenarioTabs.getByRole('tab', { name: 'Run Phases' }).click()
  208 |   await page.getByLabel('Ramp-up', { exact: true }).fill('1')
  209 |   await page.getByLabel('Ramp-up Unit').selectOption('s')
  210 |   await page.getByLabel('Sustained load', { exact: true }).fill('1')
  211 |   await page.getByLabel('Sustained load Unit').selectOption('m')
  212 |   await scenarioTabs.getByRole('tab', { name: 'Target Load' }).click()
  213 |   await page.getByLabel('Peak VUsers', { exact: true }).fill('1')
  214 |   await steps.getByRole('button', { name: /^Data/ }).click()
  215 |   await page.getByRole('button', { name: 'Next', exact: true }).click()
  216 |   await expect(page.getByRole('heading', { name: 'Review And Validate' })).toBeVisible()
  217 |   const saved = mutation(page, 'POST', '/api/v1/scenarios')
  218 |   await page.getByRole('button', { name: 'Save and configure run' }).click()
  219 |   const payload = await json(await saved)
  220 |   return idOf(payload)
  221 | }
  222 | export async function runStatus(request: APIRequestContext, id: string) {
  223 |   return String((await json(await request.get(`/api/v1/tests/${id}`))).status)
  224 | }
  225 | 
  226 | interface WorkflowFixtures {
  227 |   target: { url: string; count: () => number }
  228 |   trackRun: (id: string) => void
  229 | }
  230 | export const test = base.extend<WorkflowFixtures>({
  231 |   // Playwright requires destructured fixture dependencies, even when empty.
  232 |   // eslint-disable-next-line no-empty-pattern
  233 |   target: async ({}, provide) => {
  234 |     let count = 0
  235 |     const server = createServer((req, res) => { count++; res.setHeader('Content-Type', 'application/json'); res.statusCode = req.url === '/unavailable' ? 503 : 200; res.end(res.statusCode === 503 ? '{"message":"Service unavailable"}' : '{"orders":[{"id":1}]}') })
  236 |     await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  237 |     await provide({ url: `http://127.0.0.1:${(server.address() as AddressInfo).port}`, count: () => count })
  238 |     server.closeAllConnections()
  239 |     await new Promise<void>((resolve, reject) => server.close(err => err ? reject(err) : resolve()))
  240 |   },
  241 |   trackRun: [async ({ playwright, baseURL, page, target }, provide) => {
  242 |     // Depend on target so it remains available until run teardown completes.
  243 |     void target
```