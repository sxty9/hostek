package hardware

import (
	"testing"
	"time"
)

// diskUnreachable flags a SATA/ATA disk lsblk still lists but that has gone silent
// on SMART for >2 probe cycles (the stale-node case). The SCSI-"offline" branch
// needs real sysfs, so here we exercise the SMART-liveness logic with fake names
// (no /sys/block entry → the state read is "", falling through to the time check).
func TestDiskUnreachable(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	stale := now.Add(-3 * smartInterval) // older than the 2-cycle grace
	fresh := now.Add(-10 * time.Second)  // within grace
	cases := []struct {
		desc, tran string
		live       bool
		lastOK     time.Time
		want       bool
	}{
		{"nvme is never flagged here", "nvme", false, stale, false},
		{"answering SATA is fine", "sata", true, now, false},
		{"SATA that never did SMART", "sata", false, time.Time{}, false},
		{"silent but within grace", "sata", false, fresh, false},
		{"silent past grace → unreachable", "sata", false, stale, true},
		{"ata alias, silent past grace", "ata", false, stale, true},
	}
	for _, c := range cases {
		got := diskUnreachable("hostek-fake-disk", c.tran, c.live, c.lastOK, now)
		if got != c.want {
			t.Errorf("%s: diskUnreachable(tran=%q,live=%v) = %v, want %v", c.desc, c.tran, c.live, got, c.want)
		}
	}
}

// portLabel maps transport families to friendly hints; SATA disks additionally
// carry the resolved mainboard port. Here we exercise the transport branches and
// the fallback for a device whose sysfs topology can't be resolved (no such
// /sys/block entry in the test environment → bare "SATA").
func TestPortLabel(t *testing.T) {
	cases := []struct {
		tran, name, want string
	}{
		{"nvme", "nvme0n1", "NVMe"},
		{"NVMe", "nvme0n1", "NVMe"}, // case-insensitive
		{"usb", "sdz", "USB"},
		{"sata", "hostek-no-such-disk", "SATA"}, // unresolvable → transport family
		{"ata", "hostek-no-such-disk", "SATA"},
		{"", "sda", ""}, // unknown transport → empty
	}
	for _, c := range cases {
		if got := portLabel(c.tran, c.name); got != c.want {
			t.Errorf("portLabel(%q, %q) = %q, want %q", c.tran, c.name, got, c.want)
		}
	}
}

// pcieGenOf maps the GT/s figure in current_link_speed to the PCIe generation,
// tolerating both "8" and "8.0" spellings and unknown/empty input.
func TestPcieGenOf(t *testing.T) {
	cases := map[string]string{
		"2.5 GT/s PCIe":  "1.0",
		"5.0 GT/s PCIe":  "2.0",
		"5 GT/s":         "2.0",
		"8.0 GT/s PCIe":  "3.0",
		"16.0 GT/s PCIe": "4.0",
		"32.0 GT/s PCIe": "5.0",
		"64.0 GT/s PCIe": "6.0",
		"":               "",
		"Unknown":        "",
	}
	for in, want := range cases {
		if got := pcieGenOf(in); got != want {
			t.Errorf("pcieGenOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// nvmePort degrades to a bare "NVMe" when the device (and thus its PCIe topology)
// can't be resolved — the label must never come back empty for an NVMe disk.
func TestNvmePortFallback(t *testing.T) {
	if got := nvmePort("hostek-no-such-nvme"); got != "NVMe" {
		t.Errorf("nvmePort(unresolvable) = %q, want %q", got, "NVMe")
	}
}

// controllerName turns lspci's verbose vendor/device strings into a short label:
// the pci.ids bracketed alias wins for the vendor, and the trailing
// "…SATA Controller" noise is stripped off the model.
func TestControllerName(t *testing.T) {
	cases := []struct{ vendor, device, want string }{
		{"ASMedia Technology Inc.", "ASM1166 Serial ATA Controller", "ASMedia ASM1166"},
		{"Advanced Micro Devices, Inc. [AMD]", "X370 Series Chipset SATA Controller", "AMD X370 Series Chipset"},
		{"Intel Corporation", "Cannon Lake PCH SATA AHCI Controller", "Intel Cannon Lake PCH SATA"},
		{"Marvell Technology Group Ltd.", "88SE9215 PCIe 2.0 x1 4-port SATA 6 Gb/s Controller", "Marvell 88SE9215 PCIe 2.0 x1 4-port SATA 6 Gb/s"},
		{"ASMedia Technology Inc.", "ASMedia ASM1062 SATA Controller", "ASMedia ASM1062"}, // vendor not doubled
		{"ASMedia Technology Inc.", "", "ASMedia"},                                        // model unknown
		{"", "ASM1166 Serial ATA Controller", "ASM1166"},                                  // vendor unknown
	}
	for _, c := range cases {
		if got := controllerName(c.vendor, c.device); got != c.want {
			t.Errorf("controllerName(%q, %q) = %q, want %q", c.vendor, c.device, got, c.want)
		}
	}
}

// normalizePCI restores the domain lspci omits, and leaves explicit domains alone.
func TestNormalizePCI(t *testing.T) {
	for in, want := range map[string]string{
		"06:00.0":      "0000:06:00.0",
		"0000:06:00.0": "0000:06:00.0",
		"0001:06:00.0": "0001:06:00.0",
	} {
		if got := normalizePCI(in); got != want {
			t.Errorf("normalizePCI(%q) = %q, want %q", in, got, want)
		}
	}
}

// probeControllers must agree with this host's real PCI topology: every SATA disk
// resolves to a controller, and the add-on flag is set iff that controller's vendor
// differs from the host bridge's. Skips cleanly where lspci/sysfs aren't available.
func TestProbeControllersSmoke(t *testing.T) {
	ctrls := probeControllers()
	if len(ctrls) == 0 {
		t.Skip("lspci unavailable — nothing to check")
	}
	hostVendor := readSysStr("/sys/bus/pci/devices/0000:00:00.0/vendor")
	for _, name := range wholeDiskNames() {
		pci := devicePCIAddr(name)
		ct, ok := ctrls[pci]
		if pci == "" || !ok {
			continue // NVMe-less/virtio/USB topologies — not our business here
		}
		if ct.Name == "" {
			t.Errorf("disk %s: controller %s resolved to an empty name", name, pci)
		}
		vendor := readSysStr("/sys/bus/pci/devices/" + pci + "/vendor")
		if want := hostVendor != "" && vendor != "" && vendor != hostVendor; ct.AddOn != want {
			t.Errorf("disk %s on %s (vendor %s, host %s): AddOn = %v, want %v",
				name, pci, vendor, hostVendor, ct.AddOn, want)
		}
	}
}

// SmartPending is what puts the spinner on a Disks-tab card: the kernel lists the
// drive but the SMART probe hasn't reached it. A collector that has never probed is
// in exactly the state a freshly plugged disk is in, so it exercises that path
// without a real hotplug — every disk must be pending, and seeing one must queue the
// off-schedule probe that ends the spinner.
func TestDisksPendingBeforeFirstSmartProbe(t *testing.T) {
	c := New()
	disks := c.Disks()
	if len(disks) == 0 {
		t.Skip("lsblk unavailable — no disks to check")
	}
	for _, d := range disks {
		if !d.SmartPending {
			t.Errorf("disk %s: SmartPending = false before any SMART probe, want true", d.Name)
		}
	}
	select {
	case <-c.smartNudge:
	default:
		t.Error("Disks() saw never-probed disks but queued no SMART nudge")
	}
}

// The mirror image, and the reason `tried` exists at all: once the probe has *run* for
// a disk, nothing may still report pending — not even a device that yields no SMART
// (USB bridges, card readers). Those must render as "nothing to report", never as a
// card that spins forever.
func TestDisksNotPendingAfterSmartProbe(t *testing.T) {
	c := New()
	c.probeSmart()
	if len(c.smartTried) == 0 {
		t.Skip("lsblk unavailable — no disks to check")
	}
	for _, d := range c.Disks() {
		if d.SmartPending {
			t.Errorf("disk %s: still SmartPending after a completed SMART probe", d.Name)
		}
	}
}

// nudgeSmart runs on the request path, so it must never block — neither when the
// buffer is empty, nor when a nudge is already queued (it coalesces).
func TestNudgeSmartNeverBlocks(t *testing.T) {
	c := New()
	done := make(chan struct{})
	go func() {
		c.nudgeSmart()
		c.nudgeSmart() // buffer already full — must drop, not block
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("nudgeSmart blocked")
	}
	if len(c.smartNudge) != 1 {
		t.Errorf("smartNudge holds %d nudges, want 1 (coalesced)", len(c.smartNudge))
	}
}

// sataPort returns the real controller port for whichever SATA disks this host
// actually has (skips cleanly when none/unreadable). It's an integration-style
// smoke test: we only assert the resolver doesn't return garbage for real disks.
func TestSataPortSmoke(t *testing.T) {
	for _, name := range wholeDiskNames() {
		p := sataPort(name)
		if p == "" {
			continue // NVMe/USB/virtio or unresolvable — fine
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				t.Errorf("sataPort(%q) = %q, want digits only", name, p)
				break
			}
		}
	}
}
