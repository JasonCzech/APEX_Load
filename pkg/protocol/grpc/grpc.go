package grpc

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
	"github.com/jmespath/go-jmespath"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const defaultCallTimeout = 30 * time.Second

func init() {
	protocol.Register("grpc", func() protocol.Protocol {
		return &GRPC{}
	})
}

type GRPC struct {
	target       string
	conn         *googlegrpc.ClientConn
	metadata     metadata.MD
	useReflect   bool
	plainText    bool
	maxRecvBytes int
	callTimeout  time.Duration
	methods      map[string]protoreflect.MethodDescriptor
	mu           sync.Mutex
}

func (g *GRPC) Name() string { return "grpc" }

func (g *GRPC) Init(ctx context.Context, config map[string]any) error {
	g.target, _ = stringConfig(config, "base_url")
	if g.target == "" {
		g.target, _ = stringConfig(config, "grpc_target")
	}
	if g.target == "" {
		return fmt.Errorf("grpc target or base_url is required")
	}
	g.useReflect, _ = boolConfig(config, "grpc_use_reflection")
	g.plainText, _ = boolConfig(config, "grpc_plain_text")
	g.maxRecvBytes, _ = intConfig(config, "grpc_max_recv_msg_size")
	g.callTimeout = durationConfig(config, "grpc_timeout_ms", defaultCallTimeout)
	g.metadata = buildMetadata(config)
	g.methods = make(map[string]protoreflect.MethodDescriptor)

	opts := []googlegrpc.DialOption{}
	if authority, ok := stringConfig(config, "grpc_authority"); ok {
		opts = append(opts, googlegrpc.WithAuthority(authority))
	}
	if g.plainText {
		opts = append(opts, googlegrpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		opts = append(opts, googlegrpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})))
	}
	if g.maxRecvBytes > 0 {
		opts = append(opts, googlegrpc.WithDefaultCallOptions(googlegrpc.MaxCallRecvMsgSize(g.maxRecvBytes)))
	}
	conn, err := googlegrpc.DialContext(ctx, g.target, opts...)
	if err != nil {
		return err
	}
	g.conn = conn
	return nil
}

func (g *GRPC) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	result := &protocol.Result{
		ActionName:       action.Name,
		StartTime:        time.Now(),
		Extractions:      make(map[string]string),
		DependencyType:   "grpc",
		DependencyTarget: g.target,
	}

	var err error
	switch action.Type {
	case "grpc_unary":
		err = g.invokeUnary(ctx, action, result)
	default:
		err = fmt.Errorf("unsupported grpc action type: %s", action.Type)
	}
	result.Duration = time.Since(result.StartTime)
	if err != nil {
		result.Success = false
		result.StatusCode = int(grpcstatus.Code(err))
		result.ErrorMessage = err.Error()
		return result, nil
	}
	result.Success = true
	result.StatusCode = int(codes.OK)
	return result, nil
}

func (g *GRPC) Teardown(_ context.Context) error {
	if g.conn == nil {
		return nil
	}
	return g.conn.Close()
}

func (g *GRPC) HealthCheck(ctx context.Context) error {
	if g.target == "" {
		return fmt.Errorf("grpc target or base_url is required")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (g *GRPC) invokeUnary(ctx context.Context, action *protocol.Action, result *protocol.Result) error {
	method, err := g.resolveMethod(ctx, action.Target)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, actionTimeout(action, g.callTimeout))
	defer cancel()
	callCtx = metadata.NewOutgoingContext(callCtx, g.callMetadata(action))

	request := dynamicpb.NewMessage(method.Input())
	if len(action.Body) > 0 {
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(action.Body, request); err != nil {
			return fmt.Errorf("decode grpc request body: %w", err)
		}
	}
	response := dynamicpb.NewMessage(method.Output())
	fullMethod := "/" + string(method.Parent().FullName()) + "/" + string(method.Name())

	result.BytesSent = int64(len(action.Body))
	err = g.conn.Invoke(callCtx, fullMethod, request, response)
	if err != nil {
		return err
	}

	responseBody, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(response)
	if err != nil {
		return fmt.Errorf("encode grpc response body: %w", err)
	}
	result.ResponseBody = string(responseBody)
	result.BytesReceived = int64(len(responseBody))
	result.StatusCode = int(codes.OK)
	for _, extractor := range action.Extractors {
		value, err := extract(extractor, responseBody)
		if err == nil {
			result.Extractions[extractor.Name] = value
		}
	}
	for _, assertion := range action.Assertions {
		if err := evaluate(assertion, result, responseBody); err != nil {
			return err
		}
	}
	return nil
}

func (g *GRPC) resolveMethod(ctx context.Context, target string) (protoreflect.MethodDescriptor, error) {
	normalized, serviceName, methodName, err := splitMethodTarget(target)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	if method, ok := g.methods[normalized]; ok {
		g.mu.Unlock()
		return method, nil
	}
	g.mu.Unlock()
	if !g.useReflect {
		return nil, fmt.Errorf("grpc method %q is not cached and grpc.use_reflection is disabled", target)
	}

	files, err := g.loadReflectionFiles(ctx, serviceName)
	if err != nil {
		return nil, err
	}
	desc, err := files.FindDescriptorByName(protoreflect.FullName(serviceName))
	if err != nil {
		return nil, fmt.Errorf("resolve grpc service %q: %w", serviceName, err)
	}
	service, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%q is not a grpc service", serviceName)
	}
	method := service.Methods().ByName(protoreflect.Name(methodName))
	if method == nil {
		return nil, fmt.Errorf("grpc method %q not found on service %q", methodName, serviceName)
	}
	g.mu.Lock()
	g.methods[normalized] = method
	g.mu.Unlock()
	return method, nil
}

func (g *GRPC) loadReflectionFiles(ctx context.Context, serviceName string) (*protoregistry.Files, error) {
	stream, err := reflectionpb.NewServerReflectionClient(g.conn).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.CloseSend()
	if err := stream.Send(&reflectionpb.ServerReflectionRequest{
		MessageRequest: &reflectionpb.ServerReflectionRequest_FileContainingSymbol{
			FileContainingSymbol: serviceName,
		},
	}); err != nil {
		return nil, err
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	fileResponse := response.GetFileDescriptorResponse()
	if fileResponse == nil {
		return nil, fmt.Errorf("grpc reflection did not return descriptors for %q", serviceName)
	}

	set := &descriptorpb.FileDescriptorSet{}
	for _, raw := range fileResponse.FileDescriptorProto {
		file := &descriptorpb.FileDescriptorProto{}
		if err := proto.Unmarshal(raw, file); err != nil {
			return nil, fmt.Errorf("decode reflected descriptor: %w", err)
		}
		set.File = append(set.File, file)
	}
	return protodesc.NewFiles(set)
}

func (g *GRPC) callMetadata(action *protocol.Action) metadata.MD {
	out := metadata.Join(g.metadata)
	for key, value := range action.Headers {
		out.Set(strings.ToLower(key), value)
	}
	return out
}

func buildMetadata(config map[string]any) metadata.MD {
	md := metadata.MD{}
	if raw, ok := config["grpc_metadata"].(map[string]string); ok {
		for key, value := range raw {
			md.Set(strings.ToLower(key), value)
		}
	}
	if auth := authHeader(config); auth != "" && len(md.Get("authorization")) == 0 {
		md.Set("authorization", auth)
	}
	return md
}

func splitMethodTarget(target string) (normalized string, service string, method string, err error) {
	trimmed := strings.Trim(strings.TrimSpace(target), "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", "", fmt.Errorf("grpc target must be service/method, got %q", target)
	}
	return parts[0] + "/" + parts[1], parts[0], parts[1], nil
}

func extract(ext protocol.Extractor, body []byte) (string, error) {
	if ext.Source != "" && ext.Source != "body" {
		return "", fmt.Errorf("unsupported grpc extraction source: %s", ext.Source)
	}
	switch ext.Type {
	case "json", "jsonpath":
		return extractJSONPath(ext.Expression, body)
	case "jmespath":
		return extractJMESPath(ext.Expression, body)
	default:
		return "", fmt.Errorf("unsupported grpc extraction type: %s", ext.Type)
	}
}

func evaluate(assertion protocol.Assertion, result *protocol.Result, body []byte) error {
	switch assertion.Type {
	case "grpc_status":
		expected, ok := intValue(assertion.Expected)
		if !ok {
			return fmt.Errorf("grpc_status assertion: expected integer, got %v", assertion.Expected)
		}
		if result.StatusCode != expected {
			return fmt.Errorf("expected grpc status %d, got %d", expected, result.StatusCode)
		}
	case "body_contains":
		if !strings.Contains(string(body), assertion.Value) {
			return fmt.Errorf("grpc response body does not contain %q", assertion.Value)
		}
	case "body_not_contains":
		if strings.Contains(string(body), assertion.Value) {
			return fmt.Errorf("grpc response body contains %q (unexpected)", assertion.Value)
		}
	default:
		return fmt.Errorf("unsupported grpc assertion type: %s", assertion.Type)
	}
	return nil
}

func extractJSONPath(expr string, body []byte) (string, error) {
	expr = strings.TrimPrefix(strings.TrimSpace(expr), "$.")
	if expr == "" {
		return "", fmt.Errorf("jsonpath expression is required")
	}
	parts := strings.Split(expr, ".")

	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", fmt.Errorf("invalid JSON response: %w", err)
	}
	for _, part := range parts {
		idx := -1
		if bracket := strings.Index(part, "["); bracket != -1 {
			idxText := strings.TrimSuffix(part[bracket+1:], "]")
			if _, err := fmt.Sscanf(idxText, "%d", &idx); err != nil {
				return "", fmt.Errorf("invalid array index %q", idxText)
			}
			part = part[:bracket]
		}

		obj, ok := current.(map[string]any)
		if !ok {
			return "", fmt.Errorf("expected object at %q", part)
		}
		current, ok = obj[part]
		if !ok {
			return "", fmt.Errorf("field %q not found", part)
		}
		if idx >= 0 {
			arr, ok := current.([]any)
			if !ok {
				return "", fmt.Errorf("expected array at %q", part)
			}
			if idx >= len(arr) {
				return "", fmt.Errorf("index %d out of range for %q", idx, part)
			}
			current = arr[idx]
		}
	}
	return stringifyExtraction(current)
}

func extractJMESPath(expr string, body []byte) (string, error) {
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", fmt.Errorf("invalid JSON response: %w", err)
	}
	result, err := jmespath.Search(expr, current)
	if err != nil {
		return "", fmt.Errorf("invalid JMESPath expression: %w", err)
	}
	if result == nil {
		return "", fmt.Errorf("no match for JMESPath %q", expr)
	}
	return stringifyExtraction(result)
}

func stringifyExtraction(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []any, map[string]any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "", fmt.Errorf("marshal extraction result: %w", err)
		}
		return string(encoded), nil
	default:
		return fmt.Sprintf("%v", typed), nil
	}
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case float32:
		return int(typed), true
	default:
		return 0, false
	}
}

func authHeader(config map[string]any) string {
	authType, _ := stringConfig(config, "auth_type")
	switch strings.ToLower(strings.TrimSpace(authType)) {
	case "basic":
		username, _ := stringConfig(config, "auth_username")
		password, _ := stringConfig(config, "auth_password")
		token := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		return "Basic " + token
	case "bearer":
		token, _ := stringConfig(config, "auth_token")
		if strings.TrimSpace(token) == "" {
			return ""
		}
		return "Bearer " + token
	default:
		return ""
	}
}

func actionTimeout(action *protocol.Action, fallback time.Duration) time.Duration {
	if value, ok := intParamOK(action.Params, "timeout_ms"); ok {
		return time.Duration(value) * time.Millisecond
	}
	return fallback
}

func durationConfig(config map[string]any, key string, fallback time.Duration) time.Duration {
	value, ok := intConfig(config, key)
	if !ok || value <= 0 {
		return fallback
	}
	return time.Duration(value) * time.Millisecond
}

func intConfig(config map[string]any, key string) (int, bool) {
	raw, ok := config[key]
	if !ok {
		return 0, false
	}
	switch typed := raw.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case float32:
		return int(typed), true
	default:
		return 0, false
	}
}

func intParamOK(params map[string]any, key string) (int, bool) {
	raw, ok := params[key]
	if !ok {
		return 0, false
	}
	switch typed := raw.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case float32:
		return int(typed), true
	default:
		return 0, false
	}
}

func stringConfig(config map[string]any, key string) (string, bool) {
	raw, ok := config[key]
	if !ok {
		return "", false
	}
	value, ok := raw.(string)
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

func boolConfig(config map[string]any, key string) (bool, bool) {
	raw, ok := config[key]
	if !ok {
		return false, false
	}
	value, ok := raw.(bool)
	return value, ok
}
