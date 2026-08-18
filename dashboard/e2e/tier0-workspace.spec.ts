import { expect, test } from '@playwright/test'
import { createWorkspace, scopeQuery } from './support/workspace'

// Tier-0 journey step S2 (docs/qa/TIER0_JOURNEY.md): the workspace hierarchy a
// user builds before anything else can happen.
//
// Two assertions here are regression guards rather than happy-path checks:
//
//   * The created project must actually be reachable and shown. A dashboard
//     that renders an empty repository for a project the API just created is
//     the failure this catches.
//   * An application must be created against the project the caller asked for.
//     A local-only project cursor previously produced both a spurious "Select a
//     project before creating an application" error and, worse, silent creation
//     against the wrong project — a wrong-project write that nothing surfaced.

test('the API-created workspace hierarchy is coherent and scoped', async ({ request }) => {
  const workspace = await createWorkspace(request)

  // Every level must be addressable on its own, not merely returned by the
  // creating call.
  const project = await request.get(`/api/v1/projects/${workspace.projectId}`)
  expect(project.ok(), 'created project must be retrievable').toBeTruthy()

  const application = await request.get(`/api/v1/applications/${workspace.applicationId}`)
  expect(application.ok(), 'created application must be retrievable').toBeTruthy()

  // The wrong-project guard: the application must belong to the project it was
  // created under, not to whatever project the server considered "active".
  const applicationBody = (await application.json()) as Record<string, unknown>
  const owner = (applicationBody.application ?? applicationBody) as Record<string, unknown>
  expect(owner.project_id, 'application must belong to the project it was created under')
    .toBe(workspace.projectId)

  // The release must be listed under its application, which is the relationship
  // the run-creation flow later depends on.
  const releases = await request.get(`/api/v1/applications/${workspace.applicationId}/releases`)
  expect(releases.ok(), 'application releases must be listable').toBeTruthy()
  const releaseText = await releases.text()
  expect(releaseText, 'the created release must appear under its application')
    .toContain(workspace.releaseId)
})

test('the repository view renders a freshly created project', async ({ page, request }) => {
  const workspace = await createWorkspace(request)

  await page.goto(`/#/repository?${scopeQuery(workspace)}`)

  // Assert on the project's own name rather than on chrome: a shell that renders
  // while the data call fails would satisfy a chrome-only assertion.
  await expect(
    page.getByText(`${workspace.label} project`, { exact: false }).first(),
    'the repository view must show the project that was just created',
  ).toBeVisible({ timeout: 15_000 })
})
