#!/usr/bin/env bash
set -euo pipefail

API="${APEXLOAD_API:-http://localhost:8080}"
PROJECT_ID="${PROJECT_ID:-default}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

post_script() {
  local path="$1"
  python3 - "$ROOT/$path" "$PROJECT_ID" "$API" <<'PY'
import json
import sys
import urllib.request

path, project_id, api = sys.argv[1:4]
with open(path, "r", encoding="utf-8") as fh:
    script = json.load(fh)
script["project_id"] = project_id
payload = json.dumps({"project_id": project_id, "script": script}).encode("utf-8")
req = urllib.request.Request(api.rstrip("/") + "/api/v1/scripts", data=payload, headers={"Content-Type": "application/json"}, method="POST")
with urllib.request.urlopen(req, timeout=30) as resp:
    body = json.loads(resp.read().decode("utf-8"))
script_id = body.get("id") or body.get("script_id")
print(script_id)
print(f"seeded script: {body.get('name', path)} ({script_id})", file=sys.stderr)
PY
}

post_scenario_template() {
  local path="$1"
  shift
  python3 - "$ROOT/$path" "$PROJECT_ID" "$API" "$@" <<'PY'
import json
import sys
import urllib.request

path, project_id, api = sys.argv[1:4]
script_ids = {}
for pair in sys.argv[4:]:
    key, value = pair.split("=", 1)
    script_ids[key] = value

def post_json(endpoint, payload):
    req = urllib.request.Request(
        api.rstrip("/") + endpoint,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read().decode("utf-8"))

with open(path, "r", encoding="utf-8") as fh:
    template = json.load(fh)

groups = []
for group in template.get("groups", []):
    script_id = ""
    script_path = group.get("script_path")
    if script_path:
        script_id = script_ids.get(script_path, "")
    elif group.get("script"):
        inline_script = group["script"]
        inline_script["project_id"] = project_id
        created = post_json("/api/v1/scripts", {"project_id": project_id, "script": inline_script})
        script_id = created.get("id") or created.get("script_id")
        print(f"seeded script: {created.get('name', inline_script.get('name', 'inline'))} ({script_id})", file=sys.stderr)
    if not script_id:
        raise SystemExit(f"scenario group {group.get('name', '<unnamed>')} has no seeded script id")
    groups.append({
        "name": group.get("name", "Evaluator Group"),
        "script_id": script_id,
        "vusers": group.get("vusers", 1),
        "ramp_up": group.get("ramp_up", ""),
        "ramp_down": group.get("ramp_down", ""),
        "stages": group.get("stages", []),
        "iterations": group.get("iterations"),
        "runtime_settings": group.get("runtime_settings"),
        "initialization": group.get("initialization", "at_runtime"),
    })

payload = {
    "name": template.get("name", "Evaluator Scenario"),
    "duration": template.get("duration", "10m"),
    "scenario_mode": template.get("scenario_mode", "vuser_driven"),
    "script_shakeout": template.get("script_shakeout", False),
    "resource_baseline": template.get("resource_baseline", False),
    "assignment": template.get("assignment"),
    "goal_config": template.get("goal_config"),
    "runtime_settings": template.get("runtime_settings"),
    "groups": groups,
}
created = post_json("/api/v1/scenarios", payload)
print(f"seeded scenario: {created.get('name', payload['name'])} ({created.get('id')})", file=sys.stderr)
PY
}

main() {
  echo "Seeding APEX Load evaluator demo pack into $API (project: $PROJECT_ID)"
  local http_id
  local websocket_id
  http_id="$(post_script "examples/web-api/ecommerce-http.json")"
  post_script "examples/web-api/soap-account-service.json" >/dev/null
  post_script "examples/web-api/grpc-inventory.json" >/dev/null
  websocket_id="$(post_script "examples/web-api/websocket-order-feed.json")"
  post_script "examples/web-api/jms-order-flow.json" >/dev/null
  post_scenario_template "examples/web-api/browser-canary-plus-protocol-load.json" \
    "examples/web-api/ecommerce-http.json=$http_id" \
    "examples/web-api/websocket-order-feed.json=$websocket_id"
  echo "Evaluator demo seed complete."
}

main "$@"
