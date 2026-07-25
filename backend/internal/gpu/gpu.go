// Package gpu samples NVIDIA GPU utilization (overall + per process) by shelling out
// to nvidia-smi on its own ticker, decoupled from the gopsutil sampler since nvidia-smi
// can take 100-300ms. If nvidia-smi is absent the sampler stays empty and Available()
// reports false, so the UI hides the GPU sections entirely.
package gpu

import (
	"bufio"
	"context"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GPU is one device's live overall state.
type GPU struct {
	Index       int     `json:"index"`
	Name        string  `json:"name"`
	UtilPercent float64 `json:"utilPercent"`
	MemUsed     uint64  `json:"memUsed"`  // bytes
	MemTotal    uint64  `json:"memTotal"` // bytes
	MemPercent  float64 `json:"memPercent"`
	TempC       float64 `json:"tempC"`
	PowerW      float64 `json:"powerW"`
}

// ProcGPUDev is one process's usage on a single physical GPU.
type ProcGPUDev struct {
	Index int     `json:"index"`
	Util  float64 `json:"util,omitempty"` // SM utilization % (compute only; graphics reports none)
	Mem   uint64  `json:"mem,omitempty"`  // framebuffer bytes on this GPU
}

// ProcGPU is one process's GPU usage, aggregated across every GPU it touches (joined into the
// process list by PID). PerGPU keeps the per-device split for the per-GPU breakdown UI.
type ProcGPU struct {
	Util   float64      // summed SM % across GPUs
	Mem    uint64       // summed framebuffer bytes across GPUs
	Engine string       // which GPUs, e.g. "GPU0·GPU1"
	PerGPU []ProcGPUDev // per-device detail
}

// Snapshot is the latest sampled GPU state.
type Snapshot struct {
	GPUs []GPU
	Proc map[int32]ProcGPU
}

// Sampler owns the nvidia-smi polling loop and the latest snapshot.
type Sampler struct {
	interval time.Duration
	mu       sync.RWMutex
	snap     Snapshot
	avail    bool
}

// New returns a sampler polling at the given interval.
func New(interval time.Duration) *Sampler { return &Sampler{interval: interval} }

// Available reports whether nvidia-smi was found when Start ran.
func (s *Sampler) Available() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.avail
}

// Get returns the latest snapshot (Proc may be nil).
func (s *Sampler) Get() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// Start primes one sample, then samples on a ticker — but only if nvidia-smi exists.
func (s *Sampler) Start() {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return
	}
	s.mu.Lock()
	s.avail = true
	s.mu.Unlock()
	s.sample()
	go func() {
		t := time.NewTicker(s.interval)
		defer t.Stop()
		for range t.C {
			s.sample()
		}
	}()
}

func (s *Sampler) sample() {
	snap := Snapshot{GPUs: queryGPUs(), Proc: queryProcs()}
	s.mu.Lock()
	s.snap = snap
	s.mu.Unlock()
}

func run(timeout time.Duration, name string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func queryGPUs() []GPU {
	out, ok := run(3*time.Second, "nvidia-smi",
		"--query-gpu=index,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw",
		"--format=csv,noheader,nounits")
	if !ok {
		return nil
	}
	var gpus []GPU
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := splitCSV(sc.Text())
		if len(f) < 7 {
			continue
		}
		g := GPU{
			Index:       atoi(f[0]),
			Name:        f[1],
			UtilPercent: round1(atof(f[2])),
			MemUsed:     mib(f[3]),
			MemTotal:    mib(f[4]),
			TempC:       atof(f[5]),
			PowerW:      atof(f[6]),
		}
		if g.MemTotal > 0 {
			g.MemPercent = round1(float64(g.MemUsed) / float64(g.MemTotal) * 100)
		}
		gpus = append(gpus, g)
	}
	return gpus
}

// queryProcs reads nvidia-smi pmon for per-process, per-GPU SM utilization (sm) and
// framebuffer memory (fb). pmon lists BOTH graphics and compute processes on every GPU, so
// unlike --query-compute-apps (compute only) it also captures a desktop's graphics apps —
// which report no sm% but do hold VRAM. Column order varies by driver version, so we map the
// header's names → field positions instead of hardcoding indices. Best-effort: an absent or
// unsupported pmon simply yields no GPU rows.
func queryProcs() map[int32]ProcGPU {
	res := map[int32]ProcGPU{}
	out, ok := run(3*time.Second, "nvidia-smi", "pmon", "-c", "1", "-s", "um")
	if !ok {
		return res
	}
	var cols map[string]int // header column name → field index (nil until the header is parsed)
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			// pmon prints two '#' header lines: the first names the columns
			// ("# gpu pid type sm mem enc dec ... fb ... command"), the second the units.
			// Only the column-name row (it carries both "gpu" and "pid") defines positions.
			if cols == nil {
				m := map[string]int{}
				for i, name := range strings.Fields(strings.TrimPrefix(line, "#")) {
					m[name] = i
				}
				if _, hasGPU := m["gpu"]; hasGPU {
					if _, hasPID := m["pid"]; hasPID {
						cols = m
					}
				}
			}
			continue
		}
		if cols == nil {
			continue
		}
		f := strings.Fields(line)
		col := func(name string) (string, bool) {
			if i, ok := cols[name]; ok && i < len(f) {
				return f[i], true
			}
			return "", false
		}
		pidStr, ok := col("pid")
		if !ok {
			continue
		}
		pid, err := strconv.ParseInt(pidStr, 10, 32)
		if err != nil {
			continue
		}
		gpuStr, _ := col("gpu")
		var sm float64
		if v, ok := col("sm"); ok {
			sm = round1(dash(v))
		}
		var fbBytes uint64
		if v, ok := col("fb"); ok {
			if mb := dash(v); mb > 0 { // fb is reported in MiB
				fbBytes = uint64(mb) * 1024 * 1024
			}
		}
		pg := res[int32(pid)]
		pg.Util = round1(pg.Util + sm)
		pg.Mem += fbBytes
		pg.Engine = addEngine(pg.Engine, "GPU"+gpuStr)
		pg.PerGPU = append(pg.PerGPU, ProcGPUDev{Index: atoi(gpuStr), Util: sm, Mem: fbBytes})
		res[int32(pid)] = pg
	}
	return res
}

// addEngine appends label to a "·"-joined engine list, skipping duplicates so a process
// spanning GPU0 and GPU1 reads "GPU0·GPU1" (each GPU once).
func addEngine(existing, label string) string {
	if existing == "" {
		return label
	}
	for _, e := range strings.Split(existing, "·") {
		if e == label {
			return existing
		}
	}
	return existing + "·" + label
}

func splitCSV(line string) []string {
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// dash parses a float, treating nvidia-smi's "-"/"[N/A]" placeholders as 0.
func dash(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" || strings.HasPrefix(s, "[") {
		return 0
	}
	return atof(s)
}

// mib parses an "MiB count" string from nvidia-smi (nounits) into bytes.
func mib(s string) uint64 {
	v := atof(s)
	if v <= 0 {
		return 0
	}
	return uint64(v) * 1024 * 1024
}

func round1(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return math.Round(f*10) / 10
}
