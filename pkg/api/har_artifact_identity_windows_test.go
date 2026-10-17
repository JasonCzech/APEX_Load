//go:build windows

package api

import (
	"os"
	"testing"

	"github.com/jasonczech/apexload/pkg/internal/winfs"
)

// harArtifactTestIdentity is the persistent identity a completed HAR's
// retirement record binds on Windows, read from a handle: the volume serial
// number, the 128-bit file ID and the owner SID, with uid 0.
func harArtifactTestIdentity(t *testing.T, root *os.Root, name string) map[string]any {
	t.Helper()
	file, err := root.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	id, err := winfs.FileID(file)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := winfs.Owner(file)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"device": id.Volume, "inode": id.Low, "file_id_high": id.High, "owner": owner, "uid": 0}
}
