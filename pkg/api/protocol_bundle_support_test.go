package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestProtocolBundleSupportMatrixEndpointCoversRequestedBundles(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/protocol-bundles/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload protocolBundleSupportResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, bundle := range []string{"web_api", "database", "network", "oracle_e_business", "templates", "sap_gui_foundation"} {
		if payload.BundleCounts[bundle] == nil {
			t.Fatalf("bundle_counts missing %q in %#v", bundle, payload.BundleCounts)
		}
	}
	for _, id := range []string{"web-api-bruno", "database-sql", "network-dns", "network-ldap", "oeb-oracle-web", "oeb-oracle-nca", "sap-gui-foundation"} {
		if !protocolBundleMatrixContains(payload.SupportMatrix, id) {
			t.Fatalf("support_matrix missing %q", id)
		}
	}
	if !protocolBundleEntitlementContains(payload.Entitlements, "sap_gui_foundation") {
		t.Fatalf("entitlements = %#v, want sap_gui_foundation", payload.Entitlements)
	}
	if !protocolBundleRunnerContains(payload.RunnerClasses, "windows_sap_gui_runner") {
		t.Fatalf("runner_classes = %#v, want windows_sap_gui_runner", payload.RunnerClasses)
	}
	if !protocolBundleRunnerContains(payload.RunnerClasses, "bruno_node_runner") {
		t.Fatalf("runner_classes = %#v, want bruno_node_runner", payload.RunnerClasses)
	}
	if payload.StatusCounts["review_only"] == 0 || payload.StatusCounts["external_runner_required"] == 0 {
		t.Fatalf("status_counts = %#v, want review-only and external-runner rows", payload.StatusCounts)
	}
}

func TestTrustedHeaderAuthRequiresViewerForProtocolBundleSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/protocol-bundles/support-matrix", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d body=%s", unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
	}

	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/protocol-bundles/support-matrix", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-APEXLoad-Roles", RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusOK {
		t.Fatalf("viewer status = %d, want %d body=%s", viewerResp.Code, http.StatusOK, viewerResp.Body.String())
	}
}

func TestProtocolBundleJavaVuserRunnerDescribesQualifiedPersistentPlatforms(t *testing.T) {
	supportMatrix := protocolBundleSupportMatrix()
	var javaEntry *ProtocolBundleSupportEntry
	for index := range supportMatrix {
		if supportMatrix[index].ID == "template-java-vuser" {
			javaEntry = &supportMatrix[index]
			break
		}
	}
	if javaEntry == nil {
		t.Fatal("Java Vuser support entry is missing")
	}
	javaContractParts := append([]string(nil), javaEntry.RuntimeCoverage...)
	javaContractParts = append(javaContractParts, javaEntry.RemainingGaps...)
	javaContractParts = append(javaContractParts, javaEntry.RecommendedWorkflow)
	javaContract := strings.Join(javaContractParts, " ")
	for _, required := range []string{
		protocol.FeatureJavaVuserPersistentLifecycleV1,
		protocol.FeatureJavaVuserSessionFrameTokenV1,
		"strong descendant-process containment",
		"stock non-privileged Helm, Kustomize, and Compose sidecars support legacy full_lifecycle only",
		"qualified Windows runner or delegated-cgroup Linux runner",
	} {
		if !strings.Contains(javaContract, required) {
			t.Errorf("Java Vuser support contract is missing %q: %#v", required, javaEntry)
		}
	}

	runnerClasses := protocolBundleRunnerClasses()
	var runner *ProtocolBundleRunnerClass
	for index := range runnerClasses {
		if runnerClasses[index].ID == "java_vuser_jvm_runner" {
			runner = &runnerClasses[index]
			break
		}
	}
	if runner == nil {
		t.Fatal("Java Vuser runner class is missing")
	}
	if runner.OS != "linux_or_windows" {
		t.Errorf("Java Vuser runner OS = %q, want linux_or_windows", runner.OS)
	}
	if !strings.Contains(runner.Notes, protocol.FeatureJavaVuserPersistentLifecycleV1) ||
		!strings.Contains(runner.Notes, protocol.FeatureJavaVuserSessionFrameTokenV1) ||
		!strings.Contains(runner.Notes, "stock non-privileged sidecars remain legacy-only") {
		t.Errorf("Java Vuser runner notes = %q, want persistent capability and stock-sidecar boundary", runner.Notes)
	}
}

func protocolBundleMatrixContains(entries []ProtocolBundleSupportEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func protocolBundleEntitlementContains(entries []ProtocolBundleEntitlement, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func protocolBundleRunnerContains(entries []ProtocolBundleRunnerClass, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

// protocolBundleSelfEvidenceFiles resolves the files that define and test the
// support matrix itself. The matrix citing its own source is the purest form of
// the circular citation TestProtocolBundleSupportedEntriesCiteRealCodeEvidence
// exists to block, so those files never count as evidence.
//
// The set is resolved from the running binary rather than written down as path
// literals, and callers must compare with os.SameFile rather than string
// equality. Both are load-bearing:
//
//   - Go does not care what a file inside a package is named, so a hardcoded
//     list survives a rename or split of the matrix source with a green build
//     and green tests while every lookup quietly misses — turning the
//     circular-citation guard back into the no-op it was added to replace.
//   - An exemption keyed on the literal evidence string is defeated by any
//     respelling of the same path ("pkg/api/./protocol_bundle_support.go"),
//     which is not a rename at all and would never be noticed in review.
//
// If either file cannot be resolved on disk the caller fails loudly instead of
// degrading to "nothing is self-evidence", which would pass silently.
func protocolBundleSelfEvidenceFiles(t *testing.T) []os.FileInfo {
	t.Helper()

	matrixFunc := runtime.FuncForPC(reflect.ValueOf(protocolBundleSupportMatrix).Pointer())
	if matrixFunc == nil {
		t.Fatal("cannot resolve the file that declares protocolBundleSupportMatrix; without it the self-evidence exemption would silently disarm the circular-citation guard")
	}
	matrixFile, _ := matrixFunc.FileLine(matrixFunc.Entry())
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve the file that declares this test; without it the self-evidence exemption would silently disarm the circular-citation guard")
	}

	// go test runs with the package directory as the working directory, and a
	// Go declaration always lives in a file of its own package directory, so the
	// base name is enough to reach the file on disk even under -trimpath.
	infos := make([]os.FileInfo, 0, 2)
	for _, compiled := range []string{matrixFile, testFile} {
		onDisk := filepath.Base(filepath.FromSlash(compiled))
		info, err := os.Stat(onDisk)
		if err != nil {
			t.Fatalf("self-evidence file %q (compiled from %q) is not readable in the package directory: %v", onDisk, compiled, err)
		}
		infos = append(infos, info)
	}
	return infos
}

// protocolBundleIsSelfEvidence reports whether evidence names the same file as
// one of the self-evidence files, by file identity rather than path spelling.
func protocolBundleIsSelfEvidence(evidence os.FileInfo, selfEvidence []os.FileInfo) bool {
	for _, self := range selfEvidence {
		if os.SameFile(evidence, self) {
			return true
		}
	}
	return false
}

// protocolBundleIsCodeEvidence reports whether the artifact that evidence
// resolved to is code rather than prose. The decision is made from the artifact
// on disk — its own extension, or, for a directory, the first source file found
// beneath it — so it cannot be steered by how the evidence path was spelled.
//
// This is the inverse of the "documentation is exempt" rule it replaced, and
// the inversion is the point: exempting a prefix leaves everything that is not
// that prefix counting as code, so both "./docs/X.md" (the same file, respelled
// past the prefix) and a repo-root "ARCHITECTURE.md" (which never carried the
// prefix) were accepted as evidence of a working protocol implementation.
func protocolBundleIsCodeEvidence(absolute string, info os.FileInfo) bool {
	if !info.IsDir() {
		return protocolBundleIsSourceFile(info.Name())
	}
	found := false
	_ = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			// Dependency and VCS trees hold third-party sources, which say
			// nothing about what this repository implements.
			name := entry.Name()
			if path != absolute && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor") {
				return fs.SkipDir
			}
			return nil
		}
		if protocolBundleIsSourceFile(entry.Name()) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// protocolBundleIsSourceFile reports whether name is a source file in one of
// the languages this repository ships.
func protocolBundleIsSourceFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".java", ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".py", ".cs":
		return true
	}
	return false
}

// TestProtocolBundleSupportedEntriesCiteRealCodeEvidence keeps the published
// bundle matrix from being self-referential. docs/COMPETITIVE_COMPARISON.md
// points readers at this matrix as the code-owned record of protocol support,
// so an entry that claims "supported" while citing only that same document — or
// only the matrix source file that makes the claim — as its evidence is a
// circular citation with nothing behind it. Evidence written as "path::TestName"
// must also name a test function the file still declares, so a rename or
// deletion cannot leave the matrix pointing at a test that no longer exists.
//
// Every citation is resolved on disk before anything is exempted, so a stale
// path left behind by a rename or a move is reported rather than quietly
// skipped — documentation citations included, which is why a section anchor
// ("docs/X.md#section") is trimmed before the lookup rather than used to wave
// the citation through. Whether a resolved citation then counts as evidence is
// decided by protocolBundleIsCodeEvidence from the artifact itself, never from
// the spelling of the path: prose is not evidence no matter which directory it
// is written in or how the citation is punctuated.
func TestProtocolBundleSupportedEntriesCiteRealCodeEvidence(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	selfEvidence := protocolBundleSelfEvidenceFiles(t)
	for _, entry := range protocolBundleSupportMatrix() {
		if entry.Status != "supported" {
			continue
		}
		var cited []string
		for _, evidence := range entry.Evidence {
			// Evidence may name a specific test as "path::TestName", and a
			// documentation citation may name a section as "path#section".
			path, testName, namesTest := strings.Cut(evidence, "::")
			path, _, _ = strings.Cut(path, "#")
			absolute := filepath.Join(repoRoot, filepath.FromSlash(path))
			info, err := os.Stat(absolute)
			if err != nil {
				t.Errorf("%s: evidence %q does not exist in the repository: %v", entry.ID, evidence, err)
				continue
			}
			if protocolBundleIsSelfEvidence(info, selfEvidence) {
				continue
			}
			if namesTest && !goFileDeclaresFunc(t, absolute, testName) {
				t.Errorf("%s: evidence %q names a function that %s does not declare (renamed or deleted?)", entry.ID, evidence, path)
				continue
			}
			if !protocolBundleIsCodeEvidence(absolute, info) {
				continue
			}
			cited = append(cited, path)
		}
		if len(cited) == 0 {
			t.Errorf("%s is published as supported but cites no in-repository code evidence, only %v", entry.ID, entry.Evidence)
		}
	}
}

// goFileDeclaresFunc reports whether path declares a top-level func with the
// given name. Deliberately hermetic: reading the file keeps the check working
// for evidence in packages this test does not import.
func goFileDeclaresFunc(t *testing.T, path, name string) bool {
	t.Helper()
	if name == "" {
		return false
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("read evidence file %s: %v", path, err)
		return false
	}
	return strings.Contains(string(contents), "\nfunc "+name+"(")
}
