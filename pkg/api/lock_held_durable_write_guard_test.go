package api

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// TestEveryLockHeldDurableWriteIsCounted enumerates the durable store writes
// this package issues while tr.mu is held - to ANY store, not just the test
// store - and requires each of them to be published to durableWritesInFlight.
//
// TestEveryTestStoreWriteIsCountedOrDocumented covers tr.testStore writes
// wherever they appear. This one covers the complementary axis: writes issued
// with tr.mu held, whichever store they target. That axis is the one both
// readers of the gauge actually care about, because neither of them is asking
// about a table - the panic backstop is asking whether the mutex it cannot
// acquire is orphaned or merely held by someone stuck in the storage driver
// (awaitPanicRecoveryStage), and the liveness probe is asking whether reading
// live runner state would block behind that same holder
// (healthCheckSnapshotWithoutRunnerLock). A lock-held write to the script,
// scenario, settings or archive store stalls them exactly as a test-store write
// does. This guard originally exposed more than twenty missed call sites and now
// also ensures future changes do not undo the off-lock authoring refactor.
//
// The lock state is derived positionally rather than from an allowlist of
// function names, so a function that deliberately releases tr.mu around its
// store I/O - saveScenarioScheduleForSweepLocked, BindCapacityTimeslot,
// persistManagedTestStartOutsideLockLocked - is excluded by the analysis itself
// and needs no exemption entry that could later go stale.
func TestEveryLockHeldDurableWriteIsCounted(t *testing.T) {
	fset := token.NewFileSet()
	files := parsePackageSourceFiles(t, fset)

	var uncounted []string
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			for _, site := range uncountedLockHeldStoreWrites(fn) {
				uncounted = append(uncounted, fmt.Sprintf(
					"  %s: %s calls %s.%s with tr.mu held (%s) and outside any in-flight section",
					fset.Position(site.pos), fn.Name.Name, site.store, site.method, site.why))
			}
		}
	}
	if len(uncounted) > 0 {
		sort.Strings(uncounted)
		t.Fatalf("durable writes issued while tr.mu is held that are invisible to durableWritesInFlight:\n%s\n\n"+
			"Bracket the lock-held section with tr.beginLockHeldDurableWrite()/defer tr.endLockHeldDurableWrite(), "+
			"or wrap the call in tr.trackDurableWrite, or release tr.mu around the store call. "+
			"A lock-held write missing from the gauge makes the run-driver panic backstop re-panic a healthy "+
			"controller and makes the liveness probe block until the kubelet restarts the replica.",
			strings.Join(uncounted, "\n"))
	}
}

// TestLockHeldDurableWriteSectionsAreBalanced checks that every function that
// opens an in-flight section also defers the matching close. beginLockHeldDurableWrite
// without a deferred endLockHeldDurableWrite leaks the gauge above zero for the
// life of the process, which pins the liveness probe on its cached snapshot
// until the bypass budget expires and hands the panic backstop a permanent
// "healthy, just slow" answer.
func TestLockHeldDurableWriteSectionsAreBalanced(t *testing.T) {
	fset := token.NewFileSet()
	files := parsePackageSourceFiles(t, fset)

	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			opens := 0
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, isCall := node.(*ast.CallExpr)
				if !isCall {
					return true
				}
				if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel && sel.Sel.Name == "beginLockHeldDurableWrite" {
					opens++
				}
				return true
			})
			if opens == 0 {
				continue
			}
			deferredCloses := 0
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				stmt, isDefer := node.(*ast.DeferStmt)
				if !isDefer {
					return true
				}
				if sel, isSel := stmt.Call.Fun.(*ast.SelectorExpr); isSel && sel.Sel.Name == "endLockHeldDurableWrite" {
					deferredCloses++
				}
				return true
			})
			if deferredCloses != opens {
				t.Errorf("%s: %s opens %d durable-write section(s) but defers %d close(s); an unbalanced section leaks durableWritesInFlight above zero for the life of the process",
					fset.Position(fn.Pos()), fn.Name.Name, opens, deferredCloses)
			}
		}
	}
}

type lockHeldWriteSite struct {
	store  string
	method string
	why    string
	pos    token.Pos
}

// uncountedLockHeldStoreWrites reports the store writes in fn that are issued
// while tr.mu is held and are not published to the in-flight gauge.
//
// "Held" is decided by replaying the function's own mu events in source order:
// tr.mu.Lock() opens a held region, a non-deferred tr.mu.Unlock() closes it, and
// tr.mu.RLock() proves the write lock is not held (the mutex is not reentrant,
// so no caller can hold it either). A *Locked helper starts out held by its
// naming contract. This is deliberately conservative about what it claims to
// know: a function whose lock state depends on its caller and that neither
// takes tr.mu nor carries the Locked suffix is not inspected here - the
// testStore guard covers that axis by call site instead.
func uncountedLockHeldStoreWrites(fn *ast.FuncDecl) []lockHeldWriteSite {
	events := muLockEvents(fn)
	deferredUnlock := deferredMuUnlockPositions(fn)
	tracked := trackDurableWriteSpans(fn)
	sectionOpensAt, hasSection := firstDurableWriteSectionPos(fn)
	startsHeld := strings.HasSuffix(fn.Name.Name, "Locked")

	heldAt := func(pos token.Pos) (bool, string) {
		held := startsHeld
		why := "*Locked helper: caller holds mu"
		for _, event := range events {
			if event.pos >= pos {
				break
			}
			switch event.kind {
			case "lock":
				held, why = true, "tr.mu.Lock() earlier in this function"
			case "unlock":
				if deferredUnlock[event.pos] {
					continue
				}
				held = false
			case "rlock":
				held = false
			}
		}
		return held, why
	}

	derived := map[string]string{}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) == 0 || len(assign.Rhs) != 1 {
			return true
		}
		assertion, ok := assign.Rhs[0].(*ast.TypeAssertExpr)
		if !ok {
			return true
		}
		name, isStore := storeSelectorName(assertion.X)
		if !isStore {
			return true
		}
		if ident, ok := assign.Lhs[0].(*ast.Ident); ok && ident.Name != "_" {
			derived[ident.Name] = name
		}
		return true
	})

	var found []lockHeldWriteSite
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || isStoreReadMethod(sel.Sel.Name) {
			return true
		}
		store, isStore := storeSelectorName(sel.X)
		if !isStore {
			if ident, isIdent := sel.X.(*ast.Ident); isIdent {
				if name, wasDerived := derived[ident.Name]; wasDerived {
					store, isStore = name, true
				}
			}
		}
		if !isStore {
			return true
		}
		held, why := heldAt(call.Pos())
		if !held {
			return true
		}
		if hasSection && call.Pos() > sectionOpensAt {
			return true
		}
		for _, span := range tracked {
			if call.Pos() >= span.start && call.End() <= span.end {
				return true
			}
		}
		found = append(found, lockHeldWriteSite{store: store, method: sel.Sel.Name, why: why, pos: call.Pos()})
		return true
	})
	return found
}

// storeSelectorName matches any ".<something>Store" selector, whatever the
// receiver expression is, so a write cannot hide behind an indirection.
func storeSelectorName(expr ast.Expr) (string, bool) {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || !strings.HasSuffix(sel.Sel.Name, "Store") {
		return "", false
	}
	return sel.Sel.Name, true
}

type muLockEvent struct {
	pos  token.Pos
	kind string
}

func muLockEvents(fn *ast.FuncDecl) []muLockEvent {
	var events []muLockEvent
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		inner, ok := sel.X.(*ast.SelectorExpr)
		if !ok || inner.Sel.Name != "mu" {
			return true
		}
		switch sel.Sel.Name {
		case "Lock":
			events = append(events, muLockEvent{call.Pos(), "lock"})
		case "Unlock":
			events = append(events, muLockEvent{call.Pos(), "unlock"})
		case "RLock":
			events = append(events, muLockEvent{call.Pos(), "rlock"})
		}
		return true
	})
	sort.Slice(events, func(i, j int) bool { return events[i].pos < events[j].pos })
	return events
}

func deferredMuUnlockPositions(fn *ast.FuncDecl) map[token.Pos]bool {
	out := map[token.Pos]bool{}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		stmt, ok := node.(*ast.DeferStmt)
		if !ok {
			return true
		}
		sel, ok := stmt.Call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Unlock" {
			return true
		}
		if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "mu" {
			out[stmt.Call.Pos()] = true
		}
		return true
	})
	return out
}

func firstDurableWriteSectionPos(fn *ast.FuncDecl) (token.Pos, bool) {
	first, found := token.NoPos, false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "beginLockHeldDurableWrite" {
			return true
		}
		if !found || call.Pos() < first {
			first, found = call.Pos(), true
		}
		return true
	})
	return first, found
}
