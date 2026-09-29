package javarunner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPID1PostureFailsClosed(t *testing.T) {
	valid := "Uid:\t10001 10001 10001 10001\nNoNewPrivs:\t1\nCapInh:\t0000\nCapPrm:\t0000\nCapEff:\t0000\nCapBnd:\t0000\nCapAmb:\t0000\n"
	if err := validatePID1Posture(1, 10001, []byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		pid, uid int
		status   string
	}{{2, 10001, valid}, {1, 0, valid}, {1, 10001, strings.Replace(valid, "NoNewPrivs:\t1", "NoNewPrivs:\t0", 1)}, {1, 10001, strings.Replace(valid, "CapEff:\t0000", "CapEff:\t0001", 1)}, {1, 10001, valid + "CapAmb:\t0000\n"}, {1, 10001, strings.Replace(valid, "10001 10001 10001 10001", "10001 0 10001 10001", 1)}} {
		if err := validatePID1Posture(change.pid, change.uid, []byte(change.status)); err == nil {
			t.Fatal("unsafe PID 1 posture accepted", change)
		}
	}
	if os.Getpid() != 1 {
		if RequirePID1Isolation() == nil || ReapPodOrphans(2) == nil {
			t.Fatal("ordinary process accepted as pod supervisor")
		}
	}
}

func TestRequireReadOnlyAssetRefusesWritableAndNonFiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "asset.jar")
	if e := os.WriteFile(file, []byte("fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"relative.jar", dir, file, filepath.Join(dir, "missing")} {
		if e := RequireReadOnlyAsset(p); !errors.Is(e, ErrPID1Isolation) {
			t.Fatal("non-readonly asset accepted", p, e)
		}
	}
}
