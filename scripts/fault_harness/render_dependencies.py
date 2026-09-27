#!/usr/bin/env python3
"""Render private PostgreSQL/Redis resources for disposable worker-fault trials."""
import argparse
import json
import re
import sys

from render_target import (RenderError, input_root, read_input, read_json, read_ownership,
                           validate_image, validate_tls, write_resources)


def resources(inputs, postgres_image, redis_image, storage_class="standard"):
    root = input_root(inputs)
    run_id, cluster = read_ownership(root)
    for image in (postgres_image, redis_image):
        validate_image(image)
    if (not isinstance(storage_class, str) or len(storage_class) > 253
            or not re.fullmatch(r"[a-z0-9](?:[-a-z0-9]*[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]*[a-z0-9])?)*", storage_class)):
        raise RenderError("an explicit valid storage class is required")
    credentials = read_json(root, "credentials.json")
    for role in ("postgres", "redis"):
        if not isinstance(credentials.get(role), str) or not re.fullmatch(r"[A-Za-z0-9_-]{32,128}", credentials[role]):
            raise RenderError("invalid dependency credential")
    if credentials["postgres"] == credentials["redis"]:
        raise RenderError("dependency credentials must differ")
    ca = read_input(root, "ca.crt")
    items = []
    def obj(kind, name, api="v1"):
        return {"apiVersion": api, "kind": kind, "metadata": {
            "name": name, "namespace": "apexload-fault",
            "labels": {"app.kubernetes.io/name": name, "apexload.io/fault-run": run_id}}}
    for role, image, uid, gid, port in (("postgres", postgres_image, 70, 70, 5432),
                                       ("redis", redis_image, 999, 1000, 6379)):
        name = "apexload-" + role
        cert, key = (read_input(root, role + suffix) for suffix in (".crt", ".key"))
        validate_tls(cert, key, ca, name + ".apexload-fault.svc")
        secret = obj("Secret", name + "-server")
        data = {"tls.crt": cert, "tls.key": key, "ca.crt": ca, "password": credentials[role]}
        if role == "postgres":
            data["pg_hba.conf"] = "local all all trust\nhostssl all all all scram-sha-256\nhostnossl all all all reject\n"
        else:
            data["redis.conf"] = ("bind 0.0.0.0\nprotected-mode yes\nport 0\ntls-port 6379\n"
                                  "tls-cert-file /run/apexfault/tls.crt\ntls-key-file /run/apexfault/tls.key\n"
                                  "tls-ca-cert-file /run/apexfault/ca.crt\ntls-auth-clients no\n"
                                  "dir /data\nappendonly yes\nappendfsync everysec\n"
                                  "maxmemory 192mb\nmaxmemory-policy noeviction\nrequirepass " + credentials[role] + "\n")
        secret.update(type="Opaque", immutable=True, stringData=data)
        workload = obj("StatefulSet", name, "apps/v1")
        labels = dict(workload["metadata"]["labels"])
        probe = {"tcpSocket": {"port": role}, "timeoutSeconds": 2}
        container = {"name": role, "image": image, "imagePullPolicy": "IfNotPresent",
                     "ports": [{"name": role, "containerPort": port}],
                     "securityContext": {"allowPrivilegeEscalation": False, "readOnlyRootFilesystem": True,
                                         "capabilities": {"drop": ["ALL"]}},
                     "resources": {"requests": {"cpu": "100m", "memory": "128Mi"},
                                   "limits": {"cpu": "1", "memory": "512Mi"}},
                     "volumeMounts": [{"name": "data", "mountPath": "/var/lib/postgresql/data" if role == "postgres" else "/data"},
                                      {"name": "server", "mountPath": "/run/apexfault", "readOnly": True},
                                      {"name": "tmp", "mountPath": "/tmp"}],
                     "startupProbe": {**probe, "periodSeconds": 2, "failureThreshold": 60},
                     "livenessProbe": {**probe, "periodSeconds": 10, "failureThreshold": 6}}
        if role == "postgres":
            container["env"] = [{"name": "POSTGRES_DB", "value": "apexload"},
                                {"name": "POSTGRES_USER", "value": "apexload"},
                                {"name": "POSTGRES_PASSWORD_FILE", "value": "/run/apexfault/password"},
                                {"name": "PGDATA", "value": "/var/lib/postgresql/data/pgdata"},
                                {"name": "POSTGRES_INITDB_ARGS", "value": "--auth-host=scram-sha-256"}]
            container["args"] = ["postgres", "-c", "ssl=on", "-c", "ssl_cert_file=/run/apexfault/tls.crt",
                                 "-c", "ssl_key_file=/run/apexfault/tls.key", "-c", "hba_file=/run/apexfault/pg_hba.conf"]
            container["volumeMounts"].append({"name": "socket", "mountPath": "/var/run/postgresql"})
            check = ('PGPASSWORD="$(cat /run/apexfault/password)" psql '
                     '"host=localhost user=apexload dbname=apexload sslmode=verify-full '
                     'sslrootcert=/run/apexfault/ca.crt connect_timeout=2" -tAc "SELECT 1" | grep -qx 1')
        else:
            container["args"] = ["redis-server", "/run/apexfault/redis.conf"]
            check = ('REDISCLI_AUTH="$(cat /run/apexfault/password)" redis-cli --tls '
                     '--cacert /run/apexfault/ca.crt --sni localhost -h localhost ping | grep -qx PONG')
        container["readinessProbe"] = {"exec": {"command": ["sh", "-ec", check]}, "timeoutSeconds": 3, "periodSeconds": 5}
        volumes = [{"name": "server", "secret": {"secretName": name + "-server", "defaultMode": 0o440}},
                   {"name": "tmp", "emptyDir": {"sizeLimit": "32Mi"}}]
        if role == "postgres":
            volumes.append({"name": "socket", "emptyDir": {"sizeLimit": "8Mi"}})
        workload["spec"] = {"serviceName": name, "replicas": 1, "selector": {"matchLabels": labels},
                            "template": {"metadata": {"labels": labels}, "spec": {
                                "automountServiceAccountToken": False, "terminationGracePeriodSeconds": 60,
                                "nodeSelector": {"kubernetes.io/hostname": cluster + "-control-plane"},
                                "tolerations": [{"key": "node-role.kubernetes.io/control-plane", "operator": "Exists", "effect": "NoSchedule"}],
                                "securityContext": {"runAsNonRoot": True, "runAsUser": uid, "runAsGroup": gid,
                                                    "fsGroup": gid, "seccompProfile": {"type": "RuntimeDefault"}},
                                "containers": [container], "volumes": volumes}},
                            "volumeClaimTemplates": [{"metadata": {"name": "data", "labels": labels},
                                                      "spec": {"accessModes": ["ReadWriteOnce"], "storageClassName": storage_class,
                                                               "resources": {"requests": {"storage": "1Gi"}}}}]}
        service = obj("Service", name)
        service["spec"] = {"clusterIP": "None", "selector": labels, "ports": [{"name": role, "port": port, "targetPort": role}]}
        policy = obj("NetworkPolicy", name, "networking.k8s.io/v1")
        policy["spec"] = {"podSelector": {"matchLabels": labels}, "policyTypes": ["Ingress", "Egress"], "egress": [],
                          "ingress": [{"from": [{"podSelector": {"matchLabels": {"app.kubernetes.io/name": peer}}}
                                                 for peer in ("apexload-controller", "apexload-fault-observer")],
                                       "ports": [{"protocol": "TCP", "port": port}]}]}
        items.extend((secret, workload, service, policy))
    client = obj("Secret", "apexload-fault-data-client")
    client.update(type="Opaque", immutable=True, stringData={
        "ca.crt": ca,
        "APEXLOAD_DATABASE_URL": "postgres://apexload:" + credentials["postgres"] + "@apexload-postgres.apexload-fault.svc:5432/apexload?sslmode=verify-full&sslrootcert=/run/apexfault-data/ca.crt",
        "APEXLOAD_REDIS_URL": "rediss://:" + credentials["redis"] + "@apexload-redis.apexload-fault.svc:6379/0"})
    items.append(client)
    return run_id, {"apiVersion": "v1", "kind": "List", "items": items}


def render(inputs, postgres_image, redis_image, output, storage_class="standard"):
    run_id, manifest = resources(inputs, postgres_image, redis_image, storage_class)
    write_resources(output, manifest, "dependency-resources.json")
    return run_id


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inputs", required=True)
    parser.add_argument("--postgres-image", required=True, help="PostgreSQL 16 Alpine repository manifest digest")
    parser.add_argument("--redis-image", required=True, help="Redis 7 Alpine repository manifest digest")
    parser.add_argument("--storage-class", default="standard")
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    try:
        run_id = render(args.inputs, args.postgres_image, args.redis_image, args.output, args.storage_class)
    except RenderError as error:
        print(str(error), file=sys.stderr)
        return 1
    print("Rendered private dependency resources for run " + run_id)
    return 0


if __name__ == "__main__":
    sys.exit(main())
