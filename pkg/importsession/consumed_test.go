package importsession

import "testing"

type consumableScopeCheckingStore struct {
	*scopeCheckingStore
	marked []string
}

func (s *consumableScopeCheckingStore) MarkSessionClaimConsumed(id, projectID, actor, claimToken string) error {
	s.marked = append(s.marked, id, projectID, actor, claimToken)
	return nil
}

func TestManagerMarkConsumedRequiresACapableStoreAndAValidClaim(t *testing.T) {
	claim := Claim{Scope: Scope{ID: "session-1", Profile: "bruno", ProjectID: "project-1", Actor: "actor-1"}, Token: "token-1"}

	plain, err := NewManager(&scopeCheckingStore{})
	if err != nil {
		t.Fatal(err)
	}
	if err := plain.MarkConsumed(claim); err == nil {
		t.Fatal("a store without consumed tombstones reported success")
	}

	backend := &consumableScopeCheckingStore{scopeCheckingStore: &scopeCheckingStore{}}
	manager, err := NewManager(backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.MarkConsumed(Claim{Scope: claim.Scope}); err == nil {
		t.Fatal("a claim without its token was marked consumed")
	}
	if err := manager.MarkConsumed(claim); err != nil {
		t.Fatal(err)
	}
	want := []string{"session-1", "project-1", "actor-1", "token-1"}
	if len(backend.marked) != len(want) {
		t.Fatalf("marked = %v, want %v", backend.marked, want)
	}
	for index := range want {
		if backend.marked[index] != want[index] {
			t.Fatalf("marked = %v, want %v", backend.marked, want)
		}
	}
}
