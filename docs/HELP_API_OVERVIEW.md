# Overview & first API calls

Use the REST API for integrations, the CLI for pipelines, or MCP for agent-driven workflows. They operate on the same project-scoped resources. The [API Reference](#/swagger) is generated from controller routes; the [workflow guide](API_USER_GUIDE.md#core-workflows) describes conditional behavior and multi-step operations.

## Connect and verify access

Use the controller's reachable origin plus `/api/v1`. Hosted deployments usually use HTTPS through their configured ingress. `http://localhost:8080/api/v1` is a local example, not a universal production address. The dashboard development server normally proxies `/api` from localhost:4000.

Ask an authorized administrator for a service-account key with the project roles and token scopes needed for your task. Set `APEXLOAD_API_KEY` in your shell or CI secret store. A key is shown only once when created or rotated. Never paste it into a shared script or report. See [API authentication](API_USER_GUIDE.md#authentication).

```bash
export APEXLOAD_URL=http://localhost:8080/api/v1
curl --fail-with-body "$APEXLOAD_URL/system/health"
curl --fail-with-body -H "Authorization: Bearer $APEXLOAD_API_KEY" "$APEXLOAD_URL/auth/me"
curl --fail-with-body -H "Authorization: Bearer $APEXLOAD_API_KEY" "$APEXLOAD_URL/projects"
```

These are read-only checks. Expect HTTP 200, controller health information, the authenticated identity, and projects visible to that account. An empty project list can be a scope issue. A 401 means authentication failed; a 403 means an authenticated request was denied. Follow [authentication troubleshooting](HELP_TROUBLESHOOTING.md#authentication-or-permission-denied).

## Create, validate and run

Save the complete JSON example from [Your first test](HELP_GETTING_STARTED.md#create-and-prove-the-script) as script.json and replace its base URL with a target reachable from the execution environment. Use [API workflows](API_USER_GUIDE.md#core-workflows) to create the script in your project, validate it, assemble a scenario, and start a run. Those operations create state and the run sends traffic; use the required project and release identifiers rather than copying someone else's IDs.

For a local CLI exercise, the same script can be validated and executed without creating controller resources:

```bash
apexctl script validate --script script.json --format json
apexctl run --script script.json --vusers 1 --duration 30s
```

Expect validation to succeed before running. Inspect errors and the report rather than treating process completion as a performance verdict. Use [CLI & CI/CD](APPLICATION_USER_GUIDE.md#automating-apex-load) for exit codes and [CI pipeline examples](CICD_INTEGRATION.md) for controller-backed pipelines.

## Responses and recovery

| Status | Meaning | Next step |
| --- | --- | --- |
| 200 / 201 | Read or creation succeeded | Read the returned identity and resource IDs. |
| 202 | Work accepted asynchronously | Poll the documented status resource. |
| 400 | Invalid input | Read the validation message and correct the request. |
| 401 | Missing or invalid identity | Check the key, session and controller URL. |
| 403 | Permission or scope denied | Check both roles and token scopes. |
| 404 | Resource unavailable | Verify IDs and project scope. |
| 409 | Conflict or admission gate | Inspect the response; resolve revision, capacity or approval requirements before retrying. |
| 429 / 5xx | Throttling or service failure where declared | Preserve request context, inspect service health, and retry only when the operation is safe or idempotent. |

Error bodies normally include `error`; some workflows include structured evidence or partial results. Download endpoints may return CSV, HTML, ZIP or binary artifacts. The operation's [generated reference](#/swagger) describes declared response contracts. Do not blindly retry a mutation after an ambiguous network failure.

## Advanced local development

In local dev auth mode, `X-APEXLoad-Dev-View: admin|user` and `X-APEXLoad-Dev-Project-ID: <project-id>` simulate dashboard access. Production auth modes ignore these headers. Use service-account identity for automation; see [identity operations](AUTH_RBAC_OPERATIONS_RUNBOOK.md) for deployment configuration.
