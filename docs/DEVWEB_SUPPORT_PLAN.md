# Implementation Plan: DevWeb Protocol Support

## Objective
Enable APEX Load to execute and import DevWeb-equivalent workloads — HTTP/2, WebSocket, and gRPC — using Go-native protocol implementations that maintain the 10k VUser/pod performance target.

DevWeb is OpenText's JavaScript-based protocol-level HTTP testing engine. APEX Load's existing `pkg/protocol/http` already covers DevWeb's core HTTP capabilities. This plan addresses the three remaining gaps: **WebSocket protocol support**, **gRPC protocol support**, and **DevWeb script import**.

## Relationship To Other Plans
- **Protocol Extension** (`docs/PROTOCOL_EXTENSION_PLAN.md`): SOAP and Java-over-HTTP are separate protocol tracks. WebSocket and gRPC are additive, lightweight protocols that follow the same registration pattern.
- **Mobile Browser Emulation** (`docs/MOBILE_BROWSER_EMULATION_PLAN.md`): Orthogonal — browser-based testing at ~30 VUsers/pod. WebSocket and gRPC are high-density protocols at ~10k VUsers/pod.
- **Browser Client Metrics** (`docs/BROWSER_CLIENT_METRICS_PLAN.md`): No overlap. DevWeb operates at the transport level, not the browser level.

## Current Baseline

### Already Covered By Existing HTTP Protocol
| DevWeb Capability | APEX Load Status | Location |
|:---|:---|:---|
| HTTP/1.1 requests | ✅ Implemented | `pkg/protocol/http` |
| HTTP/2 support | ✅ Implemented | `ForceAttemptHTTP2` in `http/config.go` |
| Request headers, body, cookies | ✅ Implemented | `http/http.go` Execute() |
| Response extraction (JSON, regex, XPath) | ✅ Implemented | `http/extract.go` |
| Response assertions | ✅ Implemented | `http/assert.go` |
| TLS/SSL configuration | ✅ Implemented | `http/config.go` buildTLSConfig() |
| Proxy support | ✅ Implemented | `http/config.go` applyProxy() |
| Auth (Basic, Bearer) | ✅ Implemented | `http/config.go` buildAuthHeader() |
| Data parameterization | ✅ Implemented | `engine/vuser.go` interpolate() |
| Think time / pacing | ✅ Implemented | `engine/vuser.go`, `engine/pacing.go` |
| Cross-platform execution | ✅ Implemented | Go binary |
| WebSocket connect/send/receive/ping/close | ✅ Implemented | `pkg/protocol/websocket` |
| WebSocket DSL config + engine wiring | ✅ Implemented | `pkg/dsl/types.go`, `pkg/engine/config.go` |
| gRPC unary invocation via reflection | ✅ Implemented | `pkg/protocol/grpc` |
| gRPC descriptor-set loading | ✅ Implemented | `pkg/protocol/grpc` |
| gRPC streaming client paths | ✅ Implemented | `pkg/protocol/grpc` |
| gRPC DSL config + engine wiring | ✅ Implemented | `pkg/dsl/types.go`, `pkg/engine/config.go` |
| DevWeb `.js` script import | ✅ Implemented | `pkg/convert/devweb`, `POST /api/v1/scripts/import/devweb` |
| DevWeb construct support matrix | ✅ Implemented | `GET /api/v1/scripts/import/devweb/support-matrix`, `pkg/convert/devweb/support.go` |
| HAR file import | ✅ Implemented | `pkg/convert/har`, `POST /api/v1/scripts/import/har` |
| OpenAPI/Swagger → script | ✅ Implemented for backend import/review | `POST /api/v1/scripts/import/openapi`, `pkg/convert/openapi` |

### Not Yet Implemented
| DevWeb Capability | Priority | Notes |
|:---|:---|:---|
| Full JavaScript runtime compatibility | Demand-gated | DevWeb import remains a static conversion/review workflow, not a JavaScript runtime layer. |

## Design Principles

### 1. One Protocol Per Transport
WebSocket and gRPC are distinct transport models. Each gets its own protocol package registered through the existing `protocol.Register()` pattern.

### 2. Goroutine-Native
Both WebSocket and gRPC map naturally to Go's concurrency model. A single goroutine per VUser holds one persistent connection — no thread pools, no event loop overhead.

### 3. Reuse HTTP Infrastructure
WebSocket upgrades from HTTP. gRPC runs over HTTP/2. Both protocols should reuse TLS configuration, proxy settings, and auth patterns from the existing HTTP config surface rather than reimplementing them.

### 4. Composable With Existing Engine
New protocols must work with the existing VUser loop, think time, pacing, extractors, assertions, metrics, and pause control without engine modifications.

## 1. WebSocket Protocol

### Architecture
WebSocket is a persistent, bidirectional transport. Unlike HTTP's request-response model, a WebSocket VUser opens a connection, sends and receives messages over time, and eventually closes. The protocol implementation manages connection lifecycle within the existing `Protocol` interface.

### Protocol Registration
```go
// pkg/protocol/websocket/websocket.go
func init() {
    protocol.Register("websocket", func() protocol.Protocol {
        return &WebSocket{}
    })
}
```

### Struct Design
```go
type WebSocket struct {
    baseURL        string
    conn           *websocket.Conn
    dialer         *websocket.Dialer
    defaultHeaders http.Header
    readTimeout    time.Duration
    writeTimeout   time.Duration
    maxMessageSize int64
    authHeader     string
    mu             sync.Mutex
    messageLog     []MessageEntry
}

type MessageEntry struct {
    Direction string    // "sent" or "received"
    Type      string    // "text" or "binary"
    Data      string
    Timestamp time.Time
}
```

### Action Types

| Action Type | Purpose | Required Fields |
|:---|:---|:---|
| `ws_connect` | Open WebSocket connection | `target` (URL) |
| `ws_send` | Send a message | `body` (message content), `params.type` ("text"/"binary") |
| `ws_receive` | Wait for and capture a message | `params.timeout_ms`, optional `params.match` |
| `ws_close` | Close connection gracefully | optional `params.code`, `params.reason` |
| `ws_ping` | Send ping frame | — |

### Execute Implementation
```go
func (w *WebSocket) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
    result := &protocol.Result{
        ActionName:     action.Name,
        StartTime:      time.Now(),
        Extractions:    make(map[string]string),
        DependencyType: "websocket",
    }

    switch action.Type {
    case "ws_connect":
        err := w.connect(ctx, action)
        // Sets DependencyTarget from URL host
    case "ws_send":
        err := w.send(ctx, action)
        // Records BytesSent
    case "ws_receive":
        msg, err := w.receive(ctx, action)
        // Sets ResponseBody, runs extractors/assertions against message
    case "ws_close":
        err := w.close(ctx, action)
    case "ws_ping":
        err := w.ping(ctx, action)
    }
    // ... result population, extractor/assertion evaluation
}
```

### DSL Config Extensions
Add to `pkg/dsl/types.go`:

```go
type WebSocketConfig struct {
    ReadTimeoutMs  int    `json:"read_timeout_ms,omitempty"`   // default: 30000
    WriteTimeoutMs int    `json:"write_timeout_ms,omitempty"`  // default: 10000
    MaxMessageSize int64  `json:"max_message_size,omitempty"`  // default: 1MB
    Subprotocol    string `json:"subprotocol,omitempty"`       // e.g. "graphql-ws"
    Origin         string `json:"origin,omitempty"`
    Compression    bool   `json:"compression,omitempty"`
    Reconnect      bool   `json:"reconnect,omitempty"`         // retry writes after a stale connection
}
```

Add `WebSocket WebSocketConfig` field to `ScriptConfig`.

### DSL Script Example
```json
{
    "protocol": "websocket",
    "config": {
        "base_url": "wss://api.example.com",
        "websocket": {
            "read_timeout_ms": 5000,
            "subprotocol": "graphql-ws"
        }
    },
    "actions": [
        {
            "name": "connect",
            "type": "ws_connect",
            "target": "/ws/graphql"
        },
        {
            "name": "subscribe_orders",
            "type": "ws_send",
            "body": "{\"type\":\"subscribe\",\"payload\":{\"query\":\"subscription { orderCreated { id } }\"}}",
            "think_time": { "type": "fixed", "mean_ms": 500 }
        },
        {
            "name": "receive_order",
            "type": "ws_receive",
            "params": { "timeout_ms": 10000 },
            "extractors": [
                { "name": "order_id", "source": "body", "type": "json", "expression": "$.payload.data.orderCreated.id" }
            ]
        },
        {
            "name": "disconnect",
            "type": "ws_close"
        }
    ]
}
```

### Go Dependency
`github.com/gorilla/websocket` — mature, widely used, supports compression and subprotocols.

### Scale Characteristics
- Memory per VUser: ~8-16 KB (goroutine + connection buffers)
- Connections per pod: 10,000+ on 4 vCPU / 8 GB (similar to HTTP)
- Bottleneck: OS file descriptors and network bandwidth, not memory

## 2. gRPC Protocol

### Architecture
gRPC runs over HTTP/2 with Protocol Buffer serialization. The APEX Load implementation uses Go's native gRPC client library. For maximum flexibility without requiring `.proto` compilation at runtime, the protocol supports both **dynamic reflection** and **raw byte payloads**.

### Protocol Registration
```go
// pkg/protocol/grpc/grpc.go
func init() {
    protocol.Register("grpc", func() protocol.Protocol {
        return &GRPC{}
    })
}
```

### Struct Design
```go
type GRPC struct {
    target       string
    conn         *grpc.ClientConn
    dialOpts     []grpc.DialOption
    metadata     metadata.MD
    protoFiles   []string
    descriptors  map[string]*desc.MethodDescriptor  // service.method → descriptor
    mu           sync.Mutex
}
```

### Action Types

| Action Type | Purpose | Required Fields |
|:---|:---|:---|
| `grpc_unary` | Unary RPC call | `target` (service/method), `body` (JSON payload) |
| `grpc_server_stream` | Server-streaming RPC | `target`, `body`, `params.max_messages` |
| `grpc_client_stream` | Client-streaming RPC | `target`, `params.messages` (array) |
| `grpc_bidi_stream` | Bidirectional streaming | `target`, `params.messages`, `params.max_receive` |

### Dynamic Invocation Strategy
Rather than requiring compiled `.proto` stubs, the protocol uses gRPC server reflection or pre-loaded `.proto` file descriptors to dynamically invoke methods:

```go
func (g *GRPC) invokeUnary(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
    methodDesc, err := g.resolveMethod(action.Target)
    // Parse JSON body into dynamic protobuf message
    reqMsg := dynamic.NewMessage(methodDesc.GetInputType())
    reqMsg.UnmarshalJSON(action.Body)
    // Invoke via dynamic stub
    respMsg := dynamic.NewMessage(methodDesc.GetOutputType())
    err = g.conn.Invoke(ctx, methodDesc.GetFullyQualifiedName(), reqMsg, respMsg)
    // Marshal response to JSON for extractors
}
```

### DSL Config Extensions
Add to `pkg/dsl/types.go`:

```go
type GRPCConfig struct {
    ProtoFiles     []string          `json:"proto_files,omitempty"`
    UseReflection  bool              `json:"use_reflection,omitempty"`
    PlainText      bool              `json:"plain_text,omitempty"`       // no TLS
    MaxRecvMsgSize int               `json:"max_recv_msg_size,omitempty"`
    Metadata       map[string]string `json:"metadata,omitempty"`        // default headers
    Authority      string            `json:"authority,omitempty"`
}
```

Add `GRPC GRPCConfig` field to `ScriptConfig`.

### DSL Script Example
```json
{
    "protocol": "grpc",
    "config": {
        "base_url": "grpc.example.com:443",
        "grpc": {
            "proto_files": ["order_service.proto"],
            "metadata": { "authorization": "Bearer {{var.token}}" }
        }
    },
    "actions": [
        {
            "name": "create_order",
            "type": "grpc_unary",
            "target": "order.OrderService/CreateOrder",
            "body": "{\"customer_id\": \"{{var.customer_id}}\", \"items\": [{\"sku\": \"SKU-001\", \"qty\": 2}]}",
            "extractors": [
                { "name": "order_id", "source": "body", "type": "json", "expression": "$.order_id" }
            ],
            "assertions": [
                { "type": "grpc_status", "expected": 0 }
            ]
        },
        {
            "name": "stream_updates",
            "type": "grpc_server_stream",
            "target": "order.OrderService/WatchOrder",
            "body": "{\"order_id\": \"{{var.order_id}}\"}",
            "params": { "max_messages": 5, "timeout_ms": 10000 }
        }
    ]
}
```

### Go Dependencies
- `google.golang.org/grpc` — standard Go gRPC client
- `github.com/jhump/protoreflect` — dynamic message creation and server reflection
- `google.golang.org/protobuf` — protobuf runtime

### Scale Characteristics
- Memory per VUser: ~16-32 KB (goroutine + HTTP/2 connection + protobuf buffers)
- Connections per pod: 8,000–10,000 on 4 vCPU / 8 GB
- HTTP/2 multiplexing means fewer TCP connections needed vs. HTTP/1.1

## 3. DevWeb Script Import

### Strategy
DevWeb scripts are JavaScript files using the `load.*` SDK. The converter parses JS source to extract `load.WebRequest`, `load.WebSocket`, and `load.GrpcClient` calls, then emits the appropriate APEX Load DSL script.

### Package Location
`pkg/convert/devweb/`

### Parser Design
Unlike the LoadRunner C parser which uses regex against a limited function set, the DevWeb parser uses a lightweight JavaScript AST approach:

```go
// pkg/convert/devweb/parser.go
type ParsedStep struct {
    Type       string            // "web_request", "websocket", "grpc"
    Name       string            // from load.action() or transaction name
    Method     string
    URL        string
    Headers    map[string]string
    Body       string
    Params     map[string]any
    LineNumber int
}

func ParseDevWebScript(filename, content string) (*ParsedScript, error) {
    // 1. Scan for load.action("name", async function() { ... }) blocks
    // 2. Within each action, extract load.WebRequest / load.WebSocket / load.GrpcClient calls
    // 3. Extract URL, method, headers, body, extractors from SDK call arguments
    // 4. Extract load.transaction() start/end markers
    // 5. Extract load.thinkTime() calls
}
```

### SDK Call Mapping

| DevWeb SDK Call | APEX Load DSL | Protocol |
|:---|:---|:---|
| `new load.WebRequest({...}).send()` | HTTP action (GET/POST/etc.) | `http` |
| `new load.WebSocket(url)` | `ws_connect` | `websocket` |
| `ws.open()` | `ws_connect` | `websocket` |
| `ws.send(msg)` | `ws_send` | `websocket` |
| `ws.receive()` | `ws_receive` | `websocket` |
| `ws.close()` | `ws_close` | `websocket` |
| `new load.GrpcClient({...})` | gRPC init config | `grpc` |
| `grpcClient.invoke(service, method, payload)` | `grpc_unary` | `grpc` |
| `load.transaction("name")` | Transaction marker | — |
| `load.thinkTime(seconds)` | Think time config | — |
| `load.log(msg)` | Skipped (warning) | — |
| `load.extractors.boundary(...)` | Boundary extractor | — |
| `load.extractors.jsonPath(...)` | JSON extractor | — |

### Protocol Auto-Detection
The converter scans the script to determine which protocol to use:

```go
func detectProtocol(steps []ParsedStep) string {
    hasHTTP, hasWS, hasGRPC := false, false, false
    for _, step := range steps {
        switch step.Type {
        case "web_request": hasHTTP = true
        case "websocket":   hasWS = true
        case "grpc":        hasGRPC = true
        }
    }
    // Pure WebSocket or gRPC scripts get their protocol
    // Mixed scripts default to HTTP with warnings
}
```

### API Endpoint
Add to the existing import handlers:

```
POST /api/v1/scripts/import/devweb  — Upload DevWeb .zip or .js, get converted DSL
```

### Import Risks
- DevWeb scripts can contain arbitrary JavaScript logic (loops, conditionals, custom functions) that cannot be mechanically translated to the declarative DSL
- The converter handles the common SDK patterns; complex orchestration logic generates warnings
- `require()` imports across multiple files require the full script directory, not just `main.js`

## 4. HAR File Import

DevWeb's primary recording workflow uses HAR (HTTP Archive) files. Adding HAR import is complementary and supports both DevWeb migration and general HTTP test creation.

### Package Location
`pkg/convert/har/`

### Design
HAR files are standard JSON. The converter maps HAR entries to HTTP actions:

```go
type HARConverter struct{}

func (c *HARConverter) Convert(harData []byte, opts ConvertOptions) (*ConvertResult, error) {
    var har HARFile
    json.Unmarshal(harData, &har)
    // Map each har.Log.Entries[i] → dsl.Action
    // Auto-detect base URL from common host
    // Filter static resources (images, CSS, JS) unless opted in
    // Extract cookies as init actions
}
```

### API Endpoint
```
POST /api/v1/scripts/import/har  — Upload .har file, get converted DSL
```

## 5. Engine Config Wiring

Extend `pkg/engine/config.go` `buildProtoConfig()` for new protocols:

```go
// WebSocket config
wsCfg := script.Config.WebSocket
if wsCfg.ReadTimeoutMs > 0 {
    cfg["websocket_read_timeout_ms"] = float64(wsCfg.ReadTimeoutMs)
}
if wsCfg.WriteTimeoutMs > 0 {
    cfg["websocket_write_timeout_ms"] = float64(wsCfg.WriteTimeoutMs)
}
if wsCfg.MaxMessageSize > 0 {
    cfg["websocket_max_message_size"] = float64(wsCfg.MaxMessageSize)
}
if wsCfg.Subprotocol != "" {
    cfg["websocket_subprotocol"] = wsCfg.Subprotocol
}
if wsCfg.Origin != "" {
    cfg["websocket_origin"] = wsCfg.Origin
}
if wsCfg.Compression {
    cfg["websocket_compression"] = true
}
if wsCfg.Reconnect {
    cfg["websocket_reconnect"] = true
}

// gRPC config
grpcCfg := script.Config.GRPC
if len(grpcCfg.ProtoFiles) > 0 {
    cfg["grpc_proto_files"] = grpcCfg.ProtoFiles
}
if grpcCfg.UseReflection {
    cfg["grpc_use_reflection"] = true
}
if grpcCfg.PlainText {
    cfg["grpc_plain_text"] = true
}
if len(grpcCfg.Metadata) > 0 {
    cfg["grpc_metadata"] = grpcCfg.Metadata
}
```

## 6. Dashboard Support

### Script Creation
- Protocol selector in Create Scenario modal adds "WebSocket" and "gRPC" options
- Protocol-specific config panels for connection settings
- Action type palette changes based on selected protocol

### Script Import
- Add "DevWeb (.js)" and "HAR (.har)" options to the existing LoadRunner import UI
- Reuse the same import flow: upload → convert → review warnings → save

### Execution View
- WebSocket metrics: connection count, messages sent/received per second, message latency
- gRPC metrics: calls per second by method, stream message throughput, status code distribution

## 7. Testing Requirements

### Unit Tests

| Test Area | File | Coverage |
|:---|:---|:---|
| WebSocket connect/send/receive/close | `pkg/protocol/websocket/websocket_test.go` | ✅ All action types against local echo server, plus message extractors |
| WebSocket config parsing | `pkg/protocol/websocket/websocket_test.go` | ✅ Timeout, subprotocol, origin, compression |
| WebSocket engine execution | `pkg/engine/websocket_integration_test.go` | ✅ Init/action/teardown script through the VUser engine |
| gRPC unary invocation | `pkg/protocol/grpc/grpc_test.go` | ✅ Dynamic invoke with server reflection and JSON protobuf body |
| gRPC streaming | `pkg/protocol/grpc/grpc_test.go` | ✅ Bidirectional streaming against reflected generator heartbeat; server/client stream runtime paths implemented |
| gRPC reflection fallback | `pkg/protocol/grpc/grpc_test.go` | ✅ Reflection and file-based descriptor set loading |
| DevWeb JS parser | `pkg/convert/devweb/converter_test.go` | ✅ WebRequest, WebSocket, gRPC invoke, ZIP input |
| DevWeb converter API | `pkg/api/handlers_scripts_test.go` | ✅ `/api/v1/scripts/import/devweb` JSON import |
| HAR converter | `pkg/convert/har/converter_test.go` | ✅ Entry mapping, correlations, base URL |
| HAR converter API | `pkg/api/handlers_scripts_test.go` | ✅ `/api/v1/scripts/import/har` JSON import |
| Dashboard DevWeb/HAR import | `dashboard/src/components/ScriptsAuthoringShell.test.tsx` | ✅ Script create flow exposes DevWeb and HAR import routes |
| Dashboard protocol authoring | `dashboard/src/components/scriptAuthoringAssist.test.ts` | ✅ WebSocket and gRPC starter actions are available in the authoring assist palette |
| Dashboard protocol metrics | `dashboard/src/components/TestExecutionSummary.test.tsx` | ✅ WebSocket and gRPC live message throughput/status telemetry renders in execution summaries |
| Engine config wiring | `pkg/engine/config_test.go` | ✅ WebSocket and gRPC config propagation |
| DSL parsing | `pkg/dsl/validate_test.go` | ✅ WebSocket and gRPC config/action validation |

### Benchmark Tests

| Benchmark | Measurement | Target |
|:---|:---|:---|
| WebSocket message throughput | Messages/sec per VUser | ✅ `BenchmarkWebSocketEchoRoundTrip`: ~58k echo round-trips/sec on Apple M4 (`17.1 µs/op`, `2.4 KB/op`) |
| WebSocket memory per VUser | RSS growth per connection | Tracked by benchmark allocations plus scale-test guidance; target remains < 20 KB steady-state connection overhead |
| gRPC unary throughput | Calls/sec per VUser | ✅ `BenchmarkGRPCUnaryDescriptorSet`: ~23k unary calls/sec on Apple M4 (`43.0 µs/op`, `13.4 KB/op`) |
| gRPC memory per VUser | RSS growth per connection | Tracked by benchmark allocations plus scale-test guidance; target remains < 40 KB steady-state connection overhead |
| gRPC protobuf marshal/unmarshal | Latency per operation | < 100 μs |

Run the local protocol benchmark suite with:

```bash
go test ./pkg/protocol/websocket -run '^$' -bench BenchmarkWebSocketEchoRoundTrip -benchmem -benchtime=1s
go test ./pkg/protocol/grpc -run '^$' -bench BenchmarkGRPCUnaryDescriptorSet -benchmem -benchtime=1s
```

Scale guidance:
- WebSocket and gRPC preserve the goroutine-per-VUser execution model, so pod-level scaling remains bounded primarily by file descriptors, target-side connection limits, and network throughput.
- For protocol-level smoke gates, keep `websocket_max_message_size`, `grpc_max_recv_msg_size`, and stream `max_messages` explicit in scripts to avoid unbounded response buffering.
- For 10k VUser/pod runs, raise container and node `ulimit -n` above the expected concurrent socket count and validate target service connection caps before ramping.
- Capture benchmark output alongside any future runtime or dependency upgrade that touches `pkg/protocol/websocket`, `pkg/protocol/grpc`, or dynamic protobuf handling.

## 8. Implementation Phases

| Phase | Milestone | Deliverables | Effort | Exit Criteria |
|:---|:---|:---|:---|:---|
| **Phase 1** | **WebSocket Protocol** | ✅ `pkg/protocol/websocket`, DSL config, engine wiring, action types, unit tests with echo server | Complete | WebSocket script executes end-to-end through the engine with connect, send, receive, ping, close |
| **Phase 2** | **gRPC Protocol** | ✅ `pkg/protocol/grpc`, reflection and descriptor-set loading, unary + streaming runtime paths, DSL config, unit tests | Complete | Unary calls execute with dynamic message construction; streaming client paths execute through the same dynamic protobuf transport |
| **Phase 3** | **DevWeb Script Import** | ✅ `pkg/convert/devweb` parser and converter, API endpoint, unit tests | Complete | DevWeb `.js` files with `load.WebRequest`, `load.WebSocket`, and `grpcClient.invoke` calls convert to reviewable DSL scripts |
| **Phase 4** | **HAR Import** | ✅ `pkg/convert/har` converter, API endpoint, unit tests | Complete | `.har` files from Chrome DevTools convert to HTTP DSL scripts |
| **Phase 5** | **Dashboard Integration** | ✅ Protocol selector, DevWeb/HAR import UI, WebSocket/gRPC starter actions, live protocol stream metrics | Complete | Users can create, import, and monitor WebSocket and gRPC scenarios |
| **Phase 6** | **Benchmarks + Hardening** | ✅ WebSocket reconnect-on-write recovery, WebSocket/gRPC benchmark entry points, published local measurements, scale guidance | Complete | Published performance measurements; documented scale guidance |

**Total estimate: 22-32 days**

Phases 1 and 2 are independent and can run in parallel. Phase 3 depends on both being complete to handle mixed-protocol DevWeb scripts. Phase 4 is independent of all others.

## 9. Risks And Mitigations

| Risk | Impact | Mitigation |
|:---|:---|:---|
| DevWeb JS scripts contain complex logic | Partial import coverage | Handle common SDK patterns; warn on unsupported constructs |
| gRPC proto file management complexity | User friction | Support server reflection as zero-config alternative |
| WebSocket server-specific handshake quirks | Connection failures | Expose full header customization on `ws_connect` |
| Mixed-protocol DevWeb scripts | Unclear DSL mapping | Default to HTTP; emit separate scripts per protocol with cross-references |
| Dynamic protobuf performance | Latency regression | Benchmark early; cache descriptors; offer compiled-stub path as future optimization |

## 10. Dependencies

### New Go Packages
- `github.com/gorilla/websocket` — WebSocket client (Phase 1)
- `google.golang.org/grpc` — gRPC client (Phase 2)
- `github.com/jhump/protoreflect` — dynamic protobuf messages and server reflection (Phase 2)
- `google.golang.org/protobuf` — protobuf runtime (Phase 2)

### Existing Infrastructure Reused
- `protocol.Protocol` interface and `protocol.Register()` — protocol registration
- `protocol.Result` — response model with extractors, assertions, snapshots
- `engine/vuser.go` — VUser loop, think time, pacing, pause control
- `engine/config.go` — config wiring pattern
- `pkg/protocol/http/config.go` — TLS, proxy, and auth config patterns (reused by WebSocket/gRPC)
- `pkg/convert/loadrunner/` — converter and parser patterns for DevWeb import

## Summary
DevWeb support decomposes into two high-density protocol implementations (WebSocket, gRPC) and two import converters (DevWeb JS, HAR). All four workstreams follow established patterns already proven in the HTTP, SOAP, and LoadRunner import code. WebSocket and gRPC maintain the goroutine-per-VUser model and target the same 10k VUser/pod density as HTTP, making them natural additions to the protocol registry.

Related documentation:
- Protocol extensions (SOAP, Java): `docs/PROTOCOL_EXTENSION_PLAN.md`
- Mobile browser emulation: `docs/MOBILE_BROWSER_EMULATION_PLAN.md`
- Browser client metrics: `docs/BROWSER_CLIENT_METRICS_PLAN.md`
- LoadRunner RTS reference: `docs/LOADRUNNER_RTS_REFERENCE.md`
