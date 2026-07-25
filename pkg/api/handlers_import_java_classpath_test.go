package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/fileasset"
)

func TestStreamJavaClasspathJarStoresAndHashes(t *testing.T) {
	root := t.TempDir()
	content := []byte("PK\x03\x04 a valid-looking jar body")
	sum := sha256.Sum256(content)
	wantDigest := hex.EncodeToString(sum[:])

	digest, size, storedPath, err := streamJavaClasspathJar(bytes.NewReader(content), root, "project-1", "import-1", 1<<20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if digest != wantDigest {
		t.Fatalf("digest = %s, want %s", digest, wantDigest)
	}
	if size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", size, len(content))
	}
	stored, err := os.ReadFile(storedPath)
	if err != nil {
		t.Fatalf("read stored jar: %v", err)
	}
	if !bytes.Equal(stored, content) {
		t.Fatalf("stored bytes differ from upload")
	}
	// The stored path must be project-fenced and readable back through fileasset.
	if _, err := fileasset.ValidatePathForProject(storedPath, "project-1"); err != nil {
		t.Fatalf("stored path not project-fenced: %v", err)
	}
	if _, err := fileasset.ValidatePathForProject(storedPath, "other-project"); err == nil {
		t.Fatal("stored path incorrectly validated for a different project")
	}
}

func TestStreamJavaClasspathJarRejectsNonJar(t *testing.T) {
	root := t.TempDir()
	_, _, _, err := streamJavaClasspathJar(bytes.NewReader([]byte("not a zip at all")), root, "project-1", "import-1", 1<<20)
	if err == nil {
		t.Fatal("expected rejection of non-jar content")
	}
	// Client-input error: neither too-large nor internal.
	if errors.Is(err, errClasspathUploadTooLarge) || errors.Is(err, errClasspathUploadInternal) {
		t.Fatalf("non-jar should be a client error, got %v", err)
	}
	if !strings.Contains(err.Error(), "not a valid jar") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestStreamJavaClasspathJarRejectsShortFile(t *testing.T) {
	root := t.TempDir()
	// 2 bytes: shorter than the 4-byte magic; must not panic and must be rejected.
	_, _, _, err := streamJavaClasspathJar(bytes.NewReader([]byte{0x50, 0x4B}), root, "project-1", "import-1", 1<<20)
	if err == nil || !strings.Contains(err.Error(), "not a valid jar") {
		t.Fatalf("expected short-file rejection, got %v", err)
	}
}

func TestStreamJavaClasspathJarEmptyRejected(t *testing.T) {
	root := t.TempDir()
	_, _, _, err := streamJavaClasspathJar(bytes.NewReader(nil), root, "project-1", "import-1", 1<<20)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty rejection, got %v", err)
	}
}

func TestStreamJavaClasspathJarTooLarge(t *testing.T) {
	root := t.TempDir()
	body := append([]byte("PK\x03\x04"), bytes.Repeat([]byte("x"), 64)...)
	_, _, _, err := streamJavaClasspathJar(bytes.NewReader(body), root, "project-1", "import-1", 8)
	if !errors.Is(err, errClasspathUploadTooLarge) {
		t.Fatalf("expected errClasspathUploadTooLarge, got %v", err)
	}
}

func TestJavaClasspathUploadMaxBytesClampedToDefault(t *testing.T) {
	// The env override may only lower the cap; a value above the default (which
	// equals the transport ceiling base and fileasset.MaxJarBytes) is clamped.
	t.Setenv("APEXLOAD_JAVA_VUSER_CLASSPATH_MAX_BYTES", "9999999999999")
	if got := javaClasspathUploadMaxBytes(); got != defaultJavaClasspathUploadMaxBytes {
		t.Fatalf("oversize override not clamped: got %d want %d", got, defaultJavaClasspathUploadMaxBytes)
	}
	if defaultJavaClasspathUploadMaxBytes > fileasset.MaxJarBytes {
		t.Fatalf("upload default %d exceeds materialization cap %d", defaultJavaClasspathUploadMaxBytes, fileasset.MaxJarBytes)
	}
	t.Setenv("APEXLOAD_JAVA_VUSER_CLASSPATH_MAX_BYTES", "1048576")
	if got := javaClasspathUploadMaxBytes(); got != 1<<20 {
		t.Fatalf("valid lower override not honored: got %d", got)
	}
}
