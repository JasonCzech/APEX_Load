//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type gcDuringSaveReader struct {
	source    io.Reader
	collected bool
}

func (r *gcDuringSaveReader) Read(p []byte) (int, error) {
	if !r.collected {
		r.collected = true
		// A staging descriptor must remain owned throughout the caller's write,
		// including GC after acquireTempAt has returned and before publication.
		for range 5 {
			runtime.GC()
			time.Sleep(10 * time.Millisecond)
		}
	}
	return r.source.Read(p)
}

func TestReusedSaveFileRetainsDescriptorOwnershipAcrossGC(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "new destination"
		if existing {
			name = "existing destination"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			dst := filepath.Join(dir, "result.json")
			if existing {
				if err := os.WriteFile(dst, []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			tombstone := filepath.Join(dir, ".apexload-save-00000000000000000000000000000000.tmp")
			if err := os.WriteFile(tombstone, nil, 0600); err != nil {
				t.Fatal(err)
			}
			probe, err := os.OpenFile(tombstone, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			cleanErr := unixSaveTombstoneMetadataClean(int(probe.Fd()))
			identity, err := probe.Stat()
			if err != nil {
				t.Fatal(err)
			}
			if err := probe.Close(); err != nil {
				t.Fatal(err)
			}
			if cleanErr != nil {
				t.Skipf("verified tombstone reuse unavailable: %v", cleanErr)
			}
			_, grant, err := newWritePathGrant(dst)
			if err != nil {
				t.Fatal(err)
			}
			source := &gcDuringSaveReader{source: strings.NewReader("saved")}
			if err := secureWriteGrantedFileFrom(grant, source, 5); err != nil {
				t.Fatalf("GC invalidated the staging descriptor: %v", err)
			}
			data, err := os.ReadFile(dst)
			if err != nil || string(data) != "saved" {
				t.Fatalf("saved data=%q error=%v", data, err)
			}
			published, err := os.Stat(dst)
			if err != nil || !os.SameFile(identity, published) {
				t.Fatal("fixture did not reuse the tombstone inode")
			}
		})
	}
}
