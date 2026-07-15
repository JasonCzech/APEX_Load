package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
)

func TestReplayPreflightEnvironmentChecksMirrorRuntimeIsolation(t *testing.T) {
	t.Setenv(engine.EnvScriptEnvAllowlist, "APEXLOAD_PUBLIC_REGION,APEXLOAD_GRPC_AUTH_TOKEN")
	t.Setenv("APEXLOAD_PUBLIC_REGION", "east")
	t.Setenv("APEXLOAD_GRPC_AUTH_TOKEN", "platform-control-token")
	t.Setenv("APEXLOAD_SECRET_DB_PASSWORD", "script-secret")
	t.Setenv(engine.ProjectSecretEnvironmentName("project-a", "db.password"), "project-script-secret")

	if !engine.ScriptEnvAvailable("APEXLOAD_PUBLIC_REGION") {
		t.Fatal("allowlisted public environment value should be available")
	}
	if engine.ScriptEnvAvailable("APEXLOAD_GRPC_AUTH_TOKEN") {
		t.Fatal("credential-like environment value must remain unavailable")
	}
	if replaySecretAvailable("project-a", "APEXLOAD_GRPC_AUTH_TOKEN") {
		t.Fatal("preflight exposed an arbitrary process credential as a script secret")
	}
	if !replaySecretAvailable("project-a", "db.password") {
		t.Fatal("preflight did not recognize the dedicated script secret binding")
	}
	if replaySecretAvailable("project-b", "db.password") {
		t.Fatal("preflight crossed the project secret namespace")
	}
	if !replaySecretAvailable("", "db.password") {
		t.Fatal("projectless local preflight did not retain the legacy binding")
	}
}
