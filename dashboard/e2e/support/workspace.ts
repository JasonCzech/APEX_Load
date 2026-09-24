import { type APIRequestContext, type APIResponse } from '@playwright/test'

// Seeds the workspace hierarchy a Tier-0 journey needs: Domain -> Project ->
// Application -> Release. See docs/qa/E2E_SUITES.md step S2.
//
// It is built through the API rather than by driving the UI, for the same
// reason liveRun.ts seeds runs through the API: a spec that has to click its
// way through four creation dialogs before it can assert anything spends most
// of its runtime on setup, and fails for setup reasons rather than for the
// reason it exists. Specs that *do* assert on the creation UI drive it directly
// instead of calling this.
//
// Shapes here were confirmed against a running controller, not inferred from
// handlers. Two are easy to get wrong:
//   * POST /api/v1/domains also provisions an application repository and a
//     default category, returned alongside the domain.
//   * A project needs BOTH domain_id and category_id. Sending only the category
//     resolves the domain elsewhere and fails with "category does not belong to
//     domain".
//   * Releases are nested: POST /api/v1/applications/{id}/releases. There is no
//     top-level POST /api/v1/releases.

export interface Workspace {
  domainId: string
  categoryId: string
  projectId: string
  applicationId: string
  releaseId: string
  /** Unique across concurrent seeds so specs can assert on their own rows. */
  label: string
}

export interface CreateWorkspaceOptions {
  /** Distinguishes this workspace's entities in the controller. */
  label?: string
}

async function postJSON(
  request: APIRequestContext,
  path: string,
  body: Record<string, unknown>,
  what: string,
): Promise<Record<string, unknown>> {
  const response: APIResponse = await request.post(path, { data: body })
  if (!response.ok()) {
    // Include the body: a bare status turns every seeding failure into the same
    // unactionable message, and placement errors in particular say exactly what
    // is wrong.
    throw new Error(`${what}: ${response.status()} ${await response.text()}`)
  }
  return (await response.json()) as Record<string, unknown>
}

function nested(payload: Record<string, unknown>, key: string): Record<string, unknown> {
  const value = payload[key]
  return value && typeof value === 'object' ? (value as Record<string, unknown>) : payload
}

function identifier(payload: Record<string, unknown>, what: string): string {
  const id = payload.id
  if (typeof id !== 'string' || id === '') {
    throw new Error(`${what}: response carried no id: ${JSON.stringify(payload).slice(0, 200)}`)
  }
  return id
}

export async function createWorkspace(
  request: APIRequestContext,
  options: CreateWorkspaceOptions = {},
): Promise<Workspace> {
  const label = options.label ?? `e2e-${Date.now()}-${Math.floor(Math.random() * 1e6)}`

  const domainPayload = await postJSON(request, '/api/v1/domains', { name: `${label} domain` }, 'create domain')
  const domainId = identifier(nested(domainPayload, 'domain'), 'create domain')
  const categoryId = identifier(nested(domainPayload, 'default_category'), 'create domain (default category)')

  const projectPayload = await postJSON(
    request,
    '/api/v1/projects',
    { name: `${label} project`, domain_id: domainId, category_id: categoryId },
    'create project',
  )
  const projectId = identifier(nested(projectPayload, 'project'), 'create project')

  const applicationPayload = await postJSON(
    request,
    '/api/v1/applications',
    { name: `${label} application`, project_id: projectId },
    'create application',
  )
  const applicationId = identifier(nested(applicationPayload, 'application'), 'create application')

  const releasePayload = await postJSON(
    request,
    `/api/v1/applications/${applicationId}/releases`,
    { name: `${label} 1.0` },
    'create release',
  )
  const releaseId = identifier(nested(releasePayload, 'release'), 'create release')

  return { domainId, categoryId, projectId, applicationId, releaseId, label }
}

/**
 * Builds the workspace-scoped query string every routable view accepts. The
 * dashboard carries scope in the hash route rather than in app state, so a spec
 * that navigates without it lands on whatever the previous spec selected.
 */
export function scopeQuery(workspace: Workspace): string {
  return `projectId=${workspace.projectId}&applicationId=${workspace.applicationId}&releaseId=${workspace.releaseId}`
}
