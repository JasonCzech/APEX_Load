#!/usr/bin/env python3
"""Run the pinned Kafka/Solace fixtures without GitHub Actions or cloud secrets.

Requires Docker Compose and Go. Owns a unique Compose project; logs and an
execution receipt survive teardown. Ports must be free before fixture startup.
"""

from __future__ import annotations

import argparse
import base64
from datetime import datetime, timezone
import json
from pathlib import Path
import socket
import signal
import subprocess
import time
import urllib.error
import urllib.request
import uuid
import os

ROOT = Path(__file__).resolve().parent.parent
COMPOSE = ROOT / "docs/qa/reviews/import-fixture-matrix-2026-09-12/brokers.compose.yaml"
PROFILES = {
    "kafka": ("kafka_real,kafka_integration", "./pkg/protocol/kafka", "TestKafkaFranzProduceConsumeIntegration"),
    "solace": ("jms_solace_real,jms_solace_integration", "./pkg/protocol/jms", "TestSolaceAMQPIntegrationPublish"),
}


def kafka_fixture_override() -> dict:
    # Host clients and the container's topic administrator need independently
    # reachable advertised addresses. The internal listener is not published.
    return {"services": {"kafka": {"environment": {
        "KAFKA_LISTENERS": "PLAINTEXT://:9092,CONTROLLER://:9093,INTERNAL://:9094",
        "KAFKA_ADVERTISED_LISTENERS": "PLAINTEXT://127.0.0.1:19092,INTERNAL://127.0.0.1:9094",
        "KAFKA_LISTENER_SECURITY_PROTOCOL_MAP": "CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT,INTERNAL:PLAINTEXT",
        "KAFKA_INTER_BROKER_LISTENER_NAME": "INTERNAL",
    }}}}


def live_test_passed(path: Path, expected: str) -> bool:
    """A successful process with a skipped or absent live test is not evidence."""
    events = []
    for line in path.read_text().splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue  # compiler diagnostics share the log
        if isinstance(event, dict):
            events.append(event)
    return any(e.get("Action") == "pass" and e.get("Test") == expected for e in events) and not any(
        e.get("Action") == "fail" for e in events
    )

def require_free_ports(profiles: list[str]) -> None:
    for port in ([19092] if "kafka" in profiles else []) + ([18090, 15672] if "solace" in profiles else []):
        with socket.socket() as listener:
            listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            try:
                listener.bind(("127.0.0.1", port))
            except OSError as exc:
                raise RuntimeError(f"Port {port} is in use; stop its owner before running disposable broker fixtures.") from exc


def semp(method: str, path: str, payload: dict | None = None) -> dict:
    headers = {"Authorization": "Basic " + base64.b64encode(b"admin:fixture-admin").decode()}
    body = None
    if payload is not None:
        headers["Content-Type"] = "application/json"
        body = json.dumps(payload).encode()
    request = urllib.request.Request("http://127.0.0.1:18090/SEMP/v2/config/" + path,
                                     data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        if method == "GET":
            raise  # startup probes retry until the broker API is ready
        detail = error.read(4096).decode("utf-8", errors="replace")
        raise RuntimeError(f"Solace {method} {path} returned {error.code}: {detail}") from error


def wait_until_ready(check, description: str, timeout: int = 180) -> None:
    deadline = time.monotonic() + timeout
    while True:
        try:
            if check():
                return
        except (OSError, urllib.error.URLError, subprocess.SubprocessError):
            pass
        if time.monotonic() >= deadline:
            raise RuntimeError(f"Timed out waiting for {description}; inspect fixture.log.")
        time.sleep(2)


def run(output: Path, profiles: list[str]) -> int:
    if not profiles or any(profile not in PROFILES for profile in profiles):
        raise ValueError("At least one supported broker profile is required")
    require_free_ports(profiles)
    output.mkdir(parents=True, exist_ok=False)
    project = "apexload-live-" + uuid.uuid4().hex[:12]
    compose = ["docker", "compose", "--project-name", project, "--file", str(COMPOSE)]
    if "kafka" in profiles:
        override = output / "fixture.override.json"
        override.write_text(json.dumps(kafka_fixture_override(), indent=2) + "\n")
        compose.extend(["--file", str(override)])
    receipt = {"schema_version": 1, "started_at": datetime.now(timezone.utc).isoformat(),
               "commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
               "dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True).strip()),
               "fixture": str(COMPOSE.relative_to(ROOT)), "profiles": {}, "status": "failed"}
    env = dict(os.environ, GOMAXPROCS=os.environ.get("GOMAXPROCS", "2"))
    env.update(KAFKA_BROKERS="127.0.0.1:19092", KAFKA_TOPIC="apexload.integration",
               SOLACE_AMQP_URL="amqp://127.0.0.1:15672", SOLACE_QUEUE="apexload.integration",
               SOLACE_USERNAME="default", SOLACE_PASSWORD="fixture-client", SOLACE_VPN="default")
    with (output / "fixture.log").open("w") as log:
        try:
            subprocess.run([*compose, "up", "--detach", *profiles], cwd=ROOT, check=True, stdout=log, stderr=log)
            if "kafka" in profiles:
                command = [*compose, "exec", "-T", "kafka", "/opt/kafka/bin/kafka-topics.sh",
                           "--bootstrap-server", "127.0.0.1:9094"]
                wait_until_ready(lambda: subprocess.run([*command, "--list"], stdout=log, stderr=log,
                                                       timeout=15, check=False).returncode == 0, "Kafka")
                # The real adapter deliberately does not create topics. Create
                # the disposable fixture before the host-side produce test.
                subprocess.run([*command, "--create", "--if-not-exists", "--topic", env["KAFKA_TOPIC"],
                                "--partitions", "1", "--replication-factor", "1"],
                               stdout=log, stderr=log, timeout=30, check=True)
            if "solace" in profiles:
                wait_until_ready(lambda: semp("GET", "msgVpns/default").get("data", {}).get("enabled"), "Solace")
                semp("PATCH", "msgVpns/default", {"authenticationBasicType": "internal", "serviceAmqpPlainTextEnabled": True})
                semp("PATCH", "msgVpns/default/clientUsernames/default", {"enabled": True, "password": env["SOLACE_PASSWORD"]})
                semp("PATCH", "msgVpns/default/clientProfiles/default", {"allowGuaranteedMsgSendEnabled": True, "allowGuaranteedMsgReceiveEnabled": True})
                wait_until_ready(lambda: bool(semp("GET", "msgVpns/default/queues")), "Solace message spool")
                semp("POST", "msgVpns/default/queues", {"queueName": env["SOLACE_QUEUE"], "accessType": "exclusive", "permission": "consume", "ingressEnabled": True, "egressEnabled": True})
            for profile in profiles:
                tags, package, expected = PROFILES[profile]
                test_log = output / f"{profile}.jsonl"
                command = ["go", "test", "-json", "-p", "2", "-count=1", "-timeout=20m", "-tags", tags, package]
                print(f"Running {profile}; evidence: {test_log}", flush=True)
                with test_log.open("w") as stream:
                    result = subprocess.run(command, cwd=ROOT, env=env, stdout=stream, stderr=stream, timeout=25 * 60)
                passed = result.returncode == 0 and live_test_passed(test_log, expected)
                receipt["profiles"][profile] = {"status": "passed" if passed else "failed", "required_test": expected, "log": test_log.name}
            receipt["status"] = "passed" if all(p["status"] == "passed" for p in receipt["profiles"].values()) else "failed"
        except (OSError, RuntimeError, subprocess.SubprocessError, KeyboardInterrupt) as error:
            receipt["error"] = str(error)
            print(f"Broker verification failed: {error}", flush=True)
        finally:
            try:
                subprocess.run([*compose, "logs", "--no-color"], cwd=ROOT, stdout=log, stderr=log, timeout=30, check=False)
            except (OSError, subprocess.SubprocessError) as error:
                receipt["log_collection_error"] = str(error)
            try:
                cleanup = subprocess.run([*compose, "down", "--timeout", "10"], cwd=ROOT, stdout=log, stderr=log, timeout=60, check=False)
                receipt["cleanup_status"] = "passed" if cleanup.returncode == 0 else "failed"
            except (OSError, subprocess.SubprocessError) as error:
                receipt["cleanup_status"] = "failed"
                receipt["cleanup_error"] = str(error)
            if receipt["cleanup_status"] != "passed":
                receipt["status"] = "failed"
            receipt["completed_at"] = datetime.now(timezone.utc).isoformat()
            (output / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")
    return 0 if receipt["status"] == "passed" else 1


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True, help="new evidence directory (must not exist)")
    parser.add_argument("--profiles", nargs="+", choices=PROFILES, default=list(PROFILES))
    args = parser.parse_args()
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt("broker verification interrupted")
    signal.signal(signal.SIGTERM, interrupted)
    try:
        return run(args.output.resolve(), list(dict.fromkeys(args.profiles)))
    except (OSError, RuntimeError) as error:
        print(f"Cannot start broker verification: {error}", flush=True)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
