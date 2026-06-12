// Package metrics samples live system metrics (gopsutil) on a ticker into a ring buffer,
// and computes a Task-Manager-style per-process breakdown via CPU-time deltas.
package metrics

import (
	"math"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// DiskUsage is per-mount capacity.
type DiskUsage struct {
	Mount   string  `json:"mount"`
	Fstype  string  `json:"fstype"`
	Total   uint64  `json:"total"`
	Used    uint64  `json:"used"`
	Free    uint64  `json:"free"`
	Percent float64 `json:"percent"`
}

// Summary is the current aggregate snapshot (safe for every authenticated user).
type Summary struct {
	Time         int64       `json:"time"`
	CPUPercent   float64     `json:"cpuPercent"`
	PerCPU       []float64   `json:"perCpu"`
	MemTotal     uint64      `json:"memTotal"`
	MemUsed      uint64      `json:"memUsed"`
	MemAvailable uint64      `json:"memAvailable"`
	MemCached    uint64      `json:"memCached"`
	MemPercent   float64     `json:"memPercent"`
	SwapTotal    uint64      `json:"swapTotal"`
	SwapUsed     uint64      `json:"swapUsed"`
	SwapPercent  float64     `json:"swapPercent"`
	Disks        []DiskUsage `json:"disks"`
	NetRxRate    float64     `json:"netRxRate"` // bytes/sec
	NetTxRate    float64     `json:"netTxRate"`
	Load1        float64     `json:"load1"`
	Load5        float64     `json:"load5"`
	Load15       float64     `json:"load15"`
	Uptime       uint64      `json:"uptime"`
	Procs        int         `json:"procs"`
}

// Sample is one point in the time-series ring buffer (for charts).
type Sample struct {
	Time  int64   `json:"time"`
	CPU   float64 `json:"cpu"`
	Mem   float64 `json:"mem"`
	NetRx float64 `json:"netRx"`
	NetTx float64 `json:"netTx"`
	Disk  float64 `json:"disk"`
}

// Process is one row of the per-process breakdown (admin-only).
type Process struct {
	PID        int32   `json:"pid"`
	Name       string  `json:"name"`
	User       string  `json:"user"`
	CPUPercent float64 `json:"cpuPercent"`
	MemRSS     uint64  `json:"memRss"`
	MemPercent float64 `json:"memPercent"`
	Status     string  `json:"status"`
}

// HostInfo is static (read once at startup).
type HostInfo struct {
	Hostname        string `json:"hostname"`
	OS              string `json:"os"`
	Platform        string `json:"platform"`
	PlatformVersion string `json:"platformVersion"`
	Kernel          string `json:"kernel"`
	Arch            string `json:"arch"`
	CPUModel        string `json:"cpuModel"`
	CPUCores        int    `json:"cpuCores"`
	CPUThreads      int    `json:"cpuThreads"`
	MemTotal        uint64 `json:"memTotal"`
	BootTime        uint64 `json:"bootTime"`
}

type procCPU struct {
	total float64
	when  time.Time
}

// Collector owns the sampling loop and the latest results.
type Collector struct {
	interval time.Duration
	ringCap  int

	mu       sync.RWMutex
	summary  Summary
	ring     []Sample
	procs    []Process
	hostInfo HostInfo

	// Owned exclusively by the single sampler goroutine (and the synchronous Start()
	// call that happens-before it) — never touched elsewhere, so they need no lock.
	prevNetRx, prevNetTx uint64
	prevNetTime          time.Time
	prevProc             map[int32]procCPU
	ncpu                 int
}

// New returns a collector sampling at the given interval (~120s of history at 2s).
func New(interval time.Duration) *Collector {
	return &Collector{interval: interval, ringCap: 180, prevProc: map[int32]procCPU{}, ncpu: runtime.NumCPU()}
}

// Start loads static host info, primes the first sample, then samples on a ticker.
func (c *Collector) Start() {
	c.loadHostInfo()
	// Prime gopsutil's global CPU-time baseline so the first sample reflects a real
	// interval, not time-since-process-start (cpu.Percent(0,...) is delta-based).
	_, _ = cpu.Percent(0, false)
	_, _ = cpu.Percent(0, true)
	c.sample()
	go func() {
		t := time.NewTicker(c.interval)
		defer t.Stop()
		for range t.C {
			c.sample()
		}
	}()
}

func (c *Collector) loadHostInfo() {
	hi := HostInfo{Arch: runtime.GOARCH, OS: runtime.GOOS}
	if info, err := host.Info(); err == nil {
		hi.Hostname = info.Hostname
		hi.OS = info.OS
		hi.Platform = info.Platform
		hi.PlatformVersion = info.PlatformVersion
		hi.Kernel = info.KernelVersion
		if info.KernelArch != "" {
			hi.Arch = info.KernelArch
		}
		hi.BootTime = info.BootTime
	}
	if ci, err := cpu.Info(); err == nil && len(ci) > 0 {
		hi.CPUModel = ci[0].ModelName
	}
	if cores, err := cpu.Counts(false); err == nil {
		hi.CPUCores = cores
	}
	if threads, err := cpu.Counts(true); err == nil {
		hi.CPUThreads = threads
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		hi.MemTotal = vm.Total
	}
	c.mu.Lock()
	c.hostInfo = hi
	c.mu.Unlock()
}

func (c *Collector) sample() {
	now := time.Now()
	s := Summary{Time: now.UnixMilli()}

	// cpu.Percent(0,...) uses gopsutil's PROCESS-GLOBAL last-times; hostekd must stay
	// the only in-process caller or concurrent callers corrupt each other's deltas.
	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		s.CPUPercent = round(pcts[0])
	}
	if per, err := cpu.Percent(0, true); err == nil {
		s.PerCPU = make([]float64, len(per))
		for i, p := range per {
			s.PerCPU[i] = round(p)
		}
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		s.MemTotal = vm.Total
		s.MemUsed = vm.Used
		s.MemAvailable = vm.Available
		s.MemCached = vm.Cached
		s.MemPercent = round(vm.UsedPercent)
	}
	if sw, err := mem.SwapMemory(); err == nil {
		s.SwapTotal = sw.Total
		s.SwapUsed = sw.Used
		s.SwapPercent = round(sw.UsedPercent)
	}
	s.Disks = collectDisks()
	s.NetRxRate, s.NetTxRate = c.netRates(now)
	if l, err := load.Avg(); err == nil {
		s.Load1, s.Load5, s.Load15 = round(l.Load1), round(l.Load5), round(l.Load15)
	}
	if up, err := host.Uptime(); err == nil {
		s.Uptime = up
	}

	procs := c.sampleProcs(now)
	s.Procs = len(procs)

	var diskPct float64
	if len(s.Disks) > 0 {
		diskPct = s.Disks[0].Percent
	}
	smp := Sample{Time: s.Time, CPU: s.CPUPercent, Mem: s.MemPercent, NetRx: s.NetRxRate, NetTx: s.NetTxRate, Disk: diskPct}

	c.mu.Lock()
	c.summary = s
	c.procs = procs
	c.ring = append(c.ring, smp)
	if len(c.ring) > c.ringCap {
		// Copy down so the trimmed head is released, not retained behind a reslice.
		c.ring = append([]Sample(nil), c.ring[len(c.ring)-c.ringCap:]...)
	}
	c.mu.Unlock()
}

func collectDisks() []DiskUsage {
	parts, err := disk.Partitions(false)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]DiskUsage, 0, len(parts))
	for _, p := range parts {
		if seen[p.Mountpoint] {
			continue
		}
		seen[p.Mountpoint] = true
		u, err := disk.Usage(p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		out = append(out, DiskUsage{Mount: p.Mountpoint, Fstype: p.Fstype, Total: u.Total, Used: u.Used, Free: u.Free, Percent: round(u.UsedPercent)})
	}
	// Largest filesystem first (so Disks[0] is the meaningful root/data volume).
	sort.Slice(out, func(i, j int) bool { return out[i].Total > out[j].Total })
	return out
}

func (c *Collector) netRates(now time.Time) (rx, tx float64) {
	io, err := gnet.IOCounters(false)
	if err != nil || len(io) == 0 {
		return 0, 0
	}
	curRx, curTx := io[0].BytesRecv, io[0].BytesSent
	if !c.prevNetTime.IsZero() {
		dt := now.Sub(c.prevNetTime).Seconds()
		if dt > 0 {
			if curRx >= c.prevNetRx {
				rx = float64(curRx-c.prevNetRx) / dt
			}
			if curTx >= c.prevNetTx {
				tx = float64(curTx-c.prevNetTx) / dt
			}
		}
	}
	c.prevNetRx, c.prevNetTx, c.prevNetTime = curRx, curTx, now
	return rx, tx
}

func (c *Collector) sampleProcs(now time.Time) []Process {
	ps, err := process.Processes()
	if err != nil {
		c.mu.RLock()
		prev := c.procs
		c.mu.RUnlock()
		return prev
	}
	next := make(map[int32]procCPU, len(ps))
	out := make([]Process, 0, len(ps))
	for _, p := range ps {
		times, err := p.Times()
		if err != nil {
			continue
		}
		total := times.User + times.System
		next[p.Pid] = procCPU{total: total, when: now}
		var pct float64
		if prev, ok := c.prevProc[p.Pid]; ok {
			if dt := now.Sub(prev.when).Seconds(); dt > 0 {
				pct = (total - prev.total) / dt * 100 / float64(c.ncpu)
			}
		}
		name, _ := p.Name()
		username, _ := p.Username()
		var rss uint64
		if mi, err := p.MemoryInfo(); err == nil && mi != nil {
			rss = mi.RSS
		}
		memPct, _ := p.MemoryPercent()
		status := ""
		if st, err := p.Status(); err == nil && len(st) > 0 {
			status = st[0]
		}
		out = append(out, Process{
			PID:        p.Pid,
			Name:       name,
			User:       username,
			CPUPercent: round(clampPct(pct)),
			MemRSS:     rss,
			MemPercent: round(float64(memPct)),
			Status:     status,
		})
	}
	c.prevProc = next
	sort.Slice(out, func(i, j int) bool { return out[i].CPUPercent > out[j].CPUPercent })
	return out
}

// Summary returns the latest aggregate snapshot.
func (c *Collector) Summary() Summary {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.summary
}

// Series returns a copy of the time-series ring buffer.
func (c *Collector) Series() []Sample {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Sample, len(c.ring))
	copy(out, c.ring)
	return out
}

// Processes returns a copy of the latest per-process breakdown.
func (c *Collector) Processes() []Process {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Process, len(c.procs))
	copy(out, c.procs)
	return out
}

// Host returns static host info.
func (c *Collector) Host() HostInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hostInfo
}

func round(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return math.Round(f*10) / 10
}

func clampPct(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 100 {
		return 100
	}
	return f
}
