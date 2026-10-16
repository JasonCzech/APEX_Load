//go:build linux

package duckdb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkerCgroupHeadroomWalksVisibleAncestors(t *testing.T) {
	type limits struct{ max, current string }
	for _, tc := range []struct {
		name   string
		member string
		files  map[string]limits // directory relative to the cgroup root
		ok     bool
		// skipDirectory leaves the member's directory uncreated; noV2 omits
		// the root's cgroup.controllers file.
		skipDirectory, noV2 bool
	}{
		{
			name:   "host root has no limit files",
			member: "/system.slice/runner.service",
			files:  map[string]limits{"system.slice/runner.service": {"max", "100"}, "system.slice": {"max", "100"}},
			ok:     true,
		},
		{
			name:   "namespaced root carries the container limit",
			member: "/",
			files:  map[string]limits{".": {"8589934592", "1073741824"}},
			ok:     true,
		},
		{
			name:   "namespaced root without headroom",
			member: "/",
			files:  map[string]limits{".": {"2147483648", "1610612736"}},
			ok:     false,
		},
		{
			name:   "ancestor without headroom",
			member: "/kubepods/pod/app",
			files:  map[string]limits{"kubepods/pod/app": {"max", "1"}, "kubepods/pod": {"1073741824", "805306368"}, "kubepods": {"max", "1"}},
			ok:     false,
		},
		{
			name:   "ancestor without the memory controller",
			member: "/system.slice/runner.service",
			files:  map[string]limits{"system.slice/runner.service": {"8589934592", "1"}},
			ok:     true,
		},
		{
			name:          "membership outside the visible hierarchy",
			member:        "/missing/runner.service",
			files:         map[string]limits{},
			ok:            false,
			skipDirectory: true,
		},
		{
			name:   "not a cgroup v2 hierarchy",
			member: "/system.slice",
			files:  map[string]limits{"system.slice": {"max", "1"}},
			ok:     false,
			noV2:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "cgroup")
			memberDir := filepath.Join(root, tc.member)
			if tc.skipDirectory {
				memberDir = root
			}
			if err := os.MkdirAll(memberDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if !tc.noV2 {
				if err := os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte("memory pids\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for rel, l := range tc.files {
				for name, value := range map[string]string{"memory.max": l.max, "memory.current": l.current} {
					if err := os.WriteFile(filepath.Join(root, rel, name), []byte(value+"\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			membership := filepath.Join(dir, "membership")
			if err := os.WriteFile(membership, []byte("0::"+tc.member+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := workerCgroupHeadroom(membership, root, 1<<30)
			if (err == nil) != tc.ok {
				t.Fatalf("headroom error = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}
