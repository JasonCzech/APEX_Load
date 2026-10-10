//go:build darwin || linux

package api

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Process usage for the opt-in reference workloads (B8, C8), on platforms
// with getrusage and ps.

// referenceCPUSeconds is this process's user and system CPU time so far.
func referenceCPUSeconds() (float64, bool) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, false
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano()).Seconds(), true
}

// referenceRSSSampler samples, every interval, the resident memory of this
// process and of all its descendants (native analytics workers, a browser
// and its driver) from ps, and keeps the peaks: of this process, of the
// descendants, and of the two together at the same instant.
type referenceRSSSampler struct {
	mu                          sync.Mutex
	self, descendants, combined int64
	samples                     int
	cancel                      context.CancelFunc
	done                        chan struct{}
}

func startReferenceRSSSampler(interval time.Duration) *referenceRSSSampler {
	ctx, cancel := context.WithCancel(context.Background())
	sampler := &referenceRSSSampler{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(sampler.done)
		for {
			sampler.sample()
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
		}
	}()
	return sampler
}

func (s *referenceRSSSampler) sample() {
	// ps is itself a child of this process: its own row is not counted.
	command := exec.Command("ps", "-A", "-o", "pid=,ppid=,rss=")
	var buffer strings.Builder
	command.Stdout = &buffer
	if err := command.Start(); err != nil {
		return
	}
	psPID := command.Process.Pid
	if err := command.Wait(); err != nil {
		return
	}
	output := buffer.String()
	parent, rss := map[int]int{}, map[int]int64{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		kib, err3 := strconv.ParseInt(fields[2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || pid == psPID {
			continue
		}
		parent[pid], rss[pid] = ppid, kib*1024
	}
	self := os.Getpid()
	var descendants int64
	for pid := range rss {
		for ancestor, hops := parent[pid], 0; ancestor > 1 && hops < 64; ancestor, hops = parent[ancestor], hops+1 {
			if ancestor == self {
				descendants += rss[pid]
				break
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples++
	s.self = max(s.self, rss[self])
	s.descendants = max(s.descendants, descendants)
	s.combined = max(s.combined, rss[self]+descendants)
}

// stop ends sampling and returns the peaks in bytes; ok is false when no
// sample was taken.
func (s *referenceRSSSampler) stop() (self, descendants, combined int64, ok bool) {
	s.cancel()
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.self, s.descendants, s.combined, s.samples > 0
}
