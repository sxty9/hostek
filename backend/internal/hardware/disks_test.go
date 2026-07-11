package hardware

import "testing"

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
