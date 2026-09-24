#!/usr/bin/env python3
"""Generate local-only PNOP offline lifecycle traces using the pinned assembled harness."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
for flag in ('java-home', 'classpath-file', 'jmeter-home', 'vectors', 'evidence'):
    parser.add_argument('--' + flag, type=Path, required=True)
args = parser.parse_args()
root = Path(__file__).resolve().parent
source = root / 'instrumentation/ReferenceEventHubsLifecycle.java'
classpath = args.classpath_file.read_text().strip()
jars = [Path(p) for p in classpath.split(os.pathsep)]
locked = json.loads((root / 'startup-evidence.json').read_text())
expected = {p['artifact']: p['sha256'] for p in locked['runtime_artifacts']}
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
if len(jars) != len(expected) or {p.name for p in jars} != set(expected) or any(not p.is_file() or expected.get(p.name) != sha(p) for p in jars):
    parser.error('classpath differs from the pinned startup evidence')
with tempfile.TemporaryDirectory(prefix='apex-eventhubs-lifecycle-oracle-') as tmp:
    subprocess.run([str(args.java_home / 'bin/javac'), '--add-exports', 'java.base/jdk.internal.org.objectweb.asm=ALL-UNNAMED', '-source', '17', '-target', '17', '-cp', classpath,
                    '-d', tmp, str(source)], check=True, timeout=60)
    manifest = Path(tmp) / 'MANIFEST.MF'
    manifest.write_text('Premain-Class: ReferenceEventHubsLifecycle\n\n')
    agent = Path(tmp) / 'agent.jar'
    subprocess.run([str(args.java_home / 'bin/jar'), 'cfm', str(agent), str(manifest), '-C', tmp, '.'], check=True, timeout=60)
    run = subprocess.run([str(args.java_home / 'bin/java'), '-Duser.timezone=UTC', '--add-exports=java.base/jdk.internal.org.objectweb.asm=ALL-UNNAMED', '-javaagent:' + str(agent), '-cp',
                          tmp + os.pathsep + classpath, 'ReferenceEventHubsLifecycle', str(args.jmeter_home)],
                         capture_output=True, text=True, timeout=60)
    if run.returncode:
        raise SystemExit(run.stderr)
data = json.loads(run.stdout[run.stdout.index('{"vectors"'):])
args.vectors.write_text(json.dumps(data, separators=(',', ':')) + '\n')
args.evidence.write_text(json.dumps({
    'schema_version': 1, 'live_conformance': 'unverified', 'scope': 'Unchanged PNOP sampler lifecycle and sample boundaries with instrumented SDK transport; network forbidden',
    'source_sha256': sha(source), 'vectors_sha256': sha(args.vectors),
    'startup_evidence_sha256': sha(root / 'startup-evidence.json'),
    'vectors': len(data['vectors']),
}, indent=2) + '\n')
print('Pinned PNOP offline lifecycle captured; live conformance remains unverified.')
