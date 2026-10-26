package procidentity

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestGoneDecidesOnlyProvableExits(t *testing.T) {
	host := Host{Machine: "m1", Boot: "b1", Namespace: "pid:[1]"}
	id := Identity{Machine: "m1", Boot: "b1", Namespace: "pid:[1]", PID: 42, Start: "100"}
	lookup := func(start string, found bool, err error) func(int) (string, bool, error) {
		return func(int) (string, bool, error) { return start, found, err }
	}
	alive := lookup("100", true, nil)
	for name, c := range map[string]struct {
		id    func(Identity) Identity
		start func(int) (string, bool, error)
		gone  bool
	}{
		"same process alive":          {func(i Identity) Identity { return i }, alive, false},
		"no such PID":                 {func(i Identity) Identity { return i }, lookup("", false, nil), true},
		"PID reused":                  {func(i Identity) Identity { return i }, lookup("200", true, nil), true},
		"lookup failed":               {func(i Identity) Identity { return i }, lookup("", false, os.ErrPermission), false},
		"no lookup":                   {func(i Identity) Identity { return i }, nil, false},
		"earlier boot, same machine":  {func(i Identity) Identity { i.Boot = "b0"; return i }, alive, true},
		"earlier boot, other machine": {func(i Identity) Identity { i.Boot, i.Machine = "b0", "m2"; return i }, alive, false},
		"boot of unknown machine":     {func(i Identity) Identity { i.Boot, i.Machine = "b0", ""; return i }, alive, false},
		"other PID namespace":         {func(i Identity) Identity { i.Namespace = "pid:[2]"; return i }, lookup("", false, nil), false},
		"no start time":               {func(i Identity) Identity { i.Start = ""; return i }, lookup("", false, nil), false},
		"no PID":                      {func(i Identity) Identity { i.PID = 0; return i }, lookup("", false, nil), false},
		"no boot":                     {func(i Identity) Identity { i.Boot = ""; return i }, lookup("", false, nil), false},
		"same boot, other machine":    {func(i Identity) Identity { i.Machine = "m2"; return i }, lookup("", false, nil), false},
		"same boot, unknown machine":  {func(i Identity) Identity { i.Machine = ""; return i }, lookup("", false, nil), false},
	} {
		if got := Gone(host, c.id(id), c.start); got != c.gone {
			t.Errorf("%s: gone=%v, want %v", name, got, c.gone)
		}
	}
	if Gone(Host{Boot: "b1", Namespace: "pid:[1]"}, Identity{Boot: "b0", Namespace: "pid:[1]", PID: 42, Start: "100"}, alive) {
		t.Error("an unidentified host judged an earlier boot")
	}
	// macOS has no machine identity: its per-boot UUID alone scopes a lookup.
	if !Gone(Host{Boot: "b1", Namespace: "darwin"}, Identity{Boot: "b1", Namespace: "darwin", PID: 42, Start: "100"}, lookup("", false, nil)) {
		t.Error("an unidentified host could not judge its own boot")
	}
	for name, c := range map[string]struct {
		host      Host
		id        Identity
		judgeable bool
	}{
		"same machine, boot and namespace": {host, id, true},
		"another PID namespace":            {host, Identity{Machine: "m1", Boot: "b1", Namespace: "pid:[2]", PID: 42, Start: "100"}, false},
		"same boot, another machine":       {host, Identity{Machine: "m2", Boot: "b1", Namespace: "pid:[1]", PID: 42, Start: "100"}, false},
		"earlier boot, same machine":       {host, Identity{Machine: "m1", Boot: "b0", Namespace: "pid:[9]", PID: 42, Start: "100"}, true},
		"earlier boot, another machine":    {host, Identity{Machine: "m2", Boot: "b0", Namespace: "pid:[1]", PID: 42, Start: "100"}, false},
		"earlier boot, unknown machine":    {Host{Boot: "b1", Namespace: "pid:[1]"}, Identity{Boot: "b0", Namespace: "pid:[1]", PID: 42, Start: "100"}, false},
		"incomplete identity":              {host, Identity{Machine: "m1", Boot: "b1", Namespace: "pid:[1]"}, false},
		"empty":                            {Host{}, Identity{}, false},
	} {
		if got := Judgeable(c.host, c.id); got != c.judgeable {
			t.Errorf("Judgeable %s: %v, want %v", name, got, c.judgeable)
		}
	}
}

// TestHelperProcessSleeps is the child the exit test starts and kills.
func TestHelperProcessSleeps(t *testing.T) {
	if os.Getenv("PROCIDENTITY_HELPER") != "sleep" {
		t.Skip("helper process only")
	}
	time.Sleep(30 * time.Second)
}

func TestExitedJudgesLiveAndReapedProcesses(t *testing.T) {
	if _, err := CurrentHost(); err != nil {
		// Exact identity is required on every supported platform; a host
		// that cannot produce one must fail this test, not skip it.
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows" {
			t.Fatalf("process identity unavailable on %s: %v", runtime.GOOS, err)
		}
		t.Skipf("process identity unavailable here: %v", err)
	}
	self, err := Self()
	if err != nil {
		t.Fatal(err)
	}
	if Exited(self) {
		t.Fatal("this process judged exited")
	}
	child := exec.Command(os.Args[0], "-test.run=^TestHelperProcessSleeps$")
	child.Env = append(os.Environ(), "PROCIDENTITY_HELPER=sleep")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	identity, err := Of(child.Process.Pid)
	if err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatal(err)
	}
	if Exited(identity) {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatal("a live child judged exited")
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	if !Exited(identity) {
		t.Fatal("a reaped child judged alive")
	}
	if _, err := Of(0); err == nil {
		t.Fatal("PID 0 was identified")
	}
}
