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
	"hostek/internal/gpu"
	"hostek/internal/hardware"
	"hostek/internal/metrics"
	"hostek/internal/sysconfig"
)

const base = "/api/services/hostek/"

// Fine-grained rights hostek declares to the holistic rights standard (see
// permissions.d/hostek.json, written by `hostek setup`). Each is backed by the
// matching Linux group; admins implicitly hold all of them.
const (
	permPower     = "hp_hostek_power"     // change OS power/headless + SSH-session config (dangerous)
	permProc      = "hp_hostek_proc"      // see the per-process breakdown
	permIdentity  = "hp_hostek_hwdetail"  // sensitive identity fields (serial, MAC)
	permTechInfo  = "hp_hostek_techinfo"  // technical fields (power-on hours, firmware, driver)
	permThermal   = "hp_hostek_thermal"   // temperature info + the Thermal tab
	permPowerInfo = "hp_hostek_powerinfo" // power telemetry + the Power tab
	permDisks     = "hp_hostek_disks"     // the Disks tab (all disks)
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
	mux.HandleFunc("GET "+base+"power", s.guard(permPowerInfo, false, s.power))
	mux.HandleFunc("GET "+base+"thermal", s.guard(permThermal, false, s.thermal))
	mux.HandleFunc("GET "+base+"host", s.guard("", false, s.host))
	mux.HandleFunc("GET "+base+"hardware", s.guard("", false, s.hardware))
	mux.HandleFunc("GET "+base+"disks", s.guard(permDisks, false, s.disks))
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

func (s *Server) summary(w http.ResponseWriter, _ *http.Request, u *auth.User) {
	sum := s.c.Summary()
	// Temperature and power are gated; redact the per-GPU values without the rights.
	// Copy the GPU slice first so we never mutate the collector's cached snapshot.
	if !u.Can(permThermal) || !u.Can(permPowerInfo) {
		sum.GPUs = append([]gpu.GPU(nil), sum.GPUs...)
		for i := range sum.GPUs {
			if !u.Can(permThermal) {
				sum.GPUs[i].TempC = 0
			}
			if !u.Can(permPowerInfo) {
				sum.GPUs[i].PowerW = 0
			}
		}
	}
	writeJSON(w, http.StatusOK, sum)
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

// hardware serves the System tab's component inventory. Available to everyone, but each
// class of field is gated: temperatures (thermal), GPU power (powerinfo), technical fields
// — firmware/driver/power-on hours — (techinfo), and sensitive identity — serial/MAC —
// (hwdetail). Slices are copied before redacting so the cache stays intact.
func (s *Server) hardware(w http.ResponseWriter, _ *http.Request, u *auth.User) {
	info := s.hw.Get()
	canIdentity := u.Can(permIdentity)
	canTech := u.Can(permTechInfo)
	canTherm := u.Can(permThermal)
	canPwr := u.Can(permPowerInfo)

	if !canTherm {
		info.CPU.TempC = 0
		info.Disk.TempC = 0
	}
	if !canIdentity {
		info.Disk.Serial = ""
	}
	if !canTech {
		info.Disk.Firmware, info.Disk.PowerOnHours = "", 0
	}
	if (!canTherm || !canPwr || !canTech) && len(info.GPUs) > 0 {
		info.GPUs = append([]hardware.GPUInfo(nil), info.GPUs...)
		for i := range info.GPUs {
			if !canTherm {
				info.GPUs[i].TempC = 0
			}
			if !canPwr {
				info.GPUs[i].PowerW, info.GPUs[i].PowerLimitW = 0, 0
			}
			if !canTech {
				info.GPUs[i].Driver = ""
			}
		}
	}
	if (!canIdentity || !canTech) && len(info.NICs) > 0 {
		info.NICs = append([]hardware.NICInfo(nil), info.NICs...)
		for i := range info.NICs {
			if !canIdentity {
				info.NICs[i].MAC = ""
			}
			if !canTech {
				info.NICs[i].Driver = ""
			}
		}
	}
	writeJSON(w, http.StatusOK, info)
}

// disks serves the Disks tab (gated by the disks right). Serial needs hwdetail (identity),
// firmware/power-on hours need techinfo, temperatures need thermal.
func (s *Server) disks(w http.ResponseWriter, _ *http.Request, u *auth.User) {
	ds := s.hw.Disks() // freshly built each call, safe to mutate in place
	canIdentity := u.Can(permIdentity)
	canTech := u.Can(permTechInfo)
	canTherm := u.Can(permThermal)
	for i := range ds {
		if !canIdentity {
			ds[i].Serial = ""
		}
		if !canTech {
			ds[i].Firmware, ds[i].PowerOnHours = "", 0
		}
		if !canTherm {
			ds[i].TempC = 0
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

// setPower applies the server-autonomy toggles. Each field is optional (pointer):
// the UI sends only the one switch it flipped, so we apply just what's present and
// leave the other setting untouched.
func (s *Server) setPower(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	var body struct {
		Headless    *bool `json:"headless"`
		TmuxPersist *bool `json:"tmuxPersist"`
		TmuxResume  *bool `json:"tmuxResume"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.Headless == nil && body.TmuxPersist == nil && body.TmuxResume == nil {
		writeErr(w, http.StatusBadRequest, "No setting to change")
		return
	}
	if body.Headless != nil {
		if err := sysconfig.Apply(*body.Headless); err != nil {
			log.Printf("hostek: apply power config (headless=%v) failed: %v", *body.Headless, err)
			writeErr(w, http.StatusInternalServerError, "Failed to apply power configuration")
			return
		}
	}
	if body.TmuxPersist != nil {
		if err := sysconfig.ApplyTmux(*body.TmuxPersist); err != nil {
			log.Printf("hostek: apply tmux SSH persistence (persist=%v) failed: %v", *body.TmuxPersist, err)
			writeErr(w, http.StatusInternalServerError, "Failed to apply SSH session configuration")
			return
		}
	}
	if body.TmuxResume != nil {
		if err := sysconfig.ApplyTmuxResume(*body.TmuxResume); err != nil {
			log.Printf("hostek: apply tmux orphan-session resume (resume=%v) failed: %v", *body.TmuxResume, err)
			writeErr(w, http.StatusInternalServerError, "Failed to apply SSH session configuration")
			return
		}
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
