#!/usr/bin/env python3
"""Replace APEX Load Helm image tags with release-manifest digest references."""

from __future__ import annotations

import os
import re
import sys


IMMUTABLE = re.compile(r"^[A-Za-z0-9._:/-]+@sha256:[0-9a-f]{64}$")
IMAGE_LINE = re.compile(r'^(?P<indent>\s*image:\s*)(?P<quote>["\']?)(?P<image>[^"\'\s]+)(?P=quote)\s*$')


def required_image(name: str) -> str:
    value = os.environ.get(name, "")
    if not IMMUTABLE.fullmatch(value):
        raise SystemExit(f"digest post-renderer: {name} must be an immutable repository@sha256 reference")
    return value


def main() -> int:
    replacements = {
        "apexload-controller": required_image("APEXLOAD_CONTROLLER_IMAGE"),
        "apexload-generator": required_image("APEXLOAD_GENERATOR_IMAGE"),
    }
    counts = {key: 0 for key in replacements}
    output: list[str] = []
    for line in sys.stdin.read().splitlines(keepends=True):
        newline = "\n" if line.endswith("\n") else ""
        content = line[:-1] if newline else line
        match = IMAGE_LINE.fullmatch(content)
        if not match:
            output.append(line)
            continue
        image = match.group("image")
        component = next((key for key in replacements if image.split("/")[-1].split(":")[0] == key), None)
        if component is None:
            output.append(line)
            continue
        output.append(f'{match.group("indent")}"{replacements[component]}"{newline}')
        counts[component] += 1

    missing = [component for component, count in counts.items() if count != 1]
    if missing:
        raise SystemExit(
            "digest post-renderer: expected exactly one workload image for " + ", ".join(missing)
        )
    sys.stdout.write("".join(output))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
