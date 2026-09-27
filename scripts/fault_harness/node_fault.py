#!/usr/bin/env python3
"""Bounded node-stop driver for explicitly owned, disposable kind test clusters.

The observation adapter must return fresh target-observed traffic and assignments.
It is an argv array, never a shell fragment. This driver does not qualify recovery
from historical dashboard TPS and never force-deletes pods, nodes or PVCs.
"""
import argparse
import datetime
import json
import math
import os
from pathlib import Path
import re
import signal
import subprocess
import time


class GateError(RuntimeError):
    pass


def command(argv, timeout=20):
    result = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
    if result.returncode:
        # Command output can contain credentials. Keep it out of public evidence.
        raise GateError(f"{Path(argv[0]).name} failed with exit {result.returncode}")
    return result.stdout


def validate_config(config):
    run_id = config.get("run_id", "")
    if not re.fullmatch(r"[a-f0-9]{16}", run_id):
        raise GateError("run_id must be 16 lowercase hexadecimal characters")
    cluster = "apexload-fault-" + run_id
    if config.get("cluster") != cluster or config.get("context") != "kind-" + cluster:
        raise GateError("only the run's disposable kind cluster is allowed")
    if config.get("namespace") != "apexload-fault":
        raise GateError("unexpected disposable namespace")
    if not Path(config.get("kubeconfig", "")).is_file():
        raise GateError("an explicit existing kubeconfig is required")
    for key in ("baseline_seconds", "fault_seconds", "recovery_seconds", "expected_vusers", "minimum_target_rps"):
        value = config.get(key)
        if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value <= 0:
            raise GateError(f"{key} must be finite and positive")
    if not 10 <= config["baseline_seconds"] <= config["recovery_seconds"] or config["recovery_seconds"] > 1800 or config["fault_seconds"] > 1800:
        raise GateError("baseline must be >=10s; fault/recovery bounds must be <=1800s")
    adapter = config.get("observation_command")
    if not isinstance(adapter, list) or not adapter or any(not isinstance(a, str) or not a for a in adapter):
        raise GateError("observation_command must be a nonempty argv array")


def validate_observation(observation, config, previous=None, now=None):
    now = time.time() if now is None else now
    if observation.get("run_id") != config["run_id"]:
        raise GateError("observation belongs to another run")
    at = observation.get("observed_at_unix", 0)
    if not isinstance(at, (int, float)) or not math.isfinite(at) or not 0 <= now - at <= 5:
        raise GateError("target observation is stale or in the future")
    # Counts come from the private target; dashboard TPS is deliberately ignored.
    count = observation.get("target_requests")
    if isinstance(count, bool) or not isinstance(count, int) or count < 0:
        raise GateError("target_requests must be a nonnegative counter")
    assignments = observation.get("assignments")
    if not isinstance(assignments, list) or not assignments:
        raise GateError("current shard assignments are required")
    seen = set()
    for shard in assignments:
        key = (shard.get("group"), shard.get("shard"))
        if key in seen or not all(isinstance(v, str) and v for v in key):
            raise GateError("duplicate or invalid shard ownership")
        seen.add(key)
        if not shard.get("node") or not shard.get("pod") or not isinstance(shard.get("generation"), int) or shard["generation"] <= 0:
            raise GateError("assignment lacks node/pod/generation evidence")
    if not previous:
        return False
    elapsed = at - previous["observed_at_unix"]
    delta = count - previous["target_requests"]
    if elapsed <= 0 or delta < 0:
        raise GateError("target counter or observation clock regressed")
    return (observation.get("status") == "RUNNING" and delta / elapsed >= config["minimum_target_rps"]
            and observation.get("observed_vusers", 0) >= config["expected_vusers"]
            and observation.get("keda_total_demand", 0) >= config["expected_vusers"])


def public_observation(observation):
    # Whitelist evidence; the adapter must never make raw API/Secret snapshots public.
    result = {key: observation.get(key) for key in (
        "run_id", "observed_at_unix", "target_requests", "observed_vusers",
        "keda_total_demand", "controller_restarts", "status")}
    result["assignments"] = [{key: shard.get(key) for key in
                               ("group", "shard", "node", "pod", "generation")}
                              for shard in observation.get("assignments", [])]
    return result


class Harness:
    def __init__(self, config, output, execute=command, clock=time.monotonic, sleep=time.sleep):
        validate_config(config)
        self.config, self.output, self.execute = config, Path(output), execute
        self.clock, self.sleep = clock, sleep
        self.events = []
        self.victim_id = None
        self.restore_required = False

    def kube(self, *args):
        c = self.config
        return json.loads(self.execute(["kubectl", "--kubeconfig", c["kubeconfig"], "--context", c["context"],
                                        "--request-timeout=10s", "-n", c["namespace"], *args, "-o", "json"]))

    def record(self, kind, **fields):
        self.events.append({"event": kind, "at": datetime.datetime.now(datetime.timezone.utc).isoformat(), **fields})
        self.output.mkdir(parents=True, exist_ok=True, mode=0o700)
        tmp = self.output / "events.tmp"
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as f:
            json.dump(self.events, f, indent=2)
        tmp.replace(self.output / "events.json")

    def preflight(self, node):
        c = self.config
        namespace = self.kube("get", "namespace", c["namespace"])
        if namespace["metadata"].get("labels", {}).get("apexload.io/fault-run") != c["run_id"]:
            raise GateError("namespace is not owned by this fault run")
        nodes = self.kube("get", "nodes")["items"]
        if len(nodes) < 3 or any(not n["metadata"]["name"].startswith(c["cluster"] + "-") for n in nodes):
            raise GateError("Kubernetes nodes do not belong to the disposable multi-node cluster")
        selected = next((n for n in nodes if n["metadata"]["name"] == node), None)
        if selected is None or "node-role.kubernetes.io/control-plane" in selected["metadata"].get("labels", {}):
            raise GateError("victim must be an existing worker, not the control plane")
        info = json.loads(self.execute(["docker", "inspect", node]))[0]
        labels = info["Config"].get("Labels", {})
        if labels.get("io.x-k8s.kind.cluster") != c["cluster"] or labels.get("io.x-k8s.kind.role") != "worker":
            raise GateError("Docker container is not this run's kind worker")
        if not info["State"]["Running"] or info.get("Name", "").lstrip("/") != node:
            raise GateError("victim must be the named running container")
        self.victim_id = info["Id"]  # immutable ID prevents name reuse during cleanup
        self.record("preflight", node=node, container_id=self.victim_id, image_id=info["Image"])

    def observe(self):
        raw = self.execute(self.config["observation_command"], timeout=15)
        observation = json.loads(raw)
        validate_observation(observation, self.config)
        self.record("observation", observation=public_observation(observation))
        return observation

    def restore(self):
        if self.restore_required:
            self.execute(["docker", "start", self.victim_id])
            info = json.loads(self.execute(["docker", "inspect", self.victim_id]))[0]
            if not info["State"]["Running"]:
                raise GateError("cleanup failed: victim remains stopped")
            self.restore_required = False
            self.record("node_restored", container_id=self.victim_id)

    def run(self, node):
        self.preflight(node)
        c = self.config
        previous = None
        healthy_since = None
        deadline = self.clock() + c["recovery_seconds"]
        baseline_restarts = None
        while self.clock() < deadline:
            observation = self.observe()
            restarts = observation.get("controller_restarts")
            if not isinstance(restarts, int) or restarts < 0:
                raise GateError("controller restart evidence is required")
            if baseline_restarts is None:
                if restarts != 0:
                    raise GateError("clean baseline has prior controller restarts")
                baseline_restarts = restarts
            if restarts != baseline_restarts:
                raise GateError("controller restarted before injection")
            healthy = validate_observation(observation, c, previous)
            healthy_since = (healthy_since if healthy_since is not None else self.clock()) if healthy else None
            previous = observation
            if healthy_since is not None and self.clock() - healthy_since >= c["baseline_seconds"]:
                break
            self.sleep(1)
        else:
            raise GateError("requested target-observed load was never sustained")
        if not any(s["node"] == node for s in previous["assignments"]):
            raise GateError("victim carries no active shard")
        # Register restoration before kill: a timeout can mean the kill succeeded.
        self.restore_required = True
        fault_at = self.clock()
        recovered_at = None
        recovery_healthy_since = None
        try:
            self.execute(["docker", "kill", "--signal", "KILL", self.victim_id])
            self.record("fault_injected", node=node)
            previous = None
            while self.clock() - fault_at < max(c["fault_seconds"], c["recovery_seconds"]):
                elapsed = self.clock() - fault_at
                if elapsed >= c["fault_seconds"]:
                    self.restore()
                try:
                    observation = self.observe()
                except (GateError, ValueError, subprocess.TimeoutExpired):
                    previous = None
                    recovery_healthy_since = None
                    self.record("observation_unavailable", elapsed=elapsed)
                    self.sleep(1)
                    continue
                if observation.get("controller_restarts") != baseline_restarts:
                    raise GateError("unintended controller restart after injection")
                healthy = validate_observation(observation, c, previous)
                previous = observation
                recovery_healthy_since = (recovery_healthy_since if recovery_healthy_since is not None else elapsed) if healthy else None
                if (recovered_at is None and self.restore_required and recovery_healthy_since is not None
                        and elapsed - recovery_healthy_since >= c["baseline_seconds"]):
                    recovered_at = recovery_healthy_since
                    self.record("traffic_recovered", elapsed=recovered_at)
                self.sleep(1)
            if recovered_at is None or recovered_at > c["recovery_seconds"]:
                raise GateError("traffic recovery deadline exceeded")
            self.record("traffic_gate_passed", recovery_seconds=recovered_at)
        finally:
            self.restore()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True)
    parser.add_argument("--node", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    harness = Harness(json.loads(Path(args.config).read_text()), args.output)
    try:
        harness.run(args.node)
    except (GateError, ValueError, subprocess.TimeoutExpired, KeyboardInterrupt) as error:
        harness.record("failed", reason=type(error).__name__)
        raise SystemExit("Fault test failed; inspect private evidence and node restoration state") from None


if __name__ == "__main__":
    main()
