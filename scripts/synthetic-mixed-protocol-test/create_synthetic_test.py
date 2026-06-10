#!/usr/bin/env python3

import json
import os
import urllib.error
import urllib.request


API = os.getenv("APEXLOAD_API_URL", "http://127.0.0.1:8080").rstrip("/")
BASE = os.getenv("SYNTHETIC_DUMMY_BASE_URL", "http://127.0.0.1:18080")
HEADERS = {"Content-Type": "application/json"}
token = os.getenv("APEXLOAD_API_KEY")
if token:
    HEADERS["Authorization"] = f"Bearer {token}"


def _error_response(message, exc):
    print(message)
    if hasattr(exc, "read"):
        try:
            print(exc.read().decode())
        except Exception:
            pass
    raise SystemExit(1)


def request(path, payload=None, method="POST"):
    data = None
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(API + path, data=data, method=method, headers=HEADERS)
    try:
        with urllib.request.urlopen(req, timeout=120) as response:
            body = response.read().decode("utf-8")
            if not body:
                return {}
            return json.loads(body)
    except urllib.error.HTTPError as exc:
        _error_response(f"{path} returned {exc.code}", exc)


def create_script(script_payload):
    return request(
        "/api/v1/scripts",
        {
            "project_id": "unassigned",
            "script": script_payload,
        },
    )


def main():
    # HTTP user group script
    http_script = {
        "name": "Synthetic HTTP Users",
        "version": "1.0.0",
        "protocol": "http",
        "config": {
            "base_url": BASE,
            "think_time": {"type": "fixed", "mean_ms": 500},
        },
        "actions": [
            {
                "name": "Health",
                "type": "GET",
                "target": "/api/health",
            },
            {
                "name": "SubmitOrder",
                "type": "POST",
                "target": "/api/orders",
                "headers": {"Content-Type": "application/json"},
                "body": '{"item":"demo","qty":1}',
            },
            {
                "name": "ListOrders",
                "type": "GET",
                "target": "/api/orders",
            },
        ],
    }
    http_created = create_script(http_script)
    http_id = http_created["id"]
    print(f"HTTP script created: {http_id}")

    # Browser/HTML user group script
    browser_script = {
        "name": "Synthetic Browser Users",
        "version": "1.0.0",
        "protocol": "browser",
        "config": {
            "base_url": BASE,
            "browser": {"headless": True},
            "think_time": {"type": "fixed", "mean_ms": 600},
        },
        "actions": [
            {"name": "OpenHome", "type": "browser_navigate", "target": "/"},
            {
                "name": "WaitForLink",
                "type": "browser_wait",
                "wait_for": {
                    "type": "selector",
                    "value": "#next-link",
                    "timeout_ms": 5000,
                },
            },
            {"name": "ClickLink", "type": "browser_click", "selector": "#next-link"},
            {
                "name": "WaitForNext",
                "type": "browser_wait",
                "wait_for": {
                    "type": "url_contains",
                    "value": "/next",
                    "timeout_ms": 5000,
                },
            },
        ],
    }
    browser_created = create_script(browser_script)
    browser_id = browser_created["id"]
    print(f"Browser script created: {browser_id}")

    # DevWeb import + save
    devweb_source = """
load.action("DevWeb create order", async function() {
  const req = new load.WebRequest({
    url: "%s/api/orders",
    method: "POST",
    headers: {"Content-Type":"application/json"},
    body: '{"item":"devweb","qty":2}'
  });
  req.send();
});
""" % BASE
    imported = request(
        "/api/v1/scripts/import/devweb",
        {"name": "Synthetic DevWeb", "content": devweb_source},
    )
    if "script" not in imported:
        raise SystemExit("import/devweb response did not include script")
    devweb_saved = request(
        "/api/v1/scripts",
        {
            "project_id": "unassigned",
            "script": imported["script"],
        },
    )
    devweb_id = devweb_saved["id"]
    print(f"DevWeb script created: {devweb_id}")

    created_test = request(
        "/api/v1/tests",
        {
            "name": "45m Synthetic Mixed Protocol Test",
            "duration": "45m",
            "groups": [
                {"name": "http-users", "script_id": http_id, "vusers": 20, "ramp_up": "30s"},
                {"name": "html-users", "script_id": browser_id, "vusers": 2, "ramp_up": "20s"},
                {"name": "devweb-users", "script_id": devweb_id, "vusers": 10, "ramp_up": "20s"},
            ],
        },
    )
    test_id = created_test["id"]
    print(f"Test created: {test_id}")

    request(f"/api/v1/tests/{test_id}/start", {})
    print(f"Test started: {test_id}")
    print(f"Watch status:  {API}/api/v1/tests/{test_id}")
    print(f"Check metrics:  {API}/api/v1/tests/{test_id}/metrics")
    print(f"Check errors:   {API}/api/v1/tests/{test_id}/errors/evidence?signature=dev")


if __name__ == "__main__":
    main()
