package runtimetuning

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveFromCgroupV2Limits(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cpu.max"), []byte("250000 100000\n"), 0o600); err != nil {
		t.Fatalf("write cpu.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "memory.max"), []byte("1000000000\n"), 0o600); err != nil {
		t.Fatalf("write memory.max: %v", err)
	}

	settings := ResolveFromCgroup(func(string) string { return "" }, root)

	if settings.GOMAXPROCS != 3 {
		t.Fatalf("GOMAXPROCS = %d, want 3", settings.GOMAXPROCS)
	}
	if settings.GOMEMLIMIT != 900000000 {
		t.Fatalf("GOMEMLIMIT = %d, want 900000000", settings.GOMEMLIMIT)
	}
}

func TestResolveFromCgroupV1Limits(t *testing.T) {
	root := t.TempDir()
	cpuDir := filepath.Join(root, "cpu")
	memoryDir := filepath.Join(root, "memory")
	if err := os.MkdirAll(cpuDir, 0o700); err != nil {
		t.Fatalf("mkdir cpu dir: %v", err)
	}
	if err := os.MkdirAll(memoryDir, 0o700); err != nil {
		t.Fatalf("mkdir memory dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_quota_us"), []byte("200000\n"), 0o600); err != nil {
		t.Fatalf("write quota: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_period_us"), []byte("100000\n"), 0o600); err != nil {
		t.Fatalf("write period: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memoryDir, "memory.limit_in_bytes"), []byte("500000000\n"), 0o600); err != nil {
		t.Fatalf("write memory limit: %v", err)
	}

	settings := ResolveFromCgroup(func(string) string { return "" }, root)

	if settings.GOMAXPROCS != 2 {
		t.Fatalf("GOMAXPROCS = %d, want 2", settings.GOMAXPROCS)
	}
	if settings.GOMEMLIMIT != 450000000 {
		t.Fatalf("GOMEMLIMIT = %d, want 450000000", settings.GOMEMLIMIT)
	}
}

func TestResolveFromCgroupHonorsExplicitEnv(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cpu.max"), []byte("250000 100000\n"), 0o600); err != nil {
		t.Fatalf("write cpu.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "memory.max"), []byte("1000000000\n"), 0o600); err != nil {
		t.Fatalf("write memory.max: %v", err)
	}

	settings := ResolveFromCgroup(func(key string) string {
		switch key {
		case "GOMAXPROCS":
			return "8"
		case "GOMEMLIMIT":
			return "2GiB"
		default:
			return ""
		}
	}, root)

	if settings.GOMAXPROCS != 0 || settings.GOMEMLIMIT != 0 {
		t.Fatalf("settings = %#v, want no inferred limits when env is explicit", settings)
	}
}
