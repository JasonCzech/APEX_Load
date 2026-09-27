#!/usr/bin/env python3
"""Generate private inputs for a new disposable fault-test installation."""
import argparse
import json
import os
from pathlib import Path
import secrets
import subprocess


def prepare(directory):
    root = Path(directory).resolve()
    root.mkdir(mode=0o700, parents=False, exist_ok=False)
    run_id = secrets.token_hex(8)
    def write(name, text):
        fd = os.open(root / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as f:
            f.write(text)
    def openssl(*args):
        subprocess.run(["openssl", *args], cwd=root, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    credentials = {key: secrets.token_urlsafe(32) for key in
                   ("http", "grpc", "postgres", "redis", "s3_access_key", "s3_secret_key", "wrapping_key",
                    "target_load", "target_observer")}
    write("credentials.json", json.dumps(credentials, indent=2))
    write("target.json", json.dumps({"run_id": run_id, "load_token": credentials["target_load"],
                                     "observer_token": credentials["target_observer"]}, indent=2))
    write("kind.yaml", """kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
- role: worker
- role: worker
""")
    write("ownership.json", json.dumps({"run_id": run_id, "cluster": "apexload-fault-" + run_id,
                                      "namespace": "apexload-fault", "namespace_label": "apexload.io/fault-run=" + run_id}, indent=2))
    # Keys are generated in an inaccessible directory and tightened immediately.
    openssl("req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", "ca.key", "-out", "ca.crt",
            "-days", "2", "-subj", "/CN=apexload-fault-" + run_id,
            "-addext", "basicConstraints=critical,CA:TRUE", "-addext", "keyUsage=critical,keyCertSign,cRLSign")
    for role in ("controller", "generator", "fault-target"):
        write(role + ".ext", "basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\n"
              "extendedKeyUsage=serverAuth,clientAuth\nsubjectAltName=DNS:localhost,IP:127.0.0.1,DNS:apexload-" + role
              + ",DNS:apexload-" + role + ".apexload-fault.svc,DNS:*.apexload-" + role + ".apexload-fault.svc\n")
        openssl("req", "-new", "-newkey", "rsa:2048", "-nodes", "-keyout", role + ".key", "-out", role + ".csr",
                "-subj", "/CN=apexload-" + role)
        openssl("x509", "-req", "-in", role + ".csr", "-CA", "ca.crt", "-CAkey", "ca.key", "-set_serial",
                str(int.from_bytes(secrets.token_bytes(16), "big")), "-out", role + ".crt", "-days", "2", "-extfile", role + ".ext")
    for path in root.iterdir():
        path.chmod(0o600)
    return run_id


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", help="new private directory; existing paths are refused")
    args = parser.parse_args()
    print("Prepared fresh inputs for run " + prepare(args.directory))
