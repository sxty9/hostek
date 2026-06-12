// Package sysconfig reads and applies OS-level "headless / always-on" power settings.
// Reads are best-effort and Linux-only; writes go through the privileged sudo wrapper
// /usr/local/sbin/hostek-power-set. The BIOS "Restore AC Power Loss" setting is
// firmware-level (not writable from the OS) and surfaced as read-only information.
package sysconfig

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const wrapper = "/usr/local/sbin/hostek-power-set"

// BiosNote is the informational firmware setting (already configured in UEFI).
type BiosNote struct {
	Setting string `json:"setting"`
	Value   string `json:"value"`
	Note    string `json:"note"`
}

// PowerState is the current headless/always-on OS configuration.
type PowerState struct {
	Platform        string   `json:"platform"`
	Supported       bool     `json:"supported"`
	Headless        bool     `json:"headless"`
	LidIgnore       bool     `json:"lidIgnore"`
	SuspendMasked   bool     `json:"suspendMasked"`
	BiosAutoPowerOn BiosNote `json:"biosAutoPowerOn"`
}

func biosNote() BiosNote {
	return BiosNote{
		Setting: "Restore AC Power Loss",
		Value:   "Power On",
		Note:    "Firmware-level (UEFI), already set to power on after AC loss. Not writable from the OS on this board.",
	}
}

// Read returns the current power configuration (best-effort; Linux only).
func Read() PowerState {
	st := PowerState{Platform: runtime.GOOS, BiosAutoPowerOn: biosNote()}
	if runtime.GOOS != "linux" {
		return st
	}
	st.Supported = true
	st.SuspendMasked = sleepMasked()
	st.LidIgnore = lidIgnore()
	st.Headless = st.SuspendMasked && st.LidIgnore
	return st
}

func sleepMasked() bool {
	out, _ := exec.Command("systemctl", "is-enabled", "sleep.target").Output()
	s := strings.TrimSpace(string(out))
	return s == "masked" || s == "masked-runtime"
}

var lidRe = regexp.MustCompile(`(?m)^\s*HandleLidSwitch\s*=\s*ignore\s*$`)

func lidIgnore() bool {
	files := []string{"/etc/systemd/logind.conf"}
	if drop, _ := filepath.Glob("/etc/systemd/logind.conf.d/*.conf"); len(drop) > 0 {
		files = append(files, drop...)
	}
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && lidRe.Match(b) {
			return true
		}
	}
	return false
}

// Apply turns headless/always-on OS settings on or off via the privileged wrapper.
func Apply(headless bool) error {
	if runtime.GOOS != "linux" {
		return errors.New("power configuration is only supported on Linux")
	}
	arg := "off"
	if headless {
		arg = "on"
	}
	cmd := exec.Command("sudo", "-n", wrapper, arg)
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
