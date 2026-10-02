package sourceview

import (
	"strings"
	"testing"
)

func TestSourceReviewNeverPreviewsOpaqueAssetsEvenWhenMapped(t *testing.T) {
	files := map[string]string{"client.jar": "PK\x03\x04private-binary", "helper.class": "\xca\xfe\xba\xbeprivate-binary", "jad.exe": "MZ" + strings.Repeat("x", 64), "state.store": "private-state", "output.db": "private-data", "innocent.txt": "PK\x03\x04private-disguised-binary", "unknown": "SQLite format 3\x00private-data", "script.java": "class Example {}"}
	for _, mappings := range [][]Mapping{nil, {{SourceFile: "client.jar"}, {SourceFile: "helper.class"}, {SourceFile: "state.store"}, {SourceFile: "innocent.txt"}}} {
		review := Build(files, mappings)
		if len(review.SourceFiles) != 1 || review.SourceFiles[0].Path != "script.java" || review.TotalSourceFiles != 1 {
			t.Fatal("binary assets became source previews")
		}
		if strings.Contains(review.SourceFiles[0].Content, "private-") {
			t.Fatal("opaque bytes leaked")
		}
	}
}

func TestOpaqueAssetGuardPreservesRecognizedUTF16Source(t *testing.T) {
	for _, name := range []string{"source.xml", "source.txt", "source.java"} {
		content := "\xff\xfe" + "<\x00x\x00/\x00>\x00"
		if IsOpaqueAsset(name, content) {
			t.Fatalf("recognized BOM source %s was excluded", name)
		}
	}
	if IsOpaqueAsset("default.xml", "<\x00t\x00r\x00u\x00S\x00c\x00r\x00i\x00p\x00t\x00/\x00>\x00") {
		t.Fatal("recognized TruClient signature excluded")
	}
	if !IsOpaqueAsset("other.txt", "not\x00recognized") {
		t.Fatal("unrecognized NUL-bearing input became source")
	}
	if IsOpaqueAsset("plain.txt", "a normal plain source line") {
		t.Fatal("ordinary text excluded")
	}
}

func TestSourceReviewKeepsCertificateAndKeyAssetsInert(t *testing.T) {
	for _, ext := range []string{"pem", "crt", "cer", "p7b", "p7c", "key"} {
		name := "identity." + ext
		files := map[string]string{name: "-----BEGIN PRIVATE KEY-----\nsynthetic-key-canary\n-----END PRIVATE KEY-----", "source.java": "class Example {}"}
		review := Build(files, []Mapping{{SourceFile: name}})
		if !IsOpaqueAsset(name, files[name]) || len(review.SourceFiles) != 1 || review.SourceFiles[0].Path != "source.java" {
			t.Fatal("certificate/key material became source text")
		}
	}
}
