package grpc

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/protocol"
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

const (
	defaultCallTimeout               = 30 * time.Second
	maxGRPCMethodCacheEntries        = 256
	maxGRPCMethodCacheKeyBytes       = 4 << 10
	maxGRPCReflectionFiles           = 64
	maxGRPCReflectionDescriptorBytes = 2 << 20
	maxGRPCConfiguredDescriptorBytes = 8 << 20
)

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
	methodOrder  []string
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
	if g.maxRecvBytes <= 0 {
		g.maxRecvBytes = dsl.MaxGRPCStreamBytes
	}
	if g.maxRecvBytes > dsl.MaxGRPCStreamBytes {
		return fmt.Errorf("grpc_max_recv_msg_size exceeds limit %d", dsl.MaxGRPCStreamBytes)
	}
	var err error
	g.callTimeout, err = protocol.BoundedMilliseconds(config["grpc_timeout_ms"], defaultCallTimeout, dsl.MaxConfiguredProtocolTimeout)
	if err != nil {
		return fmt.Errorf("grpc timeout_ms %w", err)
	}
	g.metadata = buildMetadata(config)
	g.methods = make(map[string]protoreflect.MethodDescriptor)
	g.methodOrder = nil
	if err := g.loadConfiguredDescriptorFiles(config); err != nil {
		return err
	}

	opts := []googlegrpc.DialOption{}
	if authority, ok := stringConfig(config, "grpc_authority"); ok {
		opts = append(opts, googlegrpc.WithAuthority(authority))
	}
	if g.plainText {
		opts = append(opts, googlegrpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		opts = append(opts, googlegrpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})))
	}
	opts = append(opts, googlegrpc.WithDefaultCallOptions(googlegrpc.MaxCallRecvMsgSize(g.maxRecvBytes)))
	conn, err := googlegrpc.DialContext(ctx, g.target, opts...)
	if err != nil {
		return err
	}
	g.conn = conn
	return nil
}

func (g *GRPC) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	result := &protocol.Result{
		ActionName:       protocol.ActionName(action),
		StartTime:        time.Now(),
		Extractions:      make(map[string]string),
		DependencyType:   "grpc",
		DependencyTarget: g.target,
	}
	if action == nil {
		result.Duration = time.Since(result.StartTime)
		result.Success = false
		result.StatusCode = int(codes.InvalidArgument)
		result.ErrorMessage = "grpc action is required"
		return result, nil
	}
	callTimeout, err := actionTimeout(action, g.callTimeout)
	if err != nil {
		result.Duration = time.Since(result.StartTime)
		result.Success = false
		result.StatusCode = int(codes.InvalidArgument)
		result.ErrorMessage = err.Error()
		result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
		return result, nil
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	err = nil
	switch action.Type {
	case "grpc_unary":
		err = g.invokeUnary(callCtx, action, result)
	case "grpc_server_stream":
		err = g.invokeServerStream(callCtx, action, result)
	case "grpc_client_stream":
		err = g.invokeClientStream(callCtx, action, result)
	case "grpc_bidi_stream":
		err = g.invokeBidiStream(callCtx, action, result)
	default:
		err = fmt.Errorf("unsupported grpc action type: %s", action.Type)
	}
	result.Duration = time.Since(result.StartTime)
	if err != nil {
		result.Success = false
		result.StatusCode = int(grpcstatus.Code(err))
		result.ErrorMessage = err.Error()
		result.Snapshot = g.buildSnapshot(action, result)
		result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
		return result, nil
	}
	result.Success = true
	result.StatusCode = int(codes.OK)
	result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
	return result, nil
}

func (g *GRPC) buildSnapshot(action *protocol.Action, result *protocol.Result) *protocol.ErrorSnapshot {
	if action == nil || result == nil {
		return nil
	}
	requestURL := strings.TrimSpace(action.Target)
	if action.RedactedTarget != "" {
		requestURL = strings.TrimSpace(action.RedactedTarget)
	}
	if g.target != "" && requestURL != "" && !strings.Contains(requestURL, "://") {
		requestURL = strings.TrimRight(g.target, "/") + "/" + strings.TrimLeft(requestURL, "/")
	}
	headers := cloneMetadataHeaders(action.Headers)
	if len(action.RedactedHeaders) > 0 {
		headers = cloneMetadataHeaders(action.RedactedHeaders)
	}
	body := string(action.Body)
	if len(action.RedactedBody) > 0 {
		body = string(action.RedactedBody)
	}
	return &protocol.ErrorSnapshot{
		RequestMethod:  action.Type,
		RequestURL:     requestURL,
		RequestHeaders: headers,
		RequestBody:    body,
		ResponseStatus: result.StatusCode,
		ResponseBody:   result.ResponseBody,
	}
}

func cloneMetadataHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
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
	if method.IsStreamingClient() || method.IsStreamingServer() {
		return fmt.Errorf("grpc_unary requires a unary method, got streaming method %q", action.Target)
	}
	callCtx := metadata.NewOutgoingContext(ctx, g.callMetadata(action))

	request := dynamicpb.NewMessage(method.Input())
	if len(action.Body) > 0 {
		if len(action.Body) > dsl.MaxGRPCStreamBytes {
			return fmt.Errorf("grpc request body exceeds %d byte limit", dsl.MaxGRPCStreamBytes)
		}
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
	if len(responseBody) > dsl.MaxGRPCStreamBytes {
		return fmt.Errorf("grpc response exceeds %d byte limit", dsl.MaxGRPCStreamBytes)
	}
	result.ResponseBody = string(responseBody)
	result.BytesReceived = int64(len(responseBody))
	result.StatusCode = int(codes.OK)
	return g.applyResponseChecks(action, result, responseBody)
}

func (g *GRPC) invokeServerStream(ctx context.Context, action *protocol.Action, result *protocol.Result) error {
	method, err := g.resolveMethod(ctx, action.Target)
	if err != nil {
		return err
	}
	if !method.IsStreamingServer() || method.IsStreamingClient() {
		return fmt.Errorf("grpc_server_stream requires a server-streaming method, got %q", action.Target)
	}
	callCtx := metadata.NewOutgoingContext(ctx, g.callMetadata(action))

	stream, err := g.conn.NewStream(callCtx, streamDesc(method), fullMethodName(method))
	if err != nil {
		return err
	}
	request, requestBytes, err := buildDynamicMessage(method.Input(), action.Body)
	if err != nil {
		return err
	}
	result.BytesSent = int64(requestBytes)
	if err := stream.SendMsg(request); err != nil {
		return err
	}
	if err := stream.CloseSend(); err != nil {
		return err
	}

	responseBody, received, err := receiveStreamMessages(stream, method.Output(), intParam(action.Params, "max_messages", 1))
	if err != nil {
		return err
	}
	result.ResponseBody = string(responseBody)
	result.BytesReceived = int64(len(responseBody))
	result.StatusCode = int(codes.OK)
	result.ServerTelemetry = &protocol.ServerTelemetry{MessagesSent: 1, MessagesReceived: received}
	return g.applyResponseChecks(action, result, responseBody)
}

func (g *GRPC) invokeClientStream(ctx context.Context, action *protocol.Action, result *protocol.Result) error {
	method, err := g.resolveMethod(ctx, action.Target)
	if err != nil {
		return err
	}
	if !method.IsStreamingClient() || method.IsStreamingServer() {
		return fmt.Errorf("grpc_client_stream requires a client-streaming method, got %q", action.Target)
	}
	callCtx := metadata.NewOutgoingContext(ctx, g.callMetadata(action))
	bodies, err := streamBodies(action)
	if err != nil {
		return err
	}

	stream, err := g.conn.NewStream(callCtx, streamDesc(method), fullMethodName(method))
	if err != nil {
		return err
	}
	sent, sentBytes, err := sendStreamMessages(stream, method.Input(), bodies)
	if err != nil {
		return err
	}
	if err := stream.CloseSend(); err != nil {
		return err
	}
	response := dynamicpb.NewMessage(method.Output())
	if err := stream.RecvMsg(response); err != nil {
		return err
	}
	responseBody, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(response)
	if err != nil {
		return fmt.Errorf("encode grpc response body: %w", err)
	}
	if len(responseBody) > dsl.MaxGRPCStreamBytes {
		return fmt.Errorf("grpc response exceeds %d byte limit", dsl.MaxGRPCStreamBytes)
	}
	result.BytesSent = int64(sentBytes)
	result.ResponseBody = string(responseBody)
	result.BytesReceived = int64(len(responseBody))
	result.StatusCode = int(codes.OK)
	result.ServerTelemetry = &protocol.ServerTelemetry{MessagesSent: sent, MessagesReceived: 1}
	return g.applyResponseChecks(action, result, responseBody)
}

func (g *GRPC) invokeBidiStream(ctx context.Context, action *protocol.Action, result *protocol.Result) error {
	method, err := g.resolveMethod(ctx, action.Target)
	if err != nil {
		return err
	}
	if !method.IsStreamingClient() || !method.IsStreamingServer() {
		return fmt.Errorf("grpc_bidi_stream requires a bidirectional streaming method, got %q", action.Target)
	}
	callCtx := metadata.NewOutgoingContext(ctx, g.callMetadata(action))
	bodies, err := streamBodies(action)
	if err != nil {
		return err
	}

	stream, err := g.conn.NewStream(callCtx, streamDesc(method), fullMethodName(method))
	if err != nil {
		return err
	}
	sent, sentBytes, err := sendStreamMessages(stream, method.Input(), bodies)
	if err != nil {
		return err
	}
	if err := stream.CloseSend(); err != nil {
		return err
	}
	maxMessages := intParam(action.Params, "max_messages", len(bodies))
	if maxMessages <= 0 {
		maxMessages = len(bodies)
	}
	responseBody, received, err := receiveStreamMessages(stream, method.Output(), maxMessages)
	if err != nil {
		return err
	}
	result.BytesSent = int64(sentBytes)
	result.ResponseBody = string(responseBody)
	result.BytesReceived = int64(len(responseBody))
	result.StatusCode = int(codes.OK)
	result.ServerTelemetry = &protocol.ServerTelemetry{MessagesSent: sent, MessagesReceived: received}
	return g.applyResponseChecks(action, result, responseBody)
}

func (g *GRPC) applyResponseChecks(action *protocol.Action, result *protocol.Result, responseBody []byte) error {
	for _, extractor := range action.Extractors {
		value, err := extract(extractor, responseBody)
		if err == nil {
			if err := protocol.StoreExtraction(result, extractor.Name, value); err != nil {
				result.Extractions = nil
				return fmt.Errorf("grpc extractor %q output rejected: %w", extractor.Name, err)
			}
		}
	}
	for _, assertion := range action.Assertions {
		if err := evaluate(assertion, result, responseBody); err != nil {
			return err
		}
	}
	return nil
}

func buildDynamicMessage(desc protoreflect.MessageDescriptor, body []byte) (*dynamicpb.Message, int, error) {
	if len(body) > dsl.MaxGRPCStreamBytes {
		return nil, 0, fmt.Errorf("grpc request message exceeds %d byte limit", dsl.MaxGRPCStreamBytes)
	}
	message := dynamicpb.NewMessage(desc)
	if len(body) == 0 {
		return message, 0, nil
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, message); err != nil {
		return nil, 0, fmt.Errorf("decode grpc request body: %w", err)
	}
	return message, len(body), nil
}

func streamBodies(action *protocol.Action) ([][]byte, error) {
	if action == nil {
		return nil, fmt.Errorf("grpc action is required")
	}
	if raw, ok := action.Params["messages"]; ok {
		messages, err := bodiesFromParam(raw)
		if err != nil {
			return nil, err
		}
		if len(messages) > 0 {
			return messages, nil
		}
	}
	if len(action.Body) > 0 {
		if len(action.Body) > dsl.MaxGRPCStreamBytes {
			return nil, fmt.Errorf("grpc stream input exceeds %d byte limit", dsl.MaxGRPCStreamBytes)
		}
		return [][]byte{action.Body}, nil
	}
	return [][]byte{[]byte(`{}`)}, nil
}

func bodiesFromParam(raw any) ([][]byte, error) {
	var count int
	switch typed := raw.(type) {
	case []string:
		count = len(typed)
	case []any:
		count = len(typed)
	default:
		return nil, nil
	}
	if count > dsl.MaxGRPCStreamMessages {
		return nil, fmt.Errorf("grpc stream input exceeds %d message limit", dsl.MaxGRPCStreamMessages)
	}
	totalBytes := 0
	retain := func(body []byte) error {
		if len(body) > dsl.MaxGRPCStreamBytes-totalBytes {
			return fmt.Errorf("grpc stream input exceeds %d byte limit", dsl.MaxGRPCStreamBytes)
		}
		totalBytes += len(body)
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		out := make([][]byte, 0, len(typed))
		for _, value := range typed {
			if len(value) > dsl.MaxGRPCStreamBytes-totalBytes {
				return nil, fmt.Errorf("grpc stream input exceeds %d byte limit", dsl.MaxGRPCStreamBytes)
			}
			body := []byte(value)
			if err := retain(body); err != nil {
				return nil, err
			}
			out = append(out, body)
		}
		return out, nil
	case []any:
		out := make([][]byte, 0, len(typed))
		for _, value := range typed {
			switch item := value.(type) {
			case string:
				if len(item) > dsl.MaxGRPCStreamBytes-totalBytes {
					return nil, fmt.Errorf("grpc stream input exceeds %d byte limit", dsl.MaxGRPCStreamBytes)
				}
				body := []byte(item)
				if err := retain(body); err != nil {
					return nil, err
				}
				out = append(out, body)
			default:
				encoded, err := json.Marshal(item)
				if err != nil {
					return nil, fmt.Errorf("encode grpc stream input: %w", err)
				}
				if err := retain(encoded); err != nil {
					return nil, err
				}
				out = append(out, encoded)
			}
		}
		return out, nil
	default:
		return nil, nil
	}
}

func sendStreamMessages(stream googlegrpc.ClientStream, input protoreflect.MessageDescriptor, bodies [][]byte) (int, int, error) {
	if len(bodies) > dsl.MaxGRPCStreamMessages {
		return 0, 0, fmt.Errorf("grpc stream input exceeds %d message limit", dsl.MaxGRPCStreamMessages)
	}
	totalBytes := 0
	for _, body := range bodies {
		if len(body) > dsl.MaxGRPCStreamBytes-totalBytes {
			return 0, 0, fmt.Errorf("grpc stream input exceeds %d byte limit", dsl.MaxGRPCStreamBytes)
		}
		totalBytes += len(body)
	}
	sentBytes := 0
	for _, body := range bodies {
		message, size, err := buildDynamicMessage(input, body)
		if err != nil {
			return 0, 0, err
		}
		if err := stream.SendMsg(message); err != nil {
			return 0, 0, err
		}
		sentBytes += size
	}
	return len(bodies), sentBytes, nil
}

func receiveStreamMessages(stream googlegrpc.ClientStream, output protoreflect.MessageDescriptor, maxMessages int) ([]byte, int, error) {
	if maxMessages <= 0 {
		maxMessages = 1
	}
	if maxMessages > dsl.MaxGRPCStreamMessages {
		return nil, 0, fmt.Errorf("grpc max_messages exceeds limit %d", dsl.MaxGRPCStreamMessages)
	}
	messages := make([]json.RawMessage, 0, maxMessages)
	var totalBytes int
	for len(messages) < maxMessages {
		response := dynamicpb.NewMessage(output)
		err := stream.RecvMsg(response)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, len(messages), err
		}
		encoded, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(response)
		if err != nil {
			return nil, len(messages), fmt.Errorf("encode grpc response body: %w", err)
		}
		totalBytes += len(encoded)
		if totalBytes > dsl.MaxGRPCStreamBytes {
			return nil, len(messages), fmt.Errorf("grpc streamed response exceeds %d byte limit", dsl.MaxGRPCStreamBytes)
		}
		messages = append(messages, json.RawMessage(encoded))
	}
	encoded, err := json.Marshal(messages)
	return encoded, len(messages), err
}

func streamDesc(method protoreflect.MethodDescriptor) *googlegrpc.StreamDesc {
	return &googlegrpc.StreamDesc{
		StreamName:    string(method.Name()),
		ServerStreams: method.IsStreamingServer(),
		ClientStreams: method.IsStreamingClient(),
	}
}

func fullMethodName(method protoreflect.MethodDescriptor) string {
	return "/" + string(method.Parent().FullName()) + "/" + string(method.Name())
}

func (g *GRPC) resolveMethod(ctx context.Context, target string) (protoreflect.MethodDescriptor, error) {
	normalized, serviceName, methodName, err := splitMethodTarget(target)
	if err != nil {
		return nil, err
	}
	if len(normalized) > maxGRPCMethodCacheKeyBytes {
		return nil, fmt.Errorf("grpc method target exceeds %d bytes", maxGRPCMethodCacheKeyBytes)
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
	if existing, ok := g.methods[normalized]; ok {
		g.mu.Unlock()
		return existing, nil
	}
	for len(g.methods) >= maxGRPCMethodCacheEntries && len(g.methodOrder) > 0 {
		oldest := g.methodOrder[0]
		g.methodOrder = g.methodOrder[1:]
		delete(g.methods, oldest)
	}
	if len(g.methods) >= maxGRPCMethodCacheEntries {
		g.mu.Unlock()
		return nil, fmt.Errorf("grpc configured method cache exceeds %d entries", maxGRPCMethodCacheEntries)
	}
	g.methods[normalized] = method
	g.methodOrder = append(g.methodOrder, normalized)
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
	if len(fileResponse.FileDescriptorProto) > maxGRPCReflectionFiles {
		return nil, fmt.Errorf("grpc reflection returned more than %d descriptor files", maxGRPCReflectionFiles)
	}

	set := &descriptorpb.FileDescriptorSet{}
	totalDescriptorBytes := 0
	for _, raw := range fileResponse.FileDescriptorProto {
		if len(raw) > maxGRPCReflectionDescriptorBytes-totalDescriptorBytes {
			return nil, fmt.Errorf("grpc reflection descriptors exceed %d bytes", maxGRPCReflectionDescriptorBytes)
		}
		totalDescriptorBytes += len(raw)
		file := &descriptorpb.FileDescriptorProto{}
		if err := proto.Unmarshal(raw, file); err != nil {
			return nil, fmt.Errorf("decode reflected descriptor: %w", err)
		}
		set.File = append(set.File, file)
	}
	return protodesc.NewFiles(set)
}

func (g *GRPC) loadConfiguredDescriptorFiles(config map[string]any) error {
	paths := stringSliceConfig(config, "grpc_proto_files")
	for _, path := range paths {
		set, err := readDescriptorSet(path)
		if err != nil {
			return err
		}
		files, err := protodesc.NewFiles(set)
		if err != nil {
			return fmt.Errorf("load grpc descriptor set %q: %w", path, err)
		}
		var registerErr error
		files.RangeFiles(func(file protoreflect.FileDescriptor) bool {
			registerErr = registerFileMethodsBounded(g.methods, file)
			return registerErr == nil
		})
		if registerErr != nil {
			return registerErr
		}
	}
	return nil
}

func readDescriptorSet(path string) (*descriptorpb.FileDescriptorSet, error) {
	f, err := fileasset.OpenPath(path)
	if err != nil {
		return nil, fmt.Errorf("read grpc descriptor set %q: %w", path, err)
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxGRPCConfiguredDescriptorBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read grpc descriptor set %q: %w", path, err)
	}
	if len(body) > maxGRPCConfiguredDescriptorBytes {
		return nil, fmt.Errorf("grpc descriptor set %q exceeds %d bytes", path, maxGRPCConfiguredDescriptorBytes)
	}
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(body, set); err == nil {
		return set, nil
	}
	if err := json.Unmarshal(body, set); err != nil {
		return nil, fmt.Errorf("decode grpc descriptor set %q: %w", path, err)
	}
	return set, nil
}

func registerFileMethodsBounded(methods map[string]protoreflect.MethodDescriptor, file protoreflect.FileDescriptor) error {
	services := file.Services()
	for i := 0; i < services.Len(); i++ {
		service := services.Get(i)
		serviceName := string(service.FullName())
		for j := 0; j < service.Methods().Len(); j++ {
			method := service.Methods().Get(j)
			key := serviceName + "/" + string(method.Name())
			if len(key) > maxGRPCMethodCacheKeyBytes {
				return fmt.Errorf("grpc configured method target exceeds %d bytes", maxGRPCMethodCacheKeyBytes)
			}
			if _, exists := methods[key]; !exists && len(methods) >= maxGRPCMethodCacheEntries {
				return fmt.Errorf("grpc configured method cache exceeds %d entries", maxGRPCMethodCacheEntries)
			}
			methods[key] = method
		}
	}
	return nil
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

func actionTimeout(action *protocol.Action, fallback time.Duration) (time.Duration, error) {
	if action == nil || action.Params == nil {
		return fallback, nil
	}
	value, present := action.Params["timeout_ms"]
	if !present {
		return fallback, nil
	}
	timeout, err := protocol.BoundedMilliseconds(value, fallback, dsl.MaxConfiguredProtocolTimeout)
	if err != nil {
		return 0, fmt.Errorf("grpc action timeout_ms %w", err)
	}
	return timeout, nil
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

func intParam(params map[string]any, key string, fallback int) int {
	value, ok := intParamOK(params, key)
	if !ok {
		return fallback
	}
	return value
}

func stringSliceConfig(config map[string]any, key string) []string {
	raw, ok := config[key]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			text, ok := value.(string)
			if ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
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
