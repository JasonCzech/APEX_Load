package bruno

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

func TestInspectRejectsUnsafeArchivePathsAndSymlinks(t *testing.T) {
	var zipBytes bytes.Buffer
	zw := zip.NewWriter(&zipBytes)
	entry, err := zw.Create("../escape.yml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("info: {name: bad, type: http}\nhttp: {url: https://example.test}\n"))
	_ = zw.Close()
	if _, err := Inspect(Input{Filename: "bad.zip", Content: zipBytes.Bytes()}); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("unsafe ZIP error = %v", err)
	}

	var tarBytes bytes.Buffer
	tw := tar.NewWriter(&tarBytes)
	if err := tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	if _, err := Inspect(Input{Filename: "bad.tar", Content: tarBytes.Bytes()}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink TAR error = %v", err)
	}
}

func TestInspectRejectsUnicodeNormalizedPathCollisions(t *testing.T) {
	_, err := Inspect(Input{Files: map[string][]byte{
		"Caf\u00e9.yml":  []byte("info: {name: A, type: http}\nhttp: {url: https://a.test}\n"),
		"Cafe\u0301.yml": []byte("info: {name: B, type: http}\nhttp: {url: https://b.test}\n"),
	}})
	if err == nil || !strings.Contains(err.Error(), "collide after normalization") {
		t.Fatalf("Unicode collision error = %v", err)
	}
}

func TestInspectRejectsPathsThatWouldBeSilentlyTrimmed(t *testing.T) {
	_, err := Inspect(Input{Files: map[string][]byte{
		" request.yml": []byte("info: {name: Request, type: http}\nhttp: {url: https://example.test}\n"),
	}})
	if err == nil || !strings.Contains(err.Error(), "leading or trailing whitespace") {
		t.Fatalf("whitespace path error = %v", err)
	}
}

func TestInspectRejectsDotComponentsAndDriveQualifiedPaths(t *testing.T) {
	for _, unsafePath := range []string{
		"a/../request.yml",
		"./request.yml",
		"C:/request.yml",
		`C:\request.yml`,
	} {
		t.Run(unsafePath, func(t *testing.T) {
			_, err := Inspect(Input{Files: map[string][]byte{
				unsafePath: []byte("info: {name: Request, type: http}\nhttp: {url: https://example.test}\n"),
			}})
			if err == nil {
				t.Fatalf("Inspect accepted unsafe path %q", unsafePath)
			}
			lower := strings.ToLower(err.Error())
			if !strings.Contains(lower, "dot or traversal") &&
				!strings.Contains(lower, "drive-qualified") {
				t.Fatalf("unsafe path %q error = %v", unsafePath, err)
			}
		})
	}
}

func TestInspectRejectsCompressedTARExpansionBomb(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range map[string][]byte{
		"request.yml": []byte("info: {name: Safe, type: http}\nhttp: {url: https://example.test}\n"),
		"fixture.bin": bytes.Repeat([]byte{0}, 64<<10),
	} {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Inspect(Input{
		Filename: "bomb.tar.gz",
		Content:  archive.Bytes(),
		Limits:   Limits{MaxCompressionRatio: 2},
	})
	if err == nil || !strings.Contains(err.Error(), "compression-ratio limit") {
		t.Fatalf("compression bomb error = %v", err)
	}
}

func TestDeterministicBundle(t *testing.T) {
	input := Input{Files: map[string][]byte{
		"b.yml": []byte("info: {name: B, type: http}\nhttp: {url: https://b.test}\n"),
		"a.yml": []byte("info: {name: A, type: http}\nhttp: {url: https://a.test}\n"),
	}}
	first, err := Inspect(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Inspect(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.SanitizedArchive, second.SanitizedArchive) || first.Manifest.BundleSHA256 != second.Manifest.BundleSHA256 {
		t.Fatal("bundle output is not deterministic")
	}
	reader := tar.NewReader(bytes.NewReader(first.SanitizedArchive))
	var names []string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
	if strings.Join(names, ",") != "a.yml,b.yml" {
		t.Fatalf("tar order = %v", names)
	}
}

func TestSecretNameAlwaysProducesPortableBoundedReference(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		key      string
	}{
		{name: "unicode input", filename: "顧客/ログイン.bru", key: "pässwörd"},
		{name: "punctuation only", filename: "🔥.bru", key: "🔑"},
		{name: "long input", filename: strings.Repeat("collection-", 40) + ".bru", key: strings.Repeat("token", 80)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := secretName(test.filename, test.key)
			if got == "" || len(got) > credential.MaxLogicalNameBytes {
				t.Fatalf("secretName() = %q (%d bytes)", got, len(got))
			}
			for _, r := range got {
				if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
					t.Fatalf("secretName() = %q contains non-portable rune %q", got, r)
				}
			}
			if got != secretName(test.filename, test.key) {
				t.Fatalf("secretName() is not deterministic: %q", got)
			}
		})
	}
}
