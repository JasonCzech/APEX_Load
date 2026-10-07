package api

import "testing"

// Optional evidence keeps within a quarter of the free space above the floor
// (as it stood at the first optional admission) on the controller's own
// capture and in an archive merge, so required writes keep the rest.
func TestOptionalEvidenceKeepsAQuarterOfFreeSpace(t *testing.T) {
	original := optionalEvidenceFreeBytes
	t.Cleanup(func() { optionalEvidenceFreeBytes = original })
	optionalEvidenceFreeBytes = func(string) (int64, error) { return 64<<20 + 400, nil }
	capture := &archiveCaptureBudget{root: t.TempDir(), remaining: 32 << 30, minFree: 64 << 20}
	if !capture.admitOptional(60) || capture.admitOptional(60) {
		t.Fatal("capture: optional evidence not held to a quarter of the free space")
	}
	capture.releaseOptional(60)
	if !capture.admitOptional(60) {
		t.Fatal("capture: a released share was not admitted again")
	}
	merge := &archiveOutputBudget{root: t.TempDir(), remaining: 1 << 30, minFree: 64 << 20}
	if !merge.admitOptional(60) || merge.admitOptional(60) {
		t.Fatal("merge: optional evidence not held to a quarter of the free space")
	}
}
