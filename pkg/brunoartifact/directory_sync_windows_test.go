//go:build windows

package brunoartifact

import (
	"bytes"
	"errors"
	"testing"
)

func TestWritableEncryptedStoreFailsClosedWithoutDirectorySync(t *testing.T) {
	wrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{
		Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "windows-durability-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewEncryptedStore(StoreOptions{
		RootDir: t.TempDir(),
		Wrapper: wrapper,
	}); !errors.Is(err, errDirectorySyncUnsupported) {
		t.Fatalf("NewEncryptedStore error = %v, want errDirectorySyncUnsupported", err)
	}
}
