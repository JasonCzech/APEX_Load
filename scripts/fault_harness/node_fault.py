#!/usr/bin/env python3
"""Bounded node-stop driver for explicitly owned, disposable kind test clusters.

The observation adapter must return fresh target-observed traffic and assignments.
It is an argv array, never a shell fragment. This driver does not qualify recovery
from historical dashboard TPS and never force-deletes pods, nodes or PVCs.
"""
import argparse
from collections import Counter
import datetime
import hashlib
import json
import math
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time

from cluster_evidence import snapshot as cluster_snapshot, write_snapshot
from cluster_readiness import assess as assess_cluster_readiness
from cluster_sampler import ClusterSampler


class GateError(RuntimeError):
    pass


class ObservationSafetyError(GateError):
    """An observed safety violation cannot be retried away as a transport outage."""


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
    if not isinstance(config.get("capture_cluster_evidence", False), bool):
        raise GateError("capture_cluster_evidence must be a boolean")
    if "cluster_evidence_interval_seconds" in config:
        interval = config["cluster_evidence_interval_seconds"]
        if (isinstance(interval, bool) or not isinstance(interval, (int, float))
                or not math.isfinite(interval) or not 5 <= interval <= 300):
            raise GateError("cluster_evidence_interval_seconds must be between 5 and 300")
        if not config.get("capture_cluster_evidence", False):
            raise GateError("cluster sampling requires capture_cluster_evidence")
    if "restoration_readiness_seconds" in config:
        budget = config["restoration_readiness_seconds"]
        if (isinstance(budget, bool) or not isinstance(budget, (int, float))
                or not math.isfinite(budget) or not 1 <= budget <= 1800):
            raise GateError("restoration_readiness_seconds must be between 1 and 1800")
        if not config.get("capture_cluster_evidence", False):
            raise GateError("restoration readiness requires capture_cluster_evidence")
    adapter = config.get("observation_command")
    if not isinstance(adapter, list) or not adapter or any(not isinstance(a, str) or not a for a in adapter):
        raise GateError("observation_command must be a nonempty argv array")


def validate_observation(observation, config, previous=None, now=None):
    now = time.time() if now is None else now
    if not isinstance(observation, dict) or observation.get("run_id") != config["run_id"]:
        raise ObservationSafetyError("observation must belong to this run")
    instance = observation.get("target_instance_id")
    if not isinstance(instance, str) or not re.fullmatch(r"[0-9a-f]{32}", instance):
        raise ObservationSafetyError("target process identity is required")
    if previous is not None and instance != previous.get("target_instance_id"):
        raise ObservationSafetyError("target instance changed")
    at = observation.get("observed_at_unix", 0)
    if isinstance(at, bool) or not isinstance(at, (int, float)) or not now - 5 <= at <= now:
        raise GateError("target observation is stale or in the future")
    # Counts come from the private target; dashboard TPS is deliberately ignored.
    count = observation.get("target_requests")
    if isinstance(count, bool) or not isinstance(count, int) or count < 0:
        raise ObservationSafetyError("target_requests must be a nonnegative counter")
    for key in ("observed_vusers", "keda_total_demand", "controller_restarts"):
        value = observation.get(key)
        if (isinstance(value, bool) or not isinstance(value, (int, float)) or value < 0
                or (isinstance(value, float) and not math.isfinite(value))
                or (key != "keda_total_demand" and not isinstance(value, int))):
            raise ObservationSafetyError(f"{key} must be a finite nonnegative count")
    status = observation.get("status")
    if not isinstance(status, str) or not status:
        raise ObservationSafetyError("run status evidence is required")
    if status in ("COMPLETE", "FAILED", "ABORTED", "STOPPED"):
        raise ObservationSafetyError("run became terminal before the fault trial ended")
    assignments = observation.get("assignments")
    if not isinstance(assignments, list) or not assignments:
        raise GateError("current shard assignments are required")
    seen = set()
    for shard in assignments:
        if not isinstance(shard, dict):
            raise ObservationSafetyError("invalid shard ownership evidence")
        key = (shard.get("group"), shard.get("shard"))
        if not all(isinstance(v, str) and v for v in key) or key in seen:
            raise ObservationSafetyError("duplicate or invalid shard ownership")
        seen.add(key)
        if (any(not isinstance(shard.get(key), str) or not shard[key] for key in ("node", "pod"))
                or isinstance(shard.get("generation"), bool) or not isinstance(shard.get("generation"), int)
                or shard["generation"] <= 0):
            raise ObservationSafetyError("assignment lacks node/pod/generation evidence")
    if not previous:
        return False
    elapsed = at - previous["observed_at_unix"]
    delta = count - previous["target_requests"]
    if elapsed <= 0 or delta < 0:
        raise ObservationSafetyError("target counter or observation clock regressed")
    return (observation.get("status") == "RUNNING" and delta / elapsed >= config["minimum_target_rps"]
            and observation.get("observed_vusers", 0) >= config["expected_vusers"]
            and observation.get("keda_total_demand", 0) >= config["expected_vusers"])


def public_observation(observation):
    # Whitelist evidence; the adapter must never make raw API/Secret snapshots public.
    result = {key: observation.get(key) for key in (
        "run_id", "target_instance_id", "observed_at_unix", "target_requests", "observed_vusers",
        "keda_total_demand", "controller_restarts", "status")}
    result["assignments"] = [{key: shard.get(key) for key in
                               ("group", "shard", "node", "pod", "generation")}
                              for shard in observation.get("assignments", [])]
    return result


class Harness:
    def __init__(self, config, output, execute=command, clock=time.monotonic, sleep=time.sleep):
        validate_config(config)
        self.config, self.output, self.execute = config, Path(output), execute
        # A retry must not overwrite the only evidence of an earlier failed run.
        self.output.mkdir(parents=True, exist_ok=False, mode=0o700)
        self.clock, self.sleep = clock, sleep
        self.events = []
        self.victim_id = None
        self.restore_required = False
        self.last_observation = None

    def kube(self, *args, deadline=None):
        c = self.config
        timeout = 20
        if deadline is not None:
            timeout = min(timeout, deadline - self.clock())
            if timeout <= 0:
                raise GateError("cluster readiness read deadline exceeded")
        return json.loads(self.execute(["kubectl", "--kubeconfig", c["kubeconfig"], "--context", c["context"],
                                        "--request-timeout=10s", "-n", c["namespace"], *args, "-o", "json"], timeout=timeout))

    def record(self, kind, **fields):
        self.events.append({"event": kind, "at": datetime.datetime.now(datetime.timezone.utc).isoformat(), **fields})
        self.output.mkdir(parents=True, exist_ok=True, mode=0o700)
        tmp = self.output / "events.tmp"
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as f:
            json.dump(self.events, f, indent=2)
        tmp.replace(self.output / "events.json")

    def capture_cluster_evidence(self, phase, required=False):
        if not self.config.get("capture_cluster_evidence", False):
            return
        try:
            evidence = cluster_snapshot(self.config, self.kube)
            fields = self.persist_cluster_evidence(phase, evidence)
        except Exception:
            self.record("cluster_evidence_unavailable", phase=phase)
            if required:
                # Do not inject after a failed ownership check or failed artifact
                # write. Raw exception strings may contain credentials.
                raise GateError("preflight cluster evidence could not be captured") from None
            return
        self.record("cluster_evidence", **fields)
        return evidence

    def persist_cluster_evidence(self, phase, evidence):
        filename = "cluster-" + phase + ".json"
        path = self.output / filename
        write_snapshot(path, evidence)
        checksum = hashlib.sha256(path.read_bytes()).hexdigest()
        return dict(phase=phase, file=filename, sha256=checksum,
                    inventory_complete=evidence["inventory_complete"],
                    gaps=[key for key, value in evidence["resources"].items() if value["status"] != "observed"])

    def wait_for_cluster_readiness(self, baseline):
        budget = self.config["restoration_readiness_seconds"]
        started = self.clock()
        deadline = started + budget
        self.record("cluster_readiness_started", budget_seconds=budget)
        attempts, ready, evidence = 0, False, None
        result = {"ready": False, "issues": [{"resource": "cluster", "name": None, "reason": "inventory_unavailable"}]}
        while self.clock() < deadline:
            attempts += 1
            try:
                evidence = cluster_snapshot(self.config, lambda *args: self.kube(*args, deadline=deadline))
                result = assess_cluster_readiness(baseline, evidence)
            except Exception:
                evidence = None
                result = {"ready": False, "issues": [{"resource": "cluster", "name": None, "reason": "inventory_unavailable"}]}
            # Even an otherwise ready response arriving at/after the deadline
            # cannot turn an exceeded budget into a success.
            ready = result["ready"] and self.clock() < deadline
            self.record("cluster_readiness_observation", attempt=attempts, elapsed_seconds=self.clock() - started,
                        ready=ready, reasons=dict(Counter(issue["reason"] for issue in result["issues"])))
            if ready:
                break
            remaining = deadline - self.clock()
            if remaining > 0:
                self.sleep(min(5, remaining))
        elapsed = self.clock() - started
        if evidence is not None:
            self.record("cluster_evidence", **self.persist_cluster_evidence("restored", evidence))
        else:
            self.record("cluster_evidence_unavailable", phase="restored")
        report = {"schema_version": 1, "run_id": self.config["run_id"], "cluster": self.config["cluster"],
                  "namespace": baseline["namespace"], "ready": ready, "budget_seconds": budget,
                  "elapsed_seconds": elapsed, "attempts": attempts,
                  "deadline_exceeded": not ready and elapsed >= budget, "last_observation": result}
        path = self.output / "cluster-readiness.json"
        write_snapshot(path, report)
        self.record("cluster_readiness_passed" if ready else "cluster_readiness_failed", file=path.name,
                    sha256=hashlib.sha256(path.read_bytes()).hexdigest(), elapsed_seconds=report["elapsed_seconds"])
        if not ready:
            raise GateError("Kubernetes restoration readiness deadline exceeded")

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

    def finish_cluster_sampling(self, sampler):
        report = sampler.finish()
        path = self.output / "cluster-series.json"
        written = write_snapshot(path, report)
        self.record("cluster_sampling_finished", file=path.name, sha256=written["sha256"],
                    directory="cluster-series", worker_stopped=report["worker_stopped"],
                    stop_reason=report["stop_reason"], attempts=len(report["attempts"]),
                    observed_samples=sum(a["status"] == "observed" for a in report["attempts"]))
        if not report["worker_stopped"] or report["stop_reason"] != "stopped":
            raise GateError("background cluster evidence sampling failed; inspect series index")

    def observe(self, timeout=15):
        raw = self.execute(self.config["observation_command"], timeout=timeout)
        try:
            observation = json.loads(raw)
        except ValueError:
            raise ObservationSafetyError("observation adapter returned invalid JSON") from None
        # Check process continuity before freshness: a stale replacement sample
        # must not be retried away and hidden by a later sample from the old target.
        if (self.last_observation is not None and isinstance(observation, dict)
                and observation.get("target_instance_id") != self.last_observation["target_instance_id"]):
            raise ObservationSafetyError("target instance changed")
        validate_observation(observation, self.config)
        if self.last_observation is not None:
            # Resetting the consecutive-load window after a transport outage must
            # not forget counter continuity or hide a target restart.
            if (observation["target_requests"] < self.last_observation["target_requests"]
                    or observation["observed_at_unix"] < self.last_observation["observed_at_unix"]):
                raise ObservationSafetyError("target counter or observation clock regressed")
        self.last_observation = observation
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
        # Capture before the sustained baseline so inventory reads cannot age its
        # final target observation or consume the stopped-node recovery budget.
        baseline_inventory = self.capture_cluster_evidence("preflight", required=True)
        c = self.config
        if "restoration_readiness_seconds" in c:
            readiness = assess_cluster_readiness(baseline_inventory, baseline_inventory)
            self.record("cluster_readiness_preflight", **readiness)
            if not readiness["ready"]:
                raise GateError("Kubernetes baseline is not ready for restoration qualification")
        previous = None
        healthy_since = None
        deadline = self.clock() + c["recovery_seconds"]
        baseline_restarts = None
        while self.clock() < deadline:
            observation = self.observe(timeout=min(15, deadline - self.clock()))
            if self.clock() >= deadline:
                raise GateError("baseline observation deadline exceeded")
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
            self.sleep(min(1, max(0, deadline - self.clock())))
        else:
            raise GateError("requested target-observed load was never sustained")
        if not any(s["node"] == node for s in previous["assignments"]):
            raise GateError("victim carries no active shard")
        sampler = None
        if "cluster_evidence_interval_seconds" in c:
            try:
                sampler = ClusterSampler(c, baseline_inventory["namespace"], self.output / "cluster-series",
                                         self.kube, clock=self.clock)
            except Exception:
                raise GateError("background cluster evidence could not be prepared") from None
        # Register restoration before kill: a timeout can mean the kill succeeded.
        self.restore_required = True
        fault_at = self.clock()
        recovered_at = None
        recovery_healthy_since = None
        trial_deadline = fault_at + max(c["fault_seconds"], c["recovery_seconds"])
        recovery_deadline = fault_at + min(c["fault_seconds"], c["recovery_seconds"])
        try:
            self.execute(["docker", "kill", "--signal", "KILL", self.victim_id])
            self.record("fault_injected", node=node)
            if sampler is not None:
                self.record("cluster_sampling_started", directory="cluster-series",
                            interval_seconds=c["cluster_evidence_interval_seconds"])
                try:
                    sampler.start()
                except Exception:
                    raise GateError("background cluster evidence could not be started") from None
            previous = None
            while self.clock() < trial_deadline:
                elapsed = self.clock() - fault_at
                if recovered_at is None and self.clock() >= recovery_deadline:
                    raise GateError("traffic recovery deadline exceeded")
                if elapsed >= c["fault_seconds"]:
                    self.restore()
                # Reads must not postpone the next restoration/recovery boundary.
                read_deadline = trial_deadline
                if self.restore_required:
                    read_deadline = min(read_deadline, fault_at + c["fault_seconds"])
                if recovered_at is None:
                    read_deadline = min(read_deadline, recovery_deadline)
                remaining = read_deadline - self.clock()
                if remaining <= 0:
                    continue
                try:
                    observation = self.observe(timeout=min(15, remaining))
                    if observation.get("controller_restarts") != baseline_restarts:
                        raise ObservationSafetyError("unintended controller restart after injection")
                    elapsed = self.clock() - fault_at
                    if self.clock() >= read_deadline:
                        raise GateError("fault observation deadline exceeded")
                except ObservationSafetyError as error:
                    # These messages are fixed validation reasons, never adapter
                    # output or unfiltered source values.
                    self.record("observation_safety_failed", reason=str(error))
                    raise
                except (GateError, ValueError, subprocess.TimeoutExpired):
                    previous = None
                    recovery_healthy_since = None
                    self.record("observation_unavailable", elapsed=self.clock() - fault_at)
                    self.sleep(min(1, max(0, read_deadline - self.clock())))
                    continue
                healthy = validate_observation(observation, c, previous)
                previous = observation
                recovery_healthy_since = (recovery_healthy_since if recovery_healthy_since is not None else elapsed) if healthy else None
                if (recovered_at is None and self.restore_required and recovery_healthy_since is not None
                        and elapsed - recovery_healthy_since >= c["baseline_seconds"]):
                    recovered_at = elapsed
                    self.record("traffic_recovered", elapsed=recovered_at,
                                healthy_since_seconds=recovery_healthy_since)
                self.sleep(min(1, max(0, read_deadline - self.clock())))
            if recovered_at is None:
                raise GateError("traffic recovery deadline exceeded")
            self.record("traffic_gate_passed", recovery_seconds=recovered_at)
        finally:
            trial_failed = sys.exc_info()[0] is not None
            trial_interrupted = isinstance(sys.exc_info()[1], KeyboardInterrupt)
            if sampler is not None:
                sampler.stop()  # Nonblocking: never join before attempting restoration.
            try:
                self.restore()
            except BaseException:
                if sampler is not None:
                    try:
                        self.finish_cluster_sampling(sampler)
                    except BaseException:
                        pass  # Keep the restoration failure, including interruption.
                raise
            sampling_error = None
            if sampler is not None:
                try:
                    self.finish_cluster_sampling(sampler)
                except Exception:
                    sampling_error = GateError("background cluster evidence could not be finalized")
            # Restoration always precedes diagnostic reads. Even a failure to
            # write the gap event must preserve the original trial exception.
            try:
                if "restoration_readiness_seconds" in c:
                    if trial_interrupted:
                        self.record("cluster_readiness_skipped", reason="trial_interrupted")
                    else:
                        self.wait_for_cluster_readiness(baseline_inventory)
                else:
                    self.capture_cluster_evidence("restored")
            except GateError:
                if not trial_failed:
                    raise
            except Exception:
                if not trial_failed:
                    raise GateError("restored cluster evidence could not be recorded") from None
            if sampling_error is not None and not trial_failed:
                raise sampling_error from None


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
