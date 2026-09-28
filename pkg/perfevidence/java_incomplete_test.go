package perfevidence

import (
	"errors"
	"testing"
)

func TestJavaIncompleteReasonsPreserveRejection(t *testing.T) {
	b, c := javaEvidencePair()
	s := &c.Samples[0]
	s.incomplete("memory_pss_unavailable")
	s.incomplete("memory_pss_unavailable")
	if s.Complete || len(s.IncompleteReasons) != 1 || c.Validate() != nil {
		t.Fatal(s)
	}
	if got := EvaluateJavaGate(b, c); got.Status != "inconclusive" {
		t.Fatal(got)
	}
	s.Complete = true
	if c.Validate() == nil {
		t.Fatal("failure reason hidden by complete flag")
	}
	for _, reasons := range [][]string{{"unbounded raw error"}, {"caller_cancelled", "caller_cancelled"}} {
		s.Complete = false
		s.IncompleteReasons = reasons
		if c.Validate() == nil {
			t.Fatal("invalid reasons accepted")
		}
	}
}
func TestJavaMemoryFailureCodesDoNotDiscloseErrors(t *testing.T) {
	for _, tc := range []struct{ error, want string }{{"Java process PSS unavailable", "memory_pss_unavailable"}, {"cannot enumerate Java accounting cgroup", "memory_cgroup_unavailable"}, {"secret-bearing unexpected error", "memory_observation_failed"}} {
		if got := javaMemoryFailureCode(errors.New(tc.error)); got != tc.want {
			t.Fatal(got)
		}
	}
}
