// Package api serves hostek's HTTP surface under /api/services/hostek/, behind the
// shared holistic session. Aggregate metrics are available to every authenticated user;
// the per-process breakdown, hardware identifiers and power configuration are gated by
// the holistic rights standard (admin, or a granted hp_hostek_* group). Error bodies
// match holistic's contract: {"detail": "..."}.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

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
	permMount     = "hp_hostek_mount"     // mount/unmount partitions from the Disks tab
	permEject     = "hp_hostek_eject"     // safely remove a whole disk — detaches it (dangerous)
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
	mux.HandleFunc("POST "+base+"disks/eject", s.guard(permEject, true, s.ejectDisk))
	mux.HandleFunc("POST "+base+"disks/mount", s.guard(permMount, true, s.mountPartition))
	mux.HandleFunc("POST "+base+"disks/unmount", s.guard(permMount, true, s.unmountPartition))
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
		info.Disk.Raw = nil // raw SMART/NVMe counters are the technician drill-down
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
			ds[i].Raw = nil // raw SMART/NVMe counters are the technician drill-down
		}
		if !canTherm {
			ds[i].TempC = 0
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"disks": ds})
}

// ejectDisk safely removes one whole disk: its filesystems are unmounted, buffers
// flushed, and the device detached from the kernel. The system disk is refused, in
// hardware.Eject and again in the wrapper.
func (s *Server) ejectDisk(w http.ResponseWriter, r *http.Request, u *auth.User) {
	name, ok := deviceBody(w, r)
	if !ok {
		return
	}
	err := s.hw.Eject(name)
	audit(u, "eject", name, err)
	writeDiskOp(w, err, map[string]any{"ok": true})
}

// mountPartition mounts one partition, at its /etc/fstab target when it has one and
// otherwise under /media/hostek/ (nosuid,nodev). Replies with the resulting mountpoint.
func (s *Server) mountPartition(w http.ResponseWriter, r *http.Request, u *auth.User) {
	name, ok := deviceBody(w, r)
	if !ok {
		return
	}
	mnt, err := s.hw.Mount(name)
	audit(u, "mount", name, err)
	writeDiskOp(w, err, map[string]any{"ok": true, "mountpoint": mnt})
}

// unmountPartition unmounts one partition. The wrapper refuses the mounts the running
// system depends on (/, /boot, /usr, …), so this cannot take the OS apart.
func (s *Server) unmountPartition(w http.ResponseWriter, r *http.Request, u *auth.User) {
	name, ok := deviceBody(w, r)
	if !ok {
		return
	}
	err := s.hw.Unmount(name)
	audit(u, "unmount", name, err)
	writeDiskOp(w, err, map[string]any{"ok": true})
}

// deviceBody decodes the {"name": "sdb1"} body shared by the three disk actions,
// answering 400 itself when it can't. The name is only ever a kernel device name;
// hardware.go re-validates it before it goes anywhere near the wrapper.
func deviceBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Name == "" {
		writeErr(w, http.StatusBadRequest, "Invalid request body")
		return "", false
	}
	return body.Name, true
}

// writeDiskOp maps a disk action's outcome onto the HTTP contract. A failure the
// wrapper diagnosed ("cannot unmount /srv — target is busy") is passed through
// verbatim: it is the only part of the answer the user can actually act on.
func writeDiskOp(w http.ResponseWriter, err error, success map[string]any) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, success)
	case errors.Is(err, hardware.ErrUnknownDevice):
		writeErr(w, http.StatusNotFound, "No such disk or partition")
	case errors.Is(err, hardware.ErrSystemDisk):
		writeErr(w, http.StatusBadRequest, "The system disk cannot be ejected")
	default:
		writeErr(w, http.StatusConflict, strings.TrimPrefix(err.Error(), hardware.ErrDiskOpFailed.Error()+": "))
	}
}

// audit records every attempted disk action with who asked. These are rare, deliberate
// and state-changing: a disk that unexpectedly went away should be traceable to a person.
func audit(u *auth.User, op, name string, err error) {
	if err != nil {
		log.Printf("hostek: %s %q by %s failed: %v", op, name, u.Username, err)
		return
	}
	log.Printf("hostek: %s %q by %s: ok", op, name, u.Username)
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
