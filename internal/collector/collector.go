// Package collector is responsible for polling the host once per interval
// and producing a fully-populated Metrics snapshot. It never panics and
// never blocks the caller for longer than the underlying gopsutil calls
// take - each subsystem is collected defensively so a single failing
// sensor (e.g. no temperature support) can't take down the others.
package collector

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	cpuLib "github.com/shirou/gopsutil/v3/cpu"
	diskLib "github.com/shirou/gopsutil/v3/disk"
	hostLib "github.com/shirou/gopsutil/v3/host"
	loadLib "github.com/shirou/gopsutil/v3/load"
	memLib "github.com/shirou/gopsutil/v3/mem"
	netLib "github.com/shirou/gopsutil/v3/net"
	processLib "github.com/shirou/gopsutil/v3/process"

	"system-monitor/internal/system"
)

// Collector periodically samples system metrics and hands the result to a
// registered broadcast function. It keeps a small amount of state
// (previous network/disk counters) to compute per-second rates.
type Collector struct {
	interval time.Duration
	diskPath string

	mu           sync.Mutex
	lastNet      map[string]netLib.IOCountersStat
	lastNetAt    time.Time
	lastDiskIO   map[string]diskLib.IOCountersStat
	lastDiskAt   time.Time

	latest Metrics
}

// New creates a Collector. diskPath is the mount point to report disk
// usage for (defaults to "/" by the caller when empty).
func New(interval time.Duration, diskPath string) *Collector {
	return &Collector{
		interval: interval,
		diskPath: diskPath,
	}
}

// Latest returns the most recently collected snapshot. Safe for
// concurrent use; used by the REST /api/system endpoint so it doesn't
// need to re-collect on every request.
func (c *Collector) Latest() Metrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest
}

// Run collects metrics on a fixed ticker until ctx is cancelled, invoking
// onSample with every new snapshot. It runs entirely in the caller's
// goroutine - callers should `go c.Run(...)` themselves so this never
// blocks server startup.
func (c *Collector) Run(ctx context.Context, onSample func(Metrics)) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Prime an initial sample immediately so the first client doesn't
	// wait a full interval for data.
	c.sampleAndPublish(onSample)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sampleAndPublish(onSample)
		}
	}
}

func (c *Collector) sampleAndPublish(onSample func(Metrics)) {
	defer func() {
		// Collection touches several OS subsystems via cgo-free syscalls;
		// be defensive so a single unexpected panic in a dependency never
		// takes the whole collector loop down.
		if r := recover(); r != nil {
			log.Printf("collector: recovered from panic during sample: %v", r)
		}
	}()

	m := Metrics{
		Timestamp: time.Now().UnixMilli(),
		System:    system.Info(),
	}

	m.CPU = c.collectCPU()
	m.Memory = c.collectMemory()
	m.Disk = c.collectDisk()
	m.Network = c.collectNetwork()
	m.Temps = c.collectTemps()
	m.Processes = c.collectProcesses()

	c.mu.Lock()
	c.latest = m
	c.mu.Unlock()

	onSample(m)
}

func (c *Collector) collectCPU() CPUMetrics {
	out := CPUMetrics{}

	if pct, err := cpuLib.Percent(0, false); err == nil && len(pct) > 0 {
		out.Usage = pct[0]
		out.Available = true
	}

	if perCore, err := cpuLib.Percent(0, true); err == nil {
		out.PerCore = perCore
	}

	if counts, err := cpuLib.Counts(true); err == nil {
		out.Cores = counts
	}

	if avg, err := loadLib.Avg(); err == nil && avg != nil {
		out.Load1 = avg.Load1
		out.Load5 = avg.Load5
		out.Load15 = avg.Load15
	}

	return out
}

func (c *Collector) collectMemory() MemMetrics {
	vm, err := memLib.VirtualMemory()
	if err != nil || vm == nil {
		return MemMetrics{}
	}
	return MemMetrics{
		Total:     vm.Total,
		Used:      vm.Used,
		Free:      vm.Available,
		Usage:     vm.UsedPercent,
		Available: true,
	}
}

func (c *Collector) collectDisk() DiskMetrics {
	path := c.diskPath
	if path == "" {
		path = "/"
	}

	usage, err := diskLib.Usage(path)
	if err != nil || usage == nil {
		return DiskMetrics{Path: path}
	}

	dm := DiskMetrics{
		Total:     usage.Total,
		Used:      usage.Used,
		Free:      usage.Free,
		Usage:     usage.UsedPercent,
		Path:      path,
		Available: true,
	}

	// Disk I/O rate, computed from the delta since the last sample.
	if ioCounters, err := diskLib.IOCounters(); err == nil && len(ioCounters) > 0 {
		now := time.Now()

		c.mu.Lock()
		prev := c.lastDiskIO
		prevAt := c.lastDiskAt
		c.lastDiskIO = ioCounters
		c.lastDiskAt = now
		c.mu.Unlock()

		if prev != nil && !prevAt.IsZero() {
			elapsed := now.Sub(prevAt).Seconds()
			if elapsed > 0 {
				var readDelta, writeDelta uint64
				for name, cur := range ioCounters {
					if p, ok := prev[name]; ok {
						if cur.ReadBytes >= p.ReadBytes {
							readDelta += cur.ReadBytes - p.ReadBytes
						}
						if cur.WriteBytes >= p.WriteBytes {
							writeDelta += cur.WriteBytes - p.WriteBytes
						}
					}
				}
				dm.IO = &DiskIOMetrics{
					ReadBytesPerSec:  uint64(float64(readDelta) / elapsed),
					WriteBytesPerSec: uint64(float64(writeDelta) / elapsed),
				}
			}
		}
	}

	return dm
}

func (c *Collector) collectNetwork() NetMetrics {
	counters, err := netLib.IOCounters(true)
	if err != nil || len(counters) == 0 {
		return NetMetrics{}
	}

	now := time.Now()
	currentByName := make(map[string]netLib.IOCountersStat, len(counters))
	var totalRecv, totalSent uint64

	nm := NetMetrics{Available: true}

	interfaceUp := map[string]bool{}
	if ifaces, err := netLib.Interfaces(); err == nil {
		for _, iface := range ifaces {
			up := false
			for _, flag := range iface.Flags {
				if flag == "up" {
					up = true
					break
				}
			}
			interfaceUp[iface.Name] = up
		}
	}

	for _, ctr := range counters {
		currentByName[ctr.Name] = ctr
		totalRecv += ctr.BytesRecv
		totalSent += ctr.BytesSent
		nm.Interfaces = append(nm.Interfaces, NetInterface{
			Name:     ctr.Name,
			Received: ctr.BytesRecv,
			Sent:     ctr.BytesSent,
			IsUp:     interfaceUp[ctr.Name],
		})
	}

	nm.TotalReceived = totalRecv
	nm.TotalTransmitted = totalSent

	c.mu.Lock()
	prev := c.lastNet
	prevAt := c.lastNetAt
	c.lastNet = currentByName
	c.lastNetAt = now
	c.mu.Unlock()

	if prev != nil && !prevAt.IsZero() {
		elapsed := now.Sub(prevAt).Seconds()
		if elapsed > 0 {
			var recvDelta, sentDelta uint64
			for name, cur := range currentByName {
				if p, ok := prev[name]; ok {
					if cur.BytesRecv >= p.BytesRecv {
						recvDelta += cur.BytesRecv - p.BytesRecv
					}
					if cur.BytesSent >= p.BytesSent {
						sentDelta += cur.BytesSent - p.BytesSent
					}
				}
			}
			nm.DownloadBytesPerSec = uint64(float64(recvDelta) / elapsed)
			nm.UploadBytesPerSec = uint64(float64(sentDelta) / elapsed)
		}
	}

	return nm
}

func (c *Collector) collectTemps() []TempReading {
	temps, err := hostLib.SensorsTemperatures()
	if err != nil || len(temps) == 0 {
		// Gracefully handle systems with no exposed sensors (containers,
		// many virtualized/cloud hosts, some macOS configurations, etc).
		return []TempReading{}
	}

	readings := make([]TempReading, 0, len(temps))
	for _, t := range temps {
		if t.Temperature <= 0 {
			continue
		}
		readings = append(readings, TempReading{
			Sensor:      t.SensorKey,
			Temperature: t.Temperature,
		})
	}
	return readings
}

func (c *Collector) collectProcesses() []ProcessInfo {
	pids, err := processLib.Pids()
	if err != nil {
		return []ProcessInfo{}
	}

	infos := make([]ProcessInfo, 0, len(pids))
	for _, pid := range pids {
		p, err := processLib.NewProcess(pid)
		if err != nil {
			continue // process likely exited between listing and inspection
		}

		name, err := p.Name()
		if err != nil || name == "" {
			continue
		}

		cpuPct, _ := p.CPUPercent()
		memInfo, memErr := p.MemoryInfo()
		memPct, _ := p.MemoryPercent()
		status := "unknown"
		if st, err := p.Status(); err == nil && len(st) > 0 {
			status = st[0]
		}

		var memBytes uint64
		if memErr == nil && memInfo != nil {
			memBytes = memInfo.RSS
		}

		infos = append(infos, ProcessInfo{
			PID:      pid,
			Name:     name,
			CPUPct:   cpuPct,
			MemBytes: memBytes,
			MemPct:   memPct,
			Status:   status,
		})
	}

	// Sort by CPU descending by default; the frontend can re-sort
	// client-side, but we only ship the top N to keep payloads small.
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].CPUPct > infos[j].CPUPct
	})

	const maxProcesses = 50
	if len(infos) > maxProcesses {
		infos = infos[:maxProcesses]
	}

	return infos
}
