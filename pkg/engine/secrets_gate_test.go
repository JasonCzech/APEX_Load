package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func unresolvedSecretAction() dsl.Action {
	return dsl.Action{
		Name:   "login",
		Type:   "GET",
		Method: "GET",
		Target: "/login?token={{secret.missing_token}}",
	}
}

func TestExecuteWithRetry_FailsClosedOnUnresolvedSecret(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, nil)

	resolved := vu.resolveAction(unresolvedSecretAction())
	_, err := vu.executeWithRetry(context.Background(), resolved, dsl.ErrorHandlingConfig{})
	if err == nil {
		t.Fatal("expected a fail-closed error for an unresolved secret")
	}
	if !strings.Contains(err.Error(), "secret.missing_token") {
		t.Errorf("error should name the unresolved secret, got: %v", err)
	}
	if len(proto.names) != 0 {
		t.Fatalf("protocol must not execute with a placeholder secret; executed %v", proto.names)
	}
}

func TestExecuteWithRetry_OptOutAllowsUnresolvedSecret(t *testing.T) {
	t.Setenv(allowUnresolvedSecretsEnv, "true")
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, nil)

	resolved := vu.resolveAction(unresolvedSecretAction())
	if _, err := vu.executeWithRetry(context.Background(), resolved, dsl.ErrorHandlingConfig{}); err != nil {
		t.Fatalf("opt-out should allow execution, got: %v", err)
	}
	if len(proto.names) != 1 {
		t.Fatalf("expected one execution under opt-out; got %v", proto.names)
	}
}

func TestExecuteWithRetry_ResolvedSecretRuns(t *testing.T) {
	t.Setenv("APEXLOAD_SECRET_PRESENT_TOKEN", "real")
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, nil)

	resolved := vu.resolveAction(dsl.Action{
		Name:   "login",
		Type:   "GET",
		Method: "GET",
		Target: "/login?token={{secret.present_token}}",
	})
	if got := resolved.Target; got != "/login?token=real" {
		t.Fatalf("secret should be interpolated, got target %q", got)
	}
	if _, err := vu.executeWithRetry(context.Background(), resolved, dsl.ErrorHandlingConfig{}); err != nil {
		t.Fatalf("a resolved secret should run, got: %v", err)
	}
	if len(proto.names) != 1 {
		t.Fatalf("expected one execution; got %v", proto.names)
	}
}
