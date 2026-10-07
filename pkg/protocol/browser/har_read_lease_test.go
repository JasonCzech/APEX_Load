package browser

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Reading a published HAR holds the HAR directory's lease only to open and
// prove the file: while the copy streams, other sessions in the directory can
// take the lease (their closes wait on it).
func TestReadRuntimeHARArtifactStreamsOutsideTheLease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "apexload_browser_"+strings.Repeat("c", 32)+"_scrubbed.har")
	if err := os.WriteFile(path, []byte(`{"log":{"entries":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := ReadRuntimeHARArtifact(context.Background(), path, func(r io.Reader, _ int64) error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := WithRuntimeHARArtifactNamespace(ctx, dir, func(*os.Root) error { return nil }); err != nil {
			return err
		}
		_, err := io.ReadAll(r)
		return err
	})
	if err != nil || identity == nil {
		t.Fatalf("the lease was held while the HAR streamed: %v", err)
	}
}
