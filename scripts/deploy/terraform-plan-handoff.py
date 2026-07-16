#!/usr/bin/env python3
"""Create or verify an authenticated, encrypted Terraform plan handoff."""

from __future__ import annotations

import argparse
import datetime
import hashlib
import hmac
import json
import os
import pathlib
import re
import sys


SCHEMA_VERSION = 1
TERRAFORM_VERSION = "1.13.4"
MAX_AGE = datetime.timedelta(days=7)
CONTEXT = b"apexload/terraform-plan-handoff/v1\0"
EXPECTED_FILES = {"apexload.tfplan.enc", "plan-handoff.json", "plan-handoff.hmac"}


def fail(message: str) -> "NoReturn":
    raise SystemExit(f"Terraform plan handoff: {message}")


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def authentication_key() -> bytes:
    secret = os.environ.get("PLAN_ENCRYPTION_KEY", "").encode()
    if len(secret) < 32:
        fail("PLAN_ENCRYPTION_KEY must contain at least 32 bytes")
    return hmac.new(secret, CONTEXT + b"authentication-key", hashlib.sha256).digest()


def authentication_code(metadata: bytes, encrypted_plan: pathlib.Path) -> str:
    mac = hmac.new(authentication_key(), CONTEXT, hashlib.sha256)
    mac.update(b"plan-handoff.json\0")
    mac.update(len(metadata).to_bytes(8, "big"))
    mac.update(metadata)
    mac.update(b"apexload.tfplan.enc\0")
    mac.update(encrypted_plan.stat().st_size.to_bytes(8, "big"))
    with encrypted_plan.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            mac.update(chunk)
    return mac.hexdigest()


def validate_coordinate(value: str, pattern: str, name: str) -> None:
    if not re.fullmatch(pattern, value):
        fail(f"invalid {name}")


def create(args: argparse.Namespace) -> int:
    validate_coordinate(args.environment, r"staging|production", "environment")
    validate_coordinate(args.repository, r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", "repository")
    validate_coordinate(args.commit, r"[0-9a-f]{40}", "source commit")
    validate_coordinate(args.run_id, r"[1-9][0-9]*", "run ID")
    validate_coordinate(args.run_attempt, r"[1-9][0-9]*", "run attempt")
    if args.terraform_version != TERRAFORM_VERSION:
        fail(f"Terraform version must be {TERRAFORM_VERSION}")
    if not args.plan.is_file() or not args.encrypted.is_file():
        fail("plain and encrypted plan files are required")
    args.output.mkdir(parents=True, exist_ok=True)
    if any(args.output.iterdir()):
        fail("output directory must be empty")
    encrypted_output = args.output / "apexload.tfplan.enc"
    encrypted_output.write_bytes(args.encrypted.read_bytes())
    metadata = {
        "schema_version": SCHEMA_VERSION,
        "environment": args.environment,
        "source_repository": args.repository,
        "source_ref": "refs/heads/main",
        "source_commit": args.commit,
        "workflow": ".github/workflows/infra-azure.yml",
        "workflow_run_id": args.run_id,
        "workflow_run_attempt": args.run_attempt,
        "terraform_version": args.terraform_version,
        "plan_sha256": sha256(args.plan),
        "encrypted_plan_sha256": sha256(encrypted_output),
        "created_at": datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0).isoformat(),
    }
    metadata_bytes = (json.dumps(metadata, indent=2, sort_keys=True) + "\n").encode()
    metadata_path = args.output / "plan-handoff.json"
    metadata_path.write_bytes(metadata_bytes)
    (args.output / "plan-handoff.hmac").write_text(
        authentication_code(metadata_bytes, encrypted_output) + "\n", encoding="ascii"
    )
    return 0


def verify(args: argparse.Namespace) -> int:
    validate_coordinate(args.environment, r"staging|production", "environment")
    validate_coordinate(args.repository, r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", "repository")
    validate_coordinate(args.run_id, r"[1-9][0-9]*", "run ID")
    validate_coordinate(args.run_attempt, r"[1-9][0-9]*", "run attempt")
    if not args.directory.is_dir():
        fail("handoff directory does not exist")
    actual_files = {path.name for path in args.directory.iterdir() if path.is_file()}
    if actual_files != EXPECTED_FILES or any(path.is_dir() for path in args.directory.iterdir()):
        fail(f"handoff file set is invalid: {sorted(actual_files)}")
    encrypted = args.directory / "apexload.tfplan.enc"
    metadata_path = args.directory / "plan-handoff.json"
    metadata_bytes = metadata_path.read_bytes()
    supplied_mac = (args.directory / "plan-handoff.hmac").read_text(encoding="ascii").strip()
    if not re.fullmatch(r"[0-9a-f]{64}", supplied_mac) or not hmac.compare_digest(
        supplied_mac, authentication_code(metadata_bytes, encrypted)
    ):
        fail("handoff authentication failed")
    try:
        metadata = json.loads(metadata_bytes)
    except json.JSONDecodeError as error:
        fail(f"metadata is invalid JSON: {error}")
    required = {
        "schema_version",
        "environment",
        "source_repository",
        "source_ref",
        "source_commit",
        "workflow",
        "workflow_run_id",
        "workflow_run_attempt",
        "terraform_version",
        "plan_sha256",
        "encrypted_plan_sha256",
        "created_at",
    }
    if not isinstance(metadata, dict) or set(metadata) != required:
        fail("metadata fields do not match schema version 1")
    expected = {
        "schema_version": SCHEMA_VERSION,
        "environment": args.environment,
        "source_repository": args.repository,
        "source_ref": "refs/heads/main",
        "workflow": ".github/workflows/infra-azure.yml",
        "workflow_run_id": args.run_id,
        "workflow_run_attempt": args.run_attempt,
        "terraform_version": TERRAFORM_VERSION,
    }
    if any(metadata.get(key) != value for key, value in expected.items()):
        fail("metadata does not match the requested plan run")
    validate_coordinate(str(metadata.get("source_commit", "")), r"[0-9a-f]{40}", "source commit")
    validate_coordinate(str(metadata.get("plan_sha256", "")), r"[0-9a-f]{64}", "plan checksum")
    if metadata.get("encrypted_plan_sha256") != sha256(encrypted):
        fail("encrypted plan checksum does not match authenticated metadata")
    try:
        created_at = datetime.datetime.fromisoformat(str(metadata.get("created_at", "")))
    except ValueError:
        fail("metadata creation time is invalid")
    if created_at.tzinfo is None:
        fail("metadata creation time must include a timezone")
    now = datetime.datetime.now(datetime.timezone.utc)
    age = now - created_at.astimezone(datetime.timezone.utc)
    if age < -datetime.timedelta(minutes=5) or age > MAX_AGE:
        fail("authenticated plan is outside the seven-day promotion window")
    print(metadata["source_commit"])
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    create_parser = subparsers.add_parser("create")
    create_parser.add_argument("--environment", required=True)
    create_parser.add_argument("--repository", required=True)
    create_parser.add_argument("--commit", required=True)
    create_parser.add_argument("--run-id", required=True)
    create_parser.add_argument("--run-attempt", required=True)
    create_parser.add_argument("--terraform-version", required=True)
    create_parser.add_argument("--plan", required=True, type=pathlib.Path)
    create_parser.add_argument("--encrypted", required=True, type=pathlib.Path)
    create_parser.add_argument("--output", required=True, type=pathlib.Path)
    create_parser.set_defaults(handler=create)

    verify_parser = subparsers.add_parser("verify")
    verify_parser.add_argument("--environment", required=True)
    verify_parser.add_argument("--repository", required=True)
    verify_parser.add_argument("--run-id", required=True)
    verify_parser.add_argument("--run-attempt", required=True)
    verify_parser.add_argument("--directory", required=True, type=pathlib.Path)
    verify_parser.set_defaults(handler=verify)
    args = parser.parse_args()
    return args.handler(args)


if __name__ == "__main__":
    sys.exit(main())
