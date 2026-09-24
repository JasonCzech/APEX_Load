# GitHub Enterprise Server Compatibility

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

## Capability matrix

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

## Mirroring procedure

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

## Network and runner topology

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
  Node 22, Docker, Buildx 0.34.1, Cosign, Go 1.26.5, JDK 21, npm, Terraform 1.15.8,
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
  `RELEASE_LINUX_ARM64_RUNNER_LABELS`, `RELEASE_MACOS_AMD64_RUNNER_LABELS`,
  `RELEASE_MACOS_ARM64_RUNNER_LABELS`, `RELEASE_WINDOWS_AMD64_RUNNER_LABELS`,
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

## GHES qualification gate

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
