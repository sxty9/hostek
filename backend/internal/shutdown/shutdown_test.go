package shutdown

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// useTempState points the package at a throwaway state file, removes the per-attempt
// delay, and drops the bcrypt cost to its minimum so tests run instantly. It restores
// the originals on cleanup.
func useTempState(t *testing.T) {
	t.Helper()
	origFile, origDelay, origCost := stateFile, attemptDelay, bcryptCost
	stateFile = filepath.Join(t.TempDir(), "shutdown.json")
	attemptDelay = 0
	bcryptCost = bcrypt.MinCost
	t.Cleanup(func() { stateFile, attemptDelay, bcryptCost = origFile, origDelay, origCost })
}

func TestSetPasswordAndVerify(t *testing.T) {
	useTempState(t)
	m := New()

	// Disarmed by default: Verify must refuse even with no password set.
	if err := m.Verify("anything"); !errors.Is(err, ErrNotArmed) {
		t.Fatalf("disarmed Verify = %v, want ErrNotArmed", err)
	}

	if err := m.SetPassword("stormy-night", "admin"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	// A password exists but arming is still off.
	if err := m.Verify("stormy-night"); !errors.Is(err, ErrNotArmed) {
		t.Fatalf("unarmed Verify = %v, want ErrNotArmed", err)
	}
	if err := m.SetArmed(true, "admin"); err != nil {
		t.Fatalf("SetArmed: %v", err)
	}

	if err := m.Verify("stormy-night"); err != nil {
		t.Fatalf("correct password Verify = %v, want nil", err)
	}
	if err := m.Verify("wrong"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password Verify = %v, want ErrWrongPassword", err)
	}
}

func TestArmRequiresPassword(t *testing.T) {
	useTempState(t)
	m := New()
	if err := m.SetArmed(true, "admin"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("arming without a password = %v, want ErrNotConfigured", err)
	}
	armed, configured := m.Config()
	if armed || configured {
		t.Fatalf("Config after failed arm = (%v,%v), want (false,false)", armed, configured)
	}
}

func TestPasswordLengthBounds(t *testing.T) {
	useTempState(t)
	m := New()
	if err := m.SetPassword("short", "admin"); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("5-char password = %v, want ErrPasswordTooShort", err)
	}
	long := make([]byte, maxPasswordLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := m.SetPassword(string(long), "admin"); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("73-char password = %v, want ErrPasswordTooLong", err)
	}
}

func TestStatusAndConfigReflectState(t *testing.T) {
	useTempState(t)
	m := New()

	if m.Status() {
		t.Fatal("Status on fresh state = true, want false")
	}
	if err := m.SetPassword("stormy-night", "admin"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	// Configured but not armed → login page must not show the button yet.
	if m.Status() {
		t.Fatal("Status after set-but-not-armed = true, want false")
	}
	if armed, configured := m.Config(); armed || !configured {
		t.Fatalf("Config = (%v,%v), want (false,true)", armed, configured)
	}

	if err := m.SetArmed(true, "admin"); err != nil {
		t.Fatalf("SetArmed: %v", err)
	}
	if !m.Status() {
		t.Fatal("Status after arm = false, want true")
	}

	// Disarming keeps the password but hides the button.
	if err := m.SetArmed(false, "admin"); err != nil {
		t.Fatalf("SetArmed(false): %v", err)
	}
	if m.Status() {
		t.Fatal("Status after disarm = true, want false")
	}
	if armed, configured := m.Config(); armed || !configured {
		t.Fatalf("Config after disarm = (%v,%v), want (false,true)", armed, configured)
	}
}

func TestLockoutAfterRepeatedFailures(t *testing.T) {
	useTempState(t)
	m := New()
	if err := m.SetPassword("stormy-night", "admin"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := m.SetArmed(true, "admin"); err != nil {
		t.Fatalf("SetArmed: %v", err)
	}

	for i := 0; i < maxFailures; i++ {
		if err := m.Verify("nope"); !errors.Is(err, ErrWrongPassword) {
			t.Fatalf("attempt %d = %v, want ErrWrongPassword", i, err)
		}
	}
	// The next attempt — even the correct password — is locked out.
	if err := m.Verify("stormy-night"); !errors.Is(err, ErrLockedOut) {
		t.Fatalf("post-threshold Verify = %v, want ErrLockedOut", err)
	}

	// A correct attempt after the cooldown succeeds again.
	m.mu.Lock()
	m.lockedUntil = time.Now().Add(-time.Second)
	m.mu.Unlock()
	if err := m.Verify("stormy-night"); err != nil {
		t.Fatalf("Verify after cooldown = %v, want nil", err)
	}
}

// TestPersistsAcrossManagers confirms the config is read live from disk, so a fresh
// Manager (e.g. after a daemon restart) still sees an armed configuration.
func TestPersistsAcrossManagers(t *testing.T) {
	useTempState(t)
	first := New()
	if err := first.SetPassword("stormy-night", "admin"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := first.SetArmed(true, "admin"); err != nil {
		t.Fatalf("SetArmed: %v", err)
	}

	second := New()
	if !second.Status() {
		t.Fatal("second Manager Status = false, want true (state should persist on disk)")
	}
	if err := second.Verify("stormy-night"); err != nil {
		t.Fatalf("second Manager Verify = %v, want nil", err)
	}
}

// TestConcurrentMutationsPreserveLatest stresses the atomic read-modify-write. One
// goroutine hammers arm/disarm — each a load-modify-save that carries the password hash
// forward from whatever it read — while another changes the password exactly once and
// then stops. Nothing rewrites the new password afterward, so a single stale re-arm that
// loaded the record before that write and saved after it latches the reversion: the
// stored record verifies the OLD password forever. Without the state lock this is a
// frequent race; with it, every writer loads the current record first, so once the new
// password is set every subsequent re-arm carries it forward and Verify holds.
func TestConcurrentMutationsPreserveLatest(t *testing.T) {
	useTempState(t)
	m := New()
	if err := m.SetPassword("secret-old", "admin"); err != nil {
		t.Fatalf("seed SetPassword: %v", err)
	}
	if err := m.SetArmed(true, "admin"); err != nil {
		t.Fatalf("seed SetArmed: %v", err)
	}

	const armToggles = 4000
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < armToggles; i++ {
			_ = m.SetArmed(i%2 == 0, "admin") // toggle; each call rewrites the whole record
		}
	}()
	go func() {
		defer wg.Done()
		if err := m.SetPassword("secret-new", "admin"); err != nil { // exactly once
			t.Errorf("SetPassword: %v", err)
		}
	}()
	wg.Wait()

	// Re-arm so Verify has something to check, then confirm the new password survived.
	if err := m.SetArmed(true, "admin"); err != nil {
		t.Fatalf("final SetArmed: %v", err)
	}
	if err := m.Verify("secret-new"); err != nil {
		t.Fatalf("Verify(secret-new) after concurrent re-arm = %v; a stale re-arm reverted the password", err)
	}
}
