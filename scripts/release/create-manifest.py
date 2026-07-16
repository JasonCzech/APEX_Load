#!/usr/bin/env python3
"""Create the schema-v1 release manifest from verified release artifacts."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import re
import sys


SEMVER = re.compile(r"^v(?P<version>[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?)$")
APEXCTL = re.compile(
    r"^apexctl_(?P<version>[^_]+)_(?P<os>linux|darwin|windows)_(?P<arch>amd64|arm64)(?P<exe>\.exe)?$"
)
DESKTOP = re.compile(
    r"^apexload-desktop-(?P<version>.+)-(?P<os>linux|darwin|windows)-(?P<arch>amd64|arm64)\.tar\.gz$"
)
SERVER = re.compile(r"^apexload-server_(?P<version>[^_]+)_linux_(?P<arch>amd64|arm64)\.tar\.gz$")
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")


def checksum(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def fail(message: str) -> "NoReturn":
    raise SystemExit(f"release manifest: {message}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--tag", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--build-time", required=True)
    parser.add_argument("--assets", required=True, type=pathlib.Path)
    parser.add_argument("--image-metadata", required=True, type=pathlib.Path)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()

    match = SEMVER.fullmatch(args.tag)
    if not match:
        fail("invalid semantic tag")
    version = match.group("version")
    if not re.fullmatch(r"[0-9a-f]{40}", args.commit):
        fail("commit must be a full lowercase SHA")
    if not args.assets.is_dir() or not args.image_metadata.is_dir():
        fail("asset and image metadata directories must exist")

    asset_rows: list[dict[str, object]] = []
    binaries: list[dict[str, str]] = []
    desktop_clients: list[dict[str, str]] = []
    server_bundles: list[dict[str, str]] = []
    for path in sorted(item for item in args.assets.iterdir() if item.is_file()):
        if path.name in {"SHA256SUMS", "SHA256SUMS.sig", "release-manifest.json"}:
            continue
        digest = checksum(path)
        asset_rows.append({"asset": path.name, "sha256": digest, "size": path.stat().st_size})
        binary_match = APEXCTL.fullmatch(path.name)
        if binary_match:
            if binary_match.group("version") != version:
                fail(f"{path.name} has the wrong embedded asset version")
            if (binary_match.group("os") == "windows") != bool(binary_match.group("exe")):
                fail(f"{path.name} has an invalid executable suffix")
            binaries.append(
                {
                    "name": "apexctl",
                    "os": binary_match.group("os"),
                    "architecture": binary_match.group("arch"),
                    "asset": path.name,
                    "sha256": digest,
                }
            )
        desktop_match = DESKTOP.fullmatch(path.name)
        if desktop_match:
            if desktop_match.group("version") != version:
                fail(f"{path.name} has the wrong embedded asset version")
            desktop_clients.append(
                {
                    "name": "apexload-desktop",
                    "os": desktop_match.group("os"),
                    "architecture": desktop_match.group("arch"),
                    "asset": path.name,
                    "sha256": digest,
                }
            )
        server_match = SERVER.fullmatch(path.name)
        if server_match:
            if server_match.group("version") != version:
                fail(f"{path.name} has the wrong embedded asset version")
            server_bundles.append(
                {
                    "name": "apexload-server",
                    "os": "linux",
                    "architecture": server_match.group("arch"),
                    "asset": path.name,
                    "sha256": digest,
                }
            )

    expected_platforms = {
        ("linux", "amd64"),
        ("linux", "arm64"),
        ("darwin", "amd64"),
        ("darwin", "arm64"),
        ("windows", "amd64"),
    }
    actual_platforms = {(row["os"], row["architecture"]) for row in binaries}
    if actual_platforms != expected_platforms:
        fail(f"apexctl platform set is incomplete: {sorted(actual_platforms)}")

    desktop_platforms = {
        ("linux", "amd64"),
        ("linux", "arm64"),
        ("darwin", "amd64"),
        ("darwin", "arm64"),
        ("windows", "amd64"),
    }
    actual_desktop_platforms = {(row["os"], row["architecture"]) for row in desktop_clients}
    if actual_desktop_platforms != desktop_platforms or len(desktop_clients) != len(desktop_platforms):
        fail(f"desktop client platform set is incomplete or duplicated: {sorted(actual_desktop_platforms)}")

    server_platforms = {(row["os"], row["architecture"]) for row in server_bundles}
    expected_server_platforms = {("linux", "amd64"), ("linux", "arm64")}
    if server_platforms != expected_server_platforms or len(server_bundles) != len(expected_server_platforms):
        fail(f"server bundle platform set is incomplete or duplicated: {sorted(server_platforms)}")

    image_rows: list[dict[str, object]] = []
    for path in sorted(args.image_metadata.glob("*.json")):
        row = json.loads(path.read_text(encoding="utf-8"))
        required = {"component", "profile", "repository", "digest", "platforms"}
        if set(row) != required:
            fail(f"{path.name} does not contain exactly {sorted(required)}")
        if row["component"] not in {"controller", "generator"}:
            fail(f"{path.name} contains an invalid component")
        if row["profile"] not in {"standard", "enterprise"}:
            fail(f"{path.name} contains an invalid protocol profile")
        if not DIGEST.fullmatch(row["digest"]):
            fail(f"{path.name} contains a non-immutable image digest")
        platforms = row["platforms"]
        if not isinstance(platforms, list) or len(platforms) != 2:
            fail(f"{path.name} must describe exactly two image platforms")
        expected_platforms = {("linux", "amd64"), ("linux", "arm64")}
        actual_platforms: set[tuple[str, str]] = set()
        for platform in platforms:
            platform_required = {"os", "architecture", "digest", "sbom_asset", "sbom_sha256"}
            if not isinstance(platform, dict) or set(platform) != platform_required:
                fail(f"{path.name} contains an invalid platform record")
            coordinate = (platform["os"], platform["architecture"])
            actual_platforms.add(coordinate)
            if not DIGEST.fullmatch(platform["digest"]):
                fail(f"{path.name} contains a non-immutable platform digest")
            sbom = next((asset for asset in asset_rows if asset["asset"] == platform["sbom_asset"]), None)
            if sbom is None:
                fail(f"{path.name} references a missing platform SBOM asset")
            if platform["sbom_sha256"] != sbom["sha256"]:
                fail(f"{path.name} platform SBOM digest does not match its release asset")
        if actual_platforms != expected_platforms:
            fail(f"{path.name} has an incomplete image platform set: {sorted(actual_platforms)}")
        image_rows.append(row)

    expected_images = {
        (component, profile)
        for component in ("controller", "generator")
        for profile in ("standard", "enterprise")
    }
    actual_images = {(row["component"], row["profile"]) for row in image_rows}
    if actual_images != expected_images:
        fail(f"image set is incomplete: {sorted(actual_images)}")

    chart_name = f"apexload-{version}.tgz"
    chart = next((row for row in asset_rows if row["asset"] == chart_name), None)
    if chart is None:
        fail(f"missing Helm chart {chart_name}")
    appservice_name = f"apexload-appservice_{version}_linux_amd64.zip"
    appservice = next((row for row in asset_rows if row["asset"] == appservice_name), None)
    if appservice is None:
        fail(f"missing App Service package {appservice_name}")

    manifest = {
        "schema_version": 1,
        "version": version,
        "tag": args.tag,
        "commit": args.commit,
        "build_time": args.build_time,
        "source_repository": args.repository,
        "canonical_apexctl_version": args.tag,
        "images": sorted(image_rows, key=lambda row: (row["component"], row["profile"])),
        "helm": {
            "reference": f"github-release://{args.repository}/{args.tag}/{chart_name}",
            "asset": chart_name,
            "digest": f"sha256:{chart['sha256']}",
        },
        "app_service": {"asset": appservice_name, "sha256": appservice["sha256"]},
        "binaries": sorted(binaries, key=lambda row: (row["os"], row["architecture"])),
        "desktop_clients": sorted(desktop_clients, key=lambda row: (row["os"], row["architecture"])),
        "server_bundles": sorted(server_bundles, key=lambda row: (row["os"], row["architecture"])),
        "assets": asset_rows,
    }
    args.output.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main())
