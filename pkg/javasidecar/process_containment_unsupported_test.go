//go:build aix || dragonfly || freebsd || netbsd || openbsd || solaris

package javasidecar

import (
	"context"
	"testing"
)

func TestPersistentSessionFailsClosedWithoutStrongProcessContainment(t *testing.T) {
	server := NewServer(ServerOptions{})
	session, failure := server.startPersistentSession(
		context.Background(),
		preparedRuntime{},
		ExecutionRequest{
			Command: ActionCommand{
				ActionType: "java_vuser_run",
				ClassName:  "Actions",
				Method:     "action",
			},
		},
		&ownerGeneration{},
	)
	if session != nil || failure == nil || failure.code != "process_containment_unavailable" {
		t.Fatalf("startPersistentSession() = %#v, %#v, want process_containment_unavailable", session, failure)
	}
}
