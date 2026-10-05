//go:build windows

package duckdb

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestWorkerWindowsEnvironmentStrictSortedPrivateBlock(t *testing.T) {
	for _, tc := range []struct {
		name            string
		input, expected []string
		bad             bool
	}{
		{name: "sorted", input: []string{"SystemRoot=C:\\Windows", "LANG=C.UTF-8", "TEMP=C:\\private", "APEXLOAD_NATIVE_ROOT_HANDLE=13"}, expected: []string{"APEXLOAD_NATIVE_ROOT_HANDLE=13", "LANG=C.UTF-8", "SystemRoot=C:\\Windows", "TEMP=C:\\private"}},
		{name: "empty", input: nil, expected: nil},
		{name: "alias", input: []string{"TEMP=a", "Temp=b"}, bad: true},
		{name: "duplicate", input: []string{"TEMP=a", "TEMP=b"}, bad: true},
		{name: "emptyname", input: []string{"=value"}, bad: true},
		{name: "non-ascii-name", input: []string{"TÉMP=a"}, bad: true},
		{name: "embeddednul", input: []string{"TEMP=a\x00b"}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := workerWindowsEnvironmentBlock(tc.input)
			if tc.bad {
				if err == nil {
					t.Fatal("invalid environment admitted")
				}
				return
			}
			if err != nil || len(output) < 2 || output[len(output)-1] != 0 || output[len(output)-2] != 0 {
				t.Fatal("invalid termination", err)
			}
			text := string(utf16.Decode(output))
			var actual []string
			if trimmed := strings.TrimSuffix(text, "\x00\x00"); trimmed != "" {
				actual = strings.Split(trimmed, "\x00")
			}
			if !reflect.DeepEqual(actual, tc.expected) {
				t.Fatalf("ordering differs: %#v", actual)
			}
		})
	}
}
