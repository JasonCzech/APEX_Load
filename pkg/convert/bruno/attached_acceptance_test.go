package bruno

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"
)

func tarFile(t *testing.T, archive []byte, name string) []byte {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			t.Fatalf("archive does not contain %q", name)
		}
		if err != nil {
			t.Fatalf("read archive: %v", err)
		}
		if header.Name != name {
			continue
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read archive member %q: %v", name, err)
		}
		return content
	}
}
