# Contact support & collect evidence

Contact your organization's APEX Load administrator or service desk for deployment support. This self-hosted application does not configure a universal vendor support address. Your administrator should publish the team's support channel in the deployment's onboarding materials.

## Prepare a useful request

Copy this template and fill in only the information you can safely share:

```text
APEX Load controller/build version:
Dashboard build/version:
Deployment type (hosted browser / desktop / CLI):
Time of problem and timezone:
Project/application/release scope (IDs if permitted):
Run/script/scenario ID:
Action attempted:
Expected outcome:
Actual outcome and exact sanitized error:
Smallest reproduction:
Checks already performed:
Evidence attached after redaction review:
```

The build information control in the sidebar identifies the controller build. Help's footer identifies the dashboard documentation bundle. These can differ; record both when reporting a mismatch.

## Review evidence before sharing

Omit API keys, cookies, pairing tokens, passwords and private request bodies. Imported scripts may contain literal credentials; do not assume an entire archive is safe to attach. Prefer a minimal redacted reproduction and the affected action's sanitized error. Follow your organization's data-sharing policy for logs, screenshots and archives.

## Who can resolve the problem

Project owners can check scope, script configuration and test data. Administrators can review permissions, generator capacity and deployment health. Use [Troubleshooting](HELP_TROUBLESHOOTING.md) to collect the relevant checks before escalating. For a suspected product defect, your administrator can route the reviewed reproduction through your organization's established issue tracker.
