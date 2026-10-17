package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

type blockingObjectRepositoryContextCASStore struct {
	SQLiteStore *storage.SQLiteStore
	started     chan struct{}
	once        sync.Once
}

type objectRepositoryContextCASOnlyStore struct {
	storage.Store
}

func (s *objectRepositoryContextCASOnlyStore) UpdateObjectRepositoryIfUnchangedContext(ctx context.Context, repo *storage.ObjectRepository, expected time.Time) (bool, error) {
	return s.Store.(objectRepositoryContextCASPersistence).UpdateObjectRepositoryIfUnchangedContext(ctx, repo, expected)
}

func (s *blockingObjectRepositoryContextCASStore) UpdateObjectRepositoryIfUnchangedContext(ctx context.Context, _ *storage.ObjectRepository, _ time.Time) (bool, error) {
	s.signalStarted()
	<-ctx.Done()
	return false, ctx.Err()
}

func (s *blockingObjectRepositoryContextCASStore) UpdateObjectRepositoryIfUnchangedContextAndLeaseTermHeld(
	ctx context.Context,
	_ *storage.ObjectRepository,
	_ time.Time,
	_ string,
	_ string,
	_ uint64,
) (bool, error) {
	s.signalStarted()
	<-ctx.Done()
	return false, ctx.Err()
}

func (s *blockingObjectRepositoryContextCASStore) signalStarted() {
	s.once.Do(func() { close(s.started) })
}

func healEvent(actionName, healed, kind string, confidence int) engine.ReplayActionEvent {
	return engine.ReplayActionEvent{
		ActionName: actionName,
		ProtocolEvidence: map[string]any{
			"selector_self_heal": map[string]any{
				"action":          actionName,
				"healed_selector": healed,
				"candidate_kind":  kind,
				"confidence":      confidence,
				"reason":          "fallback matched",
			},
		},
	}
}

func healEventWithObjectRef(actionName, repoID, logicalName, healed, kind string, confidence int) engine.ReplayActionEvent {
	event := healEvent(actionName, healed, kind, confidence)
	fields := event.ProtocolEvidence["selector_self_heal"].(map[string]any)
	fields["object_ref_repository"] = repoID
	fields["object_ref_logical_name"] = logicalName
	return event
}

func resolvedObjectRefMetadata(repoID, logicalName string) map[string]any {
	return map[string]any{
		objectRefRepositoryMetadataKey:  repoID,
		objectRefLogicalNameMetadataKey: logicalName,
	}
}

func TestCollectSelfHeals(t *testing.T) {
	mismatchedAction := healEvent("EvidenceAction", "#forged", "css", 99)
	mismatchedAction.ActionName = "EventAction"
	missingEventAction := healEvent("NestedOnly", "#forged", "css", 99)
	missingEventAction.ActionName = ""
	report := &engine.ReplayReport{Events: []engine.ReplayActionEvent{
		healEvent("Login", "#healed", "css", 80),
		mismatchedAction,
		missingEventAction,
		{ActionName: "NoHeal"}, // no evidence → skipped
	}}
	heals := collectSelfHeals(report)
	if len(heals) != 1 {
		t.Fatalf("heals = %d, want 1", len(heals))
	}
	if heals[0].actionName != "Login" || heals[0].healed != "#healed" || heals[0].kind != "css" || heals[0].confidence != 80 {
		t.Fatalf("unexpected heal record: %#v", heals[0])
	}
}

func TestPersistSelfHealToRepositoryRejectsForgedNestedAction(t *testing.T) {
	server := recordingTestServer(t)
	now := time.Now().UTC()
	if err := server.store.SaveProject(&storage.Project{ID: "project-a", Name: "project-a", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	firstRepo := &storage.ObjectRepository{
		ID:        "object-repo-first-action",
		ProjectID: "project-a",
		Name:      "First",
		Objects: []storage.RepositoryObject{{
			LogicalName: "FirstButton",
			Candidates:  []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#first", Confidence: 70}},
		}},
	}
	secondRepo := &storage.ObjectRepository{
		ID:        "object-repo-second-action",
		ProjectID: "project-a",
		Name:      "Second",
		Objects: []storage.RepositoryObject{{
			LogicalName: "SecondButton",
			Candidates:  []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#second", Confidence: 70}},
		}},
	}
	for _, repo := range []*storage.ObjectRepository{firstRepo, secondRepo} {
		if err := server.store.SaveObjectRepository(repo); err != nil {
			t.Fatalf("save repository %q: %v", repo.ID, err)
		}
	}
	script := &dsl.Script{ProjectID: "project-a", Actions: []dsl.Action{
		{
			Name:      "FirstAction",
			Type:      "browser_click",
			ObjectRef: firstRepo.ID + ":FirstButton",
			Metadata:  resolvedObjectRefMetadata(firstRepo.ID, "FirstButton"),
		},
		{
			Name:      "SecondAction",
			Type:      "browser_click",
			ObjectRef: secondRepo.ID + ":SecondButton",
			Metadata:  resolvedObjectRefMetadata(secondRepo.ID, "SecondButton"),
		},
	}}

	forged := healEventWithObjectRef("SecondAction", secondRepo.ID, "SecondButton", "#forged-second-heal", "css", 99)
	forged.ActionName = "FirstAction"
	server.persistSelfHealToRepository(
		context.Background(),
		"project-a",
		script,
		&engine.ReplayReport{Events: []engine.ReplayActionEvent{forged}},
	)

	for _, repoID := range []string{firstRepo.ID, secondRepo.ID} {
		unchanged, err := server.store.GetObjectRepository(repoID)
		if err != nil {
			t.Fatalf("get repository %q after forged evidence: %v", repoID, err)
		}
		if len(unchanged.Objects[0].Candidates) != 1 {
			t.Fatalf("forged nested action mutated repository %q: %#v", repoID, unchanged.Objects[0].Candidates)
		}
	}

	valid := healEventWithObjectRef("FirstAction", firstRepo.ID, "FirstButton", "#valid-first-heal", "css", 85)
	server.persistSelfHealToRepository(
		context.Background(),
		"project-a",
		script,
		&engine.ReplayReport{Events: []engine.ReplayActionEvent{valid}},
	)
	updatedFirst, err := server.store.GetObjectRepository(firstRepo.ID)
	if err != nil {
		t.Fatalf("get first repository after valid evidence: %v", err)
	}
	if len(updatedFirst.Objects[0].Candidates) != 2 || updatedFirst.Objects[0].Candidates[1].Value != "#valid-first-heal" {
		t.Fatalf("valid event did not update its bound repository: %#v", updatedFirst.Objects[0].Candidates)
	}
	unchangedSecond, err := server.store.GetObjectRepository(secondRepo.ID)
	if err != nil {
		t.Fatalf("get second repository after valid evidence: %v", err)
	}
	if len(unchangedSecond.Objects[0].Candidates) != 1 {
		t.Fatalf("valid first-action event mutated second repository: %#v", unchangedSecond.Objects[0].Candidates)
	}
}

func TestObjectRefBindingsByActionName(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{
		{Name: "Login", Type: "browser_click", ObjectRef: "object-repo-a:login",
			Metadata: resolvedObjectRefMetadata("object-repo-a", "login")},
		{Name: "Plain", Type: "browser_click", Selector: "#x"}, // unbound
	}}
	bindings := objectRefBindingsByActionName(script)
	if len(bindings) != 1 {
		t.Fatalf("bindings = %d, want 1", len(bindings))
	}
	b := bindings["Login"]
	if b.logicalName != "login" || b.repoID != "object-repo-a" {
		t.Fatalf("binding = %#v", b)
	}
}

func TestObjectRefBindingForActionRequiresCompleteResolverAttestation(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
	}{
		{name: "no metadata"},
		{name: "repository stamp only", metadata: map[string]any{objectRefRepositoryMetadataKey: "object-repo-a"}},
		{name: "logical stamp only", metadata: map[string]any{objectRefLogicalNameMetadataKey: "login"}},
		{name: "wrong repository", metadata: resolvedObjectRefMetadata("object-repo-b", "login")},
		{name: "wrong logical name", metadata: resolvedObjectRefMetadata("object-repo-a", "logout")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action := &dsl.Action{
				Name:      "Login",
				Type:      "browser_click",
				ObjectRef: "object-repo-a:login",
				Metadata:  tc.metadata,
			}
			if binding, ok := objectRefBindingForAction(action); ok {
				t.Fatalf("unattested action produced binding %#v", binding)
			}
		})
	}
}

func TestObjectRefBindingsByActionNameDropsAmbiguousNames(t *testing.T) {
	// Two object_ref-bound actions share a name but point at DIFFERENT repository
	// objects: the binding must be dropped so a heal under that name is not routed
	// to the wrong object.
	script := &dsl.Script{Actions: []dsl.Action{
		{Name: "Go", Type: "browser_click", ObjectRef: "object-repo-a:go",
			Metadata: resolvedObjectRefMetadata("object-repo-a", "go")},
		{Name: "Go", Type: "browser_click", ObjectRef: "object-repo-b:go",
			Metadata: resolvedObjectRefMetadata("object-repo-b", "go")},
	}}
	if bindings := objectRefBindingsByActionName(script); len(bindings) != 0 {
		t.Fatalf("ambiguous same-name cross-repo bindings = %#v, want dropped (0)", bindings)
	}

	// Two same-named actions with the SAME binding are not ambiguous (a no-op
	// last-write); the binding is retained.
	same := &dsl.Script{Actions: []dsl.Action{
		{Name: "Submit", Type: "browser_click", ObjectRef: "object-repo-a:submit",
			Metadata: resolvedObjectRefMetadata("object-repo-a", "submit")},
		{Name: "Submit", Type: "browser_click", ObjectRef: "object-repo-a:submit",
			Metadata: resolvedObjectRefMetadata("object-repo-a", "submit")},
	}}
	if bindings := objectRefBindingsByActionName(same); len(bindings) != 1 {
		t.Fatalf("identical same-name bindings = %#v, want retained (1)", bindings)
	}

	// A same-named unbound action makes name-only replay evidence ambiguous even
	// when the other action has a valid repository binding.
	boundAndUnbound := &dsl.Script{Actions: []dsl.Action{
		{Name: "Submit", Type: "browser_click", ObjectRef: "object-repo-a:submit",
			Metadata: resolvedObjectRefMetadata("object-repo-a", "submit")},
		{Name: "Submit", Type: "browser_click", Selector: "#standalone-submit"},
	}}
	if bindings := objectRefBindingsByActionName(boundAndUnbound); len(bindings) != 0 {
		t.Fatalf("bound/unbound same-name actions = %#v, want dropped (0)", bindings)
	}
}

func TestApplyHealToRepositoryObject(t *testing.T) {
	t.Run("appends a drifted locator", func(t *testing.T) {
		repo := &storage.ObjectRepository{Objects: []storage.RepositoryObject{{
			LogicalName: "login", BestConfidence: 70,
			Candidates: []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#a", Confidence: 70}},
		}}}
		changed := applyHealToRepositoryObject(repo, "login", selfHealRecord{healed: "#b", kind: "css", confidence: 80}, false)
		if !changed {
			t.Fatal("expected changed=true")
		}
		if len(repo.Objects[0].Candidates) != 2 {
			t.Fatalf("candidates = %d, want 2", len(repo.Objects[0].Candidates))
		}
		if repo.Objects[0].BestConfidence != 80 {
			t.Fatalf("best confidence = %d, want 80", repo.Objects[0].BestConfidence)
		}
	})

	t.Run("raises confidence of an existing candidate", func(t *testing.T) {
		repo := &storage.ObjectRepository{Objects: []storage.RepositoryObject{{
			LogicalName: "login", BestConfidence: 70,
			Candidates: []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#a", Confidence: 70}},
		}}}
		changed := applyHealToRepositoryObject(repo, "login", selfHealRecord{healed: "#a", kind: "css", confidence: 88}, false)
		if !changed {
			t.Fatal("expected changed=true")
		}
		if len(repo.Objects[0].Candidates) != 1 || repo.Objects[0].Candidates[0].Confidence != 88 {
			t.Fatalf("candidate not strengthened: %#v", repo.Objects[0].Candidates)
		}
	})

	t.Run("does not lower confidence", func(t *testing.T) {
		repo := &storage.ObjectRepository{Objects: []storage.RepositoryObject{{
			LogicalName: "login",
			Candidates:  []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#a", Confidence: 70}},
		}}}
		changed := applyHealToRepositoryObject(repo, "login", selfHealRecord{healed: "#a", kind: "css", confidence: 50}, false)
		if changed {
			t.Fatal("expected changed=false for a weaker observation")
		}
		if repo.Objects[0].Candidates[0].Confidence != 70 {
			t.Fatalf("confidence lowered to %d", repo.Objects[0].Candidates[0].Confidence)
		}
	})

	t.Run("matches an xpath candidate by engine form", func(t *testing.T) {
		repo := &storage.ObjectRepository{Objects: []storage.RepositoryObject{{
			LogicalName: "login",
			Candidates:  []storage.RepositoryObjectCandidate{{Kind: "xpath", Value: "//a", Confidence: 58}},
		}}}
		// self-heal records the engine form ("xpath=//a"), not the raw value.
		changed := applyHealToRepositoryObject(repo, "login", selfHealRecord{healed: "xpath=//a", kind: "xpath", confidence: 58}, false)
		if changed {
			t.Fatal("engine-form match should not append a duplicate")
		}
		if len(repo.Objects[0].Candidates) != 1 {
			t.Fatalf("candidates = %d, want 1 (no duplicate)", len(repo.Objects[0].Candidates))
		}
	})

	t.Run("autoPromote pushes the healed candidate above the primary", func(t *testing.T) {
		repo := &storage.ObjectRepository{Objects: []storage.RepositoryObject{{
			LogicalName: "login", BestConfidence: 88,
			Candidates: []storage.RepositoryObjectCandidate{
				{Kind: "css", Value: "#login", Confidence: 88},
				{Kind: "xpath", Value: "//a", Confidence: 58},
			},
		}}}
		// The healed xpath (engine form) starts below the css primary; with
		// autoPromote it should become strictly the highest-confidence candidate.
		changed := applyHealToRepositoryObject(repo, "login", selfHealRecord{healed: "xpath=//a", kind: "xpath", confidence: 58}, true)
		if !changed {
			t.Fatal("expected changed=true")
		}
		healed := repo.Objects[0].Candidates[1]
		if healed.Confidence != 89 || repo.Objects[0].BestConfidence != 89 {
			t.Fatalf("healed not promoted: confidence=%d best=%d", healed.Confidence, repo.Objects[0].BestConfidence)
		}
		if healed.Reason != "promoted by replay self-heal" {
			t.Fatalf("reason = %q", healed.Reason)
		}
	})
}

func TestAutoPromotedRoleHealBecomesPrimary(t *testing.T) {
	repo := &storage.ObjectRepository{Objects: []storage.RepositoryObject{{
		LogicalName: "login",
		Candidates: []storage.RepositoryObjectCandidate{
			{Kind: "css", Value: "#login", Confidence: 88},
		},
	}}}
	// Baseline: the css candidate is the resolved primary.
	if before := primarySelectorFromCandidates(candidatesForRepositoryObject(repo.Objects[0])); before != "#login" {
		t.Fatalf("baseline primary = %q, want #login", before)
	}

	// Auto-promote a role heal. PlaywrightSelector ignores role candidates, so
	// before the fix the promoted role would never become primary.
	roleValue := `role=button[name="Log in"]`
	if !applyHealToRepositoryObject(repo, "login", selfHealRecord{healed: roleValue, kind: "role", confidence: 70}, true) {
		t.Fatal("expected the heal to change the repository")
	}
	after := primarySelectorFromCandidates(candidatesForRepositoryObject(repo.Objects[0]))
	if after != roleValue {
		t.Fatalf("promoted primary = %q, want the role heal %q", after, roleValue)
	}
}

func TestPersistSelfHealToRepository_Integration(t *testing.T) {
	server := recordingTestServer(t)
	repo := &storage.ObjectRepository{
		ID:        "object-repo-int",
		ProjectID: "unassigned",
		Name:      "Checkout",
		Objects: []storage.RepositoryObject{{
			LogicalName:    "Login",
			BestConfidence: 70,
			Candidates:     []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#login", Confidence: 70}},
		}},
	}
	if err := server.store.SaveObjectRepository(repo); err != nil {
		t.Fatalf("save repo: %v", err)
	}

	script := &dsl.Script{ProjectID: "unassigned", Protocol: "browser", Actions: []dsl.Action{{
		Name: "Login", Type: "browser_click", ObjectRef: "object-repo-int:Login",
		Metadata: resolvedObjectRefMetadata("object-repo-int", "Login"),
	}}}
	report := &engine.ReplayReport{Events: []engine.ReplayActionEvent{
		healEventWithObjectRef("Login", "object-repo-int", "Login", "#login-healed", "css", 80),
	}}

	staleCtx, cancelStale := context.WithCancel(context.Background())
	cancelStale()
	server.persistSelfHealToRepository(staleCtx, "unassigned", script, report)
	unchanged, err := server.store.GetObjectRepository("object-repo-int")
	if err != nil {
		t.Fatalf("get repo after stale completion: %v", err)
	}
	if len(unchanged.Objects[0].Candidates) != 1 {
		t.Fatalf("lost-epoch self-heal mutated repository: %#v", unchanged.Objects[0].Candidates)
	}

	server.persistSelfHealToRepository(context.Background(), "unassigned", script, report)

	updated, err := server.store.GetObjectRepository("object-repo-int")
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	found := false
	for _, c := range updated.Objects[0].Candidates {
		if c.Value == "#login-healed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("healed candidate not written back: %#v", updated.Objects[0].Candidates)
	}
}

func TestPersistSelfHealToRepositoryFencesDurableExecutionLeaseTerm(t *testing.T) {
	server := recordingTestServer(t)
	leaseStore, ok := server.store.(*storage.SQLiteStore)
	if !ok {
		t.Fatalf("recording server store = %T, want *storage.SQLiteStore", server.store)
	}
	leadership := newExecutionLeadership(leaseStore, "self-heal-fence")
	server.leadership = leadership
	server.runner.leadership = leadership
	server.runner.durableExecutionFence = sameStoreInstance(leadership.store, server.runner.testStore)
	if !server.runner.durableExecutionFence {
		t.Fatal("test server did not enable same-store durable execution fencing")
	}
	leadership.tick()
	defer leadership.relinquish()

	executionCtx, releaseExecutionCtx := server.runner.executionOperationContext(context.Background())
	defer releaseExecutionCtx()
	term, ok := executionLeaseTermFromContext(executionCtx)
	if !ok {
		t.Fatal("execution context did not retain its immutable durable lease term")
	}
	completionCtx, cancelCompletion := context.WithCancel(executionCtx)
	defer cancelCompletion()
	childTerm, ok := executionLeaseTermFromContext(completionCtx)
	if !ok || childTerm != term {
		t.Fatalf("child replay context term = %#v, want immutable parent term %#v", childTerm, term)
	}
	repo := &storage.ObjectRepository{
		ID:        "object-repo-ha-self-heal",
		ProjectID: "p1",
		Name:      "HA",
		Objects: []storage.RepositoryObject{{
			LogicalName: "Login",
			Candidates:  []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#login", Confidence: 70}},
		}},
	}
	if err := server.store.SaveObjectRepository(repo); err != nil {
		t.Fatalf("save repository: %v", err)
	}
	script := &dsl.Script{ProjectID: "p1", Actions: []dsl.Action{{
		Name:      "Login",
		Type:      "browser_click",
		ObjectRef: repo.ID + ":Login",
		Metadata:  resolvedObjectRefMetadata(repo.ID, "Login"),
	}}}
	missingTermReport := &engine.ReplayReport{Events: []engine.ReplayActionEvent{
		healEventWithObjectRef("Login", repo.ID, "Login", "#missing-term-heal", "css", 80),
	}}
	server.persistSelfHealToRepository(context.Background(), "p1", script, missingTermReport)
	beforeCurrentTerm, err := server.store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatalf("get repository after missing-term heal: %v", err)
	}
	if len(beforeCurrentTerm.Objects[0].Candidates) != 1 {
		t.Fatalf("HA self-heal without a durable term was not fenced: %#v", beforeCurrentTerm.Objects[0].Candidates)
	}
	currentReport := &engine.ReplayReport{Events: []engine.ReplayActionEvent{
		healEventWithObjectRef("Login", repo.ID, "Login", "#current-heal", "css", 80),
	}}
	server.persistSelfHealToRepository(completionCtx, "p1", script, currentReport)
	current, err := server.store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatalf("get repository after current-term heal: %v", err)
	}
	if len(current.Objects[0].Candidates) != 2 {
		t.Fatalf("current durable term did not persist heal: %#v", current.Objects[0].Candidates)
	}

	if err := leaseStore.ReleaseSchedulerLease(executionLeaseName, term.ownerID); err != nil {
		t.Fatalf("release original execution term: %v", err)
	}
	acquired, successorTerm, err := leaseStore.TryAcquireSchedulerLeaseTerm(
		executionLeaseName,
		"successor-controller/1",
		time.Time{},
		time.Minute,
	)
	if err != nil || !acquired || successorTerm == nil || successorTerm.Generation == term.generation {
		t.Fatalf("acquire successor term = (%v, %#v, %v), original generation %d", acquired, successorTerm, err, term.generation)
	}
	if err := completionCtx.Err(); err != nil {
		t.Fatalf("old process-local execution context unexpectedly canceled: %v", err)
	}

	staleReport := &engine.ReplayReport{Events: []engine.ReplayActionEvent{
		healEventWithObjectRef("Login", repo.ID, "Login", "#stale-heal", "css", 99),
	}}
	server.persistSelfHealToRepository(completionCtx, "p1", script, staleReport)
	fenced, err := server.store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatalf("get repository after stale-term heal: %v", err)
	}
	if len(fenced.Objects[0].Candidates) != 2 {
		t.Fatalf("replaced durable term persisted stale heal: %#v", fenced.Objects[0].Candidates)
	}
	for _, candidate := range fenced.Objects[0].Candidates {
		if candidate.Value == "#stale-heal" {
			t.Fatalf("stale heal crossed the durable lease fence: %#v", fenced.Objects[0].Candidates)
		}
	}
}

func TestPersistSelfHealToRepositoryFailsClosedWithoutLeaseTermCAS(t *testing.T) {
	server := recordingTestServer(t)
	repo := &storage.ObjectRepository{
		ID:        "object-repo-ha-no-fenced-cas",
		ProjectID: "p1",
		Name:      "HA",
		Objects: []storage.RepositoryObject{{
			LogicalName: "Login",
			Candidates:  []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#login", Confidence: 70}},
		}},
	}
	if err := server.store.SaveObjectRepository(repo); err != nil {
		t.Fatalf("save repository: %v", err)
	}
	wrappedStore := &objectRepositoryContextCASOnlyStore{Store: server.store}
	leadership := newExecutionLeadership(wrappedStore, "missing-fenced-cas")
	server.store = wrappedStore
	server.runner.testStore = wrappedStore
	server.runner.leadership = leadership
	server.runner.durableExecutionFence = true
	server.haExecution = true
	script := &dsl.Script{ProjectID: "p1", Actions: []dsl.Action{{
		Name:      "Login",
		Type:      "browser_click",
		ObjectRef: repo.ID + ":Login",
		Metadata:  resolvedObjectRefMetadata(repo.ID, "Login"),
	}}}
	report := &engine.ReplayReport{Events: []engine.ReplayActionEvent{
		healEventWithObjectRef("Login", repo.ID, "Login", "#unfenced-heal", "css", 99),
	}}
	ctx := context.WithValue(context.Background(), executionLeaseTermContextKey{}, executionLeaseTerm{
		ownerID:    "missing-fenced-cas/1",
		generation: 1,
	})

	server.persistSelfHealToRepository(ctx, "p1", script, report)

	unchanged, err := server.store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatalf("get repository: %v", err)
	}
	if len(unchanged.Objects[0].Candidates) != 1 {
		t.Fatalf("HA store without a lease-term CAS persisted self-heal: %#v", unchanged.Objects[0].Candidates)
	}
}

func TestPersistSelfHealToRepositoryRejectsForgedEvidenceBinding(t *testing.T) {
	server := recordingTestServer(t)
	now := time.Now().UTC()
	for _, projectID := range []string{"project-a", "project-b"} {
		if err := server.store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("save project %q: %v", projectID, err)
		}
	}
	boundRepo := &storage.ObjectRepository{
		ID:        "object-repo-bound",
		ProjectID: "project-a",
		Name:      "Bound",
		Objects: []storage.RepositoryObject{{
			LogicalName: "Login",
			Candidates:  []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#login", Confidence: 70}},
		}},
	}
	foreignRepo := &storage.ObjectRepository{
		ID:        "object-repo-foreign",
		ProjectID: "project-b",
		Name:      "Foreign",
		Objects: []storage.RepositoryObject{{
			LogicalName: "Admin",
			Candidates:  []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#admin", Confidence: 70}},
		}},
	}
	for _, repo := range []*storage.ObjectRepository{boundRepo, foreignRepo} {
		if err := server.store.SaveObjectRepository(repo); err != nil {
			t.Fatalf("save repository %q: %v", repo.ID, err)
		}
	}
	script := &dsl.Script{ProjectID: "project-a", Actions: []dsl.Action{{
		Name:      "Login",
		Type:      "browser_click",
		ObjectRef: "object-repo-bound:Login",
		Metadata:  resolvedObjectRefMetadata("object-repo-bound", "Login"),
	}}}
	report := &engine.ReplayReport{Events: []engine.ReplayActionEvent{
		healEventWithObjectRef("Login", "object-repo-foreign", "Admin", "#forged-heal", "css", 99),
	}}

	server.persistSelfHealToRepository(context.Background(), "project-a", script, report)

	for _, repoID := range []string{boundRepo.ID, foreignRepo.ID} {
		unchanged, err := server.store.GetObjectRepository(repoID)
		if err != nil {
			t.Fatalf("get repository %q: %v", repoID, err)
		}
		if len(unchanged.Objects[0].Candidates) != 1 {
			t.Fatalf("forged evidence mutated repository %q: %#v", repoID, unchanged.Objects[0].Candidates)
		}
	}
}

func TestPersistSelfHealToRepositoryRequiresMatchingProjectScope(t *testing.T) {
	tests := []struct {
		name          string
		repoProject   string
		hookProject   string
		scriptProject string
	}{
		{
			name:          "hook differs from script and repository",
			repoProject:   "project-a",
			hookProject:   "project-b",
			scriptProject: "project-a",
		},
		{
			name:          "script differs from hook and repository",
			repoProject:   "project-a",
			hookProject:   "project-a",
			scriptProject: "project-b",
		},
		{
			name:          "repository differs from hook and script",
			repoProject:   "project-b",
			hookProject:   "project-a",
			scriptProject: "project-a",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := recordingTestServer(t)
			now := time.Now().UTC()
			for _, projectID := range []string{"project-a", "project-b"} {
				if err := server.store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatalf("save project %q: %v", projectID, err)
				}
			}
			repoID := "object-repo-project-scope"
			repo := &storage.ObjectRepository{
				ID:        repoID,
				ProjectID: tc.repoProject,
				Name:      "Scoped",
				Objects: []storage.RepositoryObject{{
					LogicalName: "Login",
					Candidates:  []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#login", Confidence: 70}},
				}},
			}
			if err := server.store.SaveObjectRepository(repo); err != nil {
				t.Fatalf("save repository: %v", err)
			}
			script := &dsl.Script{ProjectID: tc.scriptProject, Actions: []dsl.Action{{
				Name:      "Login",
				Type:      "browser_click",
				ObjectRef: repoID + ":Login",
				Metadata:  resolvedObjectRefMetadata(repoID, "Login"),
			}}}
			report := &engine.ReplayReport{Events: []engine.ReplayActionEvent{
				healEventWithObjectRef("Login", repoID, "Login", "#project-escape", "css", 99),
			}}

			server.persistSelfHealToRepository(context.Background(), tc.hookProject, script, report)

			unchanged, err := server.store.GetObjectRepository(repoID)
			if err != nil {
				t.Fatalf("get repository: %v", err)
			}
			if len(unchanged.Objects[0].Candidates) != 1 {
				t.Fatalf("project mismatch mutated repository: %#v", unchanged.Objects[0].Candidates)
			}
		})
	}
}

func TestPersistSelfHealToRepositoryCancelsInFlightCASWhenExecutionEpochEnds(t *testing.T) {
	server := recordingTestServer(t)
	repo := &storage.ObjectRepository{
		ID:        "object-repo-context-cas",
		ProjectID: "unassigned",
		Name:      "Checkout",
		Objects: []storage.RepositoryObject{{
			LogicalName: "Login",
			Candidates:  []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#login", Confidence: 70}},
		}},
	}
	if err := server.store.SaveObjectRepository(repo); err != nil {
		t.Fatalf("save repo: %v", err)
	}
	blockingStore := &blockingObjectRepositoryContextCASStore{
		SQLiteStore: server.store.(*storage.SQLiteStore),
		started:     make(chan struct{}),
	}
	server.store = sqliteOverlay(blockingStore)
	script := &dsl.Script{ProjectID: "unassigned", Protocol: "browser", Actions: []dsl.Action{{
		Name: "Login", Type: "browser_click", ObjectRef: "object-repo-context-cas:Login",
		Metadata: resolvedObjectRefMetadata("object-repo-context-cas", "Login"),
	}}}
	report := &engine.ReplayReport{Events: []engine.ReplayActionEvent{
		healEvent("Login", "#login-healed", "css", 80),
	}}

	epochContext, cancelEpoch := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		server.persistSelfHealToRepository(epochContext, "unassigned", script, report)
		close(done)
	}()
	select {
	case <-blockingStore.started:
	case <-time.After(time.Second):
		t.Fatal("self-heal never reached the context-aware CAS boundary")
	}
	cancelEpoch()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("self-heal did not stop after its execution epoch ended")
	}

	unchanged, err := server.store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatalf("get repo after canceled CAS: %v", err)
	}
	if len(unchanged.Objects[0].Candidates) != 1 {
		t.Fatalf("canceled in-flight self-heal mutated repository: %#v", unchanged.Objects[0].Candidates)
	}
}

func TestPersistSelfHealToRepository_IgnoresUnboundActions(t *testing.T) {
	server := recordingTestServer(t)
	repo := &storage.ObjectRepository{
		ID: "object-repo-u", ProjectID: "unassigned", Name: "x",
		Objects: []storage.RepositoryObject{{LogicalName: "Login", Candidates: []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#login", Confidence: 70}}}},
	}
	if err := server.store.SaveObjectRepository(repo); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Action heals but is NOT bound to a repository (no object_ref).
	script := &dsl.Script{ProjectID: "unassigned", Actions: []dsl.Action{{Name: "Login", Type: "browser_click", Selector: "#login"}}}
	report := &engine.ReplayReport{Events: []engine.ReplayActionEvent{healEvent("Login", "#login-healed", "css", 80)}}

	server.persistSelfHealToRepository(context.Background(), "unassigned", script, report)

	updated, _ := server.store.GetObjectRepository("object-repo-u")
	if len(updated.Objects[0].Candidates) != 1 {
		t.Fatalf("unbound heal must not touch the repository, candidates = %d", len(updated.Objects[0].Candidates))
	}
}

func TestPersistSelfHealToRepository_IgnoresAmbiguousBoundAndUnboundSameName(t *testing.T) {
	server := recordingTestServer(t)
	repo := &storage.ObjectRepository{
		ID: "object-repo-ambiguous-name", ProjectID: "unassigned", Name: "x",
		Objects: []storage.RepositoryObject{{LogicalName: "Login", Candidates: []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#login", Confidence: 70}}}},
	}
	if err := server.store.SaveObjectRepository(repo); err != nil {
		t.Fatalf("save: %v", err)
	}
	script := &dsl.Script{ProjectID: "unassigned", Actions: []dsl.Action{
		{
			Name:      "Login",
			Type:      "browser_click",
			ObjectRef: repo.ID + ":Login",
			Metadata:  resolvedObjectRefMetadata(repo.ID, "Login"),
		},
		{Name: "Login", Type: "browser_click", Selector: "#standalone-login"},
	}}
	report := &engine.ReplayReport{Events: []engine.ReplayActionEvent{
		healEventWithObjectRef("Login", repo.ID, "Login", "#forged-heal", "css", 99),
	}}

	server.persistSelfHealToRepository(context.Background(), "unassigned", script, report)

	unchanged, err := server.store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatalf("get repository: %v", err)
	}
	if len(unchanged.Objects[0].Candidates) != 1 {
		t.Fatalf("ambiguous same-name heal mutated repository: %#v", unchanged.Objects[0].Candidates)
	}
}
