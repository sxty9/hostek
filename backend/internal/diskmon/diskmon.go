// Package diskmon collects per-process disk throughput, which Linux only exposes to
// privileged callers (/proc/<pid>/io is 0400, readable by the process owner or root). It runs
// the privileged wrapper /usr/local/sbin/hostek-diskmon (which streams every process's
// cumulative read_bytes/write_bytes from /proc) as a long-lived `sudo -n` co-process and
// derives per-PID rates from consecutive refreshes. If the wrapper or sudo grant is missing
// the sampler stays empty and Available() reports false, so the process list's Disk column
// simply shows "—". Mirrors package netmon.
package diskmon

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

const wrapper = "/usr/local/sbin/hostek-diskmon"

// Rate is one process's disk throughput in bytes/sec.
type Rate struct {
	Read  float64 `json:"read"`
	Write float64 `json:"write"`
}

// counters holds one process's cumulative block-I/O bytes at a point in time.
type counters struct {
	read, write uint64
}

// Sampler holds the latest per-PID rates derived from the wrapper stream.
type Sampler struct {
	mu    sync.RWMutex
	rates map[int32]Rate
	avail bool

	// Owned exclusively by the consume goroutine (single co-process loop) — no lock needed.
	prev  map[int32]counters
	prevT time.Time
}

// New returns an idle sampler. Call Start to launch the co-process.
func New() *Sampler { return &Sampler{rates: map[int32]Rate{}, prev: map[int32]counters{}} }

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
		s.prev, s.prevT = map[int32]counters{}, time.Time{} // reset deltas across restarts
		time.Sleep(5 * time.Second)                         // restart if the wrapper exits/dies
	}
}

// consume parses the wrapper stream. Each refresh block starts with a "Refreshing:" marker,
// then one "pid\tread_bytes\twrite_bytes" line per process (cumulative counters). Rates are the
// delta between the current and previous block over the elapsed time.
func (s *Sampler) consume(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	cur := map[int32]counters{}
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "Refreshing:") {
			s.flush(cur)
			cur = map[int32]counters{}
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			continue
		}
		pid, err := strconv.ParseInt(strings.TrimSpace(f[0]), 10, 32)
		if err != nil || pid <= 0 {
			continue
		}
		rd, _ := strconv.ParseUint(strings.TrimSpace(f[1]), 10, 64)
		wr, _ := strconv.ParseUint(strings.TrimSpace(f[2]), 10, 64)
		cur[int32(pid)] = counters{read: rd, write: wr}
	}
	s.flush(cur)
}

// flush turns one completed block of cumulative counters into rates against the previous block.
func (s *Sampler) flush(cur map[int32]counters) {
	if len(cur) == 0 {
		return
	}
	now := time.Now()
	next := map[int32]Rate{}
	if !s.prevT.IsZero() {
		if dt := now.Sub(s.prevT).Seconds(); dt > 0 {
			for pid, c := range cur {
				p, ok := s.prev[pid]
				if !ok {
					continue
				}
				var rd, wr float64
				if c.read >= p.read {
					rd = float64(c.read-p.read) / dt
				}
				if c.write >= p.write {
					wr = float64(c.write-p.write) / dt
				}
				if rd > 0 || wr > 0 {
					next[pid] = Rate{Read: rd, Write: wr}
				}
			}
		}
	}
	s.mu.Lock()
	s.rates = next
	s.mu.Unlock()
	s.prev, s.prevT = cur, now
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
