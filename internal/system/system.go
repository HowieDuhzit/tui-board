package system

import (
	"context"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
)

type Snapshot struct {
	CPUPercent  float64
	MemUsedPct  float64
	MemUsedGB   float64
	MemTotalGB  float64
	DiskUsedPct float64
	DiskPath    string
	NetInMbps   float64
	NetOutMbps  float64
}

func Sample(ctx context.Context, diskPath string, interval time.Duration) (Snapshot, error) {
	var snap Snapshot
	cpuPct, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		return snap, err
	}
	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return snap, err
	}
	if diskPath == "" {
		diskPath = "/"
	}
	diskStat, err := disk.UsageWithContext(ctx, diskPath)
	if err != nil {
		return snap, err
	}
	net1, err := net.IOCountersWithContext(ctx, false)
	if err != nil {
		return snap, err
	}
	if interval > 0 {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return snap, ctx.Err()
		case <-timer.C:
		}
		net2, err := net.IOCountersWithContext(ctx, false)
		if err != nil {
			return snap, err
		}
		if len(net1) > 0 && len(net2) > 0 {
			inbps := float64(net2[0].BytesRecv-net1[0].BytesRecv) * 8 / interval.Seconds()
			outbps := float64(net2[0].BytesSent-net1[0].BytesSent) * 8 / interval.Seconds()
			snap.NetInMbps = inbps / 1_000_000
			snap.NetOutMbps = outbps / 1_000_000
		}
	}

	if len(cpuPct) > 0 {
		snap.CPUPercent = cpuPct[0]
	}
	snap.MemUsedPct = vm.UsedPercent
	snap.MemUsedGB = float64(vm.Used) / (1024 * 1024 * 1024)
	snap.MemTotalGB = float64(vm.Total) / (1024 * 1024 * 1024)
	snap.DiskUsedPct = diskStat.UsedPercent
	snap.DiskPath = diskPath

	return snap, nil
}
