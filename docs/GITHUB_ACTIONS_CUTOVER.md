# GitHub Actions Cutover

GitHub Actions is the sole target CI/CD system for APEX Load. The cutover is
complete only after observed parity, not merely after workflow files merge.

1. Establish a clean `main` baseline and run the authoritative `CI` workflow on
   five representative documentation, Go, dashboard, deployment, and protocol
   changes. Resolve flakes before enforcement.
2. Configure the `main` ruleset and merge queue to require only `CI / Required`.
   This stable aggregate prevents branch-protection drift as internal jobs
   evolve. Keep the default `GITHUB_TOKEN` read-only.
3. Publish a signed `v1.0.0-rc.1`, then invoke the canonical
   `owner/repository/.github/actions/apexload-run@v1.0.0-rc.1` from a separate
   fixture repository with no APEX source and no Go toolchain.
4. Complete GitHub.com and GHES staging release, controller-backed execution,
   deployment, evidence, and rollback drills described in the operations and
   compatibility runbooks.
5. Publish the signed stable release, move the protected signed `v1` action tag,
   and activate production environment reviewers and tag restrictions.
6. Confirm no workflow, documentation example, or deployment path invokes
   Jenkins. Dormant copy-to-activate GitHub deployment templates and the Jenkins
   example were removed when the active workflows replaced them.

Fork pull requests remain untrusted. The authoritative workflow routes them to
only `fork-static`, which runs patch-whitespace checks and changed JSON/DSL
syntax validation with runner-provided `git` and `jq` only; it does not execute
checked-out scripts, binaries, tests, or source. All full CI lanes are required
to be skipped. `CI / Required` verifies that exact
success/skipped contract instead of treating arbitrary skipped jobs as success.
Same-repository pull requests, including Dependabot dependency updates, still
receive full CI.

GitHub.com uses only the hard-coded ephemeral `ubuntu-24.04` hosted runner for
authoritative CI. GHES must provide a dedicated ephemeral runner pool with the
hard-coded `apexload-ci-unprivileged` label; absence of that pool fails the GHES
readiness review and never falls back to a trusted runner. Trusted self-hosted
runner groups must be restricted at the organization/enterprise layer to
selected workflows pinned to protected refs so a fork-modified workflow cannot
request them. Controller-backed performance gates require a trusted
same-repository ref or a protected manual workflow with independent approval.
`pull_request_target` is prohibited for any workflow that checks out or
executes repository content.

Operational repository/environment configuration is deliberately outside Git:
required reviewers, prevent-self-review, protected-tag deployment restrictions,
OIDC federated credentials, GitHub App installation permissions, rulesets, and
KMS/HSM identities must be configured and audited by repository/platform
administrators before production activation.
