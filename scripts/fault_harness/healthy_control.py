#!/usr/bin/env python3
"""Measure a bounded healthy control window without injecting a node fault."""
import argparse
import json
import math
from pathlib import Path
import signal
import sys

from cluster_evidence import snapshot
from cluster_sampler import ClusterSampler
from node_fault import GateError, Harness, validate_config, validate_observation


def validate_control_config(config):
    validate_config(config)
    seconds = config.get("control_seconds")
    if (isinstance(seconds, bool) or not isinstance(seconds, (int, float))
            or not math.isfinite(seconds) or not 10 <= seconds <= 1800):
        raise GateError("control_seconds must be between 10 and 1800")
    if "restoration_readiness_seconds" in config:
        raise GateError("restoration readiness is not a healthy control option")


class HealthyControl(Harness):
    def __init__(self, config, output, **kwargs):
        validate_control_config(config)
        super().__init__(config, output, **kwargs)

    def inventory(self, phase):
        # Inventories are mandatory scope checkpoints, independent of the optional
        # background series. Each snapshot shares a bounded command budget.
        deadline = self.clock() + 20
        evidence = snapshot(self.config, lambda *args: self.kube(*args, deadline=deadline))
        if self.clock() >= deadline:
            raise GateError("control inventory deadline exceeded")
        self.record("cluster_evidence", **self.persist_cluster_evidence(phase, evidence))
        return evidence

    def read_before(self, deadline):
        remaining = deadline - self.clock()
        if remaining <= 0:
            raise GateError("control observation deadline exceeded")
        observation = self.observe(timeout=min(5, remaining))
        if self.clock() >= deadline:
            raise GateError("control observation deadline exceeded")
        if observation["controller_restarts"] != 0:
            raise GateError("healthy control requires zero controller restarts")
        return observation

    def run_control(self):
        sampler = None
        try:
            before = self.inventory("control-preflight")
            previous, healthy_since = None, None
            deadline = self.clock() + self.config["recovery_seconds"]
            while True:
                observation = self.read_before(deadline)
                healthy = validate_observation(observation, self.config, previous)
                healthy_since = (healthy_since if healthy_since is not None else self.clock()) if healthy else None
                previous = observation
                if healthy_since is not None and self.clock() - healthy_since >= self.config["baseline_seconds"]:
                    break
                self.sleep(min(1, max(0, deadline - self.clock())))
            self.record("control_baseline_passed")
            if "cluster_evidence_interval_seconds" in self.config:
                sampler = ClusterSampler(self.config, before["namespace"], self.output / "cluster-series",
                                         self.kube, clock=self.clock)
                sampler.start()
                self.record("cluster_sampling_started", directory="cluster-series",
                            interval_seconds=self.config["cluster_evidence_interval_seconds"])
            started = self.clock()
            end = started + self.config["control_seconds"]
            # Take a final sample at/after the requested window. Five seconds is
            # a bounded observation allowance, not a retry or outage allowance.
            deadline = end + 5
            self.record("control_window_started", duration_seconds=self.config["control_seconds"],
                        final_observation_allowance_seconds=5)
            while True:
                self.sleep(min(1, max(0, end - self.clock())))
                observation = self.read_before(deadline)
                if not validate_observation(observation, self.config, previous):
                    raise GateError("healthy control load fell below the required thresholds")
                previous = observation
                if self.clock() >= end:
                    break
            elapsed = self.clock() - started
            if sampler is not None:
                sampler.stop()
                self.finish_cluster_sampling(sampler)
                sampler = None
            after = self.inventory("control-completed")
            identities = lambda evidence: {(n["name"], n["uid"]) for n in evidence["nodes"]}
            if before["namespace"] != after["namespace"] or identities(before) != identities(after):
                raise GateError("control cluster identity changed")
            self.record("control_gate_passed", duration_seconds=self.config["control_seconds"], elapsed_seconds=elapsed)
        except BaseException:
            # Do not record raw adapter, kubeconfig or transport errors.
            self.record("control_gate_failed")
            raise
        finally:
            if sampler is not None:
                failed = sys.exc_info()[0] is not None
                sampler.stop()
                try:
                    self.finish_cluster_sampling(sampler)
                except BaseException:
                    if not failed:
                        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    try:
        config = json.loads(Path(args.config).read_text())
        HealthyControl(config, args.output).run_control()
    except (Exception, KeyboardInterrupt):
        raise SystemExit("Healthy control failed; inspect private evidence") from None


if __name__ == "__main__":
    main()
