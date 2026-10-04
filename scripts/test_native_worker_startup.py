#!/usr/bin/env python3
"""Fail-closed native-mode startup gate for the actual packaged executable.

This gate verifies the earliest invalid worker boundary only. Real native jobs,
containment, parent death and memory/spill qualification remain separate gates.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time

CASES = (("missing-inherited-handles", ["--apexload-native-worker-v1"]),
         ("unsupported-version", ["--apexload-native-worker-v2"]),
         ("extra-argument", ["--apexload-native-worker-v1", "unexpected"]),
         ("duplicate-marker", ["--apexload-native-worker-v1", "--apexload-native-worker-v1"]))


class NativeStartupFailure(RuntimeError):
    def __init__(self, report):
        self.report = report
        last = report["cases"][-1]
        super().__init__("native startup boundary failed: " + last["case"] + " exit=" + str(last["exit_code"]))


def verify_binary(binary, runner=subprocess.run):
    binary = Path(binary).resolve(strict=True)
    records = []
    for name, args in CASES:
        with tempfile.TemporaryDirectory(prefix="apexload-worker-startup-") as temp:
            root = Path(temp)
            (root / ".env").write_text("APEXLOAD_STORE=postgres\nAPEXLOAD_DATABASE_URL=\n", encoding="utf-8")
            before = {str(p.relative_to(root)): p.read_bytes() for p in root.rglob("*") if p.is_file()}
            # A listener cannot accidentally start without encountering this held
            # socket. Its address is also a controlled side-effect sentinel.
            with socket.socket() as listener:
                listener.bind(("127.0.0.1", 0))
                listener.listen()
                env = {key: value for key, value in os.environ.items() if key.upper() in {"SYSTEMROOT", "WINDIR", "PATH", "TEMP", "TMP", "LANG", "LC_ALL"}}
                env.update({"APEXLOAD_STORE": "postgres", "APEXLOAD_DATABASE_URL": "", "APEXLOAD_DB": str(root / "forbidden.db"), "APEXLOAD_ARCHIVE_DIR": str(root / "forbidden-archives"), "APEXLOAD_ADDR": "127.0.0.1:" + str(listener.getsockname()[1]), "APEXLOAD_OTEL_ENABLED": "true", "OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:1", "HOME": str(root), "USERPROFILE": str(root)})
                started = time.monotonic()
                result = runner([str(binary), *args], cwd=root, env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=5, check=False)
            after = {str(p.relative_to(root)): p.read_bytes() for p in root.rglob("*") if p.is_file()}
            stdout, stderr = result.stdout or b"", result.stderr or b""
            unchanged = after == before and not any(p.is_dir() for p in root.rglob("*"))
            # Invalid worker mode is deliberately silent. Exit 78 racing a Go
            # runtime fatal error is still a broken descriptor boundary.
            passed = result.returncode == 78 and unchanged and not stdout and not stderr
            records.append({"case": name, "exit_code": result.returncode, "seconds": round(time.monotonic() - started, 6), "filesystem_unchanged": unchanged, "silent": not stdout and not stderr, "passed": passed,
                            "stdout_bytes": len(stdout), "stderr_bytes": len(stderr),
                            "stdout_sha256": hashlib.sha256(stdout).hexdigest(), "stderr_sha256": hashlib.sha256(stderr).hexdigest(),
                            "stdout_prefix": stdout[:4096].decode("utf-8", errors="replace"), "stderr_prefix": stderr[:4096].decode("utf-8", errors="replace")})
            if not passed:
                raise NativeStartupFailure({"binary": str(binary), "platform": os.name, "cases": records, "qualification": "invalid-mode-early-dispatch-only", "passed": False})
    return {"binary": str(binary), "platform": os.name, "cases": records, "qualification": "invalid-mode-early-dispatch-only"}


def desktop_binary(build_dir):
    build_dir = Path(build_dir)
    candidates = [build_dir / "apexload", build_dir / "apexload.exe"]
    candidates.extend(build_dir.glob("*.app/Contents/MacOS/apexload"))
    found = [p for p in candidates if p.is_file()]
    if len(found) != 1:
        raise RuntimeError("exactly one packaged desktop executable is required")
    return found[0]


def main():
    parser = argparse.ArgumentParser()
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--binary")
    group.add_argument("--desktop-build-dir")
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    try:
        report = verify_binary(args.binary if args.binary else desktop_binary(args.desktop_build_dir))
    except NativeStartupFailure as failure:
        Path(args.output).write_text(json.dumps(failure.report, indent=2) + "\n", encoding="utf-8")
        raise
    Path(args.output).write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
