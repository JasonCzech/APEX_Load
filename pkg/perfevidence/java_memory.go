package perfevidence

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type javaMemoryReading struct {
	pss, cgroup, cpuSeconds float64
	limitEvents             int64
}

// readJavaMemory counts each PID once across a cgroup-v2 subtree. A disappearing
// process invalidates this observation rather than understating memory. The
// caller records incomplete evidence; it must not silently discard bad samples.
func readJavaMemory(root, procRoot string, requiredPIDs ...int) (javaMemoryReading, error) {
	var result javaMemoryReading
	pids := make(map[int]struct{})
	directories := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		directories++
		if directories > 10000 {
			return errors.New("Java accounting subtree too large")
		}
		data, err := os.ReadFile(filepath.Join(path, "cgroup.procs"))
		if err != nil {
			return err
		}
		for _, field := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(field)
			if err != nil || pid <= 0 {
				return errors.New("invalid cgroup pid")
			}
			pids[pid] = struct{}{}
		}
		if len(pids) > 100000 {
			return errors.New("Java accounting process limit exceeded")
		}
		return nil
	})
	if err != nil {
		return result, errors.New("cannot enumerate Java accounting cgroup")
	}
	for _, pid := range requiredPIDs {
		if _, ok := pids[pid]; !ok {
			return result, errors.New("Java accounting cgroup excludes benchmark or runner")
		}
	}
	if len(pids) == 0 {
		return result, errors.New("Java accounting cgroup is empty")
	}
	for pid := range pids {
		data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "smaps_rollup"))
		if err != nil {
			return result, errors.New("Java process PSS unavailable")
		}
		pss, err := parseJavaPSS(string(data))
		if err != nil {
			return result, err
		}
		result.pss += pss
	}
	data, err := os.ReadFile(filepath.Join(root, "memory.current"))
	if err != nil {
		return result, errors.New("Java cgroup memory unavailable")
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return result, errors.New("invalid Java cgroup memory")
	}
	result.cgroup = float64(value)
	data, err = os.ReadFile(filepath.Join(root, "memory.events"))
	if err != nil {
		return result, errors.New("Java cgroup memory events unavailable")
	}
	seenEvents := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || (f[0] != "max" && f[0] != "oom" && f[0] != "oom_kill") {
			continue
		}
		v, e := strconv.ParseInt(f[1], 10, 64)
		if e != nil || v < 0 || seenEvents[f[0]] || v > (1<<63-1)-result.limitEvents {
			return result, errors.New("invalid Java cgroup memory events")
		}
		seenEvents[f[0]] = true
		result.limitEvents += v
	}
	if len(seenEvents) != 3 {
		return result, errors.New("incomplete Java cgroup memory events")
	}
	data, err = os.ReadFile(filepath.Join(root, "cpu.stat"))
	if err != nil {
		return result, errors.New("Java cgroup CPU unavailable")
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "usage_usec" {
			v, e := strconv.ParseUint(f[1], 10, 64)
			if e != nil {
				return result, errors.New("invalid Java cgroup CPU")
			}
			result.cpuSeconds = float64(v) / 1e6
			return result, nil
		}
	}
	return result, errors.New("Java cgroup CPU usage missing")
}

func parseJavaPSS(data string) (float64, error) {
	s := bufio.NewScanner(strings.NewReader(data))
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) > 0 && f[0] == "Pss:" {
			if len(f) != 3 || f[2] != "kB" {
				break
			}
			n, err := strconv.ParseUint(f[1], 10, 64)
			if err != nil {
				break
			}
			return float64(n) * 1024, nil
		}
	}
	return 0, errors.New("invalid Java process PSS")
}
