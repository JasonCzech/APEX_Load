#!/usr/bin/env python3
"""Fail closed on high-risk GitHub Actions workflow patterns.

actionlint and zizmor provide the semantic analysis. This small policy layer
enforces the repository-specific invariants that must not regress silently.
"""

from __future__ import annotations

import pathlib
import json
import os
import re
import sys


ROOT = pathlib.Path(
    os.environ.get("APEXLOAD_WORKFLOW_POLICY_ROOT", pathlib.Path(__file__).resolve().parents[2])
).resolve()
GITHUB = ROOT / ".github"
WORKFLOWS = GITHUB / "workflows"
SHA_REF = re.compile(r"^[^\s@]+/[^\s@]+(?:/[^\s@]+)*@[0-9a-fA-F]{40}(?:\s+#.*)?$")
PARSED_SHA_REF = re.compile(
    r"^(?P<uses>[^\s@]+/[^\s@]+(?:/[^\s@]+)*)@(?P<sha>[0-9a-fA-F]{40})\s+#\s+(?P<version>\S+)$"
)
LOCAL_REF = re.compile(r"^\./")
JOB_ID = re.compile(r"^  ([A-Za-z_][A-Za-z0-9_-]*):\s*(?:#.*)?$")
MINIMUM_ACTION_MAJOR = {
    "actions/checkout": 4,
    "actions/dependency-review-action": 4,
    "actions/download-artifact": 4,
    "actions/setup-go": 5,
    "actions/setup-java": 4,
    "actions/setup-node": 4,
    "actions/upload-artifact": 4,
    "azure/setup-helm": 4,
    "azure/setup-kubectl": 4,
    "docker/build-push-action": 6,
    "docker/login-action": 3,
    "docker/setup-buildx-action": 3,
    "docker/setup-qemu-action": 4,
    "golangci/golangci-lint-action": 8,
}
GHES_ARTIFACT_WRAPPERS = {
    pathlib.Path(".github/actions/upload-artifact/action.yml"),
    pathlib.Path(".github/actions/download-artifact/action.yml"),
    pathlib.Path(".github/actions/download-release-inputs/action.yml"),
}
CI_FULL_JOB_IDS = (
    "workflow-policy",
    "go-quality",
    "go-tests",
    "build",
    "protocol-tags",
    "dashboard",
    "deployment-artifacts",
    "containers",
    "govulncheck",
    "codeql",
)
CI_FULL_PR_GUARD = (
    "if: github.event_name != 'pull_request' || "
    "(github.event.pull_request.head.repo.id == github.event.repository.id && "
    "github.event.pull_request.head.repo.full_name == github.repository)"
)
CI_DEPENDENCY_REVIEW_GUARD = (
    "if: github.event_name == 'pull_request' && "
    "github.event.pull_request.head.repo.id == github.event.repository.id && "
    "github.event.pull_request.head.repo.full_name == github.repository"
)
CI_FORK_PR_GUARD = (
    "if: github.server_url == 'https://github.com' && "
    "github.event_name == 'pull_request' && "
    "(github.event.pull_request.head.repo.id != github.event.repository.id || "
    "github.event.pull_request.head.repo.full_name != github.repository)"
)
CI_FORK_HOSTED_RUNNER = "runs-on: ubuntu-24.04"
CI_GHES_FORK_BLOCK_GUARD = (
    "if: github.server_url != 'https://github.com' && "
    "github.event_name == 'pull_request' && "
    "(github.event.pull_request.head.repo.id != github.event.repository.id || "
    "github.event.pull_request.head.repo.full_name != github.repository)"
)
CI_GHES_FORK_BLOCK_RUNNER = "runs-on: apexload-ci-fork-blocked"
TRUSTED_MAIN_GUARD = "if: github.ref == 'refs/heads/main'"
PRIVILEGED_REPOSITORY_DISPATCH_EVENTS = {
    pathlib.Path(".github/workflows/infra-azure.yml"): (
        "azure-infra-plan",
        "azure-infra-apply",
    ),
    pathlib.Path(".github/workflows/protocol-integration.yml"): (
        "protocol-integration",
    ),
}
CI_UNPRIVILEGED_RUNNER = (
    "runs-on: ${{ github.server_url == 'https://github.com' && "
    "'ubuntu-24.04' || 'apexload-ci-unprivileged' }}"
)


def load_approved_actions(errors: list[str]) -> set[tuple[str, str, str]]:
    path = GITHUB / "approved-actions.json"
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        errors.append(f".github/approved-actions.json: cannot load reviewed action allowlist: {error}")
        return set()
    if document.get("schema_version") != 1 or not isinstance(document.get("actions"), list):
        errors.append(".github/approved-actions.json: unsupported allowlist schema")
        return set()
    approved: set[tuple[str, str, str]] = set()
    for row in document["actions"]:
        if not isinstance(row, dict):
            errors.append(".github/approved-actions.json: each action must be an object")
            continue
        value = (row.get("uses"), row.get("sha"), row.get("version"))
        if not all(isinstance(item, str) and item for item in value) or not re.fullmatch(r"[0-9a-f]{40}", value[1]):
            errors.append(f".github/approved-actions.json: invalid action record: {row!r}")
            continue
        if value in approved:
            errors.append(f".github/approved-actions.json: duplicate action record: {value[0]}@{value[1]}")
        approved.add(value)
    return approved


def check_uses(
    path: pathlib.Path,
    lines: list[str],
    errors: list[str],
    approved: set[tuple[str, str, str]],
) -> None:
    for number, line in enumerate(lines, 1):
        match = re.match(r"^\s*(?:-\s*)?uses:\s*([^#]+(?:#.*)?)$", line)
        if not match:
            continue
        reference = match.group(1).strip()
        if LOCAL_REF.match(reference):
            continue
        if reference.startswith("docker://"):
            if "@sha256:" not in reference:
                errors.append(
                    f"{path.relative_to(ROOT)}:{number}: container actions must use an immutable sha256 digest: {reference}"
                )
            continue
        if not SHA_REF.fullmatch(reference):
            errors.append(
                f"{path.relative_to(ROOT)}:{number}: external action must use a full 40-character commit SHA: {reference}"
            )
            continue
        parsed = PARSED_SHA_REF.fullmatch(reference)
        if not parsed:
            errors.append(
                f"{path.relative_to(ROOT)}:{number}: pinned external action must include its reviewed version comment"
            )
            continue
        action = parsed.group("uses")
        sha = parsed.group("sha").lower()
        version = parsed.group("version")
        if (action, sha, version) not in approved:
            errors.append(
                f"{path.relative_to(ROOT)}:{number}: external action is not in .github/approved-actions.json: {action}@{sha} # {version}"
            )
        version_match = re.match(r"^v(?P<major>[0-9]+)(?:[.-]|$)", version)
        minimum = MINIMUM_ACTION_MAJOR.get(action)
        relative_path = path.relative_to(ROOT)
        ghes_artifact_exception = (
            action in {"actions/upload-artifact", "actions/download-artifact"}
            and relative_path in GHES_ARTIFACT_WRAPPERS
            and version_match is not None
            and int(version_match.group("major")) == 3
        )
        if minimum is not None and (
            version_match is None or int(version_match.group("major")) < minimum
        ) and not ghes_artifact_exception:
            errors.append(
                f"{relative_path}:{number}: obsolete {action} generation {version}; require v{minimum}+"
            )
        if action in {"actions/upload-artifact", "actions/download-artifact"} and relative_path not in GHES_ARTIFACT_WRAPPERS:
            errors.append(
                f"{relative_path}:{number}: use the checked-in platform-compatible artifact wrapper"
            )


def check_run_expressions(path: pathlib.Path, lines: list[str], errors: list[str]) -> None:
    index = 0
    while index < len(lines):
        line = lines[index]
        match = re.match(r"^(\s*)(?:-\s*)?run:\s*(.*)$", line)
        if not match:
            index += 1
            continue
        indent = len(match.group(1))
        value = match.group(2)
        if "${{" in value:
            errors.append(
                f"{path.relative_to(ROOT)}:{index + 1}: pass GitHub expressions through env instead of interpolating them into run"
            )
        if value.strip() in {"|", ">", "|-", ">-", "|+", ">+"}:
            cursor = index + 1
            while cursor < len(lines):
                candidate = lines[cursor]
                if candidate.strip() and len(candidate) - len(candidate.lstrip()) <= indent:
                    break
                if "${{" in candidate:
                    errors.append(
                        f"{path.relative_to(ROOT)}:{cursor + 1}: pass GitHub expressions through env instead of interpolating them into run"
                    )
                cursor += 1
            index = cursor
        else:
            index += 1


def workflow_job_blocks(lines: list[str]) -> dict[str, tuple[int, list[str]]]:
    in_jobs = False
    jobs: list[tuple[str, int, int]] = []
    for index, line in enumerate(lines):
        if line == "jobs:":
            in_jobs = True
            continue
        if in_jobs and line and not line.startswith((" ", "#")):
            break
        if not in_jobs:
            continue
        match = JOB_ID.match(line)
        if match:
            jobs.append((match.group(1), index, len(lines)))
    for position in range(len(jobs) - 1):
        name, start, _ = jobs[position]
        next_start = jobs[position + 1][1]
        jobs[position] = (name, start, next_start)
    return {name: (start, lines[start:end]) for name, start, end in jobs}


def check_job_timeouts(path: pathlib.Path, lines: list[str], errors: list[str]) -> None:
    jobs = workflow_job_blocks(lines)
    for name, (start, block) in jobs.items():
        # A reusable-workflow call has no timeout-minutes field in GitHub's
        # schema; its called jobs must carry their own timeouts.
        if any(re.match(r"^    uses:\s*", line) for line in block):
            continue
        if not any(re.match(r"^    timeout-minutes:\s*[1-9][0-9]*\s*(?:#.*)?$", line) for line in block):
            errors.append(
                f"{path.relative_to(ROOT)}:{start + 1}: job {name!r} must declare timeout-minutes"
            )


def check_authoritative_ci_fork_policy(
    path: pathlib.Path,
    lines: list[str],
    errors: list[str],
) -> None:
    if path.relative_to(ROOT) != pathlib.Path(".github/workflows/ci.yml"):
        return

    jobs = workflow_job_blocks(lines)

    def require_job(job_id: str) -> tuple[int, list[str]] | None:
        job = jobs.get(job_id)
        if job is None:
            errors.append(f".github/workflows/ci.yml: missing required job {job_id!r}")
        return job

    def require_line(job_id: str, expected: str, purpose: str) -> None:
        job = require_job(job_id)
        if job is None:
            return
        start, block = job
        if not any(line.strip() == expected for line in block):
            errors.append(
                f".github/workflows/ci.yml:{start + 1}: job {job_id!r} must {purpose}: {expected}"
            )

    text = "\n".join(lines)
    if "CI_RUNNER_LABELS" in text:
        errors.append(
            ".github/workflows/ci.yml: PR-capable authoritative CI must not select configurable trusted runner labels"
        )

    require_line("fork-static", CI_FORK_PR_GUARD, "run only for untrusted pull requests")
    require_line(
        "fork-static",
        CI_FORK_HOSTED_RUNNER,
        "use only a GitHub.com hosted runner with no GHES self-hosted fallback",
    )
    fork_job = require_job("fork-static")
    if fork_job is not None:
        start, block = fork_job
        fork_text = "\n".join(block)
        for marker in (
            "git diff --check",
            "git diff --name-only --diff-filter=ACMRT -z",
            "jq empty --",
        ):
            if marker not in fork_text:
                errors.append(
                    f".github/workflows/ci.yml:{start + 1}: fork-static is missing non-executing validation marker {marker!r}"
                )
        for marker in (
            "git diff --no-renames --name-only -z",
            'git ls-tree "${HEAD_SHA}" -- "${file}"',
            'disallowed+=("${file}")',
            "Fork pull requests may change only documentation and static JSON examples.",
        ):
            if marker not in fork_text:
                errors.append(
                    f".github/workflows/ci.yml:{start + 1}: fork-static is missing static-only fork change boundary marker {marker!r}"
                )
        for prohibited in (
            "go test",
            "go run",
            "go build",
            "npm ",
            "pnpm ",
            "yarn ",
            "make ",
            "./scripts/",
        ):
            if prohibited in fork_text:
                errors.append(
                    f".github/workflows/ci.yml:{start + 1}: fork-static must not execute contributor-controlled code ({prohibited.strip()!r})"
                )
        uses_lines = [line.strip() for line in block if re.match(r"^\s*(?:-\s*)?uses:", line)]
        expected_checkout = (
            "uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1"
        )
        if uses_lines != [expected_checkout]:
            errors.append(
                f".github/workflows/ci.yml:{start + 1}: fork-static may use only the reviewed checkout action"
            )

    for job_id in CI_FULL_JOB_IDS:
        require_line(job_id, CI_FULL_PR_GUARD, "skip fork pull requests")
        require_line(
            job_id,
            CI_UNPRIVILEGED_RUNNER,
            "use the explicit GitHub.com/GHES unprivileged runner mapping",
        )

    require_line(
        "workflow-policy",
        '"${RUNNER_TEMP}/bin/actionlint" -color -shellcheck="${RUNNER_TEMP}/bin/shellcheck"',
        "run actionlint with the checksum-pinned ShellCheck binary for inline workflow scripts",
    )

    deployment_artifacts = require_job("deployment-artifacts")
    if deployment_artifacts is not None:
        start, block = deployment_artifacts
        deployment_text = "\n".join(block)
        if deployment_text.count("docker run --rm --entrypoint /bin/promtool") != 2:
            errors.append(
                f".github/workflows/ci.yml:{start + 1}: deployment-artifacts must override the pinned Prometheus image entrypoint for both promtool checks"
            )
        if '${PROMETHEUS_IMAGE}" promtool check' in deployment_text:
            errors.append(
                f".github/workflows/ci.yml:{start + 1}: deployment-artifacts must not pass promtool through the image's /bin/prometheus entrypoint"
            )

    require_line(
        "dependency-review",
        CI_DEPENDENCY_REVIEW_GUARD,
        "run only for trusted same-repository pull requests",
    )
    require_line(
        "dependency-review",
        CI_UNPRIVILEGED_RUNNER,
        "use the explicit GitHub.com/GHES unprivileged runner mapping",
    )
    require_line("ghes-fork-block", CI_GHES_FORK_BLOCK_GUARD, "run only for GHES fork pull requests")
    require_line(
        "ghes-fork-block",
        CI_GHES_FORK_BLOCK_RUNNER,
        "use the deliberately unprovisioned fail-closed runner label",
    )
    require_line("ghes-fork-block", "run: exit 1", "fail if the sentinel runner label is ever provisioned")
    ghes_fork_block = require_job("ghes-fork-block")
    if ghes_fork_block is not None:
        start, block = ghes_fork_block
        if any(re.match(r"^\s*(?:-\s*)?uses:", line) for line in block):
            errors.append(
                f".github/workflows/ci.yml:{start + 1}: ghes-fork-block must not check out or execute repository actions"
            )

    require_line("required", "if: always()", "always publish the stable aggregate")
    require_line(
        "required",
        CI_UNPRIVILEGED_RUNNER,
        "use the explicit GitHub.com/GHES unprivileged runner mapping",
    )

    required = require_job("required")
    if required is not None:
        start, block = required
        required_text = "\n".join(block)
        for marker in (
            "- fork-static",
            "- ghes-fork-block",
            "IS_UNTRUSTED_PR:",
            "expected_success=()",
            "expected_skipped=()",
            "ghes-fork-block",
        ):
            if marker not in required_text:
                errors.append(
                    f".github/workflows/ci.yml:{start + 1}: Required must enforce event-specific fork aggregation marker {marker!r}"
                )


def check_client_package_fork_policy(
    path: pathlib.Path,
    lines: list[str],
    errors: list[str],
) -> None:
    if path.relative_to(ROOT) != pathlib.Path(".github/workflows/desktop.yml"):
        return
    jobs = workflow_job_blocks(lines)
    for job_id in ("desktop", "recording-agent"):
        job = jobs.get(job_id)
        if job is None:
            errors.append(f".github/workflows/desktop.yml: missing required job {job_id!r}")
            continue
        start, block = job
        if any(line.strip() == CI_FULL_PR_GUARD for line in block):
            errors.append(
                f".github/workflows/desktop.yml:{start + 1}: credential-free job {job_id!r} must validate fork pull requests"
            )


def check_protected_scheduled_workflow_policy(
    path: pathlib.Path,
    lines: list[str],
    errors: list[str],
) -> None:
    relative = path.relative_to(ROOT)
    required_jobs = {
        pathlib.Path(".github/workflows/protocol-integration.yml"): ("live-protocol",),
        pathlib.Path(".github/workflows/security.yml"): ("govulncheck", "dashboard-audit"),
    }.get(relative)
    if required_jobs is None:
        return

    jobs = workflow_job_blocks(lines)
    for job_id in required_jobs:
        job = jobs.get(job_id)
        if job is None:
            errors.append(f"{relative}: missing protected scheduled job {job_id!r}")
            continue
        start, block = job
        if not any(line.strip() == TRUSTED_MAIN_GUARD for line in block):
            errors.append(
                f"{relative}:{start + 1}: job {job_id!r} must restrict protected scheduled/manual runs with {TRUSTED_MAIN_GUARD}"
            )


def check_privileged_repository_dispatch_policy(
    path: pathlib.Path,
    lines: list[str],
    errors: list[str],
) -> None:
    relative = path.relative_to(ROOT)
    required_events = PRIVILEGED_REPOSITORY_DISPATCH_EVENTS.get(relative)
    if required_events is None:
        return

    text = "\n".join(lines)
    if re.search(r"(?m)^\s*workflow_dispatch\s*:", text):
        errors.append(
            f"{relative}: privileged manual execution must use default-branch repository_dispatch, not workflow_dispatch"
        )
    if not re.search(r"(?m)^  repository_dispatch\s*:", text):
        errors.append(f"{relative}: missing required default-branch repository_dispatch trigger")
    for event_type in required_events:
        if not re.search(rf"(?m)^\s+- {re.escape(event_type)}\s*$", text):
            errors.append(f"{relative}: missing reviewed repository_dispatch event {event_type!r}")
    if "ref: ${{ github.sha }}" not in text:
        errors.append(f"{relative}: privileged checkout must pin the immutable default-branch event SHA")

    payload_contracts = {
        pathlib.Path(".github/workflows/infra-azure.yml"): (
            'keys == ["environment"]',
            'keys == ["environment", "plan_run_attempt", "plan_run_id"]',
        ),
        pathlib.Path(".github/workflows/protocol-integration.yml"): (
            "keys == []",
        ),
    }
    for contract in payload_contracts[relative]:
        if contract not in text:
            errors.append(f"{relative}: missing strict client_payload contract {contract!r}")


def check_workflow(
    path: pathlib.Path,
    errors: list[str],
    approved: set[tuple[str, str, str]],
) -> None:
    raw = path.read_text(encoding="utf-8")
    if "\t" in raw:
        errors.append(f"{path.relative_to(ROOT)}: YAML indentation must not contain tab characters")
    lines = raw.splitlines()
    text = "\n".join(lines)
    if re.search(r"(?m)^\s*pull_request_target\s*:", text):
        errors.append(f"{path.relative_to(ROOT)}: pull_request_target is prohibited")
    if re.search(r"(?m)^\s*permissions:\s*write-all\s*$", text):
        errors.append(f"{path.relative_to(ROOT)}: permissions: write-all is prohibited")
    if not re.search(r"(?m)^permissions:\s*(?:\n|\{)", text):
        errors.append(f"{path.relative_to(ROOT)}: workflow must declare top-level permissions")
    if "@latest" in text:
        errors.append(f"{path.relative_to(ROOT)}: floating @latest tool versions are prohibited")
    check_uses(path, lines, errors, approved)
    check_run_expressions(path, lines, errors)
    check_job_timeouts(path, lines, errors)
    check_authoritative_ci_fork_policy(path, lines, errors)
    check_client_package_fork_policy(path, lines, errors)
    check_protected_scheduled_workflow_policy(path, lines, errors)
    check_privileged_repository_dispatch_policy(path, lines, errors)


def main() -> int:
    errors: list[str] = []
    approved = load_approved_actions(errors)
    paths = sorted(WORKFLOWS.glob("*.yml")) + sorted(WORKFLOWS.glob("*.yaml"))
    for path in paths:
        check_workflow(path, errors, approved)
    for path in sorted((GITHUB / "actions").glob("*/action.y*ml")):
        raw = path.read_text(encoding="utf-8")
        if "\t" in raw:
            errors.append(f"{path.relative_to(ROOT)}: YAML indentation must not contain tab characters")
        lines = raw.splitlines()
        check_uses(path, lines, errors, approved)
        check_run_expressions(path, lines, errors)
    if errors:
        print("GitHub Actions policy violations:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1
    print(f"validated {len(paths)} workflows and local composite actions")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
