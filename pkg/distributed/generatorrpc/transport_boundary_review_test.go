package generatorrpc

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestProductionTLSWithTokenRequiresMinimumTokenLength(t *testing.T) {
	clearTransportEnv(t)
	certFile, keyFile, caFile := testCertFiles(t, t.TempDir(), "generator.test")
	t.Setenv("APEXLOAD_ENV", "production")
	t.Setenv(grpcTLSCertEnvVar, certFile)
	t.Setenv(grpcTLSKeyEnvVar, keyFile)
	t.Setenv(grpcAuthTokenEnvVar, "short-token")
	if _, err := ServerOptionsFromEnv(); err == nil || !strings.Contains(err.Error(), "at least") {
		t.Fatalf("production server short-token error = %v", err)
	}

	strongToken := strings.Repeat("x", minProductionAuthTokenBytes)
	t.Setenv(grpcAuthTokenEnvVar, strongToken)
	if _, err := ServerOptionsFromEnv(); err != nil {
		t.Fatalf("production server rejected minimum-length token: %v", err)
	}

	// The controller side has only a trusted server CA in a TLS-plus-token
	// topology. It must enforce the same token-only authentication floor.
	t.Setenv(grpcTLSCertEnvVar, "")
	t.Setenv(grpcTLSKeyEnvVar, "")
	t.Setenv(grpcTLSCAEnvVar, caFile)
	t.Setenv(grpcAuthTokenEnvVar, "short-token")
	if _, err := DialOptionsFromEnv(); err == nil || !strings.Contains(err.Error(), "at least") {
		t.Fatalf("production client short-token error = %v", err)
	}
	t.Setenv(grpcAuthTokenEnvVar, strongToken)
	if _, err := DialOptionsFromEnv(); err != nil {
		t.Fatalf("production client rejected minimum-length token: %v", err)
	}
}

func TestTokenInterceptorRequiresBearerScheme(t *testing.T) {
	const token = "controller-generator-token"
	unary, _ := tokenAuthInterceptors(token)
	handler := func(context.Context, any) (any, error) { return "accepted", nil }
	info := &grpc.UnaryServerInfo{}

	for _, value := range []string{token, "bearer " + token, "Basic " + token} {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(grpcAuthorizationHeader, value))
		if _, err := unary(ctx, nil, info, handler); status.Code(err) != codes.Unauthenticated {
			t.Errorf("authorization %q error = %v, want Unauthenticated", value, err)
		}
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(grpcAuthorizationHeader, grpcBearerPrefix+token))
	if got, err := unary(ctx, nil, info, handler); err != nil || got != "accepted" {
		t.Fatalf("valid bearer authorization = (%v, %v)", got, err)
	}
}
