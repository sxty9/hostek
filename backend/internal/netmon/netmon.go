// Package netmon collects per-process network throughput, which Linux does not expose
// to unprivileged callers. It runs the privileged wrapper /usr/local/sbin/hostek-netmon
// (which streams `nethogs -t` output) as a long-lived `sudo -n` co-process and parses its
// stdout. If the wrapper, sudo grant, or nethogs is unavailable the sampler stays empty and
// Available() reports false, so the process list's Network column simply shows "—".
package netmon

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const wrapper = "/usr/local/sbin/hostek-netmon"

// Rate is one process's throughput in bytes/sec.
type Rate struct {
	Rx float64 `json:"rx"`
	Tx float64 `json:"tx"`
}

// Sampler holds the latest per-PID rates parsed from the nethogs stream.
type Sampler struct {
	mu    sync.RWMutex
	rates map[int32]Rate
	avail bool
}

// New returns an idle sampler. Call Start to launch the co-process.
func New() *Sampler { return &Sampler{rates: map[int32]Rate{}} }

// Available reports whether the wrapper co-process is currently streaming.
func (s *Sampler) Available() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.avail
}

// Get returns a copy of the latest per-PID rates.
func (s *Sampler) Get() map[int32]Rate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[int32]Rate, len(s.rates))
	for k, v := range s.rates {
		out[k] = v
	}
	return out
}

// Start launches the privileged co-process (Linux only) and keeps it alive with backoff.
func (s *Sampler) Start() {
	if runtime.GOOS != "linux" {
		return
	}
	if _, err := os.Stat(wrapper); err != nil {
		return // wrapper not installed → stay empty
	}
	go s.loop()
}

func (s *Sampler) loop() {
	for {
		cmd := exec.Command("sudo", "-n", wrapper)
		stdout, err := cmd.StdoutPipe()
		if err == nil && cmd.Start() == nil {
			s.setAvail(true)
			s.consume(stdout)
			_ = cmd.Wait()
		}
		s.setAvail(false)
		s.clear()
		time.Sleep(5 * time.Second) // restart if the wrapper exits/dies
	}
}

// consume parses the nethogs trace stream. Each refresh block is delimited by a line
// containing "Refreshing:"; data lines are "program/PID/UID\tSENT_KB\tRECV_KB".
func (s *Sampler) consume(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	pending := map[int32]Rate{}
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "Refreshing:") {
			if len(pending) > 0 {
				s.swap(pending)
			}
			pending = map[int32]Rate{}
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			continue
		}
		segs := strings.Split(strings.TrimSpace(f[0]), "/")
		if len(segs) < 2 {
			continue
		}
		pid, err := strconv.ParseInt(segs[len(segs)-2], 10, 32)
		if err != nil || pid <= 0 {
			continue
		}
		tx, _ := strconv.ParseFloat(strings.TrimSpace(f[1]), 64) // SENT KB/s
		rx, _ := strconv.ParseFloat(strings.TrimSpace(f[2]), 64) // RECV KB/s
		pending[int32(pid)] = Rate{Rx: rx * 1024, Tx: tx * 1024}
	}
	if len(pending) > 0 {
		s.swap(pending)
	}
}

func (s *Sampler) swap(next map[int32]Rate) {
	s.mu.Lock()
	s.rates = next
	s.mu.Unlock()
}

func (s *Sampler) clear() {
	s.mu.Lock()
	s.rates = map[int32]Rate{}
	s.mu.Unlock()
}

func (s *Sampler) setAvail(v bool) {
	s.mu.Lock()
	s.avail = v
	s.mu.Unlock()
}
