//go:build !windows

package javasidecar

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPooledManifestRejectsFIFOWithoutWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPooledPilotManifest(context.Background(), path, 25); err == nil {
		t.Fatal("FIFO accepted")
	}
	f, err := openPooledManifestFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatal("unexpected FIFO descriptor", err)
	}
}
