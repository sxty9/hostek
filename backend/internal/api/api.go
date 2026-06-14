// Package api serves hostek's HTTP surface under /api/services/hostek/, behind the
// shared holistic session. Aggregate metrics are available to every authenticated user;
// the per-process breakdown, hardware identifiers and power configuration are gated by
// the holistic rights standard (admin, or a granted hp_hostek_* group). Error bodies
// match holistic's contract: {"detail": "..."}.
package api

import (
	"encoding/json"
	"log"
	"net/http"

	"hostek/internal/auth"
	"hostek/internal/hardware"
	"hostek/internal/metrics"
	"hostek/internal/sysconfig"
)

const base = "/api/services/hostek/"

// Fine-grained rights hostek declares to the holistic rights standard (see
// permissions.d/hostek.json, written by `hostek setup`). Each is backed by the
// matching Linux group; admins implicitly hold all of them.
const (
	permPower    = "hp_hostek_power"    // read/change OS power + headless config
	permProc     = "hp_hostek_proc"     // see the per-process breakdown
	permHWDetail = "hp_hostek_hwdetail" // see identifying hardware fields (serial, MAC)
)

// Server wires the verifier and collectors into HTTP handlers.
type Server struct {
	v  *auth.Verifier
	c  *metrics.Collector
	hw *hardware.Collector
}

// New builds a server.
func New(v *auth.Verifier, c *metrics.Collector, hw *hardware.Collector) *Server {
	return &Server{v: v, c: c, hw: hw}
}

type handler func(w http.ResponseWriter, r *http.Request, u *auth.User)

// Handler returns the routed http.Handler (Go 1.22 method+path patterns).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+base+"summary", s.guard("", false, s.summary))
	mux.HandleFunc("GET "+base+"metrics", s.guard("", false, s.series))
	mux.HandleFunc("GET "+base+"power", s.guard("", false, s.power))
	mux.HandleFunc("GET "+base+"thermal", s.guard("", false, s.thermal))
	mux.HandleFunc("GET "+base+"host", s.guard("", false, s.host))
	mux.HandleFunc("GET "+base+"hardware", s.guard("", false, s.hardware))
	mux.HandleFunc("GET "+base+"disks", s.guard("", false, s.disks))
	mux.HandleFunc("GET "+base+"processes", s.guard(permProc, false, s.processes))
	mux.HandleFunc("GET "+base+"config/power", s.guard(permPower, false, s.getPower))
	mux.HandleFunc("POST "+base+"config/power", s.guard(permPower, true, s.setPower))
	mux.HandleFunc("GET "+base+"health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	return mux
}

// guard authenticates, optionally requires a fine-grained right (perm != "" ⇒
// admin or membership in the backing group), and optionally enforces CSRF.
func (s *Server) guard(perm string, csrf bool, h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := s.v.User(r)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		if perm != "" && !u.Can(perm) {
			writeErr(w, http.StatusForbidden, "You do not have permission for this action")
			return
		}
		if csrf && !s.v.CheckCSRF(r) {
			writeErr(w, http.StatusForbidden, "CSRF check failed")
			return
		}
		h(w, r, u)
	}
}

func (s *Server) summary(w http.ResponseWriter, _ *http.Request, _ *auth.User) {
	writeJSON(w, http.StatusOK, s.c.Summary())
}

func (s *Server) series(w http.ResponseWriter, _ *http.Request, _ *auth.User) {
	writeJSON(w, http.StatusOK, map[string]any{"samples": s.c.Series()})
}

func (s *Server) host(w http.ResponseWriter, _ *http.Request, _ *auth.User) {
	writeJSON(w, http.StatusOK, s.c.Host())
}

// power serves the Power tab's per-component power series + 1/5/15-min averages.
func (s *Server) power(w http.ResponseWriter, _ *http.Request, _ *auth.User) {
	writeJSON(w, http.StatusOK, s.c.Power())
}

// thermal serves the Thermal tab's per-component temperature series + critical limits.
func (s *Server) thermal(w http.ResponseWriter, _ *http.Request, _ *auth.User) {
	writeJSON(w, http.StatusOK, s.hw.Thermal())
}

// hardware serves the System tab's component inventory. Available to everyone, but
// identifying fields (disk serial, NIC MAC) need the hardware-detail right.
func (s *Server) hardware(w http.ResponseWriter, _ *http.Request, u *auth.User) {
	info := s.hw.Get()
	if !u.Can(permHWDetail) {
		info.Disk.Serial = ""
		for i := range info.NICs {
			info.NICs[i].MAC = ""
		}
	}
	writeJSON(w, http.StatusOK, info)
}

// disks serves the Disks tab's full device list. Serial numbers need the hardware-detail right.
func (s *Server) disks(w http.ResponseWriter, _ *http.Request, u *auth.User) {
	ds := s.hw.Disks()
	if !u.Can(permHWDetail) {
		for i := range ds {
			ds[i].Serial = ""
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"disks": ds})
}

func (s *Server) processes(w http.ResponseWriter, _ *http.Request, _ *auth.User) {
	writeJSON(w, http.StatusOK, map[string]any{"processes": s.c.Processes()})
}

func (s *Server) getPower(w http.ResponseWriter, _ *http.Request, _ *auth.User) {
	writeJSON(w, http.StatusOK, sysconfig.Read())
}

func (s *Server) setPower(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	var body struct {
		Headless bool `json:"headless"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if err := sysconfig.Apply(body.Headless); err != nil {
		log.Printf("hostek: apply power config (headless=%v) failed: %v", body.Headless, err)
		writeErr(w, http.StatusInternalServerError, "Failed to apply power configuration")
		return
	}
	writeJSON(w, http.StatusOK, sysconfig.Read())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}
