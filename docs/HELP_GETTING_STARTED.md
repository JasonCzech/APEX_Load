# Your first test

Follow a small HTTP test from script to report. This walkthrough assumes an existing APEX Load deployment. Use [Choose an installation](../deployments/README.md) for installation and [Concepts & navigation](APPLICATION_USER_GUIDE.md#what-apex-load-does) for terminology.

## Before you start

You need permission to create scripts, scenarios and runs in a project; a healthy execution target; and an HTTP endpoint you own or have permission to test. A shared generator must be able to reach that endpoint. A workstation's localhost address is not the generator's localhost.

Select Domain, Project, Application and Release in the sidebar. Ask your administrator for the correct project if the scope is missing. Production sign-in uses your deployment's identity provider; local developer personas are for non-production use only.

For a self-contained local exercise, run this dependency-free sample server on the same machine as a local APEX Load execution target. It returns a fixed JSON response and does not require credentials or data files:

```python
from http.server import BaseHTTPRequestHandler, HTTPServer

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        payload = b'{"status":"ok"}'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

HTTPServer(("127.0.0.1", 8099), Handler).serve_forever()
```

Save as sample_target.py and run `python3 sample_target.py`. Keep it running until the test completes. For distributed execution, use a reachable approved test service instead and replace the base URL below; do not use this loopback target from remote generators.

## Create and prove the script

Open [Scripts](#/scripts), create a script and use the JSON editor to load the example below. The editor accepts the wrapped form shown here (`{"script": {...}}`, the shape API requests use) or the bare script object. Save it in the selected project. The example sends only one GET per iteration and includes explicit response checks.

```json
{
  "script": {
    "name": "First_HTTP_Test",
    "version": "1.0.0",
    "protocol": "http",
    "config": {
      "base_url": "http://127.0.0.1:8099",
      "pacing": { "type": "fixed_interval", "interval_ms": 1000 }
    },
    "actions": [
      {
        "name": "Check_Service",
        "type": "GET",
        "target": "/",
        "assertions": [
          { "type": "status_code", "expected": 200 },
          { "type": "body_contains", "value": "ok" }
        ]
      }
    ]
  }
}
```

Click Validate and resolve every reported issue. Validation checks the script contract; launch still checks permissions, capacity and deployment capabilities. Replay once and inspect the response. Expect HTTP 200, a body containing `ok`, and successful checks. If replay cannot reach the endpoint, fix connectivity before increasing load. See [Validate & replay](APPLICATION_USER_GUIDE.md#validating-and-replaying).

## Run a small scenario

1. Open [Scenarios](#/tests) and create a scenario with one group using First_HTTP_Test.
2. Set 1 VUser, a 5-second ramp and a 30-second duration. Validate the load model and save.
3. Open [New Test](#/new-test), choose that scenario, confirm the application/release and start. This creates a run and sends traffic to the configured target.
4. Watch the run in [Command Center](#/command-center). Confirm the Check_Service action succeeds and throughput is near one iteration per second after ramp-up. Scheduling and service time can lower the observed rate.
5. When it reaches COMPLETE, open [Results](#/results) and inspect errors, timing and the SLA verdict. COMPLETE describes lifecycle completion; it does not by itself establish an SLA pass.

If a run waits for approval, capacity or shakeout review, use [Troubleshooting](HELP_TROUBLESHOOTING.md). Do not repeatedly start duplicates.

## Clean up and continue

Stop the sample server with Ctrl+C after the run ends. Keep the run as learning evidence or remove sample assets using your project's normal retention and deletion controls. Do not delete a shared scenario or baseline used by someone else.

Next, [model load](APPLICATION_USER_GUIDE.md#building-a-scenario), [add data and secrets](APPLICATION_USER_GUIDE.md#parameterizing-and-data), or [interpret results](APPLICATION_USER_GUIDE.md#reading-results-and-reports). Check [Capabilities & limitations](HELP_CAPABILITIES.md) before choosing a protocol or importing a legacy script.
