# Diagnose a problem

Start with the symptom and keep the current run, scope and error evidence. Many problems can be resolved without creating another run. The [testing FAQ](APPLICATION_USER_GUIDE.md#troubleshooting-and-faq) covers additional script and report questions.

## Authentication or permission denied

**Symptom:** Sign-in fails, an action is unavailable, or the API returns 401/403.

Check the controller URL and identity first. A valid identity still needs an active local user binding or a service account with both appropriate roles and token scopes. A 401 indicates missing/invalid authentication; a 403 indicates authorization denial. Ask your administrator to review access for the specific project and operation, using [API authentication](API_USER_GUIDE.md#authentication) and the [identity runbook](AUTH_RBAC_OPERATIONS_RUNBOOK.md). Share the operation, status and sanitized error, never the key or session cookie.

## A script or run is missing

Check Domain, Project, Application and Release in the sidebar. Then check filters and whether the item was archived or deleted. Select the scope in which it was created. If it remains unavailable, ask an administrator to check access and retention using its ID. See [Concepts & navigation](APPLICATION_USER_GUIDE.md#signing-in-and-getting-oriented).

## Missing secrets or unexpanded templates

Replay one user and inspect the failing action. Check namespace spelling, extractor matches and secret status in the selected project. An imported literal credential and a project secret have different disclosure behavior. Do not add real secrets to help requests or logs. Resolve the binding with an authorized human workflow; see [Parameters, data & secrets](APPLICATION_USER_GUIDE.md#parameterizing-and-data).

## Recording agent will not pair

Confirm the recording agent is running on the operator workstation, that the displayed command belongs to the current pairing session, and that Verify Agent can reach its loopback address. Check browser private-network permission and workstation endpoint controls with your administrator. A remote controller cannot reach a workstation's localhost on your behalf. Use the [recording workflow](APPLICATION_USER_GUIDE.md#recording-and-correlation); include agent/build versions and the sanitized pairing error when escalating, but omit pairing tokens.

## An import cannot execute

Read the import disposition before launching. Review-only, unsupported and external-runner-required outcomes are different from executable output. Java VUser and Bruno need configured governed runtimes. SAP GUI has no registered runtime in standard binaries, and Oracle NCA needs a bridge that is not supplied by enabling an ordinary pool setting. See [Capabilities & limitations](HELP_CAPABILITIES.md) and [Import existing scripts](APPLICATION_USER_GUIDE.md#importing-existing-scripts).

## Launch refused or waiting for capacity

Inspect the capacity preview, generator health, project limits and reservations. Reduce the requested load only if that still meets your test objective; otherwise ask an administrator to provide capacity. A slower ramp does not necessarily reduce peak VUser capacity requirements. Validation alone does not guarantee admission. See [Model load](APPLICATION_USER_GUIDE.md#building-a-scenario) and [Cluster connections](CLUSTER_CONNECTIONS.md).

## Shakeout failed

Open SHAKEOUT_REVIEW and inspect the canary evidence. Correct the script or environment and replay before trying the full load again. Override only through the authorized review workflow with a documented reason. See [Run & monitor](APPLICATION_USER_GUIDE.md#launching-a-test).

## Metrics or traces are missing

Confirm the run is active and the dashboard's live connection is healthy. Check controller/generator reachability and your Prometheus scrape configuration for metrics. OpenTelemetry exports opt-in traces only; enabling it does not create a metrics or log exporter. See [OpenTelemetry traces](OPENTELEMETRY.md) and your deployment's monitoring runbook.

## Reports or archives are unavailable

Check whether the run is still STOPPING or finalizing archives, whether your account can read the run, and whether retention has removed its evidence. Use a report profile that supports the requested format. Do not equate a missing archive with a zero-error test. Preserve the run ID and failure message; use [Backup, recovery & retention](ENTERPRISE_DATA_PLATFORM_OPERATIONS_RUNBOOK.md).

## Still blocked

Use [Contact support & collect evidence](HELP_SUPPORT.md). Include what you expected, what happened, the smallest reproduction and the exact build. Do not include credentials, private target payloads or unreviewed archives.
