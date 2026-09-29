package perfevidence

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJavaPSS(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  float64
		valid bool
	}{{"Rss: 500 kB\nPss: 123 kB\nPss_Anon: 12 kB\n", 125952, true}, {"Pss: 0 kB\n", 0, true}, {"Pss: -1 kB", 0, false}, {"Pss: 12 MB", 0, false}, {"Rss: 1 kB", 0, false}} {
		got, err := parseJavaPSS(tt.input)
		if (err == nil) != tt.valid || got != tt.want {
			t.Fatalf("parse %q = %v, %v", tt.input, got, err)
		}
	}
}

func TestJavaMemorySubtree(t *testing.T) {
	dir := t.TempDir()
	cg := filepath.Join(dir, "cgroup")
	proc := filepath.Join(dir, "proc")
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(cg, "cgroup.procs"), "11\n")
	write(filepath.Join(cg, "host", "cgroup.procs"), "22\n11\n")
	write(filepath.Join(cg, "memory.current"), "100000")
	write(filepath.Join(cg, "memory.events"), "low 0\nhigh 0\nmax 2\noom 3\noom_kill 1\n")
	write(filepath.Join(cg, "cpu.stat"), "usage_usec 2500000\n")
	write(filepath.Join(proc, "11", "smaps_rollup"), "Pss: 10 kB\n")
	write(filepath.Join(proc, "22", "smaps_rollup"), "Pss: 20 kB\n")
	r, err := readJavaMemory(cg, proc, 11, 22)
	if err != nil || r.pss != 30720 || r.cgroup != 100000 || r.cpuSeconds != 2.5 || r.limitEvents != 6 {
		t.Fatalf("reading = %+v, %v", r, err)
	}
	if _, err = readJavaMemory(cg, proc, 33); err == nil {
		t.Fatal("excluded process accepted")
	}
	if err = os.Remove(filepath.Join(proc, "22", "smaps_rollup")); err != nil {
		t.Fatal(err)
	}
	if _, err = readJavaMemory(cg, proc, 11, 22); err == nil {
		t.Fatal("disappearing process silently undercounted")
	}
}
