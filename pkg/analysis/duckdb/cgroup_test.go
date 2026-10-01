package duckdb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContainerMemoryHeadroom(t *testing.T) {
	for _, tc := range []struct {
		name, limit, used string
		fail              bool
	}{
		{"headroom", "2147483648", "1073741824", false},
		{"low", "2147483648", "1879048192", true},
		{"over", "2147483648", "3147483648", true},
		{"unlimited", "max", "", false},
		{"missing_usage", "2147483648", "", true},
		{"invalid_limit", "invalid", "1", true},
		{"invalid_usage", "2147483648", "bad", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "memory.max"), []byte(tc.limit), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.used != "" {
				if err := os.WriteFile(filepath.Join(root, "memory.current"), []byte(tc.used), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := cgroupHeadroom(root); (err != nil) != tc.fail {
				t.Fatalf("headroom = %v", err)
			}
		})
	}
	if err := cgroupHeadroom(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
