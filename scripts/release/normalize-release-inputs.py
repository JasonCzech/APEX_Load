#!/usr/bin/env python3
"""Normalize GitHub.com/GHES artifact layouts without overwriting collisions."""

from __future__ import annotations

import pathlib
import sys


CORE_ARTIFACT = "release-core-assets"
CLIENT_ARTIFACTS = {
    "release-client-linux-amd64",
    "release-client-linux-arm64",
    "release-client-macos-amd64",
    "release-client-macos-arm64",
    "release-client-windows-amd64",
}
IMAGE_ARTIFACTS = {
    "release-image-controller-standard",
    "release-image-controller-enterprise",
    "release-image-generator-standard",
    "release-image-generator-enterprise",
}
EXPECTED_ARTIFACTS = {CORE_ARTIFACT, *CLIENT_ARTIFACTS, *IMAGE_ARTIFACTS}


def fail(message: str) -> "NoReturn":
    raise SystemExit(f"release input normalization: {message}")


def destination_kind(relative: pathlib.PurePath) -> str | None:
    parts = relative.parts
    if len(parts) < 2:
        return None
    artifact = parts[0]
    artifact_path = pathlib.PurePath(*parts[1:])

    # release-core-assets uploads the single directory release/assets/. Both
    # upload-artifact v4 and the GHES-compatible v3 client make that directory
    # the archive root, so its files download directly below the artifact name.
    if artifact == CORE_ARTIFACT and len(artifact_path.parts) == 1:
        return "assets"

    # Client artifacts upload two paths. Their common archive root is the
    # workspace, preserving the release/assets and dist/desktop directories.
    if artifact in CLIENT_ARTIFACTS:
        if artifact_path.parent == pathlib.PurePath("release/assets"):
            return "assets"
        if (
            artifact_path.parent == pathlib.PurePath("dist/desktop")
            and artifact_path.suffixes[-2:] == [".tar", ".gz"]
        ):
            return "assets"

    # Image artifacts upload the single release/ directory, so assets/ and
    # image-metadata/ are preserved immediately below the artifact root.
    if artifact in IMAGE_ARTIFACTS:
        if artifact_path.parent == pathlib.PurePath("assets"):
            return "assets"
        if artifact_path.parent == pathlib.PurePath("image-metadata"):
            return "image-metadata"

    return None


def main() -> int:
    if len(sys.argv) != 2:
        fail("usage: normalize-release-inputs.py <artifact-root>")
    root = pathlib.Path(sys.argv[1])
    if not root.is_dir():
        fail("artifact root does not exist")
    root = root.resolve()
    destinations = {
        "assets": root / "assets",
        "image-metadata": root / "image-metadata",
    }
    for destination in destinations.values():
        destination.mkdir(parents=True, exist_ok=True)

    actual_artifacts = {
        path.name
        for path in root.iterdir()
        if path.is_dir() and path.name not in destinations
    }
    if actual_artifacts != EXPECTED_ARTIFACTS:
        fail(
            "artifact directory set is invalid: "
            f"expected {sorted(EXPECTED_ARTIFACTS)}, found {sorted(actual_artifacts)}"
        )

    planned: dict[pathlib.Path, pathlib.Path] = {}
    for source in sorted(root.rglob("*")):
        if not source.is_file():
            continue
        relative = source.relative_to(root)
        kind = destination_kind(relative)
        if kind is None:
            fail(f"unexpected artifact path: {relative}")
        target = destinations[kind] / source.name
        if source == target:
            continue
        if target in planned or target.exists():
            first = planned.get(target, target)
            fail(f"duplicate artifact filename {target.name}: {first.relative_to(root)} and {relative}")
        planned[target] = source

    for target, source in planned.items():
        source.replace(target)

    empty = next((path for path in destinations["assets"].iterdir() if path.is_file() and path.stat().st_size == 0), None)
    if empty is not None:
        fail(f"empty release asset found: {empty.name}")
    if not any(path.is_file() for path in destinations["assets"].iterdir()):
        fail("no release assets were normalized")
    return 0


if __name__ == "__main__":
    sys.exit(main())
