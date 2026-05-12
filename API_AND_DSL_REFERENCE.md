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
  "script_id": "script-456",
  "vusers": 100,
  "duration_seconds": 300,
  "ramp_up_seconds": 30,
  "notes": "Peak hour simulation"
}
```

**Response:**
```json
{
  "id": "test-789",
  "name": "Homepage Load Test",
  "script_id": "script-456",
  "status": "queued",
  "vusers": 100,
  "duration_seconds": 300,
  "ramp_up_seconds": 30,
  "created_at": "2026-05-11T10:30:00Z"
}
```

#### List all tests

```
GET /api/v1/tests?project_id=proj-123&status=all
```

**Response:**
```json
[
  {
    "id": "test-789",
    "name": "Homepage Load Test",
    "script_id": "script-456",
    "status": "running",
    "vusers": 100,
    "duration_seconds": 300,
    "started_at": "2026-05-11T10:31:00Z",
    "current_vusers": 85
  },
  {
    "id": "test-788",
    "name": "Checkout Flow Test",
    "status": "completed",
    "completed_at": "2026-05-11T09:45:00Z"
  }
]
```

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
- `protocol`: "http", "browser", "jms-solace"
- `base_url`: Base URL for relative paths (HTTP)
- `timeout_seconds`: Request timeout
- `follow_redirects`: Auto-follow HTTP redirects
- `ssl_verify`: Validate SSL certificates
- `headers`: Default headers for all actions
- `connection_pool_size`: Connection reuse pool size (default: 10)

**JMS Solace config keys:**
- `config.jms.broker_url`: Solace broker URL, for example `tcps://broker.example.com:55443`
- `config.jms.message_vpn`: Solace message VPN
- `config.jms.destination`: Default queue/topic destination
- `config.jms.destination_type`: `queue` or `topic`
- `config.jms.reply_to`: Default reply destination for request/reply
- `config.jms.timeout_ms`: Default consume/request timeout
- `config.jms.mock`: Use the local in-memory broker for replay. Real Solace broker execution requires the Solace adapter build.

### Data Sources Section

```json
{
  "data_sources": [
    {
      "name": "users",
      "type": "csv",
      "path": "users.csv",
      "headers": true,
      "cycling": "sequential"
    },
    {
      "name": "products",
      "type": "json",
      "path": "products.json",
      "selector": "$.items",
      "cycling": "random"
    }
  ]
}
```

**Data source options:**
- `name`: Reference name for templating
- `type`: "csv", "json"
- `path`: File path (relative to script directory)
- `headers`: CSV has header row (true/false)
- `selector`: JSONPath for selecting array items
- `cycling`: "sequential", "random", "once"

**Usage in templates:**
```json
{
  "body": "{\"user_id\": \"{{data.users.id}}\", \"product\": \"{{data.products.name}}\"}"
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

**Functions:**
```
{{fn.uuid}}                    — Generate UUID
{{fn.uuid.v4}}                 — Generate UUID v4
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
