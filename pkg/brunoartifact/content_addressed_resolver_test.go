package brunoartifact

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func TestContentAddressedResolverOpensSanitizedBundleByProjectAndDigest(t *testing.T) {
	wrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{Key: bytes.Repeat([]byte{0x42}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	objects, err := NewEncryptedStore(StoreOptions{RootDir: t.TempDir(), Wrapper: wrapper})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("sanitized Bruno bundle")
	stored, err := objects.Put(context.Background(), "project-a", "sanitized", bytes.NewReader(content), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &ContentAddressedResolver{Objects: objects, Kind: "sanitized"}
	reader, size, err := resolver.OpenBrunoArtifact(context.Background(), Request{
		Reference: "artifact://opaque-id",
		ProjectID: "project-a",
		SHA256:    stored.SHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(content)) || !bytes.Equal(got, content) {
		t.Fatalf("resolved artifact = size:%d content:%q", size, got)
	}
}

func TestContentAddressedResolverRequiresDescriptorDigest(t *testing.T) {
	resolver := &ContentAddressedResolver{Objects: &EncryptedStore{}, Kind: "sanitized"}
	if _, _, err := resolver.OpenBrunoArtifact(context.Background(), Request{
		Reference: "artifact://opaque-id",
		ProjectID: "project-a",
	}); err == nil {
		t.Fatal("resolver accepted a descriptor without a SHA-256 digest")
	}
}
