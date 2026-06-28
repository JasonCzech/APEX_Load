package enterprisearchive

import (
	"reflect"
	"testing"
)

func TestSplitJavaArgsPreservesGenericTypeCommas(t *testing.T) {
	got := splitJavaArgs(`new java.util.HashMap<String, java.util.List<Integer>>(), "tail"`)
	want := []string{`new java.util.HashMap<String, java.util.List<Integer>>()`, `"tail"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitJavaArgs() = %#v, want %#v", got, want)
	}
}
