package generatorrpc

import "testing"

// Optional evidence keeps within a quarter of the free space above the floor
// as it stood at the first optional admission, even when the run's byte limit
// (256 GiB by default) would allow far more.
func TestGeneratorOptionalEvidenceKeepsAQuarterOfFreeSpace(t *testing.T) {
	original := optionalEvidenceFreeBytes
	t.Cleanup(func() { optionalEvidenceFreeBytes = original })
	optionalEvidenceFreeBytes = func(string) (int64, error) { return 1<<30 + 400, nil }
	budget := &generatorArchiveByteBudget{root: t.TempDir(), maxBytes: 256 << 30, minFreeBytes: 1 << 30}
	if !budget.admitOptional(60) {
		t.Fatal("optional evidence within its share was refused")
	}
	if budget.admitOptional(60) {
		t.Fatal("optional evidence past a quarter of the free space was admitted")
	}
	budget.releaseOptional(60)
	if !budget.admitOptional(60) {
		t.Fatal("a released share was not admitted again")
	}
}
