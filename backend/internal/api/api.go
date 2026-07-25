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
	"time"

	"hostek/internal/auth"
	"hostek/internal/gpu"
	"hostek/internal/hardware"
	"hostek/internal/metrics"
	"hostek/internal/rights"
	"hostek/internal/shutdown"
	"hostek/internal/sysconfig"
)

const base = "/api/services/hostek/"

// Fine-grained rights hostek declares to the holistic rights standard live in the
// internal/rights package (backed 1:1 by permissions/hostek.json). Each is backed by
// the matching Linux group; admins implicitly hold all of them.

// Server wires the verifier and collectors into HTTP handlers.
type Server struct {
	v    *auth.Verifier
	c    *metrics.Collector
	hw   *hardware.Collector
	shut *shutdown.Manager
}

// New builds a server.
func New(v *auth.Verifier, c *metrics.Collector, hw *hardware.Collector, shut *shutdown.Manager) *Server {
	return &Server{v: v, c: c, hw: hw, shut: shut}
}

type handler func(w http.ResponseWriter, r *http.Request, u *auth.User)

// Handler returns the routed http.Handler (Go 1.22 method+path patterns).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+base+"summary", s.guard("", false, s.summary))
	mux.HandleFunc("GET "+base+"metrics", s.guard("", false, s.series))
	mux.HandleFunc("GET "+base+"power", s.guard(rights.GroupPowerInfo, false, s.power))
	mux.HandleFunc("GET "+base+"thermal", s.guard(rights.GroupThermal, false, s.thermal))
	mux.HandleFunc("GET "+base+"host", s.guard("", false, s.host))
	mux.HandleFunc("GET "+base+"hardware", s.guard("", false, s.hardware))
	mux.HandleFunc("GET "+base+"disks", s.guard(rights.GroupDisks, false, s.disks))
	mux.HandleFunc("POST "+base+"disks/eject", s.guard(rights.GroupEject, true, s.ejectDisk))
	mux.HandleFunc("POST "+base+"disks/mount", s.guard(rights.GroupMount, true, s.mountPartition))
	mux.HandleFunc("POST "+base+"disks/unmount", s.guard(rights.GroupMount, true, s.unmountPartition))
	mux.HandleFunc("GET "+base+"processes", s.guard(rights.GroupProc, false, s.processes))
	mux.HandleFunc("GET "+base+"config/power", s.guard(rights.GroupPower, false, s.getPower))
	mux.HandleFunc("POST "+base+"config/power", s.guard(rights.GroupPower, true, s.setPower))
	// Emergency shutdown („Not-Aus"). The two config routes are admin-gated (reusing the
	// power right); the two bare shutdown routes are deliberately UNAUTHENTICATED so the
	// holistic login page can reach them before there is a session — the configured
	// password in the body is the sole gate (see the shutdown package).
	mux.HandleFunc("GET "+base+"config/shutdown", s.guard(rights.GroupPower, false, s.getShutdownConfig))
	mux.HandleFunc("POST "+base+"config/shutdown", s.guard(rights.GroupPower, true, s.setShutdownConfig))
	mux.HandleFunc("GET "+base+"shutdown", s.shutdownStatus)
	mux.HandleFunc("POST "+base+"shutdown", s.shutdownTrigger)
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
	if !u.Can(rights.GroupThermal) || !u.Can(rights.GroupPowerInfo) {
		sum.GPUs = append([]gpu.GPU(nil), sum.GPUs...)
		for i := range sum.GPUs {
			if !u.Can(rights.GroupThermal) {
				sum.GPUs[i].TempC = 0
			}
			if !u.Can(rights.GroupPowerInfo) {
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
	canIdentity := u.Can(rights.GroupIdentity)
	canTech := u.Can(rights.GroupTechInfo)
	canTherm := u.Can(rights.GroupThermal)
	canPwr := u.Can(rights.GroupPowerInfo)

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
	canIdentity := u.Can(rights.GroupIdentity)
	canTech := u.Can(rights.GroupTechInfo)
	canTherm := u.Can(rights.GroupThermal)
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

// getShutdownConfig serves the admin Config panel's state: whether Not-Aus is armed and
// whether a password has been set. The password/hash is never returned.
func (s *Server) getShutdownConfig(w http.ResponseWriter, _ *http.Request, _ *auth.User) {
	armed, configured := s.shut.Config()
	writeJSON(w, http.StatusOK, map[string]bool{"armed": armed, "configured": configured})
}

// setShutdownConfig arms/disarms Not-Aus and/or sets its password. Both fields are
// optional; the password is applied before the arm flag so a set-and-arm in one request
// succeeds (arming refuses without a configured password).
func (s *Server) setShutdownConfig(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body struct {
		Armed    *bool   `json:"armed"`
		Password *string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.Armed == nil && body.Password == nil {
		writeErr(w, http.StatusBadRequest, "No setting to change")
		return
	}
	if body.Password != nil {
		switch err := s.shut.SetPassword(*body.Password, u.Username); {
		case err == nil:
			log.Printf("hostek: emergency shutdown password set by %s", u.Username)
		case errors.Is(err, shutdown.ErrPasswordTooShort), errors.Is(err, shutdown.ErrPasswordTooLong):
			writeErr(w, http.StatusBadRequest, "Password must be between 6 and 72 characters")
			return
		default:
			log.Printf("hostek: set emergency shutdown password failed: %v", err)
			writeErr(w, http.StatusInternalServerError, "Failed to save emergency shutdown password")
			return
		}
	}
	if body.Armed != nil {
		switch err := s.shut.SetArmed(*body.Armed, u.Username); {
		case err == nil:
			state := "disarmed"
			if *body.Armed {
				state = "armed"
			}
			log.Printf("hostek: emergency shutdown %s by %s", state, u.Username)
		case errors.Is(err, shutdown.ErrNotConfigured):
			writeErr(w, http.StatusBadRequest, "Set a password before enabling")
			return
		default:
			log.Printf("hostek: arm emergency shutdown failed: %v", err)
			writeErr(w, http.StatusInternalServerError, "Failed to update emergency shutdown")
			return
		}
	}
	armed, configured := s.shut.Config()
	writeJSON(w, http.StatusOK, map[string]bool{"armed": armed, "configured": configured})
}

// shutdownStatus is UNAUTHENTICATED (like health): the holistic login page probes it
// before there is a session, to decide whether to show the Not-Aus button. It reveals
// only whether the feature is armed — nothing sensitive.
func (s *Server) shutdownStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"armed": s.shut.Status()})
}

// shutdownTrigger is UNAUTHENTICATED and carries no CSRF token (holistic issues none
// before login): the configured emergency password in the body is the sole gate, and
// the shutdown package rate-limits + locks out to blunt brute force. On success the
// machine powers off shortly after this response is flushed, so the caller sees the
// confirmation first.
func (s *Server) shutdownTrigger(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Password == "" {
		writeErr(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	switch err := s.shut.Verify(body.Password); {
	case err == nil:
		// authorized — fall through
	case errors.Is(err, shutdown.ErrLockedOut):
		log.Printf("hostek: emergency shutdown locked out after repeated failures")
		writeErr(w, http.StatusTooManyRequests, "Too many attempts — try again later")
		return
	case errors.Is(err, shutdown.ErrNotArmed):
		writeErr(w, http.StatusForbidden, "Emergency shutdown is not enabled")
		return
	default:
		log.Printf("hostek: emergency shutdown attempt with wrong password")
		writeErr(w, http.StatusUnauthorized, "Wrong password")
		return
	}
	log.Printf("hostek: emergency shutdown authorized — powering off")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	// Power off only after the response has been flushed to the client.
	go func() {
		time.Sleep(time.Second)
		if err := s.shut.Trigger(); err != nil {
			log.Printf("hostek: emergency shutdown trigger failed: %v", err)
		}
	}()
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
