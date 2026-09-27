"""Conservative recovery checks over sanitized, same-run cluster inventories.

This is a Kubernetes readiness observation, not a data-integrity, fencing or
application recovery guarantee. Inputs are produced by cluster_evidence.snapshot.
"""


def assess(baseline, current):
    issues = []

    def issue(resource, name, reason):
        issues.append(dict(resource=resource, name=name, reason=reason))

    try:
        for key in ("schema_version", "run_id", "cluster", "namespace"):
            if current[key] != baseline[key]:
                issue("cluster", None, "identity_changed")
                return {"ready": False, "issues": issues}
        required = ("pods", "deployments", "statefulsets", "replicasets", "persistentvolumeclaims")
        for evidence in (baseline, current):
            for resource in required:
                if evidence["resources"][resource]["status"] != "observed":
                    issue(resource, None, "inventory_gap")
        if issues:
            return {"ready": False, "issues": issues}

        def index(values):
            result = {obj["name"]: obj for obj in values}
            if len(result) != len(values):
                raise ValueError("duplicate identity")
            return result

        def resources(evidence, resource):
            return index(evidence["resources"][resource]["items"])

        def condition(obj, name):
            matches = [c["status"] for c in obj["conditions"] if c["type"] == name]
            return matches[0] if len(matches) == 1 else None

        def retained(resource, before, after):
            for name, obj in before.items():
                if name not in after or obj["uid"] != after[name]["uid"]:
                    issue(resource, name, "identity_missing_or_replaced")

        before_nodes, nodes = index(baseline["nodes"]), index(current["nodes"])
        if len(before_nodes) < 3:
            issue("nodes", None, "insufficient_baseline")
        retained("nodes", before_nodes, nodes)
        for name, node in nodes.items():
            if condition(node, "Ready") != "True":
                issue("nodes", name, "not_ready")
            for key in ("MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable"):
                reported = [c for c in node["conditions"] if c["type"] == key]
                if reported and condition(node, key) != "False":
                    issue("nodes", name, "pressure_or_network_unknown")

        workloads = {}
        for resource, kind in (("deployments", "Deployment"), ("statefulsets", "StatefulSet")):
            before, after = resources(baseline, resource), resources(current, resource)
            retained(resource, before, after)
            for name, obj in after.items():
                workloads[(kind, name, obj["uid"])] = (resource, obj)
                desired, generation = obj["desired_replicas"], obj["generation"]
                if obj["terminating"]:
                    issue(resource, name, "terminating")
                if type(desired) is not int or desired < 0 or type(generation) is not int or generation < 1:
                    issue(resource, name, "unknown_replica_state")
                    continue
                if name in before and before[name]["desired_replicas"] > 0 and desired == 0:
                    issue(resource, name, "scaled_to_zero")
                status = obj["status"]
                observed = status["observedGeneration"]
                if type(observed) is not int or observed < generation:
                    issue(resource, name, "generation_unobserved")
                # At zero, Kubernetes may omit the zero-valued status fields.
                fields = ("replicas", "readyReplicas", "updatedReplicas")
                if kind == "Deployment":
                    fields += ("availableReplicas",)
                if any((0 if status[k] is None and desired == 0 else status[k]) != desired for k in fields):
                    issue(resource, name, "replicas_not_converged")
        if not workloads or not any(obj["desired_replicas"] for _, obj in workloads.values()):
            issue("workloads", None, "no_active_workloads")

        before_claims = resources(baseline, "persistentvolumeclaims")
        claims = resources(current, "persistentvolumeclaims")
        retained("persistentvolumeclaims", before_claims, claims)
        for name, obj in claims.items():
            if obj["terminating"] or obj["phase"] != "Bound" or not obj["volume"]:
                issue("persistentvolumeclaims", name, "not_bound")
            if name in before_claims and any(obj[k] != before_claims[name][k] for k in ("volume", "storage_class", "access_modes")):
                issue("persistentvolumeclaims", name, "binding_changed")

        before_pods, pods = resources(baseline, "pods"), resources(current, "pods")
        # A bare target/observer pod cannot disappear and be hidden by healthy
        # deployment counts. Managed pods may legitimately be replaced on eviction.
        retained("pods", {name: obj for name, obj in before_pods.items()
                          if obj["phase"] != "Succeeded" and obj["controller_owner"] is None}, pods)
        replicasets = resources(current, "replicasets")
        pod_counts = {key: 0 for key in workloads}
        for name, obj in pods.items():
            if obj["phase"] == "Succeeded":
                if name in before_pods and before_pods[name]["phase"] != "Succeeded" and obj["controller_owner"] is None:
                    issue("pods", name, "baseline_pod_completed")
                continue
            applications = [c for c in obj["containers"] if c["category"] == "application"]
            if (obj["terminating"] or obj["phase"] != "Running" or condition(obj, "Ready") != "True"
                    or condition(obj, "ContainersReady") != "True" or not applications
                    or any(c["state"] != "running" or c["ready"] is not True for c in applications)
                    or obj["node"] not in nodes):
                issue("pods", name, "not_ready")
            owner = obj["controller_owner"]
            if owner is None:
                continue
            if owner["kind"] == "ReplicaSet":
                rs = replicasets.get(owner["name"])
                if rs is None or rs["uid"] != owner["uid"] or rs["terminating"]:
                    issue("pods", name, "owner_missing_or_replaced")
                    continue
                owner = rs["controller_owner"]
            key = tuple(owner[k] for k in ("kind", "name", "uid")) if owner else None
            if key not in workloads:
                issue("pods", name, "unsupported_or_missing_owner")
            else:
                pod_counts[key] += 1
        for key, count in pod_counts.items():
            resource, obj = workloads[key]
            if count != obj["desired_replicas"]:
                issue(resource, obj["name"], "pod_inventory_not_converged")
    except (KeyError, TypeError, ValueError):
        # Missing fields in older/malformed inventories are unknown, never healthy.
        return {"ready": False, "issues": [{"resource": "cluster", "name": None, "reason": "invalid_evidence"}]}
    return {"ready": not issues, "issues": issues}
