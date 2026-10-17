package importreview

import (
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func memoryDraftFixture() *dsl.Script {
	return &dsl.Script{ID: "draft", Name: "import", Protocol: "http", ProjectID: "project-a", Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "sample", Preserved: map[string]any{}}}, Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/", HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer original-source-value"}}}}}
}
func TestMemoryDraftOwnerRotatesOneShotClaimAndPreservesExactCandidate(t *testing.T) {
	owner := NewMemoryDraftOwner()
	scope := DraftScope{ProjectID: "project-a", Actor: "operator"}
	draft := memoryDraftFixture()
	expires, projection, err := owner.Attach(draft, DraftAttachOptions{Scope: scope, DisableExpiration: true})
	if err != nil || !expires.IsZero() || projection == nil {
		t.Fatalf("attach = %v %#v %v", expires, projection, err)
	}
	token := ImportDraftAttestationToken(draft)
	submitted := CloneScriptDefinition(projection)
	EnsureScriptImportPreserved(submitted, "sample")[ImportDraftAttestationMetadataKey] = token
	claim, err := owner.Claim(scope, submitted, nil)
	if err != nil || claim == nil || claim.Script.Actions[0].HeaderList[0].Value != "Bearer original-source-value" {
		t.Fatalf("claim = %#v %v", claim, err)
	}
	if _, err := owner.Claim(scope, submitted, nil); !errors.Is(err, ErrDraftAttestationBusy) {
		t.Fatalf("duplicate claim = %v", err)
	}
	claim.Script.Name = "reviewed"
	expiry, err := owner.Rotate(scope, claim, claim.Script)
	if err != nil || !expiry.IsZero() {
		t.Fatalf("rotate = %v %v", expiry, err)
	}
	if ImportDraftAttestationToken(claim.Script) == token {
		t.Fatal("rotation reused capability")
	}
	if _, err := owner.Claim(scope, submitted, nil); !errors.Is(err, ErrDraftAttestationInvalid) {
		t.Fatalf("old capability = %v", err)
	}
	next, err := owner.Claim(scope, claim.Script, nil)
	if err != nil || !next.ExpirationDisabled {
		t.Fatalf("replacement = %#v %v", next, err)
	}
	owner.Complete(next.Token)
	if owner.Len() != 0 {
		t.Fatal("completed owner retained draft")
	}
}
func TestMemoryDraftOwnerScopeExpiryAndRotationFailure(t *testing.T) {
	now := time.Now()
	owner := NewMemoryDraftOwner(MemoryDraftOptions{Now: func() time.Time { return now }, Capacity: 1})
	scope := DraftScope{ProjectID: "project-a", Actor: "operator"}
	draft := memoryDraftFixture()
	if _, _, err := owner.Attach(draft, DraftAttachOptions{Scope: scope}); err != nil {
		t.Fatal(err)
	}
	for _, foreign := range []DraftScope{{ProjectID: "project-b", Actor: scope.Actor}, {ProjectID: scope.ProjectID, Actor: "other"}} {
		if _, err := owner.Claim(foreign, draft, nil); !errors.Is(err, ErrDraftAttestationInvalid) {
			t.Fatalf("foreign scope = %v", err)
		}
	}
	claim, err := owner.Claim(scope, draft, nil)
	if err != nil {
		t.Fatal(err)
	}
	original := CloneScriptDefinition(draft)
	if _, err := owner.Rotate(scope, claim, CloneScriptDefinition(draft)); err == nil {
		t.Fatal("rotation exceeded capacity")
	}
	owner.Release(claim.Token)
	if _, err := owner.Claim(scope, original, nil); err != nil {
		t.Fatalf("failed rotation stranded old capability: %v", err)
	}
	now = now.Add(ImportDraftAttestationTTL + time.Second)
	owner.PruneExpired()
	if owner.Len() != 0 {
		t.Fatal("expired claim was retained")
	}
	if _, err := owner.Claim(scope, original, nil); !errors.Is(err, ErrDraftAttestationInvalid) {
		t.Fatalf("expired claim = %v", err)
	}
}
func TestMemoryDraftOwnerRetainedClaimCannotBeReused(t *testing.T) {
	owner := NewMemoryDraftOwner()
	scope := DraftScope{ProjectID: "project-a"}
	draft := memoryDraftFixture()
	if _, _, err := owner.Attach(draft, DraftAttachOptions{Scope: scope}); err != nil {
		t.Fatal(err)
	}
	claim, err := owner.Claim(scope, draft, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner.RetainCommitted(claim.Token)
	owner.Release(claim.Token) // A deferred release cannot reopen committed ownership.
	if _, err := owner.Claim(scope, draft, nil); !errors.Is(err, ErrDraftAttestationInvalid) {
		t.Fatalf("retained claim = %v", err)
	}
	owner.Discard(claim.Token)
	if owner.Len() != 0 {
		t.Fatal("discard retained bytes")
	}
}
func TestMemoryDraftOwnerNoCapabilityNeededForOrdinaryDraft(t *testing.T) {
	owner := NewMemoryDraftOwner()
	draft := memoryDraftFixture()
	draft.Actions[0].HeaderList = nil
	expiry, _, err := owner.Attach(draft, DraftAttachOptions{Scope: DraftScope{ProjectID: draft.ProjectID}})
	if err != nil || !expiry.IsZero() || ImportDraftAttestationToken(draft) != "" || owner.Len() != 0 {
		t.Fatalf("ordinary attach = %v %v", expiry, err)
	}
}

func TestMemoryDraftOwnerConcurrentClaimHasOneWinnerUntilRelease(t *testing.T) {
	owner := NewMemoryDraftOwner()
	scope := DraftScope{ProjectID: "project-a"}
	draft := memoryDraftFixture()
	if _, _, err := owner.Attach(draft, DraftAttachOptions{Scope: scope}); err != nil {
		t.Fatal(err)
	}
	const attempts = 12
	start := make(chan struct{})
	results := make(chan error, attempts)
	for range attempts {
		go func() { <-start; _, err := owner.Claim(scope, draft, nil); results <- err }()
	}
	close(start)
	won := 0
	for range attempts {
		select {
		case err := <-results:
			if err == nil {
				won++
			} else if !errors.Is(err, ErrDraftAttestationBusy) {
				t.Fatalf("claim = %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("claim blocked")
		}
	}
	if won != 1 {
		t.Fatalf("claim winners = %d", won)
	}
	owner.Release(ImportDraftAttestationToken(draft))
	if _, err := owner.Claim(scope, draft, nil); err != nil {
		t.Fatalf("released claim = %v", err)
	}
}
func TestMemoryDraftOwnerJavaTransformFailureLeavesBaselineAndClaimReusable(t *testing.T) {
	for _, stage := range []string{"prepare", "verify"} {
		t.Run(stage, func(t *testing.T) {
			owner := NewMemoryDraftOwner()
			scope := DraftScope{ProjectID: "project-a"}
			draft := memoryDraftFixture()
			draft.Protocol = "java_vuser"
			if _, _, err := owner.Attach(draft, DraftAttachOptions{Scope: scope}); err != nil {
				t.Fatal(err)
			}
			want := errors.New("transform refused")
			refuse := func(baseline *dsl.Script) error {
				baseline.Actions[0].HeaderList[0].Value = "modified callback copy"
				return want
			}
			transform := &DraftClaimTransform{JavaReview: true}
			if stage == "prepare" {
				transform.Prepare = func(submitted, baseline *dsl.Script) (*dsl.Script, error) { return submitted, refuse(baseline) }
			} else {
				transform.Verify = func(_, baseline *dsl.Script) error { return refuse(baseline) }
			}
			if _, err := owner.Claim(scope, draft, transform); !errors.Is(err, want) {
				t.Fatalf("failed transform = %v", err)
			}
			claim, err := owner.Claim(scope, draft, nil)
			if err != nil || claim == nil || claim.Script.Actions[0].HeaderList[0].Value != "Bearer original-source-value" {
				t.Fatalf("retry changed baseline or retained claim: %#v %v", claim, err)
			}
		})
	}
}
func TestMemoryDraftOwnerJavaFinishRunsOnlyAfterFingerprintAcceptance(t *testing.T) {
	owner := NewMemoryDraftOwner()
	scope := DraftScope{ProjectID: "project-a"}
	draft := memoryDraftFixture()
	draft.Protocol = "java_vuser"
	if _, _, err := owner.Attach(draft, DraftAttachOptions{Scope: scope}); err != nil {
		t.Fatal(err)
	}
	finished := 0
	transform := &DraftClaimTransform{JavaReview: true, Finish: func(restored *dsl.Script) { finished++; restored.Name = "reviewed" }}
	changed := CloneScriptDefinition(draft)
	changed.Actions[0].HeaderList[0].Value = "Bearer changed-value"
	if _, err := owner.Claim(scope, changed, transform); !errors.Is(err, ErrDraftAttestationInvalid) || finished != 0 {
		t.Fatalf("rejected transform: finish=%d err=%v", finished, err)
	}
	claim, err := owner.Claim(scope, draft, transform)
	if err != nil || finished != 1 || !claim.JavaClasspathReviewChecked || claim.Script.Name != "reviewed" {
		t.Fatalf("accepted transform: finish=%d claim=%#v err=%v", finished, claim, err)
	}
}
