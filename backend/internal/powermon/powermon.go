// Package powermon reads CPU package power (watts) from RAPL, whose energy counter
// (/sys/class/powercap/intel-rapl:*/energy_uj) is root-only since the Platypus
// mitigation. It runs the privileged wrapper /usr/local/sbin/hostek-powermon — which
// streams a watts value every couple of seconds — as a long-lived `sudo -n` co-process
// and keeps the latest reading. Without the wrapper/RAPL it stays unavailable and the
// CPU power series simply reads zero.
package powermon

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

const wrapper = "/usr/local/sbin/hostek-powermon"

// Sampler holds the latest CPU package power in watts.
type Sampler struct {
	mu    sync.RWMutex
	watts float64
	avail bool
}

// New returns an idle sampler. Call Start to launch the co-process.
func New() *Sampler { return &Sampler{} }

// Available reports whether the wrapper co-process is currently streaming.
func (s *Sampler) Available() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.avail
}

// Watts returns the latest CPU package power (0 when unavailable).
func (s *Sampler) Watts() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.watts
}

// Start launches the privileged co-process (Linux only) and keeps it alive with backoff.
func (s *Sampler) Start() {
	if runtime.GOOS != "linux" {
		return
	}
	if _, err := os.Stat(wrapper); err != nil {
		return // wrapper not installed → stay unavailable
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
		s.set(0)
		time.Sleep(5 * time.Second) // restart if the wrapper exits
	}
}

func (s *Sampler) consume(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		v, err := strconv.ParseFloat(strings.TrimSpace(sc.Text()), 64)
		if err != nil || v < 0 {
			continue
		}
		s.set(v)
	}
}

func (s *Sampler) set(v float64) {
	s.mu.Lock()
	s.watts = v
	s.mu.Unlock()
}

func (s *Sampler) setAvail(v bool) {
	s.mu.Lock()
	s.avail = v
	if !v {
		s.watts = 0
	}
	s.mu.Unlock()
}
