// Package sysconfig reads and applies OS-level server-autonomy settings: the
// "headless / always-on" power config and tmux-backed SSH session persistence.
// Reads are best-effort and Linux-only; writes go through the privileged sudo
// wrappers /usr/local/sbin/hostek-power-set and /usr/local/sbin/hostek-tmux-set.
// The BIOS "Restore AC Power Loss" setting is firmware-level (not writable from
// the OS) and surfaced as read-only information.
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

const (
	wrapper     = "/usr/local/sbin/hostek-power-set"
	tmuxWrapper = "/usr/local/sbin/hostek-tmux-set"
	// profile.d snippet the tmux wrapper installs; its presence is the on/off state.
	tmuxProfile = "/etc/profile.d/hostek-tmux.sh"
	// marker the tmux wrapper drops for the resume sub-option; its presence is the on/off state.
	tmuxResume = "/etc/hostek/tmux-resume"
)

// BiosNote is the informational firmware setting (already configured in UEFI).
type BiosNote struct {
	Setting string `json:"setting"`
	Value   string `json:"value"`
	Note    string `json:"note"`
}

// PowerState is the current server-autonomy OS configuration: headless/always-on
// power plus tmux-backed SSH session persistence.
type PowerState struct {
	Platform        string   `json:"platform"`
	Supported       bool     `json:"supported"`
	Headless        bool     `json:"headless"`
	LidIgnore       bool     `json:"lidIgnore"`
	SuspendMasked   bool     `json:"suspendMasked"`
	TmuxPersist     bool     `json:"tmuxPersist"`
	TmuxResume      bool     `json:"tmuxResume"`
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
	st.TmuxPersist = tmuxPersist()
	st.TmuxResume = tmuxResumeOn()
	return st
}

// tmuxPersist reports whether interactive SSH logins are routed into a persistent
// tmux session. The profile.d snippet installed by the wrapper is the source of truth.
func tmuxPersist() bool {
	_, err := os.Stat(tmuxProfile)
	return err == nil
}

// tmuxResumeOn reports whether the resume sub-option is enabled — a new login reattaches
// to an orphaned (detached) session instead of always opening a fresh one. The marker the
// wrapper drops is the source of truth; it only matters while tmuxPersist is on.
func tmuxResumeOn() bool {
	_, err := os.Stat(tmuxResume)
	return err == nil
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
	return runWrapper(wrapper, headless)
}

// ApplyTmux turns tmux-backed SSH session persistence on or off via the privileged
// wrapper (installs/removes the /etc/profile.d snippet).
func ApplyTmux(persist bool) error {
	return runWrapper(tmuxWrapper, persist)
}

// ApplyTmuxResume turns the resume sub-option on or off via the privileged wrapper
// (drops/removes the /etc/hostek/tmux-resume marker the login snippet checks).
func ApplyTmuxResume(resume bool) error {
	arg := "resume-off"
	if resume {
		arg = "resume-on"
	}
	return runWrapperArg(tmuxWrapper, arg)
}

// runWrapper invokes a privileged on|off wrapper via `sudo -n`.
func runWrapper(path string, on bool) error {
	arg := "off"
	if on {
		arg = "on"
	}
	return runWrapperArg(path, arg)
}

// runWrapperArg invokes a privileged wrapper with the given subcommand via `sudo -n`,
// surfacing its stderr.
func runWrapperArg(path, arg string) error {
	if runtime.GOOS != "linux" {
		return errors.New("system configuration is only supported on Linux")
	}
	cmd := exec.Command("sudo", "-n", path, arg)
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
