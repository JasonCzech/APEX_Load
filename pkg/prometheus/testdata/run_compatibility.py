#!/usr/bin/env python3
"""Qualify the SDK against owned disposable upstream servers; never contact a SUT."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.request
import uuid

IMAGES = {
 "2.55.1": "prom/prometheus:v2.55.1@sha256:2659f4c2ebb718e7695cb9b25ffa7d6be64db013daba13e05c875451cf51b0d3",
 "3.13.0": "prom/prometheus:v3.13.0@sha256:c6b27ea434f8389bfe233fbc7be381cf50587c286e871bc842008f5a1b1908a7",
}
root = Path(__file__).resolve().parents[3]
names = []
try:
 with tempfile.TemporaryDirectory(prefix="apex-prometheus-compat-", dir="/private/tmp") as temporary:
  fixture = Path(temporary)
  fixture.chmod(0o755)
  (fixture / "prometheus.yml").write_text("""global:
  scrape_interval: 5s
  scrape_timeout: 5s
  evaluation_interval: 1s
rule_files: ["/etc/prometheus/fixture-rules.yml"]
scrape_configs:
  - job_name: sut
    static_configs:
      - targets: ["localhost:9090"]
        labels:
          application: fixture
""")
  (fixture / "fixture-rules.yml").write_text("""groups:
  - name: fixture
    rules:
      - alert: FixtureSUTUp
        expr: up{job="sut"} == 1
        labels:
          severity: warning
          job: sut
          application: fixture
""")
  endpoints = {}
  for version, image in IMAGES.items():
   name = "apex-prometheus-compat-" + uuid.uuid4().hex[:12]
   subprocess.run(["docker", "pull", image], check=True)
   subprocess.run(["docker", "run", "--rm", "--detach", "--name", name, "--env", "GOMAXPROCS=2", "--publish", "127.0.0.1::9090", "--mount", f"type=bind,src={fixture},dst=/etc/prometheus,readonly", image, "--config.file=/etc/prometheus/prometheus.yml"], check=True)
   names.append(name)
   port = subprocess.check_output(["docker", "port", name, "9090/tcp"], text=True).strip().split(":")[-1]
   endpoint = f"http://127.0.0.1:{port}"
   deadline = time.monotonic() + 30
   while True:
    try:
     with urllib.request.urlopen(endpoint + "/-/ready", timeout=1) as response:
      if response.status == 200:
       break
    except Exception:
     if time.monotonic() > deadline:
      raise RuntimeError("owned Prometheus fixture did not become ready")
     time.sleep(0.25)
   endpoints[version] = endpoint
  time.sleep(8)
  environment = os.environ.copy()
  environment["APEX_PROMETHEUS_FIXTURE_ENDPOINTS"] = json.dumps(endpoints)
  environment.setdefault("GOCACHE", "/private/tmp/apex-prometheus-go-cache")
  environment.setdefault("GOGC", "75")
  environment.setdefault("GOMAXPROCS", "4")
  environment.setdefault("GOFLAGS", "-p=1")
  subprocess.run(["go", "test", "./pkg/prometheus", "-run", "^TestPrometheusRealCompatibilityFixture$", "-count=1", "-v"], cwd=root, env=environment, check=True)
finally:
 for name in names:
  subprocess.run(["docker", "logs", "--tail", "40", name], check=False)
  subprocess.run(["docker", "rm", "--force", name], check=False)
