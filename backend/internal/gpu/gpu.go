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

// ProcGPU is one process's GPU usage, joined into the process list by PID.
type ProcGPU struct {
	Util   float64 // SM utilization %
	Mem    uint64  // bytes (framebuffer)
	Engine string  // dominant engine label, e.g. "GPU0·SM"
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

// queryProcs joins nvidia-smi pmon (SM/engine utilization) with the compute-apps
// query (per-process framebuffer bytes). Both are best-effort; either may be empty.
func queryProcs() map[int32]ProcGPU {
	res := map[int32]ProcGPU{}

	if out, ok := run(3*time.Second, "nvidia-smi", "pmon", "-c", "1", "-s", "u"); ok {
		sc := bufio.NewScanner(strings.NewReader(out))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			f := strings.Fields(line)
			// "-s u" layout: gpu pid type sm enc dec command. f[3] (sm) is the GPU%; the
			// engine column layout varies by nvidia-smi version, so we label by GPU index.
			if len(f) < 4 {
				continue
			}
			pid, err := strconv.ParseInt(f[1], 10, 32)
			if err != nil {
				continue
			}
			res[int32(pid)] = ProcGPU{Util: round1(dash(f[3])), Engine: "GPU" + f[0]}
		}
	}

	if out, ok := run(3*time.Second, "nvidia-smi",
		"--query-compute-apps=pid,used_memory", "--format=csv,noheader,nounits"); ok {
		sc := bufio.NewScanner(strings.NewReader(out))
		for sc.Scan() {
			f := splitCSV(sc.Text())
			if len(f) < 2 {
				continue
			}
			pid, err := strconv.ParseInt(f[0], 10, 32)
			if err != nil {
				continue
			}
			pg := res[int32(pid)]
			pg.Mem = mib(f[1])
			if pg.Engine == "" {
				pg.Engine = "GPU·Compute"
			}
			res[int32(pid)] = pg
		}
	}
	return res
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
