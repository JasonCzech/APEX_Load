package engine

import (
	"os"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

type ResourceCollector struct {
	proc        *process.Process
	lastNet     net.IOCountersStat
	lastDisk    disk.IOCountersStat
	lastTime    time.Time
	initialized bool
}

func NewResourceCollector() *ResourceCollector {
	p, _ := process.NewProcess(int32(os.Getpid()))
	return &ResourceCollector{
		proc: p,
	}
}

func (rc *ResourceCollector) Collect() *SystemResources {
	now := time.Now()
	res := &SystemResources{}

	// CPU
	if percentages, err := cpu.Percent(0, false); err == nil && len(percentages) > 0 {
		res.CPUFreePct = 100.0 - percentages[0]
	}
	if rc.proc != nil {
		if p, err := rc.proc.CPUPercent(); err == nil {
			res.CPUProcessPct = p
		}
	}

	// Memory
	if v, err := mem.VirtualMemory(); err == nil {
		res.MemFreeMb = float64(v.Available) / 1024 / 1024
	}
	if rc.proc != nil {
		if m, err := rc.proc.MemoryInfo(); err == nil {
			res.MemProcessMb = float64(m.RSS) / 1024 / 1024
		}
	}

	// Network
	if io, err := net.IOCounters(false); err == nil && len(io) > 0 {
		current := io[0]
		if rc.initialized {
			dt := now.Sub(rc.lastTime).Seconds()
			if dt > 0 {
				res.NetSentKbps = float64(current.BytesSent-rc.lastNet.BytesSent) / 1024 / dt
				res.NetRecvKbps = float64(current.BytesRecv-rc.lastNet.BytesRecv) / 1024 / dt
			}
		}
		rc.lastNet = current
	}

	// Disk
	if io, err := disk.IOCounters(); err == nil {
		// Aggregate all disks
		var current disk.IOCountersStat
		for _, d := range io {
			current.ReadBytes += d.ReadBytes
			current.WriteBytes += d.WriteBytes
		}
		if rc.initialized {
			dt := now.Sub(rc.lastTime).Seconds()
			if dt > 0 {
				res.DiskReadKbps = float64(current.ReadBytes-rc.lastDisk.ReadBytes) / 1024 / dt
				res.DiskWriteKbps = float64(current.WriteBytes-rc.lastDisk.WriteBytes) / 1024 / dt
			}
		}
		rc.lastDisk = current
	}

	rc.lastTime = now
	rc.initialized = true
	return res
}
