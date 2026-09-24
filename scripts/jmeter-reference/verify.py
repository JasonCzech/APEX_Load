#!/usr/bin/env python3
"""Run the pinned local-only reference startup check and record its artifacts.

This never executes the imported load plan or sends broker traffic. Startup is
not live conformance. The caller must first assemble the pinned dependencies.
"""
import argparse
import hashlib
import json
import pathlib
import subprocess
import tempfile


def sha256(path):
    with path.open('rb') as source:
        return hashlib.file_digest(source, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--java-home', type=pathlib.Path, required=True)
    parser.add_argument('--classpath-file', type=pathlib.Path, required=True)
    parser.add_argument('--jmeter-home', type=pathlib.Path, required=True)
    parser.add_argument('--fixture', type=pathlib.Path, required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    args = parser.parse_args()
    import os
    classpath = args.classpath_file.read_text().strip()
    jars = [pathlib.Path(value) for value in classpath.split(os.pathsep)]
    if any(not path.is_file() or path.suffix != '.jar' for path in jars):
        parser.error('classpath must contain existing JAR files only')
    root = pathlib.Path(__file__).resolve().parent
    source = root / 'src/main/java/ReferenceSmoke.java'
    with tempfile.TemporaryDirectory(prefix='apex-reference-') as output:
        subprocess.run([str(args.java_home / 'bin/javac'), '--release', '17', '-cp', classpath,
                        '-d', output, str(source)], check=True, timeout=60)
        run = subprocess.run([str(args.java_home / 'bin/java'), '-Duser.timezone=UTC',
                              '-cp', output + os.pathsep + classpath, 'ReferenceSmoke',
                              str(args.jmeter_home.resolve()), str(args.fixture.resolve())],
                             capture_output=True, text=True, timeout=30)
    if run.returncode or 'REFERENCE_STARTUP_OK; LIVE_CONFORMANCE_UNVERIFIED' not in run.stdout:
        raise SystemExit('Reference startup failed:\n' + run.stdout + run.stderr)
    report = {
        'schema_version': 1, 'startup': 'passed', 'live_conformance': 'unverified',
        'timezone': 'UTC', 'property_overrides': {},
        'fixture': {'name': args.fixture.name, 'sha256': sha256(args.fixture)},
        'harness_source_sha256': sha256(source),
        'runtime_artifacts': [{'artifact': p.name, 'sha256': sha256(p)} for p in jars],
        'stdout': run.stdout, 'stderr': run.stderr,
    }
    args.output.write_text(json.dumps(report, indent=2) + '\n')
    print('Reference startup passed; live conformance remains unverified.')


if __name__ == '__main__':
    main()
