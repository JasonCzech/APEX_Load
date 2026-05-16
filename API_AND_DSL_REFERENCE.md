# API and DSL Reference

## REST API Endpoints

### Authentication

Currently, Stormrunner uses project-based scoping without user authentication. APIs are protected by project ID.

### Tests

#### Create and queue a test

```
POST /api/v1/tests
Content-Type: application/json

{
  "name": "Homepage Load Test",
  "project_id": "proj-123",
**Response:**
```json
{
  "status": "started",
  "test_id": "test-789"
}
```
  "vusers": 100,
  "duration_seconds": 300,
  "ramp_up_seconds": 30,
  "notes": "Peak hour simulation"
}
```

Allows VUsers to complete the current action and cleanup while transitioning the run into `STOPPING`.

**Response:**
```json
{
  "status": "stopping",
  "test_id": "test-789"
}
```
```json
{
  "id": "test-789",
  "name": "Homepage Load Test",
  "script_id": "script-456",
  "status": "queued",
  "vusers": 100,
Terminates the run through the same immediate-stop control path exposed by the current controller.

**Response:**
```json
{
  "status": "aborted",
  "test_id": "test-789"
}
```

#### Pause a running test

```
POST /api/v1/tests/test-789/pause
```

Pauses execution between actions without tearing down the active run.

**Response:**
```json
{
  "status": "paused",
  "test_id": "test-789"
}
```

#### Resume a paused test

```
POST /api/v1/tests/test-789/resume
```

Resumes a run that is currently in the `PAUSED` state.

**Response:**
```json
{
  "status": "resumed",
  "test_id": "test-789"
}
```

#### Scale VUsers at runtime

```
PATCH /api/v1/tests/test-789/vusers
```

**Request:**
```json
{
  "group": "buyers",
  "target_vusers": 120
}
```

**Response:**
```json
{
  "test_id": "test-789",
  "group": "buyers",
  "target_vusers": 120,
  "status": "scaled"
}
```

#### Adjust duration at runtime

```
PATCH /api/v1/tests/test-789/duration
```

**Request:**
```json
{
  "duration": "10m"
}
```

**Response:**
```json
{
  "test_id": "test-789",
  "new_duration": "10m",
  "status": "adjusted"
}
```
  "ramp_up_seconds": 30,
  "created_at": "2026-05-11T10:30:00Z"
}
```

#### List all tests

**Response:**
```json
{
  "test_id": "test-789",
  "status": "RUNNING",
  "metrics": {
    "timestamp": "2026-05-11T10:31:45Z",
    "total_requests": 4250,
    "total_errors": 12,
    "tps": 94.4,
    "avg_response_ms": 156.3,
    "p95_ms": 482.5,
    "p99_ms": 892.1
  },
  "history": [
    {
      "timestamp": "2026-05-11T10:31:40Z",
      "tps": 92.1,
      "avg_response_ms": 149.7
    }
  ]
}
```
  }
]
```
- `test_started`: Run start transition
- `test_stopping`: Graceful stop requested
- `test_paused`: Run paused between actions
- `test_resumed`: Run resumed from pause
- `vusers_changed`: Runtime group VUser scaling applied
- `duration_changed`: Runtime duration change applied

#### Get test details and status

```
GET /api/v1/tests/test-789
```

**Response:**
```json
{
  "id": "test-789",
  "name": "Homepage Load Test",
  "status": "running",
  "vusers": 100,
  "duration_seconds": 300,
  "ramp_up_seconds": 30,
  "started_at": "2026-05-11T10:31:00Z",
  "current_vusers": 85,
  "elapsed_seconds": 45,
  "metrics": {
    "total_transactions": 4250,
    "total_errors": 12,
    "throughput_per_sec": 94.4,
    "avg_latency_ms": 156.3,
    "p95_latency_ms": 482.5,
    "p99_latency_ms": 892.1
  }
}
```

#### Start a queued test

```
POST /api/v1/tests/test-789/start
```

**Response:** `204 No Content`

#### Stop test gracefully

```
POST /api/v1/tests/test-789/stop
```

Allows VUsers to complete current action and cleanup; respects teardown phase. Response: `204 No Content`.

#### Abort test immediately

```
POST /api/v1/tests/test-789/abort
```

Terminates all VUsers immediately without teardown. Response: `204 No Content`.

#### Get current metrics snapshot

```
GET /api/v1/tests/test-789/metrics
```

**Response:**
```json
{
  "timestamp": "2026-05-11T10:31:45Z",
  "transactions": 4250,
  "errors": 12,
  "throughput_per_sec": 94.4,
  "latency_ms": {
    "min": 23.5,
    "max": 1205.3,
    "avg": 156.3,
    "p50": 142.0,
    "p95": 482.5,
    "p99": 892.1
  },
  "status_codes": {
    "200": 4200,
    "500": 12,
    "404": 38
  }
}
```

#### WebSocket stream (test-specific)

```
WS /api/v1/tests/test-789/stream
```

## Browser and Mobile DSL

Stormrunner keeps mobile emulation under the `browser` protocol rather than introducing a separate mobile protocol. Use browser config to apply named device presets, touch behavior, orientation, and client-side UX telemetry.

### Browser Config

```json
{
  "protocol": "browser",
  "config": {
    "base_url": "https://www.example.com",
    "browser": {
      "engine": "webkit",
      "device_profile": "ipad_air",
      "touch_mode": true,
      "orientation": "landscape",
      "client_metrics": true,
      "user_agent": "Mozilla/5.0 (...) Safari/601.1",
      "viewport": {
        "width": 1180,
        "height": 820
      }
    }
  }
}
```

Supported browser config keys:
- `config.browser.engine`: `chromium` or `webkit` (`webkit` executes through Playwright WebKit and requires Playwright WebKit assets installed on the controller host)
- `config.browser.device_profile`: named preset from the built-in device registry such as `pixel_8`, `iphone_15_pro`, or `ipad_air`
- `config.browser.touch_mode`: optional boolean override for touch emulation
- `config.browser.orientation`: `portrait` or `landscape`
- `config.browser.client_metrics`: enables browser UX metrics collection
- `config.browser.user_agent`: custom UA override layered on top of the chosen device profile
- `config.browser.viewport`: explicit viewport override

### Supported Browser Actions

Stormrunner validates and executes these browser action types:
- `browser_navigate`
- `browser_click`
- `browser_fill`
- `browser_wait`
- `browser_screenshot`
- `browser_tap`
- `browser_long_press`
- `browser_swipe`

Gesture action parameters:
- `browser_long_press.params.duration_ms`: positive integer hold time
- `browser_swipe.params.direction`: `left`, `right`, `up`, or `down`
- `browser_swipe.params.distance`: positive integer swipe distance in pixels

### Browser Metrics In Live And Archived Results

When `config.browser.client_metrics` is enabled, browser actions can populate `client_metrics` on archived transaction events and browser summary blocks on live and archived metrics payloads.

Summary metrics fields:
- `avg_fcp_ms`
- `avg_lcp_ms`
- `avg_cls_score`
- `avg_inp_ms`
- `avg_blocking_resources`
- `avg_long_task_count`
- `avg_js_heap_used_mb`

Response surfaces carrying these summaries:
- live test details and `/api/v1/tests/{id}/metrics` via `browser` and `browser_by_device`
- interval summary history records via `browser` and `browser_by_device`
- archive reports via `overview.browser` and `browser_by_device`
- archived transaction events via `client_metrics`

Example archive report excerpt:

```json
{
  "overview": {
    "transactions": 1280,
    "avg_response_ms": 142.6,
    "browser": {
      "samples": 1280,
      "avg_fcp_ms": 811.4,
      "avg_lcp_ms": 1420.8,
      "avg_cls_score": 0.031,
      "avg_inp_ms": 88.7,
      "avg_long_task_count": 1.4
    }
  },
  "browser_by_device": {
    "iphone_15_pro": {
      "samples": 640,
      "device_profile": "iphone_15_pro",
      "device_tier": "high",
      "avg_lcp_ms": 1488.2,
      "avg_inp_ms": 92.1
    },
    "ipad_air": {
      "samples": 640,
      "device_profile": "ipad_air",
      "device_tier": "mid",
      "avg_lcp_ms": 1353.5,
      "avg_inp_ms": 79.4
    }
  }
}
```

Emits JSON messages:
- `metrics_update`: Periodic snapshot (every 1-5 seconds)
- `test_complete`: Test finished successfully
- `test_failed`: Test encountered error
- `test_stopping`: Graceful stop initiated

**Example message:**
```json
{
  "type": "metrics_update",
  "timestamp": "2026-05-11T10:31:45Z",
  "test_id": "test-789",
  "metrics": {
    "transactions": 4250,
    "errors": 12,
    "throughput": 94.4,
    "latency_p95": 482.5
  }
}
```

### Scenario Schedules

Scenario schedules bind a reusable scenario to a five-field cron expression. The controller evaluates these schedules on its background loop and creates queued runs through the same scenario execution path used by manual launches.

#### List schedules for a scenario

```
GET /api/v1/scenarios/scenario-123/schedules
```

**Response:**
```json
{
  "schedules": [
    {
      "id": "schedule-456",
      "scenario_id": "scenario-123",
      "name": "Weekday smoke",
      "cron_expr": "0 9 * * 1-5",
      "release_id": "release-22",
      "run_name_template": "Checkout Smoke",
      "enabled": true,
      "next_run_at": "2026-05-18T09:00:00Z",
      "last_run_at": "2026-05-17T09:00:12Z",
      "created_at": "2026-05-12T10:00:00Z",
      "updated_at": "2026-05-17T09:00:12Z"
    }
  ],
  "count": 1
}
```

#### Create or update a schedule

```
POST /api/v1/scenarios/scenario-123/schedules
PUT /api/v1/scenarios/scenario-123/schedules/schedule-456
Content-Type: application/json

{
  "name": "Weekday smoke",
  "cron_expr": "0 9 * * 1-5",
  "release_id": "release-22",
  "run_name_template": "Checkout Smoke",
  "enabled": true
}
```

The `PUT` variant updates the targeted schedule in place. Setting `enabled` to `false` keeps the schedule persisted while preventing future launches.

#### Delete a schedule

```
DELETE /api/v1/scenarios/scenario-123/schedules/schedule-456
```

### Scripts

#### Create/upload script

```
POST /api/v1/scripts
Content-Type: application/json

{
  "name": "REST API Performance",
  "project_id": "proj-123",
  "folder_id": "folder-456",
  "protocol": "http",
  "content": {
    "config": { ... },
    "data_sources": [ ... ],
    "init": [ ... ],
    "actions": [ ... ],
    "teardown": [ ... ]
  }
}
```

#### List all scripts

```
GET /api/v1/scripts?project_id=proj-123
```

#### Get script details

```
GET /api/v1/scripts/script-456
```

#### Update script

```
PUT /api/v1/scripts/script-456
Content-Type: application/json

{
  "name": "REST API Performance v2",
  "content": { ... }
}
```

#### Delete script

```
DELETE /api/v1/scripts/script-456
```

#### Validate script

```
POST /api/v1/scripts/script-456/validate
```

Dry-run parse and check syntax. Response includes any errors found.

#### Export script as JSON file

```
GET /api/v1/scripts/script-456/download
```

Returns raw JSON with `Content-Disposition: attachment` header.

#### Convert LoadRunner .zip to DSL

```
POST /api/v1/scripts/import/loadrunner
Content-Type: multipart/form-data

[file: loadrunner_project.zip]
```

**Response:**
```json
{
  "script_id": "script-999",
  "name": "Imported LoadRunner",
  "conversion_warnings": [
    "VB script blocks converted to comments",
    "Custom extractors simplified to JSON path extraction"
  ]
}
```

#### Build a script from sample traffic

```
POST /api/v1/scripts/import/sample
Content-Type: application/json

{
  "name": "Sample Import",
  "format": "raw_http",
  "content": "POST /java/session HTTP/1.1\nHost: api.example.com\nContent-Type: application/x-java-serialized-object\n\nrO0ABXQABGRlbW8=",
  "response_content": "HTTP/1.1 200 OK\nContent-Type: application/x-java-serialized-object\n\nrO0ABXQACnNlc3Npb24=",
  "options": {
    "base_url": "https://api.example.com",
    "include_assertions": true,
    "infer_extractors": true,
    "sanitize_sensitive": true
  }
}
```

**Notes:**
- `format` may be `raw_http`, `curl`, `rest_json`, or `har`.
- HAR imports preserve inter-request pauses as `think_time` blocks when `startedDateTime` metadata is present on consecutive entries.
- SOAP-oriented samples can be promoted onto the `soap` protocol surface when the import set is consistently SOAP-shaped.
- Java-over-HTTP imports are intentionally narrower: use raw HTTP or cURL samples whose opaque request bodies are already base64-encoded serialized Java bytes. The dashboard then promotes the reviewed draft to `protocol: "java_http"`.

### Projects and Folders

#### List projects

```
GET /api/v1/projects
```

#### Create project

```
POST /api/v1/projects

{
  "name": "E-commerce Platform",
  "description": "Load tests for checkout and payment flows"
}
```

#### List folders in project

```
GET /api/v1/projects/proj-123/folders
```

**Response:**
```json
[
  {
    "id": "folder-456",
    "name": "Checkout Tests",
    "parent_id": null,
    "created_at": "2026-05-10T14:20:00Z"
  },
  {
    "id": "folder-789",
    "name": "Payment Gateway",
    "parent_id": "folder-456",
    "created_at": "2026-05-10T15:10:00Z"
  }
]
```

#### Create folder

```
POST /api/v1/projects/proj-123/folders

{
  "name": "API Tests",
  "parent_id": null
}
```

#### Rename folder

```
PUT /api/v1/projects/proj-123/folders/folder-456

{
  "name": "Checkout Tests - Updated"
}
```

#### Delete folder

```
DELETE /api/v1/projects/proj-123/folders/folder-456
```

### Recorder

#### Get recorder status

```
GET /api/v1/recorder/status
```

**Response:**
```json
{
  "recording": false,
  "start_time": null,
  "request_count": 0,
  "proxy_port": 8081
}
```

#### Start HTTP recording

```
POST /api/v1/recorder/start

{
  "ignore_patterns": ["*.jpg", "*.png", "*.css"],
  "capture_headers": true
}
```

#### Stop recording and save HAR

```
POST /api/v1/recorder/stop
```

**Response:**
```json
{
  "har_id": "har-001",
  "request_count": 47,
  "duration_seconds": 123
}
```

#### Download recorded HAR file

```
GET /api/v1/recorder/har?har_id=har-001
```

Returns HAR file with `Content-Type: application/json`.

### System

#### Health check

```
GET /api/v1/system/health
```

**Response:**
```json
{
  "status": "healthy",
  "uptime_seconds": 3600,
  "db": "connected",
  "version": "1.0.0"
}
```

#### Global WebSocket stream

```
WS /api/v1/ws
```

Emits JSON messages for all test events:
- `test_created`: Test added to queue
- `test_started`: Test moved to running
- `test_complete`: Test finished successfully
- `test_failed`: Test encountered error
- `metrics_update`: Periodic metrics (optional filter by test_id)

**Example message:**
```json
{
  "type": "test_created",
  "test_id": "test-789",
  "name": "Homepage Load Test",
  "timestamp": "2026-05-11T10:30:00Z"
}
```

---

## DSL Reference

### JSON Script Structure

```json
{
  "name": "Optional descriptive name",
  "config": { ... },
  "data_sources": [ ... ],
  "init": [ ... ],
  "actions": [ ... ],
  "teardown": [ ... ]
}
```

### Config Section

```json
{
  "config": {
    "protocol": "http",
    "base_url": "https://api.example.com",
    "timeout_seconds": 30,
    "follow_redirects": true,
    "ssl_verify": true,
    "headers": {
      "User-Agent": "Stormrunner/1.0",
      "Accept": "application/json"
    }
  }
}
```

**Common config keys:**
- `protocol`: `http`, `soap`, `java_http`, `browser`, `jms-solace`
- `base_url`: Base URL for relative paths (HTTP)
- `timeout_seconds`: Request timeout
- `follow_redirects`: Auto-follow HTTP redirects
- `ssl_verify`: Validate SSL certificates
- `headers`: Default headers for all actions
- `connection_pool_size`: Connection reuse pool size (default: 10)

**Java-over-HTTP config keys:**
- `config.java_http.codec`: Currently `java_serialized_object_base64`
- `config.java_http.helper_mode`: Currently `embedded`
- `config.java_http.content_type`: Defaults to `application/x-java-serialized-object`
- `config.java_http.accept`: Defaults to `application/x-java-serialized-object`
- `config.java_http.body_encoding`: Currently `base64`
- `config.java_http.object_schema`: Optional application-level schema or class hint
- `config.java_http.extractor_type`: Defaults to `opaque`
- `config.java_http.preserve_binary_result`: When `true`, response bodies stay base64-encoded in replay output

**SOAP config keys:**
- `config.soap.version`: `1.1` or `1.2`
- `config.soap.namespaces`: Prefix-to-URI map used for generated envelopes and namespace-aware SOAP authoring
- `config.soap.ws_security.username_token.username`: WS-Security UsernameToken username
- `config.soap.ws_security.username_token.password`: WS-Security UsernameToken password
- `config.soap.ws_security.username_token.password_type`: `text` or `digest`
- `config.soap.ws_security.username_token.nonce`: Include a nonce when `true`
- `config.soap.ws_security.username_token.created`: Include a created timestamp when `true`
- `config.soap.ws_security.timestamp.ttl_seconds`: Optional WS-Security Timestamp TTL in seconds
- `config.soap.fault_handling.fail_on_fault`: When `true`, SOAP Fault envelopes fail the action even if the HTTP status is 200; fault responses also populate action snapshots for `snapshot_on_error` workflows

**JMS Solace config keys:**
- `config.jms.broker_url`: Solace broker URL, for example `tcps://broker.example.com:55443`
- `config.jms.message_vpn`: Solace message VPN
- `config.jms.destination`: Default queue/topic destination
- `config.jms.destination_type`: `queue` or `topic`
- `config.jms.reply_to`: Default reply destination for request/reply
- `config.jms.timeout_ms`: Default consume/request timeout
- `config.jms.mock`: Use the local in-memory broker for replay. Real Solace broker execution requires the Solace adapter build.

**Browser config keys:**
- `config.browser.engine`: Browser engine identifier: `chromium` or `webkit` (`webkit` requires Playwright WebKit assets on the controller host)
- `config.browser.headless`: Run the browser headlessly when `true`
- `config.browser.slow_mo_ms`: Add fixed browser-action delay in milliseconds
- `config.browser.user_agent`: Override browser user agent string
- `config.browser.capture_screenshots`: Screenshot capture policy, for example `per_action`
- `config.browser.cache_enabled`: Enable browser cache when `true`
- `config.browser.cache_content_urls`: When `false`, Stormrunner forces content-bearing browser requests to revalidate instead of using cached content responses
- `config.browser.check_newer_versions`: Force browser cache revalidation with `Cache-Control: no-cache` / `Pragma: no-cache`
- `config.browser.new_user_per_iteration`: Reset the browser protocol between iterations when `true`
- `config.browser.download_resources`: `all`, `none`, or `recorded` (`recorded` executes in browser replay when preserved recorded-resource metadata is available)
- `config.browser.viewport.width`, `config.browser.viewport.height`: Browser viewport dimensions

**Web HTTP/HTML compatibility keys:**
- `config.web_runtime.additional_attributes`: Arbitrary preserved RTS name/value pairs
- `config.web_runtime.download_filters.mode`: `none`, `allow_list`, or `block_list`
- `config.web_runtime.download_filters.hosts`: Hostnames matched by the selected download-filter mode
- `config.web_runtime.content_checks[]`: Global response content checks with `text`, `scope`, and `fail_if_found`
- `config.web_runtime.network_simulation.mode`: `max`, `preset`, or `custom`
- `config.web_runtime.network_simulation.preset`: `56k`, `dsl`, `t1`, or `10mbps`
- `config.web_runtime.network_simulation.bandwidth_kbps`, `config.web_runtime.network_simulation.latency_ms`: Custom browser network throttling values
- `config.web_runtime.streaming.mode`: `adaptive` or `fixed` compatibility setting for imported/authored media RTS
- `config.web_runtime.streaming.buffer_size_seconds`: Preserved streaming buffer setting
- `config.web_runtime.streaming.fixed_bitrate_kbps`: Preserved fixed-bitrate setting
- `config.web_runtime.streaming.bandwidth_estimation`: `rolling` or `instant` compatibility setting
- `config.web_runtime.data_formatting.auto_detect`: Preserve or enable automatic DFE detection behavior
- `config.web_runtime.data_formatting.chain[]`: Ordered DFE transform chain

### Web HTTP/HTML RTS Support

| Surface | Stormrunner field(s) | Current support |
| --- | --- | --- |
| Browser cache toggle | `config.browser.cache_enabled` | Executed in browser runtime |
| Cache content URLs | `config.browser.cache_content_urls` | Executed in browser runtime |
| Check newer versions | `config.browser.check_newer_versions` | Executed in browser runtime |
| New user each iteration | `config.browser.new_user_per_iteration` | Executed in browser runtime |
| Download resources | `config.browser.download_resources` | Partial: `all` and `none` execute directly; `recorded` executes when preserved recorded-resource metadata is available |
| Download filters | `config.web_runtime.download_filters.*` | Executed in browser runtime |
| Global content checks | `config.web_runtime.content_checks[]` | Executed in HTTP runtime |
| Network speed simulation | `config.web_runtime.network_simulation.*` | Executed in browser runtime |
| Data formatting | `config.web_runtime.data_formatting.*` | Partial: HTTP runtime executes configured DFE chains and auto-detects obvious base64-encoded text payloads; browser/runtime-wide DFE parity is still incomplete |
| Streaming | `config.web_runtime.streaming.*` | Compatibility-only: preserved and importable, but media playback semantics are not executed yet |
| Additional attributes | `config.web_runtime.additional_attributes` | Compatibility-only metadata |

See [docs/LOADRUNNER_RTS_REFERENCE.md](docs/LOADRUNNER_RTS_REFERENCE.md) for the broader Web HTTP/HTML mapping, including compatibility-only and non-applicable RTS concepts.

### Data Sources Section

```json
{
  "data_sources": [
    {
      "name": "users",
      "type": "csv",
      "path": "users.csv",
      "strategy": "sequential",
      "first_row_header": true
    }
  ]
}
```

**Data source options:**
- `name`: Reference name for templating
- `type`: `csv` or `dat`
- `path`: File path when inline data is not used
- `inline_data`: Optional inline CSV or DAT content
- `strategy`: `sequential`, `random`, or `unique`
- `delimiter`: Optional column delimiter
- `first_row_header`: Whether the first row contains column names
- `columns`: Explicit column names when the file has no header row

### Parameters Section

```json
{
  "parameters": [
    {
      "name": "order_id",
      "type": "random_number",
      "update": "each_occurrence",
      "min": 1000,
      "max": 9999,
      "pad_width": 6
    },
    {
      "name": "run_date",
      "type": "date_time",
      "update": "each_iteration",
      "format": "2006-01-02",
      "timezone": "UTC"
    },
    {
      "name": "username",
      "type": "file",
      "update": "each_iteration",
      "selection": "sequential",
      "on_exhaustion": "wrap",
      "data_source": "users",
      "column": "email"
    }
  ]
}
```

**Parameter options:**
- `name`: Parameter name used in templates
- `type`: `file`, `list`, `random_number`, `date_time`, `timestamp`, `counter`, `uuid`, or `random_string`
- `update`: `once`, `each_iteration`, or `each_occurrence`
- `selection`: For `file` and `list`, `sequential`, `random`, or `unique`
- `on_exhaustion`: For `file` and `list`, `wrap`, `empty`, or `error`
- `data_source`, `column`: Bind a parameter to one data source column
- `values`: Inline list values for `list`
- `min`, `max`: Bounds for `random_number`
- `start`, `end`, `step`: Counter configuration
- `format`, `timezone`, `offset_seconds`: Date/time and timestamp configuration
- `length`, `charset`: Random string configuration
- `pad_width`, `pad_char`: Left-padding for numeric outputs

**Usage in templates:**
```json
{
	"body": "{\"user\": \"{{param.username}}\", \"order\": \"{{param.order_id}}\", \"at\": \"{{param.run_date}}\"}"
}
```

### Init Section

```json
{
  "init": [
    {
      "name": "login",
      "type": "http",
      "method": "POST",
      "target": "/auth/login",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"username\": \"{{data.users.email}}\", \"password\": \"{{data.users.password}}\"}",
      "extractors": [
        {
          "name": "auth_token",
          "source": "body",
          "type": "json",
          "expression": "$.token"
        }
      ],
      "assertions": [
        {
          "type": "status",
          "expected": 200
        },
        {
          "type": "json",
          "value": "$.success",
          "expected": true
        }
      ]
    }
  ]
}
```

### Actions Section

**Java-over-HTTP action note:**
- Use normal HTTP-style request actions with `protocol: "java_http"`, but keep request bodies as opaque base64-encoded serialized bytes when using the current MVP codec.

```json
{
  "actions": [
    {
      "name": "view_products",
      "type": "http",
      "method": "GET",
      "target": "/products?category={{data.products.category}}&limit=50",
      "headers": {
        "Authorization": "Bearer {{var.auth_token}}"
      },
      "think_time": {
        "type": "gaussian",
        "mean_ms": 2000,
        "std_ms": 500,
        "min_ms": 500,
        "max_ms": 5000
      },
      "extractors": [
        {
          "name": "product_ids",
          "source": "body",
          "type": "json",
          "expression": "$.products[*].id"
        }
      ],
      "assertions": [
        {
          "type": "status",
          "expected": 200
        },
        {
          "type": "body",
          "value": "product",
          "expected": null
        }
      ]
    },
    {
      "name": "add_to_cart",
      "type": "http",
      "method": "POST",
      "target": "/cart/add",
      "body": "{\"product_id\": \"{{var.product_ids[0]}}\", \"quantity\": 1}",
      "headers": {
        "Authorization": "Bearer {{var.auth_token}}",
        "X-CSRF-Token": "{{var.csrf_token}}"
      }
    }
  ]
}
```

### Teardown Section

```json
{
  "teardown": [
    {
      "name": "logout",
      "type": "http",
      "method": "POST",
      "target": "/auth/logout",
      "headers": {
        "Authorization": "Bearer {{var.auth_token}}"
      }
    }
  ]
}
```

### Template Variables

**Environment variables:**
```
{{env.DATABASE_URL}}
{{env.API_KEY}}
```

**Data source columns:**
```
{{data.users.email}}
{{data.users.password}}
{{data.products.name}}
```

**Extracted variables:**
```
{{var.auth_token}}
{{var.session_id}}
{{var.csrf_token}}
```

Imported LoadRunner `{ParamName}` references are also resolved through `{{var.ParamName}}` compatibility lookup when the script declares a matching parameter.

**Functions:**
```
{{fn.uuid()}}                  — Generate UUID
{{fn.uuid}}                    — Generate UUID
{{fn.uuid.v4}}                 — Generate UUID v4
{{fake.uuid}}                  — Generate UUID using faker-style alias
{{fake.email}}                 — Generate a synthetic email address
{{fn.random(1,100)}}           — Random integer 1-100
{{fn.timestamp.unix}}          — Current Unix timestamp
{{fn.timestamp.unix_ms}}       — Current Unix timestamp in milliseconds
{{fn.timestamp.iso8601}}       — Current ISO8601 timestamp
{{fn.random_string(10)}}       — Random alphanumeric string (10 chars)
```

### Think Time Configurations

**Fixed delay (1000 ms):**
```json
{
  "think_time": {
    "type": "fixed",
    "mean_ms": 1000
  }
}
```

**Random between min and max:**
```json
{
  "think_time": {
    "type": "random",
    "min_ms": 500,
    "max_ms": 3000
  }
}
```

**Gaussian (normal) distribution:**
```json
{
  "think_time": {
    "type": "gaussian",
    "mean_ms": 2000,
    "std_ms": 500,
    "min_ms": 500,
    "max_ms": 5000
  }
}
```

**Ignore think time for this action:**
```json
{
  "think_time": {
    "ignore": true
  }
}
```

### Assertions

**Status code equals:**
```json
{
  "type": "status",
  "expected": 200
}
```

**JSON value matches:**
```json
{
  "type": "json",
  "value": "$.status",
  "expected": "success"
}
```

**Body contains text:**
```json
{
  "type": "body",
  "value": "success",
  "expected": null
}
```

**Header value matches:**
```json
{
  "type": "header",
  "value": "Content-Type",
  "expected": "application/json"
}
```

**Response time under threshold:**
```json
{
  "type": "response_time",
  "expected": 2000
}
```

### Complete Example Script

See `examples/simple-http.json` for a working example.

```json
{
  "config": {
    "protocol": "http",
    "base_url": "https://petstore.example.com",
    "timeout_seconds": 30
  },
  "data_sources": [
    {
      "name": "pets",
      "type": "csv",
      "path": "pets.csv",
      "headers": true,
      "cycling": "sequential"
    }
  ],
  "init": [],
  "actions": [
    {
      "name": "list_pets",
      "type": "http",
      "method": "GET",
      "target": "/api/pets?status=available",
      "assertions": [
        { "type": "status", "expected": 200 }
      ]
    },
    {
      "name": "get_pet",
      "type": "http",
      "method": "GET",
      "target": "/api/pets/{{data.pets.pet_id}}",
      "assertions": [
        { "type": "status", "expected": 200 }
      ]
    }
  ],
  "teardown": []
}
```
