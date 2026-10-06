package api

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// testStoreWriteCallSitesOffLock lists the tr.testStore write call sites that are
// deliberately NOT published to the panic backstop's in-flight gauge, each with
// the reason it cannot be running with tr.mu write-locked. Everything else must
// go through trackDurableWrite.
//
// tr.mu is a sync.RWMutex and is not reentrant, so "the function takes tr.mu
// itself" is a proof that no caller can be holding the write lock across it.
//
// Keys are "<function>/<StoreMethod>".
var testStoreWriteCallSitesOffLock = map[string]string{
	"validatePreparedMonitorReadiness/AppDynamicsCollectorPublicKey":                                    "read-only enrolled-key lookup; this function takes and releases tr.mu.RLock before storage access, so no caller can hold tr.mu write-locked",
	"saveManagedTestRecordSnapshot/SaveTestWithRunMonitors":                                             "same snapshot path: callers wrap the whole projection and atomic monitor write in trackDurableWrite",
	"saveManagedTestStartSnapshot/SaveTest":                                                             "persistManagedTestStartOutsideLockLocked releases tr.mu around this call (testexecution.go)",
	"saveManagedTestStartSnapshot/SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease":                "same start path: tr.mu is released across the durable start transition",
	"saveManagedTestStartSnapshot/SaveTestStartIfNoDeploymentLease":                                     "same start path: tr.mu is released across the durable start transition",
	"failExecutionPlanStagedTestWithTerm/UpdateTestStatusIfActiveAndMaterializationKeyAndLeaseTermHeld": "the function takes tr.mu.RLock itself, so no caller can hold the write lock",
	"failExecutionPlanStagedTestWithTerm/UpdateTestStatusIfActiveAndMaterializationKey":                 "the function takes tr.mu.RLock itself, so no caller can hold the write lock",
	"captureImportedDashboardSnapshotAttempt/SaveGrafanaDashboardSnapshot":                              "runs on the archive-finalization outbox worker under a context deadline, never under tr.mu",
	"persistManagedTestRuntimeRepair/RepairTestRuntimeSnapshotIfLeaseHeld":                              "runtime hydration; every caller runs it outside tr.mu (see the comment in bindCapacityTimeslot)",
	"persistManagedTestRuntimeRepair/RepairTestRuntimeSnapshot":                                         "runtime hydration; every caller runs it outside tr.mu",
	"saveManagedTestRecordSnapshot/SaveTestIfLeaseHeld":                                                 "only reached through saveManagedTestRecord, which wraps the whole projection-and-write in trackDurableWrite",
	"saveManagedTestRecordSnapshot/SaveTest":                                                            "only reached through saveManagedTestRecord, which wraps the whole projection-and-write in trackDurableWrite",
	"persistManagedTestShakeoutTransition/TransitionTestFromShakeout":                                   "the shakeout callback releases tr.mu across this write (testexecution.go)",
	"persistInitialArchiveProvenance/SaveTestInitialProvenanceIfLeaseHeld":                              "the function releases tr.mu before issuing the write",
	"persistInitialArchiveProvenance/SaveTestInitialProvenance":                                         "the function releases tr.mu before issuing the write",
	"persistStoredTestStatus/UpdateTestStatusIfActiveAndLeaseHeld":                                      "persistStoredTestStatus increments durableWritesInFlight directly for its whole body",
	"persistStoredTestStatus/UpdateTestStatusIfActive":                                                  "persistStoredTestStatus increments durableWritesInFlight directly for its whole body",
	"persistStoredTestStatus/UpdateTestStatus":                                                          "persistStoredTestStatus increments durableWritesInFlight directly for its whole body",
	"markTestPendingLockedWithExpectedTerm/TransitionTestToPending":                                     "explicitly unlocks tr.mu around the transition (\"storage latency must not hold tr.mu\")",
	"saveNewManagedTestRecordWithTerm/CreateTestIfAbsentIfLeaseTermHeld":                                "creation path; both callers release tr.mu before persisting",
	"saveNewManagedTestRecordWithTerm/CreateTestIfAbsent":                                               "creation path; both callers release tr.mu before persisting",
	"saveNewManagedTestRecordWithTerm/SaveTest":                                                         "creation path; the compatibility branch serializes on compatCreateMu, not tr.mu",
	"saveNewManagedTestRecordWithTerm/CreateTestAndBindCapacityReservationIfLeaseTermHeld":              "creation path; both callers release tr.mu before persisting",
	"saveNewManagedTestRecordWithTerm/CreateTestAndBindCapacityReservation":                             "creation path; both callers release tr.mu before persisting",
}

// storeReadMethodPrefixes are the accessor prefixes treated as reads. Anything
// else on the test store counts as a durable write, so a new write cannot slip
// through by not being on a list of known write names.
var storeReadMethodPrefixes = []string{
	"Get", "List", "Latest", "Count", "Load", "Find", "Has", "Exists",
	"Lookup", "Read", "Fetch", "Query", "Select", "Stream", "Resolve",
}

type testStoreWriteCallSite struct {
	function string
	method   string
	position string
}

// TestEveryTestStoreWriteIsCountedOrDocumented enumerates the tr.testStore write
// call sites in this package instead of trusting the prose invariant stated on
// trackDurableWrite and durableWritesInFlight.
//
// That invariant is load-bearing for two independent mechanisms - the run-driver
// panic backstop's orphaned-lock classification and the liveness probe's cached
// answer - and it has now been broken twice by adding a store write beside an
// existing one without noticing that the caller holds tr.mu. A comment cannot
// catch the third time; this can.
func TestEveryTestStoreWriteIsCountedOrDocumented(t *testing.T) {
	fset := token.NewFileSet()
	files := parsePackageSourceFiles(t, fset)

	var uncounted []testStoreWriteCallSite
	seenAllowlistKeys := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			for _, site := range uncountedTestStoreWrites(fn) {
				key := fn.Name.Name + "/" + site.method
				if _, allowed := testStoreWriteCallSitesOffLock[key]; allowed {
					seenAllowlistKeys[key] = true
					continue
				}
				uncounted = append(uncounted, testStoreWriteCallSite{
					function: fn.Name.Name,
					method:   site.method,
					position: fset.Position(site.pos).String(),
				})
			}
		}
	}
	if len(uncounted) > 0 {
		sort.Slice(uncounted, func(i, j int) bool { return uncounted[i].position < uncounted[j].position })
		lines := make([]string, 0, len(uncounted))
		for _, site := range uncounted {
			lines = append(lines, fmt.Sprintf("  %s: %s calls %s outside trackDurableWrite", site.position, site.function, site.method))
		}
		t.Fatalf("durable test-store writes that are invisible to durableWritesInFlight:\n%s\n\nWrap each in tr.trackDurableWrite, or - if it provably cannot run with tr.mu write-locked - add \"<function>/<Method>\" to testStoreWriteCallSitesOffLock with that reason. A write missing from the gauge makes the run-driver panic backstop re-panic a healthy controller and makes the liveness probe block until the kubelet restarts it.", strings.Join(lines, "\n"))
	}
	for key := range testStoreWriteCallSitesOffLock {
		if !seenAllowlistKeys[key] {
			t.Errorf("testStoreWriteCallSitesOffLock has a stale entry %q; no such call site exists any more", key)
		}
	}
}

// TestAllowlistedUncountedWritesStillReachTheGauge checks the two allowlist
// reasons that are mechanically checkable, so the entries cannot quietly become
// false: persistStoredTestStatus must still increment the gauge itself, and the
// snapshot save must still be reached only from inside a trackDurableWrite.
func TestAllowlistedUncountedWritesStillReachTheGauge(t *testing.T) {
	fset := token.NewFileSet()
	files := parsePackageSourceFiles(t, fset)

	incrementsGauge := false
	snapshotCallSites := 0
	untrackedSnapshotCallSites := []string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if fn.Name.Name == "persistStoredTestStatus" {
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, isCall := node.(*ast.CallExpr)
					if !isCall {
						return true
					}
					sel, isSel := call.Fun.(*ast.SelectorExpr)
					if !isSel || sel.Sel.Name != "Add" {
						return true
					}
					if inner, isSel := sel.X.(*ast.SelectorExpr); isSel && inner.Sel.Name == "durableWritesInFlight" {
						incrementsGauge = true
					}
					return true
				})
			}
			spans := trackDurableWriteSpans(fn)
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, isCall := node.(*ast.CallExpr)
				if !isCall {
					return true
				}
				sel, isSel := call.Fun.(*ast.SelectorExpr)
				if !isSel || sel.Sel.Name != "saveManagedTestRecordSnapshot" {
					return true
				}
				snapshotCallSites++
				for _, span := range spans {
					if call.Pos() >= span.start && call.End() <= span.end {
						return true
					}
				}
				untrackedSnapshotCallSites = append(untrackedSnapshotCallSites, fset.Position(call.Pos()).String())
				return true
			})
		}
	}
	if !incrementsGauge {
		t.Error("persistStoredTestStatus no longer increments durableWritesInFlight itself, but testStoreWriteCallSitesOffLock still exempts its store calls on that basis")
	}
	if snapshotCallSites == 0 {
		t.Error("found no saveManagedTestRecordSnapshot call sites; the allowlist entries that depend on its single tracked caller are stale")
	}
	if len(untrackedSnapshotCallSites) > 0 {
		t.Errorf("saveManagedTestRecordSnapshot is called outside trackDurableWrite at %s; its store writes are then invisible to the in-flight gauge", strings.Join(untrackedSnapshotCallSites, ", "))
	}
}

func parsePackageSourceFiles(t *testing.T, fset *token.FileSet) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatal("parsed no package source files")
	}
	return files
}

type sourceSpan struct{ start, end token.Pos }

type foundCallSite struct {
	method string
	pos    token.Pos
}

func trackDurableWriteSpans(fn *ast.FuncDecl) []sourceSpan {
	var spans []sourceSpan
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "trackDurableWrite" {
			spans = append(spans, sourceSpan{start: call.Pos(), end: call.End()})
		}
		return true
	})
	return spans
}

func uncountedTestStoreWrites(fn *ast.FuncDecl) []foundCallSite {
	tracked := trackDurableWriteSpans(fn)
	derived := map[string]bool{}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) == 0 || len(assign.Rhs) != 1 {
			return true
		}
		assertion, ok := assign.Rhs[0].(*ast.TypeAssertExpr)
		if !ok || !isTestStoreSelector(assertion.X) {
			return true
		}
		if ident, ok := assign.Lhs[0].(*ast.Ident); ok && ident.Name != "_" {
			derived[ident.Name] = true
		}
		return true
	})

	var found []foundCallSite
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		receiverIsStore := isTestStoreSelector(sel.X)
		if !receiverIsStore {
			if ident, isIdent := sel.X.(*ast.Ident); isIdent && derived[ident.Name] {
				receiverIsStore = true
			}
		}
		if !receiverIsStore || isStoreReadMethod(sel.Sel.Name) {
			return true
		}
		for _, wrapper := range tracked {
			if call.Pos() >= wrapper.start && call.End() <= wrapper.end {
				return true
			}
		}
		found = append(found, foundCallSite{method: sel.Sel.Name, pos: call.Pos()})
		return true
	})
	return found
}

// isTestStoreSelector matches any ".testStore" selector, whatever the receiver
// expression is (tr.testStore, s.runner.testStore, ...), so a write cannot hide
// behind an indirection.
func isTestStoreSelector(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "testStore"
}

func isStoreReadMethod(name string) bool {
	for _, prefix := range storeReadMethodPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
