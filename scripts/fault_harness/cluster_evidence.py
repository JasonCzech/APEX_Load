#!/usr/bin/env python3
"""Read-only, allowlisted Kubernetes evidence for an owned disposable fault cluster."""
import argparse
import datetime
import json
import os
from pathlib import Path
import re


MAX_ITEMS = 2000
MAX_ARTIFACT_BYTES = 8 * 1024 * 1024
RESOURCES = ("pods", "deployments", "statefulsets", "replicasets", "horizontalpodautoscalers",
             "scaledobjects.keda.sh", "persistentvolumeclaims", "events")


class EvidenceError(RuntimeError):
    pass


def identifier(value):
    if value is None:
        return None
    if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,252}", value):
        raise EvidenceError("invalid identifier")
    return value


def count(value):
    if value is None:
        return None
    if isinstance(value, bool) or not isinstance(value, int) or not 0 <= value <= 2**63 - 1:
        raise EvidenceError("invalid count")
    return value


def timestamp(value):
    if value is None:
        return None
    if not isinstance(value, str) or len(value) > 64:
        raise EvidenceError("invalid timestamp")
    parsed = datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        raise EvidenceError("timestamp requires timezone")
    return parsed.isoformat()


def choice(value, allowed):
    if value is None:
        return None
    if value not in allowed or not isinstance(value, str):
        raise EvidenceError("invalid state")
    return value


def identity(obj):
    meta = obj["metadata"]
    name, uid = identifier(meta["name"]), identifier(meta["uid"])
    if not name or not uid:
        raise EvidenceError("missing identity")
    return {"name": name, "uid": uid}


def controller_owner(obj):
    owners = []
    for ref in bounded_list(obj["metadata"].get("ownerReferences", []), 64):
        if "controller" in ref and not isinstance(ref["controller"], bool):
            raise EvidenceError("invalid controller ownership")
        if ref.get("controller"):
            owner = {key: identifier(ref.get(key)) for key in ("kind", "name", "uid")}
            if not all(owner.values()):
                raise EvidenceError("missing controller identity")
            owners.append(owner)
    if len(owners) > 1:
        raise EvidenceError("ambiguous controller ownership")
    return owners[0] if owners else None


def bounded_list(values, limit=MAX_ITEMS):
    if not isinstance(values, list) or len(values) > limit:
        raise EvidenceError("invalid or oversized list")
    return values


def conditions(obj, allowed):
    result = []
    for condition in bounded_list(obj.get("status", {}).get("conditions", []), 32):
        if condition.get("type") in allowed:
            result.append({"type": condition["type"], "status": choice(condition.get("status"), ("True", "False", "Unknown"))})
    return result


def digest(value):
    # Keep the runtime digest only, never registry credentials or arbitrary URLs.
    if not isinstance(value, str):
        return None
    match = re.search(r"(?:^|@|://)(sha256:[a-fA-F0-9]{64})$", value)
    return match.group(1).lower() if match else None


def pod(obj):
    result = identity(obj)
    result["controller_owner"] = controller_owner(obj)
    result["terminating"] = obj["metadata"].get("deletionTimestamp") is not None
    spec, status = obj.get("spec", {}), obj.get("status", {})
    result.update(node=identifier(spec.get("nodeName")),
                  phase=choice(status.get("phase"), ("Pending", "Running", "Succeeded", "Failed", "Unknown")),
                  conditions=conditions(obj, ("Ready", "PodScheduled", "Initialized", "ContainersReady")))
    containers = []
    for field, category in (("containerStatuses", "application"), ("initContainerStatuses", "init")):
        for container in bounded_list(status.get(field, []), 128):
            ready = container.get("ready")
            if not isinstance(ready, bool):
                raise EvidenceError("invalid container readiness")
            state = container.get("state", {})
            states = [key for key in ("running", "waiting", "terminated") if key in state]
            if len(states) > 1:
                raise EvidenceError("ambiguous container state")
            containers.append({"name": identifier(container["name"]), "category": category,
                               "ready": ready, "restarts": count(container.get("restartCount")),
                               "state": states[0] if states else None,
                               "image_digest": digest(container.get("imageID"))})
    result["containers"] = containers
    return result


def workload(obj):
    result = identity(obj)
    result["controller_owner"] = controller_owner(obj)
    result["terminating"] = obj["metadata"].get("deletionTimestamp") is not None
    result["generation"] = count(obj["metadata"].get("generation"))
    result["desired_replicas"] = count(obj.get("spec", {}).get("replicas"))
    status = obj.get("status", {})
    result["status"] = {key: count(status.get(key)) for key in
                        ("observedGeneration", "replicas", "readyReplicas", "availableReplicas", "updatedReplicas")}
    return result


def autoscaler(obj, keda=False):
    result = identity(obj)
    spec, status = obj.get("spec", {}), obj.get("status", {})
    result["generation"] = count(obj["metadata"].get("generation"))
    ref = spec.get("scaleTargetRef", {})
    result["target"] = {"kind": identifier(ref.get("kind")), "name": identifier(ref.get("name"))}
    fields = ("minReplicaCount", "maxReplicaCount", "idleReplicaCount", "pollingInterval", "cooldownPeriod") if keda else ("minReplicas", "maxReplicas")
    result["spec"] = {key: count(spec.get(key)) for key in fields}
    fields = ("originalReplicaCount",) if keda else ("observedGeneration", "currentReplicas", "desiredReplicas")
    result["status"] = {key: count(status.get(key)) for key in fields}
    if keda:
        result["hpa_name"] = identifier(status.get("hpaName"))
    result["conditions"] = conditions(obj, ("Ready", "Active", "Fallback", "Paused") if keda else ("AbleToScale", "ScalingActive", "ScalingLimited"))
    # Trigger metadata, metric selectors/queries and authentication references
    # are intentionally absent. Traffic demand comes from the independent adapter.
    return result


def claim(obj):
    result = identity(obj)
    result["terminating"] = obj["metadata"].get("deletionTimestamp") is not None
    spec = obj.get("spec", {})
    result.update(phase=choice(obj.get("status", {}).get("phase"), ("Pending", "Bound", "Lost")),
                  storage_class=identifier(spec.get("storageClassName")) if spec.get("storageClassName") else None,
                  volume=identifier(spec.get("volumeName")),
                  access_modes=[choice(mode, ("ReadWriteOnce", "ReadOnlyMany", "ReadWriteMany", "ReadWriteOncePod")) for mode in bounded_list(spec.get("accessModes", []), 4)])
    return result


def event(obj):
    result = identity(obj)
    ref = obj.get("involvedObject", {})
    result.update(type=choice(obj.get("type"), ("Normal", "Warning")), reason=identifier(obj.get("reason")),
                  object={key: identifier(ref.get(key)) for key in ("kind", "name", "uid")},
                  count=count(obj.get("count")), first_seen=timestamp(obj.get("firstTimestamp")),
                  last_seen=timestamp(obj.get("lastTimestamp")), event_time=timestamp(obj.get("eventTime")))
    # Kubernetes messages may include environment values, URLs and credentials.
    # Omit messages, annotations and source/controller instance data entirely.
    return result


def node(obj):
    result = identity(obj)
    labels = obj["metadata"].get("labels", {})
    result.update(control_plane="node-role.kubernetes.io/control-plane" in labels,
                  zone=identifier(labels.get("topology.kubernetes.io/zone")),
                  conditions=conditions(obj, ("Ready", "MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable")))
    return result


PROJECTORS = {"pods": pod, "deployments": workload, "statefulsets": workload, "replicasets": workload,
              "horizontalpodautoscalers": autoscaler, "scaledobjects.keda.sh": lambda obj: autoscaler(obj, True),
              "persistentvolumeclaims": claim, "events": event}


def items(payload):
    values = payload["items"]
    if not isinstance(values, list) or len(values) > MAX_ITEMS:
        raise EvidenceError("resource inventory exceeds bound or is invalid")
    # Do not silently claim a paginated subset is the complete inventory.
    if payload.get("metadata", {}).get("continue"):
        raise EvidenceError("resource inventory is incomplete")
    return values


def namespace_identity(config, kube):
    obj = kube("get", "namespace", config["namespace"])
    if obj["metadata"].get("labels", {}).get("apexload.io/fault-run") != config["run_id"]:
        raise EvidenceError("namespace ownership mismatch")
    result = identity(obj)
    if result["name"] != config["namespace"]:
        raise EvidenceError("namespace identity mismatch")
    return result


def snapshot(config, kube):
    """Return a sanitized read-only snapshot, explicitly labelling observation gaps."""
    started = datetime.datetime.now(datetime.timezone.utc).isoformat()
    namespace = namespace_identity(config, kube)
    nodes = items(kube("get", "nodes"))
    if len(nodes) < 3 or any(not n["metadata"]["name"].startswith(config["cluster"] + "-") for n in nodes):
        raise EvidenceError("nodes do not belong to disposable multi-node cluster")
    if len({n["metadata"]["name"] for n in nodes}) != len(nodes):
        raise EvidenceError("duplicate node identity")
    result = {"schema_version": 1, "run_id": config["run_id"], "cluster": config["cluster"],
              "namespace": namespace, "started_at": started, "nodes": [node(n) for n in nodes], "resources": {}}
    for resource in RESOURCES:
        try:
            payload = kube("get", resource)
            values = items(payload)
            if any(obj["metadata"].get("namespace") != config["namespace"] for obj in values):
                raise EvidenceError("resource namespace mismatch")
            projected = [PROJECTORS[resource](obj) for obj in values]
            if len({obj["name"] for obj in projected}) != len(projected):
                raise EvidenceError("duplicate resource identity")
            result["resources"][resource] = {"status": "observed", "items": projected}
        except (KeyError, TypeError, ValueError, EvidenceError):
            result["resources"][resource] = {"status": "invalid_response"}
        except Exception:
            # Only a fixed status crosses the command boundary, never raw stderr
            # or exception text (including credentials in kubeconfig/plugin errors).
            result["resources"][resource] = {"status": "unavailable"}
    if namespace_identity(config, kube) != namespace:
        raise EvidenceError("namespace changed during snapshot")
    result["completed_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
    result["inventory_complete"] = all(value["status"] == "observed" for value in result["resources"].values())
    return result


def write_snapshot(path, evidence):
    """Create a private artifact without overwriting prior evidence or following links."""
    data = (json.dumps(evidence, indent=2, allow_nan=False) + "\n").encode("utf-8")
    if len(data) > MAX_ARTIFACT_BYTES:
        raise EvidenceError("cluster evidence artifact exceeds size bound")
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as stream:
        stream.write(data)


def main():
    # Share the driver's explicit disposable targeting and command boundary.
    from node_fault import Harness, validate_config
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True)
    parser.add_argument("--output", required=True, help="new private output directory")
    args = parser.parse_args()
    try:
        config = json.loads(Path(args.config).read_text())
        validate_config(config)
        harness = Harness(config, args.output)
        evidence = snapshot(config, harness.kube)
        write_snapshot(Path(args.output) / "cluster.json", evidence)
    except (Exception, KeyboardInterrupt):
        raise SystemExit("Cluster evidence collection failed; no raw command output was retained") from None
    if not evidence["inventory_complete"]:
        raise SystemExit("Cluster evidence is incomplete; inspect resource statuses in cluster.json")


if __name__ == "__main__":
    main()
