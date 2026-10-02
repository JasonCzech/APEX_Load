package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/storage"
)

func javaReviewTestServer(t *testing.T, protected bool, store storage.Store) *Server {
	t.Helper()
	cfg := ServerConfig{Store: store, ImportAssetDir: t.TempDir()}
	if protected {
		wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x43}, 32), KeyID: "java-review-test"})
		if err != nil {
			t.Fatal(err)
		}
		cfg.BrunoArtifactDir = t.TempDir()
		cfg.BrunoArtifactWrapper = wrapper
	}
	return NewServer(cfg)
}

func javaReviewTestAsset(t *testing.T, server *Server, projectID, entry, content string) dsl.JavaClasspathAsset {
	t.Helper()
	payload := append(append([]byte(nil), jarLocalFileHeaderSignature...), []byte(content)...)
	digest, size, stored, err := streamJavaClasspathJar(bytes.NewReader(payload), server.importAssetDir, projectID, "java-review-test", defaultJavaClasspathUploadMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	return dsl.JavaClasspathAsset{Entry: entry, FileName: "dependency.jar", SHA256: digest, SizeBytes: size, StorageRef: (&url.URL{Scheme: "file", Path: stored}).String(), ContentType: "application/java-archive"}
}

func javaReviewTestDraft(t *testing.T, server *Server, baseline []dsl.JavaClasspathAsset) *dsl.Script {
	t.Helper()
	script := attestedImportTestScript("java-review", "java-review-canary-a391")
	script.Protocol = "java_vuser"
	script.Metadata.Import.Source = "enterprise_script_archive"
	script.Config.JavaVuser.Classpath = []string{"libs/client.jar", "libs/extra.jar"}
	script.Config.JavaVuser.ClasspathAssets = baseline
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil)
	response := server.newScriptImportResponseWithSourceSecrets(request, scriptImportResponseOptions{Script: script})
	if importDraftAttestationToken(script) == "" {
		t.Fatalf("missing attestation: %#v", response)
	}
	return script
}

func TestAuthoringJavaClasspathAssetsLegacyAndProtectedClaims(t *testing.T) {
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "true")
	for _, protected := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "protected"}[protected], func(t *testing.T) {
			server := javaReviewTestServer(t, protected, nil)
			old := javaReviewTestAsset(t, server, "project-a", "libs/client.jar", "old")
			added := javaReviewTestAsset(t, server, "project-a", "libs/extra.jar", "added")
			for _, tc := range []struct {
				name                string
				baseline, requested []dsl.JavaClasspathAsset
			}{
				{"add", nil, []dsl.JavaClasspathAsset{added}},
				{"remove", []dsl.JavaClasspathAsset{old}, []dsl.JavaClasspathAsset{}},
				{"replace and remove", []dsl.JavaClasspathAsset{old}, []dsl.JavaClasspathAsset{added}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					draft := javaReviewTestDraft(t, server, tc.baseline)
					submitted := cloneScriptDefinition(draft)
					submitted.Config.JavaVuser.ClasspathAssets = tc.requested
					req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", nil)
					// The existing credential-set contract remains unchanged. This
					// fixture allows non-credential edits through ordinary claims;
					// Java review adds its own full-baseline and capability checks.
					ordinary, err := server.claimImportDraftAttestation(req, submitted)
					if err != nil || ordinary == nil {
						t.Fatalf("ordinary credential-set claim changed: %v", err)
					}
					if len(ordinary.Script.Config.JavaVuser.ClasspathAssets) != len(tc.requested) {
						t.Fatal("ordinary credential-set claim asset behavior changed")
					}
					server.releaseImportDraftAttestation(ordinary)
					claim, err := server.claimImportDraftAttestationWithJavaReview(req, submitted, true)
					if err != nil || claim == nil {
						t.Fatalf("review claim error=%v", err)
					}
					defer server.releaseImportDraftAttestation(claim)
					if len(claim.Script.Config.JavaVuser.ClasspathAssets) != len(tc.requested) || claim.Script.Config.JavaVuser.Enabled {
						t.Fatal("verified assets omitted or runner enabled")
					}
					if len(tc.requested) > 0 && !reflect.DeepEqual(claim.Script.Config.JavaVuser.ClasspathAssets, tc.requested) {
						t.Fatal("verified binding changed")
					}
					if !reflect.DeepEqual(draft.Config.JavaVuser.ClasspathAssets, tc.baseline) {
						t.Fatal("trusted baseline mutated")
					}
				})
			}
		})
	}
}

func TestAuthoringJavaClasspathAssetsRejectForgedCapabilitiesAndOtherMutations(t *testing.T) {
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "true")
	for _, protected := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "protected"}[protected], func(t *testing.T) {
			server := javaReviewTestServer(t, protected, nil)
			asset := javaReviewTestAsset(t, server, "project-a", "libs/client.jar", "safe")
			foreign := javaReviewTestAsset(t, server, "project-b", "libs/client.jar", "foreign")
			ordinary := filepath.Join(t.TempDir(), "ordinary.jar")
			if err := os.WriteFile(ordinary, append(jarLocalFileHeaderSignature, []byte("safe")...), 0600); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name   string
				mutate func(*dsl.Script)
			}{
				{"cross project", func(s *dsl.Script) { s.Config.JavaVuser.ClasspathAssets[0] = foreign }},
				{"changed draft project", func(s *dsl.Script) { s.ProjectID = "project-b"; s.Config.JavaVuser.ClasspathAssets[0] = foreign }},
				{"outside capability store", func(s *dsl.Script) {
					s.Config.JavaVuser.ClasspathAssets[0].StorageRef = (&url.URL{Scheme: "file", Path: ordinary}).String()
				}},
				{"forged capability", func(s *dsl.Script) {
					a := &s.Config.JavaVuser.ClasspathAssets[0]
					a.StorageRef = strings.Replace(a.StorageRef, "java-review-test/", "different-import/", 1)
				}},
				{"digest", func(s *dsl.Script) { s.Config.JavaVuser.ClasspathAssets[0].SHA256 = strings.Repeat("a", 64) }},
				{"size", func(s *dsl.Script) { s.Config.JavaVuser.ClasspathAssets[0].SizeBytes++ }},
				{"new entry", func(s *dsl.Script) {
					s.Config.JavaVuser.Classpath = append(s.Config.JavaVuser.Classpath, "new.jar")
					s.Config.JavaVuser.ClasspathAssets[0].Entry = "new.jar"
				}},
				{"duplicate entry", func(s *dsl.Script) {
					s.Config.JavaVuser.ClasspathAssets = append(s.Config.JavaVuser.ClasspathAssets, asset)
				}},
				{"staging bypass", func(s *dsl.Script) { s.Config.JavaVuser.ClasspathAssets[0].StagedAsset = "forged" }},
				{"whitespace protocol", func(s *dsl.Script) { s.Protocol = " java_vuser " }},
				{"whitespace protocol forged asset", func(s *dsl.Script) {
					s.Protocol = " java_vuser "
					s.Config.JavaVuser.ClasspathAssets[0].StorageRef = "file:///tmp/forged.jar"
				}},
				{"whitespace protocol enabled runner", func(s *dsl.Script) { s.Protocol = " java_vuser "; s.Config.JavaVuser.Enabled = true }},
				{"changed protocol family", func(s *dsl.Script) { s.Protocol = "http" }},
				{"changed runner config", func(s *dsl.Script) { s.Config.JavaVuser.Enabled = true }},
				{"changed action", func(s *dsl.Script) { s.Actions[0].Target = "/changed" }},
				{"changed credential", func(s *dsl.Script) { s.Actions[0].HeaderList[0].Value = "Bearer changed-credential" }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					draft := javaReviewTestDraft(t, server, nil)
					submitted := cloneScriptDefinition(draft)
					submitted.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{asset}
					tc.mutate(submitted)
					claim, err := server.claimImportDraftAttestationWithJavaReview(httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", nil), submitted, true)
					if err == nil {
						server.releaseImportDraftAttestation(claim)
						t.Fatal("forged dependency mutation accepted")
					}
					if claim, err := server.claimImportDraftAttestation(httptest.NewRequest(http.MethodPost, "/api/v1/scripts", nil), draft); err != nil {
						t.Fatalf("rejected delta stranded baseline: %v", err)
					} else {
						server.releaseImportDraftAttestation(claim)
					}
				})
			}
		})
	}
}

func TestAuthoringJavaClasspathAssetsPolicyOff(t *testing.T) {
	server := javaReviewTestServer(t, false, nil)
	asset := javaReviewTestAsset(t, server, "project-a", "libs/client.jar", "safe")
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "false")
	draft := javaReviewTestDraft(t, server, nil)
	submitted := cloneScriptDefinition(draft)
	submitted.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{asset}
	if claim, err := server.claimImportDraftAttestationWithJavaReview(httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", nil), submitted, true); err == nil {
		server.releaseImportDraftAttestation(claim)
		t.Fatal("policy-disabled attachment accepted")
	}
	empty := javaReviewTestDraft(t, server, nil)
	empty.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{}
	if claim, err := server.claimImportDraftAttestationWithJavaReview(httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", nil), empty, true); err != nil {
		t.Fatalf("policy-off empty representation rejected: %v", err)
	} else {
		server.releaseImportDraftAttestation(claim)
	}
	baseline := javaReviewTestDraft(t, server, []dsl.JavaClasspathAsset{asset})
	if claim, err := server.claimImportDraftAttestationWithJavaReview(httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", nil), baseline, true); err != nil {
		t.Fatalf("policy-off unchanged baseline rejected: %v", err)
	} else {
		server.releaseImportDraftAttestation(claim)
	}
	removed := cloneScriptDefinition(baseline)
	removed.Config.JavaVuser.ClasspathAssets = nil
	if claim, err := server.claimImportDraftAttestationWithJavaReview(httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", nil), removed, true); err == nil {
		server.releaseImportDraftAttestation(claim)
		t.Fatal("policy-disabled asset delta accepted")
	}
}

func TestAuthoringJavaClasspathAssetsPersistAndReplayIdempotently(t *testing.T) {
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "true")
	for _, protected := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "protected"}[protected], func(t *testing.T) {
			t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "true")
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "review.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			seedAuthoringScriptProjects(t, store, "project-a")
			server := javaReviewTestServer(t, protected, store)
			asset := javaReviewTestAsset(t, server, "project-a", "libs/client.jar", "safe")
			draft := javaReviewTestDraft(t, server, nil)
			submitted := cloneScriptDefinition(draft)
			submitted.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{asset}
			payload := map[string]any{"project_id": "project-a", "source": "enterprise_script_archive", "name": "Java dependency review", "status": "editing", "draft": submitted, "idempotency_key": "java-dependency-review-test"}
			created := authoringSecretRequest(t, server, http.MethodPost, "/api/v1/scripts/authoring/sessions", payload)
			if created.Code != http.StatusCreated {
				t.Fatalf("create=%d %s", created.Code, created.Body.String())
			}
			var session storage.AuthoringSession
			if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
				t.Fatal(err)
			}
			persisted, err := store.GetAuthoringSession(session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(persisted.Draft.Config.JavaVuser.ClasspathAssets, []dsl.JavaClasspathAsset{asset}) || persisted.Draft.Config.JavaVuser.Enabled {
				t.Fatal("persisted dependency bindings omitted or execution enabled")
			}
			t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "false")
			replayed := authoringSecretRequest(t, server, http.MethodPost, "/api/v1/scripts/authoring/sessions", payload)
			if replayed.Code != http.StatusOK {
				t.Fatalf("replay=%d %s", replayed.Code, replayed.Body.String())
			}
			changed := cloneScriptDefinition(submitted)
			changed.Config.JavaVuser.ClasspathAssets = nil
			payload["draft"] = changed
			if conflict := authoringSecretRequest(t, server, http.MethodPost, "/api/v1/scripts/authoring/sessions", payload); conflict.Code != http.StatusConflict {
				t.Fatalf("changed idempotency payload=%d %s", conflict.Code, conflict.Body.String())
			}
		})
	}
}

func TestAuthoringJavaClasspathAssetsUpdateChecksChangedBindings(t *testing.T) {
	for _, protected := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "protected"}[protected], func(t *testing.T) {
			t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "true")
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "review.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			seedAuthoringScriptProjects(t, store, "project-a", "project-b")
			server := javaReviewTestServer(t, protected, store)
			asset := javaReviewTestAsset(t, server, "project-a", "libs/client.jar", "safe")
			foreign := javaReviewTestAsset(t, server, "project-b", "libs/client.jar", "foreign")
			added := javaReviewTestAsset(t, server, "project-a", "libs/extra.jar", "added")
			draft := javaReviewTestDraft(t, server, nil)
			draft.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{asset}
			created := authoringSecretRequest(t, server, http.MethodPost, "/api/v1/scripts/authoring/sessions", map[string]any{"project_id": "project-a", "source": "enterprise_script_archive", "status": "editing", "draft": draft})
			if created.Code != http.StatusCreated {
				t.Fatalf("create=%d %s", created.Code, created.Body.String())
			}
			var session storage.AuthoringSession
			if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
				t.Fatal(err)
			}
			persisted, err := store.GetAuthoringSession(session.ID)
			if err != nil {
				t.Fatal(err)
			}
			route := "/api/v1/scripts/authoring/sessions/" + session.ID
			for _, tc := range []struct {
				name   string
				mutate func(*dsl.Script)
			}{
				{"foreign project", func(s *dsl.Script) { s.Config.JavaVuser.ClasspathAssets[0] = foreign }},
				{"forged reference", func(s *dsl.Script) { s.Config.JavaVuser.ClasspathAssets[0].StorageRef = "file:///tmp/forged.jar" }},
				{"changed digest", func(s *dsl.Script) { s.Config.JavaVuser.ClasspathAssets[0].SHA256 = strings.Repeat("a", 64) }},
				{"changed size", func(s *dsl.Script) { s.Config.JavaVuser.ClasspathAssets[0].SizeBytes++ }},
				{"undeclared entry", func(s *dsl.Script) { s.Config.JavaVuser.ClasspathAssets[0].Entry = "new.jar" }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					candidate := cloneScriptDefinition(persisted.Draft)
					tc.mutate(candidate)
					response := authoringSecretRequest(t, server, http.MethodPut, route, map[string]any{"project_id": "project-a", "source": "enterprise_script_archive", "status": "editing", "draft": candidate})
					if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Java classpath attachment") {
						t.Fatalf("update=%d %s", response.Code, response.Body.String())
					}
					unchanged, err := store.GetAuthoringSession(session.ID)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(unchanged.Draft.Config.JavaVuser.ClasspathAssets, []dsl.JavaClasspathAsset{asset}) {
						t.Fatal("rejected update changed saved binding")
					}
				})
			}
			valid := cloneScriptDefinition(persisted.Draft)
			valid.Config.JavaVuser.ClasspathAssets = append(valid.Config.JavaVuser.ClasspathAssets, added)
			response := authoringSecretRequest(t, server, http.MethodPut, route, map[string]any{"project_id": "project-a", "source": "enterprise_script_archive", "status": "editing", "draft": valid})
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "execution context for retained imported credentials") {
				t.Fatalf("retained-credential execution context guard changed: %d %s", response.Code, response.Body.String())
			}
			// A verified asset does not authorize redirecting a retained literal
			// credential into new Java code. Governed runtime binding permits the
			// ordinary authoring edit without weakening that existing boundary.
			valid.Actions[0].HeaderList[0].Value = "Bearer {{secret.JAVA_REVIEW_TOKEN}}"
			response = authoringSecretRequest(t, server, http.MethodPut, route, map[string]any{"project_id": "project-a", "source": "enterprise_script_archive", "status": "editing", "draft": valid})
			if response.Code != http.StatusOK {
				t.Fatalf("runtime-bound verified update=%d %s", response.Code, response.Body.String())
			}
			persisted, err = store.GetAuthoringSession(session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(persisted.Draft.Config.JavaVuser.ClasspathAssets, []dsl.JavaClasspathAsset{asset, added}) {
				t.Fatal("verified update binding omitted")
			}
			t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "false")
			response = authoringSecretRequest(t, server, http.MethodPut, route, map[string]any{"project_id": "project-a", "source": "enterprise_script_archive", "status": "editing", "draft": persisted.Draft})
			if response.Code != http.StatusOK {
				t.Fatalf("policy-off unchanged update=%d %s", response.Code, response.Body.String())
			}
			removed := cloneScriptDefinition(persisted.Draft)
			removed.Config.JavaVuser.ClasspathAssets = nil
			response = authoringSecretRequest(t, server, http.MethodPut, route, map[string]any{"project_id": "project-a", "source": "enterprise_script_archive", "status": "editing", "draft": removed})
			if response.Code != http.StatusBadRequest {
				t.Fatalf("policy-off removal=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAuthoringJavaClasspathAssetsNormalizedProtocolVerification(t *testing.T) {
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "true")
	server := javaReviewTestServer(t, false, nil)
	asset := javaReviewTestAsset(t, server, "project-a", "libs/client.jar", "safe")
	draft := &dsl.Script{ProjectID: "project-a", Protocol: " JAVA_VUSER\t", Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{Classpath: []string{"libs/client.jar"}, ClasspathAssets: []dsl.JavaClasspathAsset{asset}}}}
	if err := server.validateAuthoringJavaClasspathAssets(draft, draft.Config.JavaVuser.Classpath); err != nil {
		t.Fatalf("normalized Java asset family rejected: %v", err)
	}
	draft.Config.JavaVuser.ClasspathAssets[0].StorageRef = "file:///tmp/forged.jar"
	if err := server.validateAuthoringJavaClasspathAssets(draft, draft.Config.JavaVuser.Classpath); err == nil || !strings.Contains(err.Error(), "valid project-owned uploaded JAR") {
		t.Fatalf("normalized protocol skipped capability verification: %v", err)
	}
}

func TestAuthoringJavaClasspathAssetsNonJavaClaimCannotSkipValidation(t *testing.T) {
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "true")
	for _, protected := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "protected"}[protected], func(t *testing.T) {
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "review.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			seedAuthoringScriptProjects(t, store, "project-a", "project-b")
			server := javaReviewTestServer(t, protected, store)
			foreign := javaReviewTestAsset(t, server, "project-b", "libs/client.jar", "foreign")
			draft := attestedImportTestScript("http-review", "java-transition-canary-a391")
			draft.Config.JavaVuser.Classpath = []string{"libs/client.jar"}
			server.newScriptImportResponseWithSourceSecrets(httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil), scriptImportResponseOptions{Script: draft})
			candidate := cloneScriptDefinition(draft)
			candidate.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{foreign}
			response := authoringSecretRequest(t, server, http.MethodPost, "/api/v1/scripts/authoring/sessions", map[string]any{"project_id": "project-a", "source": "sample", "status": "editing", "draft": candidate})
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Java classpath attachment") {
				t.Fatalf("non-Java claim waived attachment verification: %d %s", response.Code, response.Body.String())
			}
			if protected {
				candidate.Protocol = "java_vuser"
				response = authoringSecretRequest(t, server, http.MethodPost, "/api/v1/scripts/authoring/sessions", map[string]any{"project_id": "project-a", "source": "sample", "status": "editing", "draft": candidate})
				if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "valid project-owned uploaded JAR") {
					t.Fatalf("protected non-Java transition waived capability verification: %d %s", response.Code, response.Body.String())
				}
			}
			response = authoringSecretRequest(t, server, http.MethodPost, "/api/v1/scripts/authoring/sessions", map[string]any{"project_id": "project-a", "source": "sample", "status": "editing", "draft": draft})
			if response.Code != http.StatusCreated {
				t.Fatalf("rejected transition stranded ordinary baseline: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAuthoringJavaClasspathAssetsProtocolTransitionsAndActivation(t *testing.T) {
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "true")
	server := javaReviewTestServer(t, false, nil)
	asset := javaReviewTestAsset(t, server, "project-a", "libs/client.jar", "safe")
	foreign := javaReviewTestAsset(t, server, "project-b", "libs/client.jar", "foreign")
	base := &dsl.Script{ProjectID: "project-a", Protocol: "http", Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{Classpath: []string{"libs/client.jar"}, ClasspathAssets: []dsl.JavaClasspathAsset{foreign}}}}
	candidate := cloneScriptDefinition(base)
	candidate.Protocol = " java_vuser "
	if err := server.validateAuthoringJavaClasspathAssetMutation(candidate, base); err == nil {
		t.Fatal("unchanged foreign evidence activated from HTTP")
	}
	base.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{asset}
	candidate = cloneScriptDefinition(base)
	candidate.Protocol = "java_vuser"
	if err := server.validateAuthoringJavaClasspathAssetMutation(candidate, base); err != nil {
		t.Fatalf("verified transition rejected: %v", err)
	}
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "false")
	if err := server.validateAuthoringJavaClasspathAssetMutation(candidate, base); err == nil {
		t.Fatal("policy-off HTTP evidence activated into Java")
	}
	if err := server.validateAuthoringJavaClasspathAssetMutation(candidate, candidate); err != nil {
		t.Fatalf("unchanged policy-off Java evidence rejected: %v", err)
	}
	activated := cloneScriptDefinition(candidate)
	activated.Config.JavaVuser.Enabled = true
	if err := server.validateAuthoringJavaClasspathAssetMutation(activated, candidate); err == nil {
		t.Fatal("policy-off inert evidence enabled without verification")
	}
	empty := cloneScriptDefinition(base)
	empty.Config.JavaVuser.ClasspathAssets = nil
	emptyJava := cloneScriptDefinition(empty)
	emptyJava.Protocol = "java_vuser"
	if err := server.validateAuthoringJavaClasspathAssetMutation(emptyJava, empty); err != nil {
		t.Fatalf("empty protocol transition was blocked: %v", err)
	}
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "true")
	reverse := cloneScriptDefinition(candidate)
	reverse.Protocol = "http"
	if err := server.validateAuthoringJavaClasspathAssetMutation(reverse, candidate); err == nil {
		t.Fatal("reverse transition carried latent Java bindings")
	}
	reverse.Config.JavaVuser.ClasspathAssets = nil
	if err := server.validateAuthoringJavaClasspathAssetMutation(reverse, candidate); err != nil {
		t.Fatalf("explicit removal before reverse transition rejected: %v", err)
	}
	back := cloneScriptDefinition(reverse)
	back.Protocol = "java_vuser"
	back.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{foreign}
	if err := server.validateAuthoringJavaClasspathAssetMutation(back, reverse); err == nil {
		t.Fatal("reverse-then-back transition revived unchecked bindings")
	}
	ref, err := url.Parse(asset.StorageRef)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ref.Path); err != nil {
		t.Fatal(err)
	}
	if err := server.validateAuthoringJavaClasspathAssetMutation(activated, candidate); err == nil {
		t.Fatal("stale trusted reference activated without content verification")
	}
}

func TestAuthoringJavaClasspathAssetsHTTPStoredDraftCannotActivateForeignBindings(t *testing.T) {
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "true")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedAuthoringScriptProjects(t, store, "project-a", "project-b")
	server := javaReviewTestServer(t, false, store)
	foreign := javaReviewTestAsset(t, server, "project-b", "libs/client.jar", "foreign")
	draft := attestedImportTestScript("legacy-http-review", "unused")
	draft.Metadata = nil
	draft.Actions[0].HeaderList[0].Value = "Bearer {{secret.JAVA_REVIEW_TOKEN}}"
	draft.Config.JavaVuser.Classpath = []string{"libs/client.jar"}
	draft.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{foreign}
	now := time.Now().UTC()
	session := &storage.AuthoringSession{ID: "legacy-http-assets", ProjectID: "project-a", Source: "manual", Name: "Legacy HTTP draft", Status: "editing", Draft: draft, CreatedAt: now, UpdatedAt: now}
	// Model a pre-fix row containing ignored Java config. Runtime-bound
	// credentials keep the assertion focused on asset ownership, not raw-value
	// execution-context protection.
	if err := store.SaveAuthoringSession(session); err != nil {
		t.Fatal(err)
	}
	candidate := cloneScriptDefinition(draft)
	candidate.Protocol = "java_vuser"
	response := authoringSecretRequest(t, server, http.MethodPut, "/api/v1/scripts/authoring/sessions/"+session.ID, map[string]any{"project_id": "project-a", "source": "manual", "status": "editing", "draft": candidate})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "valid project-owned uploaded JAR") {
		t.Fatalf("legacy HTTP asset transition=%d %s", response.Code, response.Body.String())
	}
	unchanged, err := store.GetAuthoringSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Draft.Protocol != "http" {
		t.Fatal("blocked activation changed stored protocol")
	}
}

func TestAuthoringJavaClasspathAssetsDormantBindingCannotActivateThroughClasspath(t *testing.T) {
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "true")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedAuthoringScriptProjects(t, store, "project-a", "project-b")
	server := javaReviewTestServer(t, false, store)
	foreign := javaReviewTestAsset(t, server, "project-b", "libs/client.jar", "foreign")
	draft := attestedImportTestScript("dormant-java-review", "unused")
	draft.Metadata = nil
	draft.Protocol = "java_vuser"
	draft.Actions[0].HeaderList[0].Value = "Bearer {{secret.JAVA_REVIEW_TOKEN}}"
	draft.Config.JavaVuser.Enabled = true
	draft.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{foreign}
	now := time.Now().UTC()
	session := &storage.AuthoringSession{ID: "dormant-java-assets", ProjectID: "project-a", Source: "manual", Name: "Dormant Java binding", Status: "editing", Draft: draft, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveAuthoringSession(session); err != nil {
		t.Fatal(err)
	}
	candidate := cloneScriptDefinition(draft)
	candidate.Config.JavaVuser.Classpath = []string{"libs/client.jar"}
	response := authoringSecretRequest(t, server, http.MethodPut, "/api/v1/scripts/authoring/sessions/"+session.ID, map[string]any{"project_id": "project-a", "source": "manual", "status": "editing", "draft": candidate})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "valid project-owned uploaded JAR") {
		t.Fatalf("dormant foreign binding activated via classpath: %d %s", response.Code, response.Body.String())
	}
	unchanged, err := store.GetAuthoringSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged.Draft.Config.JavaVuser.Classpath) != 0 {
		t.Fatal("rejected activation changed saved classpath")
	}
	t.Setenv(config.EnvJavaVuserClasspathUploadEnabled, "false")
	empty := cloneScriptDefinition(draft)
	empty.Config.JavaVuser.Classpath = []string{}
	if err := server.validateAuthoringJavaClasspathAssetMutation(empty, draft); err != nil {
		t.Fatalf("nil/empty unchanged inert classpath rejected: %v", err)
	}
	if err := server.validateAuthoringJavaClasspathAssetMutation(draft, empty); err != nil {
		t.Fatalf("empty/nil unchanged inert classpath rejected: %v", err)
	}
	if err := server.validateAuthoringJavaClasspathAssetMutation(candidate, draft); err == nil {
		t.Fatal("policy-off dormant classpath activation accepted")
	}
}
