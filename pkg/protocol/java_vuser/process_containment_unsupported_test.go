//go:build aix || dragonfly || freebsd || netbsd || openbsd || solaris

package java_vuser

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestPersistentExecutionFailsClosedWithoutStrongProcessContainment(t *testing.T) {
	runner := &JavaVuser{cfg: configuration{Enabled: true}}
	result, err := runner.executePersistent(context.Background(), &protocol.Action{
		Name:   "Java action",
		Type:   "java_vuser_run",
		Target: "Actions",
		Params: map[string]any{"method": "action"},
	}, nil)
	if err != nil {
		t.Fatalf("executePersistent() error = %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "process_containment_unavailable") {
		t.Fatalf("executePersistent() = %#v, want value-free containment rejection", result)
	}
}
