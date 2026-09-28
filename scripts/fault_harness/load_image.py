#!/usr/bin/env python3
"""Load a verified local application image onto the owned kind workers by digest."""
import argparse
import json
import os
from pathlib import Path
import re
import signal
import subprocess

from render_target import input_root, read_ownership, validate_image


class ImageError(RuntimeError):
    pass


def validate_source(tag, reference, inspected):
    validate_image(reference)
    repository, digest = reference.split('@')
    if (not isinstance(tag, str) or not tag.startswith(repository + ':')
            or not re.fullmatch(r'[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}', tag[len(repository) + 1:])):
        raise ImageError('tag must be a fully qualified tag in the pinned repository')
    if (not isinstance(inspected, list) or len(inspected) != 1
            or not isinstance(inspected[0], dict)):
        raise ImageError('expected exactly one local image')
    image = inspected[0]
    digests = image.get('RepoDigests')
    if (not isinstance(digests, list) or not any(isinstance(d, str) and d.endswith('@' + digest) for d in digests)
            or image.get('Os') != 'linux' or image.get('Architecture') not in ('arm64', 'amd64')):
        raise ImageError('local image does not match the pinned Linux manifest')
    return image['Architecture']


def validate_worker(inspected, cluster, node):
    if not isinstance(inspected, list) or len(inspected) != 1:
        raise ImageError('expected one owned kind worker')
    info = inspected[0]
    labels = info.get('Config', {}).get('Labels', {})
    if (node not in (cluster + '-worker', cluster + '-worker2')
            or info.get('Name') != '/' + node
            or labels.get('io.x-k8s.kind.cluster') != cluster
            or labels.get('io.x-k8s.kind.role') != 'worker'
            or info.get('State', {}).get('Running') is not True
            or not re.fullmatch(r'[a-f0-9]{64}', info.get('Id', ''))):
        raise ImageError('container is not the expected running disposable worker')
    return info['Id']


def digest_alias_needed(listing, tag, reference):
    """Verify the imported manifest before creating a repository@digest alias."""
    if not isinstance(listing, str) or len(listing) > 2 * 1024 * 1024:
        raise ImageError('invalid image inventory')
    rows = {}
    for line in listing.splitlines():
        fields = line.split()
        if fields and fields[0] in (tag, reference):
            if len(fields) < 3 or fields[0] in rows:
                raise ImageError('ambiguous image inventory')
            rows[fields[0]] = fields[2]
    digest = reference.split('@')[1]
    if rows.get(tag) != digest:
        raise ImageError('imported tag does not resolve to the pinned manifest')
    if reference in rows and rows[reference] != digest:
        raise ImageError('existing digest alias conflicts with the pinned manifest')
    return reference not in rows


def command(argv, timeout=15):
    result = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
    if result.returncode or len(result.stdout) > 2 * 1024 * 1024:
        raise ImageError('image command failed; inspect private evidence')
    return result.stdout


def stream_import(tag, architecture, container_id, log):
    # Stream rather than writing a large tar to the host. Always reap both
    # processes on timeout/interruption; the container's immutable ID prevents
    # operating on a replacement that reused its name.
    source = subprocess.Popen(['docker', 'image', 'save', '--platform', 'linux/' + architecture, tag],
                              stdout=subprocess.PIPE, stderr=log)
    sink = None
    try:
        sink = subprocess.Popen(['docker', 'exec', '-i', container_id, 'ctr', '-n', 'k8s.io',
                                 'images', 'import', '--all-platforms', '--digests', '-'],
                                stdin=source.stdout, stdout=log, stderr=log)
        source.stdout.close()
        if sink.wait(timeout=180) or source.wait(timeout=15):
            raise ImageError('image streaming failed; inspect private evidence')
    finally:
        if source.stdout is not None:
            source.stdout.close()
        for process in (sink, source):
            if process is not None and process.poll() is None:
                process.kill()
                process.wait(timeout=5)


def load(inputs, tag, reference, output):
    run_id, cluster = read_ownership(input_root(inputs))
    validate_image(reference)
    architecture = validate_source(tag, reference, json.loads(command(['docker', 'image', 'inspect', tag])))
    # Validate all targets before importing anything; never import on the host
    # control plane or on an unrelated Compose/user container.
    workers = []
    for suffix in ('-worker', '-worker2'):
        node = cluster + suffix
        container_id = validate_worker(json.loads(command(['docker', 'inspect', node])), cluster, node)
        actual = command(['docker', 'exec', container_id, 'uname', '-m']).strip()
        if actual != {'arm64': 'aarch64', 'amd64': 'x86_64'}[architecture]:
            raise ImageError('image and worker architectures differ')
        workers.append((node, container_id))
    root = Path(output)
    if not root.is_absolute():
        raise ImageError('output must be a new absolute directory')
    root.mkdir(mode=0o700, exist_ok=False)
    evidence = {'run_id': run_id, 'reference': reference, 'tag': tag, 'architecture': architecture, 'workers': []}
    for node, container_id in workers:
        fd = os.open(root / (node + '.log'), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'wb') as log:
            stream_import(tag, architecture, container_id, log)
        prefix = ['docker', 'exec', container_id, 'ctr', '-n', 'k8s.io', 'images']
        listing = command(prefix + ['list'])
        created = digest_alias_needed(listing, tag, reference)
        if created:
            command(prefix + ['tag', tag, reference])
        if digest_alias_needed(command(prefix + ['list']), tag, reference):
            raise ImageError('digest alias is not present after import')
        evidence['workers'].append({'node': node, 'container_id': container_id, 'digest_alias_created': created})
    fd = os.open(root / 'result.json', os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump(evidence, stream, indent=2)
        stream.write('\n')
    return evidence


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('inputs', 'tag', 'reference', 'output'):
        parser.add_argument('--' + name, required=True)
    args = parser.parse_args()
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    try:
        load(args.inputs, args.tag, args.reference, args.output)
    except (Exception, KeyboardInterrupt):
        raise SystemExit('Image load failed; inspect private evidence') from None
    print('Verified pinned image and digest aliases on both owned workers')


if __name__ == '__main__':
    main()
