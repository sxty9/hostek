// Package metrics samples live system metrics (gopsutil) on a ticker into a ring buffer,
// and computes a Task-Manager-style per-process breakdown via CPU-time deltas.
package metrics

import (
	"math"
	"runtime"
	"sort"
	"sync"
	"time"

	"hostek/internal/diskutil"
	"hostek/internal/gpu"
	"hostek/internal/netmon"
	"hostek/internal/powermon"

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
	// GPU(s) — empty when no NVIDIA GPU / nvidia-smi is present.
	GPUs []gpu.GPU `json:"gpus,omitempty"`
	// System disk (the device backing "/") activity — Task-Manager-style read/write.
	SysDiskDevice      string  `json:"sysDiskDevice,omitempty"`
	SysDiskReadRate    float64 `json:"sysDiskReadRate"`    // bytes/sec
	SysDiskWriteRate   float64 `json:"sysDiskWriteRate"`   // bytes/sec
	SysDiskBusyPercent float64 `json:"sysDiskBusyPercent"` // active-time %, 0-100
	Load1              float64 `json:"load1"`
	Load5              float64 `json:"load5"`
	Load15             float64 `json:"load15"`
	// Per-component utilization averages (kernel-style EWMA over 1/5/15 min) — the
	// per-component analogue of the system load average, shown on hover.
	Loads  Loads  `json:"loads"`
	Uptime uint64 `json:"uptime"`
	Procs  int    `json:"procs"`
}

// Avg holds a value averaged over 1, 5 and 15 minutes (EWMA).
type Avg struct {
	A1  float64 `json:"a1"`
	A5  float64 `json:"a5"`
	A15 float64 `json:"a15"`
}

// Loads is the per-component utilization average (CPU/Mem/GPU = %, SSD = active-time %,
// Net = bytes/sec).
type Loads struct {
	CPU Avg `json:"cpu"`
	Mem Avg `json:"mem"`
	GPU Avg `json:"gpu"`
	SSD Avg `json:"ssd"`
	Net Avg `json:"net"`
}

// PowerSample is one point of the per-component power time-series (watts).
type PowerSample struct {
	Time  int64   `json:"time"`
	CPU   float64 `json:"cpu"`
	GPU   float64 `json:"gpu"`
	Total float64 `json:"total"`
}

// PowerResponse is the Power tab payload: the recent series plus 1/5/15-min averages
// and which sources are actually reporting.
type PowerResponse struct {
	Samples      []PowerSample `json:"samples"`
	Avg          PowerAvg      `json:"avg"`
	CPUAvailable bool          `json:"cpuAvailable"`
	GPUAvailable bool          `json:"gpuAvailable"`
}

// PowerAvg holds the 1/5/15-min EWMA power for each component (watts).
type PowerAvg struct {
	CPU   Avg `json:"cpu"`
	GPU   Avg `json:"gpu"`
	Total Avg `json:"total"`
}

// ewma3 maintains a value's exponentially-weighted moving average over 1/5/15 min,
// the same scheme the kernel uses for the load average.
type ewma3 struct {
	a1, a5, a15 float64
	primed      bool
}

func (e *ewma3) update(sample, dt float64) {
	if !e.primed {
		e.a1, e.a5, e.a15 = sample, sample, sample
		e.primed = true
		return
	}
	e.a1 += (1 - math.Exp(-dt/60)) * (sample - e.a1)
	e.a5 += (1 - math.Exp(-dt/300)) * (sample - e.a5)
	e.a15 += (1 - math.Exp(-dt/900)) * (sample - e.a15)
}

func (e *ewma3) avg() Avg { return Avg{A1: round(e.a1), A5: round(e.a5), A15: round(e.a15)} }

// Sample is one point in the time-series ring buffer (for charts). Percentage fields
// feed the combined utilization chart; the byte-rate fields feed the per-component
// detail graphs (network Rx/Tx, system-disk read/write).
type Sample struct {
	Time     int64   `json:"time"`
	CPU      float64 `json:"cpu"`      // %
	Mem      float64 `json:"mem"`      // %
	GPU      float64 `json:"gpu"`      // % (max across GPUs)
	SSDBusy  float64 `json:"ssdBusy"`  // system-disk active-time %
	SSDRead  float64 `json:"ssdRead"`  // bytes/sec
	SSDWrite float64 `json:"ssdWrite"` // bytes/sec
	NetRx    float64 `json:"netRx"`    // bytes/sec
	NetTx    float64 `json:"netTx"`    // bytes/sec
}

// Process is one row of the per-process breakdown (admin-only).
type Process struct {
	PID        int32   `json:"pid"`
	Name       string  `json:"name"`
	User       string  `json:"user"`
	CPUPercent float64 `json:"cpuPercent"`
	MemRSS     uint64  `json:"memRss"`
	MemPercent float64 `json:"memPercent"`
	// GPU — best-effort via nvidia-smi pmon; zero/empty when the process uses no GPU.
	GPUPercent float64 `json:"gpuPercent"`
	GPUEngine  string  `json:"gpuEngine,omitempty"`
	GPUMem     uint64  `json:"gpuMem,omitempty"`
	// Network — best-effort via the privileged netmon co-process; zero when unavailable.
	NetRxRate float64 `json:"netRxRate"`
	NetTxRate float64 `json:"netTxRate"`
	Status    string  `json:"status"`
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

	// External samplers (GPU via nvidia-smi, per-process network and CPU package power
	// via privileged co-processes). All non-nil; they degrade to empty when unsupported.
	gpu   *gpu.Sampler
	net   *netmon.Sampler
	power *powermon.Sampler

	mu        sync.RWMutex
	summary   Summary
	ring      []Sample
	procs     []Process
	hostInfo  HostInfo
	powerRing []PowerSample
	powerAvg  PowerAvg // snapshot of the power EWMAs (read by Power())

	// Per-component EWMA averages (owned by the sampler goroutine).
	cpuL, memL, gpuL, ssdL, netL ewma3 // utilization averages
	cpuPwrL, gpuPwrL, totPwrL    ewma3 // power averages (watts)

	// Owned exclusively by the single sampler goroutine (and the synchronous Start()
	// call that happens-before it) — never touched elsewhere, so they need no lock.
	prevNetRx, prevNetTx uint64
	prevNetTime          time.Time
	prevProc             map[int32]procCPU
	ncpu                 int

	sysDevice                                   string // device backing "/", e.g. "nvme0n1"
	prevDiskRead, prevDiskWrite, prevDiskIoTime uint64
	prevDiskTime                                time.Time
}

// New returns a collector sampling at the given interval (~120s of history at 2s).
// The GPU, network and power samplers are injected so the daemon owns their lifecycles.
func New(interval time.Duration, g *gpu.Sampler, n *netmon.Sampler, p *powermon.Sampler) *Collector {
	return &Collector{interval: interval, ringCap: 180, gpu: g, net: n, power: p, prevProc: map[int32]procCPU{}, ncpu: runtime.NumCPU()}
}

// Start loads static host info, primes the first sample, then samples on a ticker.
func (c *Collector) Start() {
	c.loadHostInfo()
	c.sysDevice = diskutil.RootDevice()
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
	s.SysDiskDevice = c.sysDevice
	s.SysDiskReadRate, s.SysDiskWriteRate, s.SysDiskBusyPercent = c.diskRates(now)

	gpuSnap := c.gpu.Get()
	s.GPUs = gpuSnap.GPUs

	if l, err := load.Avg(); err == nil {
		s.Load1, s.Load5, s.Load15 = round(l.Load1), round(l.Load5), round(l.Load15)
	}
	if up, err := host.Uptime(); err == nil {
		s.Uptime = up
	}

	procs := c.sampleProcs(now, gpuSnap, c.net.Get())
	s.Procs = len(procs)

	var gpuPct, gpuW float64
	for _, g := range s.GPUs {
		if g.UtilPercent > gpuPct {
			gpuPct = g.UtilPercent
		}
		gpuW += g.PowerW
	}
	smp := Sample{
		Time:     s.Time,
		CPU:      s.CPUPercent,
		Mem:      s.MemPercent,
		GPU:      gpuPct,
		SSDBusy:  s.SysDiskBusyPercent,
		SSDRead:  s.SysDiskReadRate,
		SSDWrite: s.SysDiskWriteRate,
		NetRx:    s.NetRxRate,
		NetTx:    s.NetTxRate,
	}

	// Per-component utilization averages (1/5/15 min EWMA).
	dt := c.interval.Seconds()
	c.cpuL.update(s.CPUPercent, dt)
	c.memL.update(s.MemPercent, dt)
	c.gpuL.update(gpuPct, dt)
	c.ssdL.update(s.SysDiskBusyPercent, dt)
	c.netL.update(s.NetRxRate+s.NetTxRate, dt)
	s.Loads = Loads{CPU: c.cpuL.avg(), Mem: c.memL.avg(), GPU: c.gpuL.avg(), SSD: c.ssdL.avg(), Net: c.netL.avg()}

	// Per-component power (CPU via RAPL co-process, GPU via nvidia-smi) + averages.
	cpuW := c.power.Watts()
	totW := cpuW + gpuW
	c.cpuPwrL.update(cpuW, dt)
	c.gpuPwrL.update(gpuW, dt)
	c.totPwrL.update(totW, dt)
	psmp := PowerSample{Time: s.Time, CPU: round(cpuW), GPU: round(gpuW), Total: round(totW)}

	c.mu.Lock()
	c.summary = s
	c.procs = procs
	c.ring = append(c.ring, smp)
	if len(c.ring) > c.ringCap {
		// Copy down so the trimmed head is released, not retained behind a reslice.
		c.ring = append([]Sample(nil), c.ring[len(c.ring)-c.ringCap:]...)
	}
	c.powerRing = append(c.powerRing, psmp)
	if len(c.powerRing) > c.ringCap {
		c.powerRing = append([]PowerSample(nil), c.powerRing[len(c.powerRing)-c.ringCap:]...)
	}
	c.powerAvg = PowerAvg{CPU: c.cpuPwrL.avg(), GPU: c.gpuPwrL.avg(), Total: c.totPwrL.avg()}
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

// diskRates returns the system disk's read/write throughput (bytes/sec) and active-time
// percentage (Windows-style "% busy") via /proc/diskstats deltas. Zero until primed.
func (c *Collector) diskRates(now time.Time) (read, write, busy float64) {
	if c.sysDevice == "" {
		return 0, 0, 0
	}
	io, err := disk.IOCounters(c.sysDevice)
	if err != nil || len(io) == 0 {
		return 0, 0, 0
	}
	st, ok := io[c.sysDevice]
	if !ok {
		for _, v := range io { // IOCounters keys by device name; take the single entry.
			st = v
			ok = true
			break
		}
	}
	if !ok {
		return 0, 0, 0
	}
	if !c.prevDiskTime.IsZero() {
		if dt := now.Sub(c.prevDiskTime).Seconds(); dt > 0 {
			if st.ReadBytes >= c.prevDiskRead {
				read = float64(st.ReadBytes-c.prevDiskRead) / dt
			}
			if st.WriteBytes >= c.prevDiskWrite {
				write = float64(st.WriteBytes-c.prevDiskWrite) / dt
			}
			if st.IoTime >= c.prevDiskIoTime {
				// IoTime is milliseconds spent doing I/O; over dt seconds → % active.
				busy = float64(st.IoTime-c.prevDiskIoTime) / (dt * 1000) * 100
				if busy > 100 {
					busy = 100
				}
			}
		}
	}
	c.prevDiskRead, c.prevDiskWrite, c.prevDiskIoTime, c.prevDiskTime = st.ReadBytes, st.WriteBytes, st.IoTime, now
	return round(read), round(write), round(busy)
}

func (c *Collector) sampleProcs(now time.Time, gpuSnap gpu.Snapshot, netRates map[int32]netmon.Rate) []Process {
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
		proc := Process{
			PID:        p.Pid,
			Name:       name,
			User:       username,
			CPUPercent: round(clampPct(pct)),
			MemRSS:     rss,
			MemPercent: round(float64(memPct)),
			Status:     status,
		}
		if g, ok := gpuSnap.Proc[p.Pid]; ok {
			proc.GPUPercent, proc.GPUEngine, proc.GPUMem = g.Util, g.Engine, g.Mem
		}
		if r, ok := netRates[p.Pid]; ok {
			proc.NetRxRate, proc.NetTxRate = round(r.Rx), round(r.Tx)
		}
		out = append(out, proc)
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

// Power returns the per-component power time-series, its 1/5/15-min averages, and which
// sources are reporting (CPU via RAPL co-process, GPU via nvidia-smi).
func (c *Collector) Power() PowerResponse {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]PowerSample, len(c.powerRing))
	copy(out, c.powerRing)
	return PowerResponse{
		Samples:      out,
		Avg:          c.powerAvg,
		CPUAvailable: c.power.Available(),
		GPUAvailable: c.gpu.Available(),
	}
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
