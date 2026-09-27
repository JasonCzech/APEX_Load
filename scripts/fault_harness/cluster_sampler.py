"""Background, read-only Kubernetes inventories; never writes driver events.

The caller restores Docker before waiting for this worker. A single worker keeps
slow API reads from accumulating concurrent polls. Stop is cooperative between
reads; each command also inherits a shared per-snapshot deadline.
"""
import copy
import datetime
from pathlib import Path
import threading
import time

from cluster_evidence import EvidenceError, snapshot, write_snapshot


MAX_ATTEMPTS = 512
MAX_SERIES_BYTES = 64 * 1024 * 1024
SNAPSHOT_SECONDS = 10
STOP_SECONDS = SNAPSHOT_SECONDS + 1


class SamplingStopped(Exception):
    pass


class ScopeChanged(EvidenceError):
    pass


class ClusterSampler:
    def __init__(self, config, namespace, output, kube, *, clock=time.monotonic):
        # Retain only targeting data, never the observation command or credentials.
        self.config = {key: config[key] for key in ("run_id", "cluster", "namespace")}
        self.namespace = dict(namespace)
        self.interval = config["cluster_evidence_interval_seconds"]
        self.output = Path(output)
        self.output.mkdir(mode=0o700, exist_ok=False)
        self.kube, self.clock = kube, clock
        self._stop = threading.Event()
        self._lock = threading.Lock()
        self._attempts = []
        self._bytes = 0
        self._reason = "not_started"
        self._started = None
        self._started_at = None
        self._thread = threading.Thread(target=self._run, name="fault-cluster-evidence", daemon=True)

    def start(self):
        # Thread.start is intentionally nonblocking with respect to Kubernetes.
        self._started = self.clock()
        self._started_at = datetime.datetime.now(datetime.timezone.utc).isoformat()
        self._reason = "sampling"
        try:
            self._thread.start()
        except BaseException:
            self._reason = "worker_start_failed"
            raise

    def _read(self, *args, deadline):
        if self._stop.is_set():
            raise SamplingStopped()
        if self.clock() >= deadline:
            raise TimeoutError()
        response = self.kube(*args, deadline=deadline)
        if self._stop.is_set():
            raise SamplingStopped()
        if args[:2] == ("get", "namespace"):
            meta = response.get("metadata", {})
            if (meta.get("name") != self.namespace["name"] or meta.get("uid") != self.namespace["uid"]
                    or meta.get("labels", {}).get("apexload.io/fault-run") != self.config["run_id"]):
                raise ScopeChanged()
        return response

    def _append(self, entry):
        # No filesystem or network operation occurs while holding the lock.
        with self._lock:
            self._attempts.append(entry)

    def _run(self):
        try:
            for attempt in range(1, MAX_ATTEMPTS + 1):
                if self._stop.is_set():
                    self._reason = "stopped"
                    return
                started = self.clock()
                entry = {"attempt": attempt, "started_seconds": started - self._started}
                try:
                    deadline = started + SNAPSHOT_SECONDS
                    evidence = snapshot(self.config, lambda *args: self._read(*args, deadline=deadline))
                    if self._stop.is_set():
                        raise SamplingStopped()
                    if self.clock() >= deadline:
                        raise TimeoutError()
                except SamplingStopped:
                    entry["status"] = "cancelled"
                    self._reason = "stopped"
                except EvidenceError:
                    entry["status"] = "scope_or_inventory_invalid"
                    self._reason = "scope_or_inventory_invalid"
                except Exception:
                    entry["status"] = "unavailable"
                else:
                    filename = f"sample-{attempt:04d}.json"
                    try:
                        written = write_snapshot(self.output / filename, evidence, max_bytes=MAX_SERIES_BYTES - self._bytes)
                    except EvidenceError:
                        entry["status"] = "artifact_limit"
                        self._reason = "artifact_limit"
                    except Exception:
                        entry["status"] = "artifact_write_failed"
                        self._reason = "artifact_write_failed"
                    else:
                        self._bytes += written["bytes"]
                        entry.update(status="observed", file=filename, **written,
                                     inventory_complete=evidence["inventory_complete"],
                                     gaps=[key for key, value in evidence["resources"].items() if value["status"] != "observed"])
                entry["completed_seconds"] = self.clock() - self._started
                self._append(entry)
                if self._reason != "sampling":
                    return
                if self._stop.wait(self.interval):
                    self._reason = "stopped"
                    return
            self._reason = "attempt_limit"
        except BaseException:
            # A background exception must never print a traceback containing raw
            # command output or silently leave the index looking successful.
            self._reason = "worker_failed"

    def stop(self):
        self._stop.set()

    def finish(self):
        """Call only after restoration has been attempted; join is bounded."""
        self.stop()
        if self._thread.ident is not None:
            self._thread.join(STOP_SECONDS)
        stopped = not self._thread.is_alive()
        with self._lock:
            attempts = copy.deepcopy(self._attempts)
        # Do not publish a stable inventory/byte count while a hung worker might
        # still write. Such files, if present later, are explicitly unindexed.
        return {"schema_version": 1, **self.config, "namespace": self.namespace,
                "started_at": self._started_at,
                "interval_seconds": self.interval, "snapshot_timeout_seconds": SNAPSHOT_SECONDS,
                "max_attempts": MAX_ATTEMPTS, "max_artifact_bytes": MAX_SERIES_BYTES,
                "worker_stopped": stopped, "stop_reason": self._reason if stopped else "worker_stop_timeout",
                "indexed_bytes": self._bytes if stopped else None, "attempts": attempts}
