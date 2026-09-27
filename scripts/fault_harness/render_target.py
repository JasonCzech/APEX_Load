#!/usr/bin/env python3
"""Render private target resources for an owned disposable fault run; never apply them."""
import argparse
import json
import os
from pathlib import Path
import re
import ssl
import sys
import tempfile


class RenderError(RuntimeError):
    pass


def read_input(root, name, limit=32768):
    try:
        path = root / name
        if not path.is_file():
            raise RenderError("required private input is missing")
        with path.open("rb") as stream:
            data = stream.read(limit + 1)
        if len(data) > limit:
            raise RenderError("private input exceeds size bound")
        return data.decode("utf-8")
    except (OSError, UnicodeError):
        raise RenderError("cannot read private input") from None


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise RenderError("duplicate private configuration field")
        result[key] = value
    return result


def read_json(root, name):
    try:
        result = json.loads(read_input(root, name, 8192), object_pairs_hook=unique_object)
        if not isinstance(result, dict):
            raise RenderError("private configuration must be an object")
        return result
    except (ValueError, RecursionError):
        raise RenderError("invalid private configuration") from None


def validate_tls(cert, key, ca, hostname="apexload-fault-target.apexload-fault.svc"):
    try:
        client_context = ssl.create_default_context(cadata=ca)
        client_context.verify_flags |= ssl.VERIFY_X509_STRICT
        server_context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        # Validate the exact captured bytes that will be rendered, not a second
        # read of inputs which could have changed while they were collected.
        with tempfile.TemporaryDirectory() as temporary:
            for name, data in (("cert", cert), ("key", key)):
                fd = os.open(Path(temporary) / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                with os.fdopen(fd, "w") as stream:
                    stream.write(data)
            # Never prompt on stdin if an unexpected encrypted key is supplied.
            server_context.load_cert_chain(Path(temporary) / "cert", Path(temporary) / "key", password="")
        client_in, client_out, server_in, server_out = (ssl.MemoryBIO() for _ in range(4))
        client = client_context.wrap_bio(client_in, client_out, server_hostname=hostname)
        server = server_context.wrap_bio(server_in, server_out, server_side=True)
        complete = set()
        for _ in range(20):
            for name, peer in (("client", client), ("server", server)):
                try:
                    peer.do_handshake()
                    complete.add(name)
                except ssl.SSLWantReadError:
                    pass
            if complete == {"client", "server"}:
                return
            server_in.write(client_out.read())
            client_in.write(server_out.read())
    except (OSError, ValueError):
        raise RenderError("invalid target TLS inputs") from None
    raise RenderError("target TLS validation did not complete")


def input_root(inputs):
    root = Path(inputs)
    if not root.is_absolute() or not root.is_dir():
        raise RenderError("inputs must be an existing absolute directory")
    return root


def validate_image(image):
    # A registry manifest digest is required, not a mutable tag or local image ID.
    if (not isinstance(image, str) or len(image) > 512 or not re.fullmatch(
            r"[a-z0-9]+(?:[.-][a-z0-9]+)*(?::[0-9]{1,5})?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+@sha256:[a-f0-9]{64}", image)):
        raise RenderError("image must be a repository reference pinned by SHA-256 digest")
    registry = image.split("/", 1)[0]
    if ":" in registry and not 1 <= int(registry.rsplit(":", 1)[1]) <= 65535:
        raise RenderError("image registry port is invalid")


def read_ownership(root):
    ownership = read_json(root, "ownership.json")
    run_id = ownership.get("run_id")
    if not isinstance(run_id, str) or not re.fullmatch(r"[a-f0-9]{16}", run_id):
        raise RenderError("invalid fault run identity")
    cluster = "apexload-fault-" + run_id
    if (ownership.get("cluster") != cluster or ownership.get("namespace") != "apexload-fault"
            or ownership.get("namespace_label") != "apexload.io/fault-run=" + run_id):
        raise RenderError("private ownership inputs do not match the disposable run")
    return run_id, cluster


def resources(inputs, image):
    root = input_root(inputs)
    validate_image(image)
    run_id, cluster = read_ownership(root)
    config = read_json(root, "target.json")
    if set(config) != {"run_id", "load_token", "observer_token"} or config["run_id"] != run_id:
        raise RenderError("target configuration does not match the disposable run")
    for key in ("load_token", "observer_token"):
        if not isinstance(config[key], str) or not re.fullmatch(r"[A-Za-z0-9_-]{32,128}", config[key]):
            raise RenderError("invalid target credential")
    if config["load_token"] == config["observer_token"]:
        raise RenderError("load and observer credentials must differ")
    cert, key, ca = (read_input(root, name) for name in ("fault-target.crt", "fault-target.key", "ca.crt"))
    validate_tls(cert, key, ca)
    name = "apexload-fault-target"
    labels = {"app.kubernetes.io/name": name, "apexload.io/fault-run": run_id}
    def obj(api, kind, suffix=""):
        return {"apiVersion": api, "kind": kind,
                "metadata": {"name": name + suffix, "namespace": "apexload-fault", "labels": dict(labels)}}
    secret = obj("v1", "Secret", "-server")
    secret.update(type="Opaque", immutable=True, stringData={"target.json": json.dumps(config), "tls.crt": cert, "tls.key": key})
    load_secret = obj("v1", "Secret", "-load")
    load_secret.update(type="Opaque", immutable=True, stringData={"load_token": config["load_token"], "ca.crt": ca})
    deployment = obj("apps/v1", "Deployment")
    health = {"httpGet": {"path": "/healthz", "port": "https", "scheme": "HTTPS"}, "timeoutSeconds": 2}
    container = {"name": "target", "image": image, "imagePullPolicy": "IfNotPresent",
                 "args": ["--config", "/var/run/apexfault/target.json", "--listen", ":8090",
                          "--tls-cert", "/var/run/apexfault/tls.crt", "--tls-key", "/var/run/apexfault/tls.key"],
                 "ports": [{"name": "https", "containerPort": 8090}],
                 "resources": {"requests": {"cpu": "100m", "memory": "32Mi"}, "limits": {"cpu": "1", "memory": "128Mi"}},
                 "securityContext": {"allowPrivilegeEscalation": False, "readOnlyRootFilesystem": True, "capabilities": {"drop": ["ALL"]}},
                 "volumeMounts": [{"name": "server", "mountPath": "/var/run/apexfault", "readOnly": True}],
                 "startupProbe": {**health, "periodSeconds": 2, "failureThreshold": 30},
                 "readinessProbe": {**health, "periodSeconds": 5}, "livenessProbe": {**health, "periodSeconds": 10}}
    deployment["spec"] = {"replicas": 1, "strategy": {"type": "Recreate"}, "selector": {"matchLabels": dict(labels)},
                          "template": {"metadata": {"labels": dict(labels)}, "spec": {
                              "automountServiceAccountToken": False, "terminationGracePeriodSeconds": 10,
                              "nodeSelector": {"kubernetes.io/hostname": cluster + "-control-plane"},
                              "tolerations": [{"key": "node-role.kubernetes.io/control-plane", "operator": "Exists", "effect": "NoSchedule"}],
                              "securityContext": {"runAsNonRoot": True, "runAsUser": 65532, "runAsGroup": 65532,
                                                  "fsGroup": 65532, "seccompProfile": {"type": "RuntimeDefault"}},
                              "containers": [container], "volumes": [{"name": "server", "secret": {"secretName": name + "-server", "defaultMode": 0o440}}]}}}
    service = obj("v1", "Service")
    service["spec"] = {"type": "ClusterIP", "selector": dict(labels), "ports": [{"name": "https", "port": 443, "targetPort": "https"}]}
    policy = obj("networking.k8s.io/v1", "NetworkPolicy")
    policy["spec"] = {"podSelector": {"matchLabels": dict(labels)}, "policyTypes": ["Ingress", "Egress"], "egress": [],
                      "ingress": [{"from": [{"podSelector": {"matchLabels": {"app.kubernetes.io/name": peer}}}
                                             for peer in ("apexload-generator", "apexload-fault-observer")],
                                   "ports": [{"protocol": "TCP", "port": 8090}]}]}
    return run_id, {"apiVersion": "v1", "kind": "List", "items": [secret, load_secret, deployment, service, policy]}


def write_resources(output, manifest, filename):
    root = Path(output)
    if not root.is_absolute():
        raise RenderError("output must be a new absolute directory")
    try:
        root.mkdir(mode=0o700, parents=False, exist_ok=False)
        for name, content in ((filename, json.dumps(manifest, indent=2) + "\n"),
                              ("kustomization.yaml", "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - " + filename + "\n")):
            fd = os.open(root / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "w") as stream:
                stream.write(content)
    except OSError:
        raise RenderError("cannot create new private resource output") from None


def render(inputs, image, output):
    run_id, manifest = resources(inputs, image)
    write_resources(output, manifest, "target-resources.json")
    return run_id


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inputs", required=True, help="absolute fresh private input directory")
    parser.add_argument("--image", required=True, help="target registry image pinned by digest")
    parser.add_argument("--output", required=True, help="new absolute private output directory")
    args = parser.parse_args()
    try:
        run_id = render(args.inputs, args.image, args.output)
    except RenderError as error:
        print(str(error), file=sys.stderr)
        return 1
    print("Rendered private target resources for run " + run_id)
    return 0


if __name__ == "__main__":
    sys.exit(main())
