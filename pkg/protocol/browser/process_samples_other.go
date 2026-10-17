//go:build !windows

package browser

import (
	"context"
	"os/exec"
	"runtime"
)

// listProcessTreeSamples lists every process with its parent, resident memory
// and CPU time; callers walk rootPID's tree from that list. Linux reads /proc
// directly: container images ship BusyBox ps, whose RSS column is abbreviated
// (292m), or no ps at all.
func listProcessTreeSamples(ctx context.Context, _ int) ([]processSample, error) {
	if runtime.GOOS == "linux" {
		return readProcProcessSamples(ctx, "/proc")
	}
	out, err := exec.CommandContext(ctx, "ps", "-eo", "pid=,ppid=,rss=,time=").Output()
	if err != nil {
		return nil, err
	}
	return parseProcessSamples(string(out)), nil
}
