package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
)

func supplementalTestZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	return enterpriseCollectionTestZIP(t, files)
}

type supplementalTestUpload struct {
	field, name string
	body        []byte
}

func supplementalTestMultipart(t *testing.T, route string, uploads []supplementalTestUpload, fields map[string]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, upload := range uploads {
		part, err := writer.CreateFormFile(upload.field, upload.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(upload.body); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, route, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func TestEnterpriseSupplementalZIPPreservesLogicalPathsAndByteProvenance(t *testing.T) {
	primary := supplementalTestZIP(t, map[string][]byte{
		"Project/Demo.usr":     []byte("[General]\nType=Java\n"),
		"Project/Actions.java": []byte("public class Actions { public int action(){ return 0; } }"),
	})
	binary := []byte{'P', 'K', 3, 4, 0, 0xff, 0xfe}
	dependency := supplementalTestZIP(t, map[string][]byte{
		"libs/client.jar": binary, "Infra/BasicClient.class": {0xca, 0xfe, 0xba, 0xbe},
		"Infra/BasicClient.txt": []byte("public class BasicClient {}"), "tools/helper.exe": {'M', 'Z', 0, 0xff},
		"empty.txt": {},
	})
	merged, manifest, err := mergeEnterpriseArchiveZipWithSupplementalEvidence(primary, []string{"dependencies.zip"}, [][]byte{dependency})
	if err != nil {
		t.Fatal(err)
	}
	files, err := archiveingest.ReadZIP(merged, archiveingest.Options{StripFirstComponent: true})
	if err != nil {
		t.Fatal(err)
	}
	actual := map[string][]byte{}
	for _, file := range files {
		actual[file.Name] = file.Data
	}
	if !bytes.Equal(actual["libs/client.jar"], binary) || !bytes.Equal(actual["tools/helper.exe"], []byte{'M', 'Z', 0, 0xff}) {
		t.Fatal("binary dependency bytes or relative paths changed")
	}
	if _, exists := actual["Demo.usr"]; !exists {
		t.Fatal("supplemental ZIP changed primary logical root")
	}
	if _, exists := actual["empty.txt"]; !exists {
		t.Fatal("empty evidence file was omitted")
	}
	if manifest == nil || manifest.PrimarySHA256 != supplementalArchiveSHA256(primary) || manifest.PrimarySizeBytes != int64(len(primary)) || len(manifest.Bundles) != 1 {
		t.Fatalf("primary provenance mismatch: %#v", manifest)
	}
	bundle := manifest.Bundles[0]
	if bundle.SourceName != "dependencies.zip" || bundle.SizeBytes != int64(len(dependency)) || bundle.SHA256 != supplementalArchiveSHA256(dependency) || len(bundle.Files) != 5 {
		t.Fatalf("bundle provenance mismatch: %#v", bundle)
	}
	for _, member := range bundle.Files {
		body, present := actual[member.LogicalPath]
		if !present || member.SourcePath != member.LogicalPath || member.ArchivePath != "Project/"+member.SourcePath || member.SHA256 != supplementalArchiveSHA256(body) || member.SizeBytes != int64(len(body)) {
			t.Fatalf("member provenance mismatch: %#v", member)
		}
	}
}

func TestEnterpriseSupplementalZIPRejectsAmbiguousUnsafeAndConflictingBundles(t *testing.T) {
	primary := supplementalTestZIP(t, map[string][]byte{"Root/Demo.usr": []byte("project"), "Root/Actions.java": []byte("primary")})
	for _, tc := range []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{"primary source", map[string][]byte{"Actions.java": []byte("replace")}, "collides with primary"},
		{"primary case", map[string][]byte{"actions.JAVA": []byte("replace")}, "collides with primary"},
		{"script descriptor", map[string][]byte{"child/Demo.usr": []byte("project")}, "script project"},
		{"USZ project", map[string][]byte{"child.USZ": []byte("PK")}, "script project"},
		{"nested ZIP", map[string][]byte{"child.zip": []byte("PK")}, "nested supplemental"},
		{"traversal", map[string][]byte{"../helper.java": []byte("unsafe")}, "unsafe"},
		{"case duplicate", map[string][]byte{"libs/a.jar": []byte("a"), "libs/A.jar": []byte("b")}, "colliding"},
		{"Unicode duplicate", map[string][]byte{"libs/café.jar": []byte("a"), "libs/cafe\u0301.jar": []byte("b")}, "colliding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := mergeEnterpriseArchiveZipWithSupplementalEvidence(primary, []string{"dependencies.zip"}, [][]byte{supplementalTestZIP(t, tc.files)})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}
	one := supplementalTestZIP(t, map[string][]byte{"libs/a.jar": []byte("a")})
	two := supplementalTestZIP(t, map[string][]byte{"libs/A.jar": []byte("b")})
	for _, contents := range [][][]byte{{one, two}, {two, one}} {
		if _, _, err := mergeEnterpriseArchiveZipWithSupplementalEvidence(primary, []string{"one.zip", "two.zip"}, contents); err == nil {
			t.Fatal("cross-bundle collision accepted")
		}
	}
	for _, names := range [][]string{{"direct.jar", "dependencies.zip"}, {"dependencies.zip", "direct.jar"}} {
		contents := [][]byte{[]byte("direct"), supplementalTestZIP(t, map[string][]byte{"direct.jar": []byte("bundle")})}
		if strings.HasSuffix(names[0], ".zip") {
			contents[0], contents[1] = contents[1], contents[0]
		}
		if _, _, err := mergeEnterpriseArchiveZipWithSupplementalEvidence(primary, names, contents); err == nil {
			t.Fatal("bundle/direct collision accepted")
		}
	}
}

func TestEnterpriseSupplementalZIPCombinedBudgets(t *testing.T) {
	primary := supplementalTestZIP(t, map[string][]byte{"Demo.usr": []byte("123"), "Actions.java": []byte("123")})
	bundle := supplementalTestZIP(t, map[string][]byte{"a.jar": []byte("12345"), "b.class": []byte("12345")})
	limits := archiveingest.Limits{MaxEntries: 4, MaxEntryBytes: 8, MaxTotalBytes: 16, MaxDepth: 4}
	if _, _, err := prepareEnterpriseSupplementalArchivesWithLimits(primary, []string{"deps.zip"}, [][]byte{bundle}, limits); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		limits archiveingest.Limits
	}{
		{"entry count", archiveingest.Limits{MaxEntries: 3, MaxEntryBytes: 8, MaxTotalBytes: 16, MaxDepth: 4}},
		{"combined bytes", archiveingest.Limits{MaxEntries: 4, MaxEntryBytes: 8, MaxTotalBytes: 15, MaxDepth: 4}},
		{"member bytes", archiveingest.Limits{MaxEntries: 4, MaxEntryBytes: 4, MaxTotalBytes: 16, MaxDepth: 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := prepareEnterpriseSupplementalArchivesWithLimits(primary, []string{"deps.zip"}, [][]byte{bundle}, tc.limits); err == nil || !strings.Contains(err.Error(), "limit") {
				t.Fatalf("error=%v", err)
			}
		})
	}
	other := supplementalTestZIP(t, map[string][]byte{"c.txt": []byte("1")})
	if _, _, err := prepareEnterpriseSupplementalArchivesWithLimits(primary, []string{"deps.zip", "other.zip"}, [][]byte{bundle, other}, limits); err == nil {
		t.Fatal("each bundle reset combined budget")
	}
	if _, _, err := prepareEnterpriseSupplementalArchivesWithLimits(primary, []string{"deps.zip", "other.txt"}, [][]byte{bundle, []byte("1")}, limits); err == nil {
		t.Fatal("direct file bypassed combined budget")
	}
}

func TestEnterpriseSupplementalZIPDirectHelperKeepsPrimaryContainer(t *testing.T) {
	primary := supplementalTestZIP(t, map[string][]byte{"Root/Demo.usr": []byte("project"), "Root/Actions.java": []byte("source")})
	merged, err := mergeEnterpriseArchiveZipWithSupplementalFiles(primary, []string{"BasicClient.java"}, [][]byte{[]byte("helper")})
	if err != nil {
		t.Fatal(err)
	}
	files, err := archiveingest.ReadZIP(merged, archiveingest.Options{StripFirstComponent: true})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range files {
		names = append(names, file.Name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"Actions.java", "BasicClient.java", "Demo.usr"}) {
		t.Fatalf("logical paths=%v", names)
	}
}

func TestEnterpriseSupplementalZIPImportAndValidatePublishProvenance(t *testing.T) {
	primary := supplementalTestZIP(t, map[string][]byte{
		"Root/Demo.usr":     []byte("[General]\nType=Java\n[Actions]\nAction=Actions.java\n"),
		"Root/Actions.java": []byte("public class Actions extends BasicClient { public int action(){ return 0; } }"),
	})
	bundle := supplementalTestZIP(t, map[string][]byte{
		"Infra/BasicClient.txt": []byte("public class BasicClient { public BasicClient(){} }"),
		"libs/client.jar":       {'P', 'K', 3, 4, 0, 0xff}, "tools/helper.exe": {'M', 'Z', 0, 0xff},
	})
	server := NewServer(ServerConfig{})
	for _, suffix := range []string{"", "/validate"} {
		t.Run(suffix, func(t *testing.T) {
			req := supplementalTestMultipart(t, "/api/v1/scripts/import/enterprise-script-archive"+suffix, []supplementalTestUpload{{"file", "primary.zip", primary}, {"supplemental_file", "dependencies.zip", bundle}}, nil)
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
			}
			var payload struct {
				Script         *dsl.Script `json:"script"`
				ReviewArtifact struct {
					Metadata map[string]json.RawMessage `json:"metadata"`
				} `json:"review_artifact"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Script == nil || payload.Script.Protocol != "java_vuser" || payload.Script.Config.JavaVuser.Enabled || len(payload.Script.Config.JavaVuser.ClasspathAssets) != 0 {
				t.Fatal("dependency expansion implicitly provisioned runner input")
			}
			encoded, err := json.Marshal(payload.Script.Metadata.Import.Preserved[enterpriseSupplementalArchiveManifestKey])
			if err != nil {
				t.Fatal(err)
			}
			var manifest enterpriseSupplementalArchiveManifest
			if err := json.Unmarshal(encoded, &manifest); err != nil {
				t.Fatal(err)
			}
			if len(manifest.Bundles) != 1 || len(manifest.Bundles[0].Files) != 3 || manifest.Bundles[0].SHA256 != supplementalArchiveSHA256(bundle) {
				t.Fatalf("saved-draft provenance=%s", encoded)
			}
			if len(payload.ReviewArtifact.Metadata[enterpriseSupplementalArchiveManifestKey]) == 0 {
				t.Fatal("review artifact missing supplemental provenance")
			}
			if bytes.Contains(resp.Body.Bytes(), []byte(`"content":"MZ`)) {
				t.Fatal("executable bytes appeared as source text")
			}
		})
	}
}

// ZIP iteration order is allowed to differ; each member must remain byte exact.
func TestEnterpriseSupplementalZIPJARRemainsOpaque(t *testing.T) {
	primary := supplementalTestZIP(t, map[string][]byte{"Demo.usr": []byte("project")})
	jar := supplementalTestZIP(t, map[string][]byte{"nested/Demo.usr": []byte("resource, not another script")})
	bundle := supplementalTestZIP(t, map[string][]byte{"lib.jar": jar})
	merged, _, err := mergeEnterpriseArchiveZipWithSupplementalEvidence(primary, []string{"deps.zip"}, [][]byte{bundle})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(merged), int64(len(merged)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		if file.Name == "lib.jar" {
			r, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(r)
			r.Close()
			if err != nil || !bytes.Equal(body, jar) {
				t.Fatal("JAR was expanded or changed")
			}
			return
		}
	}
	t.Fatal("JAR member omitted")
}

func TestEnterpriseSupplementalZIPRejectsPrimaryNonregularMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		mode fs.FileMode
	}{
		{"symlink", "Root/Actions.java", fs.ModeSymlink | 0o777},
		{"named pipe", "Root/Actions.java", fs.ModeNamedPipe | 0o600},
		{"directory metadata", "Root/Actions.java", fs.ModeDir | 0o755},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := zip.NewWriter(&body)
			project, err := writer.Create("Root/Demo.usr")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = project.Write([]byte("[General]\nType=Java\n"))
			header := &zip.FileHeader{Name: tc.path}
			header.SetMode(tc.mode)
			entry, err := writer.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = entry.Write([]byte("public class Actions { public int action(){ return 0; } }"))
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			primary := body.Bytes()
			if _, err := enterprisearchive.ConvertZip(primary, enterprisearchive.ConvertOptions{}); err == nil {
				t.Fatal("bare converter accepted nonregular source")
			}
			for _, dependency := range []struct {
				name string
				data []byte
			}{
				{"dependencies.zip", supplementalTestZIP(t, map[string][]byte{"libs/client.jar": []byte("PK\x03\x04inert")})},
				{"Actions.java", []byte("public class Actions { public int action(){ return 0; } }")},
			} {
				if _, _, err := mergeEnterpriseArchiveZipWithSupplementalEvidence(primary, []string{dependency.name}, [][]byte{dependency.data}); err == nil {
					t.Fatal("supplemental merge regularized nonregular primary source")
				}
				for _, suffix := range []string{"", "/validate"} {
					server := NewServer(ServerConfig{})
					request := supplementalTestMultipart(t, "/api/v1/scripts/import/enterprise-script-archive"+suffix, []supplementalTestUpload{{"file", "primary.zip", primary}, {"supplemental_file", dependency.name, dependency.data}}, nil)
					response := httptest.NewRecorder()
					server.mux.ServeHTTP(response, request)
					if response.Code != http.StatusBadRequest {
						t.Fatalf("supplemental %s route %s status=%d", dependency.name, suffix, response.Code)
					}
				}
			}
		})
	}
}

func TestEnterpriseSupplementalZIPRejectsUnsupportedPrimaryHeaderFlags(t *testing.T) {
	for _, tc := range []struct {
		name    string
		flags   uint16
		nonUTF8 bool
	}{
		{name: "encrypted", flags: 1},
		{name: "non-UTF8 name", nonUTF8: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := zip.NewWriter(&body)
			name := "Actions.java"
			if tc.nonUTF8 {
				name = "Actions\xff.java"
			}
			header := &zip.FileHeader{Name: name, Flags: tc.flags, NonUTF8: tc.nonUTF8}
			entry, err := writer.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = entry.Write([]byte("public class Actions {}"))
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			// Replacing the bytes must not bypass unsupported source metadata.
			if _, _, err := mergeEnterpriseArchiveZipWithSupplementalEvidence(body.Bytes(), []string{"Actions.java"}, [][]byte{[]byte("public class Actions {}")}); err == nil {
				t.Fatal("merge discarded unsupported source header metadata")
			}
		})
	}
}
