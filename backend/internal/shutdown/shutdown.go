// Package shutdown backs the „Not-Aus" (emergency shutdown) feature: a big-red button
// on the holistic login page that lets someone who is NOT logged in power the server
// down cleanly when the admin is away (e.g. an approaching thunderstorm) instead of
// pulling the plug. The button is gated only by a shared password an admin arms in
// hostek's Config tab.
//
// State (armed flag + bcrypt password hash) lives in a small daemon-owned file under
// /var/lib/hostek and is read live on every request, so it is fail-safe: a missing or
// unreadable file means disarmed. The password is never returned; only whether the
// feature is armed/configured. The actual poweroff escalates through the root-owned
// wrapper /usr/local/sbin/hostek-shutdown via `sudo -n`, mirroring sysconfig's wrappers.
//
// Because the trigger endpoint is unauthenticated (there is no session before login)
// and carries no CSRF token (holistic issues none pre-login), the password is the sole
// gate. A global lockout plus a fixed per-attempt delay blunt brute force; the endpoint
// is loopback-proxied by Caddy, so per-client IP throttling would not be reliable.
package shutdown

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	wrapper = "/usr/local/sbin/hostek-shutdown"

	minPasswordLen = 6
	maxPasswordLen = 72 // bcrypt only hashes the first 72 bytes; reject longer to avoid silent truncation.

	maxFailures     = 5           // consecutive wrong passwords before a global cooldown.
	lockoutDuration = time.Minute // how long the endpoint stays locked after too many failures.
)

// Overridable so tests can point at a temp file and skip the delay; production keeps the
// real state path and the guessing-rate cap.
var (
	stateFile    = "/var/lib/hostek/shutdown.json"
	attemptDelay = time.Second // applied to every trigger attempt to slow automated guessing.
)

// Errors surfaced to the API layer, which maps them onto HTTP status codes.
var (
	ErrPasswordTooShort = errors.New("password too short")
	ErrPasswordTooLong  = errors.New("password too long")
	ErrNotConfigured    = errors.New("emergency shutdown password not configured")
	ErrNotArmed         = errors.New("emergency shutdown not armed")
	ErrLockedOut        = errors.New("too many attempts")
	ErrWrongPassword    = errors.New("wrong password")
)

// state is the on-disk shape at stateFile.
type state struct {
	Armed     bool   `json:"armed"`
	Hash      string `json:"hash"`
	UpdatedBy string `json:"updatedBy"`
	UpdatedAt string `json:"updatedAt"`
}

// Manager owns the in-memory brute-force guard; the armed config itself is read live
// from disk so external edits (or a restart) are picked up without cached staleness.
type Manager struct {
	mu          sync.Mutex
	failures    int
	lockedUntil time.Time
}

// New builds a Manager.
func New() *Manager { return &Manager{} }

// Status reports whether the feature is effectively armed — a password is set AND the
// admin toggled it on. Cheap; used by the unauthenticated login-page probe.
func (m *Manager) Status() bool {
	st, err := load()
	if err != nil {
		return false
	}
	return st.Armed && st.Hash != ""
}

// Config reports the arm toggle and whether a password has ever been set, for the admin
// UI. The hash itself is never exposed.
func (m *Manager) Config() (armed, configured bool) {
	st, err := load()
	if err != nil {
		return false, false
	}
	return st.Armed, st.Hash != ""
}

// SetPassword stores a new emergency password (bcrypt-hashed), preserving the arm flag.
func (m *Manager) SetPassword(pw, user string) error {
	switch {
	case len(pw) < minPasswordLen:
		return ErrPasswordTooShort
	case len(pw) > maxPasswordLen:
		return ErrPasswordTooLong
	}
	st, err := load()
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	st.Hash = string(hash)
	stamp(&st, user)
	return save(st)
}

// SetArmed arms or disarms the feature. Arming requires a password to exist first.
func (m *Manager) SetArmed(on bool, user string) error {
	st, err := load()
	if err != nil {
		return err
	}
	if on && st.Hash == "" {
		return ErrNotConfigured
	}
	st.Armed = on
	stamp(&st, user)
	return save(st)
}

// Verify checks a submitted password against the armed config, applying the lockout and
// per-attempt delay. It returns nil when the caller is authorized to power the box off;
// the API layer then responds before calling Trigger. It does NOT itself shut down.
func (m *Manager) Verify(pw string) error {
	m.mu.Lock()
	if !m.lockedUntil.IsZero() && time.Now().Before(m.lockedUntil) {
		m.mu.Unlock()
		return ErrLockedOut
	}
	m.mu.Unlock()

	// A fixed delay on every attempt (right or wrong) caps the guess rate.
	time.Sleep(attemptDelay)

	st, err := load()
	if err != nil || !st.Armed || st.Hash == "" {
		return ErrNotArmed
	}
	if bcrypt.CompareHashAndPassword([]byte(st.Hash), []byte(pw)) != nil {
		m.recordFailure()
		return ErrWrongPassword
	}
	m.recordSuccess()
	return nil
}

// Trigger runs the privileged poweroff wrapper via `sudo -n`, surfacing its stderr.
func (m *Manager) Trigger() error {
	if runtime.GOOS != "linux" {
		return errors.New("shutdown is only supported on Linux")
	}
	cmd := exec.Command("sudo", "-n", wrapper)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

func (m *Manager) recordFailure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures++
	if m.failures >= maxFailures {
		m.lockedUntil = time.Now().Add(lockoutDuration)
		m.failures = 0
	}
}

func (m *Manager) recordSuccess() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures = 0
	m.lockedUntil = time.Time{}
}

func stamp(st *state, user string) {
	st.UpdatedBy = user
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

// load reads the state file. A missing file is not an error — it means disarmed.
func load() (state, error) {
	b, err := os.ReadFile(stateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state{}, nil
		}
		return state{}, err
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		return state{}, err
	}
	return st, nil
}

// save writes the state file atomically at mode 0600 (only the hostek user reads it).
func save(st state) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(stateFile)
	tmp, err := os.CreateTemp(dir, ".shutdown-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, stateFile)
}
