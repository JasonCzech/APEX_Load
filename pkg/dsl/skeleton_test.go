package dsl

import "testing"

func TestSkeletonActionPopulatesRequiredFields(t *testing.T) {
	action := SkeletonAction("grpc_unary")
	if action.Type != "grpc_unary" {
		t.Fatalf("type = %q, want grpc_unary", action.Type)
	}
	if action.Target == "" {
		t.Fatal("target is empty, want required target placeholder")
	}
}

func TestSkeletonActionPopulatesRequireOneOf(t *testing.T) {
	action := SkeletonAction("db_query")
	if action.Type != "db_query" {
		t.Fatalf("type = %q, want db_query", action.Type)
	}
	if action.Target == "" && action.Body == "" && action.BodyFile == "" {
		t.Fatal("db_query skeleton did not populate a target/body/body_file one-of field")
	}
}

func TestSkeletonActionFallbackForUnknownType(t *testing.T) {
	action := SkeletonAction("request")
	if action.Type != "request" || action.Name != "request" {
		t.Fatalf("fallback action = %#v, want request name/type", action)
	}
}
