# GitHub Actions Release and Deployment Operations

APEX Load releases and deployments are owned by the active workflows under
`.github/workflows`. Deployment workflows never compile application code. They
accept an exact semantic release tag, verify its portable signature, read the
schema-v1 release manifest, and deploy only the assets and image digests named
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
initiate the run, prevent self-review, restrict deployment to protected release
tags, and disable routine administrator bypass. Terraform environments use
protected `main` instead: a plan-only dispatch creates an authenticated handoff
and a later apply dispatch receives a fresh approval. Use workflow concurrency
as an additional guard; do not configure environment rules that permit
overlapping production deployments.

The repository token remains read-only by default. Individual release jobs
receive `contents: write` or `packages: write` only where publication requires
it. Azure access uses environment-specific OIDC identities rather than client
secrets.

### Reviewed active-run deployment override

AKS and App Service deployments fail closed when the controller reports active
tests. An exceptional deployment may proceed only when the target protected
environment supplies the environment secret
`APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL`. Do not define that name as a repository
or organization secret, and do not add a `workflow_dispatch` input for it. The
workflow intentionally accepts only `release` and `environment`; the protected
environment releases the approval after its independent reviewer approves the
deployment job.

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
the future when the preflight runs, and be no more than four hours away. Remove
the secret immediately after the approved deployment. The preflight validates
the entire document but never prints it or its reason. It emits one JSON audit
line containing only the non-sensitive approval ID, active-test count, exact
release/environment, initiating actor, workflow run URL/attempt, and expiry;
the protected-environment deployment record remains the authority for reviewer
identity. Invalid, expired, mismatched, or absent approvals do not bypass the
zero-active-test invariant. Because the Actions `secrets` context does not
report a secret's scope, activation review must also verify that no repository-
or organization-level secret with this name exists.

## Release trust roots and credentials

Configure the following reviewed variables and secrets. Public keys are
variables so operators and consumers can compare them out of band; private keys
must be environment secrets or KMS/HSM key references.

| Scope | Name | Meaning |
| --- | --- | --- |
| Repository variable | `APEXLOAD_RELEASE_TAG_PUBLIC_KEYS` | Armored GPG public keys trusted to sign semantic release tags. |
| Repository variables | `APEXLOAD_RELEASE_REGISTRY`, `APEXLOAD_RELEASE_REGISTRY_NAMESPACE` | Release registry host and namespace. Defaults are `ghcr.io` and the repository owner; GHES uses its internal mirror coordinates. |
| `registry-release` variable | `APEXLOAD_RELEASE_SIGNING_PUBLIC_KEY_PEM` | OpenSSL public key that verifies raw `SHA256SUMS.sig`. Bundle the same key as `release-signing-key.pem` in protected action releases or supply it explicitly to the action. |
| `registry-release` variable/secret | `APEXLOAD_RELEASE_SIGNER_PATH` or `APEXLOAD_RELEASE_SIGNING_KEY_PEM` | Prefer an absolute protected-runner executable that asks KMS/HSM to sign `SHA256SUMS` and writes an OpenSSL-verifiable signature. The exportable PEM is the fallback. |
| `registry-release` variable/secret | `COSIGN_PUBLIC_KEY` plus `COSIGN_KEY_REF` or `COSIGN_PRIVATE_KEY`/`COSIGN_PASSWORD` | Portable image signature key pair. Prefer a supported KMS/HSM URI in `COSIGN_KEY_REF`; the exportable secret-key form is a fallback. |
| `registry-release` variable/secret | `APEXLOAD_RELEASE_TAG_SIGNING_KEY_ID`, `APEXLOAD_RELEASE_TAG_SIGNER_MODE`, `APEXLOAD_RELEASE_TAG_GNUPGHOME`, `APEXLOAD_RELEASE_TAG_PRIVATE_KEY` | Prefer `preinstalled` mode with a protected GPG agent/HSM keyring; the exportable private-key secret is the fallback. |
| `client-release` secrets | `APEXLOAD_MACOS_*`, `APEXLOAD_WINDOWS_*` | Developer ID, notarization, and Authenticode identities. All are mandatory; release jobs fail closed. |
| `onprem-registry` variable/secret | `ONPREM_COSIGN_PUBLIC_KEY` plus `ONPREM_COSIGN_KEY_REF` or `ONPREM_COSIGN_PRIVATE_KEY`/`ONPREM_COSIGN_PASSWORD` | Internal portable digest signer; use the KMS/HSM reference form in production. |

The protected semantic tag must be annotated, GPG-signed, match
`vMAJOR.MINOR.PATCH[-prerelease]`, and point to a commit reachable from `main`.
The canonical action's `apexctl-version` must already equal that tag. Update it
in the release PR; the release job will not rewrite source after tagging.

`release.yml` performs the authoritative reusable CI workflow, freezes build
metadata once from the tag commit time, and creates an empty draft Release. It
then:

1. Builds five standalone `apexctl` binaries with the asset names consumed by
   the canonical action.
2. Packages Linux controller-side tools, the App Service zip, Helm chart, and
   signed desktop clients.
3. Builds standard and enterprise controller/generator images once for amd64
   and arm64, generates SPDX SBOMs, blocks HIGH/CRITICAL findings, signs their
   exact multi-platform digests, and optionally emits GitHub-hosted
   attestations.
4. Creates `release-manifest.json` schema version 1, a single `SHA256SUMS`, and
   the raw detached OpenSSL SHA-256 signature `SHA256SUMS.sig`.
5. Verifies every promised asset and signature before uploading to the draft.
   A retry may reuse only the marker-bound draft and exact already-promoted
   image digests. Existing mismatched bytes or tags fail closed.
6. Downloads the bytes stored in the draft, compares the exact asset set,
   reverifies the portable signature/checksums, and only then publishes it.
   Stable releases then move the
   protected, signed `v1` action tag. Prereleases do not move `v1`.

No workflow publishes `latest`.

## AKS and on-premises Kubernetes

`deploy-aks.yml` accepts only `release` and `environment`. Environment variables
provide cluster coordinates, namespace, Helm release name, controller URL, and
the protected smoke project/script. `APEXLOAD_HELM_VALUES_YAML` supplies reviewed
environment values, while Kubernetes Secrets continue to hold runtime secrets.

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
The workflow treats `ONPREM_REGISTRY_HOST` and
`ONPREM_REGISTRY_NAMESPACE` as different values. It mirrors release-manifest
digests to non-release candidate tags, scans and signs the exact internal
digests, waits for all four component/profile lanes, then exposes semantic
release tags. A release tag is never visible before all candidates verify.

## App Service

`deploy-appservice.yml` verifies the released zip and its required launcher,
package, and controller entries. It deploys only to the configured staging slot,
then checks health, release commit, dashboard assets, live generator capacity,
and a full RFC 6455 WebSocket upgrade. It checks the zero-active-test invariant
before deployment and again immediately before swapping; both checks require
the same protected-environment approval when active tests remain. Production is
swapped only after staging succeeds.
Production receives the same checks plus an authenticated execution smoke. A
failed post-swap check records durable swap state, swaps the former production
package back even when the new production health endpoint is unreachable, and
revalidates its commit. The App Service plan must support deployment slots.

The active workflow requires an already healthy production baseline so it can
prove rollback. Bootstrap a new App Service through the infrastructure/onboarding
runbook, validate its baseline controller, and only then enable this release
workflow; a first-ever unverified production slot is intentionally unsupported.

## Terraform

`infra-azure.yml` is separate from application deployment. Dispatch
`operation=plan` from protected `main`; the plan-only identity creates a saved
plan whose ciphertext and source/environment/run metadata are authenticated and
retained for seven days. After review, start a separate dispatch with
`operation=apply`, `plan-run-id`, and `plan-run-attempt`. The apply identity
downloads only that successful plan-run artifact, verifies its HMAC, age,
Terraform version, source commit, repository, and environment, confirms the
commit remains in protected-main history, decrypts and checksums it, and applies
without replanning. Configure least-privilege roles for both identities and use
a private, ephemeral runner when the state account or Azure resources expose
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
