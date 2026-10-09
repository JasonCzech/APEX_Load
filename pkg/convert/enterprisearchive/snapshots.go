package enterprisearchive

import (
	"fmt"
	"strings"
)

// VuGen keeps each recorded step's snapshot (the recorded request and
// response the correlation scan compares against) in the script's data/
// folder; steps name theirs with Snapshot=tN.inf. An archive exported
// without "all files" has no data/ folder.
//
// The snapshot file layout has not been confirmed against a real all-files
// export (an open item of the parity plan, WP4.1), so snapshots are not read
// yet; the review says whether they are there.

// recordingSnapshotNote is the one-line review note on the archive's
// recording snapshots, or "" when no step records one.
func recordingSnapshotNote(files map[string]string, actions []*ParsedAction) string {
	steps := 0
	for _, action := range actions {
		if action == nil {
			continue
		}
		for _, fn := range action.Functions {
			if snapshot := firstNonEmptyArg(fn, "Snapshot"); strings.HasSuffix(strings.ToLower(strings.TrimSpace(snapshot)), ".inf") {
				steps++
			}
		}
	}
	if steps == 0 {
		return ""
	}
	snapshotFiles := 0
	for name := range files {
		lower := strings.ToLower(strings.ReplaceAll(name, "\\", "/"))
		if strings.HasPrefix(lower, "data/") || strings.Contains(lower, "/data/") {
			snapshotFiles++
		}
	}
	if snapshotFiles == 0 {
		return fmt.Sprintf("The archive has no VuGen recording snapshots (data/ folder) for its %d recorded step(s); APEX does not read snapshots yet, so the import is not affected.", steps)
	}
	return fmt.Sprintf("The archive includes VuGen recording snapshots (%d file(s) under data/ for %d recorded step(s)); they are not read yet because their file layout has not been confirmed against a real export.", snapshotFiles, steps)
}
