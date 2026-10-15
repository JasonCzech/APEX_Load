package runtimetuning

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCgroupMemoryLimitAt(t *testing.T) {
	write := func(t *testing.T, root, name, value string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	v2 := t.TempDir()
	write(t, v2, "memory.max", "2147483648")
	if limit, ok := cgroupMemoryLimitAt(v2); !ok || limit != 2<<30 {
		t.Fatalf("v2 limit %d %v", limit, ok)
	}
	unlimited := t.TempDir()
	write(t, unlimited, "memory.max", "max")
	write(t, unlimited, "memory/memory.limit_in_bytes", "1073741824")
	if _, ok := cgroupMemoryLimitAt(unlimited); ok {
		t.Fatal("unlimited v2 cgroup reported a limit")
	}
	v1 := t.TempDir()
	write(t, v1, "memory/memory.limit_in_bytes", "1073741824")
	if limit, ok := cgroupMemoryLimitAt(v1); !ok || limit != 1<<30 {
		t.Fatalf("v1 limit %d %v", limit, ok)
	}
	write(t, v1, "memory/memory.limit_in_bytes", "9223372036854771712")
	if _, ok := cgroupMemoryLimitAt(v1); ok {
		t.Fatal("unlimited v1 cgroup reported a limit")
	}
	if _, ok := cgroupMemoryLimitAt(t.TempDir()); ok {
		t.Fatal("missing cgroup reported a limit")
	}
}
