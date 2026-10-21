# GitHub Actions Release and Deployment Operations

APEX Load releases and deployments are owned by the active workflows under
`.github/workflows`. Deployment workflows never compile application code. They
accept an exact semantic release tag, verify its portable signature, read the
current schema-v2 release manifest (with schema-v1 verification retained for
legacy releases), and deploy only the assets and image digests named
by that manifest.

## Protected environments

Create these environments before enabling the workflows:

| Environment | Purpose |
| --- | --- |
| `registry-release` | Image publication, portable signatures, draft Release publication, and the signed moving `v1` tag. |
| `client-release` | macOS Developer ID/notarization and Windows Authenticode client packaging. |
| `aks-staging`, `aks-production` | AKS deployment identities, Helm values, controller smoke project, and deployment API key. |
| `appservice-staging`, `appservice-production` | App Service slot identity, endpoints, and smoke project. |
| `onprem-registry` | Internal registry credentials and KMS/HSM-backed signing identity. |
| `azure-infra-staging`, `azure-infra-production` | Separate Terraform plan and apply identities, backend settings, encrypted plan handoff. |

Production application environments must require a reviewer who did not
initiate the run, prevent self-review, restrict deployment to protected `main`,
and disable routine administrator bypass. These workflows use
`repository_dispatch`, so the job ref is `main`; do not configure their
environment rules to allow only release tags. The workflow's signed annotated
tag verification is the release gate. Terraform environments likewise use
protected `main`: a plan-only dispatch creates an authenticated handoff and a
later apply dispatch receives a fresh approval. Use workflow concurrency as an
additional guard; do not configure environment rules that permit overlapping
production deployments.

The repository token remains read-only by default. Individual release jobs
receive `contents: write` or `packages: write` only where publication requires
it. Azure access uses environment-specific OIDC identities rather than client
secrets.

### Reviewed active-run deployment override

AKS and App Service deployments acquire a database-backed controller maintenance
lease before any mutation. Acquisition, active-test counting, and new-test start
admission use the same durable row lock across controller replicas. The deployment
service account requires the global `system_admin` role and only the
`deployments:manage` token scope; do not grant it the broader `settings:manage`
scope merely for deployment.

An exceptional deployment with already executing tests may proceed only when the target protected
environment supplies both `APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL` and
`APEXLOAD_ACTIVE_RUN_OVERRIDE_API_KEY`. The override key must belong to a
different, short-lived service account with the global `system_admin` role and
the two explicit scopes `deployments:manage` and
`deployments:override-active-tests`. The ordinary deployment account must not
carry the override scope, and wildcard scopes do not satisfy this conditional
controller check. Do not define either secret name as a repository or
organization secret, and do not add a `workflow_dispatch` input for either one.
The workflow intentionally accepts only `release` and `environment`; the
protected environment releases the approval and separate credential after its
independent reviewer approves the deployment job.

The secret is a short-lived JSON document bound to the exact signed release and
full GitHub environment name:

```json
{
  "approval_id": "CHG-12345",
  "environment": "aks-production",
  "expires_at": "2026-07-15T19:30:00Z",
  "reason": "Emergency controller security patch approved by change management",
  "release": "v1.2.3",
  "schema_version": 1
}
```

Use `appservice-staging`, `appservice-production`, `aks-staging`, or
`aks-production` as appropriate. The expiry must be UTC RFC 3339, still be in
the future when the preflight runs, and be no more than four hours away. Issue
the override account with an expiry no later than the approval, use a key
different from `APEXLOAD_DEPLOY_API_KEY`, and remove both override secrets
immediately after the approved deployment. The preflight validates the entire
document, selects the elevated key only when that document is present, and
never prints the document, either API key, or its reason. It never sends,
stores, or audits the reason. The
controller audit contains only the non-sensitive approval ID, active-test count,
exact release/environment, initiating actor, repository, and workflow run/attempt;
the protected-environment deployment record remains the authority for reviewer
identity. A valid approval permits atomic lease acquisition despite the counted
existing tests, while the held lease still blocks every unrelated new test/CI
start. Invalid, expired, mismatched, or absent approvals do not bypass the
active-test gate. Because the Actions `secrets` context does not
report a secret's scope, activation review must also verify that no repository-
or organization-level secret with either override name exists.

The lease token is random, masked immediately, stored only in a mode-0600 runner
state file, and carried to the controller only in the dedicated redacted
`X-APEXLoad-Deployment-Lease-Token` header, never in JSON request bodies or
command arguments. Ingress and proxy logging must redact that header. Only its
hash is written to durable CI metadata. The protected
workflow's own distributed smoke run must present that token; matching GitHub
source fields alone never bypass the fence. Deploy mutations are renewed first
and bounded to 24 minutes with a 60-second termination grace. Authoritative
workflows pin the lease to 60 minutes, while smoke and fallback rollback remain
bounded to 10 and 25 minutes. The rollback job bounds a renewal attempt to one
minute and the recovery script to 23 minutes; the recovery scripts also bound
each platform mutation, rollout/slot operation, metadata query, and controller
smoke phase. The 60-minute lease therefore preserves a 10-minute handoff margin
even when the failed candidate cannot service the renewal immediately before
rollback. Release runs under `always()` before
evidence upload. An expired or wrong-owner renewal/release fails closed; a
crashed runner can block unrelated starts only until the one-hour lease expires.

## Release trust roots and credentials

Configure the following reviewed variables and secrets. Public keys are
variables so operators and consumers can compare them out of band; private keys
must be environment secrets or KMS/HSM key references.

| Scope | Name | Meaning |
| --- | --- | --- |
| Repository variable | `APEXLOAD_RELEASE_TAG_PUBLIC_KEYS` | Armored GPG public keys trusted to sign semantic release tags. |
| Repository variables/secrets | `APEXLOAD_RELEASE_REGISTRY`, `APEXLOAD_RELEASE_REGISTRY_NAMESPACE`, `APEXLOAD_RELEASE_REGISTRY_USERNAME`, `APEXLOAD_RELEASE_REGISTRY_PASSWORD` | Release registry host, namespace, and optional explicit credentials. GitHub.com may omit credentials only for `ghcr.io`, where the workflow uses its scoped token. Custom and GHES registries must configure the username and protected password explicitly. |
| Repository variable | `BUILDX_RELEASE_BASE_URL` | Optional HTTPS mirror for the reviewed Buildx 0.34.1 binaries. Release jobs verify the official architecture-specific SHA-256 value before installing the Docker CLI plugin; air-gapped runners may instead use the exact preinstalled version. |
| Repository variables | `NSIS_RELEASE_BASE_URL`, `NSIS_ARCHIVE_PATH` | Optional HTTPS mirror for the reviewed NSIS 3.10 zip, or the sealed local archive path used on air-gapped Windows runners. Both paths are verified against the repository-pinned SHA-256 before extraction. |
| Repository variable | `APEXLOAD_BUILDKIT_IMAGE` | Optional internal mirror of the reviewed BuildKit 0.31.1 image. It must remain an immutable digest reference ending in `sha256:6b59b7df63a8cb9902736f9ddf7fcff8261613d3e7449b8ea8b7537fc399c03a`; tag-only or changed-digest values fail before a privileged builder starts. |
| Repository variable | `SKOPEO_RELEASE_BASE_URL` | Optional HTTPS mirror for the pinned Skopeo 1.20.0 deployment binary. The installer verifies architecture-specific SHA-256 checksums before execution. Air-gapped runners may instead use the reviewed preinstalled-tool path. |
| `registry-release` variable/secret | `APEXLOAD_ENABLE_HOSTED_ATTESTATIONS`, `APEXLOAD_GITHUB_ATTESTATIONS_TOKEN` | Optional GitHub.com provenance. Enable only where the repository plan supports attestations and give the protected fine-grained/GitHub App token attestation write access. The workflow passes this token explicitly and does not declare the GHES-incompatible `attestations` permission key. Portable signatures remain mandatory and authoritative. |
| `registry-release` variable | `APEXLOAD_RELEASE_SIGNING_PUBLIC_KEY_PEM` | OpenSSL public key that verifies raw `SHA256SUMS.sig`. Every canonical action release, including prereleases, must bundle the same key as `release-signing-key.pem`; mirrored consumers may also supply it explicitly to the action. |
| `registry-release` variable/secret | `APEXLOAD_RELEASE_SIGNER_PATH` or `APEXLOAD_RELEASE_SIGNING_KEY_PEM` | Prefer an absolute protected-runner executable that asks KMS/HSM to sign `SHA256SUMS` and writes an OpenSSL-verifiable signature. The exportable PEM is the fallback. |
| `registry-release` variable/secret | `COSIGN_PUBLIC_KEY` plus `COSIGN_KEY_REF` or `COSIGN_PRIVATE_KEY`/`COSIGN_PASSWORD` | Portable image signature key pair. Prefer a supported KMS/HSM URI in `COSIGN_KEY_REF`; the exportable secret-key form is a fallback. |
| `registry-release` variable/secret | `APEXLOAD_RELEASE_TAG_SIGNING_KEY_ID`, `APEXLOAD_RELEASE_TAG_SIGNER_MODE`, `APEXLOAD_RELEASE_TAG_GNUPGHOME`, `APEXLOAD_RELEASE_TAG_PRIVATE_KEY` | Prefer `preinstalled` mode with a protected GPG agent/HSM keyring; the exportable private-key secret is the fallback. |
| `client-release` variables/secrets | `APEXLOAD_MACOS_CERTIFICATE_P12_BASE64`, `APEXLOAD_MACOS_CERTIFICATE_PASSWORD`, `APEXLOAD_MACOS_CODESIGN_IDENTITY`, `APEXLOAD_MACOS_NOTARY_KEY_P8_BASE64`, `APEXLOAD_MACOS_NOTARY_KEY_ID`, `APEXLOAD_MACOS_NOTARY_ISSUER_ID`, `APEXLOAD_WINDOWS_CODESIGN_PFX_BASE64`, `APEXLOAD_WINDOWS_CODESIGN_PASSWORD` | Developer ID, App Store Connect API-key notarization, and Authenticode identities. The private materials are environment secrets; key/issuer IDs are variables. All are mandatory and release jobs fail closed. P12/PFX passwords are consumed through environment-backed APIs, never command arguments. |
| `onprem-registry` variable/secret | `ONPREM_COSIGN_PUBLIC_KEY` plus `ONPREM_COSIGN_KEY_REF` or `ONPREM_COSIGN_PRIVATE_KEY`/`ONPREM_COSIGN_PASSWORD` | Internal portable digest signer; use the KMS/HSM reference form in production. |

External/KMS/HSM signer modes and exportable software-key modes are mutually
exclusive. Do not retain a PEM/password fallback secret when a signer path or
key reference is configured; the workflows reject mixed mode and do not inject
software private material into external-signer steps.

Both the public release registry and the on-premises registry must enforce
server-side tag immutability and deletion protection for semantic release tags
such as `v1.2.3-standard` and `v1.2.3-enterprise`. The workflows serialize
publication, distinguish confirmed tag absence from authentication or transport
failures, and verify the final digest, but the OCI distribution API does not
provide a portable atomic create-if-absent operation. Registry policy is
therefore the final protection against an external writer racing tag promotion.
Publication credentials may create new release tags but must not be able to
delete or overwrite an existing release tag.

The protected semantic tag must be annotated, GPG-signed, match
`vMAJOR.MINOR.PATCH[-prerelease]`, and point to a commit reachable from `main`.
The canonical action's `apexctl-version` must already equal that tag. Update it
in the release PR; the release job will not rewrite source after tagging.

`release.yml` performs the authoritative reusable CI workflow, freezes build
metadata once from the tag commit time, and creates an empty draft Release. It
then:

1. Builds four standalone `apexctl` binaries with the asset names consumed by
   the canonical action.
2. Packages Linux controller-side tools, the App Service zip, Helm chart, and
   signed desktop clients.
3. Builds standard and enterprise controller/generator images once for amd64
   and arm64, generates SPDX SBOMs, blocks fixable HIGH/CRITICAL findings, signs
   their exact multi-platform digests, and verifies portable SBOM attestations.
   The gate, the same in CI and on-prem publishing, blocks every
   application-library finding, fixed upstream or not, and every OS-package
   finding the distribution has shipped a fix for. OS findings with no
   distribution fix are listed in the log without blocking.
   GitHub.com hosted provenance is also emitted when the explicit capability
   flag and protected attestation token are configured.
4. Creates `release-manifest.json` schema version 2, a single `SHA256SUMS`, and
   the raw detached OpenSSL SHA-256 signature `SHA256SUMS.sig`.
5. Verifies every promised asset and signature before uploading to the draft.
   A retry may reuse only the marker-bound draft and exact already-promoted
   image digests. Existing mismatched bytes or tags fail closed.
6. Downloads the bytes stored in the draft, compares the exact asset set,
   reverifies the portable signature/checksums, rechecks the API asset contract,
   and only then publishes it. GitHub.com releases fail closed unless repository
   immutable releases are enabled and the published Release reports
   `immutable=true`. GHES uses the signed annotated tag plus exact portable
   asset contract as a tamper-evident trust path because supported GHES lines do
   not provide the hosted write lock.
   Stable releases then move the
   protected, signed `v1` action tag. Prereleases do not move `v1`.

No workflow publishes `latest`.

## Trusted dispatch bootstrap

Privileged release, deployment, infrastructure, and manual live-protocol
workflows use `repository_dispatch`, so GitHub loads their workflow definitions
from protected `main`. Release dispatches submit only
`client_payload.release` and, where applicable,
`client_payload.environment`, using the reviewed event types
`apexload-release`, `deploy-aks-release`, `deploy-appservice-release`, or
`publish-onprem-release`. The release and application-deployment verifier first
checks out only `refs/heads/main`, fetches the requested tag, verifies its
trusted GPG signature and `main` ancestry, and compares the exact annotated tag
object and commit with the GitHub API. Candidate code is not checked out or
executed until that gate passes.

Infrastructure uses the distinct `azure-infra-plan` and `azure-infra-apply`
event types. Plan payloads contain exactly `environment`; apply payloads contain
exactly `environment`, `plan_run_id`, and `plan_run_attempt`. The protected
workflow validates those exact keys before checking out the immutable
default-branch event SHA. Manual protected-protocol execution uses
`protocol-integration` with an empty `client_payload`; its scheduled trigger is
unchanged. Protect `main`, restrict repository-dispatch credentials to dedicated
automation identities, and require the matching protected environment
approvals. Do not add a tag-push or `workflow_dispatch` bootstrap that can load
privileged workflow or verifier code from an unverified ref.

The dispatcher can use a GitHub App installation token or fine-grained token
restricted to this repository with `Contents: write`, which is the permission
required by the repository-dispatch endpoint. These copy-ready examples assume
`gh` is already authenticated as that dedicated automation identity:

```bash
repository=owner/repository
release=v1.2.3

jq -n --arg release "$release" \
  '{event_type:"apexload-release",client_payload:{release:$release}}' |
  gh api --method POST "repos/$repository/dispatches" --input -
```

```bash
repository=owner/repository
release=v1.2.3
environment=production

jq -n \
  --arg release "$release" \
  --arg environment "$environment" \
  '{event_type:"deploy-aks-release",client_payload:{release:$release,environment:$environment}}' |
  gh api --method POST "repos/$repository/dispatches" --input -

jq -n \
  --arg release "$release" \
  --arg environment "$environment" \
  '{event_type:"deploy-appservice-release",client_payload:{release:$release,environment:$environment}}' |
  gh api --method POST "repos/$repository/dispatches" --input -

jq -n --arg release "$release" \
  '{event_type:"publish-onprem-release",client_payload:{release:$release}}' |
  gh api --method POST "repos/$repository/dispatches" --input -
```

Create an Azure infrastructure plan:

```bash
repository=owner/repository
environment=production

jq -n --arg environment "$environment" \
  '{event_type:"azure-infra-plan",client_payload:{environment:$environment}}' |
  gh api --method POST "repos/$repository/dispatches" --input -
```

After independently reviewing the successful plan run, promote its exact
attempt:

```bash
repository=owner/repository
environment=production
plan_run_id=123456789
plan_run_attempt=1

jq -n \
  --arg environment "$environment" \
  --arg plan_run_id "$plan_run_id" \
  --arg plan_run_attempt "$plan_run_attempt" \
  '{
    event_type:"azure-infra-apply",
    client_payload:{
      environment:$environment,
      plan_run_id:$plan_run_id,
      plan_run_attempt:$plan_run_attempt
    }
  }' |
  gh api --method POST "repos/$repository/dispatches" --input -
```

Run all protected live-protocol lanes manually:

```bash
repository=owner/repository

jq -n '{event_type:"protocol-integration",client_payload:{}}' |
  gh api --method POST "repos/$repository/dispatches" --input -
```

## AKS and on-premises Kubernetes

`deploy-aks.yml` accepts only `release` and `environment`. Environment variables
provide cluster coordinates, namespace, Helm release name, controller URL, and
the protected smoke project/script. `APEXLOAD_HELM_VALUES_YAML` supplies reviewed
environment values, while Kubernetes Secrets continue to hold runtime secrets.
The deployment identity needs read access to those externally provisioned
Secrets, but it is deliberately not granted Secret creation authority.

The deployment verifies the release and Cosign signatures, selects the
enterprise controller and generator digests, refuses deployment while a test is
active unless the target environment supplies the reviewed, release-bound
override above, validates RBAC/TLS/storage/secret prerequisites, renders Helm with a
strict digest post-renderer, performs server-side schema validation and
`kubectl diff`, and applies with `--atomic --wait`. It then verifies each pod's
runtime `imageID`, release commit, readiness, dashboard assets, generator
connectivity, and a controller-backed authenticated performance smoke.

The protected namespace must already exist with Pod Security `enforce=restricted`;
the workflow forces `namespace.create=false` and binds every rendered resource
to that namespace. Runtime `imageID` values are checked against the signed
amd64/arm64 child digest that matches each pod's node architecture.

The prior Helm revision is captured before mutation. A rollout or direct smoke
failure triggers rollback inside the deployment script. A later performance or
evidence failure invokes an explicit rollback job step and waits for the prior
controller and generator workloads. Kustomize remains CI-validated for manual
compatibility but is not the production interface.

`publish-onprem.yml` runs only on runners labeled `self-hosted`, `onprem`, and
`trusted`. Keep those runners ephemeral or rebuilt from an immutable internal
runner image; untrusted pull-request workflows must not reference these labels.
Set the protected `APEXLOAD_ONPREM_RUNNER_IMAGE_DIGEST` environment variable to
that image's immutable `sha256:` digest, and inject the same value as
`APEXLOAD_RUNNER_IMAGE_DIGEST` when the runner starts. Publishing fails closed
when the two values differ or when the exact reviewed Skopeo version is absent.
The workflow treats `ONPREM_REGISTRY_HOST` and
`ONPREM_REGISTRY_NAMESPACE` as different values. It mirrors release-manifest
digests to non-release candidate tags, scans and signs the exact internal
digests, waits for all four component/profile lanes, then exposes semantic
release tags. A release tag is never visible before all candidates verify.

## App Service

`deploy-appservice.yml` verifies the released zip and its required launcher,
package, and controller entries. A dispatch targeting `staging` deploys and
validates only the configured staging slot and never receives the production
URL or invokes a slot swap. A `production` dispatch deploys the candidate to
that slot, verifies the exact signed revision, version, build time, dashboard,
live generator capacity, and RFC 6455 WebSocket upgrade, and swaps only after
those checks pass. One shared workflow concurrency group serializes staging and
production dispatches because both mutate the same slot. The controller
maintenance lease is renewed before each bounded mutation, post-deploy URL
validation, or execution smoke, so unrelated new tests cannot start between
validation and completion.

Configure `APEXLOAD_PUBLIC_URL` as a deployment-slot setting on both slots:
the staging slot value must equal `APPSERVICE_STAGING_URL`, and the production
slot value must equal `APPSERVICE_PRODUCTION_URL`. The workflow verifies this
binding before any deployment and again after deployment or swap. This keeps
the controller's OIDC audience and GitHub Check links bound to the URL of the
slot currently serving it; missing, non-sticky, or mismatched values fail the
deployment closed.

The selected environment then receives an authenticated execution smoke. A
failed production post-swap check records durable swap state, swaps the former
production package back even when the new production health endpoint is
unreachable, and revalidates its full revision/version/build-time identity.
The App Service plan must support deployment slots.

The active workflow requires an already healthy production baseline so it can
prove rollback. Bootstrap a new App Service through the infrastructure/onboarding
runbook, validate its baseline controller, and only then enable this release
workflow; a first-ever unverified production slot is intentionally unsupported.

## Terraform

`infra-azure.yml` is separate from application deployment. Dispatch
`azure-infra-plan` with exactly the protected `staging` or `production`
environment; the plan-only identity creates a saved plan whose ciphertext and
source/environment/run metadata are authenticated and retained for seven days.
After review, dispatch `azure-infra-apply` with that environment plus the exact
`plan_run_id` and `plan_run_attempt`. The apply identity downloads only that
successful plan-run artifact, verifies its HMAC, age, Terraform version, source
commit, repository, and environment, confirms the commit remains in
protected-main history, decrypts and checksums it, and applies without
replanning. Configure least-privilege roles for both identities and use a
private, ephemeral runner when the state account or Azure resources expose
private endpoints.

## External activation prerequisites

The workflows fail closed until repository administrators configure the real
action-bundled `release-signing-key.pem`, protected environments/reviewers,
rulesets requiring `CI / Required`, GitHub App installation, registry/cloud
identities, platform signing certificates, and KMS/HSM or fallback signing
identities. Those values cannot be safely invented in source. GitHub.com and
each targeted GHES line also require recorded release, staging deployment,
controller-run, and rollback drills before production activation.

## Rollback drills and release acceptance

Before production activation, exercise all of the following in staging:

- a signed prerelease with every binary, desktop package, SBOM, manifest,
  checksum, signature, and image digest;
- an AKS install and a deliberately broken post-deployment smoke that restores
  the captured Helm revision;
- an App Service package that fails staging and never swaps, followed by a
  production-check failure that swaps back;
- a controller-backed performance smoke whose Check completes after the
  workflow client disconnects;
- concurrent dispatch attempts, confirming environment concurrency serializes
  the target; and
- a Terraform plan-only run plus an independently approved apply of the saved
  encrypted plan.

Keep deployment evidence for at least the configured 90 days or longer where
regulation requires it.

## GitHub Enterprise Server compatibility

APEX Load implements a GitHub.com path and a GHES capability profile instead of
assuming GitHub.com services are present. A GHES line is not considered
qualified until its staging drill is recorded. The table below is reviewed
against GitHub's [release and minimum-runner table](https://docs.github.com/en/enterprise-server@3.21/admin/all-releases)
as of 2026-09-24.

| GHES line | GitHub support closes | Minimum runner | APEX qualification |
| --- | --- | --- | --- |
| 3.21 | 2027-06-11 | 2.331.0 | Target; drill pending |
| 3.20 | 2027-03-17 | 2.330.0 | Target; drill pending |
| 3.19 | 2026-12-09 | 2.328.0 | Target; drill pending |
| 3.18 | 2026-10-14 | 2.324.0 | Target; drill pending |

Do not advertise any row as compatible while its status is `Target; drill
pending`. Remove a line from the target matrix after its closing date unless a
documented exception is approved; unsupported legacy releases are out of scope.
3.17 closed 2026-08-25 and was removed on 2026-09-24 (no exception recorded);
3.18 closes 2026-10-14 and is the next row due for removal.

### Capability matrix

| Capability | GitHub.com | GHES profile |
| --- | --- | --- |
| Actions runners | Authoritative CI uses the hard-coded GitHub-hosted `ubuntu-24.04` image. | Authoritative CI uses only dedicated ephemeral runners carrying the hard-coded `apexload-ci-unprivileged` label. Trusted release/deployment runners use separate groups and labels. |
| Repository/server API | `GITHUB_SERVER_URL` and `GITHUB_API_URL` | Derive both from runtime variables; never hard-code `github.com` or `api.github.com`. |
| Controller OIDC issuer | GitHub.com Actions issuer | Trust the configured `https://HOSTNAME/_services/token` issuer and exact audience. Validate individual repository, ref, workflow, environment, runner, expiry, and run claims. |
| Controller auth fallback | Scoped API key only when explicitly selected | Expiring `ci_runner` API key when the GHES release/installation cannot provide usable OIDC. The controller verifies the exact workflow run through its GitHub App; policies requiring job environment or runner claims remain OIDC-only. |
| Release/action assets | Public Release assets | Mirror release assets to an internal HTTPS endpoint without changing bytes or names. Verify `SHA256SUMS.sig` after mirroring. |
| Workflow actions | Public actions pinned to full commit SHAs | Mirror every action internally at the reviewed commit or pin an internal OCI action by digest. |
| Workflow artifacts | `upload-artifact@v4` / `download-artifact@v4` | Checked-in wrappers select pinned v3-node20 clients on GHES because `v4+` is not supported there. Mirror those exact commits internally. |
| Hosted artifact attestations | Optional. Set `APEXLOAD_ENABLE_HOSTED_ATTESTATIONS=true` only on a plan/repository where the feature is available, and provide a protected fine-grained or GitHub App token in `APEXLOAD_GITHUB_ATTESTATIONS_TOKEN` with attestation write access. Portable OpenSSL/Cosign signatures remain authoritative. | Disabled. The portable release workflow deliberately omits the `attestations` workflow-permission key because supported GHES lines may reject that key while parsing the workflow. Use KMS/HSM-backed portable signatures and mirror the signed bytes unchanged. |
| Azure federation | GitHub issuer reachable by Entra | If Entra cannot reach the private issuer, use managed identity from an Azure-hosted ephemeral runner. Do not introduce long-lived Azure client secrets. |
| Private PKI/proxy | Public roots | Install the enterprise CA in runner, Git, container, Go, Node, Azure CLI, and controller trust stores; configure `HTTPS_PROXY`/`NO_PROXY` explicitly. |

### Mirroring procedure

For every supported workflow revision:

1. Mirror each external action at its exact 40-character commit. Do not mirror a
   moving major tag as if it were immutable. Record upstream repository, commit,
   internal reference, review ticket, and mirror checksum.
2. Mirror tool archives and checksums used by CI. The internal download service
   must preserve filenames and bytes.
3. Mirror the complete APEX Load Release, including
   `release-manifest.json`, `SHA256SUMS`, raw `SHA256SUMS.sig`, every `apexctl`
   platform asset, chart, App Service zip, desktop clients, and SBOMs.
4. Verify the OpenSSL signature and every downloaded checksum from the internal
   endpoint. Configure the canonical action's `release-base-url` to the mirror
   root and supply the same protected `release-public-key`.
5. Copy images by manifest digest. Verify portable Cosign signatures and their
   Rekor transparency-log entries before and after the copy; if the registry
   rewrites the manifest digest or Rekor is unreachable, fail rather than
   silently trusting the tag. Fully disconnected environments must mirror the
   required Rekor evidence instead of disabling transparency-log verification.
6. Store portable private keys in enterprise KMS/HSM. Runners receive only a
   short-lived signing session and never write exportable private keys to an
   artifact.

The checked-in `.github/actions/upload-artifact`, `download-artifact`, and
`download-release-inputs` wrappers choose v4 on GitHub.com and the pinned
v3-node20 clients on GHES. No out-of-tree patch is needed. An air-gapped mirror
must preserve the `actions/upload-artifact` and `actions/download-artifact`
owner/repository coordinates at the reviewed commits, or the workflow and
`.github/approved-actions.json` must be changed together under CODEOWNERS review.
The canonical APEX action emits JSON/JUnit paths and intentionally does not
choose an uploader for consumer repositories.

### Network and runner topology

- Release, deployment, infrastructure, and protected manual protocol automation
  is started through a narrowly scoped `repository_dispatch` credential. This
  guarantees GHES loads the privileged workflow from protected `main`.
  Release/deployment candidate tags are checked out only after their exact
  annotated tag objects and commits have been verified through the GHES API;
  infrastructure and protocol workflows pin the immutable default-branch event
  SHA and reject unexpected payload keys. Do not replace this with tag-push
  execution or branch-selectable `workflow_dispatch`.
- Controller-backed load gates run on trusted same-repository refs. On
  GitHub.com, fork pull requests receive only the authoritative CI workflow's
  `fork-static` syntax validation job on an ephemeral GitHub-hosted runner; it
  never executes checked-out source, tests, binaries, or scripts.
- GHES must disable Actions execution from fork pull requests. GHES has no
  hosted-runner isolation boundary, and a fork can modify the workflow version
  associated with its merge commit; assigning even a nominally unprivileged
  self-hosted runner would violate the no-untrusted-code-on-self-hosted-runner
  rule. A maintainer must import the proposed commit onto a same-repository
  review branch before `CI / Required` can run. The checked-in `fork-static`
  job is therefore GitHub.com-only and has no GHES self-hosted fallback.
- Provision `apexload-ci-unprivileged` as an ephemeral, one-job GHES runner
  pool for trusted same-repository CI. Give it no runner-local credentials or
  persistent caches and limit egress to reviewed source/tool/module mirrors.
- Restrict every trusted self-hosted runner group to explicitly selected
  workflows pinned to protected branches, protected tags, or reviewed SHAs.
  Do not grant those groups generic repository access or access from pull
  request merge refs. This platform control is required because a pull request
  can modify the workflow version associated with its merge commit. See
  GitHub's documentation for [runner-group workflow restrictions](https://docs.github.com/en/actions/how-tos/manage-runners/self-hosted-runners/manage-access)
  and [event-associated workflow versions](https://docs.github.com/en/actions/concepts/workflows-and-actions/workflows).
- Never use `pull_request_target` to check out or execute contributor-controlled
  content.
- Put private-controller runners on a segment that can reach the controller but
  not production targets outside the project's allowlist.
- Use a distinct ephemeral runner group for Azure private endpoints and a
  separate internal registry group for on-premises publishing.
- Pin the base runner image by digest and verify required `skopeo`, Azure CLI,
  Helm, kubectl, OpenSSL, jq, and CA bundle versions during runner bootstrap.
- The base image also needs GitHub CLI, GNU coreutils/find, Bash 5, Python 3,
  Node 22, Docker, Buildx 0.34.1, Cosign, Go 1.26.10, JDK 21, npm, Terraform 1.15.8,
  and the platform tools used by its lane. Trivy/Syft, Azure CLI, native Wails
  dependencies, and browser dependencies are required only by their matching
  scan/deploy/client/dashboard lanes. Fail runner-image validation if an exact
  required version is absent.
- Configure `GITHUB_SERVER_URL`, `GITHUB_API_URL`, the GHES OIDC issuer, CA bundle,
  and proxy as installation data. Do not derive trust from repository input.
- Set the protected environment variable `AZURE_AUTH_MODE=managed-identity` only
  for Azure-hosted ephemeral GHES runners whose user-assigned identity matches
  the workflow's environment-specific client ID. Leave it unset for Actions
  OIDC federation.
- The authoritative `CI` workflow intentionally does not accept a
  `CI_RUNNER_LABELS` override. Set runner labels as JSON arrays only for the
  non-PR security, integration, release, and deployment workflows:
  `SECURITY_RUNNER_LABELS`, `PROTOCOL_RUNNER_LABELS`,
  `RELEASE_LINUX_RUNNER_LABELS`, `RELEASE_LINUX_AMD64_RUNNER_LABELS`,
  `RELEASE_LINUX_ARM64_RUNNER_LABELS`, `RELEASE_MACOS_ARM64_RUNNER_LABELS`,
  `RELEASE_WINDOWS_AMD64_RUNNER_LABELS`,
  `AKS_RUNNER_LABELS`, `APPSERVICE_RUNNER_LABELS`, and
  `AZURE_INFRA_RUNNER_LABELS`. Example: `["self-hosted","linux","x64","trusted-ci"]`.
- Set protected repository variable `APEXLOAD_USE_PREINSTALLED_TOOLS=true` on
  air-gapped release, AKS, and Terraform runners. Network installer steps are
  skipped, but the workflows still verify exact Helm 3.21.3, kubectl 1.34.9,
  Buildx 0.34.1, BuildKit 0.31.1, Skopeo 1.20.0, Cosign 3.1.1, and Terraform
  1.15.8 versions before using them. Release builders additionally verify that
  the running BuildKit container uses the reviewed immutable image digest.
  On-premises publishing always requires preinstalled Skopeo 1.20.0 and Cosign
  3.1.1.
- Mirror `docker.io/moby/buildkit@sha256:6b59b7df63a8cb9902736f9ddf7fcff8261613d3e7449b8ea8b7537fc399c03a`
  without changing its digest and set protected variable
  `APEXLOAD_BUILDKIT_IMAGE` to the mirrored digest reference. Release jobs
  reject tag-only, changed-digest, or wrong-version BuildKit images before
  starting a privileged builder. Use the release registry for this mirror or
  pre-authenticate the ephemeral runner to a separately hosted mirror.
- Package-ecosystem traffic must use an approved transparent proxy/cache, or a
  pre-baked lane image with every tool and dependency already mirrored. Configure
  `GOPROXY`, npm registry/CA settings, Git, Docker registries, pip, apt/brew,
  and proxy variables at runner bootstrap. The active workflows still verify
  pinned tool checksums; a mirror must preserve those bytes. No public fallback
  is permitted in an air-gapped qualification run.
- Repository variables `GO_MODULE_PROXY`, `NPM_REGISTRY_URL`,
  `PLAYWRIGHT_DOWNLOAD_HOST`, `ACTIONLINT_RELEASE_BASE_URL`,
  `SHELLCHECK_RELEASE_BASE_URL`, `ZIZMOR_WHEEL_URL`,
  `TERRAFORM_RELEASE_BASE_URL`, `KUBECTL_RELEASE_BASE_URL`,
  `BUILDX_RELEASE_BASE_URL`,
  `HELM_RELEASE_BASE_URL`, `KUBECONFORM_RELEASE_BASE_URL`,
  `SKOPEO_RELEASE_BASE_URL`, and `NSIS_RELEASE_BASE_URL` redirect the
  explicitly downloaded ecosystems/tools. Set `RELEASE_PREBAKED_NATIVE_DEPS=true`
  when Wails native dependencies are already sealed into the release runner.
  Windows preinstalled-tool lanes must also set `NSIS_ARCHIVE_PATH` to the
  sealed official NSIS 3.10 zip; the workflow verifies its reviewed SHA-256 and
  extracts a job-local toolchain before use.

### GHES qualification gate

A GHES release line is compatible only after a recorded staging drill proves:

1. a protected signed semantic tag produces a fully signed Release through the
   internally mirrored actions;
2. a consumer fixture with no APEX source or Go toolchain downloads `apexctl`
   from the internal mirror and completes a controller-backed run;
3. valid GHES OIDC succeeds and wrong issuer/audience/repository/ref/workflow,
   expired, replayed, rotated-key, disabled-policy, and old-policy-revision tokens
   fail; or the scoped API-key fallback is rotated and expiry-tested;
4. a staging deployment consumes the signed release manifest and exact image
   digests;
5. an induced AKS/App Service failure restores the prior release; and
6. JSON, JUnit, Check lifecycle, SBOM, signature, and deployment evidence can be
   exported for audit.

Record the GHES version, runner image digest, action mirror mapping, controller
version, and drill evidence URL in the release qualification record.

## Release evidence manifest

This manifest links competitive claims to artifacts that can be reproduced for a release candidate.

### Required Evidence Before Raising Claims

| Claim area | Required artifact |
| --- | --- |
| HTTP density | Raw generator benchmark output with commit SHA, CPU, memory, container image, protocol mix, VUsers, duration, errors, and p95/p99. |
| Browser canary envelope | Raw output from `docs/PERFORMANCE_BENCHMARKS.md` profile commands for the exact generator image. |
| Load zones | `/api/v1/fleet/load-zones` snapshot plus generator inventory showing zone region, capacity, protocol compatibility, and benchmark evidence id. |
| CI/CD gating | JUnit XML and JSON output from `apexctl run`, `status`, or `wait`. |
| Migration import | Import response archive with warnings, unsupported elements, source review, and generated DSL validation output. |

### Current Code-Owned Evidence

| Evidence | Location |
| --- | --- |
| Evaluator seed pack | `scripts/seed-evaluator-demo.sh` |
| Evaluator one-pager | `docs/COMPETITIVE_COMPARISON.md#one-page-summary` |
| Browser benchmark artifact contract, harness overhead benchmark, and versioned local performance component evidence | `docs/PERFORMANCE_BENCHMARKS.md` and `scripts/run-performance-evidence.sh` |
| CI/CD integration | `docs/CICD_INTEGRATION.md` |
| Competitive support matrix | `docs/COMPETITIVE_COMPARISON.md` |

### Claim Discipline

Do not promote a `Partial` or `Evidence required` item to a customer-facing `Supported` claim unless the manifest contains reproducible artifacts for that release candidate. Keep high-density browser load, managed SaaS control plane, and unsupported legacy desktop protocols out of marketing copy unless a product decision changes scope.
