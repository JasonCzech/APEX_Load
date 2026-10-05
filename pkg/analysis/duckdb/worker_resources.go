package duckdb

import (
	"io/fs"
	"os"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/diskspace"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

// This default is an observed-memory watchdog profile, not hard containment.
// Platform hard caps and actual packaged performance qualification follow.
func workerAdmissionHeadroom(parent string, b workerBootstrap) error {
	free, err := diskspace.AvailablePath(parent)
	if err != nil {
		return errWorkerResourceLimit
	}
	reserve, err := workerPipelineDiskBytes(b.Limits, 0)
	if err != nil || !workerDiskHeadroom(free, reserve) {
		return errWorkerResourceLimit
	}
	parentProcess, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return errWorkerResourceLimit
	}
	parentMemory, err := parentProcess.MemoryInfo()
	if err != nil || parentMemory.RSS > 4<<30 || parentMemory.RSS+uint64(b.Limits.MemoryBytes)+(1<<30) > 6<<30 {
		return errWorkerResourceLimit
	}
	vm, err := mem.VirtualMemory()
	if err != nil || vm.Available < uint64(b.Limits.MemoryBytes)+(1<<30) {
		return errWorkerResourceLimit
	}
	if workerContainerHeadroom(b.Limits.MemoryBytes+(1<<30)) != nil {
		return errWorkerResourceLimit
	}
	return nil
}

// The parent reserves both API copies and the single worker's full geometry
// before any download or child allocation. Nested worker bytes must be counted
// against that same combined budget, never only the API staging subset.
func workerPipelineDiskBytes(limits workerLimits, staging int64) (int64, error) {
	var total int64
	var err error
	for _, bytes := range []int64{staging, limits.InputBytes, limits.OutputBytes, limits.SpillBytes, workerExecutableMaxBytes} {
		total, err = workerAdd(total, bytes)
		if err != nil {
			return 0, errWorkerResourceLimit
		}
	}
	return total, nil
}

// Both diskspace and quota arithmetic use signed, overflow-checked bytes.
func workerDiskHeadroom(free, reserve int64) bool {
	required, err := workerAdd(reserve, 512<<20)
	return err == nil && free >= required
}
func workerObserveResources(a *workerAttempt, child *os.Process) error {
	if err := workerContextError(a.ctx); err != nil {
		return err
	}
	free, err := diskspace.AvailablePath(a.path)
	if err != nil || free < 512<<20 {
		return errWorkerResourceLimit
	}
	vm, err := mem.VirtualMemory()
	if err != nil || vm.Available < 512<<20 || workerContainerHeadroom(512<<20) != nil {
		return errWorkerResourceLimit
	}
	parent, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return errWorkerResourceLimit
	}
	parentMem, err := parent.MemoryInfo()
	if err != nil || parentMem.RSS > 4<<30 {
		return errWorkerResourceLimit
	}
	var observationErr error
	if child != nil {
		owned, err := process.NewProcess(int32(child.Pid))
		if err != nil {
			observationErr = workerChildObservationError(child, err)
			if !workerObservationMissingChild(observationErr, child) {
				return observationErr
			}
		} else {
			childMem, err := owned.MemoryInfo()
			if err != nil {
				observationErr = workerChildObservationError(child, err)
				if !workerObservationMissingChild(observationErr, child) {
					return observationErr
				}
			} else if childMem == nil || childMem.RSS > uint64(a.bootstrap.Limits.MemoryBytes)+(512<<20) || parentMem.RSS+childMem.RSS > 6<<30 {
				return errWorkerResourceLimit
			}
		}
	}
	// Missing child inspection must not mask a genuine disk/output/spill breach.
	if err := workerScratchQuotas(a); err != nil {
		return err
	}
	return observationErr
}
func workerScratchQuotas(a *workerAttempt) error {
	input, output, spill := int64(0), int64(0), int64(0)
	count := 0
	err := workerOwnedQuotaWalk(a.root, a, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return errWorkerResourceLimit
		}
		count++
		if count > 16384 || workerContextError(a.ctx) != nil {
			return errWorkerResourceLimit
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errWorkerResourceLimit
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if os.IsNotExist(err) {
			return nil // An enumerated ephemeral file disappeared; no reap proof.
		}
		if err != nil || !info.Mode().IsRegular() {
			return errWorkerResourceLimit
		}
		if path == workerExecutableSnapshotName() {
			if info.Size() > workerExecutableMaxBytes {
				return errWorkerResourceLimit
			}
			return nil
		}
		kind := "output"
		for _, h := range a.bootstrap.Handles {
			if h.Role == "input" && path == h.RelativeName {
				kind = "input"
			}
		}
		if strings.HasPrefix(path, "spill-") {
			kind = "spill"
		}
		var addErr error
		switch kind {
		case "input":
			input, addErr = workerAdd(input, info.Size())
		case "spill":
			spill, addErr = workerAdd(spill, info.Size())
		default:
			output, addErr = workerAdd(output, info.Size())
		}
		if addErr != nil || input > a.bootstrap.Limits.InputBytes || output > a.bootstrap.Limits.OutputBytes || spill > a.bootstrap.Limits.SpillBytes {
			return errWorkerResourceLimit
		}
		return nil
	})
	if err != nil {
		return errWorkerResourceLimit
	}
	return nil
}

// Runs from reservation through hashing/download/staging, before the first
// native child exists. Once a child is active, its exact supervisor observes
// child RSS/quota; this guard continues to protect controller and disk headroom.
func workerObservePipelineResources(lease *NativeIOLease, parent string, directories []string, quota int64, childActive bool) error {
	free, err := diskspace.AvailablePath(parent)
	if err != nil || free < 512<<20 {
		return errWorkerResourceLimit
	}
	vm, err := mem.VirtualMemory()
	if err != nil || vm.Available < 512<<20 || workerContainerHeadroom(512<<20) != nil {
		return errWorkerResourceLimit
	}
	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return errWorkerResourceLimit
	}
	info, err := proc.MemoryInfo()
	if err != nil || info.RSS > 4<<30 {
		return errWorkerResourceLimit
	}
	_ = childActive // exact-child RSS remains with the sole process supervisor.
	_ = quota       // Actual lease carries current immutable/explicitly admitted geometry.
	var used int64
	count := 0
	for _, directory := range directories {
		root, err := os.OpenRoot(directory)
		if err != nil {
			return errWorkerResourceLimit
		}
		lease.TrackOwner(root)
		walkErr := workerOwnedQuotaWalk(root, lease, func(path string, entry fs.DirEntry, err error) error {
			count++
			if err != nil || count > 16384 || entry.Type()&os.ModeSymlink != 0 {
				return errWorkerResourceLimit
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if os.IsNotExist(err) {
				return nil // Legitimate worker cleanup may remove an enumerated file.
			}
			if err != nil || !info.Mode().IsRegular() {
				return errWorkerResourceLimit
			}
			used, err = workerAdd(used, info.Size())
			if err != nil {
				return errWorkerResourceLimit
			}
			// Geometry may be admitted while a prior descriptor probe is reading.
			// Consult the current approved budget before each comparison; neither
			// child startup nor an admitted expansion can use a stale staging cap.
			currentQuota, quotaErr := lease.pipelineDiskBytes()
			if quotaErr != nil || used > currentQuota {
				return errWorkerResourceLimit
			}
			return nil
		})
		closeErr := lease.CloseOwner(root)
		if walkErr != nil || closeErr != nil {
			return errWorkerResourceLimit
		}
	}
	return nil
}
