package enterprisearchive

import "testing"

func TestJavaSourcePathsUseArchiveSemanticsOnEveryHost(t *testing.T) {
	t.Parallel()
	if left, right := javaNormalizedSourcePath(`src\com\example\Actions.java`), javaNormalizedSourcePath("src/com/example/Actions.java"); left != right {
		t.Fatalf("Windows path %q != slash path %q", left, right)
	}
	if rank := javaSourcePathMatchRank(`src\com\example\Actions.java`, "src/com/example/Actions.java"); rank != 0 {
		t.Fatalf("match rank = %d", rank)
	}
}
