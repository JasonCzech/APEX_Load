package protocol

import (
	"context"
	"strings"
	"testing"
)

type panickingRuntimeExtensionProtocol struct{}

func (*panickingRuntimeExtensionProtocol) Name() string { return "panicking_runtime_extension_test" }
func (*panickingRuntimeExtensionProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (*panickingRuntimeExtensionProtocol) Execute(context.Context, *Action) (*Result, error) {
	return &Result{Success: true}, nil
}
func (*panickingRuntimeExtensionProtocol) Teardown(context.Context) error    { return nil }
func (*panickingRuntimeExtensionProtocol) HealthCheck(context.Context) error { return nil }
func (*panickingRuntimeExtensionProtocol) RuntimeCapabilities() RuntimeCapabilities {
	panic("resolved-capability-secret")
}
func (*panickingRuntimeExtensionProtocol) RuntimeHealthy(context.Context) bool {
	panic("resolved-health-secret")
}
func (*panickingRuntimeExtensionProtocol) PreflightRuntime(context.Context, RuntimePreflightRequest) (RuntimePreflightResult, error) {
	panic("resolved-preflight-secret")
}

func TestClassifyPayloadSize(t *testing.T) {
	testCases := []struct {
		name     string
		bytes    int64
		expected string
	}{
		{name: "empty", bytes: 0, expected: ""},
		{name: "small", bytes: 8 * 1024, expected: "small"},
		{name: "medium lower", bytes: 16 * 1024, expected: "medium"},
		{name: "medium upper", bytes: 64 * 1024, expected: "medium"},
		{name: "large", bytes: 128 * 1024, expected: "large"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ClassifyPayloadSize(testCase.bytes); got != testCase.expected {
				t.Fatalf("ClassifyPayloadSize(%d) = %q, want %q", testCase.bytes, got, testCase.expected)
			}
		})
	}
}

func TestBuildProtocolEvidenceLabelsJMSGenerically(t *testing.T) {
	evidence := BuildProtocolEvidence(&Action{Type: "jms_publish", Target: "orders"}, &Result{
		Success:          true,
		DependencyType:   "jms_queue",
		DependencyTarget: "orders",
	})
	if evidence["protocol"] != "jms" {
		t.Fatalf("protocol evidence = %#v, want provider-neutral jms", evidence)
	}
}

func TestBuildProtocolEvidenceSurfacesConsumerOffsetLag(t *testing.T) {
	evidence := BuildProtocolEvidence(&Action{Type: "kafka_consume", Target: "orders"}, &Result{
		Success:          true,
		DependencyType:   "kafka_topic",
		DependencyTarget: "orders",
		ServerTelemetry:  &ServerTelemetry{MessagesReceived: 1, ConsumerOffsetLag: 5},
	})
	messages, ok := evidence["messages"].(map[string]any)
	if !ok {
		t.Fatalf("evidence has no messages map: %#v", evidence)
	}
	if messages["consumer_offset_lag"] != int64(5) {
		t.Fatalf("consumer_offset_lag = %#v, want int64(5)", messages["consumer_offset_lag"])
	}
}

func TestBuildProtocolEvidencePrefersRedactedTarget(t *testing.T) {
	const secretTarget = "https://credential.example.test/private"
	evidence := BuildProtocolEvidence(&Action{
		Type:           "database_query",
		Target:         secretTarget,
		RedactedTarget: "[REDACTED]",
	}, &Result{
		Success:        true,
		DependencyType: "database",
	})
	if evidence["target"] != "[REDACTED]" {
		t.Fatalf("target evidence = %#v, want redacted target", evidence["target"])
	}
}

func TestBuildProtocolEvidenceRecognizesSOAPAndRedactsMetadata(t *testing.T) {
	evidence := BuildProtocolEvidence(&Action{
		Type:            "request",
		Target:          "https://soap.example.test/AuthService",
		Headers:         map[string]string{"SOAPAction": "urn:auth#super-secret"},
		RedactedHeaders: map[string]string{"SOAPAction": "urn:auth#[REDACTED]"},
		Params:          map[string]any{"lr_parameters": map[string]any{"Password": "super-secret"}},
		RedactedParams:  map[string]any{"lr_parameters": map[string]any{"Password": "[REDACTED]"}},
		SOAP:            &SOAPActionConfig{Action: "urn:auth#super-secret", Operation: "Login", BodyMode: "generated"},
		RedactedSOAP:    &SOAPActionConfig{Action: "urn:auth#[REDACTED]", Operation: "Login", BodyMode: "generated"},
	}, &Result{
		Success:          true,
		DependencyType:   "soap",
		DependencyTarget: "soap.example.test#Login",
		StatusCode:       200,
		Extractions:      map[string]string{"soap_version": "1.1"},
	})
	if evidence["protocol"] != "soap" {
		t.Fatalf("protocol evidence = %#v, want soap", evidence)
	}
	if evidence["operation"] != "Login" {
		t.Fatalf("operation = %#v, want Login", evidence["operation"])
	}
	metadata, ok := evidence["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %#v", evidence["metadata"])
	}
	headers, ok := metadata["headers"].(map[string]string)
	if !ok || headers["SOAPAction"] != "urn:auth#[REDACTED]" {
		t.Fatalf("headers = %#v, want redacted SOAPAction", metadata["headers"])
	}
	soapMetadata, ok := metadata["soap"].(map[string]any)
	if !ok || soapMetadata["action"] != "urn:auth#[REDACTED]" {
		t.Fatalf("soap metadata = %#v, want redacted action", metadata["soap"])
	}
	params, ok := metadata["params"].(map[string]any)
	if !ok {
		t.Fatalf("params = %#v, want redacted params map", metadata["params"])
	}
	lrParams, ok := params["lr_parameters"].(map[string]any)
	if !ok || lrParams["Password"] != "[REDACTED]" {
		t.Fatalf("params = %#v, want redacted lr parameter", metadata["params"])
	}
}

func TestRuntimeFeaturesSatisfyNormalizedMembership(t *testing.T) {
	tests := []struct {
		name     string
		features []string
		required string
		want     bool
	}{
		{name: "phase exact", features: []string{FeatureJavaVuserPhaseLifecycleV1}, required: FeatureJavaVuserPhaseLifecycleV1, want: true},
		{name: "governed persistent satisfies phase", features: []string{FeatureJavaVuserPersistentLifecycleV1, FeatureJavaVuserSessionFrameTokenV1}, required: FeatureJavaVuserPhaseLifecycleV1, want: true},
		{name: "tokenless persistent does not satisfy phase", features: []string{FeatureJavaVuserPersistentLifecycleV1}, required: FeatureJavaVuserPhaseLifecycleV1},
		{name: "phase does not satisfy persistent", features: []string{FeatureJavaVuserPhaseLifecycleV1}, required: FeatureJavaVuserPersistentLifecycleV1},
		{name: "governed persistent exact", features: []string{FeatureJavaVuserPersistentLifecycleV1, FeatureJavaVuserSessionFrameTokenV1}, required: FeatureJavaVuserPersistentLifecycleV1, want: true},
		{name: "tokenless persistent rejected", features: []string{FeatureJavaVuserPersistentLifecycleV1}, required: FeatureJavaVuserPersistentLifecycleV1},
		{name: "frame token alone rejected", features: []string{FeatureJavaVuserSessionFrameTokenV1}, required: FeatureJavaVuserPersistentLifecycleV1},
		{name: "case and whitespace normalized", features: []string{" JAVA_VUSER_PHASE_LIFECYCLE_V1 "}, required: FeatureJavaVuserPhaseLifecycleV1, want: true},
		{name: "unrelated", features: []string{"java_vuser_validation_only_v1"}, required: FeatureJavaVuserPhaseLifecycleV1},
		{name: "empty requirement", required: "", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := RuntimeFeaturesSatisfy(test.features, test.required); got != test.want {
				t.Fatalf("RuntimeFeaturesSatisfy(%v, %q) = %v, want %v", test.features, test.required, got, test.want)
			}
		})
	}
}

func TestMissingJavaVuserPersistentRuntimeFeature(t *testing.T) {
	if got := MissingJavaVuserPersistentRuntimeFeature(nil); got != FeatureJavaVuserPersistentLifecycleV1 {
		t.Fatalf("missing feature for empty capabilities = %q", got)
	}
	if got := MissingJavaVuserPersistentRuntimeFeature([]string{FeatureJavaVuserPersistentLifecycleV1}); got != FeatureJavaVuserSessionFrameTokenV1 {
		t.Fatalf("missing feature for tokenless persistent capabilities = %q", got)
	}
	if got := MissingJavaVuserPersistentRuntimeFeature([]string{
		FeatureJavaVuserPersistentLifecycleV1,
		FeatureJavaVuserSessionFrameTokenV1,
	}); got != "" {
		t.Fatalf("missing feature for governed persistent capabilities = %q", got)
	}
}

func TestRuntimeExtensionBoundariesRecoverWithoutLeakingPanicValues(t *testing.T) {
	instance := &panickingRuntimeExtensionProtocol{}
	if _, supported, err := ReadRuntimeCapabilities(instance); !supported || err == nil {
		t.Fatalf("ReadRuntimeCapabilities supported=%v error=%v, want recovered failure", supported, err)
	} else if strings.Contains(err.Error(), "resolved-capability-secret") {
		t.Fatalf("capability error leaked panic value: %v", err)
	}
	if healthy, supported, err := CheckRuntimeHealth(context.Background(), instance); healthy || !supported || err == nil {
		t.Fatalf("CheckRuntimeHealth healthy=%v supported=%v error=%v, want recovered failure", healthy, supported, err)
	} else if strings.Contains(err.Error(), "resolved-health-secret") {
		t.Fatalf("health error leaked panic value: %v", err)
	}
	if _, supported, err := RunRuntimePreflight(context.Background(), instance, RuntimePreflightRequest{}); !supported || err == nil {
		t.Fatalf("RunRuntimePreflight supported=%v error=%v, want recovered failure", supported, err)
	} else if strings.Contains(err.Error(), "resolved-preflight-secret") {
		t.Fatalf("preflight error leaked panic value: %v", err)
	}
}

func TestRuntimeExtensionBoundariesReportUnsupportedProtocol(t *testing.T) {
	instance := &unsupportedRuntimeExtensionProtocol{}
	if _, supported, err := ReadRuntimeCapabilities(instance); supported || err != nil {
		t.Fatalf("ReadRuntimeCapabilities supported=%v error=%v", supported, err)
	}
	if _, supported, err := CheckRuntimeHealth(context.Background(), instance); supported || err != nil {
		t.Fatalf("CheckRuntimeHealth supported=%v error=%v", supported, err)
	}
	if _, supported, err := RunRuntimePreflight(context.Background(), instance, RuntimePreflightRequest{}); supported || err != nil {
		t.Fatalf("RunRuntimePreflight supported=%v error=%v", supported, err)
	}
}

type unsupportedRuntimeExtensionProtocol struct{}

func (*unsupportedRuntimeExtensionProtocol) Name() string {
	return "unsupported_runtime_extension_test"
}
func (*unsupportedRuntimeExtensionProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (*unsupportedRuntimeExtensionProtocol) Execute(context.Context, *Action) (*Result, error) {
	return &Result{Success: true}, nil
}
func (*unsupportedRuntimeExtensionProtocol) Teardown(context.Context) error    { return nil }
func (*unsupportedRuntimeExtensionProtocol) HealthCheck(context.Context) error { return nil }
